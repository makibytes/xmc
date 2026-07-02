package cmd

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---------- Window kind discriminator ----------

// objWindowKind identifies the type of a sidebar window.
type objWindowKind int

const (
	objWindowObjects objWindowKind = iota // default: ManageSpec object list
	objWindowProcs                         // background process manager
)

// ---------- Constants & styles ----------

const maxProcCapture = 256 * 1024 // 256 KB per background process

var (
	warnTriangleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))  // yellow ▲ finished ok
	procRunStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))  // cyan ● running
	procErrStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))  // bright-red ▲ error
)

// ---------- bgProcess ----------

// bgProcess holds state for one managed background process.
// cancel, done, err, finishedAt are only accessed on the UI goroutine.
// out and lines are shared with the background goroutine via mu.
type bgProcess struct {
	id        int
	name      string
	command   string
	startedAt time.Time
	cancel    context.CancelFunc // set via procCancelMsg; UI goroutine only thereafter
	doneCh    chan struct{}       // closed by the background goroutine when it exits

	mu         sync.Mutex
	out        cappedBuffer // goroutine writes; UI reads via snapshotText()
	lines      int          // count of '\n' in out
	stderrSeen bool         // process wrote to stderr since the user last viewed its output

	// Set in handleProcDoneMsg (UI goroutine only):
	done       bool
	err        error
	finishedAt time.Time
}

// snapshotText returns a safe copy of the captured output under mu.
func (p *bgProcess) snapshotText() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.out.String()
}

// hasUnseenStderr reports whether the process wrote to stderr since the user
// last viewed its output (Enter in the Processes window clears it).
func (p *bgProcess) hasUnseenStderr() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stderrSeen
}

// ---------- procWriter ----------

// procWriter is a thread-safe io.Writer that routes stdout or stderr of
// executePipelineIO into a bgProcess's output buffer (interleaved). The
// stderr variant additionally flags the process so the sidebar renders its
// name in red, and wakes the UI so the highlight appears immediately.
type procWriter struct {
	p      *bgProcess
	stderr bool
	prog   **tea.Program
}

func (w procWriter) Write(b []byte) (int, error) {
	w.p.mu.Lock()
	w.p.lines += bytes.Count(b, []byte{'\n'})
	n, err := w.p.out.Write(b)
	notify := false
	if w.stderr && len(b) > 0 && !w.p.stderrSeen {
		w.p.stderrSeen = true
		notify = true
	}
	w.p.mu.Unlock()
	// Send outside the lock: Program.Send may block during shutdown.
	if notify {
		if prog := derefProgram(w.prog); prog != nil {
			prog.Send(procStderrMsg{id: w.p.id})
		}
	}
	return n, err
}

// ---------- Bubble Tea messages ----------

// procCancelMsg delivers the context cancel func from the spawned goroutine
// back to the UI goroutine so it can be stored for kill/delete operations.
type procCancelMsg struct {
	id     int
	cancel context.CancelFunc
}

// procDoneMsg is sent by the background goroutine when the process finishes.
type procDoneMsg struct {
	id  int
	err error
}

// procStderrMsg wakes the UI when a background process first writes to
// stderr, so the red name highlight in the Processes window renders
// immediately instead of at the next tick or keystroke.
type procStderrMsg struct{ id int }

// ---------- Pure helpers ----------

// backgroundVerbs is the set of xmc verbs whose --for flag starts a background process.
var backgroundVerbs = map[string]bool{
	"receive":   true,
	"subscribe": true,
	"peek":      true,
	"forward":   true,
	"bridge":    true,
	"reply":     true,
}

// processName returns a short display name: verb + first positional (non-flag) arg.
// "receive q1 --for 1h" → "receive q1"; "forward q1 q2 --for 5m" → "forward q1".
func processName(command string) string {
	parts := shellSplit(command)
	if len(parts) == 0 {
		return command
	}
	verb := parts[0]
	for i := 1; i < len(parts); i++ {
		if !strings.HasPrefix(parts[i], "-") {
			return verb + " " + parts[i]
		}
	}
	return verb
}

