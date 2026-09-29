package cmd

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/makibytes/xmc/broker/backends"
)

func press(t *testing.T, m aiTUIModel, k tea.KeyType) aiTUIModel {
	t.Helper()
	updated, _ := m.Update(tea.KeyMsg{Type: k})
	return updated.(aiTUIModel)
}

// ---------- Ctrl+C / Ctrl+D / Ctrl+L ----------

func TestAITUI_CtrlC_ClearsLineBeforeQuitting(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("send orders hel")

	m = press(t, m, tea.KeyCtrlC)
	if m.quitting {
		t.Fatal("Ctrl+C with text in the input must clear it, not quit")
	}
	if m.input.Value() != "" {
		t.Fatalf("input = %q, want cleared", m.input.Value())
	}

	m = press(t, m, tea.KeyCtrlC)
	if !m.quitting {
		t.Error("Ctrl+C on an empty line must quit")
	}
}

func TestAITUI_CtrlD_QuitsOnlyOnEmptyLine(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("abc")
	m = press(t, m, tea.KeyCtrlD)
	if m.quitting {
		t.Fatal("Ctrl+D with text must edit, not quit")
	}
	m.input.SetValue("")
	m = press(t, m, tea.KeyCtrlD)
	if !m.quitting {
		t.Error("Ctrl+D on an empty line must quit")
	}
}

func TestAITUI_CtrlC_InSidebarQuits_InFilterClears(t *testing.T) {
	m := newTestModelWithObjects()
	m.focus = 1
	m.filtering = true
	m.objTypes[0].filter = "pay"

	m = press(t, m, tea.KeyCtrlC)
	if m.quitting || m.filtering || m.objTypes[0].filter != "" {
		t.Fatalf("Ctrl+C in the filter must cancel it (quitting=%v filtering=%v filter=%q)", m.quitting, m.filtering, m.objTypes[0].filter)
	}
	m = press(t, m, tea.KeyCtrlC)
	if !m.quitting {
		t.Error("Ctrl+C in a sidebar window (nothing to cancel) must quit")
	}
}

func TestAITUI_CtrlL_ClearsTranscriptAndClipboardItems(t *testing.T) {
	m := newTestModel()
	m.appendTranscript("old stuff ⧉\n")
	m.copyItems = []string{"old"}
	m = press(t, m, tea.KeyCtrlL)
	if strings.Contains(m.transcript.String(), "old stuff") || len(m.copyItems) != 0 {
		t.Errorf("Ctrl+L must clear transcript and copy items; transcript=%q items=%v", m.transcript.String(), m.copyItems)
	}
}

// ---------- Home/End ----------

func TestAITUI_HomeEnd_MoveCursorWhenInputHasText(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("send orders")
	m.follow = true
	m = press(t, m, tea.KeyHome)
	if !m.follow {
		t.Error("Home with text in the input must not scroll the transcript")
	}
	if col := m.input.LineInfo().ColumnOffset; col != 0 {
		t.Errorf("Home should move the cursor to column 0, got %d", col)
	}

	m.input.SetValue("")
	m = press(t, m, tea.KeyHome)
	if m.follow {
		t.Error("Home on an empty input scrolls the transcript to the top")
	}
}

// ---------- Sidebar prompt / filter text entry ----------

func TestAITUI_SendPrompt_PasteArrivesVerbatim(t *testing.T) {
	m := newTestModelWithQueueWindow(&mockQueueBackend{}, nil, nil)
	m = pressRune(t, m, "S")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(`{"a": 1}`), Paste: true})
	m = updated.(aiTUIModel)
	if m.promptName != `{"a": 1}` {
		t.Errorf("pasted payload = %q, want it verbatim (no [..] paste brackets)", m.promptName)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x"), Alt: true})
	m = updated.(aiTUIModel)
	if strings.Contains(m.promptName, "alt") {
		t.Errorf("an Alt chord must not insert text, payload = %q", m.promptName)
	}
}

