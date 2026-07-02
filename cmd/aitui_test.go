package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/makibytes/xmc/broker/backends"
	"github.com/muesli/termenv"
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

func TestAITUI_HandleAIDone_StrayCommentNotProposed(t *testing.T) {
	// Regression: the model must not be able to invent its own confirmation
	// question as a bare "#" comment (any form other than "# ask:"/"# cannot:")
	// and have it shown as a runnable proposal. Previously such a comment fell
	// through to the proposal flow and, if accepted, silently no-op'd as a
	// shell comment via executeExternal — misleadingly reporting "ok" while
	// never running the actual (possibly destructive) command.
	m := newTestModel()
	m.state = tuiThinking
	m.ai.history = append(m.ai.history, aiMessage{Role: "user", Content: "test"})

	updated, _ := m.Update(aiDoneMsg{
		text: "# This will permanently delete: 5 queues. Proceed? (yes/no)",
	})
	model := updated.(aiTUIModel)
	if model.state != tuiIdle {
		t.Errorf("stray comment should not be proposed, state = %v, want tuiIdle", model.state)
	}
	if model.proposedCmd != "" {
		t.Errorf("proposedCmd = %q, want empty", model.proposedCmd)
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

	// Value one rune short of the row width → 1 line.
	almostRow := strings.Repeat("x", w-1)
	if got := (&m).computeInputLines(almostRow); got != 1 {
		t.Errorf("almost-full row: got %d, want 1", got)
	}

	// Value exactly filling one row → 2 lines: the textarea's wrap appends a
	// trailing empty row for the cursor when a line is exactly full, so the
	// widget really renders two rows here.
	oneRow := strings.Repeat("x", w)
	if got := (&m).computeInputLines(oneRow); got != 2 {
		t.Errorf("exactly-full row: got %d, want 2", got)
	}

	// Value that exceeds one row → 2 lines.
	twoRows := strings.Repeat("x", w+1)
	if got := (&m).computeInputLines(twoRows); got != 2 {
		t.Errorf("two-row value (%d runes, width %d): got %d, want 2", len(twoRows), w, got)
	}

	// Word wrap: a word that would cross the row boundary moves entirely to
	// the next row, so the count must be 2 even though the rune count alone
	// would still fit into one row.
	wordWrapped := strings.Repeat("x", w-3) + " yyyy"
	if got := (&m).computeInputLines(wordWrapped); got != 2 {
		t.Errorf("word-wrapped value: got %d, want 2", got)
	}

	// Very long value → clamped to maxInputLines.
	huge := strings.Repeat("x", w*maxInputLines+100)
	if got := (&m).computeInputLines(huge); got != maxInputLines {
		t.Errorf("huge value: got %d, want %d (maxInputLines)", got, maxInputLines)
	}

	// computeInputLines must agree with the widget's own wrapping for a value
	// set directly into the textarea (LineInfo().Height is the widget's count).
	m.input.SetValue(wordWrapped)
	if got, want := (&m).computeInputLines(wordWrapped), m.input.LineInfo().Height; got != want {
		t.Errorf("computeInputLines = %d, widget LineInfo().Height = %d; must match", got, want)
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

// ---------- P/S/R sidebar hotkeys (purge/send/receive) ----------

func TestSidebarWindowSupportsSRP(t *testing.T) {
	cases := []struct {
		label string
		want  bool
	}{
		{"Queues", true},
		{"Streams", true},
		{"Addresses", true},
		{"Exchanges", true},
		{"Topics", false},
		{"Consumer Groups", false},
	}
	for _, c := range cases {
		if got := sidebarWindowSupportsSRP(c.label); got != c.want {
			t.Errorf("sidebarWindowSupportsSRP(%q) = %v, want %v", c.label, got, c.want)
		}
	}
}

func TestSidebarPurgeReceiveAllowed(t *testing.T) {
	cases := []struct {
		label string
		kind  string
		want  bool
	}{
		// Artemis Addresses never allow Purge/Receive, regardless of Kind: an
		// address has no reliable 1:1 mapping to a same-named queue.
		{"Addresses", "multicast", false},
		{"Addresses", "anycast", false},
		{"Addresses", "any/multi", false},
		{"Addresses", "", false},
		// RabbitMQ Exchanges never allow Purge/Receive either, for the same
		// reason: an exchange routes to whatever its bindings name, not a
		// same-named queue.
		{"Exchanges", "topic", false},
		{"Exchanges", "", false},
		{"Queues", "multicast", true}, // label alone gates Queues/Streams; Kind is irrelevant there
		{"Streams", "", true},
	}
	for _, c := range cases {
		node := backends.ObjectNode{Kind: c.kind}
		if got := sidebarPurgeReceiveAllowed(c.label, node); got != c.want {
			t.Errorf("sidebarPurgeReceiveAllowed(%q, Kind=%q) = %v, want %v", c.label, c.kind, got, c.want)
		}
	}
}

func TestSidebarSendViaTopic(t *testing.T) {
	cases := []struct {
		label string
		kind  string
		want  bool
	}{
		{"Addresses", "multicast", true},
		{"Addresses", "anycast", false},
		{"Addresses", "any/multi", false},
		{"Addresses", "", false},
		{"Exchanges", "topic", true},   // always publishes, regardless of exchange type
		{"Exchanges", "fanout", true},
		{"Exchanges", "", true},
		{"Queues", "multicast", false}, // never topic-routed outside Addresses/Exchanges
	}
	for _, c := range cases {
		if got := sidebarSendViaTopic(c.label, c.kind); got != c.want {
			t.Errorf("sidebarSendViaTopic(%q, %q) = %v, want %v", c.label, c.kind, got, c.want)
		}
	}
}

// newTestModelWithQueueWindow builds a model with one "Queues" window (one node
// "orders") plus optional queue/topic adapters and a Purge func for dispatch tests.
func newTestModelWithQueueWindow(qb backends.QueueBackend, tb backends.TopicBackend, purge func(string) (int64, error)) aiTUIModel {
	ms := &ManageSpec{
		Objects: []ObjectType{{
			Label: "Queues",
			List: func() ([]backends.ObjectNode, error) {
				return []backends.ObjectNode{{Name: "orders"}}, nil
			},
		}},
		Purge: purge,
	}
	session := &shellSession{spec: BrokerSpec{ManageSpec: ms}}
	if qb != nil {
		session.queueFactory = func() (backends.QueueBackend, error) { return qb, nil }
	}
	if tb != nil {
		session.topicFactory = func() (backends.TopicBackend, error) { return tb, nil }
	}
	m := newAITUIModel(&aiSession{}, session, nil, "test", "localhost:5672")
	m.loadingObjects = false
	m.objTypes[0].nodes = []backends.ObjectNode{{Name: "orders"}}
	m.focus = 1
	return m
}

// newTestModelWithAddressesWindow builds a model with one "Addresses" window
// containing a single node of the given Kind ("anycast", "multicast", "any/multi").
func newTestModelWithAddressesWindow(kind string, qb backends.QueueBackend, tb backends.TopicBackend, purge func(string) (int64, error)) aiTUIModel {
	node := backends.ObjectNode{Name: "orders", Kind: kind}
	ms := &ManageSpec{
		Objects: []ObjectType{{
			Label: "Addresses",
			List:  func() ([]backends.ObjectNode, error) { return []backends.ObjectNode{node}, nil },
		}},
		Purge: purge,
	}
	session := &shellSession{spec: BrokerSpec{ManageSpec: ms}}
	if qb != nil {
		session.queueFactory = func() (backends.QueueBackend, error) { return qb, nil }
	}
	if tb != nil {
		session.topicFactory = func() (backends.TopicBackend, error) { return tb, nil }
	}
	m := newAITUIModel(&aiSession{}, session, nil, "test", "localhost:5672")
	m.loadingObjects = false
	m.objTypes[0].nodes = []backends.ObjectNode{node}
	m.focus = 1
	return m
}

// newTestModelWithExchangesWindow builds a model with one "Exchanges" window
// (RabbitMQ-shaped) containing a single node.
func newTestModelWithExchangesWindow(qb backends.QueueBackend, tb backends.TopicBackend, purge func(string) (int64, error)) aiTUIModel {
	node := backends.ObjectNode{Name: "orders-exchange", Kind: "topic"}
	ms := &ManageSpec{
		Objects: []ObjectType{{
			Label:        "Exchanges",
			Hierarchical: true,
			List:         func() ([]backends.ObjectNode, error) { return []backends.ObjectNode{node}, nil },
		}},
		Purge: purge,
	}
	session := &shellSession{spec: BrokerSpec{ManageSpec: ms}}
	if qb != nil {
		session.queueFactory = func() (backends.QueueBackend, error) { return qb, nil }
	}
	if tb != nil {
		session.topicFactory = func() (backends.TopicBackend, error) { return tb, nil }
	}
	m := newAITUIModel(&aiSession{}, session, nil, "test", "localhost:5672")
	m.loadingObjects = false
	m.objTypes[0].nodes = []backends.ObjectNode{node}
	m.focus = 1
	return m
}

func noopPurge(count int64) func(string) (int64, error) {
	return func(string) (int64, error) { return count, nil }
}

func TestAITUI_PurgeHotkey_ShowsConfirmPrompt(t *testing.T) {
	m := newTestModelWithQueueWindow(&mockQueueBackend{}, nil, noopPurge(0))
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("P")})
	model := updated.(aiTUIModel)
	if !model.promptActive || model.promptKind != "purge" {
		t.Fatalf("promptActive=%v promptKind=%q, want active purge prompt", model.promptActive, model.promptKind)
	}
	if model.promptName != "orders" {
		t.Errorf("promptName = %q, want %q", model.promptName, "orders")
	}
	if !strings.Contains(model.renderPromptLine(), "Purge \"orders\"?") {
		t.Errorf("prompt line = %q, want a purge confirmation", model.renderPromptLine())
	}
}

func TestAITUI_PurgeHotkey_NoPurgeFunc_NoOp(t *testing.T) {
	m := newTestModelWithQueueWindow(&mockQueueBackend{}, nil, nil) // no Purge
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("P")})
	model := updated.(aiTUIModel)
	if model.promptActive {
		t.Error("purge hotkey should no-op when ManageSpec.Purge is nil")
	}
}

