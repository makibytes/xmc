package cmd

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// The AI shell has ONE reconnect clock: the countdown in the title bar. Every
// piece that talks to the broker follows it, so they all succeed or fail at
// the same moment instead of each retrying on its own schedule:
//
//   - the sidebar windows stop their periodic refresh while the broker is
//     unreachable and reload the instant the probe succeeds (aiTUIModel.connDown,
//     resumeSidebar);
//   - the session's reconnecting adapters — behind foreground commands, sidebar
//     actions and background processes — don't run a backoff of their own
//     but wait on connGate.pace, which the title-bar probe opens;
//   - a connection lost mid-session (a sidebar listing, a command or an
//     adapter failing with a connection error) starts that same countdown
//     (noteConnFailure, connLostMsg).

// connState holds connection probe and auto-reconnect state for the AI TUI.
type connState struct {
	checked           bool          // true after the initial probe completes
	err               error         // non-nil if the broker is unreachable
	reconnecting      bool          // true while backoff is ticking or a probe is in-flight
	reconnectAt       time.Time     // wall-clock time the next probe fires
	reconnectBackoff  time.Duration // current wait interval (doubles each failure, capped at 3 min)
	reconnectDisabled bool          // user ran /disconnect — stop auto-retry
	reconnectStatus   string        // one-line countdown shown below the viewport
	lastCheck         time.Time     // when a confirming probe was last started (see noteConnFailure)
}

const (
	reconnectInitialBackoff = 2 * time.Second
	reconnectMaxBackoff     = 3 * time.Minute

	// connRecheckEvery bounds how often a failure seen by a sidebar listing, a
	// command or an action starts a confirming probe, so a broker whose
	// management API alone is down doesn't make every refresh probe again.
	connRecheckEvery = 30 * time.Second
)

// errReconnectDisabled ends the wait of a command that was retrying through
// the gate when the user ran /disconnect.
var errReconnectDisabled = errors.New("auto-reconnect is disabled (/connect to retry)")

// connGate is the shared up/down state behind the reconnect clock. The TUI
// opens and closes it (SetDown, Up, Disable, Enable); the reconnecting
// adapters of the session wait on it (pace).
type connGate struct {
	mu       sync.Mutex
	down     bool
	disabled bool
	changed  chan struct{} // closed and replaced on every change
	onLost   func(error)   // tells the TUI an adapter hit a connection error; set before the program runs
}

func newConnGate() *connGate { return &connGate{changed: make(chan struct{})} }

// update applies fn to the state and wakes every waiter if it changed.
func (g *connGate) update(fn func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	downBefore, disabledBefore := g.down, g.disabled
	fn()
	if g.down != downBefore || g.disabled != disabledBefore {
		close(g.changed)
		g.changed = make(chan struct{})
	}
}

func (g *connGate) SetDown() { g.update(func() { g.down = true }) }
func (g *connGate) Up()      { g.update(func() { g.down = false }) }
func (g *connGate) Disable() { g.update(func() { g.disabled = true }) }
func (g *connGate) Enable()  { g.update(func() { g.disabled = false }) }

// pace is ReconnectOptions.Pace: called after a connection error, it reports
// the loss (once per outage) and returns when the TUI's reconnect loop has
// found the broker again — or with an error when ctx ends or auto-reconnect is
// disabled.
func (g *connGate) pace(ctx context.Context, cause error) error {
	g.mu.Lock()
	wasUp := !g.down
	g.mu.Unlock()
	if wasUp {
		g.SetDown()
		if g.onLost != nil {
			g.onLost(cause)
		}
	}
	for {
		g.mu.Lock()
		down, disabled, changed := g.down, g.disabled, g.changed
		g.mu.Unlock()
		switch {
		case disabled && down:
			return errReconnectDisabled
		case !down:
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

// statusLine says where the reconnect stands, for messages that explain why
// something could not run.
func (c connState) statusLine() string {
	switch {
	case c.reconnectDisabled:
		return "auto-reconnect is off (/connect to retry)"
	case c.reconnectStatus != "":
		return c.reconnectStatus + " (/connect to retry now)"
	default:
		return "reconnecting (/connect to retry now)"
	}
}

// connLostMsg reports that an adapter of the session hit a connection error.
type connLostMsg struct{ err error }

// reconnectTick schedules the next countdown/probe check of the reconnect loop.
func reconnectTick() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return reconnectTickMsg{} })
}

// connDown reports whether the broker is currently considered unreachable —
// the state the sidebar's periodic refresh and the session's adapters wait out.
func (m aiTUIModel) connDown() bool {
	return m.conn.reconnecting || m.conn.err != nil
}

// ---------- Connection probe ----------

// probe pings the broker and hands the result to mk, which wraps it as the
// caller's own tea.Msg type — connMsg for the initial and confirming probes,
// reconnectProbeMsg for the auto-reconnect loop, kept distinct so the two
// paths don't collide.
func (m aiTUIModel) probe(mk func(error) tea.Msg) tea.Cmd {
	ping := m.session.spec.Ping
	return guard(func(err error) tea.Msg { return mk(err) }, func() tea.Msg {
		conn, err := ping()
		if err != nil {
			return mk(err)
		}
		_ = conn.Close()
		return mk(nil)
	})
}

func (m aiTUIModel) startProbeConnection() tea.Cmd {
	return m.probe(func(err error) tea.Msg { return connMsg{err: err} })
}