func TestAITUI_SendPrompt_BackspaceIsRuneAware(t *testing.T) {
	m := newTestModelWithQueueWindow(&mockQueueBackend{}, nil, nil)
	m = pressRune(t, m, "S")
	m = pressRune(t, m, "grüß")
	m = press(t, m, tea.KeyBackspace)
	if m.promptName != "grü" {
		t.Errorf("after backspace payload = %q, want %q", m.promptName, "grü")
	}
	m = press(t, m, tea.KeyCtrlU)
	if m.promptName != "" {
		t.Errorf("Ctrl+U should clear the payload, got %q", m.promptName)
	}
}

func TestAITUI_Filter_PasteAndUnicode(t *testing.T) {
	m := newTestModelWithObjects()
	m.focus = 1
	m = pressRune(t, m, "/")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("pay"), Paste: true})
	m = updated.(aiTUIModel)
	if m.objTypes[0].filter != "pay" {
		t.Fatalf("filter = %q, want %q", m.objTypes[0].filter, "pay")
	}
	m = pressRune(t, m, "é")
	m = press(t, m, tea.KeyBackspace)
	if m.objTypes[0].filter != "pay" {
		t.Errorf("filter after backspace = %q, want %q", m.objTypes[0].filter, "pay")
	}
}

// ---------- Focus ----------

func TestAITUI_NarrowTerminal_NoFocusIntoHiddenSidebar(t *testing.T) {
	m := newTestModelWithObjects()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(aiTUIModel)
	if m.sidebarVisible() {
		t.Fatal("an 80-column terminal hides the sidebar")
	}
	m = press(t, m, tea.KeyShiftTab)
	if m.focus != focusChat {
		t.Errorf("Shift+Tab must not focus an invisible sidebar window, focus = %v", m.focus)
	}
	if strings.Contains(m.renderStatusBar(), "browse") {
		t.Error("status bar must not advertise browsing a hidden sidebar")
	}
}

func TestAITUI_ShrinkingTerminal_ReturnsFocusToChat(t *testing.T) {
	m := newTestModelWithObjects()
	m = press(t, m, tea.KeyShiftTab)
	if m.focus == focusChat {
		t.Fatal("precondition: sidebar window focused")
	}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 70, Height: 30})
	m = updated.(aiTUIModel)
	if m.focus != focusChat || !m.input.Focused() {
		t.Errorf("after the sidebar disappears focus must return to the input (focus=%v)", m.focus)
	}
}

func TestAITUI_SidebarAction_KeepsPaneFocus(t *testing.T) {
	m := newTestModelWithQueueWindow(&mockQueueBackend{}, nil, nil)
	m.input.Blur() // as cycleFocus does when a sidebar window takes focus
	updated, _ := m.Update(sideActionMsg{action: "   └ sent"})
	m = updated.(aiTUIModel)
	if m.focus == focusChat {
		t.Fatal("precondition: the test model focuses the Queues window")
	}
	if m.input.Focused() {
		t.Error("finishing a sidebar action must not focus the input while a sidebar window owns the keyboard")
	}
}

// ---------- Proposal editing ----------

func TestAITUI_EditingEsc_RecordsDiscardInHistory(t *testing.T) {
	m := newTestModel()
	m.state = tuiProposing
	m.proposedCmd = "send q hi"
	m = pressRune(t, m, "e")
	m = press(t, m, tea.KeyEsc)
	if m.state != tuiIdle {
		t.Fatalf("state = %v, want idle", m.state)
	}
	h := m.ai.history
	if len(h) < 2 || h[len(h)-2].Content != "send q hi" || !strings.Contains(h[len(h)-1].Content, "discarded") {
		t.Errorf("history must record the discarded proposal like a plain Esc does, got %+v", h)
	}
}

// ---------- AI request / execution lifecycle ----------