// segHasFor returns true if a single pipeline segment is a streaming verb with
// --for or --forever, making it eligible to run as a background process.
func segHasFor(segment string) bool {
	parts := shellSplit(segment)
	if len(parts) == 0 {
		return false
	}
	if !backgroundVerbs[strings.ToLower(parts[0])] {
		return false
	}
	for i, p := range parts {
		switch {
		case p == "--for" && i+1 < len(parts) && parts[i+1] != "" && !strings.HasPrefix(parts[i+1], "-"):
			return true
		case strings.HasPrefix(p, "--for=") && len(p) > 6:
			return true
		case p == "--forever":
			return true
		case strings.HasPrefix(p, "--forever=") && len(p) > 10:
			return true
		}
	}
	return false
}

// commandHasFor returns true if any segment of line is a backgroundable --for/--forever command.
func commandHasFor(line string) bool {
	return anyCommand(line, segHasFor)
}

// commandWellFormed returns false when the command ends with a bare top-level '&'
// (the shell background operator). An '&' inside single or double quotes is fine.
// Only applied in cmd mode; AI-proposed commands are never rejected by this check.
func commandWellFormed(s string) bool {
	var lastTopLevel rune
	inSingle, inDouble, escaped := false, false, false
	for _, r := range s {
		if escaped {
			escaped = false
			lastTopLevel = r
			continue
		}
		switch {
		case r == '\\' && !inSingle:
			escaped = true
			lastTopLevel = r
		case r == '\'' && !inDouble:
			inSingle = !inSingle
			lastTopLevel = r
		case r == '"' && !inSingle:
			inDouble = !inDouble
			lastTopLevel = r
		case !inSingle && !inDouble:
			if r != ' ' && r != '\t' {
				lastTopLevel = r
			}
		}
	}
	return lastTopLevel != '&'
}

// ---------- Process window lifecycle ----------

// ensureProcWindow adds the Processes sidebar window if it does not exist yet.
func (m *aiTUIModel) ensureProcWindow() {
	if m.procWinIdx >= 0 {
		return
	}
	m.objTypes = append(m.objTypes, objWindow{
		label: "Processes",
		kind:  objWindowProcs,
	})
	m.procWinIdx = len(m.objTypes) - 1
	m.recalcLayout()
}

// removeProcWindow removes the Processes sidebar window (called when procs is empty).
// The proc window is always the last entry; no other window index changes.
func (m *aiTUIModel) removeProcWindow() {
	if m.procWinIdx < 0 {
		return
	}
	// If focused on the proc window, exit and return to chat.
	if int(m.focus)-1 == m.procWinIdx {
		m.exitProcessView()
		m.focus = focusChat
		m.input.Focus()
	}
	m.objTypes = m.objTypes[:len(m.objTypes)-1]
	m.procWinIdx = -1
	m.recalcLayout()
}

// rebuildProcessNodes clamps procSel to the valid range.
// Actual sidebar rendering reads m.procs directly in writeProcessSection.
func (m *aiTUIModel) rebuildProcessNodes() {
	n := len(m.procs)
	if n == 0 {
		m.procSel = 0
		return
	}
	if m.procSel >= n {
		m.procSel = n - 1
	}
	if m.procSel < 0 {
		m.procSel = 0
	}
}

// ---------- Launch ----------

