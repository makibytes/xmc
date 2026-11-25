package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	"gopkg.in/yaml.v3"
)

// pickerState holds the state for an interactive selection list (Claude Code-style).
type pickerState struct {
	title    string                       // heading shown above the list
	items    []string                     // selectable labels
	sel      int                          // currently highlighted index
	current  int                          // index of the active item (-1 if none matches)
	onSelect func(m *aiTUIModel, idx int) // called on Enter with the chosen index
}

// pickerSelectedStyle is the highlighted item in the picker.
var pickerSelectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))

// ---------- Proposal shimmer ----------

// shimmerBand is the number of "lit" runes in the shimmer highlight band.
const shimmerBand = 4

// ---------- Copy-to-clipboard helpers ----------

const copyMarker = "⧉"

// ---------- Key handling ----------

func (m aiTUIModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.promptActive {
		return m.handleKeyPrompt(msg)
	}

	switch m.state {
	case tuiIdle:
		return m.handleKeyIdle(msg)
	case tuiThinking:
		return m.handleKeyThinking(msg)
	case tuiProposing:
		return m.handleKeyProposing(msg)
	case tuiEditing:
		return m.handleKeyEditing(msg)
	case tuiExecuting:
		return m.handleKeyExecuting(msg)
	case tuiPicking:
		return m.handleKeyPicking(msg)
	}
	return m, nil
}

func (m aiTUIModel) handleKeyPicking(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.picker
	if p == nil {
		m.toIdle()
		return m, nil
	}
	switch msg.Type {
	case tea.KeyUp:
		if p.sel > 0 {
			p.sel--
			m.setViewportContent()
		}
		return m, nil
	case tea.KeyDown:
		if p.sel < len(p.items)-1 {
			p.sel++
			m.setViewportContent()
		}
		return m, nil
	case tea.KeyEnter:
		p.onSelect(&m, p.sel)
		m.picker = nil
		m.toIdle()
		m.setViewportContent()
		return m, nil
	case tea.KeyEsc, tea.KeyCtrlC:
		m.appendTranscript(dimStyle.Render("(cancelled)") + "\n\n")
		m.picker = nil
		m.toIdle()
		return m, nil
	}
	return m, nil
}

func (m aiTUIModel) handleKeyIdle(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Shift+Tab cycles focus backward (chat → last window → … → first window → chat).
	if msg.Type == tea.KeyShiftTab {
		m.cycleFocus(false)
		return m, nil
	}

	// When a sidebar pane is focused, delegate.
	if m.focus != focusChat {
		if m.filtering {
			return m.handleKeyFilter(msg)
		}
		// Ctrl+C quits from anywhere there is nothing to cancel — a sidebar
		// window has no input line to clear.
		if msg.Type == tea.KeyCtrlC {
			return m.quit()
		}
		// Route process pane keys to the dedicated handler.
		if m.procsFocused() {
			return m.handleKeyProcessPane(msg)
		}
		return m.handleKeyPane(msg)
	}

	switch msg.Type {
	case tea.KeyCtrlC:
		// Like an interactive shell: Ctrl+C discards the line being typed;
		// on an empty line it quits.
		if m.input.Value() != "" {
			m.clearInput()
			return m, nil
		}
		return m.quit()

	case tea.KeyCtrlD:
		// EOF on an empty line quits (as in the shell); otherwise the
		// textarea deletes the character under the cursor.
		if m.input.Value() == "" {
			return m.quit()
		}

	case tea.KeyCtrlL:
		m.clearTranscript()
		return m, nil

	case tea.KeyEsc:
		// Esc in chat toggles between AI and command mode.
		m.toggleInputMode()
		return m, nil

	case tea.KeyTab:
		if m.mode == modeCmd {
			m.doAutocomplete()
		} else {
			// Tab in AI mode → cycle sidebar focus forward.
			m.cycleFocus(true)
		}
		return m, nil

	case tea.KeyUp:
		m.historyPrev()
		m.updateInputHeight()
		return m, nil

	case tea.KeyDown:
		m.historyNext()
		m.updateInputHeight()
		return m, nil

	case tea.KeyEnter:
		prompt := strings.TrimSpace(m.input.Value())
		if prompt == "" {
			return m, nil
		}

		// Well-formedness check: reject a trailing bare '&' (use --for instead).
		// Check BEFORE resetting the input so the user's text is preserved on rejection.
		if m.mode == modeCmd && !strings.HasPrefix(prompt, "/") && !commandWellFormed(prompt) {
			m.appendTranscript(warnStyle.Render("✗ trailing & is not allowed — use --for <duration> to run in the background") + "\n\n")
			return m, nil
		}

		m.clearInput()

		// Slash commands (work in both modes).
		if strings.HasPrefix(prompt, "/") {
			return m.handleSlashCommand(prompt)
		}

		if m.mode == modeCmd {
			// Direct command execution (shell-like).
			if prompt == "exit" || prompt == "quit" {
				return m.quit()
			}
			m.cmdHistory = append(m.cmdHistory, prompt)
			// The echo is the command's single transcript line (with its ⧉
			// copy marker); the output follows directly underneath.
			m.copyItems = append(m.copyItems, prompt)
			m.appendTranscript(cmdStyle.Render(m.binaryName+"> ") + prompt + copyHintStyle.Render(" ⧉") + "\n")
			m.proposedCmd = prompt
			return m.runCommand(prompt)
		}

		// AI mode: send to the model.
		m.askHistory = append(m.askHistory, prompt)
		askHistory.Append(prompt)
		m.appendTranscript(userStyle.Render("you: ") + prompt + "\n\n")

		m.ai.history = append(m.ai.history, aiMessage{Role: "user", Content: prompt})
		trimHistory(&m.ai.history, maxHistory)

		m.fixAttempts = 0
		request := m.beginAIRequest() // a statement of its own: it mutates m
		return m, request
	}

	// Everything else edits the input. Pre-size BEFORE forwarding the key:
	// resize the textarea so that repositionView() inside Update() sees the
	// correct height and doesn't scroll content out of view when the text
	// wraps to a new visual row. predictValue avoids calling Update() twice
	// (which caused double-insertion due to shared backing arrays in the
	// textarea's [][]rune value).
	if n := m.computeInputLines(predictValue(m.input.Value(), msg)); n != m.input.Height() {
		m.input.SetHeight(n)
		m.recalcLayout()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.updateInputHeight()
	return m, cmd
}

// quit ends the session, cancelling any background processes first.
func (m aiTUIModel) quit() (tea.Model, tea.Cmd) {
	m.killAllProcs()
	m.quitting = true
	return m, tea.Quit
}

// clearInput empties the text input and leaves history navigation.
func (m *aiTUIModel) clearInput() {
	m.input.Reset()
	m.histIdx = -1
	m.input.SetHeight(1)
	m.recalcLayout()
}

// toIdle returns to the idle state with the input focused.
func (m *aiTUIModel) toIdle() {
	m.state = tuiIdle
	m.input.Focus()
}

// noteSaved prints the confirmation of a setting change in the transcript,
// flagging a failed write of it to the config file.
func (m *aiTUIModel) noteSaved(msg string, saveErr error) {
	if saveErr != nil {
		msg += fmt.Sprintf(" (save failed: %s)", saveErr)
	}
	m.appendTranscript(dimStyle.Render(msg) + "\n\n")
}

// setMetadataFormat switches the sidebar's metadata display format (J/Y) and
// persists the choice; choosing the format already in effect does nothing.
func (m *aiTUIModel) setMetadataFormat(format metadataFormat) {
	if m.metadataFormat == format {
		return
	}
	m.metadataFormat = format
	m.noteSaved("metadata format → "+string(format), saveMetadataFormat(format))
}

// runCommand runs a command line the user accepted: one with --for becomes a
// managed background process, anything else runs in the foreground.
func (m aiTUIModel) runCommand(command string) (tea.Model, tea.Cmd) {
	if commandHasFor(command, m.aliases()) {
		return m.startBackgroundProcess(command)
	}
	return m.startExecution(command)
}

// discardProposal records that the user discarded the proposed command, in
// the transcript (grey ✗) and in the conversation, so the model knows it was
// not run.
func (m *aiTUIModel) discardProposal() {
	m.appendTranscript(freezeProposal(m.proposedCmd, "✗", false))
	m.ai.history = append(m.ai.history,
		aiMessage{Role: "assistant", Content: m.proposedCmd},
		aiMessage{Role: "user", Content: "[user discarded the command]"})
}

// clearTranscript empties the conversation display (/clear, Ctrl+L). The
// clipboard items go with it: ⧉ markers are resolved by counting them from
// the top of the transcript, so stale items would make a click copy the
// wrong thing.
func (m *aiTUIModel) clearTranscript() {
	m.transcript.Reset()
	m.copyItems = nil
	m.setViewportContent()
}

// focusChat returns keyboard focus from a sidebar window to the input.
func (m *aiTUIModel) focusChat() {
	if m.procsFocused() {
		m.exitProcessView()
	}
	m.focus = focusChat
	m.filtering = false
	m.input.Focus()
}

// insertAtCursor inserts text (a sidebar object name) at the input's cursor,
// adding a separating space on either side where it would otherwise run into
// a neighbouring word — like shell completion, so "send " + orders + " hi"
// composes naturally instead of replacing what was already typed.
func (m *aiTUIModel) insertAtCursor(text string) {
	lines := strings.Split(m.input.Value(), "\n")
	row := min(max(m.input.Line(), 0), len(lines)-1)
	runes := []rune(lines[row])
	li := m.input.LineInfo()
	col := min(max(li.StartColumn+li.ColumnOffset, 0), len(runes))
	if col > 0 && !unicode.IsSpace(runes[col-1]) {
		text = " " + text
	}
	if col == len(runes) || !unicode.IsSpace(runes[col]) {
		text += " "
	}
	m.input.InsertString(text)
}

// shellQuote returns name unchanged when it is a plain shell word, or wrapped
// in single quotes otherwise, so an inserted object name survives the
// command-line splitter (shellSplit) as one argument.
func shellQuote(name string) string {
	isPlain := func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._-:/@%+=,#", r)
	}
	if name != "" && strings.IndexFunc(name, func(r rune) bool { return !isPlain(r) }) < 0 {
		return name
	}
	return "'" + strings.ReplaceAll(name, "'", `'"'"'`) + "'"
}