func TestAITUI_StaleAIResponse_AfterCancel_IsIgnored(t *testing.T) {
	m := newTestModel()
	m.state = tuiThinking
	m.ai.history = []aiMessage{{Role: "user", Content: "delete everything"}}
	m = press(t, m, tea.KeyEsc) // cancel
	updated, _ := m.Update(aiDoneMsg{text: "manage delete-queue orders"})
	m = updated.(aiTUIModel)
	if m.state != tuiIdle || m.proposedCmd != "" {
		t.Errorf("a response arriving after cancel must not be proposed (state=%v proposed=%q)", m.state, m.proposedCmd)
	}
}

func TestAITUI_CancelledExecution_DoesNotAutoFix(t *testing.T) {
	m := newTestModel()
	m.mode = modeAI
	m.proposedCmd = "subscribe events"
	m.state = tuiExecuting
	cancelled := false
	m.execCancel = func() { cancelled = true }

	m = press(t, m, tea.KeyEsc)
	if !cancelled {
		t.Fatal("Esc must cancel the running command")
	}
	updated, _ := m.Update(execDoneMsg{err: errors.New("signal: killed")})
	m = updated.(aiTUIModel)
	if m.state != tuiIdle || m.fixAttempts != 0 {
		t.Errorf("a user-cancelled command must not trigger auto-fix (state=%v fixAttempts=%d)", m.state, m.fixAttempts)
	}
	if !strings.Contains(m.transcript.String(), "(cancelled)") {
		t.Errorf("transcript should say the command was cancelled:\n%s", m.transcript.String())
	}
}

// The transcript shows stderr (message properties, warnings) interleaved with
// stdout, as a terminal would, and far more than the AI's 2 KB feedback tail.
func TestAITUI_ExecOutput_InterleavesStderr_NotCappedAtFeedbackSize(t *testing.T) {
	m := newTestModel()
	m.state = tuiExecuting
	m.mode = modeCmd
	m.proposedCmd = "receive orders -n 0"

	capture := newExecCapture()
	out, errw := capture.writer(false), capture.writer(true)
	for i := range 200 {
		_, _ = fmt.Fprintf(errw, "Properties: seq=%d\n", i)
		_, _ = fmt.Fprintf(out, "payload-%03d %s\n", i, strings.Repeat("x", 20))
	}
	chunks, truncated, stdout, stderr := capture.snapshot()
	if len(stdout) > maxCapture || len(stderr) > maxCapture {
		t.Fatalf("AI feedback tails must stay within %d bytes", maxCapture)
	}

	updated, _ := m.Update(execDoneMsg{chunks: chunks, truncated: truncated, stdout: stdout, stderr: stderr})
	m = updated.(aiTUIModel)
	tr := m.transcript.String()
	for _, want := range []string{"payload-000", "payload-199", "Properties: seq=0", "Properties: seq=199"} {
		if !strings.Contains(tr, want) {
			t.Errorf("transcript missing %q", want)
		}
	}
	if strings.Index(tr, "Properties: seq=5") > strings.Index(tr, "payload-005") {
		t.Error("a message's properties (stderr) must precede its payload (stdout), in write order")
	}
	fb := m.ai.history[len(m.ai.history)-1].Content
	if !strings.Contains(fb, fmt.Sprintf("stdout (last %d bytes)", maxCapture)) {
		t.Errorf("feedback should say the stdout tail was truncated, got:\n%s", fb)
	}
}

func TestExecCapture_DropsOldestBeyondDisplayBudget(t *testing.T) {
	c := newExecCapture()
	w := c.writer(false)
	line := strings.Repeat("y", 99) + "\n"
	for range (maxDisplayCapture / len(line)) + 50 {
		_, _ = w.Write([]byte(line))
	}
	chunks, truncated, _, _ := c.snapshot()
	total := 0
	for _, ch := range chunks {
		total += len(ch.text)
	}
	if !truncated || total > maxDisplayCapture {
		t.Errorf("truncated=%v total=%d, want truncated and ≤ %d", truncated, total, maxDisplayCapture)
	}
	if !strings.HasPrefix(string(chunks[0].text), "yyy") {
		t.Error("trimming should cut at a line boundary")
	}
}