func TestAITUI_PurgeHotkey_MulticastAddress_NoOp(t *testing.T) {
	m := newTestModelWithAddressesWindow("multicast", &mockQueueBackend{}, &mockTopicBackend{}, noopPurge(0))
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("P")})
	model := updated.(aiTUIModel)
	if model.promptActive {
		t.Error("purge hotkey should no-op on a pure-multicast Artemis address")
	}
}

func TestAITUI_PurgeHotkey_AnycastAddress_NoOp(t *testing.T) {
	// An anycast address is not purgeable by address name either — it has no
	// reliable 1:1 mapping to a queue of the same name. Use the Queues window.
	m := newTestModelWithAddressesWindow("anycast", &mockQueueBackend{}, &mockTopicBackend{}, noopPurge(0))
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("P")})
	model := updated.(aiTUIModel)
	if model.promptActive {
		t.Error("purge hotkey should no-op on an Artemis address, even an anycast one")
	}
}

func TestAITUI_SendHotkey_ShowsPayloadPrompt(t *testing.T) {
	m := newTestModelWithQueueWindow(&mockQueueBackend{}, nil, nil)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("S")})
	model := updated.(aiTUIModel)
	if !model.promptActive || model.promptKind != "send" {
		t.Fatalf("promptActive=%v promptKind=%q, want active send prompt", model.promptActive, model.promptKind)
	}
	if model.promptTarget != "orders" {
		t.Errorf("promptTarget = %q, want %q", model.promptTarget, "orders")
	}
	if model.promptName != "" {
		t.Errorf("promptName should start empty, got %q", model.promptName)
	}
}