func (m aiTUIModel) handleSlashCommand(input string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(input)
	cmd := strings.ToLower(parts[0])
	arg := ""
	if len(parts) > 1 {
		arg = strings.Join(parts[1:], " ")
	}

	switch cmd {
	case "/help":
		m.appendTranscript(dimStyle.Render(aiHelpText(m.binaryName)) + "\n")

	case "/exit", "/quit":
		return m.quit()

	case "/reset":
		// The session token totals (status bar, exit summary) are kept:
		// /reset starts a new conversation, it doesn't undo what was spent.
		m.ai.history = nil
		m.fixAttempts = 0
		m.clearTranscript()
		m.appendTranscript(dimStyle.Render("(conversation reset)") + "\n\n")

	case "/refresh":
		if arg != "" {
			// /refresh <interval> — change periodic refresh interval.
			d, enabled, err := parseRefreshInterval(arg)
			if err != nil {
				m.appendTranscript(warnStyle.Render(err.Error()) + "\n\n")
				return m, nil
			}
			wasEnabled := m.refreshEnabled
			m.refreshEnabled = enabled
			if enabled {
				m.refreshPeriod = d
			}
			// Persist to config.
			persistVal := formatRefreshInterval(d, enabled)
			m.noteSaved("periodic refresh → "+persistVal, saveRefreshInterval(persistVal))
			// If turned on and not already refreshing, kick off the loop.
			if enabled && !wasEnabled && !m.refreshing && len(m.objTypes) > 0 {
				refresh := m.beginRefresh()
				return m, refresh
			}
			return m, nil
		}
		// No arg: manual one-shot reload.
		if m.connDown() {
			m.appendTranscript(warnStyle.Render("broker unreachable — "+m.conn.statusLine()) + "\n\n")
			return m, nil
		}
		if len(m.objTypes) > 0 {
			m.appendTranscript(dimStyle.Render("refreshing…") + "\n\n")
			m.loadingObjects = true
			return m, m.startLoadObjects()
		}
		m.appendTranscript(dimStyle.Render("(no management API)") + "\n\n")

	case "/disconnect":
		if m.conn.reconnecting {
			m.disconnect()
			m.appendTranscript(dimStyle.Render("Auto-reconnect disabled. Use /connect to reconnect manually.") + "\n\n")
		} else {
			m.appendTranscript(dimStyle.Render("Not currently reconnecting.") + "\n\n")
		}

	case "/connect":
		if m.session == nil || m.session.spec.Ping == nil {
			m.appendTranscript(warnStyle.Render("No connection probe available for this broker.") + "\n\n")
			return m, nil
		}
		if m.conn.err == nil && !m.conn.reconnecting {
			m.appendTranscript(dimStyle.Render("Already connected.") + "\n\n")
			return m, nil
		}
		m.appendTranscript(dimStyle.Render("Connecting…") + "\n\n")
		probe := m.reconnectNow()
		return m, probe

	case "/clear":
		m.clearTranscript()

	case "/model":
		if arg == "" {
			m.appendTranscript(dimStyle.Render(fmt.Sprintf("current: %s · %s", m.ai.modelName, m.ai.providerName)) + "\n")
			m.appendTranscript(dimStyle.Render("fetching models…") + "\n\n")
			m.fetchingModels = true
			return m, m.startListModels()
		}
		m.selectModel(arg)

	case "/effort":
		if arg == "" {
			// Open interactive effort picker.
			effortLevels := []string{string(effortLow), string(effortMedium), string(effortHigh)}
			currentIdx := 0
			for i, e := range effortLevels {
				if aiEffort(e) == m.ai.currentEffort() {
					currentIdx = i
				}
			}
			m.picker = &pickerState{
				title:   "Select effort level:",
				items:   effortLevels,
				sel:     currentIdx,
				current: currentIdx,
				onSelect: func(model *aiTUIModel, idx int) {
					model.applyEffort(effortLevels[idx])
				},
			}
			m.state = tuiPicking
			m.input.Blur()
			m.setViewportContent()
			return m, nil
		}
		m.applyEffort(arg)

	default:
		m.appendTranscript(warnStyle.Render("unknown command: "+cmd+" (type /help)") + "\n\n")
	}

	return m, nil
}

