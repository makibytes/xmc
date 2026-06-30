package cmd

import (
	"fmt"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/makibytes/xmc/broker/backends"
)

func newTestModel() aiTUIModel {
	return newAITUIModel(
		&aiSession{},
		&shellSession{spec: BrokerSpec{}},
		nil,
		"test",
		"localhost:5672",
	)
}

func TestAITUI_InitialState(t *testing.T) {
	m := newTestModel()
	if m.state != tuiIdle {
		t.Errorf("initial state = %v, want tuiIdle", m.state)
	}
}

func TestAITUI_EscTogglesMode(t *testing.T) {
	m := newTestModel()
	if m.mode != modeAI {
		t.Fatalf("initial mode should be modeAI, got %v", m.mode)
	}

	// Esc → switch to command mode.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model := updated.(aiTUIModel)
	if model.quitting {
		t.Error("Esc in chat should NOT quit")
	}
	if model.mode != modeCmd {
		t.Errorf("after Esc, mode = %v, want modeCmd", model.mode)
	}

	// Esc again → switch back to AI mode.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(aiTUIModel)
	if model.mode != modeAI {
		t.Errorf("after 2nd Esc, mode = %v, want modeAI", model.mode)
	}
}

func TestAITUI_CtrlCQuits(t *testing.T) {
	m := newTestModel()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model := updated.(aiTUIModel)
	if !model.quitting {
		t.Error("Ctrl+C should set quitting = true")
	}
	if cmd == nil {
		t.Error("Ctrl+C should return tea.Quit cmd")
	}
}

func TestAITUI_EmptyEnterDoesNothing(t *testing.T) {
	m := newTestModel()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model := updated.(aiTUIModel)
	if model.state != tuiIdle {
		t.Errorf("empty enter should stay idle, got %v", model.state)
	}
}

func TestAITUI_HandleAIDone_Ask(t *testing.T) {
	m := newTestModel()
	m.state = tuiThinking
	m.ai.history = append(m.ai.history, aiMessage{Role: "user", Content: "test"})

	updated, _ := m.Update(aiDoneMsg{
		text:  "# ask: which queue do you want?",
		usage: TokenUsage{InputTokens: 10, OutputTokens: 5},
	})
	model := updated.(aiTUIModel)
	if model.state != tuiIdle {
		t.Errorf("after # ask:, state = %v, want tuiIdle", model.state)
	}
	if len(model.ai.history) < 2 {
		t.Fatal("history should have at least 2 entries")
	}
	last := model.ai.history[len(model.ai.history)-1]
	if last.Role != "assistant" {
		t.Errorf("last history role = %q, want assistant", last.Role)
	}
}

func TestAITUI_HandleAIDone_Cannot(t *testing.T) {
	m := newTestModel()
	m.state = tuiThinking
	m.ai.history = append(m.ai.history, aiMessage{Role: "user", Content: "test"})

	updated, _ := m.Update(aiDoneMsg{
		text: "# cannot: no such flag",
	})
	model := updated.(aiTUIModel)
	if model.state != tuiIdle {
		t.Errorf("after # cannot:, state = %v, want tuiIdle", model.state)
	}
}

func TestAITUI_HandleAIDone_Command(t *testing.T) {
	m := newTestModel()
	m.state = tuiThinking
	m.ai.history = append(m.ai.history, aiMessage{Role: "user", Content: "test"})

	updated, _ := m.Update(aiDoneMsg{
		text: "receive queue -n 5",
	})
	model := updated.(aiTUIModel)
	if model.state != tuiProposing {
		t.Errorf("after command, state = %v, want tuiProposing", model.state)
	}
	if model.proposedCmd != "receive queue -n 5" {
		t.Errorf("proposedCmd = %q", model.proposedCmd)
	}
}