func TestAITUI_LivePreview_ShowsRunningOutput(t *testing.T) {
	m := newTestModel()
	m.proposedCmd = "subscribe events"
	updated, _ := m.startExecution("subscribe events")
	m = updated.(aiTUIModel)
	_, _ = m.liveCapture.writer(false).Write([]byte("event-1\nevent-2\n"))
	m.setViewportContent()
	if !strings.Contains(m.viewport.View(), "event-2") {
		t.Errorf("running command's output should be previewed live:\n%s", m.viewport.View())
	}
}

// ---------- Clipboard markers ----------

func TestAppendTranscript_TrimDropsCopyItemsOfTrimmedMarkers(t *testing.T) {
	m := newTestModel()
	m.appendTranscript("first ⧉\n")
	m.copyItems = append(m.copyItems, "first")
	m.appendTranscript("second ⧉\n")
	m.copyItems = append(m.copyItems, "second")
	m.appendTranscript(strings.Repeat("z", maxTranscriptBytes-20) + "\n")
	// The first marker(s) are trimmed away with the oldest content.
	markers := strings.Count(m.transcript.String(), copyMarker)
	if len(m.copyItems) != markers {
		t.Fatalf("copyItems = %v but %d markers remain — clicks would copy the wrong item", m.copyItems, markers)
	}
	if markers == 1 && m.copyItems[0] != "second" {
		t.Errorf("remaining item = %q, want %q", m.copyItems[0], "second")
	}
}

// ---------- /effort, /reset ----------

func TestAITUI_EffortBeforeFirstRequest_IsKeptAndPersisted(t *testing.T) {
	m := newTestModel() // no AI client yet (init() runs lazily on the first request)
	if !m.applyEffort("high") {
		t.Fatal("applyEffort(high) failed")
	}
	if got := m.ai.currentEffort(); got != effortHigh {
		t.Errorf("pending effort = %q, want high", got)
	}
	cfg, err := loadConfig()
	if err != nil || cfg.AI.Effort != "high" {
		t.Errorf("effort not persisted: cfg=%+v err=%v", cfg, err)
	}

	// Once the client exists, the pending choice is applied to it.
	client := &openaiClient{}
	m.ai.client = client
	m.ai.setEffort(m.ai.effort)
	if client.Effort() != effortHigh {
		t.Errorf("client effort = %q, want high", client.Effort())
	}
}

func TestAITUI_Reset_KeepsSessionTokenTotals(t *testing.T) {
	m := newTestModel()
	m.totalIn, m.totalOut = 1000, 200
	m.ai.history = []aiMessage{{Role: "user", Content: "x"}}
	m.copyItems = []string{"a"}
	m.input.SetValue("/reset")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(aiTUIModel)
	if m.totalIn != 1000 || m.totalOut != 200 {
		t.Error("/reset must keep the session's token totals")
	}
	if len(m.ai.history) != 0 || len(m.copyItems) != 0 {
		t.Error("/reset must clear the conversation and clipboard items")
	}
	if cmd == nil {
		t.Error("/reset should refresh the topology in the background (a tea.Cmd), not inline")
	}
}

// ---------- Destructive detection through aliases ----------

func TestAITUI_ProposedAliasToPurge_IsFlaggedDestructive(t *testing.T) {
	m := newTestModel()
	m.session.aliases = map[string]string{"nuke": "manage purge $1"}
	m.state = tuiThinking
	updated, _ := m.Update(aiDoneMsg{text: "nuke orders"})
	m = updated.(aiTUIModel)
	if !m.proposedDestructive {
		t.Error("an alias that expands to a purge must get the destructive warning")
	}
}

// ---------- Queue send via the S hotkey reaches the backend ----------