// aiHelpText is the /help reference card: slash commands, then every key
// binding grouped by where it applies.
func aiHelpText(binaryName string) string {
	return `Slash commands
  /model [name]          pick a model, or switch directly (saved to config)
  /effort [low|med|high] pick reasoning effort, or set directly (saved to config)
  /refresh [dur|off]     reload the sidebar now · set the periodic interval (min 1s) · disable it
  /connect · /disconnect reconnect to the broker now · stop auto-reconnect
  /reset                 start a new conversation
  /clear                 clear the display (also Ctrl+L)
  /exit                  quit (also Ctrl+C or Ctrl+D on an empty line)

Input
  Enter          ask the AI (ask>) · run the command (` + binaryName + `>)
  Esc            toggle between ask> and ` + binaryName + `> mode
  Tab            complete (` + binaryName + `>) · browse the sidebar (ask>)
  Shift+Tab      browse the sidebar
  Up/Down        history of the current mode
  Ctrl+C         clear the line; quit when it is empty
  PgUp/PgDn      scroll · Home/End jump to top/bottom (when the input is empty)
  click ⧉        copy that command or payload to the clipboard
  --for <dur>    run a streaming command (receive, subscribe, forward, …) in the background

Proposed command
  Enter run · e edit · c discuss instead of running · Esc discard

Sidebar window
  ↑↓/j/k move · Enter insert name at cursor · / filter · s sort · x tree · Space collapse · r refresh · Esc back
  c create · d delete · p peek · m peek metadata (J/Y: JSON/YAML) · P purge (publish on topics)
  S send · R receive — the status bar lists the keys that apply to the selected row

Processes window
  ↑↓/j/k move · Enter/p show output · K kill · d remove · P purge finished · D remove all
`
}

func (m aiTUIModel) handleKeyThinking(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyEsc || msg.Type == tea.KeyCtrlC {
		if m.aiCancel != nil {
			m.aiCancel()
			m.aiCancel = nil
		}
		m.aiGen++ // whatever the cancelled request still reports is stale
		m.appendTranscript(dimStyle.Render("(cancelled)") + "\n\n")
		m.dropPendingUserTurn()
		m.toIdle()
		return m, nil
	}
	return m, nil
}

func (m aiTUIModel) handleKeyProposing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.discardProposal()
		m.toIdle()
		return m, nil

	case tea.KeyEnter:
		// Freeze with a green ✓ marker, then execute (or background if --for).
		m.acceptProposal(m.proposedCmd)
		return m.runCommand(m.proposedCmd)

	case tea.KeyRunes:
		switch msg.String() {
		case "e":
			// Switch to editing sub-state — shimmer continues while the user types.
			m.input.SetValue(m.proposedCmd)
			m.state = tuiEditing
			m.input.Focus()
			m.updateInputHeight()
			return m, nil
		case "c":
			// Freeze with a yellow ? marker to mark it as a follow-up topic.
			m.appendTranscript(freezeProposal(m.proposedCmd, "?", m.proposedDestructive))
			m.ai.history = append(m.ai.history, aiMessage{Role: "assistant", Content: m.proposedCmd})
			m.ai.history = append(m.ai.history, aiMessage{Role: "user", Content: "[command NOT executed — user wants to discuss or refine it before running]"})
			m.input.SetValue("")
			m.toIdle()
			m.updateInputHeight()
			return m, nil
		}
	}
	return m, nil
}

// handleKeyEditing handles keyboard events in tuiEditing state: the user is
// refining the proposed command. Enter accepts and runs it; Esc cancels.
// Any other key is forwarded to the textarea so the user can edit freely.
func (m aiTUIModel) handleKeyEditing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		cmd := strings.TrimSpace(m.input.Value())
		if cmd == "" {
			return m, nil
		}
		m.proposedCmd = cmd
		m.proposedDestructive = anyCommand(cmd, m.aliases(), isDestructive)
		// Freeze with a green ✓ and run (or background if --for).
		m.acceptProposal(cmd)
		m.input.SetValue("")
		m.input.SetHeight(1)
		m.recalcLayout()
		return m.runCommand(cmd)

	case tea.KeyEsc, tea.KeyCtrlC:
		// Recorded in the conversation exactly like discarding the unedited
		// proposal, so the model knows its command was not run.
		m.discardProposal()
		m.input.SetValue("")
		m.input.SetHeight(1)
		m.toIdle()
		m.recalcLayout()
		return m, nil

	default:
		if n := m.computeInputLines(predictValue(m.input.Value(), msg)); n != m.input.Height() {
			m.input.SetHeight(n)
			m.recalcLayout()
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.updateInputHeight()
		return m, cmd
	}
}

// handleKeyPane processes keys when a sidebar pane has focus.
func (m aiTUIModel) handleKeyPane(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	wi, ok := m.focusedWindow()
	if !ok {
		return m, nil
	}

	switch msg.Type {
	case tea.KeyEsc:
		m.focusChat()
		return m, nil
	case tea.KeyUp:
		m.moveSel(-1)
		return m, nil
	case tea.KeyDown:
		m.moveSel(1)
		return m, nil
	case tea.KeyEnter:
		if name := m.selectedName(); name != "" {
			m.focusChat()
			m.insertAtCursor(shellQuote(name))
			m.updateInputHeight()
		}
		return m, nil
	case tea.KeySpace:
		// Space collapses/expands the whole window (title only vs. full content).
		m.objTypes[wi].collapsed = !m.objTypes[wi].collapsed
		return m, nil
	case tea.KeyTab:
		m.cycleFocus(true)
		return m, nil
	case tea.KeyRunes:
		switch msg.String() {
		case "j":
			m.moveSel(1)
			return m, nil
		case "k":
			m.moveSel(-1)
			return m, nil
		case "/":
			m.filtering = true
			return m, nil
		case "J":
			m.setMetadataFormat(metadataFormatJSON)
			return m, nil
		case "Y":
			m.setMetadataFormat(metadataFormatYAML)
			return m, nil
		case "s":
			m.cycleSort()
			return m, nil
		case "r":
			if m.connDown() && m.session != nil && m.session.spec.Ping != nil {
				// A refresh can't succeed before the connection is back, and
				// the reconnect countdown is the one clock: fire it now.
				m.appendTranscript(dimStyle.Render("Connecting…") + "\n\n")
				probe := m.reconnectNow()
				return m, probe
			}
			m.loadingObjects = true
			return m, m.startLoadObjects()
		case "x":
			// x toggles the hierarchical tree-view (show/hide children). Toggling
			// changes which row index sel points at (children insert rows between
			// top-level items), so re-resolve sel by node identity afterward
			// instead of leaving the raw index pointing at a now-different row.
			if m.objTypes[wi].hierarchical {
				selectedName := m.selectedName()
				m.objTypes[wi].treeView = !m.objTypes[wi].treeView
				m.objTypes[wi].sel = indexOfRowNamed(m.sidebarRows(wi), selectedName)
			}
			return m, nil
		case "c", "d", "p", "m", "P", "S", "R":
			// All selection-dependent sidebar object hotkeys share one
			// eligibility+behavior table (cmd/aisidebaractions.go) with the
			// status-bar hint renderer, so the two can't drift out of sync.
			if a, ok := lookupSidebarAction(msg.String()); ok {
				if _, run, ok := a.resolve(&m, wi); ok {
					return run()
				}
			}
			return m, nil
		}
	}
	return m, nil
}