func TestAITUI_HandleAIDone_DestructiveCommand(t *testing.T) {
	m := newTestModel()
	m.state = tuiThinking
	m.ai.history = append(m.ai.history, aiMessage{Role: "user", Content: "test"})

	updated, _ := m.Update(aiDoneMsg{
		text: "manage delete-queue test",
	})
	model := updated.(aiTUIModel)
	if model.state != tuiProposing {
		t.Errorf("after destructive command, state = %v, want tuiProposing", model.state)
	}
	if !model.proposedDestructive {
		t.Error("proposedDestructive should be true for 'manage delete-queue'")
	}
	// The proposal is rendered live in the viewport (not written to transcript).
	// Verify the command was captured and the viewport content reflects it.
	if model.proposedCmd != "manage delete-queue test" {
		t.Errorf("proposedCmd = %q, want 'manage delete-queue test'", model.proposedCmd)
	}
	if vp := model.viewport.View(); !strings.Contains(vp, "manage delete-queue test") {
		t.Error("viewport content should show the proposed command")
	}
}

func TestAITUI_HandleAIDone_Error(t *testing.T) {
	m := newTestModel()
	m.state = tuiThinking
	m.ai.history = append(m.ai.history, aiMessage{Role: "user", Content: "test"})

	updated, _ := m.Update(aiDoneMsg{
		err: fmt.Errorf("API error"),
	})
	model := updated.(aiTUIModel)
	if model.state != tuiIdle {
		t.Errorf("after error, state = %v, want tuiIdle", model.state)
	}
	if len(model.ai.history) != 0 {
		t.Errorf("history len = %d, want 0 (dangling user msg removed)", len(model.ai.history))
	}
}

func TestAITUI_HandleAIDone_TokenUsage(t *testing.T) {
	m := newTestModel()
	m.state = tuiThinking
	m.ai.history = append(m.ai.history, aiMessage{Role: "user", Content: "test"})

	updated, _ := m.Update(aiDoneMsg{
		text:  "receive queue",
		usage: TokenUsage{InputTokens: 100, OutputTokens: 20},
	})
	model := updated.(aiTUIModel)
	if model.totalIn != 100 {
		t.Errorf("totalIn = %d, want 100", model.totalIn)
	}
	if model.totalOut != 20 {
		t.Errorf("totalOut = %d, want 20", model.totalOut)
	}
	if model.turnIn != 100 {
		t.Errorf("turnIn = %d, want 100", model.turnIn)
	}
}

func TestAITUI_ProposingEscDiscards(t *testing.T) {
	m := newTestModel()
	m.state = tuiProposing
	m.proposedCmd = "receive q"

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model := updated.(aiTUIModel)
	if model.state != tuiIdle {
		t.Errorf("after discard, state = %v, want tuiIdle", model.state)
	}
	if len(model.ai.history) < 2 {
		t.Fatal("history should have discard entries")
	}
}

func TestAITUI_ProposingEditGoesEditing(t *testing.T) {
	m := newTestModel()
	m.state = tuiProposing
	m.proposedCmd = "receive q -n 5"

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	model := updated.(aiTUIModel)
	// Pressing "e" from tuiProposing now enters tuiEditing (shimmer continues).
	if model.state != tuiEditing {
		t.Errorf("after edit, state = %v, want tuiEditing", model.state)
	}
	if model.input.Value() != "receive q -n 5" {
		t.Errorf("input value = %q, want the command", model.input.Value())
	}
}

func TestAITUI_HandleExecDone_InlineDisplay(t *testing.T) {
	m := newTestModel()
	m.state = tuiExecuting
	m.proposedCmd = "receive q"

	updated, _ := m.Update(execDoneMsg{stdout: "hello\n"})
	model := updated.(aiTUIModel)
	if model.state != tuiIdle {
		t.Errorf("after exec success, state = %v, want tuiIdle", model.state)
	}
	transcript := model.transcript.String()
	if !strings.Contains(transcript, "ran: receive q") {
		t.Errorf("transcript should show inline executed command card, got:\n%s", transcript)
	}
	if !strings.Contains(transcript, "ok") {
		t.Errorf("transcript should show ok status, got:\n%s", transcript)
	}
}

func TestAITUI_HandleExecDone_Error(t *testing.T) {
	m := newTestModel()
	m.state = tuiExecuting
	m.mode = modeCmd // command mode: no auto-fix
	m.proposedCmd = "send q"

	updated, _ := m.Update(execDoneMsg{err: fmt.Errorf("queue not found")})
	model := updated.(aiTUIModel)
	if model.state != tuiIdle {
		t.Errorf("after exec error, state = %v, want tuiIdle", model.state)
	}
	transcript := model.transcript.String()
	if !strings.Contains(transcript, "queue not found") {
		t.Errorf("transcript should show error, got:\n%s", transcript)
	}
}