// startBackgroundProcess registers a new background process, keeps the TUI
// interactive, and returns a tea.Cmd that runs the pipeline asynchronously.
// The caller must have already written the transcript echo line and appended
// the command to cmdHistory.
func (m aiTUIModel) startBackgroundProcess(command string) (tea.Model, tea.Cmd) {
	p := &bgProcess{
		id:        m.procNextID,
		name:      processName(command),
		command:   command,
		startedAt: time.Now(),
		doneCh:    make(chan struct{}),
	}
	p.out.max = maxProcCapture
	m.procNextID++
	m.procs = append(m.procs, p)
	(&m).ensureProcWindow()
	(&m).rebuildProcessNodes()

	// Stay idle and interactive immediately.
	m.state = tuiIdle
	m.input.Focus()

	// Write to shell history so the command is available via Up/Down recall.
	shellHistory.Append(command)

	// Append a dim note below the echo line (echo written by caller).
	m.appendTranscript(dimStyle.Render("↳ started background process: "+p.name) + "\n\n")

	// Record in AI history so the model has context.
	if m.ai != nil {
		m.ai.history = append(m.ai.history, aiMessage{Role: "assistant", Content: command})
		m.ai.history = append(m.ai.history, aiMessage{Role: "user", Content: "[background process started: " + command + "]"})
		trimHistory(&m.ai.history, maxHistory)
	}

	pptr := m.program
	rootCmd := m.rootCmd
	id := p.id
	pwOut := procWriter{p: p, prog: pptr}
	pwErr := procWriter{p: p, stderr: true, prog: pptr}

	// Background processes get their own broker connection rather than
	// sharing m.session's cached adapter. A long-running stream (receive
	// --for 1h, forward --for, ...) would otherwise run concurrently with
	// foreground send/peek/manage commands (and other background processes)
	// on the exact same connection/session — most broker client libraries
	// are not safe for concurrent use of one connection this way. Wrapping
	// with wrapReconnectQueue/Topic mirrors the wrapping runAI gives the
	// foreground session, so the process still gets auto-reconnect.
	procSess := &shellSession{
		spec:         m.session.spec,
		queueFactory: wrapReconnectQueue(m.session.spec.Queue, ReconnectOptions{}),
		topicFactory: wrapReconnectTopic(m.session.spec.Topic, ReconnectOptions{}),
		aliases:      m.session.aliases,
	}

	return m, func() tea.Msg {
		defer close(p.doneCh)
		defer procSess.close()
		ctx, cancel := context.WithCancel(context.Background())
		if prog := derefProgram(pptr); prog != nil {
			prog.Send(procCancelMsg{id: id, cancel: cancel})
		}
		err := procSess.executePipelineIO(ctx, command, rootCmd, strings.NewReader(""), pwOut, pwErr)
		cancel()
		return procDoneMsg{id: id, err: err}
	}
}

// ---------- Message handlers ----------

func (m aiTUIModel) handleProcCancelMsg(msg procCancelMsg) (tea.Model, tea.Cmd) {
	for _, p := range m.procs {
		if p.id == msg.id {
			p.cancel = msg.cancel // UI goroutine; no lock needed
			return m, nil
		}
	}
	// Process was already deleted before the cancel arrived — fire it now.
	msg.cancel()
	return m, nil
}

func (m aiTUIModel) handleProcDoneMsg(msg procDoneMsg) (tea.Model, tea.Cmd) {
	for _, p := range m.procs {
		if p.id == msg.id {
			p.done = true       // UI goroutine only; no lock
			p.err = msg.err
			p.finishedAt = time.Now()
			(&m).rebuildProcessNodes()
			// Trigger sidebar refresh when the process may have changed message counts.
			if anyCommand(p.command, mutatesMessages) && !m.refreshing && len(m.objTypes) > 0 {
				return m, (&m).beginRefresh()
			}
			return m, nil
		}
	}
	return m, nil
}

// ---------- Key handling ----------