// handleKeyFilter processes keys while the inline filter is active.
func (m aiTUIModel) handleKeyFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	wi, ok := m.focusedWindow()
	if !ok {
		return m, nil
	}

	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.filtering = false
		m.objTypes[wi].filter = ""
		m.objTypes[wi].sel = 0
		return m, nil
	case tea.KeyEnter:
		m.filtering = false
		return m, nil
	case tea.KeyBackspace:
		if len(m.objTypes[wi].filter) > 0 {
			m.objTypes[wi].filter = dropLastRune(m.objTypes[wi].filter)
			m.objTypes[wi].sel = 0
		}
		return m, nil
	case tea.KeyCtrlU:
		m.objTypes[wi].filter = ""
		m.objTypes[wi].sel = 0
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		text := " "
		if msg.Type == tea.KeyRunes {
			text = typedText(msg)
		}
		m.objTypes[wi].filter += text
		m.objTypes[wi].sel = 0
		return m, nil
	}
	return m, nil
}

// ---------- Sidebar create/delete prompt ----------

func (m *aiTUIModel) startPrompt(kind string, objIdx int, name string) {
	m.promptActive = true
	m.promptKind = kind
	m.promptObjIdx = objIdx
	m.promptName = name
	m.input.Blur()
}

// promptSpec describes one sidebar prompt kind's key handling: whether it
// accepts free-text input beyond Enter/Esc (and whether that includes a
// literal space), and what happens on Enter. confirm returns (m, nil)
// unmodified-except-for-blur to no-op when the required action/hook isn't
// wired. Every kind startPrompt is ever called with has an entry here.
type promptSpec struct {
	textEntry  bool // Backspace/Runes accepted (create/send/publish); not delete/purge/purge-subscription, which just confirm a fixed name
	allowSpace bool // also accept KeySpace as input (send/publish only — a payload may contain spaces; create's name may not)
	confirm    func(m aiTUIModel, ow *objWindow) (tea.Model, tea.Cmd)
}

func promptSpecs() map[string]promptSpec {
	return map[string]promptSpec{
		"create":             {textEntry: true, confirm: confirmCreatePrompt},
		"delete":             {confirm: confirmDeletePrompt},
		"purge":              {confirm: confirmPurgePrompt},
		"purge-subscription": {confirm: confirmPurgeSubscriptionPrompt},
		"send":               {textEntry: true, allowSpace: true, confirm: confirmSendPrompt},
		"publish":            {textEntry: true, allowSpace: true, confirm: confirmPublishPrompt},
	}
}

// sideActionTimeout bounds the broker round trip of one single-key sidebar
// action (peek, receive, send, publish).
const sideActionTimeout = 5 * time.Second

// startSideAction echoes desc into the transcript, ends the active prompt (if
// any), switches to the executing state and returns the tea.Cmd that runs work.
// work gets a context that Esc cancels (create/delete/purge call the broker's
// management API without one, so for those only the UI-side abandon applies);
// a panic in it, or a result arriving after the UI gave up on the action
// (abandonExec), never reaches the transcript.
func (m *aiTUIModel) startSideAction(desc string, work func(ctx context.Context) sideActionMsg) tea.Cmd {
	m.appendTranscript(histCmdStyle.Render(desc) + "\n")
	m.promptActive = false
	m.state = tuiExecuting
	ctx, cancel := context.WithCancel(context.Background())
	m.execCancel = cancel
	m.execCancelled = false
	m.execGen++
	gen := m.execGen
	return guard(func(err error) tea.Msg { return sideActionMsg{err: err, gen: gen} }, func() tea.Msg {
		defer cancel()
		msg := work(ctx)
		msg.gen = gen
		return msg
	})
}

// closePrompt ends the active sidebar prompt without running anything.
func (m *aiTUIModel) closePrompt() {
	m.promptActive = false
	m.restoreInputFocus()
}

// manageSpec returns the broker's management spec, or nil when there is no
// session or the broker declares none.
func (m *aiTUIModel) manageSpec() *ManageSpec {
	if m.session == nil {
		return nil
	}
	return m.session.spec.ManageSpec
}

// purgedMsg reports the outcome of a queue or subscription purge.
func purgedMsg(count int64, err error) sideActionMsg {
	switch {
	case err != nil:
		return sideActionMsg{err: err}
	case count > 0:
		return sideActionMsg{action: fmt.Sprintf("   └ purged %d messages", count)}
	default:
		return sideActionMsg{action: "   └ purged"}
	}
}

func confirmCreatePrompt(m aiTUIModel, ow *objWindow) (tea.Model, tea.Cmd) {
	name := strings.TrimSpace(m.promptName)
	if name == "" {
		return m, nil
	}
	action := ow.createAction
	if action == nil {
		m.closePrompt()
		return m, nil
	}
	initManageAction(action)
	cmd := m.startSideAction(fmt.Sprintf("▶ create %s \"%s\"", ow.singularLabel(), name), func(context.Context) sideActionMsg {
		return sideActionMsg{err: action.Run(name), objects: true}
	})
	return m, cmd
}

func confirmDeletePrompt(m aiTUIModel, ow *objWindow) (tea.Model, tea.Cmd) {
	action := ow.deleteAction
	if action == nil {
		m.closePrompt()
		return m, nil
	}
	name := m.promptName
	initManageAction(action)
	cmd := m.startSideAction(fmt.Sprintf("▶ delete %s \"%s\"", ow.singularLabel(), name), func(context.Context) sideActionMsg {
		return sideActionMsg{err: action.Run(name), objects: true}
	})
	return m, cmd
}