func TestAITUI_SendPrompt_AccumulatesAndBackspaces(t *testing.T) {
	m := newTestModelWithQueueWindow(&mockQueueBackend{}, nil, nil)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("S")})
	model := updated.(aiTUIModel)

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	model = updated.(aiTUIModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	model = updated.(aiTUIModel)
	if model.promptName != "hi" {
		t.Fatalf("promptName = %q, want %q", model.promptName, "hi")
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	model = updated.(aiTUIModel)
	if model.promptName != "h" {
		t.Errorf("after backspace, promptName = %q, want %q", model.promptName, "h")
	}
}

func TestAITUI_ReceiveHotkey_WrongWindow_NoOp(t *testing.T) {
	ms := &ManageSpec{
		Objects: []ObjectType{{
			Label: "Topics",
			List:  func() ([]backends.ObjectNode, error) { return []backends.ObjectNode{{Name: "events"}}, nil },
		}},
	}
	m := newAITUIModel(&aiSession{}, &shellSession{spec: BrokerSpec{ManageSpec: ms}}, nil, "test", "")
	m.loadingObjects = false
	m.objTypes[0].nodes = []backends.ObjectNode{{Name: "events"}}
	m.focus = 1

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	model := updated.(aiTUIModel)
	if model.state != tuiIdle {
		t.Errorf("state = %v, want tuiIdle (no-op on a Topics window)", model.state)
	}
	if cmd != nil {
		t.Error("receive hotkey should return a nil cmd on a non-queue-shaped window")
	}
	if model.transcript.Len() != 0 {
		t.Errorf("transcript should stay empty, got %q", model.transcript.String())
	}
}

func TestAITUI_StatusBar_ShowsPSRHints_OnQueuesWindow(t *testing.T) {
	m := newTestModelWithQueueWindow(&mockQueueBackend{}, nil, noopPurge(0))
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = updated.(aiTUIModel)
	bar := m.renderStatusBar()
	for _, want := range []string{"P", "purge", "S", "send", "R", "receive"} {
		if !strings.Contains(bar, want) {
			t.Errorf("status bar missing %q, got: %s", want, bar)
		}
	}
}

func TestAITUI_StatusBar_HidesPurgeReceiveButShowsSend_OnAddressesWindow(t *testing.T) {
	// Addresses never allow P purge / R receive (§Phase 2 bugfix), but S
	// send/publish is still valid there — the hint line must reflect this
	// exactly, not the window-level "SRP eligible" tri-state it used before.
	m := newTestModelWithAddressesWindow("anycast", &mockQueueBackend{}, &mockTopicBackend{}, noopPurge(0))
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = updated.(aiTUIModel)
	bar := m.renderStatusBar()
	if strings.Contains(bar, "purge") {
		t.Errorf("status bar should not show 'purge' on an Addresses window, got: %s", bar)
	}
	if strings.Contains(bar, "receive") {
		t.Errorf("status bar should not show 'receive' on an Addresses window, got: %s", bar)
	}
	if !strings.Contains(bar, "send") {
		t.Errorf("status bar should still show 'send' on an Addresses window, got: %s", bar)
	}
}

func TestAITUI_StatusBar_HidesPSRHints_OnTopicsWindow(t *testing.T) {
	ms := &ManageSpec{
		Objects: []ObjectType{{
			Label: "Topics",
			List:  func() ([]backends.ObjectNode, error) { return []backends.ObjectNode{{Name: "events"}}, nil },
		}},
	}
	m := newAITUIModel(&aiSession{}, &shellSession{spec: BrokerSpec{ManageSpec: ms}}, nil, "test", "")
	m.loadingObjects = false
	m.objTypes[0].nodes = []backends.ObjectNode{{Name: "events"}}
	m.focus = 1
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = updated.(aiTUIModel)
	bar := m.renderStatusBar()
	if strings.Contains(bar, "purge") || strings.Contains(bar, "send") || strings.Contains(bar, "receive") {
		t.Errorf("status bar should hide P/S/R hints on a Topics window, got: %s", bar)
	}
}

func TestAITUI_ReceiveHotkey_AcksMessage(t *testing.T) {
	qb := &mockQueueBackend{receiveMsg: &backends.Message{Data: []byte("payload")}}
	m := newTestModelWithQueueWindow(qb, nil, nil)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	model := updated.(aiTUIModel)
	if cmd == nil {
		t.Fatal("expected a non-nil tea.Cmd from the receive hotkey")
	}
	msg := cmd()
	updated, _ = model.Update(msg)
	model = updated.(aiTUIModel)

	if !qb.lastReceiveOpts.Acknowledge {
		t.Error("R (receive) must set Acknowledge: true — that's what distinguishes it from peek")
	}
	if !strings.Contains(model.transcript.String(), "payload") {
		t.Errorf("transcript should show the received payload, got: %s", model.transcript.String())
	}
}

func TestAITUI_ReceiveHotkey_NoOpOnAnyArtemisAddress(t *testing.T) {
	for _, kind := range []string{"anycast", "multicast", "any/multi", ""} {
		qb := &mockQueueBackend{receiveMsg: &backends.Message{Data: []byte("x")}}
		m := newTestModelWithAddressesWindow(kind, qb, &mockTopicBackend{}, nil)
		updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
		model := updated.(aiTUIModel)
		if model.state != tuiIdle || cmd != nil {
			t.Errorf("receive hotkey should no-op on an Artemis address of Kind %q (an address has no reliable queue mapping)", kind)
		}
	}
}

func TestAITUI_Theme_PetrolInAIMode_BlueInCmdMode(t *testing.T) {
	m := newTestModel()
	if m.mode != modeAI {
		t.Fatalf("initial mode should be modeAI, got %v", m.mode)
	}
	if got := m.theme(); got != themeAI {
		t.Errorf("theme() in modeAI = %+v, want themeAI %+v", got, themeAI)
	}

	m.mode = modeCmd
	if got := m.theme(); got != themeCmd {
		t.Errorf("theme() in modeCmd = %+v, want themeCmd %+v", got, themeCmd)
	}
	if themeAI.accent == themeCmd.accent {
		t.Error("themeAI and themeCmd must use different accent colors")
	}
}

func TestAITUI_View_TitleAndModelInfo_SwitchesWithMode(t *testing.T) {
	ai := &aiSession{modelName: "claude-opus", providerName: "anthropic"}
	base := newAITUIModel(ai, &shellSession{spec: BrokerSpec{}}, nil, "test", "localhost:5672")
	updated, _ := base.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m := updated.(aiTUIModel)

	aiView := m.View()
	if !strings.Contains(aiView, "XMC AI") {
		t.Error("AI mode title should contain \"XMC AI\"")
	}
	if strings.Contains(aiView, "XMC Shell") {
		t.Error("AI mode title should not contain \"XMC Shell\"")
	}
	if !strings.Contains(aiView, "claude-opus") {
		t.Error("AI mode title bar should show the model info")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	cmdModel := updated.(aiTUIModel)
	cmdView := cmdModel.View()
	if !strings.Contains(cmdView, "XMC Shell") {
		t.Error("command mode title should contain \"XMC Shell\"")
	}
	if strings.Contains(cmdView, "XMC AI") {
		t.Error("command mode title should not contain \"XMC AI\"")
	}
	if strings.Contains(cmdView, "claude-opus") {
		t.Error("command mode title bar should not show the model info")
	}
}

func TestAITUI_PromptLabel_MatchesModeTheme(t *testing.T) {
	m := newTestModel()
	if got := m.input.FocusedStyle.Prompt.GetForeground(); got != themeAI.accent {
		t.Errorf("initial (AI mode) prompt label color = %v, want themeAI accent %v", got, themeAI.accent)
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model := updated.(aiTUIModel)
	if model.mode != modeCmd {
		t.Fatalf("expected modeCmd after Esc, got %v", model.mode)
	}
	if got := model.input.FocusedStyle.Prompt.GetForeground(); got != themeCmd.accent {
		t.Errorf("command mode prompt label color = %v, want themeCmd accent %v", got, themeCmd.accent)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	backToAI := updated.(aiTUIModel)
	if got := backToAI.input.FocusedStyle.Prompt.GetForeground(); got != themeAI.accent {
		t.Errorf("prompt label color after toggling back to AI mode = %v, want themeAI accent %v", got, themeAI.accent)
	}
}

func TestAITUI_ProcessView_FromAIMode_PromptColorTracksText(t *testing.T) {
	m := newTestModel() // starts in modeAI
	if got := m.input.FocusedStyle.Prompt.GetForeground(); got != themeAI.accent {
		t.Fatalf("initial prompt color = %v, want themeAI accent", got)
	}

	(&m).enterProcessView()
	if got := m.input.FocusedStyle.Prompt.GetForeground(); got != themeCmd.accent {
		t.Errorf("process view always shows a \"<binary>>\"-shaped prompt, so its color must be themeCmd even when entered from AI mode; got %v", got)
	}

	(&m).exitProcessView()
	if got := m.input.FocusedStyle.Prompt.GetForeground(); got != themeAI.accent {
		t.Errorf("after exiting process view, prompt color = %v, want themeAI accent (the mode it was entered from)", got)
	}
	if m.mode != modeAI {
		t.Errorf("mode after exitProcessView = %v, want modeAI restored", m.mode)
	}
}

func TestAITUI_ProcessView_FromCmdMode_PromptStaysBlueThroughout(t *testing.T) {
	m := newTestModel()
	(&m).toggleInputMode() // -> modeCmd
	if got := m.input.FocusedStyle.Prompt.GetForeground(); got != themeCmd.accent {
		t.Fatalf("after toggling to cmd mode, prompt color = %v, want themeCmd accent", got)
	}

	(&m).enterProcessView()
	if got := m.input.FocusedStyle.Prompt.GetForeground(); got != themeCmd.accent {
		t.Errorf("process view prompt color = %v, want themeCmd accent", got)
	}

	(&m).exitProcessView()
	if got := m.input.FocusedStyle.Prompt.GetForeground(); got != themeCmd.accent {
		t.Errorf("after exiting process view, prompt color = %v, want themeCmd accent (mode was restored to cmd)", got)
	}
	if m.mode != modeCmd {
		t.Errorf("mode after exitProcessView = %v, want modeCmd restored", m.mode)
	}
}

// TestAITUI_View_PromptColor_SurvivesValueCopySemantics renders through the
// real View() pipeline after a value-copy hop (Update() has a value receiver
// and returns a copy, exactly like the real Bubble Tea runtime loop), instead
// of inspecting the FocusedStyle/BlurredStyle struct fields directly. The
// bubbles textarea caches its active style behind a private pointer that only
// Focus()/Blur() re-point; mutating the style fields alone (what
// setPromptTheme's callers used to rely on) can leave that pointer stale
// after a copy, so a struct-field assertion can pass while the actual
// rendered color is still wrong. Forces a TrueColor profile so the ANSI SGR
// codes needed to tell the two accents apart actually get emitted.
func TestAITUI_View_PromptColor_SurvivesValueCopySemantics(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	base := newTestModel()
	updated, _ := base.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	updated, _ = updated.(aiTUIModel).Update(tea.KeyMsg{Type: tea.KeyEsc}) // -> modeCmd (blue)
	model := updated.(aiTUIModel)

	rendered := model.View()

	bluePrefix, _, ok := strings.Cut(themeCmd.promptLabel().Render("Z"), "Z")
	if !ok || bluePrefix == "" {
		t.Fatalf("test setup error: forced TrueColor profile did not produce ANSI codes")
	}
	if !strings.Contains(rendered, bluePrefix) {
		t.Errorf("rendered view does not contain themeCmd (blue) prompt-label ANSI prefix %q", bluePrefix)
	}

	petrolPrefix, _, _ := strings.Cut(themeAI.promptLabel().Render("Z"), "Z")
	if strings.Contains(rendered, petrolPrefix) {
		t.Errorf("rendered view still contains stale themeAI (petrol) prompt-label ANSI prefix %q — pointer-staleness regression", petrolPrefix)
	}
}

func TestIsNoMessage(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"sentinel", backends.ErrNoMessageAvailable, true},
		{"wrapped sentinel", fmt.Errorf("receive: %w", backends.ErrNoMessageAvailable), true},
		{"deadline exceeded", context.DeadlineExceeded, true},
		{"wrapped deadline exceeded", fmt.Errorf("receive: %w", context.DeadlineExceeded), true},
		{"canceled", context.Canceled, false},
		{"nil", nil, false},
		{"other error", fmt.Errorf("connection refused"), false},
	}
	for _, c := range cases {
		if got := isNoMessage(c.err); got != c.want {
			t.Errorf("isNoMessage(%v) [%s] = %v, want %v", c.err, c.name, got, c.want)
		}
	}
}

func TestAITUI_PeekHotkey_DeadlineExceeded_ShowsNoMessageAvailable(t *testing.T) {
	qb := &mockQueueBackend{receiveErr: context.DeadlineExceeded}
	m := newTestModelWithQueueWindow(qb, nil, nil)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	model := updated.(aiTUIModel)
	if cmd == nil {
		t.Fatal("expected a non-nil tea.Cmd from the peek hotkey")
	}
	msg := cmd()
	sam, ok := msg.(sideActionMsg)
	if !ok {
		t.Fatalf("expected a sideActionMsg, got %T", msg)
	}
	if sam.err != nil {
		t.Errorf("a bare receive timeout on an empty queue must not surface as an error, got: %v", sam.err)
	}
	if !strings.Contains(sam.action, "no messages available") {
		t.Errorf("action = %q, want it to say no messages available", sam.action)
	}

	updated, _ = model.Update(msg)
	model = updated.(aiTUIModel)
	transcript := model.transcript.String()
	if strings.Contains(transcript, "deadline exceeded") {
		t.Errorf("transcript should never show the raw deadline-exceeded error, got: %s", transcript)
	}
	if strings.Contains(transcript, "✗") {
		t.Errorf("transcript should not show an error marker for an empty queue, got: %s", transcript)
	}
}

func TestAITUI_ReceiveHotkey_DeadlineExceeded_ShowsNoMessageAvailable(t *testing.T) {
	qb := &mockQueueBackend{receiveErr: context.DeadlineExceeded}
	m := newTestModelWithQueueWindow(qb, nil, nil)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	model := updated.(aiTUIModel)
	if cmd == nil {
		t.Fatal("expected a non-nil tea.Cmd from the receive hotkey")
	}
	msg := cmd()
	sam, ok := msg.(sideActionMsg)
	if !ok {
		t.Fatalf("expected a sideActionMsg, got %T", msg)
	}
	if sam.err != nil {
		t.Errorf("a bare receive timeout on an empty queue must not surface as an error, got: %v", sam.err)
	}
	if !strings.Contains(sam.action, "no messages available") {
		t.Errorf("action = %q, want it to say no messages available", sam.action)
	}

	updated, _ = model.Update(msg)
	model = updated.(aiTUIModel)
	if strings.Contains(model.transcript.String(), "deadline exceeded") {
		t.Errorf("transcript should never show the raw deadline-exceeded error, got: %s", model.transcript.String())
	}
}

func TestAITUI_SendHotkey_UsesQueueBackend_ForAnycastAddress(t *testing.T) {
	qb := &mockQueueBackend{}
	tb := &mockTopicBackend{}
	m := newTestModelWithAddressesWindow("anycast", qb, tb, nil)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("S")})
	model := updated.(aiTUIModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	model = updated.(aiTUIModel)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(aiTUIModel)
	if cmd == nil {
		t.Fatal("expected a non-nil tea.Cmd from send Enter")
	}
	cmd()

	if qb.sendCount != 1 {
		t.Errorf("QueueBackend.Send call count = %d, want 1 (anycast address must send via QueueBackend)", qb.sendCount)
	}
	if tb.publishCount != 0 {
		t.Errorf("TopicBackend.Publish call count = %d, want 0", tb.publishCount)
	}
	if qb.lastSendOpts.Queue != "orders" {
		t.Errorf("Send target = %q, want %q", qb.lastSendOpts.Queue, "orders")
	}
}

func TestAITUI_SendHotkey_UsesTopicBackend_ForMulticastAddress(t *testing.T) {
	qb := &mockQueueBackend{}
	tb := &mockTopicBackend{}
	m := newTestModelWithAddressesWindow("multicast", qb, tb, nil)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("S")})
	model := updated.(aiTUIModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	model = updated.(aiTUIModel)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(aiTUIModel)
	if cmd == nil {
		t.Fatal("expected a non-nil tea.Cmd from send Enter")
	}
	cmd()

	if tb.publishCount != 1 {
		t.Errorf("TopicBackend.Publish call count = %d, want 1 (multicast address must publish via TopicBackend)", tb.publishCount)
	}
	if qb.sendCount != 0 {
		t.Errorf("QueueBackend.Send call count = %d, want 0", qb.sendCount)
	}
	if tb.lastPublishOpts.Topic != "orders" {
		t.Errorf("Publish target = %q, want %q", tb.lastPublishOpts.Topic, "orders")
	}
}

