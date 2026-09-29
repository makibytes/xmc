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
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// ---------- Window kind discriminator ----------

// objWindowKind identifies the type of a sidebar window.
type objWindowKind int

const (
	objWindowObjects objWindowKind = iota // default: ManageSpec object list
	objWindowProcs                        // background process manager
)

// ---------- Constants & styles ----------

const maxProcCapture = 256 * 1024 // 256 KB per background process

var (
	warnTriangleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3")) // yellow ▲ finished ok
	procRunStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("6")) // cyan ● running
	procErrStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("9")) // bright-red ▲ error
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
	doneCh    chan struct{}      // closed by the background goroutine when it exits

	mu         sync.Mutex
	out        cappedBuffer // goroutine writes; UI reads via snapshotText()
	lines      int          // count of '\n' in out
	stderrSeen bool         // process wrote to stderr since the user last viewed its output

	killed bool // stopped by the user (K); UI goroutine only

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

// processName returns a short display name: verb + first positional arg.
// "receive q1 --for 1h" → "receive q1"; "forward q1 q2 --for 5m" → "forward q1".
// With rootCmd, the verb's real flag definitions tell which flags consume the
// next token, so "receive --for 1h q1" is "receive q1", not "receive 1h";
// without it every non-flag token counts as positional.
func processName(command string, rootCmd *cobra.Command) string {
	parts := shellSplit(command)
	if len(parts) == 0 {
		return command
	}
	verb := parts[0]
	var flags *pflag.FlagSet
	if rootCmd != nil {
		if c, _, err := rootCmd.Find([]string{verb}); err == nil && c != rootCmd {
			flags = c.Flags()
		}
	}
	for i := 1; i < len(parts); i++ {
		p := parts[i]
		if p == "--" {
			if i+1 < len(parts) {
				return verb + " " + parts[i+1]
			}
			break
		}
		if strings.HasPrefix(p, "-") && len(p) > 1 {
			if flagTakesValue(flags, p) {
				i++ // skip the flag's value
			}
			continue
		}
		return verb + " " + p
	}
	return verb
}

// flagTakesValue reports whether token (a "--name" or "-x" flag without an
// inline "=value") consumes the following token as its value in flags.
func flagTakesValue(flags *pflag.FlagSet, token string) bool {
	if flags == nil || strings.Contains(token, "=") {
		return false
	}
	var f *pflag.Flag
	if name, ok := strings.CutPrefix(token, "--"); ok {
		f = flags.Lookup(name)
	} else if len(token) == 2 {
		f = flags.ShorthandLookup(token[1:])
	}
	return f != nil && f.NoOptDefVal == ""
}

// segHasFor returns true if a single pipeline stage is a streaming verb (or
// verb alias) with --for or --forever, making it eligible to run as a
// background process.
func segHasFor(segment string) bool {
	parts := shellSplit(stripBinaryPrefix(strings.TrimSpace(segment)))
	if len(parts) == 0 {
		return false
	}
	if !backgroundVerbs[canonicalVerb(strings.ToLower(parts[0]))] {
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

// commandHasFor returns true if any stage of line (aliases expanded) is a
// backgroundable --for/--forever command.
func commandHasFor(line string, aliases map[string]string) bool {
	return anyCommand(line, aliases, segHasFor)
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
		m.focusChat()
	}
	m.objTypes = m.objTypes[:len(m.objTypes)-1]
	m.procWinIdx = -1
	m.recalcLayout()
}

// clampProcSel keeps procSel within the process list after it changed.
func (m *aiTUIModel) clampProcSel() {
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
		name:      processName(command, m.rootCmd),
		command:   command,
		startedAt: time.Now(),
		doneCh:    make(chan struct{}),
	}
	p.out.max = maxProcCapture
	m.procNextID++
	m.procs = append(m.procs, p)
	(&m).ensureProcWindow()
	(&m).clampProcSel()

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
			if p.killed {
				msg.cancel() // K was pressed before the cancel func arrived
			}
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
			p.done = true // UI goroutine only; no lock
			p.err = msg.err
			p.finishedAt = time.Now()
			(&m).clampProcSel()
			// Say so in the transcript — the sidebar glyph alone is easy to
			// miss, and invisible on a terminal too narrow for the sidebar,
			// where the output is shown right away since the Processes
			// window can't be browsed to view it.
			status := histOkStyle.Render("✓")
			switch {
			case p.killed:
				status = dimStyle.Render("(stopped)")
			case msg.err != nil:
				status = warnStyle.Render("✗ " + msg.err.Error())
			}
			m.appendTranscript(dimStyle.Render("↳ background process finished: "+p.name+" ") + status + "\n\n")
			if !m.sidebarVisible() {
				(&m).dumpProcessOutput(p)
			}
			// Trigger sidebar refresh when the process may have changed message counts.
			if anyCommand(p.command, m.aliases(), mutatesMessages) && !m.refreshing && len(m.objTypes) > 0 {
				return m, (&m).beginRefresh()
			}
			return m, nil
		}
	}
	return m, nil
}