func confirmPurgePrompt(m aiTUIModel, ow *objWindow) (tea.Model, tea.Cmd) {
	ms := m.manageSpec()
	if ms == nil || ms.Purge == nil {
		m.closePrompt()
		return m, nil
	}
	name, purge := m.promptName, ms.Purge
	cmd := m.startSideAction(fmt.Sprintf("▶ purge %s \"%s\"", ow.singularLabel(), name), func(context.Context) sideActionMsg {
		return purgedMsg(purge(name))
	})
	return m, cmd
}

func confirmPurgeSubscriptionPrompt(m aiTUIModel, _ *objWindow) (tea.Model, tea.Cmd) {
	ms := m.manageSpec()
	if ms == nil || ms.PurgeSubscription == nil {
		m.closePrompt()
		return m, nil
	}
	sub, topic, purge := m.promptName, m.promptTarget, ms.PurgeSubscription
	cmd := m.startSideAction(fmt.Sprintf("▶ purge Subscription \"%s\"", sub), func(context.Context) sideActionMsg {
		return purgedMsg(purge(topic, sub))
	})
	return m, cmd
}

// confirmSendPrompt sends the typed payload to the selected object: through
// the topic adapter when the window sends via topic (RabbitMQ exchange:
// publish -e <exchange>, which targets the routing entity itself), else
// through the queue adapter.
func confirmSendPrompt(m aiTUIModel, ow *objWindow) (tea.Model, tea.Cmd) {
	useTopic := ow.sendsViaTopic(m.promptNodeKind)
	return m.confirmProduce(ow, useTopic, useTopic)
}

// confirmPublishPrompt publishes the typed payload to the selected topic.
func confirmPublishPrompt(m aiTUIModel, ow *objWindow) (tea.Model, tea.Cmd) {
	return m.confirmProduce(ow, true, false)
}

// confirmProduce sends (queue adapter) or publishes (useTopic) the prompt's
// payload to the prompt's target; viaExchange addresses the routing entity
// itself (see sidebarTarget).
func (m aiTUIModel) confirmProduce(ow *objWindow, useTopic, viaExchange bool) (tea.Model, tea.Cmd) {
	payload := m.promptName
	if payload == "" {
		return m, nil
	}
	name := m.promptTarget
	verb := "send"
	if useTopic {
		verb = "publish"
	}
	target, resolveErr := m.sidebarTarget(name, useTopic, viaExchange)
	session := m.session
	cmd := m.startSideAction(fmt.Sprintf("▶ %s %s \"%s\"", verb, ow.singularLabel(), name), func(ctx context.Context) sideActionMsg {
		if resolveErr != nil {
			return sideActionMsg{err: resolveErr}
		}
		ctx, cancel := context.WithTimeout(ctx, sideActionTimeout)
		defer cancel()
		if useTopic {
			ta, err := session.getTopicAdapter()
			if err != nil {
				return sideActionMsg{err: fmt.Errorf("adapter: %w", err)}
			}
			if err := ta.Publish(ctx, backends.PublishOptions{Topic: target, Message: []byte(payload)}); err != nil {
				return sideActionMsg{err: err}
			}
			return sideActionMsg{action: "   └ published"}
		}
		qa, err := session.getQueueAdapter()
		if err != nil {
			return sideActionMsg{err: fmt.Errorf("adapter: %w", err)}
		}
		if err := qa.Send(ctx, backends.SendOptions{Queue: target, Message: []byte(payload)}); err != nil {
			return sideActionMsg{err: err}
		}
		return sideActionMsg{action: "   └ sent"}
	})
	return m, cmd
}

func (m aiTUIModel) handleKeyPrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	wi := m.promptObjIdx
	if wi < 0 || wi >= len(m.objTypes) {
		m.closePrompt()
		return m, nil
	}
	ow := &m.objTypes[wi]

	spec := promptSpecs()[m.promptKind]

	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.closePrompt()
		return m, nil
	case tea.KeyEnter:
		return spec.confirm(m, ow)
	case tea.KeyBackspace:
		if spec.textEntry {
			m.promptName = dropLastRune(m.promptName)
		}
		return m, nil
	case tea.KeyCtrlU:
		if spec.textEntry {
			m.promptName = ""
		}
		return m, nil
	case tea.KeyRunes:
		if spec.textEntry {
			text := typedText(msg)
			if !spec.allowSpace {
				text = strings.Join(strings.Fields(text), "") // a pasted name can't carry spaces
			}
			m.promptName += text
		}
		return m, nil
	case tea.KeySpace:
		if spec.textEntry && spec.allowSpace {
			m.promptName += " "
		}
		return m, nil
	}
	return m, nil
}

// typedText returns the text a KeyRunes event inserts. Unlike msg.String(),
// it never includes bubbletea's "[…]" paste brackets or an "alt+" prefix —
// a pasted payload must arrive verbatim, and an Alt-chord inserts nothing.
// Pasted newlines become spaces: sidebar prompts are single-line.
func typedText(msg tea.KeyMsg) string {
	if msg.Alt {
		return ""
	}
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, string(msg.Runes))
}

// dropLastRune removes the last character (not byte) of s, so Backspace never
// leaves a broken UTF-8 sequence behind in a payload or filter.
func dropLastRune(s string) string {
	_, size := utf8.DecodeLastRuneInString(s)
	return s[:len(s)-size]
}

// restoreInputFocus re-focuses the text input after a sidebar prompt or
// action ends — but only when the chat has focus. Focusing it while a
// sidebar window still owns the keyboard would show a blinking cursor in the
// input that keystrokes never reach.
func (m *aiTUIModel) restoreInputFocus() {
	if m.focus == focusChat {
		m.input.Focus()
	}
}

// initManageAction calls SetupFlags on a throwaway command to initialise
// default flag-bound variables before calling Run.
func initManageAction(a *ManageAction) {
	if a == nil || a.SetupFlags == nil {
		return
	}
	a.SetupFlags(&cobra.Command{})
}

func formatMessagePayloadForSideAction(msg *backends.Message) sideActionMsg {
	payload := strings.TrimRight(string(msg.Data), "\n\r\t ")
	if payload == "" {
		return sideActionMsg{action: "   └ (empty payload)"}
	}
	return sideActionMsg{
		action: renderMessagePayload(payload),
		copy:   payload,
	}
}

func normalizeMessageMetadata(msg *backends.Message) (map[string]any, error) {
	return recordAsMap(recordForDisplay(msg, false, true))
}

func formatMessageMetadata(msg *backends.Message, format metadataFormat) (string, error) {
	normalized, err := normalizeMessageMetadata(msg)
	if err != nil {
		return "", err
	}
	switch format {
	case metadataFormatJSON:
		b, err := json.MarshalIndent(normalized, "", "  ")
		if err != nil {
			return "", err
		}
		return string(b), nil
	default:
		b, err := yaml.Marshal(normalized)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(b), "\n"), nil
	}
}