// noteConnFailure is called with the error of a sidebar listing, a command or
// a sidebar action. A connection error while the broker is considered
// reachable starts one confirming probe (at most one per connRecheckEvery,
// and none when the broker has no probe): if it fails, the same reconnect
// countdown as for a broker that was down from the start begins.
func (m *aiTUIModel) noteConnFailure(err error) tea.Cmd {
	if err == nil || !isConnectionError(err) || m.connDown() ||
		m.session == nil || m.session.spec.Ping == nil || time.Since(m.conn.lastCheck) < connRecheckEvery {
		return nil
	}
	m.conn.lastCheck = time.Now()
	return m.startProbeConnection()
}

// beginReconnect marks the broker unreachable and starts the countdown; a
// no-op while one is already running.
func (m *aiTUIModel) beginReconnect(err error) tea.Cmd {
	m.conn.checked = true
	m.conn.err = err
	if m.session != nil && m.session.gate != nil {
		m.session.gate.SetDown()
	}
	if m.conn.reconnecting {
		return nil
	}
	m.appendTranscript(warnStyle.Render("connection error: "+err.Error()) + "\n\n")
	if m.conn.reconnectDisabled {
		return nil
	}
	m.conn.reconnecting = true
	m.conn.reconnectBackoff = reconnectInitialBackoff
	m.conn.reconnectAt = time.Now().Add(m.conn.reconnectBackoff)
	return reconnectTick()
}

func (m aiTUIModel) handleConnDone(msg connMsg) (tea.Model, tea.Cmd) {
	if msg.err == nil {
		if !m.conn.reconnecting { // a loop already running decides when we're back
			m.conn.checked = true
			m.conn.err = nil
		}
		return m, nil
	}
	cmd := m.beginReconnect(msg.err)
	return m, cmd
}

func (m aiTUIModel) handleConnLost(msg connLostMsg) (tea.Model, tea.Cmd) {
	if m.connDown() {
		return m, nil
	}
	cmd := m.beginReconnect(msg.err)
	return m, cmd
}

// startReconnectProbe fires the connection probe for the auto-reconnect loop.
func (m aiTUIModel) startReconnectProbe() tea.Cmd {
	return m.probe(func(err error) tea.Msg { return reconnectProbeMsg{err: err} })
}

func (m aiTUIModel) handleReconnectTick() (tea.Model, tea.Cmd) {
	if !m.conn.reconnecting || m.conn.reconnectDisabled {
		m.conn.reconnectStatus = ""
		return m, nil
	}

	remaining := time.Until(m.conn.reconnectAt)
	if remaining > 0 {
		secs := int(remaining.Seconds()) + 1
		m.conn.reconnectStatus = fmt.Sprintf("↻ reconnecting in %ds…", secs)
		return m, reconnectTick()
	}

	// Time to probe.
	m.conn.reconnectStatus = "↻ connecting…"
	return m, m.startReconnectProbe()
}

func (m aiTUIModel) handleReconnectProbe(msg reconnectProbeMsg) (tea.Model, tea.Cmd) {
	if !m.conn.reconnecting {
		return m, nil // /disconnect ended the loop while the probe was in flight
	}
	if msg.err == nil {
		// Connected! Everything that waited on the clock goes at once: the
		// session's adapters retry their commands, and the sidebar reloads.
		m.conn.reconnecting = false
		m.conn.reconnectStatus = ""
		m.conn.err = nil
		m.conn.checked = true
		if m.session != nil && m.session.gate != nil {
			m.session.gate.Up()
		}
		m.appendTranscript(infoStyle.Render("✓ connected") + "\n\n")
		resume := m.resumeSidebar()
		return m, resume
	}
	// Still failing — double backoff and keep ticking.
	m.conn.reconnectBackoff = min(m.conn.reconnectBackoff*2, reconnectMaxBackoff)
	m.conn.reconnectAt = time.Now().Add(m.conn.reconnectBackoff)
	return m, reconnectTick()
}

// resumeSidebar reloads the sidebar windows right after the connection came
// back — and, with periodic refresh on, restarts its chain (a fetch's
// completion schedules the next one). A fetch already in flight does both
// itself.
func (m *aiTUIModel) resumeSidebar() tea.Cmd {
	if len(m.objTypes) == 0 || m.refreshing {
		return nil
	}
	if m.autoUpdateEnabled() {
		return m.beginRefresh()
	}
	m.loadingObjects = true
	return m.startLoadObjects()
}

// disconnect ends the auto-reconnect loop (/disconnect). Commands waiting on
// the clock give up with errReconnectDisabled.
func (m *aiTUIModel) disconnect() {
	m.conn.reconnecting = false
	m.conn.reconnectDisabled = true
	m.conn.reconnectStatus = ""
	if m.session != nil && m.session.gate != nil {
		m.session.gate.Disable()
	}
}

// reconnectNow makes the countdown fire at once (/connect, or r on a sidebar
// window while the broker is down): the next tick of the loop probes. A loop
// that is already running keeps its single tick chain.
func (m *aiTUIModel) reconnectNow() tea.Cmd {
	running := m.conn.reconnecting
	m.conn.reconnectDisabled = false
	m.conn.reconnecting = true
	m.conn.reconnectAt = time.Now()
	m.conn.reconnectStatus = "↻ connecting…"
	if m.session != nil && m.session.gate != nil {
		m.session.gate.Enable()
	}
	if running {
		return nil
	}
	return reconnectTick()
}
