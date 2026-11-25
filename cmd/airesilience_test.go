package cmd

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/makibytes/xmc/broker/backends"
)

// These tests pin the AI shell's central promise: whatever a provider, a
// broker call or a command does — errors, panics, hangs, cancellations that
// arrive late — the UI ends up idle again with the outcome on screen.

// fakeAI is an aiClient driven by a func.
type fakeAI struct {
	complete func(ctx context.Context) (string, error)
}

func (f fakeAI) Complete(ctx context.Context, _ string, _ []aiMessage, _ func(string)) (string, TokenUsage, error) {
	text, err := f.complete(ctx)
	return text, TokenUsage{}, err
}

// newAskModel is an AI-mode model whose session is already initialised with c.
func newAskModel(c aiClient) aiTUIModel {
	m := newTestModel()
	m.ai.initOnce.Do(func() {})
	m.ai.client = c
	m.ai.providerName, m.ai.modelName = "fake", "fake-1"
	return m
}

// msgFrom runs cmd and returns the first message want accepts. Batches run
// their members concurrently, so a member that only ticks (the watchdog)
// does not hold the result up.
func msgFrom(t *testing.T, cmd tea.Cmd, want func(tea.Msg) bool) tea.Msg {
	t.Helper()
	ch := make(chan tea.Msg, 16)
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			msg := c()
			if batch, ok := msg.(tea.BatchMsg); ok {
				for _, sub := range batch {
					run(sub)
				}
				return
			}
			ch <- msg
		}()
	}
	run(cmd)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-ch:
			if want(msg) {
				return msg
			}
		case <-deadline:
			t.Fatal("the command never produced the expected message")
			return nil
		}
	}
}

func isAIDone(msg tea.Msg) bool { _, ok := msg.(aiDoneMsg); return ok }

// ask types prompt in AI mode, presses Enter and returns the model plus the
// request command.
func ask(t *testing.T, m aiTUIModel, prompt string) (aiTUIModel, tea.Cmd) {
	t.Helper()
	m.input.SetValue(prompt)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(aiTUIModel)
	if m.state != tuiThinking {
		t.Fatalf("state after Enter = %v, want thinking", m.state)
	}
	return m, cmd
}

func settle(t *testing.T, m aiTUIModel, cmd tea.Cmd) aiTUIModel {
	t.Helper()
	msg := msgFrom(t, cmd, isAIDone)
	updated, _ := m.Update(msg)
	return updated.(aiTUIModel)
}

func TestAIShell_EveryRequestFailureEndsIdleWithOneErrorLine(t *testing.T) {
	apiErr := &apiError{provider: "gemini", status: 400, kind: "API_KEY_INVALID", message: "API key not valid.", hint: "check GEMINI_API_KEY"}
	tests := []struct {
		name string
		fn   func(context.Context) (string, error)
		want string
	}{
		{"rejected key", func(context.Context) (string, error) { return "", apiErr }, "error: gemini: HTTP 400 API_KEY_INVALID: API key not valid. — check GEMINI_API_KEY"},
		{"timeout", func(context.Context) (string, error) { return "", context.DeadlineExceeded }, "the AI request timed out after 2m0s"},
		{"spontaneous cancel", func(context.Context) (string, error) { return "", context.Canceled }, "the AI request was cancelled"},
		{"panic in the client", func(context.Context) (string, error) { panic("boom") }, "internal error: boom"},
		{"plain error", func(context.Context) (string, error) { return "", errors.New("connection reset") }, "error: connection reset"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newAskModel(fakeAI{complete: tt.fn})
			m, cmd := ask(t, m, "list the queues")
			m = settle(t, m, cmd)
			if m.state != tuiIdle {
				t.Fatalf("state = %v, want idle", m.state)
			}
			out := m.transcript.String()
			if !strings.Contains(out, tt.want) {
				t.Errorf("transcript lacks %q:\n%s", tt.want, out)
			}
			for _, h := range m.ai.history {
				if h.Role == "user" && h.Content == "list the queues" {
					t.Error("the unanswered question must not stay in the history")
				}
			}
			if !m.input.Focused() {
				t.Error("the input must have focus again")
			}
		})
	}
}

