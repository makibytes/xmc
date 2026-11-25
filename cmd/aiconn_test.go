package cmd

import (
	"context"
	"errors"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/makibytes/xmc/broker/backends"
)

// These tests pin the AI shell's single reconnect clock (aiconn.go): while the
// broker is unreachable the sidebar does not poll on its own schedule, and the
// moment the title bar's probe succeeds everything that waited goes at once.

type closer struct{}

func (closer) Close() error { return nil }

// clockModel is a model with two sidebar windows (their List funcs counting
// calls), a probe that succeeds or fails per *pingFails, and a gate.
func clockModel(t *testing.T, pingFails *atomic.Bool, lists *atomic.Int32) aiTUIModel {
	t.Helper()
	m := newTestModelWithObjects()
	for i := range m.objTypes {
		m.objTypes[i].listFn = func() ([]backends.ObjectNode, error) {
			lists.Add(1)
			return []backends.ObjectNode{{Name: "q"}}, nil
		}
	}
	gate := newConnGate()
	m.session = &shellSession{gate: gate, spec: BrokerSpec{
		Ping: func() (Closeable, error) {
			if pingFails.Load() {
				return nil, &errConnRefused
			}
			return closer{}, nil
		},
	}}
	m.ai = &aiSession{}
	return m
}

// errConnRefused is a refused connection as the brokers' clients report it.
var errConnRefused = refusedErr{}

type refusedErr struct{}

func (refusedErr) Error() string        { return "dial tcp 127.0.0.1:8161: connect: connection refused" }
func (refusedErr) Is(target error) bool { return target == syscall.ECONNREFUSED }

func TestConnClock_SidebarStopsPollingWhileTheBrokerIsDown(t *testing.T) {
	var fails atomic.Bool
	var lists atomic.Int32
	m := clockModel(t, &fails, &lists)
	m.ai.refreshEnabled = true
	m.refreshEnabled = true

	// The initial probe failed: the countdown runs.
	updated, cmd := m.Update(connMsg{err: &errConnRefused})
	m = updated.(aiTUIModel)
	if !m.conn.reconnecting || cmd == nil {
		t.Fatalf("reconnecting=%v cmd=%v, want the countdown started", m.conn.reconnecting, cmd != nil)
	}
	if !m.session.gate.down {
		t.Error("the gate must be down so the session's adapters wait on the clock")
	}

	// The periodic refresh tick is dropped — and nothing re-arms it.
	updated, cmd = m.Update(refreshTickMsg{})
	m = updated.(aiTUIModel)
	if cmd != nil || m.refreshing || lists.Load() != 0 {
		t.Errorf("a refresh tick during an outage must do nothing (cmd=%v refreshing=%v lists=%d)", cmd != nil, m.refreshing, lists.Load())
	}
	// Neither does the watchdog re-issue a fetch.
	m.refreshing, m.refreshGen = true, 4
	if _, cmd = m.Update(refreshWatchdogMsg{gen: 4}); cmd != nil {
		t.Error("the refresh watchdog must not fetch during an outage")
	}
}

func TestConnClock_ReconnectReloadsTheSidebarOnceAndResumesPolling(t *testing.T) {
	var fails atomic.Bool
	var lists atomic.Int32
	m := clockModel(t, &fails, &lists)
	m.ai.refreshEnabled, m.refreshEnabled = true, true
	updated, _ := m.Update(connMsg{err: &errConnRefused})
	m = updated.(aiTUIModel)

	// A failed probe keeps the countdown going, with a longer wait.
	before := m.conn.reconnectBackoff
	updated, cmd := m.Update(reconnectProbeMsg{err: &errConnRefused})
	m = updated.(aiTUIModel)
	if cmd == nil || m.conn.reconnectBackoff <= before || !m.conn.reconnecting {
		t.Fatalf("backoff %s→%s reconnecting=%v", before, m.conn.reconnectBackoff, m.conn.reconnecting)
	}

	// The probe succeeds: gate up, one sidebar load for every window, polling resumed.
	updated, cmd = m.Update(reconnectProbeMsg{})
	m = updated.(aiTUIModel)
	if m.connDown() || m.session.gate.down {
		t.Fatalf("still down after a successful probe (conn=%+v gate down=%v)", m.conn, m.session.gate.down)
	}
	if !m.refreshing {
		t.Error("the periodic refresh chain must restart (a fetch in flight re-arms the next tick)")
	}
	msg := msgFrom(t, cmd, func(msg tea.Msg) bool { _, ok := msg.(objectsMsg); return ok })
	if got := lists.Load(); got != 2 {
		t.Errorf("List calls = %d, want exactly one per window", got)
	}
	// Its completion schedules the next tick.
	updated, cmd = m.Update(msg)
	if cmd == nil || updated.(aiTUIModel).refreshing {
		t.Errorf("after the reload the chain must be scheduled again (cmd=%v)", cmd != nil)
	}
}