func formatMessageMetadataForSideAction(msg *backends.Message, format metadataFormat) sideActionMsg {
	rendered, err := formatMessageMetadata(msg, format)
	if err != nil {
		return sideActionMsg{err: err}
	}
	return sideActionMsg{
		action: renderMessagePayload(rendered),
		copy:   rendered,
	}
}

// handleKeyExecuting: Esc/Ctrl+C cancels the running command or sidebar action.
// A well-behaved one returns within milliseconds; if it ignores its context
// (a blocked broker driver) the UI stops waiting — after execAbandonAfter, or
// at once on a second press — instead of staying "Running…" for good.
func (m aiTUIModel) handleKeyExecuting(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type != tea.KeyEsc && msg.Type != tea.KeyCtrlC {
		return m, nil
	}
	if m.execCancel != nil {
		m.execCancel()
	}
	if m.execCancelled {
		return m.abandonExec() // pressed again: stop waiting now
	}
	m.execCancelled = true
	gen := m.execGen
	return m, tea.Tick(execAbandonAfter, func(time.Time) tea.Msg { return execAbandonMsg{gen: gen} })
}

// ---------- Input prompt helpers ----------

// applyPromptFunc configures ta so that only the first visual line shows the
// prompt label; all wrapped continuation lines receive indent spaces of the
// same width. Must be called before SetWidth so the textarea can account for
// promptWidth in its layout.
func applyPromptFunc(ta *textarea.Model, text string) {
	w := len([]rune(text))
	indent := strings.Repeat(" ", w)
	ta.SetPromptFunc(w, func(lineIdx int) string {
		if lineIdx == 0 {
			return text
		}
		return indent
	})
}

// predictValue estimates the textarea value after applying msg without calling
// Update() — used for pre-sizing the textarea height before the actual Update
// so the resize happens in the right order. Cursor position is not modelled
// precisely; only total length matters for row-count calculation.
func predictValue(current string, msg tea.KeyMsg) string {
	switch msg.Type {
	case tea.KeyRunes:
		return current + string(msg.Runes)
	case tea.KeyBackspace, tea.KeyDelete:
		runes := []rune(current)
		if len(runes) > 0 {
			return string(runes[:len(runes)-1])
		}
	}
	return current
}

// ---------- Broker objects ----------

func (m aiTUIModel) startLoadObjects() tea.Cmd {
	// Capture the list functions so they can be called from the background.
	type listFn struct {
		fn func() ([]backends.ObjectNode, error)
	}
	fns := make([]listFn, len(m.objTypes))
	for i, w := range m.objTypes {
		fns[i] = listFn{fn: w.listFn}
	}
	return guard(func(err error) tea.Msg { return objectsMsg{errs: []error{err}} }, func() tea.Msg {
		if len(fns) == 0 {
			return objectsMsg{errs: []error{errNoManageAPI}}
		}
		msg := objectsMsg{
			windows: make([][]backends.ObjectNode, len(fns)),
			errs:    make([]error, len(fns)),
		}
		// Each window's List() is an independent broker call; fetch them
		// concurrently so refresh latency is the slowest window instead of
		// their sum. Each goroutine writes only its own index of
		// msg.windows/msg.errs, so no lock is needed, and a window's own
		// error never aborts the others — g.Go always returns nil, since a
		// per-window failure is reported via msg.errs[i], not by failing the
		// whole refresh.
		var g errgroup.Group
		for i, f := range fns {
			if f.fn == nil {
				continue
			}
			g.Go(func() error {
				nodes, err := f.fn()
				msg.windows[i] = nodes
				msg.errs[i] = err
				return nil
			})
		}
		_ = g.Wait()
		return msg
	})
}

// autoUpdateEnabled returns true when periodic sidebar refresh is enabled.
func (m aiTUIModel) autoUpdateEnabled() bool {
	return m.ai != nil && m.refreshEnabled
}

// beginRefresh starts a background sidebar fetch, stamps the start time, bumps
// the generation counter, and arms a watchdog. Must be called as a pointer
// receiver so the state mutation is visible to the caller.
func (m *aiTUIModel) beginRefresh() tea.Cmd {
	m.refreshing = true
	m.refreshStart = time.Now()
	m.refreshGen++
	gen := m.refreshGen
	return tea.Batch(
		m.startLoadObjects(),
		tea.Tick(refreshWatchdogTimeout, func(time.Time) tea.Msg { return refreshWatchdogMsg{gen: gen} }),
	)
}

func (m aiTUIModel) handleObjectsDone(msg objectsMsg) (tea.Model, tea.Cmd) {
	m.loadingObjects = false
	for i := range m.objTypes {
		// Never overwrite the Processes window with broker-objects data.
		if m.objTypes[i].kind == objWindowProcs {
			continue
		}
		if i < len(msg.windows) {
			m.objTypes[i].nodes = msg.windows[i]
			m.objTypes[i].dataGen++ // invalidate getFilteredSortedNodes's cache
		}
		if i < len(msg.errs) {
			m.objTypes[i].err = msg.errs[i]
			if msg.errs[i] != nil && !errors.Is(msg.errs[i], errNoManageAPI) {
				log.Verbose("objects fetch [%s]: %s", m.objTypes[i].label, msg.errs[i])
			}
		}
		// Clamp selection.
		rows := m.sidebarRows(i)
		if m.objTypes[i].sel >= len(rows) {
			m.objTypes[i].sel = max(0, len(rows)-1)
		}
	}

	// The model learns the broker's contents from this same listing — it never
	// lists the broker itself, and a request never waits for one.
	if m.ai != nil {
		if text, ok := m.topologyText(); ok {
			m.ai.setTopology(text)
		}
	}

	// A listing that failed only because the broker is unreachable starts the
	// reconnect countdown (confirmed by a probe) — the windows don't retry on
	// a schedule of their own.
	probe := m.noteConnFailure(m.connectionErrorOf(msg))

	// Schedule the next periodic refresh if this was a background fetch.
	if m.refreshing {
		m.refreshing = false
		m.lastFetchDur = time.Since(m.refreshStart)
		if m.autoUpdateEnabled() {
			next := max(time.Duration(refreshFactor)*m.lastFetchDur, m.refreshPeriod)
			return m, tea.Batch(probe, tea.Tick(next, func(time.Time) tea.Msg { return refreshTickMsg{} }))
		}
	}
	return m, probe
}

// connectionErrorOf returns a connection error when every window that was
// listed failed with one (a window that listed fine proves the management
// connection is up), else nil.
func (m aiTUIModel) connectionErrorOf(msg objectsMsg) error {
	var first error
	for i := range msg.errs {
		if msg.errs[i] == nil && i < len(msg.windows) {
			return nil
		}
		if msg.errs[i] != nil && first == nil && isConnectionError(msg.errs[i]) {
			first = msg.errs[i]
		}
	}
	return first
}