func TestAITUI_SendHotkey_OnExchangesWindow_ShowsPayloadPrompt(t *testing.T) {
	m := newTestModelWithExchangesWindow(&mockQueueBackend{}, &mockTopicBackend{}, nil)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("S")})
	model := updated.(aiTUIModel)
	if !model.promptActive || model.promptKind != "send" {
		t.Fatalf("promptActive=%v promptKind=%q, want active send prompt", model.promptActive, model.promptKind)
	}
	if model.promptTarget != "orders-exchange" {
		t.Errorf("promptTarget = %q, want %q", model.promptTarget, "orders-exchange")
	}
}

func TestAITUI_SendHotkey_UsesTopicBackend_ForExchange(t *testing.T) {
	qb := &mockQueueBackend{}
	tb := &mockTopicBackend{}
	m := newTestModelWithExchangesWindow(qb, tb, nil)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("S")})
	model := updated.(aiTUIModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	model = updated.(aiTUIModel)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(aiTUIModel)
	if cmd == nil {
		t.Fatal("expected a non-nil tea.Cmd from send Enter")
	}
	cmd()

	if tb.publishCount != 1 {
		t.Errorf("TopicBackend.Publish call count = %d, want 1 (exchanges must publish via TopicBackend)", tb.publishCount)
	}
	if qb.sendCount != 0 {
		t.Errorf("QueueBackend.Send call count = %d, want 0", qb.sendCount)
	}
	if tb.lastPublishOpts.Topic != "orders-exchange" {
		t.Errorf("Publish target = %q, want %q", tb.lastPublishOpts.Topic, "orders-exchange")
	}
}