// Esc right after Enter must cancel the request, even before the background
// goroutine got going: the context is created with the request, not delivered
// later by a message.
func TestAIShell_EscCancelsTheRequestContextImmediately(t *testing.T) {
	started := make(chan struct{})
	var cancelled atomic.Bool
	m := newAskModel(fakeAI{complete: func(ctx context.Context) (string, error) {
		close(started)
		<-ctx.Done()
		cancelled.Store(true)
		return "", ctx.Err()
	}})
	m, cmd := ask(t, m, "hello")
	done := make(chan tea.Msg, 1)
	go func() { done <- msgFrom(t, cmd, isAIDone) }()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // no message round trip needed
	m = updated.(aiTUIModel)
	if m.state != tuiIdle || !strings.Contains(m.transcript.String(), "(cancelled)") {
		t.Fatalf("state = %v, transcript:\n%s", m.state, m.transcript.String())
	}
	<-started
	late := <-done
	if !cancelled.Load() {
		t.Error("the request's context was not cancelled")
	}
	before := m.transcript.String()
	updated, _ = m.Update(late)
	m = updated.(aiTUIModel)
	if m.transcript.String() != before || m.state != tuiIdle {
		t.Errorf("the cancelled request's late result must be ignored:\n%s", m.transcript.String())
	}
}

func TestAIShell_WatchdogEndsAWedgedRequest(t *testing.T) {
	m := newAskModel(fakeAI{complete: func(context.Context) (string, error) { select {} }})
	m, cmd := ask(t, m, "hello")
	_ = cmd // never run: the request goroutine would block for good
	gen := m.aiGen

	// A stale watchdog (a request that has since been replaced) does nothing.
	updated, _ := m.Update(aiWatchdogMsg{gen: gen - 1})
	if updated.(aiTUIModel).state != tuiThinking {
		t.Fatal("a stale watchdog must not end the current request")
	}
	updated, _ = m.Update(aiWatchdogMsg{gen: gen})
	m = updated.(aiTUIModel)
	if m.state != tuiIdle || !strings.Contains(m.transcript.String(), "no answer from the AI provider in time") {
		t.Errorf("state = %v, transcript:\n%s", m.state, m.transcript.String())
	}
	// Whatever the stuck request reports afterwards is stale.
	before := m.transcript.String()
	updated, _ = m.Update(aiDoneMsg{text: "manage list", gen: gen})
	if got := updated.(aiTUIModel); got.transcript.String() != before || got.state != tuiIdle {
		t.Error("the abandoned request's result must be ignored")
	}
}

func TestAIShell_WatchdogDelayFollowsTheConfiguredTimeout(t *testing.T) {
	m := newAskModel(fakeAI{})
	if got := m.ai.timeout(); got != defaultRequestTimeout {
		t.Errorf("timeout = %s", got)
	}
	m.ai.requestTimeout = 5 * time.Minute
	if got := m.ai.timeout(); got != 5*time.Minute {
		t.Errorf("timeout = %s", got)
	}
}