func TestAITUI_SendPrompt_PastedPayloadIsSent(t *testing.T) {
	qb := &mockQueueBackend{}
	m := newTestModelWithQueueWindow(qb, nil, nil)
	m = pressRune(t, m, "S")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello world"), Paste: true})
	m = updated.(aiTUIModel)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter should dispatch the send")
	}
	cmd()
	if string(qb.lastSendOpts.Message) != "hello world" {
		t.Errorf("sent %q, want %q", qb.lastSendOpts.Message, "hello world")
	}
}

// A single huge unbroken line (minified JSON, base64, an --ndjson record) must
// neither freeze rendering nor overflow the pane: the transcript is re-wrapped
// on every repaint, and the previous wrapper was quadratic in word length.
func TestWrapText_LongWordIsFastAndHardBroken(t *testing.T) {
	word := strings.Repeat("z", 200_000)
	start := time.Now()
	out := wrapText(word, 80)
	if d := time.Since(start); d > time.Second {
		t.Fatalf("wrapping a 200 KB word took %v", d)
	}
	for i, line := range strings.Split(out, "\n") {
		if len(line) > 80 {
			t.Fatalf("line %d is %d columns wide, want ≤ 80", i, len(line))
		}
	}
	if got := wrapText("hello world again", 11); got != "hello world\nagain" {
		t.Errorf("word wrapping changed: %q", got)
	}
}

// Each command appears once in the transcript — as the cmd-mode echo or the
// accepted proposal, carrying its ⧉ copy marker — not again as a "ran:" card.
func TestAITUI_CommandAppearsOnceWithCopyMarker(t *testing.T) {
	m := newTestModel()
	m.mode = modeCmd
	m.input.SetValue("peek orders")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(aiTUIModel)
	updated, _ = m.Update(execDoneMsg{stdout: "payload\n"})
	m = updated.(aiTUIModel)

	tr := m.transcript.String()
	if n := strings.Count(tr, "peek orders"); n != 1 {
		t.Errorf("command appears %d times, want once:\n%s", n, tr)
	}
	lines := strings.Split(tr, "\n")
	if idx := m.copyIdxForLineText(lines, "peek orders"); idx < 0 || m.copyItems[idx] != "peek orders" {
		t.Errorf("the command line's ⧉ must copy the command; items=%v", m.copyItems)
	}
	if idx := m.copyIdxForLineText(lines, "⧉"); idx < 0 {
		t.Error("no copy markers found")
	}
}

// copyIdxForLineText resolves the copy item for the first transcript line
// containing needle, mirroring what a mouse click on that line would copy.
func (m aiTUIModel) copyIdxForLineText(lines []string, needle string) int {
	m.wrappedContentLines = lines
	for i, l := range lines {
		if strings.Contains(l, needle) {
			return m.copyIdxForLine(i)
		}
	}
	return -1
}

// ---------- Sidebar targets resolve like CLI arguments ----------

// redisLikeResolver mimics broker/redis.ResolveTarget: bare names get the key
// prefix, anything containing ':' passes through.
func redisLikeResolver(t TargetSpec) (string, error) {
	if strings.Contains(t.To, ":") {
		return t.To, nil
	}
	if t.IsTopic {
		return "xmc:topic:" + t.To, nil
	}
	return "xmc:queue:" + t.To, nil
}

// rabbitLikeResolver mimics broker/rabbitmq.ResolveTarget's address rules.
func rabbitLikeResolver(t TargetSpec) (string, error) {
	switch {
	case t.Exchange != "" && t.To != "":
		return "/exchanges/" + t.Exchange + "/" + t.To, nil
	case t.Exchange != "":
		return "/exchanges/" + t.Exchange, nil
	case t.IsTopic:
		return "/exchanges/amq.topic/" + t.To, nil
	default:
		return "/queues/" + t.To, nil
	}
}