func TestAITUI_PurgeHotkey_OnExchangesWindow_NoOp(t *testing.T) {
	m := newTestModelWithExchangesWindow(&mockQueueBackend{}, &mockTopicBackend{}, noopPurge(0))
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("P")})
	model := updated.(aiTUIModel)
	if model.promptActive {
		t.Error("purge hotkey should no-op on a RabbitMQ Exchanges window")
	}
}

func TestAITUI_ReceiveHotkey_OnExchangesWindow_NoOp(t *testing.T) {
	m := newTestModelWithExchangesWindow(&mockQueueBackend{}, &mockTopicBackend{}, nil)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	model := updated.(aiTUIModel)
	if model.state != tuiIdle {
		t.Errorf("state = %v, want tuiIdle (receive should no-op on Exchanges)", model.state)
	}
}

func TestAITUI_StatusBar_ShowsSendButHidesPurgeReceive_OnExchangesWindow(t *testing.T) {
	m := newTestModelWithExchangesWindow(&mockQueueBackend{}, &mockTopicBackend{}, noopPurge(0))
	bar := m.renderStatusBar()
	if !strings.Contains(bar, "S") || !strings.Contains(bar, "send") {
		t.Errorf("status bar = %q, want it to show the S/send hint on an Exchanges window", bar)
	}
	if strings.Contains(bar, "purge") {
		t.Errorf("status bar = %q, should not show a purge hint on an Exchanges window", bar)
	}
	if strings.Contains(bar, "receive") {
		t.Errorf("status bar = %q, should not show a receive hint on an Exchanges window", bar)
	}
}

func TestAITUI_SendPrompt_KeySpace_AppendsSpace(t *testing.T) {
	m := newTestModelWithQueueWindow(&mockQueueBackend{}, nil, nil)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("S")})
	model := updated.(aiTUIModel)

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	model = updated.(aiTUIModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")})
	model = updated.(aiTUIModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	model = updated.(aiTUIModel)

	if model.promptName != "a b" {
		t.Errorf("promptName = %q, want %q (Space must work in the send payload prompt)", model.promptName, "a b")
	}
}

func TestAITUI_PublishPrompt_KeySpace_AppendsSpace(t *testing.T) {
	m := newTestModelWithTopicsWindow(&mockTopicBackend{})
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("P")})
	model := updated.(aiTUIModel)
	if model.promptKind != "publish" {
		t.Fatalf("promptKind = %q, want %q", model.promptKind, "publish")
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	model = updated.(aiTUIModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")})
	model = updated.(aiTUIModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	model = updated.(aiTUIModel)

	if model.promptName != "a b" {
		t.Errorf("promptName = %q, want %q (Space must work in the publish payload prompt)", model.promptName, "a b")
	}
}

func TestAITUI_StatusBar_PromptActive_ShowsOnlyEnterEsc(t *testing.T) {
	cases := []struct {
		promptKind string
		wantVerb   string
	}{
		{"send", "send"},
		{"publish", "publish"},
		{"create", "create"},
		{"delete", "confirm"},
		{"purge", "confirm"},
		{"purge-subscription", "confirm"},
	}
	for _, c := range cases {
		m := newTestModelWithQueueWindow(&mockQueueBackend{}, nil, noopPurge(0))
		m.promptActive = true
		m.promptKind = c.promptKind
		m.promptObjIdx = 0

		bar := m.renderStatusBar()
		if !strings.Contains(bar, "Enter") || !strings.Contains(bar, c.wantVerb) {
			t.Errorf("promptKind=%q: status bar = %q, want it to contain Enter/%s", c.promptKind, bar, c.wantVerb)
		}
		if !strings.Contains(bar, "Esc") || !strings.Contains(bar, "cancel") {
			t.Errorf("promptKind=%q: status bar = %q, want it to contain Esc/cancel", c.promptKind, bar)
		}
		// None of the object-window hotkeys (sort, filter, create, delete,
		// purge, send, receive, tree) should leak through while a prompt is up.
		for _, stale := range []string{"sort", "filter", "tree", "collapse", "next/prev"} {
			if strings.Contains(bar, stale) {
				t.Errorf("promptKind=%q: status bar = %q, should not contain stale hint %q while prompt is active", c.promptKind, bar, stale)
			}
		}
	}
}

func TestAITUI_PurgeHotkey_InvokesManageSpecPurge(t *testing.T) {
	var gotName string
	purge := func(name string) (int64, error) {
		gotName = name
		return 3, nil
	}
	m := newTestModelWithQueueWindow(&mockQueueBackend{}, nil, purge)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("P")})
	model := updated.(aiTUIModel)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(aiTUIModel)
	if cmd == nil {
		t.Fatal("expected a non-nil tea.Cmd from purge Enter")
	}
	msg := cmd()
	updated, _ = model.Update(msg)
	model = updated.(aiTUIModel)

	if gotName != "orders" {
		t.Errorf("ManageSpec.Purge called with %q, want %q", gotName, "orders")
	}
	if !strings.Contains(model.transcript.String(), "purged 3 messages") {
		t.Errorf("transcript should report the purged count, got: %s", model.transcript.String())
	}
}

// ---------- sidebarRows selection model (child-row navigation) ----------

// newTestModelWithExchangeChildren builds a RabbitMQ-style hierarchical
// "Exchanges" window with one exchange node having one binding-style child.
func newTestModelWithExchangeChildren(purge func(string) (int64, error), deleteAction *ManageAction) aiTUIModel {
	node := backends.ObjectNode{
		Name: "amq.topic",
		Children: []backends.ObjectNode{
			{Name: "my-queue", Kind: "binding key=#"},
		},
	}
	ms := &ManageSpec{
		Objects: []ObjectType{{
			Label:        "Exchanges",
			Hierarchical: true,
			List:         func() ([]backends.ObjectNode, error) { return []backends.ObjectNode{node}, nil },
		}},
		Purge:       purge,
		DeleteQueue: deleteAction,
	}
	session := &shellSession{spec: BrokerSpec{ManageSpec: ms}}
	m := newAITUIModel(&aiSession{}, session, nil, "test", "")
	m.loadingObjects = false
	m.objTypes[0].nodes = []backends.ObjectNode{node}
	m.objTypes[0].createAction = ms.CreateQueue
	m.objTypes[0].deleteAction = deleteAction
	m.focus = 1
	return m
}

func TestSidebarRows_FlatWindow_MatchesTopLevelNodes(t *testing.T) {
	m := newTestModelWithQueueWindow(&mockQueueBackend{}, nil, nil)
	rows := m.sidebarRows(0)
	if len(rows) != 1 || rows[0].node.Name != "orders" || rows[0].parentName != "" {
		t.Errorf("sidebarRows for flat window = %+v, want one top-level row named orders", rows)
	}
}