// topologyText renders the sidebar's object windows the way "manage list"
// prints them. ok is false while any window has no listing (never loaded, or
// the last fetch failed), so a failed refresh keeps the previous topology.
func (m aiTUIModel) topologyText() (string, bool) {
	var groups []objectGroup
	for _, ow := range m.objTypes {
		if ow.kind == objWindowProcs {
			continue
		}
		if ow.err != nil {
			return "", false
		}
		groups = append(groups, objectGroup{label: ow.label, nodes: ow.nodes})
	}
	if len(groups) == 0 {
		return "", false
	}
	var b strings.Builder
	writeObjectList(&b, groups)
	return b.String(), true
}

// ---------- Sidebar helpers ----------

// cycleFocus moves focus to the next (forward=true) or previous available pane.
func (m *aiTUIModel) cycleFocus(forward bool) {
	// Targets: focusChat (0), then 1..len(objTypes) for each window.
	n := len(m.objTypes) + 1
	if n <= 1 || !m.sidebarVisible() {
		return // nothing to browse — or a sidebar too narrow to be shown
	}
	prevFocus := m.focus
	cur := int(m.focus)
	if forward {
		cur = (cur + 1) % n
	} else {
		cur = (cur - 1 + n) % n
	}
	m.focus = focusTarget(cur)

	// Exit process view when leaving the process pane.
	if prevWi := int(prevFocus) - 1; prevWi >= 0 && prevWi < len(m.objTypes) &&
		m.objTypes[prevWi].kind == objWindowProcs {
		m.exitProcessView()
	}

	if m.focus == focusChat {
		m.input.Focus()
	} else {
		m.input.Blur()
		// Enter process view when arriving at the process pane.
		if m.procsFocused() {
			m.enterProcessView()
		}
	}
}

// sidebarRow is one navigable row in a sidebar window: a top-level node, or a
// child row (with its parent's name, needed for compound-key dispatch such as
// Azure Service Bus's topic+subscription addressing).
type sidebarRow struct {
	node       backends.ObjectNode
	parentName string // "" for top-level rows
}

// sidebarRows returns the flattened, navigable rows for window idx, in the
// exact order writeObjectSection renders them: top-level nodes alone normally,
// or top-level+children interleaved when tree view is active for a
// hierarchical window. This is the single source of truth for both selection
// (moveSel, selectedNode and friends) and rendering, so the two can never
// drift apart the way a separately-maintained row list could.
func (m aiTUIModel) sidebarRows(idx int) []sidebarRow {
	if idx < 0 || idx >= len(m.objTypes) {
		return nil
	}
	items := m.getFilteredSortedNodes(idx)
	w := m.objTypes[idx]
	rows := make([]sidebarRow, 0, len(items))
	for _, node := range items {
		rows = append(rows, sidebarRow{node: node})
		if w.treeView && w.hierarchical {
			for _, child := range node.Children {
				rows = append(rows, sidebarRow{node: child, parentName: node.Name})
			}
		}
	}
	return rows
}

// indexOfRowNamed returns the index of the first row in rows whose node has
// the given name, or 0 if not found (an empty/not-found name also lands on 0,
// which is always a safe selection when rows is non-empty).
func indexOfRowNamed(rows []sidebarRow, name string) int {
	if name != "" {
		for i, r := range rows {
			if r.node.Name == name {
				return i
			}
		}
	}
	return 0
}

// moveSel moves the selection in the focused pane by delta.
func (m *aiTUIModel) moveSel(delta int) {
	wi, ok := m.focusedWindow()
	if !ok {
		return
	}
	rows := m.sidebarRows(wi)
	if len(rows) == 0 {
		return
	}
	m.objTypes[wi].sel = min(max(m.objTypes[wi].sel+delta, 0), len(rows)-1)
}

// focusedWindow returns the index of the focused sidebar window; ok is false
// when focus is on the chat input.
func (m *aiTUIModel) focusedWindow() (wi int, ok bool) {
	wi = int(m.focus) - 1
	return wi, wi >= 0 && wi < len(m.objTypes)
}

// selectedRow returns the selected row of the focused window (ok=false when
// nothing is selected or the window is empty).
func (m *aiTUIModel) selectedRow() (sidebarRow, bool) {
	wi, ok := m.focusedWindow()
	if !ok {
		return sidebarRow{}, false
	}
	rows := m.sidebarRows(wi)
	if sel := m.objTypes[wi].sel; sel < len(rows) {
		return rows[sel], true
	}
	return sidebarRow{}, false
}

// selectedNode returns the full ObjectNode for the currently selected row in
// the focused pane — top-level or child (ok=false when nothing is selected or
// the pane is empty). Most callers that dispatch an action keyed by window
// label (not node kind) should use selectedTopLevelNode instead, since a
// child row's name is not a valid target for those actions.
func (m *aiTUIModel) selectedNode() (backends.ObjectNode, bool) {
	row, ok := m.selectedRow()
	return row.node, ok
}

// selectedName returns the name of the currently selected row in the focused pane.
func (m *aiTUIModel) selectedName() string {
	node, ok := m.selectedNode()
	if !ok {
		return ""
	}
	return node.Name
}

// selectedTopLevelNode returns the currently selected node only when it is a
// top-level row (ok=false for a child row). Use this for any action keyed by
// window label rather than node kind — e.g. create/delete/purge/send/receive
// dispatch via ManageSpec or the queue/topic adapters, where a child row's
// name (a RabbitMQ binding, a NATS consumer, ...) is never a valid target.
func (m *aiTUIModel) selectedTopLevelNode() (backends.ObjectNode, bool) {
	row, ok := m.selectedRow()
	if !ok || row.parentName != "" {
		return backends.ObjectNode{}, false
	}
	return row.node, true
}

// selectedChildNode returns the currently selected node and its parent's name
// only when the selection is a child row (ok=false for a top-level row).
func (m *aiTUIModel) selectedChildNode() (node backends.ObjectNode, parentName string, ok bool) {
	row, ok := m.selectedRow()
	if !ok || row.parentName == "" {
		return backends.ObjectNode{}, "", false
	}
	return row.node, row.parentName, true
}

// Sidebar S/P/R/peek/subscription eligibility is declared per broker on
// ObjectType (cmd/manage.go) and read off the focused objWindow via its
// sendEligible/sendsViaTopic/subscriptionEligible/singularLabel methods
// (cmd/aitui.go) — see those for the rules this used to hand-match on Label.

// cycleSort advances the sort mode for the focused pane.
func (m *aiTUIModel) cycleSort() {
	wi, ok := m.focusedWindow()
	if !ok {
		return
	}
	w := &m.objTypes[wi]
	metrics := firstMetrics(w.nodes)
	nModes := 1 + len(metrics) // name + one per metric
	w.sortIdx = sortMode((int(w.sortIdx) + 1) % nModes)
	w.sel = 0
}

// ---------- Input mode & history ----------

