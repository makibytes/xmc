package cmd

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// connState holds connection probe and auto-reconnect state for the AI TUI.
type connState struct {
	checked           bool          // true after the initial probe completes
	err               error         // non-nil if the broker is unreachable
	reconnecting      bool          // true while backoff is ticking or a probe is in-flight
	reconnectAt       time.Time     // wall-clock time the next probe fires
	reconnectBackoff  time.Duration // current wait interval (doubles each failure, capped at 3 min)
	reconnectDisabled bool          // user ran /disconnect — stop auto-retry
	reconnectBlink    bool          // toggled every 500ms for the title-bar yellow blink
	reconnectStatus   string        // one-line countdown shown below the viewport
}

const (
	reconnectInitialBackoff = 2 * time.Second
	reconnectMaxBackoff     = 3 * time.Minute
)

// ---------- Connection probe ----------

func (m aiTUIModel) startProbeConnection() tea.Cmd {
	ping := m.session.spec.Ping
	return func() tea.Msg {
		conn, err := ping()
		if err != nil {
			return connMsg{err: err}
		}
		_ = conn.Close()
		return connMsg{}
	}
}

func (m aiTUIModel) handleConnDone(msg connMsg) (tea.Model, tea.Cmd) {
	m.conn.checked = true
	m.conn.err = msg.err
	if msg.err == nil {
		return m, nil
	}
	m.appendTranscript(warnStyle.Render("connection error: "+msg.err.Error()) + "\n\n")
	if m.conn.reconnectDisabled {
		return m, nil
	}
	// Start auto-reconnect with initial backoff.
	m.conn.reconnecting = true
	m.conn.reconnectBackoff = reconnectInitialBackoff
	m.conn.reconnectAt = time.Now().Add(m.conn.reconnectBackoff)
	return m, tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return reconnectTickMsg{} })
}

// startReconnectProbe fires the connection probe and returns the result as
// reconnectProbeMsg (distinct from connMsg so the two paths don't collide).
func (m aiTUIModel) startReconnectProbe() tea.Cmd {
	ping := m.session.spec.Ping
	return func() tea.Msg {
		conn, err := ping()
		if err != nil {
			return reconnectProbeMsg{err: err}
		}
		_ = conn.Close()
		return reconnectProbeMsg{}
	}
}

func (m aiTUIModel) handleReconnectTick() (tea.Model, tea.Cmd) {
	if !m.conn.reconnecting || m.conn.reconnectDisabled {
		m.conn.reconnectStatus = ""
		return m, nil
	}
	m.conn.reconnectBlink = !m.conn.reconnectBlink

	remaining := time.Until(m.conn.reconnectAt)
	if remaining > 0 {
		secs := int(remaining.Seconds()) + 1
		m.conn.reconnectStatus = fmt.Sprintf("↻ reconnecting in %ds…", secs)
		return m, tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return reconnectTickMsg{} })
	}

	// Time to probe.
	m.conn.reconnectStatus = "↻ connecting…"
	return m, m.startReconnectProbe()
}

func (m aiTUIModel) handleReconnectProbe(msg reconnectProbeMsg) (tea.Model, tea.Cmd) {
	if msg.err == nil {
		// Connected!
		m.conn.reconnecting = false
		m.conn.reconnectStatus = ""
		m.conn.err = nil
		m.conn.checked = true
		m.appendTranscript(infoStyle.Render("✓ connected") + "\n\n")
		return m, nil
	}
	// Still failing — double backoff and keep ticking.
	m.conn.reconnectBackoff *= 2
	if m.conn.reconnectBackoff > reconnectMaxBackoff {
		m.conn.reconnectBackoff = reconnectMaxBackoff
	}
	m.conn.reconnectAt = time.Now().Add(m.conn.reconnectBackoff)
	return m, tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return reconnectTickMsg{} })
}