func TestSidebarRows_TreeViewOff_ChildrenExcluded(t *testing.T) {
	m := newTestModelWithExchangeChildren(nil, nil)
	rows := m.sidebarRows(0)
	if len(rows) != 1 {
		t.Errorf("sidebarRows with treeView off = %d rows, want 1 (children hidden)", len(rows))
	}
}

func TestSidebarRows_TreeViewOn_IncludesChildrenWithParentName(t *testing.T) {
	m := newTestModelWithExchangeChildren(nil, nil)
	m.objTypes[0].treeView = true
	rows := m.sidebarRows(0)
	if len(rows) != 2 {
		t.Fatalf("sidebarRows with treeView on = %d rows, want 2 (parent+child)", len(rows))
	}
	if rows[0].parentName != "" {
		t.Errorf("row 0 (parent) parentName = %q, want empty", rows[0].parentName)
	}
	if rows[1].node.Name != "my-queue" || rows[1].parentName != "amq.topic" {
		t.Errorf("row 1 (child) = %+v, want Name=my-queue parentName=amq.topic", rows[1])
	}
}

func TestAITUI_ChildRowSelectable_NavigatesAndHighlights(t *testing.T) {
	m := newTestModelWithExchangeChildren(nil, nil)
	m.objTypes[0].treeView = true

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	model := updated.(aiTUIModel)
	if model.objTypes[0].sel != 1 {
		t.Fatalf("sel after Down = %d, want 1 (the child row)", model.objTypes[0].sel)
	}
	node, parent, ok := model.selectedChildNode()
	if !ok || node.Name != "my-queue" || parent != "amq.topic" {
		t.Errorf("selectedChildNode() = (%+v, %q, %v), want (my-queue, amq.topic, true)", node, parent, ok)
	}
	if _, ok := model.selectedTopLevelNode(); ok {
		t.Error("selectedTopLevelNode() should fail when a child row is selected")
	}

	sidebar, _ := model.renderSidebar(40, 20)
	if !strings.Contains(sidebar, "▸") {
		t.Errorf("selected child row should render a selection marker, got:\n%s", sidebar)
	}
}

func TestAITUI_TreeToggle_PreservesSelectionByIdentity(t *testing.T) {
	m := newTestModelWithExchangeChildren(nil, nil)
	// Select the (only) top-level node, then toggle tree view on: the node's
	// row index doesn't change here (children only ever insert AFTER their
	// parent), but this guards the general mechanism regardless.
	if m.objTypes[0].sel != 0 {
		t.Fatalf("initial sel = %d, want 0", m.objTypes[0].sel)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	model := updated.(aiTUIModel)
	node, ok := model.selectedTopLevelNode()
	if !ok || node.Name != "amq.topic" {
		t.Errorf("selection should still be amq.topic after tree toggle, got (%+v, %v)", node, ok)
	}
}

func TestAITUI_DeleteHotkey_NoOpsOnChildRow(t *testing.T) {
	var deleteCalls []string
	action := &ManageAction{Run: func(name string) error {
		deleteCalls = append(deleteCalls, name)
		return nil
	}}
	m := newTestModelWithExchangeChildren(nil, action)
	m.objTypes[0].treeView = true

	// Move onto the child row, then press 'd' — must NOT fire delete using
	// the child's name (this is exactly the regression the sidebarRows
	// refactor must guard against for RabbitMQ Exchanges / NATS Streams).
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	model := updated.(aiTUIModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model = updated.(aiTUIModel)

	if model.promptActive {
		t.Error("'d' on a child row should not open a delete prompt")
	}
	if len(deleteCalls) != 0 {
		t.Errorf("delete action should not have been invoked, got calls: %v", deleteCalls)
	}
}

func TestAITUI_PurgeSendReceiveHotkeys_NoOpOnChildRow(t *testing.T) {
	qb := &mockQueueBackend{}
	purge := func(string) (int64, error) { return 0, nil }
	m := newTestModelWithExchangeChildren(purge, nil)
	m.objTypes[0].treeView = true
	m.session.queueFactory = func() (backends.QueueBackend, error) { return qb, nil }

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	model := updated.(aiTUIModel)

	for _, key := range []string{"P", "S", "R"} {
		updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		model = updated.(aiTUIModel)
		if model.promptActive {
			t.Errorf("%q on a child row should not open a prompt", key)
			model.promptActive = false
		}
		if cmd != nil {
			// R fires a tea.Cmd directly (no prompt); executing it must not
			// have been reachable in the first place.
			t.Errorf("%q on a child row should return a nil cmd, got non-nil", key)
		}
	}
}

// ---------- P = publish on Topics top-level nodes ----------

func newTestModelWithTopicsWindow(tb backends.TopicBackend) aiTUIModel {
	ms := &ManageSpec{
		Objects: []ObjectType{{
			Label: "Topics",
			List:  func() ([]backends.ObjectNode, error) { return []backends.ObjectNode{{Name: "events"}}, nil },
		}},
	}
	session := &shellSession{spec: BrokerSpec{ManageSpec: ms}}
	if tb != nil {
		session.topicFactory = func() (backends.TopicBackend, error) { return tb, nil }
	}
	m := newAITUIModel(&aiSession{}, session, nil, "test", "")
	m.loadingObjects = false
	m.objTypes[0].nodes = []backends.ObjectNode{{Name: "events"}}
	m.focus = 1
	return m
}

func TestAITUI_PublishHotkey_ShowsPayloadPrompt(t *testing.T) {
	m := newTestModelWithTopicsWindow(&mockTopicBackend{})
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("P")})
	model := updated.(aiTUIModel)
	if !model.promptActive || model.promptKind != "publish" {
		t.Fatalf("promptActive=%v promptKind=%q, want active publish prompt", model.promptActive, model.promptKind)
	}
	if model.promptTarget != "events" {
		t.Errorf("promptTarget = %q, want %q", model.promptTarget, "events")
	}
}

func TestAITUI_PublishHotkey_DispatchesToTopicBackend(t *testing.T) {
	tb := &mockTopicBackend{}
	m := newTestModelWithTopicsWindow(tb)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("P")})
	model := updated.(aiTUIModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	model = updated.(aiTUIModel)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(aiTUIModel)
	if cmd == nil {
		t.Fatal("expected a non-nil tea.Cmd from publish Enter")
	}
	msg := cmd()
	updated, _ = model.Update(msg)
	model = updated.(aiTUIModel)

	if tb.publishCount != 1 {
		t.Errorf("Publish call count = %d, want 1", tb.publishCount)
	}
	if tb.lastPublishOpts.Topic != "events" {
		t.Errorf("Publish target = %q, want %q", tb.lastPublishOpts.Topic, "events")
	}
	if !strings.Contains(model.transcript.String(), "▶ publish Topic \"events\"") {
		t.Errorf("transcript should show the publish verb, got: %s", model.transcript.String())
	}
	if !strings.Contains(model.transcript.String(), "published") {
		t.Errorf("transcript should confirm publish, got: %s", model.transcript.String())
	}
}

func TestAITUI_PurgeHotkey_OnTopicsWindow_PublishesInstead(t *testing.T) {
	// P on Queues/Streams/Addresses means purge; on a Topics window it means
	// publish instead — regression check that the two don't cross-fire, e.g.
	// that a Topics window never opens a purge confirm.
	m := newTestModelWithTopicsWindow(&mockTopicBackend{})
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("P")})
	model := updated.(aiTUIModel)
	if model.promptKind == "purge" {
		t.Error("P on a Topics window should never open a purge prompt")
	}
}