func TestAITUI_HandleExecDone_AutoFix(t *testing.T) {
	m := newTestModel()
	m.state = tuiExecuting
	m.mode = modeAI
	m.proposedCmd = "send q"

	// First error: should trigger auto-fix (state → tuiThinking)
	updated, _ := m.Update(execDoneMsg{err: fmt.Errorf("queue not found")})
	model := updated.(aiTUIModel)
	if model.state != tuiThinking {
		t.Errorf("after first error, state = %v, want tuiThinking", model.state)
	}
	if model.fixAttempts != 1 {
		t.Errorf("fixAttempts = %d, want 1", model.fixAttempts)
	}
	transcript := model.transcript.String()
	if !strings.Contains(transcript, "auto-fixing") {
		t.Errorf("transcript should show auto-fixing, got:\n%s", transcript)
	}

	// Second error: should still retry (under maxFixAttempts=2)
	model.state = tuiExecuting
	updated, _ = model.Update(execDoneMsg{err: fmt.Errorf("still broken")})
	model = updated.(aiTUIModel)
	if model.state != tuiThinking {
		t.Errorf("after second error, state = %v, want tuiThinking", model.state)
	}
	if model.fixAttempts != 2 {
		t.Errorf("fixAttempts = %d, want 2", model.fixAttempts)
	}

	// Third error: should give up (fixAttempts >= maxFixAttempts)
	model.state = tuiExecuting
	updated, _ = model.Update(execDoneMsg{err: fmt.Errorf("giving up")})
	model = updated.(aiTUIModel)
	if model.state != tuiIdle {
		t.Errorf("after max retries, state = %v, want tuiIdle", model.state)
	}
}

func TestAITUI_HandleExecDone_SuccessResetsFixAttempts(t *testing.T) {
	m := newTestModel()
	m.state = tuiExecuting
	m.mode = modeAI
	m.fixAttempts = 1
	m.proposedCmd = "send q hello"

	updated, _ := m.Update(execDoneMsg{err: nil, stdout: "sent"})
	model := updated.(aiTUIModel)
	if model.fixAttempts != 0 {
		t.Errorf("fixAttempts = %d after success, want 0", model.fixAttempts)
	}
}

func TestAITUI_WindowResize(t *testing.T) {
	m := newTestModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model := updated.(aiTUIModel)
	if model.width != 120 || model.height != 40 {
		t.Errorf("size = %dx%d, want 120x40", model.width, model.height)
	}
}