// Esc on a command that ignores its context: the UI stops waiting (after the
// grace period, or at once on a second press) and drops the late result.
func TestAIShell_EscAbandonsACommandThatWillNotStop(t *testing.T) {
	m := newTestModel()
	m.state = tuiExecuting
	m.execGen = 3
	var cancelled atomic.Bool
	m.execCancel = func() { cancelled.Store(true) }
	m.liveCapture = newExecCapture()
	m.proposedCmd = "receive q"

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(aiTUIModel)
	if !cancelled.Load() || !m.execCancelled || cmd == nil {
		t.Fatalf("first Esc must cancel and arm the abandon timer (cancelled=%v execCancelled=%v cmd=%v)", cancelled.Load(), m.execCancelled, cmd != nil)
	}
	if m.state != tuiExecuting {
		t.Fatal("the first Esc gives the command its chance to stop")
	}

	// A timer for another run does nothing; this run's timer abandons it.
	updated, _ = m.Update(execAbandonMsg{gen: 2})
	if updated.(aiTUIModel).state != tuiExecuting {
		t.Fatal("a stale abandon timer must not end the current run")
	}
	updated, _ = m.Update(execAbandonMsg{gen: 3})
	m = updated.(aiTUIModel)
	if m.state != tuiIdle || !strings.Contains(m.transcript.String(), "did not stop in time") {
		t.Fatalf("state = %v, transcript:\n%s", m.state, m.transcript.String())
	}
	before := m.transcript.String()
	updated, _ = m.Update(execDoneMsg{gen: 3, stdout: "late output"})
	if got := updated.(aiTUIModel); got.transcript.String() != before {
		t.Errorf("the abandoned run's late result must be dropped:\n%s", got.transcript.String())
	}
}

func TestAIShell_SecondEscAbandonsAtOnce(t *testing.T) {
	m := newTestModel()
	m.state = tuiExecuting
	m.execGen = 1
	m.execCancel = func() {}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if got := updated.(aiTUIModel); got.state != tuiIdle {
		t.Errorf("state = %v, want idle after the second Esc", got.state)
	}
}