func TestAITUI_SidebarSendAndPeek_ResolveTargetLikeTheCLI(t *testing.T) {
	qb := &mockQueueBackend{receiveMsg: &backends.Message{Data: []byte("x")}}
	m := newTestModelWithQueueWindow(qb, nil, nil)
	m.session.spec.ResolveTarget = redisLikeResolver

	m = pressRune(t, m, "S")
	m = pressRune(t, m, "hi")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	cmd()
	if qb.lastSendOpts.Queue != "xmc:queue:orders" {
		t.Errorf("S sent to %q, want the resolved %q (same as `send orders`)", qb.lastSendOpts.Queue, "xmc:queue:orders")
	}

	m.state = tuiIdle
	m.promptActive = false
	_, cmd = pressRuneCmd(t, m, "p")
	cmd()
	if qb.lastReceiveOpts.Queue != "xmc:queue:orders" {
		t.Errorf("p peeked %q, want %q", qb.lastReceiveOpts.Queue, "xmc:queue:orders")
	}
}

func TestAITUI_SidebarSendOnExchange_PublishesToTheExchange(t *testing.T) {
	tb := &mockTopicBackend{}
	m := newTestModelWithExchangesWindow(nil, tb, nil)
	m.session.spec.ResolveTarget = rabbitLikeResolver
	m.session.spec.ExchangeRouting = true

	m = pressRune(t, m, "S")
	m = pressRune(t, m, "hi")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	cmd()
	// Equivalent of `publish -e orders-exchange hi` — not amq.topic with the
	// exchange's name as routing key.
	if tb.lastPublishOpts.Topic != "/exchanges/orders-exchange" {
		t.Errorf("S published to %q, want %q", tb.lastPublishOpts.Topic, "/exchanges/orders-exchange")
	}
}

func pressRuneCmd(t *testing.T, m aiTUIModel, key string) (aiTUIModel, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	if cmd == nil {
		t.Fatalf("key %q dispatched no command", key)
	}
	return updated.(aiTUIModel), cmd
}

func TestAITUI_SideAction_RefreshFollowsMatchingAutoUpdateSetting(t *testing.T) {
	m := newTestModelWithQueueWindow(&mockQueueBackend{}, nil, nil)
	m.ai.autoUpdateObjects, m.ai.autoUpdateMessages = false, true

	_, cmd := m.Update(sideActionMsg{action: "   └ sent"})
	if cmd == nil {
		t.Error("a message action (send) must refresh when auto-update-messages is on")
	}
	_, cmd = m.Update(sideActionMsg{objects: true})
	if cmd != nil {
		t.Error("an object action must not refresh when auto-update-objects is off")
	}
}

// A response from a cancelled request that arrives while a newer request is
// in flight must not be taken as the newer request's answer.
func TestAITUI_OldRequestResponse_IgnoredWhileNewerInFlight(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("first question")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(aiTUIModel)
	oldGen := m.aiGen
	m = press(t, m, tea.KeyEsc) // cancel the first request

	m.input.SetValue("second question")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(aiTUIModel)
	if m.state != tuiThinking || m.aiGen == oldGen {
		t.Fatalf("precondition: second request in flight (state=%v gen=%d)", m.state, m.aiGen)
	}

	updated, _ = m.Update(tokenMsg{text: "stale", gen: oldGen})
	m = updated.(aiTUIModel)
	updated, _ = m.Update(aiDoneMsg{text: "manage purge orders", gen: oldGen})
	m = updated.(aiTUIModel)
	if m.state != tuiThinking || m.proposedCmd != "" || strings.Contains(m.streamBuf.String(), "stale") {
		t.Errorf("stale response leaked into the new request (state=%v proposed=%q)", m.state, m.proposedCmd)
	}

	updated, _ = m.Update(aiDoneMsg{text: "peek orders", gen: m.aiGen})
	m = updated.(aiTUIModel)
	if m.state != tuiProposing || m.proposedCmd != "peek orders" {
		t.Errorf("the current response must be proposed (state=%v proposed=%q)", m.state, m.proposedCmd)
	}
}