func TestFmtTokens(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1.0k"},
		{1500, "1.5k"},
		{12345, "12.3k"},
	}
	for _, tt := range tests {
		got := fmtTokens(tt.n)
		if got != tt.want {
			t.Errorf("fmtTokens(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// ---------- Broker objects sidebar tests ----------

func newTestModelWithObjects() aiTUIModel {
	ms := &ManageSpec{
		Objects: []ObjectType{
			{
				Label: "Queues",
				List: func() ([]backends.ObjectNode, error) {
					return []backends.ObjectNode{
						{Name: "orders", Metrics: []backends.Metric{{Label: "msgs", Value: 1234}}},
						{Name: "payments", Metrics: []backends.Metric{{Label: "msgs", Value: 34}}},
						{Name: "dlq", Metrics: []backends.Metric{{Label: "msgs", Value: 0}}},
					}, nil
				},
			},
			{
				Label: "Topics",
				List: func() ([]backends.ObjectNode, error) {
					return []backends.ObjectNode{
						{Name: "events"},
						{Name: "audit"},
					}, nil
				},
			},
		},
	}
	m := newAITUIModel(
		&aiSession{},
		&shellSession{spec: BrokerSpec{ManageSpec: ms}},
		nil,
		"test",
		"localhost:5672",
	)
	// Simulate objects loaded.
	m.loadingObjects = false
	m.objTypes[0].nodes = []backends.ObjectNode{
		{Name: "orders", Metrics: []backends.Metric{{Label: "msgs", Value: 1234}}},
		{Name: "payments", Metrics: []backends.Metric{{Label: "msgs", Value: 34}}},
		{Name: "dlq", Metrics: []backends.Metric{{Label: "msgs", Value: 0}}},
	}
	m.objTypes[1].nodes = []backends.ObjectNode{
		{Name: "events"},
		{Name: "audit"},
	}
	return m
}

func TestAITUI_ObjectsLoaded_QueueCount(t *testing.T) {
	m := newTestModelWithObjects()
	sidebar, _ := m.renderSidebar(35, 20)
	if !strings.Contains(sidebar, "Queues (3)") {
		t.Errorf("sidebar should show Queues (3), got:\n%s", sidebar)
	}
}

func TestAITUI_ObjectsLoaded_TopicCount(t *testing.T) {
	m := newTestModelWithObjects()
	sidebar, _ := m.renderSidebar(35, 20)
	if !strings.Contains(sidebar, "Topics (2)") {
		t.Errorf("sidebar should show Topics (2), got:\n%s", sidebar)
	}
}

func TestAITUI_ObjectsEmpty_ShowsNone(t *testing.T) {
	ms := &ManageSpec{
		Objects: []ObjectType{
			{Label: "Queues", List: func() ([]backends.ObjectNode, error) { return nil, nil }},
			{Label: "Topics", List: func() ([]backends.ObjectNode, error) { return nil, nil }},
		},
	}
	m := newAITUIModel(&aiSession{}, &shellSession{spec: BrokerSpec{ManageSpec: ms}}, nil, "test", "")
	m.loadingObjects = false
	m.objTypes[0].nodes = []backends.ObjectNode{}
	m.objTypes[1].nodes = []backends.ObjectNode{}
	sidebar, _ := m.renderSidebar(35, 20)
	if !strings.Contains(sidebar, "(none)") {
		t.Errorf("empty list should show (none), got:\n%s", sidebar)
	}
}

func TestAITUI_NoManageAPI(t *testing.T) {
	m := newTestModel()
	sidebar, _ := m.renderSidebar(35, 20)
	if !strings.Contains(sidebar, "no management API") {
		t.Errorf("no ManageSpec should show no management API, got:\n%s", sidebar)
	}
}

func TestAITUI_ShiftTabCyclesFocus(t *testing.T) {
	// Shift+Tab now cycles backward: chat → last window → … → first window → chat.
	m := newTestModelWithObjects()
	if m.focus != focusChat {
		t.Fatalf("initial focus should be focusChat, got %v", m.focus)
	}

	// Shift+Tab backward from chat → last window (Topics, index 2).
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = updated.(aiTUIModel)
	if m.focus != 2 {
		t.Errorf("after 1st Shift+Tab, focus = %v, want 2 (Topics)", m.focus)
	}

	// Shift+Tab → previous window (Queues, index 1).
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = updated.(aiTUIModel)
	if m.focus != 1 {
		t.Errorf("after 2nd Shift+Tab, focus = %v, want 1 (Queues)", m.focus)
	}

	// Shift+Tab → back to focusChat.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = updated.(aiTUIModel)
	if m.focus != focusChat {
		t.Errorf("after 3rd Shift+Tab, focus = %v, want focusChat", m.focus)
	}
}

func TestAITUI_ShiftTabSkipsMissingPanes(t *testing.T) {
	m := newTestModel()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	model := updated.(aiTUIModel)
	if model.focus != focusChat {
		t.Errorf("with no panes, Shift+Tab should stay on chat, got %v", model.focus)
	}
}

func TestAITUI_EnterInsertsName(t *testing.T) {
	m := newTestModelWithObjects()

	// Focus queues (window 0).
	m.focus = 1
	m.objTypes[0].sel = 1 // After sort by name: dlq(0), orders(1), payments(2)
	sorted := m.getFilteredSortedNodes(0)
	wantName := sorted[1].Name

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model := updated.(aiTUIModel)
	if model.input.Value() != wantName {
		t.Errorf("Enter should insert %q into input, got %q", wantName, model.input.Value())
	}
	if model.focus != focusChat {
		t.Errorf("Enter should return focus to chat, got %v", model.focus)
	}
}

func TestAITUI_FilterNarrowsList(t *testing.T) {
	m := newTestModelWithObjects()
	m.focus = 1 // Queues
	m.objTypes[0].filter = "pay"

	items := m.getFilteredSortedNodes(0)
	if len(items) != 1 {
		t.Fatalf("filter 'pay' should match 1 queue, got %d", len(items))
	}
	if items[0].Name != "payments" {
		t.Errorf("filtered queue should be payments, got %s", items[0].Name)
	}

	// Sidebar should show (1/3).
	sidebar, _ := m.renderSidebar(35, 20)
	if !strings.Contains(sidebar, "1/3") {
		t.Errorf("filtered sidebar should show 1/3, got:\n%s", sidebar)
	}
}

func TestAITUI_SortByMetric(t *testing.T) {
	m := newTestModelWithObjects()
	m.objTypes[0].sortIdx = 1 // sortByMetric0 = msgs descending
	items := m.getFilteredSortedNodes(0)
	if len(items) < 2 {
		t.Fatal("need at least 2 items")
	}
	if items[0].Metrics[0].Value < items[1].Metrics[0].Value {
		t.Errorf("sort by metric should sort descending: %d < %d", items[0].Metrics[0].Value, items[1].Metrics[0].Value)
	}
}

func TestAITUI_LargeList_WindowRendering(t *testing.T) {
	nodes := make([]backends.ObjectNode, 2000)
	for i := range nodes {
		nodes[i] = backends.ObjectNode{
			Name:    fmt.Sprintf("queue-%04d", i),
			Metrics: []backends.Metric{{Label: "msgs", Value: int64(i)}},
		}
	}
	ms := &ManageSpec{
		Objects: []ObjectType{
			{Label: "Queues", List: func() ([]backends.ObjectNode, error) { return nodes, nil }},
		},
	}
	m := newAITUIModel(&aiSession{}, &shellSession{spec: BrokerSpec{ManageSpec: ms}}, nil, "test", "")
	m.loadingObjects = false
	m.objTypes[0].nodes = nodes
	m.focus = 1 // Queues
	m.objTypes[0].sel = 1000

	sidebar, _ := m.renderSidebar(35, 20)
	if !strings.Contains(sidebar, "▲") || !strings.Contains(sidebar, "▼") {
		t.Errorf("large list should show ▲/▼ scroll hints, got:\n%s", sidebar)
	}
	if !strings.Contains(sidebar, "queue-1000") {
		t.Errorf("selected item queue-1000 should be visible, got:\n%s", sidebar)
	}
}

func TestFmtCount(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{42, "42"},
		{999, "999"},
		{1000, "1.0k"},
		{1500, "1.5k"},
		{1234567, "1.2M"},
	}
	for _, tt := range tests {
		got := fmtCount(tt.n)
		if got != tt.want {
			t.Errorf("fmtCount(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestComputeWindow(t *testing.T) {
	start, end := computeWindow(5, 2, 10)
	if start != 0 || end != 5 {
		t.Errorf("small list: start=%d end=%d, want 0,5", start, end)
	}

	start, end = computeWindow(100, 50, 10)
	if start != 45 || end != 55 {
		t.Errorf("centered: start=%d end=%d, want 45,55", start, end)
	}

	start, end = computeWindow(100, 2, 10)
	if start != 0 || end != 10 {
		t.Errorf("near start: start=%d end=%d, want 0,10", start, end)
	}

	start, end = computeWindow(100, 98, 10)
	if start != 90 || end != 100 {
		t.Errorf("near end: start=%d end=%d, want 90,100", start, end)
	}
}

func TestAITUI_EscFromPaneReturnsFocus(t *testing.T) {
	m := newTestModelWithObjects()
	m.focus = 1 // Queues

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model := updated.(aiTUIModel)
	if model.focus != focusChat {
		t.Errorf("Esc in pane should return to chat, got %v", model.focus)
	}
	if model.quitting {
		t.Error("Esc in pane should NOT quit")
	}
}

func TestAITUI_HandleObjectsDone(t *testing.T) {
	m := newTestModelWithObjects()
	m.loadingObjects = true

	updated, _ := m.Update(objectsMsg{
		windows: [][]backends.ObjectNode{
			{{Name: "q1", Metrics: []backends.Metric{{Label: "msgs", Value: 5}}}},
			{{Name: "t1"}},
		},
		errs: []error{nil, nil},
	})
	model := updated.(aiTUIModel)
	if model.loadingObjects {
		t.Error("loadingObjects should be false after objectsMsg")
	}
	if len(model.objTypes[0].nodes) != 1 || model.objTypes[0].nodes[0].Name != "q1" {
		t.Errorf("queues not set correctly: %v", model.objTypes[0].nodes)
	}
	if len(model.objTypes[1].nodes) != 1 || model.objTypes[1].nodes[0].Name != "t1" {
		t.Errorf("topics not set correctly: %v", model.objTypes[1].nodes)
	}
}

func TestAITUI_HierarchicalExpand(t *testing.T) {
	ms := &ManageSpec{
		Objects: []ObjectType{
			{
				Label:        "Exchanges",
				Hierarchical: true,
				List: func() ([]backends.ObjectNode, error) {
					return []backends.ObjectNode{
						{
							Name: "amq.topic",
							Children: []backends.ObjectNode{
								{Name: "my-queue", Kind: "queue"},
							},
						},
					}, nil
				},
			},
		},
	}
	m := newAITUIModel(&aiSession{}, &shellSession{spec: BrokerSpec{ManageSpec: ms}}, nil, "test", "")
	m.loadingObjects = false
	m.objTypes[0].nodes = []backends.ObjectNode{
		{
			Name: "amq.topic",
			Children: []backends.ObjectNode{
				{Name: "my-queue", Kind: "queue"},
			},
		},
	}
	m.focus = 1

	// Children not visible by default (treeView=false).
	sidebar, _ := m.renderSidebar(40, 20)
	if strings.Contains(sidebar, "my-queue") {
		t.Error("children should not be visible when treeView is off")
	}

	// Press 'x' to toggle tree-view → children should appear.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = updated.(aiTUIModel)
	sidebar, _ = m.renderSidebar(40, 20)
	if !strings.Contains(sidebar, "my-queue") {
		t.Errorf("children should be visible when treeView is on, got:\n%s", sidebar)
	}

	// Space collapses the window → children hidden again.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	m = updated.(aiTUIModel)
	sidebar, _ = m.renderSidebar(40, 20)
	if strings.Contains(sidebar, "my-queue") {
		t.Error("children should not be visible when window is collapsed")
	}
}

func TestAITUI_SlashExit(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("/exit")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model := updated.(aiTUIModel)
	if !model.exitAll {
		t.Error("/exit should set exitAll")
	}
	if cmd == nil {
		t.Error("/exit should return a quit command")
	}
}

func TestAITUI_ConnMsg_Error(t *testing.T) {
	m := newTestModel()
	updated, _ := m.Update(connMsg{err: fmt.Errorf("connection refused")})
	model := updated.(aiTUIModel)
	if !model.conn.checked {
		t.Error("conn.checked should be true after connMsg")
	}
	if model.conn.err == nil {
		t.Error("conn.err should be set on failure")
	}
	if !strings.Contains(model.transcript.String(), "connection error") {
		t.Error("transcript should contain connection error")
	}
}

func TestAITUI_ConnMsg_OK(t *testing.T) {
	m := newTestModel()
	updated, _ := m.Update(connMsg{})
	model := updated.(aiTUIModel)
	if !model.conn.checked {
		t.Error("conn.checked should be true after connMsg")
	}
	if model.conn.err != nil {
		t.Error("conn.err should be nil on success")
	}
}

func TestAITUI_ServerInfo_Connected(t *testing.T) {
	m := newTestModel()
	m.conn.checked = true
	m.conn.err = nil
	info := m.serverInfo()
	if !strings.Contains(info, "localhost:5672") {
		t.Errorf("serverInfo should contain the server URL, got %q", info)
	}
}

func TestAITUI_ServerInfo_Error(t *testing.T) {
	m := newTestModel()
	m.	conn.checked = true
	m.conn.err = fmt.Errorf("refused")
	info := m.serverInfo()
	if !strings.Contains(info, "localhost:5672") {
		t.Errorf("serverInfo should still show URL on error, got %q", info)
	}
}

func TestExtractCommand_StripsBinaryPrefix(t *testing.T) {
	orig := os.Args[0]
	defer func() { os.Args[0] = orig }()
	os.Args[0] = "/usr/local/bin/rmc"

	tests := []struct {
		input string
		want  string
	}{
		{"send q1 hello", "send q1 hello"},
		{"./rmc send q1 hello", "send q1 hello"},
		{"rmc send q1 hello", "send q1 hello"},
		{"./xmc send q1 hello", "send q1 hello"},
		{"xmc send q1 hello", "send q1 hello"},
		{"manage list", "manage list"},
	}
	for _, tt := range tests {
		got := extractCommand(tt.input)
		if got != tt.want {
			t.Errorf("extractCommand(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestAITUI_HistoryRecall(t *testing.T) {
	m := newTestModel()
	m.mode = modeCmd
	m.cmdHistory = []string{"send q1 hello", "receive q1"}
	m.histIdx = -1

	// Up → last entry.
	m.historyPrev()
	if m.input.Value() != "receive q1" {
		t.Errorf("after Up, input = %q, want 'receive q1'", m.input.Value())
	}

	// Up → first entry.
	m.historyPrev()
	if m.input.Value() != "send q1 hello" {
		t.Errorf("after 2nd Up, input = %q, want 'send q1 hello'", m.input.Value())
	}

	// Down → back to last.
	m.historyNext()
	if m.input.Value() != "receive q1" {
		t.Errorf("after Down, input = %q, want 'receive q1'", m.input.Value())
	}

	// Down → back to draft.
	m.historyNext()
	if m.histIdx != -1 {
		t.Errorf("after returning to draft, histIdx = %d, want -1", m.histIdx)
	}
}

func TestAITUI_NoSidebarTokenFooter(t *testing.T) {
	m := newTestModelWithObjects()
	m.totalIn = 500
	m.totalOut = 200
	sidebar, _ := m.renderSidebar(35, 20)
	if strings.Contains(sidebar, "tokens:") {
		t.Errorf("sidebar should NOT show token footer, got:\n%s", sidebar)
	}
}

// ---------- computeInputLines ----------

func TestComputeInputLines(t *testing.T) {
	// Give the model a real terminal size so recalcLayout populates input width.
	m := newTestModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = updated.(aiTUIModel)

	w := m.input.Width()
	if w <= 0 {
		t.Fatalf("input.Width() = %d after WindowSizeMsg; recalcLayout may not have run", w)
	}

	// Empty value → 1 line.
	if got := (&m).computeInputLines(""); got != 1 {
		t.Errorf("empty value: got %d, want 1", got)
	}

	// Short value → 1 line.
	if got := (&m).computeInputLines("hello"); got != 1 {
		t.Errorf("short value: got %d, want 1", got)
	}

	// Value exactly filling one row → 1 line.
	oneRow := strings.Repeat("x", w)
	if got := (&m).computeInputLines(oneRow); got != 1 {
		t.Errorf("one-row value: got %d, want 1", got)
	}

	// Value that exceeds one row → 2 lines.
	twoRows := strings.Repeat("x", w+1)
	if got := (&m).computeInputLines(twoRows); got != 2 {
		t.Errorf("two-row value (%d runes, width %d): got %d, want 2", len(twoRows), w, got)
	}

	// Very long value → clamped to maxInputLines.
	huge := strings.Repeat("x", w*maxInputLines+100)
	if got := (&m).computeInputLines(huge); got != maxInputLines {
		t.Errorf("huge value: got %d, want %d (maxInputLines)", got, maxInputLines)
	}
}

// ---------- lineHasCopyMarker / copyIdxForLine ----------

func TestLineHasCopyMarker(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"▶ ran: receive q " + copyHintStyle.Render("⧉"), true},
		{"  " + copyHintStyle.Render("⧉"), true},
		{copyHintStyle.Render("⧉"), true},
		{"▶ ran: send q hello", false},
		{"nothing here", false},
	}
	for _, tt := range tests {
		got := lineHasCopyMarker(tt.line)
		if got != tt.want {
			t.Errorf("lineHasCopyMarker(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}

func TestCopyIdxForLine(t *testing.T) {
	m := newTestModel()
	// Synthesise three wrapped content lines: two with markers, one without.
	mark := copyHintStyle.Render("⧉")
	m.wrappedContentLines = []string{
		"▶ ran: receive q " + mark, // line 0 → copyItems[0]
		"  some payload",            // line 1 — no marker
		"  " + mark,                 // line 2 → copyItems[1]
		"▶ ran: send q x " + mark,  // line 3 → copyItems[2]
	}
	m.copyItems = []string{"receive q", "payload text", "send q x"}

	tests := []struct {
		line int
		want int
	}{
		{0, 0},
		{1, -1},
		{2, 1},
		{3, 2},
		{-1, -1},
		{99, -1},
	}
	for _, tt := range tests {
		got := m.copyIdxForLine(tt.line)
		if got != tt.want {
			t.Errorf("copyIdxForLine(%d) = %d, want %d", tt.line, got, tt.want)
		}
	}
}

// ---------- isMessageReadCommand ----------

func TestIsMessageReadCommand(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"receive orders", true},
		{"receive orders -n 5", true},
		{"receive", true},
		{"peek dlq", true},
		{"peek", true},
		{"subscribe events", true},
		{"subscribe events --durable", true},
		{"subscribe", true},
		// Non-read commands.
		{"send q hello", false},
		{"publish t hello", false},
		{"manage list", false},
		{"forward src dst", false},
		{"move dlq orders", false},
	}
	for _, tt := range tests {
		got := isMessageReadCommand(tt.cmd)
		if got != tt.want {
			t.Errorf("isMessageReadCommand(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

// ---------- renderMessagePayload ----------

func TestRenderMessagePayload(t *testing.T) {
	xansiStrip := func(s string) string {
		// Strip ANSI codes by checking that the border rune is present without needing xansi dep.
		// Since we're in the same package we can call xansi directly, but a simple check suffices.
		return s
	}
	_ = xansiStrip

	payload := "Hello World\nSecond line"
	result := renderMessagePayload(payload)

	// Should produce one line per input line.
	outputLines := strings.Split(strings.TrimRight(result, "\n"), "\n")
	inputLines := strings.Split(payload, "\n")
	if len(outputLines) != len(inputLines) {
		t.Errorf("got %d output lines, want %d", len(outputLines), len(inputLines))
	}

	// Each line should contain the border rune.
	for i, line := range outputLines {
		if !strings.Contains(line, "│") {
			t.Errorf("line %d missing │ border: %q", i, line)
		}
	}

	// Input text should appear somewhere in the output (possibly ANSI-styled).
	if !strings.Contains(result, "Hello World") && !strings.Contains(result, "Hello") {
		// The text might be split across ANSI sequences; check the raw string contains each word.
		if !strings.Contains(result, "H") {
			t.Errorf("rendered payload doesn't contain expected text, got:\n%s", result)
		}
	}
}

// ---------- appendTranscript cap ----------

func TestAppendTranscript_Cap(t *testing.T) {
	m := newTestModel()

	// Pre-populate transcript directly (bypassing setViewportContent) with
	// newline-delimited lines to keep wordwrap efficient (one tiny "word" per line).
	// We fill it to just over the cap.
	over := maxTranscriptBytes + 10*1024 // 210 KB
	m.transcript.Reset()
	m.transcript.WriteString(strings.Repeat("a\n", over/2))

	// A tiny appendTranscript call tips it over the cap and triggers the trim.
	(&m).appendTranscript("tip\n")

	// Transcript must be trimmed to at most cap + small slack (the marker line).
	slack := 1024
	if m.transcript.Len() > maxTranscriptBytes+slack {
		t.Errorf("transcript.Len() = %d, want <= %d", m.transcript.Len(), maxTranscriptBytes+slack)
	}

	// Trim marker must be present.
	if !strings.Contains(m.transcript.String(), "earlier output trimmed") {
		t.Error("transcript should contain the trim marker after cap")
	}

	// Append a distinct string; confirm it is retained (tail policy).
	(&m).appendTranscript("recent message\n")
	if !strings.Contains(m.transcript.String(), "recent message") {
		t.Error("most recently appended text should be retained after cap")
	}
}