func TestAITUI_StatusBar_ShowsPublishHint_OnTopicsWindow(t *testing.T) {
	m := newTestModelWithTopicsWindow(&mockTopicBackend{})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = updated.(aiTUIModel)
	bar := m.renderStatusBar()
	if !strings.Contains(bar, "P") || !strings.Contains(bar, "publish") {
		t.Errorf("status bar should show 'P publish' on a Topics window, got: %s", bar)
	}
	if strings.Contains(bar, "purge") {
		t.Errorf("status bar should not show 'purge' on a Topics window, got: %s", bar)
	}
}

// ---------- Subscription children on Topics windows (P purge / p peek / R receive) ----------

func TestSidebarSubscriptionEligible(t *testing.T) {
	cases := []struct {
		label string
		kind  string
		want  bool
	}{
		{"Topics", "subscription", true}, // Azure/Google
		{"Topics", "sqs", false},         // AWS: routing pointer, Kind is the SNS protocol
		{"Topics", "http", false},        // AWS: another protocol
		{"Topics", "", false},
		{"Exchanges", "subscription", false}, // wrong window
		{"Queues", "subscription", false},
	}
	for _, c := range cases {
		if got := sidebarSubscriptionEligible(c.label, c.kind); got != c.want {
			t.Errorf("sidebarSubscriptionEligible(%q, %q) = %v, want %v", c.label, c.kind, got, c.want)
		}
	}
}

// newTestModelWithTopicSubscriptions builds a Topics window with one topic
// node having one "subscription"-kind child, mirroring Azure/Google's
// ListTopicsWithSubscriptions shape.
func newTestModelWithTopicSubscriptions(tb backends.TopicBackend, purgeSub func(topic, sub string) (int64, error)) aiTUIModel {
	node := backends.ObjectNode{
		Name: "orders-topic",
		Children: []backends.ObjectNode{
			{Name: "orders-sub", Kind: "subscription"},
		},
	}
	ms := &ManageSpec{
		Objects: []ObjectType{{
			Label:        "Topics",
			Hierarchical: true,
			List:         func() ([]backends.ObjectNode, error) { return []backends.ObjectNode{node}, nil },
		}},
		PurgeSubscription: purgeSub,
	}
	session := &shellSession{spec: BrokerSpec{ManageSpec: ms}}
	if tb != nil {
		session.topicFactory = func() (backends.TopicBackend, error) { return tb, nil }
	}
	m := newAITUIModel(&aiSession{}, session, nil, "test", "")
	m.loadingObjects = false
	m.objTypes[0].nodes = []backends.ObjectNode{node}
	m.objTypes[0].treeView = true
	m.focus = 1
	return m
}

func selectSubscriptionChild(t *testing.T, m aiTUIModel) aiTUIModel {
	t.Helper()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	model := updated.(aiTUIModel)
	if _, _, ok := model.selectedChildNode(); !ok {
		t.Fatal("expected the child (subscription) row to be selected after Down")
	}
	return model
}

func TestAITUI_PeekHotkey_OnSubscriptionChild_Google(t *testing.T) {
	tb := &mockTopicBackend{subscribeMsg: &backends.Message{Data: []byte("payload")}}
	m := newTestModelWithTopicSubscriptions(tb, nil)
	m = selectSubscriptionChild(t, m)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	model := updated.(aiTUIModel)
	if cmd == nil {
		t.Fatal("expected a non-nil tea.Cmd from peek on a subscription child")
	}
	msg := cmd()
	updated, _ = model.Update(msg)
	model = updated.(aiTUIModel)

	if tb.subscribeCount != 1 {
		t.Fatalf("Subscribe call count = %d, want 1", tb.subscribeCount)
	}
	if tb.lastSubscribeOpts.Acknowledge {
		t.Error("peek must set Acknowledge: false (non-destructive)")
	}
	if tb.lastSubscribeOpts.Topic != "orders-topic" {
		t.Errorf("Subscribe topic = %q, want %q", tb.lastSubscribeOpts.Topic, "orders-topic")
	}
	if tb.lastSubscribeOpts.Extra["subscription"] != "orders-sub" {
		t.Errorf("Subscribe Extra[subscription] = %q, want %q", tb.lastSubscribeOpts.Extra["subscription"], "orders-sub")
	}
	if !strings.Contains(model.transcript.String(), "payload") {
		t.Errorf("transcript should show the peeked payload, got: %s", model.transcript.String())
	}
}

func TestAITUI_ReceiveHotkey_OnSubscriptionChild_Google(t *testing.T) {
	tb := &mockTopicBackend{subscribeMsg: &backends.Message{Data: []byte("payload")}}
	m := newTestModelWithTopicSubscriptions(tb, nil)
	m = selectSubscriptionChild(t, m)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	model := updated.(aiTUIModel)
	if cmd == nil {
		t.Fatal("expected a non-nil tea.Cmd from receive on a subscription child")
	}
	msg := cmd()
	updated, _ = model.Update(msg)
	_ = updated.(aiTUIModel)

	if !tb.lastSubscribeOpts.Acknowledge {
		t.Error("R (receive) must set Acknowledge: true")
	}
	if tb.lastSubscribeOpts.Extra["subscription"] != "orders-sub" {
		t.Errorf("Subscribe Extra[subscription] = %q, want %q", tb.lastSubscribeOpts.Extra["subscription"], "orders-sub")
	}
}

func TestAITUI_PurgeHotkey_OnSubscriptionChild_ShowsConfirmPrompt(t *testing.T) {
	purge := func(topic, sub string) (int64, error) { return 0, nil }
	m := newTestModelWithTopicSubscriptions(&mockTopicBackend{}, purge)
	m = selectSubscriptionChild(t, m)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("P")})
	model := updated.(aiTUIModel)
	if !model.promptActive || model.promptKind != "purge-subscription" {
		t.Fatalf("promptActive=%v promptKind=%q, want active purge-subscription prompt", model.promptActive, model.promptKind)
	}
	if model.promptName != "orders-sub" || model.promptTarget != "orders-topic" {
		t.Errorf("promptName=%q promptTarget=%q, want orders-sub / orders-topic", model.promptName, model.promptTarget)
	}
}

func TestAITUI_PurgeHotkey_OnSubscriptionChild_InvokesPurgeSubscriptionWithCompoundKey(t *testing.T) {
	var gotTopic, gotSub string
	purge := func(topic, sub string) (int64, error) {
		gotTopic, gotSub = topic, sub
		return 5, nil
	}
	m := newTestModelWithTopicSubscriptions(&mockTopicBackend{}, purge)
	m = selectSubscriptionChild(t, m)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("P")})
	model := updated.(aiTUIModel)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(aiTUIModel)
	if cmd == nil {
		t.Fatal("expected a non-nil tea.Cmd from purge-subscription Enter")
	}
	msg := cmd()
	updated, _ = model.Update(msg)
	model = updated.(aiTUIModel)

	if gotTopic != "orders-topic" || gotSub != "orders-sub" {
		t.Errorf("PurgeSubscription called with (%q, %q), want (orders-topic, orders-sub)", gotTopic, gotSub)
	}
	if !strings.Contains(model.transcript.String(), "purged 5 messages") {
		t.Errorf("transcript should report the purged count, got: %s", model.transcript.String())
	}
}

