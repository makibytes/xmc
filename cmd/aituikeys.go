package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
)

// connState holds connection probe and auto-reconnect state for the AI TUI.
type connState struct {
	checked            bool          // true after the initial probe completes
	err                error         // non-nil if the broker is unreachable
	reconnecting       bool          // true while backoff is ticking or a probe is in-flight
	reconnectAt        time.Time     // wall-clock time the next probe fires
	reconnectBackoff   time.Duration // current wait interval (doubles each failure, capped at 3 min)
	reconnectDisabled  bool          // user ran /disconnect — stop auto-retry
	reconnectBlink     bool          // toggled every 500ms for the title-bar yellow blink
	reconnectStatus    string        // one-line countdown shown below the viewport
}

// pickerState holds the state for an interactive selection list (Claude Code-style).
type pickerState struct {
	title    string   // heading shown above the list
	items    []string // selectable labels
	sel      int      // currently highlighted index
	current  int      // index of the active item (-1 if none matches)
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
		m.state = tuiIdle
		m.input.Focus()
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
		m.state = tuiIdle
		m.input.Focus()
		m.setViewportContent()
		return m, nil
	case tea.KeyEsc, tea.KeyCtrlC:
		m.appendTranscript(dimStyle.Render("(cancelled)") + "\n\n")
		m.picker = nil
		m.state = tuiIdle
		m.input.Focus()
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
		// Route process pane keys to the dedicated handler.
		if wi := int(m.focus) - 1; wi >= 0 && wi < len(m.objTypes) && m.objTypes[wi].kind == objWindowProcs {
			return m.handleKeyProcessPane(msg)
		}
		return m.handleKeyPane(msg)
	}

	switch msg.Type {
	case tea.KeyCtrlC:
		(&m).killAllProcs()
		m.quitting = true
		return m, tea.Quit

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
		(&m).updateInputHeight()
		return m, nil

	case tea.KeyDown:
		m.historyNext()
		(&m).updateInputHeight()
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

		m.input.Reset()
		m.histIdx = -1
		// Unconditional height reset (fixes stale inputLines glitch after multi-line input).
		m.inputLines = 1
		m.input.SetHeight(1)
		m.recalcLayout()

		// Slash commands (work in both modes).
		if strings.HasPrefix(prompt, "/") {
			return m.handleSlashCommand(prompt)
		}

		if m.mode == modeCmd {
			// Direct command execution (shell-like).
			if prompt == "exit" || prompt == "quit" {
				(&m).killAllProcs()
				m.exitAll = true
				m.quitting = true
				return m, tea.Quit
			}
			m.cmdHistory = append(m.cmdHistory, prompt)
			m.appendTranscript(cmdStyle.Render(m.binaryName+"> ") + prompt + "\n")
			m.proposedCmd = prompt
			// Commands with --for become managed background processes.
			if commandHasFor(prompt) {
				return m.startBackgroundProcess(prompt)
			}
			return m.startExecution(prompt)
		}

		// AI mode: send to the model.
		m.askHistory = append(m.askHistory, prompt)
		askHistory.Append(prompt)
		m.appendTranscript(userStyle.Render("you: ") + prompt + "\n\n")

		m.ai.history = append(m.ai.history, aiMessage{Role: "user", Content: prompt})
		trimHistory(&m.ai.history, maxHistory)

		m.fixAttempts = 0
		m.state = tuiThinking
		m.streamBuf.Reset()
		m.turnIn = 0
		m.turnOut = 0

		return m, m.startAIRequest()

	case tea.KeyRunes:
		// Fall through to textarea update.
		fallthrough

	default:
		// Pre-size BEFORE forwarding the key: resize the textarea so that
		// repositionView() inside Update() sees the correct height and doesn't
		// scroll content out of view when the text wraps to a new visual row.
		// predictValue avoids calling Update() twice (which caused double-insertion
		// due to shared backing arrays in the textarea's [][]rune value).
		if n := (&m).computeInputLines(predictValue(m.input.Value(), msg)); n != m.inputLines {
			m.inputLines = n
			m.input.SetHeight(n)
			m.recalcLayout()
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		(&m).updateInputHeight()
		return m, cmd
	}
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
		m.appendTranscript(dimStyle.Render(
			"/model               pick a model from the provider\n"+
				"/model <name>        switch to a model directly\n"+
				"/effort              pick reasoning effort (temperature)\n"+
				"/effort low|med|high set effort directly\n"+
				"/refresh             reload broker objects now\n"+
				"/refresh off         disable periodic refresh\n"+
				"/refresh <dur>       set refresh interval (e.g. 3s, 3m; min 1s)\n"+
				"/connect             reconnect to the broker (enables auto-reconnect)\n"+
				"/disconnect          stop auto-reconnect\n"+
				"/reset               reset conversation history\n"+
				"/clear               clear the display\n"+
				"/exit                quit xmc\n"+
				"/help                show this help\n"+
				"\n"+
				"Esc          toggle between ask> (AI) and "+m.binaryName+"> (command) mode\n"+
				"Tab          autocomplete (command mode) · browse sidebar forward (AI mode)\n"+
				"Shift+Tab    browse sidebar backward\n"+
				"Up/Down      recall history\n"+
				"Space        collapse/expand selected sidebar window\n"+
				"x            toggle hierarchical tree-view for broker objects\n"+
				"PgUp/PgDn    scroll conversation · mouse wheel also works\n"+
				"Home/End     jump to top/bottom\n"+
				"             click ⧉ in the transcript to copy any item\n") + "\n")

	case "/exit":
		(&m).killAllProcs()
		m.exitAll = true
		m.quitting = true
		return m, tea.Quit

	case "/reset":
		m.ai.resetHistory()
		m.transcript.Reset()
		m.totalIn = 0
		m.totalOut = 0
		m.turnIn = 0
		m.turnOut = 0
		m.setViewportContent()
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
			msg := "periodic refresh → " + persistVal
			if err := saveRefreshInterval(persistVal); err != nil {
				msg += fmt.Sprintf(" (save failed: %s)", err)
			}
			m.appendTranscript(dimStyle.Render(msg) + "\n\n")
			// If turned on and not already refreshing, kick off the loop.
			if enabled && !wasEnabled && !m.refreshing && len(m.objTypes) > 0 {
				return m, (&m).beginRefresh()
			}
			return m, nil
		}
		// No arg: manual one-shot reload.
		if len(m.objTypes) > 0 {
			m.appendTranscript(dimStyle.Render("refreshing…") + "\n\n")
			m.loadingObjects = true
			return m, m.startLoadObjects()
		}
		m.appendTranscript(dimStyle.Render("(no management API)") + "\n\n")

	case "/disconnect":
		if m.conn.reconnecting {
			m.conn.reconnecting = false
			m.conn.reconnectDisabled = true
			m.conn.reconnectStatus = ""
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
		m.conn.reconnectDisabled = false
		m.conn.reconnecting = true
		m.conn.reconnectAt = time.Now() // fire probe immediately
		m.conn.reconnectStatus = "↻ connecting…"
		m.appendTranscript(dimStyle.Render("Connecting…") + "\n\n")
		return m, m.startReconnectProbe()

	case "/clear":
		m.transcript.Reset()
		m.setViewportContent()

	case "/model":
		if arg == "" {
			m.appendTranscript(dimStyle.Render(fmt.Sprintf("current: %s · %s", m.ai.modelName, m.ai.providerName)) + "\n")
			m.appendTranscript(dimStyle.Render("fetching models…") + "\n\n")
			m.fetchingModels = true
			return m, m.startListModels()
		}
		m.applyModel(arg)
		msg := "model → " + arg
		if err := saveAIModel(arg); err != nil {
			msg += fmt.Sprintf(" (save failed: %s)", err)
		}
		m.appendTranscript(dimStyle.Render(msg) + "\n\n")

	case "/effort":
		if arg == "" {
			// Open interactive effort picker.
			effortLevels := []string{"low", "medium", "high"}
			currentIdx := -1
			if setter, ok := m.ai.client.(modelSettable); ok {
				switch t := setter.Temperature(); {
				case t <= 0:
					currentIdx = 0
				case t <= 0.4:
					currentIdx = 1
				default:
					currentIdx = 2
				}
			}
			startSel := currentIdx
			if startSel < 0 {
				startSel = 0
			}
			m.picker = &pickerState{
				title:   "Select effort level:",
				items:   effortLevels,
				sel:     startSel,
				current: currentIdx,
				onSelect: func(model *aiTUIModel, idx int) {
					temps := []float64{0, 0.3, 0.7}
					if setter, ok := model.ai.client.(modelSettable); ok {
						setter.SetTemperature(temps[idx])
					}
					model.appendTranscript(dimStyle.Render(fmt.Sprintf("effort → %s (temperature %.1f)", effortLevels[idx], temps[idx])) + "\n\n")
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

// applyEffort parses an effort level string and sets the temperature.
// Returns false if the argument is invalid.
func (m *aiTUIModel) applyEffort(arg string) bool {
	var temp float64
	switch strings.ToLower(arg) {
	case "low", "l":
		temp = 0
	case "medium", "med", "m":
		temp = 0.3
	case "high", "h":
		temp = 0.7
	default:
		m.appendTranscript(warnStyle.Render("effort must be low, medium, or high") + "\n\n")
		return false
	}
	if setter, ok := m.ai.client.(modelSettable); ok {
		setter.SetTemperature(temp)
	}
	m.appendTranscript(dimStyle.Render(fmt.Sprintf("effort → %s (temperature %.1f)", strings.ToLower(arg), temp)) + "\n\n")
	return true
}

// applyModel switches the AI client to a new model (in-memory only).
func (m *aiTUIModel) applyModel(name string) {
	if setter, ok := m.ai.client.(modelSettable); ok {
		setter.SetModel(name)
	}
	m.ai.modelName = name
	m.ai.rebuildPrompt()
}

func (m aiTUIModel) handleKeyThinking(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyEsc || msg.Type == tea.KeyCtrlC {
		if m.execCancel != nil {
			m.execCancel()
		}
		m.appendTranscript(dimStyle.Render("(cancelled)") + "\n\n")
		m.state = tuiIdle
		if len(m.ai.history) > 0 && m.ai.history[len(m.ai.history)-1].Role == "user" {
			m.ai.history = m.ai.history[:len(m.ai.history)-1]
		}
		m.input.Focus()
		return m, nil
	}
	return m, nil
}

func (m aiTUIModel) handleKeyProposing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		// Freeze with a grey ✗ marker.
		m.appendTranscript(freezeProposal(m.proposedCmd, "✗", true, false))
		m.ai.history = append(m.ai.history, aiMessage{Role: "assistant", Content: m.proposedCmd})
		m.ai.history = append(m.ai.history, aiMessage{Role: "user", Content: "[user discarded the command]"})
		m.state = tuiIdle
		m.input.Focus()
		return m, nil

	case tea.KeyEnter:
		// Freeze with a green ✓ marker, then execute (or background if --for).
		m.appendTranscript(freezeProposal(m.proposedCmd, "✓", false, m.proposedDestructive))
		if commandHasFor(m.proposedCmd) {
			return m.startBackgroundProcess(m.proposedCmd)
		}
		return m.startExecution(m.proposedCmd)

	case tea.KeyRunes:
		switch msg.String() {
		case "e":
			// Switch to editing sub-state — shimmer continues while the user types.
			m.input.SetValue(m.proposedCmd)
			m.state = tuiEditing
			m.input.Focus()
			(&m).updateInputHeight()
			return m, nil
		case "c":
			// Freeze with a yellow ? marker to mark it as a follow-up topic.
			m.appendTranscript(freezeProposal(m.proposedCmd, "?", false, m.proposedDestructive))
			m.ai.history = append(m.ai.history, aiMessage{Role: "assistant", Content: m.proposedCmd})
			m.ai.history = append(m.ai.history, aiMessage{Role: "user", Content: "[command NOT executed — user wants to discuss or refine it before running]"})
			m.input.SetValue("")
			m.state = tuiIdle
			m.input.Focus()
			(&m).updateInputHeight()
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
		m.proposedDestructive = anyCommand(cmd, isDestructive)
		// Freeze with a green ✓ and run (or background if --for).
		m.appendTranscript(freezeProposal(cmd, "✓", false, m.proposedDestructive))
		m.input.SetValue("")
		m.inputLines = 1
		m.input.SetHeight(1)
		m.recalcLayout()
		if commandHasFor(cmd) {
			return m.startBackgroundProcess(cmd)
		}
		return m.startExecution(cmd)

	case tea.KeyEsc, tea.KeyCtrlC:
		// Freeze with a grey ✗ marker and return to idle.
		m.appendTranscript(freezeProposal(m.proposedCmd, "✗", true, false))
		m.input.SetValue("")
		m.inputLines = 1
		m.input.SetHeight(1)
		m.state = tuiIdle
		m.input.Focus()
		m.recalcLayout()
		return m, nil

	default:
		if n := (&m).computeInputLines(predictValue(m.input.Value(), msg)); n != m.inputLines {
			m.inputLines = n
			m.input.SetHeight(n)
			m.recalcLayout()
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		(&m).updateInputHeight()
		return m, cmd
	}
}

// handleKeyPane processes keys when a sidebar pane has focus.
func (m aiTUIModel) handleKeyPane(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	wi := int(m.focus) - 1
	if wi < 0 || wi >= len(m.objTypes) {
		return m, nil
	}

	switch msg.Type {
	case tea.KeyEsc:
		m.focus = focusChat
		m.input.Focus()
		return m, nil
	case tea.KeyUp:
		m.moveSel(-1)
		return m, nil
	case tea.KeyDown:
		m.moveSel(1)
		return m, nil
	case tea.KeyEnter:
		name := m.selectedName()
		if name != "" {
			m.input.SetValue(name)
			m.focus = focusChat
			m.input.Focus()
			(&m).updateInputHeight()
		}
		return m, nil
	case tea.KeySpace:
		// Space collapses/expands the whole window (title only vs. full content).
		m.objTypes[wi].collapsed = !m.objTypes[wi].collapsed
		return m, nil
	case tea.KeyTab:
		m.cycleFocus(true)
		return m, nil
	case tea.KeyShiftTab:
		m.cycleFocus(false)
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
		case "s":
			m.cycleSort()
			return m, nil
		case "r":
			m.loadingObjects = true
			return m, m.startLoadObjects()
		case "x":
			// x toggles the hierarchical tree-view (show/hide children).
			if m.objTypes[wi].hierarchical {
				m.objTypes[wi].treeView = !m.objTypes[wi].treeView
			}
			return m, nil
		case "c":
			if m.objTypes[wi].createAction != nil {
				m.startPrompt("create", wi, "")
			}
			return m, nil
		case "d":
			name := m.selectedName()
			if name != "" && m.objTypes[wi].deleteAction != nil {
				m.startPrompt("delete", wi, name)
			}
			return m, nil
		case "p":
			name := m.selectedName()
			if name == "" {
				return m, nil
			}
			switch m.objTypes[wi].label {
			case "Queues", "Streams":
			default:
				return m, nil
			}
			desc := fmt.Sprintf("▶ peek %s \"%s\"", singular(m.objTypes[wi].label), name)
			m.appendTranscript(histCmdStyle.Render(desc) + "\n")
			m.state = tuiExecuting
			// capture by value
			queue := name
			return m, func() tea.Msg {
				qa, err := m.session.getQueueAdapter()
				if err != nil {
					return sideActionMsg{err: fmt.Errorf("adapter: %w", err)}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				msg, err := qa.Receive(ctx, backends.ReceiveOptions{
					Queue:       queue,
					Acknowledge: false,
					Timeout:     1,
					Wait:        false,
				})
				if err != nil {
					if errors.Is(err, backends.ErrNoMessageAvailable) {
						return sideActionMsg{action: "   └ (no messages available)"}
					}
					return sideActionMsg{err: err}
				}
				result := formatPeekMessage(msg)
				return sideActionMsg{action: result}
			}
		}
	}
	return m, nil
}

// handleKeyFilter processes keys while the inline filter is active.
func (m aiTUIModel) handleKeyFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	wi := int(m.focus) - 1
	if wi < 0 || wi >= len(m.objTypes) {
		return m, nil
	}

	switch msg.Type {
	case tea.KeyEsc:
		m.filtering = false
		m.objTypes[wi].filter = ""
		m.objTypes[wi].sel = 0
		return m, nil
	case tea.KeyEnter:
		m.filtering = false
		return m, nil
	case tea.KeyBackspace:
		if len(m.objTypes[wi].filter) > 0 {
			m.objTypes[wi].filter = m.objTypes[wi].filter[:len(m.objTypes[wi].filter)-1]
			m.objTypes[wi].sel = 0
		}
		return m, nil
	case tea.KeyRunes:
		m.objTypes[wi].filter += msg.String()
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

func (m aiTUIModel) handleKeyPrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	wi := m.promptObjIdx
	if wi < 0 || wi >= len(m.objTypes) {
		m.promptActive = false
		m.input.Focus()
		return m, nil
	}
	ow := &m.objTypes[wi]

	switch m.promptKind {
	case "create":
		switch msg.Type {
		case tea.KeyEsc, tea.KeyCtrlC:
			m.promptActive = false
			m.input.Focus()
			return m, nil
		case tea.KeyEnter:
			name := strings.TrimSpace(m.promptName)
			if name == "" {
				return m, nil
			}
			action := ow.createAction
			if action == nil {
				m.promptActive = false
				m.input.Focus()
				return m, nil
			}
			desc := fmt.Sprintf("▶ create %s \"%s\"", singular(ow.label), name)
			m.appendTranscript(histCmdStyle.Render(desc) + "\n")
			m.promptActive = false
			initManageAction(action)
			m.state = tuiExecuting
			return m, func() tea.Msg {
				err := action.Run(name)
				return sideActionMsg{err: err}
			}
		case tea.KeyBackspace:
			if len(m.promptName) > 0 {
				m.promptName = m.promptName[:len(m.promptName)-1]
			}
			return m, nil
		case tea.KeyRunes:
			m.promptName += msg.String()
			return m, nil
		}

	case "delete":
		switch msg.Type {
		case tea.KeyEsc, tea.KeyCtrlC:
			m.promptActive = false
			m.input.Focus()
			return m, nil
		case tea.KeyEnter:
			action := ow.deleteAction
			if action == nil {
				m.promptActive = false
				m.input.Focus()
				return m, nil
			}
			desc := fmt.Sprintf("▶ delete %s \"%s\"", singular(ow.label), m.promptName)
			m.appendTranscript(histCmdStyle.Render(desc) + "\n")
			m.promptActive = false
			initManageAction(action)
			m.state = tuiExecuting
			return m, func() tea.Msg {
				err := action.Run(m.promptName)
				return sideActionMsg{err: err}
			}
		}
	}
	return m, nil
}

// singular strips a trailing "s" from a plural label for display purposes
// (e.g. "Queues" → "Queue", "Addresses" → "Address").
func singular(label string) string {
	if s, ok := irregularPlurals[label]; ok {
		return s
	}
	return strings.TrimSuffix(label, "s")
}

// irregularPlurals maps plural labels to their singular form for labels that
// don't follow the simple "trim trailing s" rule.
var irregularPlurals = map[string]string{
	"Addresses": "Address",
}

// initManageAction calls SetupFlags on a throwaway command to initialise
// default flag-bound variables before calling Run.
func initManageAction(a *ManageAction) {
	if a == nil || a.SetupFlags == nil {
		return
	}
	a.SetupFlags(&cobra.Command{})
}

// formatPeekMessage formats a peeked message payload for display in the TUI transcript.
func formatPeekMessage(msg *backends.Message) string {
	if len(msg.Data) == 0 {
		return ""
	}
	payload := strings.TrimRight(string(msg.Data), "\n\r\t ")
	if len(payload) > 255 {
		payload = payload[:255] + "..."
	}
	return payload + "\n"
}

func (m aiTUIModel) handleKeyExecuting(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyEsc || msg.Type == tea.KeyCtrlC {
		if m.execCancel != nil {
			m.execCancel()
		}
		return m, nil
	}
	return m, nil
}

// ---------- AI request ----------

func (m aiTUIModel) startAIRequest() tea.Cmd {
	ai := m.ai
	pptr := m.program

	return func() tea.Msg {
		if err := ai.init(); err != nil {
			return aiDoneMsg{err: err}
		}

		if ai.topology == "" && len(ai.history) <= 1 {
			ai.refreshTopology()
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		prog := derefProgram(pptr)

		if prog != nil {
			prog.Send(setCancelMsg{cancel: cancel})
		}

		var onToken func(string)
		if prog != nil {
			onToken = func(token string) {
				prog.Send(tokenMsg{text: token})
			}
		}

		text, usage, err := ai.client.Complete(ctx, ai.sysPrompt, ai.history, onToken)
		return aiDoneMsg{text: text, usage: usage, err: err}
	}
}

func (m aiTUIModel) startListModels() tea.Cmd {
	ai := m.ai
	return func() tea.Msg {
		if err := ai.init(); err != nil {
			return modelsMsg{err: err}
		}
		lister, ok := ai.client.(modelLister)
		if !ok {
			return modelsMsg{err: fmt.Errorf("model listing not supported for %s", ai.providerName)}
		}
		models, err := lister.ListModels(context.Background())
		return modelsMsg{models: models, err: err}
	}
}

func (m aiTUIModel) handleModelsDone(msg modelsMsg) (tea.Model, tea.Cmd) {
	m.fetchingModels = false
	if msg.err != nil {
		m.appendTranscript(warnStyle.Render("error listing models: "+msg.err.Error()) + "\n\n")
		return m, nil
	}
	if len(msg.models) == 0 {
		m.appendTranscript(dimStyle.Render("(no models returned)") + "\n\n")
		return m, nil
	}

	// Open an interactive picker with the fetched models.
	currentIdx := -1
	for i, id := range msg.models {
		if id == m.ai.modelName {
			currentIdx = i
			break
		}
	}
	startSel := currentIdx
	if startSel < 0 {
		startSel = 0
	}
	models := msg.models // capture for closure
	m.picker = &pickerState{
		title:   fmt.Sprintf("Select model (%s):", m.ai.providerName),
		items:   models,
		sel:     startSel,
		current: currentIdx,
		onSelect: func(model *aiTUIModel, idx int) {
			name := models[idx]
			model.applyModel(name)
			info := "model → " + name
			if err := saveAIModel(name); err != nil {
				info += fmt.Sprintf(" (save failed: %s)", err)
			}
			model.appendTranscript(dimStyle.Render(info) + "\n\n")
		},
	}
	m.state = tuiPicking
	m.input.Blur()
	m.setViewportContent()
	return m, nil
}

func (m aiTUIModel) handleAIDone(msg aiDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		if strings.Contains(msg.err.Error(), "context canceled") {
			return m, nil
		}
		m.appendTranscript(warnStyle.Render("error: "+msg.err.Error()) + "\n\n")
		if len(m.ai.history) > 0 && m.ai.history[len(m.ai.history)-1].Role == "user" {
			m.ai.history = m.ai.history[:len(m.ai.history)-1]
		}
		m.state = tuiIdle
		m.input.Focus()
		return m, nil
	}

	m.turnIn = msg.usage.InputTokens
	m.turnOut = msg.usage.OutputTokens
	m.totalIn += msg.usage.InputTokens
	m.totalOut += msg.usage.OutputTokens

	if msg.usage.InputTokens > 0 || msg.usage.OutputTokens > 0 {
		log.Verbose("AI tokens: %d input, %d output", msg.usage.InputTokens, msg.usage.OutputTokens)
	}

	command := extractCommandWithVerbs(msg.text, m.ai.verbSet)
	if command == "" {
		raw := strings.TrimSpace(msg.text)
		if raw != "" {
			// The model replied with prose instead of a command — show it.
			m.appendTranscript(dimStyle.Render(raw) + "\n\n")
		} else {
			m.appendTranscript(dimStyle.Render(fmt.Sprintf("(empty response from %s — try rephrasing)", m.ai.modelName)) + "\n\n")
		}
		if len(m.ai.history) > 0 && m.ai.history[len(m.ai.history)-1].Role == "user" {
			m.ai.history = m.ai.history[:len(m.ai.history)-1]
		}
		m.state = tuiIdle
		m.input.Focus()
		return m, nil
	}

	if question, ok := strings.CutPrefix(command, "# ask:"); ok {
		m.appendTranscript(infoStyle.Render("? "+strings.TrimSpace(question)) + "\n\n")
		m.ai.history = append(m.ai.history, aiMessage{Role: "assistant", Content: command})
		m.fixAttempts = 0
		m.state = tuiIdle
		m.input.Focus()
		return m, nil
	}

	if reason, ok := strings.CutPrefix(command, "# cannot:"); ok {
		m.appendTranscript(infoStyle.Render("✗ "+strings.TrimSpace(reason)) + "\n\n")
		m.ai.history = append(m.ai.history, aiMessage{Role: "assistant", Content: command})
		m.fixAttempts = 0
		m.state = tuiIdle
		m.input.Focus()
		return m, nil
	}

	// Normalize: collapse embedded newlines (xmc commands are single logical lines).
	command = strings.Join(strings.FieldsFunc(command, func(r rune) bool {
		return r == '\n' || r == '\r'
	}), " ")
	command = strings.TrimSpace(command)

	m.proposedCmd = command
	m.proposedDestructive = anyCommand(command, isDestructive)
	m.shimmerPhase = 0
	// Switch to tuiProposing BEFORE calling setViewportContent so that the
	// state guard in setViewportContent renders the live proposal overlay.
	// (The proposal is NOT written to the transcript yet — it is frozen there
	// only when the user accepts, rejects, edits, or chats.)
	m.state = tuiProposing
	m.input.Blur()
	m.setViewportContent()
	return m, nil
}

// ---------- Command execution ----------

func (m aiTUIModel) startExecution(command string) (tea.Model, tea.Cmd) {
	m.state = tuiExecuting
	m.input.Blur()

	m.ai.history = append(m.ai.history, aiMessage{Role: "assistant", Content: command})

	ai := m.ai
	sess := m.session
	rootCmd := m.rootCmd
	pptr := m.program

	return m, func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		prog := derefProgram(pptr)

		if prog != nil {
			prog.Send(setCancelMsg{cancel: cancel})
		}

		var capBuf cappedBuffer
		capBuf.max = maxCapture
		var errBuf cappedBuffer
		errBuf.max = maxCapture

		execErr := sess.executePipelineIO(ctx, command, rootCmd, strings.NewReader(""), &capBuf, &errBuf)

		refresh := anyCommand(command, isManageList) ||
			(ai.autoUpdateObjects && anyCommand(command, mutatesObjects)) ||
			(ai.autoUpdateMessages && anyCommand(command, mutatesMessages))
		if refresh {
			ai.refreshTopology()
		}

		stdout, stderr := capBuf.String(), errBuf.String()
		feedback := buildFeedback(execErr, stdout, stderr)
		ai.history = append(ai.history, aiMessage{Role: "user", Content: feedback})
		trimHistory(&ai.history, maxHistory)

		return execDoneMsg{err: execErr, stdout: stdout, stderr: stderr}
	}
}

func (m aiTUIModel) handleExecDone(msg execDoneMsg) (tea.Model, tea.Cmd) {
	var result strings.Builder

	// Register the executed command as a copyable item.
	m.copyItems = append(m.copyItems, m.proposedCmd)
	result.WriteString(histCmdStyle.Render("▶ ran: "+m.proposedCmd) +
		copyHintStyle.Render(" ⧉") + "\n")

	if msg.stdout != "" {
		trimmed := strings.TrimRight(msg.stdout, "\n")
		if isMessageReadCommand(m.proposedCmd) {
			// Style message payloads with a left border and italic text.
			result.WriteString(renderMessagePayload(trimmed))
			// Register the payload as a copyable item.
			m.copyItems = append(m.copyItems, trimmed)
			result.WriteString(copyHintStyle.Render("  ⧉") + "\n")
		} else {
			result.WriteString(dimStyle.Render(trimmed) + "\n")
		}
	}
	if msg.err != nil {
		result.WriteString(warnStyle.Render("✗ "+msg.err.Error()) + "\n")
	} else {
		result.WriteString(histOkStyle.Render("✓ ok") + "\n")
		m.fixAttempts = 0
	}
	result.WriteString("\n")

	shellHistory.Append(m.proposedCmd)
	m.appendTranscript(result.String())

	// Auto-fix: on error, ask the AI to correct the command (up to maxFixAttempts).
	if msg.err != nil && m.fixAttempts < maxFixAttempts && m.mode == modeAI {
		m.fixAttempts++
		m.appendTranscript(dimStyle.Render("↻ auto-fixing…") + "\n\n")
		m.state = tuiThinking
		m.streamBuf.Reset()
		return m, m.startAIRequest()
	}

	m.state = tuiIdle
	m.input.Focus()
	return m, nil
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
	return func() tea.Msg {
		if len(fns) == 0 {
			return objectsMsg{errs: []error{errNoManageAPI}}
		}
		msg := objectsMsg{
			windows: make([][]backends.ObjectNode, len(fns)),
			errs:    make([]error, len(fns)),
		}
		for i, f := range fns {
			if f.fn != nil {
				nodes, err := f.fn()
				msg.windows[i] = nodes
				msg.errs[i] = err
			}
		}
		return msg
	}
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
		}
		if i < len(msg.errs) {
			m.objTypes[i].err = msg.errs[i]
			if msg.errs[i] != nil && !errors.Is(msg.errs[i], errNoManageAPI) {
				log.Verbose("objects fetch [%s]: %s", m.objTypes[i].label, msg.errs[i])
			}
		}
		// Clamp selection.
		filtered := m.getFilteredSortedNodes(i)
		if m.objTypes[i].sel >= len(filtered) {
			m.objTypes[i].sel = max(0, len(filtered)-1)
		}
	}

	// Schedule the next periodic refresh if this was a background fetch.
	if m.refreshing {
		m.refreshing = false
		m.lastFetchDur = time.Since(m.refreshStart)
		if m.autoUpdateEnabled() {
			next := time.Duration(refreshFactor) * m.lastFetchDur
			if next < m.refreshPeriod {
				next = m.refreshPeriod
			}
			return m, tea.Tick(next, func(time.Time) tea.Msg { return refreshTickMsg{} })
		}
	}
	return m, nil
}

// ---------- Sidebar helpers ----------

// cycleFocus moves focus to the next (forward=true) or previous available pane.
func (m *aiTUIModel) cycleFocus(forward bool) {
	// Targets: focusChat (0), then 1..len(objTypes) for each window.
	n := len(m.objTypes) + 1
	if n <= 1 {
		return
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
		if wi := int(m.focus) - 1; wi >= 0 && wi < len(m.objTypes) &&
			m.objTypes[wi].kind == objWindowProcs {
			m.enterProcessView()
		}
	}
}

// moveSel moves the selection in the focused pane by delta.
func (m *aiTUIModel) moveSel(delta int) {
	wi := int(m.focus) - 1
	if wi < 0 || wi >= len(m.objTypes) {
		return
	}
	items := m.getFilteredSortedNodes(wi)
	if len(items) == 0 {
		return
	}
	m.objTypes[wi].sel = clampInt(m.objTypes[wi].sel+delta, 0, len(items)-1)
}

// selectedName returns the name of the currently selected item in the focused pane.
func (m *aiTUIModel) selectedName() string {
	wi := int(m.focus) - 1
	if wi < 0 || wi >= len(m.objTypes) {
		return ""
	}
	items := m.getFilteredSortedNodes(wi)
	if m.objTypes[wi].sel < len(items) {
		return items[m.objTypes[wi].sel].Name
	}
	return ""
}

// cycleSort advances the sort mode for the focused pane.
func (m *aiTUIModel) cycleSort() {
	wi := int(m.focus) - 1
	if wi < 0 || wi >= len(m.objTypes) {
		return
	}
	w := &m.objTypes[wi]
	metrics := firstMetrics(w.nodes)
	nModes := 1 + len(metrics) // name + one per metric
	w.sortIdx = sortMode((int(w.sortIdx) + 1) % nModes)
	w.sel = 0
}

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

const (
	reconnectInitialBackoff = 2 * time.Second
	reconnectMaxBackoff     = 3 * time.Minute
)

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
	// Re-layout: prompt width may have changed (e.g. "ask> " vs "awsmc> ").
	// updateInputHeight recomputes inputLines for the new prompt width, then calls recalcLayout.
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
	if len(lcp) > 0 && len(lcp) > int(prefixLen) {
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
		n := len(prefix)
		if len(c) < n {
			n = len(c)
		}
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

// isMessageReadCommand reports whether cmd is one that consumes / peeks
// messages and therefore produces payload output on stdout.
func isMessageReadCommand(cmd string) bool {
	verbs := []string{"receive ", "receive\n", "get ", "get\n", "peek ", "peek\n", "subscribe ", "subscribe\n"}
	cmd = strings.TrimSpace(cmd)
	for _, v := range verbs {
		if strings.HasPrefix(cmd, strings.TrimSpace(v)) {
			return true
		}
	}
	// Also match bare verb (no args).
	switch cmd {
	case "receive", "get", "peek", "subscribe":
		return true
	}
	return false
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
// resolves a proposed command. The marker (✓, ✗, ?) is appended with the
// appropriate style. When dim is true the command text is rendered in dimStyle.
func freezeProposal(cmd, marker string, dim, destructive bool) string {
	var prefix string
	if dim {
		prefix = dimStyle.Render("▶ " + cmd)
	} else {
		prefix = cmdStyle.Render("▶ " + cmd)
	}
	var result string
	switch marker {
	case "✓":
		result = prefix + " " + cmdStyle.Render("✓")
	case "✗":
		result = prefix + " " + warnStyle.Render("✗")
	case "?":
		result = prefix + " " + infoStyle.Render("?")
	default:
		result = prefix
	}
	if destructive && marker != "✗" {
		result += "\n" + warnStyle.Render("  ⚠ destructive — review carefully")
	}
	return result + "\n\n"
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
			b.WriteString(pickerSelectedStyle.Render("▸ " + label) + "\n")
		} else {
			b.WriteString(dimStyle.Render("  " + label) + "\n")
		}
	}
	b.WriteString(dimStyle.Render("\n↑/↓ select · Enter confirm · Esc cancel") + "\n")
	return b.String()
}