// ---------- Key handling ----------

// handleKeyProcessPane handles keyboard events when the Processes window has
// focus. Its keys follow the object windows' conventions: ↑↓/j/k move, Enter
// or p (peek) shows the output, d removes, uppercase keys act more broadly —
// K kills (keeps the entry), P purges all finished entries, D kills and
// removes everything.
func (m aiTUIModel) handleKeyProcessPane(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyUp:
		m.moveProcSel(-1)
		return m, nil

	case tea.KeyDown:
		m.moveProcSel(1)
		return m, nil

	case tea.KeyEnter:
		m.viewSelectedProc()
		return m, nil

	case tea.KeySpace:
		// Space collapses/expands the Processes window.
		if m.procWinIdx >= 0 {
			m.objTypes[m.procWinIdx].collapsed = !m.objTypes[m.procWinIdx].collapsed
		}
		return m, nil

	case tea.KeyEsc:
		m.focusChat()
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
		case "j":
			m.moveProcSel(1)

		case "k":
			m.moveProcSel(-1)

		case "p": // peek at the output, like p on an object window
			m.viewSelectedProc()

		case "d": // remove selected (kill first if running)
			if len(m.procs) == 0 {
				return m, nil
			}
			p := m.procs[m.procSel]
			if p.cancel != nil {
				p.cancel()
			}
			m.procs = append(m.procs[:m.procSel], m.procs[m.procSel+1:]...)
			(&m).clampProcSel()
			if len(m.procs) == 0 {
				(&m).removeProcWindow()
			} else {
				(&m).updateProcPrompt()
			}

		case "K": // kill but keep in list (its output stays viewable)
			if len(m.procs) > 0 && m.procSel < len(m.procs) {
				// The cancel func may not have arrived yet (procCancelMsg);
				// handleProcCancelMsg then fires it on arrival.
				if p := m.procs[m.procSel]; !p.done {
					p.killed = true
					if p.cancel != nil {
						p.cancel()
					}
				}
			}

		case "P": // purge all finished processes
			var keep []*bgProcess
			for _, p := range m.procs {
				if !p.done { // done is UI-goroutine-only; no lock needed
					keep = append(keep, p)
				}
			}
			m.procs = keep
			(&m).clampProcSel()
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

// moveProcSel moves the Processes window selection by delta.
func (m *aiTUIModel) moveProcSel(delta int) {
	if len(m.procs) == 0 {
		return
	}
	m.procSel = min(max(m.procSel+delta, 0), len(m.procs)-1)
	m.updateProcPrompt()
}

// viewSelectedProc appends the selected process's output to the transcript.
func (m *aiTUIModel) viewSelectedProc() {
	if m.procSel >= 0 && m.procSel < len(m.procs) {
		m.dumpProcessOutput(m.procs[m.procSel])
	}
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
	// Always re-color to the cmd theme (blue) regardless of the branch above:
	// the label text is always "<binary>>" in process view, so its color must
	// always be themeCmd, even when m.mode was already modeCmd (in which case
	// the text didn't need to change, but the color must still match it).
	m.setPromptTheme(themeCmd)
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
	// Re-color to match the restored mode (petrol for ask>, blue for
	// <binary>>) — must track the text set just above, not the cmd-theme
	// color left over from process view.
	m.setPromptTheme(m.theme())
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
	headerText := fmt.Sprintf("Processes (%d)", len(m.procs))

	renderRow := func(i int, selected bool) string {
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

		if selected {
			return sidebarSelStyle.Render(fmt.Sprintf("▸ %s %s", glyph, name))
		}
		return fmt.Sprintf("  %s %s", glyph, name)
	}

	return m.writeWindow(b, width, bodyLines, collapsed, headerText, focused, "", len(m.procs), m.procSel, renderRow, nil)
}