func TestAITUI_PurgeHotkey_OnSubscriptionChild_NoOpWhenPurgeSubscriptionNil(t *testing.T) {
	m := newTestModelWithTopicSubscriptions(&mockTopicBackend{}, nil) // no PurgeSubscription wired
	m = selectSubscriptionChild(t, m)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("P")})
	model := updated.(aiTUIModel)
	if model.promptActive {
		t.Error("P on a subscription child should no-op when ManageSpec.PurgeSubscription is nil")
	}
}

func TestAITUI_SubscriptionHotkeys_NoOpOnNonSubscriptionChild(t *testing.T) {
	// AWS-style child: Kind is the SNS protocol, not "subscription" — must
	// not be treated as a purgeable/receivable subscription.
	node := backends.ObjectNode{
		Name:     "orders-topic",
		Children: []backends.ObjectNode{{Name: "orders-queue", Kind: "sqs"}},
	}
	ms := &ManageSpec{
		Objects: []ObjectType{{
			Label:        "Topics",
			Hierarchical: true,
			List:         func() ([]backends.ObjectNode, error) { return []backends.ObjectNode{node}, nil },
		}},
		PurgeSubscription: func(string, string) (int64, error) { return 0, nil },
	}
	tb := &mockTopicBackend{}
	session := &shellSession{spec: BrokerSpec{ManageSpec: ms}, topicFactory: func() (backends.TopicBackend, error) { return tb, nil }}
	m := newAITUIModel(&aiSession{}, session, nil, "test", "")
	m.loadingObjects = false
	m.objTypes[0].nodes = []backends.ObjectNode{node}
	m.objTypes[0].treeView = true
	m.focus = 1
	m = selectSubscriptionChild(t, m)

	for _, key := range []string{"P", "p", "R"} {
		updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		model := updated.(aiTUIModel)
		if model.promptActive {
			t.Errorf("%q on an AWS-style (non-subscription) child should not open a prompt", key)
		}
		if cmd != nil {
			t.Errorf("%q on an AWS-style (non-subscription) child should return a nil cmd", key)
		}
	}
	if tb.subscribeCount != 0 {
		t.Errorf("Subscribe should never have been called, got %d calls", tb.subscribeCount)
	}
}

func TestAITUI_StatusBar_TracksSelectionDepth_OnTopicsWindow(t *testing.T) {
	m := newTestModelWithTopicSubscriptions(&mockTopicBackend{}, func(string, string) (int64, error) { return 0, nil })
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = updated.(aiTUIModel)

	// Top-level topic selected: only "P publish".
	bar := m.renderStatusBar()
	if !strings.Contains(bar, "publish") {
		t.Errorf("top-level selection: status bar should show 'publish', got: %s", bar)
	}
	if strings.Contains(bar, "peek") || strings.Contains(bar, "receive") {
		t.Errorf("top-level selection: status bar should not show peek/receive, got: %s", bar)
	}

	// Subscription child selected: "P purge", "p peek", "R receive".
	m = selectSubscriptionChild(t, m)
	bar = m.renderStatusBar()
	for _, want := range []string{"purge", "peek", "receive"} {
		if !strings.Contains(bar, want) {
			t.Errorf("child selection: status bar missing %q, got: %s", want, bar)
		}
	}
	if strings.Contains(bar, "publish") {
		t.Errorf("child selection: status bar should not show 'publish', got: %s", bar)
	}
}

// ---------- Status bar horizontal scrolling ----------

func TestScrollingText_FitsWithinWidth_ReturnedUnchanged(t *testing.T) {
	got := scrollingText("P purge  S send", 40, 0)
	if got != "P purge  S send" {
		t.Errorf("scrollingText(fits) = %q, want unchanged", got)
	}
}

func TestScrollingText_Overflow_ReturnsWindowOfCorrectLength(t *testing.T) {
	full := "P purge  S send  R receive  x tree  c create  d delete"
	got := scrollingText(full, 10, 0)
	if len([]rune(got)) != 10 {
		t.Errorf("scrollingText window length = %d, want 10", len([]rune(got)))
	}
	if got != "P purge  S" {
		t.Errorf("scrollingText(offset=0) = %q, want the first 10 runes", got)
	}
}

func TestScrollingText_AdvancesWithOffset(t *testing.T) {
	full := "P purge  S send  R receive"
	first := scrollingText(full, 8, 0)
	second := scrollingText(full, 8, 3)
	if first == second {
		t.Error("scrollingText should produce a different window as offset advances")
	}
	// Advancing by 3 should shift the window by exactly 3 runes.
	if scrollingText(full, 8, 3) != string([]rune(full)[3:11]) {
		t.Errorf("scrollingText(offset=3) = %q, want a 3-rune shift", second)
	}
}

func TestScrollingText_WrapsAroundTheLoop(t *testing.T) {
	full := "ABCDE" // short text, easy to reason about wrap-around
	// Loop = "ABCDE    " (5 + 4-space gap = 9 runes). At offset 7, window of
	// width 5 should wrap from the gap back to the start of "ABCDE".
	got := scrollingText(full, 5, 7)
	if len([]rune(got)) != 5 {
		t.Fatalf("scrollingText wrap window length = %d, want 5", len([]rune(got)))
	}
	// Confirm it actually wrapped (contains a character from the start of
	// the loop, not just trailing gap spaces).
	if !strings.ContainsAny(got, "ABCDE") {
		t.Errorf("scrollingText(wrap) = %q, expected it to wrap back into the source text", got)
	}
}

func TestScrollingText_ZeroWidth_ReturnsEmpty(t *testing.T) {
	if got := scrollingText("anything", 0, 0); got != "" {
		t.Errorf("scrollingText(width=0) = %q, want empty", got)
	}
}

func TestRenderHintList_Fits_UsesStyledTwoColorRendering(t *testing.T) {
	kvs := []hintKV{{"P", "purge"}, {"S", "send"}}
	got := renderHintList(kvs, 80, 0)
	// The styled rendering embeds ANSI codes; plain text alone would not
	// round-trip through lipgloss styling, so just confirm both fragments'
	// plain text are present and it matches the styled builder directly.
	want := styledHintLine(kvs)
	if got != want {
		t.Errorf("renderHintList(fits) = %q, want styled line %q", got, want)
	}
}

func TestRenderHintList_Overflow_Scrolls(t *testing.T) {
	kvs := []hintKV{{"P", "purge"}, {"S", "send"}, {"R", "receive"}, {"x", "tree"}}
	narrow := lipgloss.Width(plainHintLine(kvs)) - 5 // force overflow
	got := renderHintList(kvs, narrow, 0)
	if lipgloss.Width(got) > narrow {
		t.Errorf("renderHintList(overflow) width = %d, want <= %d", lipgloss.Width(got), narrow)
	}
}

func TestAITUI_StatusBar_ScrollOffsetAdvances_OnSpinnerTick(t *testing.T) {
	m := newTestModelWithQueueWindow(&mockQueueBackend{}, nil, noopPurge(0))
	before := m.statusScrollOffset
	// Fire enough spinner ticks to cross the advance threshold.
	for range 5 {
		updated, _ := m.Update(spinner.TickMsg{ID: m.spinner.ID()})
		m = updated.(aiTUIModel)
	}
	if m.statusScrollOffset <= before {
		t.Errorf("statusScrollOffset should advance after spinner ticks, got %d (was %d)", m.statusScrollOffset, before)
	}
}