func TestConnClock_ReconnectWithAutoRefreshOffStillReloadsOnce(t *testing.T) {
	var fails atomic.Bool
	var lists atomic.Int32
	m := clockModel(t, &fails, &lists)
	m.ai.refreshEnabled, m.refreshEnabled = false, false
	updated, _ := m.Update(connMsg{err: &errConnRefused})
	updated, cmd := updated.Update(reconnectProbeMsg{})
	if updated.(aiTUIModel).refreshing {
		t.Error("with periodic refresh off there is no chain to restart")
	}
	msgFrom(t, cmd, func(msg tea.Msg) bool { _, ok := msg.(objectsMsg); return ok })
	if lists.Load() != 2 {
		t.Errorf("List calls = %d, want one per window", lists.Load())
	}
}

// A connection lost mid-session — seen by an adapter, a listing, a command —
// starts the same countdown.
func TestConnClock_ConnectionLostMidSession(t *testing.T) {
	var fails atomic.Bool
	var lists atomic.Int32
	m := clockModel(t, &fails, &lists)

	updated, cmd := m.Update(connLostMsg{err: &errConnRefused})
	m = updated.(aiTUIModel)
	if !m.conn.reconnecting || cmd == nil || !m.session.gate.down {
		t.Fatalf("an adapter's connection error must start the countdown (conn=%+v)", m.conn)
	}
	// A second report during the outage changes nothing.
	if _, cmd = m.Update(connLostMsg{err: &errConnRefused}); cmd != nil {
		t.Error("a second report during an outage must not start another loop")
	}
}

func TestConnClock_FailedListingsConfirmWithOneProbe(t *testing.T) {
	var fails atomic.Bool
	var lists atomic.Int32
	m := clockModel(t, &fails, &lists)
	m.conn.lastCheck = time.Time{} // the initial probe is long past
	fails.Store(true)

	// Every window failed with a connection error: one confirming probe, which
	// fails and so starts the countdown.
	updated, cmd := m.Update(objectsMsg{windows: make([][]backends.ObjectNode, 2), errs: []error{&errConnRefused, &errConnRefused}})
	m = updated.(aiTUIModel)
	if cmd == nil {
		t.Fatal("expected a confirming probe")
	}
	probed := msgFrom(t, cmd, func(msg tea.Msg) bool { _, ok := msg.(connMsg); return ok })
	updated, _ = m.Update(probed)
	m = updated.(aiTUIModel)
	if !m.conn.reconnecting {
		t.Error("the failed confirming probe must start the reconnect countdown")
	}

	// A second batch of failures during the outage probes nothing more.
	if _, cmd = m.Update(objectsMsg{windows: make([][]backends.ObjectNode, 2), errs: []error{&errConnRefused, &errConnRefused}}); cmd != nil {
		t.Error("no further probe while reconnecting")
	}
}

func TestConnClock_ConfirmingProbesAreRateLimitedAndNeedARealOutage(t *testing.T) {
	var fails atomic.Bool
	var lists atomic.Int32
	m := clockModel(t, &fails, &lists)
	down := []error{&errConnRefused, &errConnRefused}

	// Just probed (newAITUIModel stamps lastCheck): no second probe yet.
	if cmd := m.noteConnFailure(down[0]); cmd != nil {
		t.Error("a probe within connRecheckEvery of the last one must not start")
	}
	m.conn.lastCheck = time.Now().Add(-time.Minute)
	if cmd := m.noteConnFailure(errors.New("queue not found")); cmd != nil {
		t.Error("an ordinary error is not evidence of an outage")
	}
	// One window listed fine: the management connection is up; a failure of
	// another window (a permission error, a missing plugin) is not an outage.
	m.conn.lastCheck = time.Time{}
	if err := m.connectionErrorOf(objectsMsg{windows: make([][]backends.ObjectNode, 2), errs: []error{nil, &errConnRefused}}); err != nil {
		t.Errorf("connectionErrorOf = %v, want nil when a window listed", err)
	}
	if cmd := m.noteConnFailure(down[0]); cmd == nil {
		t.Error("a connection error with no recent probe must start one")
	}
}