// handleKeyProcessPane handles keyboard events when the Processes window has focus.
func (m aiTUIModel) handleKeyProcessPane(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyUp:
		if len(m.procs) > 0 && m.procSel > 0 {
			m.procSel--
			(&m).updateProcPrompt()
		}
		return m, nil

	case tea.KeyDown:
		if m.procSel < len(m.procs)-1 {
			m.procSel++
			(&m).updateProcPrompt()
		}
		return m, nil

	case tea.KeyEnter:
		if len(m.procs) > 0 && m.procSel < len(m.procs) {
			(&m).dumpProcessOutput(m.procs[m.procSel])
		}
		return m, nil

	case tea.KeySpace:
		// Space collapses/expands the Processes window.
		if m.procWinIdx >= 0 {
			m.objTypes[m.procWinIdx].collapsed = !m.objTypes[m.procWinIdx].collapsed
		}
		return m, nil

	case tea.KeyEsc:
		(&m).exitProcessView()
		m.focus = focusChat
		m.input.Focus()
		return m, nil

	case tea.KeyTab:
		// Tab moves focus forward (to the next window).
		m.cycleFocus(true)
		return m, nil

	case tea.KeyShiftTab:
		// Shift+Tab moves focus backward.
		m.cycleFocus(false)
		return m, nil

	case tea.KeyRunes:
		switch msg.String() {
		case "d": // remove selected (kill first if running)
			if len(m.procs) == 0 {
				return m, nil
			}
			p := m.procs[m.procSel]
			if p.cancel != nil {
				p.cancel()
			}
			m.procs = append(m.procs[:m.procSel], m.procs[m.procSel+1:]...)
			(&m).rebuildProcessNodes()
			if len(m.procs) == 0 {
				(&m).removeProcWindow()
			} else {
				(&m).updateProcPrompt()
			}

		case "k": // kill but keep in list
			if len(m.procs) > 0 && m.procSel < len(m.procs) {
				p := m.procs[m.procSel]
				if p.cancel != nil {
					p.cancel()
				}
			}

		case "p": // purge all finished processes
			var keep []*bgProcess
			for _, p := range m.procs {
				if !p.done { // done is UI-goroutine-only; no lock needed
					keep = append(keep, p)
				}
			}
			m.procs = keep
			(&m).rebuildProcessNodes()
			if len(m.procs) == 0 {
				(&m).removeProcWindow()
			} else {
				(&m).updateProcPrompt()
			}

		case "D": // kill and delete all
			for _, p := range m.procs {
				if p.cancel != nil {
					p.cancel()
				}
			}
			m.procs = nil
			(&m).removeProcWindow()
		}
		return m, nil
	}
	return m, nil
}

// ---------- Prompt save/restore ----------

// enterProcessView saves prompt state and switches to read-only process-command
// display. Idempotent.
func (m *aiTUIModel) enterProcessView() {
	if m.inProcView {
		return
	}
	m.inProcView = true
	m.savedMode = m.mode
	m.savedInput = m.input.Value()
	m.savedHist = m.histIdx
	// Use cmd-mode prompt label so the selected command renders as "<binary>> <cmd>".
	if m.mode != modeCmd {
		applyPromptFunc(&m.input, m.binaryName+"> ")
	}
	m.updateProcPrompt()
	m.input.Blur()
}

// exitProcessView restores the prompt state saved by enterProcessView. Idempotent.
func (m *aiTUIModel) exitProcessView() {
	if !m.inProcView {
		return
	}
	m.inProcView = false
	if m.savedMode == modeAI {
		applyPromptFunc(&m.input, "ask> ")
	} else {
		applyPromptFunc(&m.input, m.binaryName+"> ")
	}
	m.mode = m.savedMode
	m.input.SetValue(m.savedInput)
	m.histIdx = m.savedHist
	m.updateInputHeight()
}

// updateProcPrompt sets the textarea to the selected process's command (read-only).
func (m *aiTUIModel) updateProcPrompt() {
	if !m.inProcView || m.procWinIdx < 0 || len(m.procs) == 0 {
		return
	}
	if m.procSel >= len(m.procs) {
		m.procSel = len(m.procs) - 1
	}
	m.input.SetValue(m.procs[m.procSel].command)
}

// ---------- Output dump ----------