// toggleInputMode switches between AI prompt and direct command modes.
func (m *aiTUIModel) toggleInputMode() {
	m.histIdx = -1
	if m.mode == modeAI {
		m.mode = modeCmd
		applyPromptFunc(&m.input, m.binaryName+"> ")
		m.input.Placeholder = "Type an xmc command..."
	} else {
		m.mode = modeAI
		applyPromptFunc(&m.input, "ask> ")
		m.input.Placeholder = "Ask anything..."
	}
	// Re-style the prompt label to match the new mode's accent (petrol for
	// ask>, blue for xmc>) — same visual cue as the title bar/rules/sidebar
	// header, so the active mode is unmistakable everywhere at once.
	m.setPromptTheme(m.theme())
	// Re-layout: prompt width may have changed (e.g. "ask> " vs "awsmc> ").
	// updateInputHeight recomputes the input area's height for the new prompt width, then calls recalcLayout.
	m.updateInputHeight()
}

// historyPrev recalls the previous entry from the active history.
func (m *aiTUIModel) historyPrev() {
	hist := m.activeHistory()
	if len(hist) == 0 {
		return
	}
	if m.histIdx == -1 {
		// Save the current draft before navigating.
		m.histDraft = m.input.Value()
		m.histIdx = len(hist) - 1
	} else if m.histIdx > 0 {
		m.histIdx--
	}
	m.input.SetValue(hist[m.histIdx])
}

// historyNext recalls the next entry, returning to the draft at the end.
func (m *aiTUIModel) historyNext() {
	hist := m.activeHistory()
	if m.histIdx == -1 {
		return
	}
	if m.histIdx < len(hist)-1 {
		m.histIdx++
		m.input.SetValue(hist[m.histIdx])
	} else {
		m.histIdx = -1
		m.input.SetValue(m.histDraft)
	}
}

// activeHistory returns the history list for the current input mode.
func (m *aiTUIModel) activeHistory() []string {
	if m.mode == modeCmd {
		return m.cmdHistory
	}
	return m.askHistory
}

// ---------- Autocomplete ----------

// doAutocomplete performs Tab-completion on the current input line using the
// readline-compatible PrefixCompleter (same tree as the regular shell).
func (m *aiTUIModel) doAutocomplete() {
	if m.completer == nil {
		return
	}
	line := []rune(m.input.Value())
	candidates, prefixLen := m.completer.Do(line, len(line))
	if len(candidates) == 0 {
		return
	}
	if len(candidates) == 1 {
		// Single match — append it.
		m.input.SetValue(string(line) + string(candidates[0]))
		return
	}
	// Multiple matches — append longest common prefix.
	lcp := longestCommonPrefix(candidates)
	if len(lcp) > 0 && len(lcp) > prefixLen {
		// Only append the portion beyond what's already typed.
		m.input.SetValue(string(line) + string(lcp))
		return
	}
	// Show candidates as a transient line in the transcript.
	var names []string
	for _, c := range candidates {
		name := strings.TrimSpace(string(c))
		if name != "" {
			names = append(names, name)
		}
	}
	if len(names) > 0 {
		m.appendTranscript(dimStyle.Render("  "+strings.Join(names, "  ")) + "\n")
	}
}

// longestCommonPrefix returns the longest common prefix among rune slices.
func longestCommonPrefix(candidates [][]rune) []rune {
	if len(candidates) == 0 {
		return nil
	}
	prefix := candidates[0]
	for _, c := range candidates[1:] {
		n := min(len(c), len(prefix))
		i := 0
		for i < n && prefix[i] == c[i] {
			i++
		}
		prefix = prefix[:i]
		if len(prefix) == 0 {
			break
		}
	}
	return prefix
}

// ---------- Copy helpers ----------

// lineHasCopyMarker reports whether the (potentially ANSI-coloured) line
// contains a ⧉ clipboard marker.
func lineHasCopyMarker(line string) bool {
	return strings.Contains(xansi.Strip(line), copyMarker)
}

// copyIdxForLine returns the 0-based index into m.copyItems for a click on
// wrappedContentLines[clickedLine].  It counts the number of ⧉ markers that
// appear at or before clickedLine (the Nth marker → copyItems[N-1]).
// Returns -1 if the clicked line has no marker.
func (m aiTUIModel) copyIdxForLine(clickedLine int) int {
	if clickedLine < 0 || clickedLine >= len(m.wrappedContentLines) {
		return -1
	}
	if !lineHasCopyMarker(m.wrappedContentLines[clickedLine]) {
		return -1
	}
	count := 0
	for i := 0; i <= clickedLine; i++ {
		if lineHasCopyMarker(m.wrappedContentLines[i]) {
			count++
		}
	}
	return count - 1 // 0-based
}

// renderMessagePayload renders a message payload (or multiple NDJSON records)
// with a left border and italic cyan body, mimicking a blockquote.
func renderMessagePayload(content string) string {
	lines := strings.Split(content, "\n")
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(msgBorderStyle.Render("│") + " " + msgBodyStyle.Render(line) + "\n")
	}
	return b.String()
}

// ---------- Proposal rendering ----------

// freezeProposal builds the static transcript line written when the user
// resolves a proposed command: "✗" (discarded, command text dimmed) or "?"
// (discuss, with the destructive warning when destructive). An accepted (✓)
// command goes through acceptProposal instead: its output follows directly
// underneath.
func freezeProposal(cmd, marker string, destructive bool) string {
	var result string
	if marker == "✗" {
		result = dimStyle.Render("▶ "+cmd) + " " + warnStyle.Render("✗")
	} else {
		result = cmdStyle.Render("▶ "+cmd) + " " + infoStyle.Render(marker)
		if destructive {
			result += "\n" + warnStyle.Render("  ⚠ destructive — review carefully")
		}
	}
	return result + "\n\n"
}

// acceptProposal freezes an accepted command into the transcript as
// "▶ <cmd> ✓ ⧉" — the one line that stands for the command, with its copy
// marker; the command's output follows directly underneath.
func (m *aiTUIModel) acceptProposal(cmd string) {
	m.copyItems = append(m.copyItems, cmd)
	m.appendTranscript(cmdStyle.Render("▶ "+cmd) + " " + cmdStyle.Render("✓") + copyHintStyle.Render(" ⧉") + "\n")
}

// renderPicker renders the interactive picker list.
func (m *aiTUIModel) renderPicker() string {
	p := m.picker
	if p == nil {
		return ""
	}
	var b strings.Builder
	if p.title != "" {
		b.WriteString(infoStyle.Render(p.title) + "\n")
	}
	for i, item := range p.items {
		label := item
		if i == p.current {
			label += " (current)"
		}
		if i == p.sel {
			b.WriteString(pickerSelectedStyle.Render("▸ "+label) + "\n")
		} else {
			b.WriteString(dimStyle.Render("  "+label) + "\n")
		}
	}
	b.WriteString(dimStyle.Render("\n↑/↓ select · Enter confirm · Esc cancel") + "\n")
	return b.String()
}