func TestConnClock_DisconnectEndsTheWait(t *testing.T) {
	var fails atomic.Bool
	var lists atomic.Int32
	m := clockModel(t, &fails, &lists)
	updated, _ := m.Update(connMsg{err: &errConnRefused})
	m = updated.(aiTUIModel)

	errc := make(chan error, 1)
	go func() { errc <- m.session.gate.pace(context.Background(), &errConnRefused) }()
	time.Sleep(20 * time.Millisecond)

	m.input.SetValue("/disconnect")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(aiTUIModel)
	select {
	case err := <-errc:
		if !errors.Is(err, errReconnectDisabled) {
			t.Errorf("pace = %v, want errReconnectDisabled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("/disconnect must release commands waiting on the clock")
	}
	if m.conn.reconnecting || !m.conn.reconnectDisabled {
		t.Errorf("conn = %+v", m.conn)
	}
}

func TestConnGate_PaceWaitsForUpAndHonoursTheContext(t *testing.T) {
	g := newConnGate()
	var lost atomic.Int32
	g.onLost = func(error) { lost.Add(1) }

	// The first connection error reports the loss and blocks; Up releases it.
	errc := make(chan error, 1)
	go func() { errc <- g.pace(context.Background(), errors.New("x")) }()
	for g.mu.Lock(); !g.down; g.mu.Lock() {
		g.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	g.mu.Unlock()
	select {
	case <-errc:
		t.Fatal("pace returned while the broker is down")
	case <-time.After(30 * time.Millisecond):
	}
	g.Up()
	if err := <-errc; err != nil {
		t.Errorf("pace = %v after Up", err)
	}
	if lost.Load() != 1 {
		t.Errorf("onLost called %d times, want once", lost.Load())
	}

	// Further errors during one outage don't re-report; a cancelled ctx ends the wait.
	g.SetDown()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := g.pace(ctx, errors.New("y")); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("pace = %v, want the context error", err)
	}
	if lost.Load() != 1 {
		t.Errorf("onLost called %d times during one outage", lost.Load())
	}
}

// A paced reconnecting adapter waits for the gate instead of backing off by
// itself, then reconnects and retries the call exactly once the clock says so.
func TestReconnectingQueue_PacedByTheGate(t *testing.T) {
	g := newConnGate()
	var lost atomic.Int32
	g.onLost = func(error) { lost.Add(1) }

	var connects atomic.Int32
	factory := func() (backends.QueueBackend, error) {
		n := connects.Add(1)
		return &flakyQueue{failFirst: n == 1}, nil
	}
	q := newReconnectingQueue(factory, ReconnectOptions{Pace: g.pace})

	done := make(chan error, 1)
	go func() {
		_, err := q.Receive(context.Background(), backends.ReceiveOptions{Queue: "q"})
		done <- err
	}()

	deadline := time.After(2 * time.Second)
	for lost.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("the connection error was never reported to the gate")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	select {
	case err := <-done:
		t.Fatalf("the call returned (%v) before the clock said the broker is back", err)
	case <-time.After(50 * time.Millisecond):
	}
	if connects.Load() != 1 {
		t.Errorf("reconnected %d times before the gate opened", connects.Load())
	}

	g.Up()
	if err := <-done; err != nil {
		t.Fatalf("Receive after reconnect: %v", err)
	}
	if connects.Load() != 2 {
		t.Errorf("connects = %d, want 2", connects.Load())
	}
}

func TestReconnectingQueue_PaceErrorEndsTheRetry(t *testing.T) {
	boom := errors.New("auto-reconnect is disabled")
	q := newReconnectingQueue(func() (backends.QueueBackend, error) { return &flakyQueue{failFirst: true}, nil },
		ReconnectOptions{Pace: func(context.Context, error) error { return boom }})
	_, err := q.Receive(context.Background(), backends.ReceiveOptions{Queue: "q"})
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want the pacer's error", err)
	}
}

// flakyQueue fails its first Receive with a connection error.
type flakyQueue struct {
	backends.QueueBackend
	failFirst bool
}

func (f *flakyQueue) Receive(context.Context, backends.ReceiveOptions) (*backends.Message, error) {
	if f.failFirst {
		f.failFirst = false
		return nil, &errConnRefused
	}
	return &backends.Message{Data: []byte("ok")}, nil
}

func (f *flakyQueue) Close() error { return nil }