// Sidebar actions are cancellable too, and report a cancellation as such.
func TestAIShell_SideActionEscCancelsItsContext(t *testing.T) {
	m := newTestModel()
	var sawCancel atomic.Bool
	cmd := m.startSideAction("▶ receive Queue \"q\"", func(ctx context.Context) sideActionMsg {
		<-ctx.Done()
		sawCancel.Store(true)
		return sideActionMsg{err: ctx.Err()}
	})
	if m.state != tuiExecuting {
		t.Fatal("a side action runs in the executing state")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(aiTUIModel)
	msg := <-done
	if !sawCancel.Load() {
		t.Error("Esc did not cancel the action's context")
	}
	updated, _ = m.Update(msg)
	m = updated.(aiTUIModel)
	if m.state != tuiIdle || !strings.Contains(m.transcript.String(), "(cancelled)") || strings.Contains(m.transcript.String(), "✗") {
		t.Errorf("state = %v, transcript:\n%s", m.state, m.transcript.String())
	}
}

func TestAIShell_PanicInASideActionIsReported(t *testing.T) {
	m := newTestModel()
	cmd := m.startSideAction("▶ send", func(context.Context) sideActionMsg { panic("driver bug") })
	updated, _ := m.Update(cmd())
	m = updated.(aiTUIModel)
	if m.state != tuiIdle || !strings.Contains(m.transcript.String(), "internal error: driver bug") {
		t.Errorf("state = %v, transcript:\n%s", m.state, m.transcript.String())
	}
}

func TestGuard_TurnsAPanicIntoTheDoneMessage(t *testing.T) {
	msg := guard(func(err error) tea.Msg { return connMsg{err: err} }, func() tea.Msg { panic("kaboom") })()
	cm, ok := msg.(connMsg)
	if !ok || cm.err == nil || !strings.Contains(cm.err.Error(), "internal error: kaboom") {
		t.Errorf("msg = %#v", msg)
	}
}

// A connection error is not something the AI can fix by rewording the command.
func TestAIShell_NoAutoFixForConnectionErrors(t *testing.T) {
	m := newTestModel()
	m.state = tuiExecuting
	m.mode = modeAI
	m.proposedCmd = "receive q"
	gen := m.aiGen
	updated, cmd := m.Update(execDoneMsg{err: &errRefused, stdout: ""})
	m = updated.(aiTUIModel)
	if m.state != tuiIdle || m.aiGen != gen || cmd != nil {
		t.Errorf("state = %v aiGen %d→%d cmd=%v: a connection problem must not start an auto-fix request", m.state, gen, m.aiGen, cmd != nil)
	}
	if !strings.Contains(m.transcript.String(), "not asking the AI to fix it") {
		t.Errorf("transcript:\n%s", m.transcript.String())
	}

	// An ordinary failure still is.
	m = newTestModel()
	m.state, m.mode, m.proposedCmd = tuiExecuting, modeAI, "receive q"
	updated, _ = m.Update(execDoneMsg{err: errors.New("queue not found")})
	if got := updated.(aiTUIModel); got.state != tuiThinking {
		t.Errorf("state = %v, want an auto-fix request", got.state)
	}
}

// errRefused is a refused connection, as the brokers' clients report it.
var errRefused = connRefused{}

type connRefused struct{}

func (connRefused) Error() string { return "dial tcp 127.0.0.1:5672: connect: connection refused" }
func (connRefused) Is(target error) bool {
	return target == syscall.ECONNREFUSED
}

// The system prompt's topology comes from the sidebar's listing, in the same
// text "manage list" prints — and a failed refresh keeps the previous one.
func TestAIShell_TopologyComesFromTheSidebarListing(t *testing.T) {
	m := newTestModelWithObjects()
	m.ai = &aiSession{}
	updated, _ := m.Update(objectsMsg{
		windows: [][]backends.ObjectNode{
			{{Name: "orders", Kind: "queue"}, {Name: "audit", Kind: "queue"}},
			{{Name: "events", Kind: "topic"}},
		},
		errs: make([]error, 2),
	})
	m = updated.(aiTUIModel)
	if got := m.ai.topology; !strings.Contains(got, "audit") || !strings.Contains(got, "orders") || strings.Index(got, "audit") > strings.Index(got, "orders") {
		t.Errorf("topology = %q, want the names sorted", got)
	}

	previous := m.ai.topology
	updated, _ = m.Update(objectsMsg{windows: make([][]backends.ObjectNode, 2), errs: []error{errors.New("jolokia down"), nil}})
	m = updated.(aiTUIModel)
	if m.ai.topology != previous {
		t.Errorf("a failed listing replaced the topology: %q", m.ai.topology)
	}
}

func TestWriteObjectList_FormatIsStable(t *testing.T) {
	var b strings.Builder
	writeObjectList(&b, []objectGroup{
		{label: "Queues", nodes: []backends.ObjectNode{
			{Name: "q.b", Kind: "anycast", Metrics: []backends.Metric{{Label: "messages", Value: 3}}},
			{Name: "q.a"},
		}},
		{label: "Topics", nodes: []backends.ObjectNode{
			{Name: "t", Children: []backends.ObjectNode{{Name: "sub", Kind: "subscription"}}},
		}},
	})
	want := "queues  q.a\n" +
		"queues  q.b                                       anycast messages=3\n" +
		"topics  t\n" +
		"topics    └ subscription sub\n"
	if got := b.String(); got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
	// A single group is not prefixed.
	b.Reset()
	writeObjectList(&b, []objectGroup{{label: "Queues", nodes: []backends.ObjectNode{{Name: "only"}}}})
	if b.String() != "only\n" {
		t.Errorf("single group = %q", b.String())
	}
}

// A killed process whose cancel func was set at creation stops immediately.
func TestBackgroundProcess_HasItsCancelFromTheStart(t *testing.T) {
	m := newTestModel()
	m.rootCmd = nil
	m.session = &shellSession{spec: BrokerSpec{}}
	model, _ := m.startBackgroundProcess("receive q --for 1s")
	got := model.(aiTUIModel)
	if len(got.procs) != 1 || got.procs[0].cancel == nil {
		t.Fatalf("procs = %+v: the cancel func must exist before the goroutine runs", got.procs)
	}
	got.procs[0].stop() // K right after start must not be lost
}