// dumpProcessOutput appends the captured output of p to the main transcript,
// styled like received messages (│ border + ⧉ clipboard markers).
func (m *aiTUIModel) dumpProcessOutput(p *bgProcess) {
	text := p.snapshotText()
	// The user is looking at the output now — clear the red stderr highlight.
	p.mu.Lock()
	p.stderrSeen = false
	p.mu.Unlock()
	var b strings.Builder

	m.copyItems = append(m.copyItems, p.command)
	b.WriteString(histCmdStyle.Render("▶ "+p.name) + copyHintStyle.Render(" ⧉") + "\n")

	trimmed := strings.TrimRight(text, "\n")
	if trimmed != "" {
		b.WriteString(renderMessagePayload(trimmed))
		m.copyItems = append(m.copyItems, trimmed)
		b.WriteString(copyHintStyle.Render("  ⧉") + "\n")
	} else {
		b.WriteString(dimStyle.Render("  (no output yet)") + "\n")
	}
	if p.err != nil {
		b.WriteString(warnStyle.Render("✗ "+p.err.Error()) + "\n")
	}
	b.WriteString("\n")
	m.appendTranscript(b.String())
}

// ---------- Sidebar rendering ----------

// killAllProcs cancels every running background process. Safe to call multiple times.
func (m *aiTUIModel) killAllProcs() {
	for _, p := range m.procs {
		if p.cancel != nil {
			p.cancel()
		}
	}
}

// ---------- Sidebar rendering ----------

// writeProcessSection renders the Processes sidebar window, returning lines written.
// Called from writeObjectSection when kind == objWindowProcs.
// collapsed=true renders only the title line (no underline, no body rows).
func (m aiTUIModel) writeProcessSection(b *strings.Builder, width, bodyLines int, collapsed bool) int {
	focused := m.procWinIdx >= 0 && int(m.focus)-1 == m.procWinIdx
	lines := 0

	// Disclosure glyph: ▸ when collapsed, ▾ when expanded.
	glyph := "▾ "
	if collapsed {
		glyph = "▸ "
	}

	// Header.
	headerText := glyph + fmt.Sprintf("Processes (%d)", len(m.procs))
	if focused {
		pad := width - lipgloss.Width(headerText) - 4
		if pad < 0 {
			pad = 0
		}
		b.WriteString(sidebarFocusStyle.Render(headerText + strings.Repeat(" ", pad) + "◂"))
	} else {
		b.WriteString(histTitleStyle.Render(headerText))
	}
	b.WriteString("\n")
	lines++

	// When collapsed, stop here.
	if collapsed {
		return lines
	}

	b.WriteString(dimStyle.Render(strings.Repeat("─", width-1)))
	b.WriteString("\n")
	lines++

	if len(m.procs) == 0 {
		b.WriteString(dimStyle.Render("  (none)") + "\n")
		return lines + 1
	}

	start, end := computeWindow(len(m.procs), m.procSel, bodyLines)

	if start > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  ▲ %d more", start)) + "\n")
		lines++
		start++
		if start > m.procSel {
			start = m.procSel
		}
	}

	showBottomHint := end < len(m.procs)
	limit := end
	if showBottomHint {
		limit = end - 1
		if limit < start {
			limit = start
		}
	}

	for i := start; i < limit; i++ {
		p := m.procs[i]
		// p.done and p.err are UI-goroutine-only fields; no lock needed.
		var glyph string
		switch {
		case !p.done:
			glyph = procRunStyle.Render("●")
		case p.err != nil:
			glyph = procErrStyle.Render("▲")
		default:
			glyph = warnTriangleStyle.Render("▲")
		}

		name := p.name
		maxName := width - 5
		if maxName < 3 {
			maxName = 3
		}
		runes := []rune(name)
		if len(runes) > maxName {
			name = string(runes[:maxName-1]) + "…"
		}
		// Unseen stderr output turns the name red until the user views the
		// process output (Enter).
		if p.hasUnseenStderr() {
			name = procErrStyle.Render(name)
		}

		if focused && i == m.procSel {
			b.WriteString(sidebarSelStyle.Render(fmt.Sprintf("▸ %s %s", glyph, name)))
		} else {
			b.WriteString(fmt.Sprintf("  %s %s", glyph, name))
		}
		b.WriteString("\n")
		lines++
	}

	if showBottomHint {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  ▼ %d more", len(m.procs)-limit)) + "\n")
		lines++
	}

	return lines
}
