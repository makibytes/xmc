package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
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
				"r            refresh the selected sidebar window now\n"+
				"m            peek metadata (where peek is available)\n"+
				"J / Y        metadata output format (JSON / YAML)\n"+
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
		case "J":
			if m.metadataFormat != metadataFormatJSON {
				m.metadataFormat = metadataFormatJSON
				msg := "metadata format → json"
				if err := saveMetadataFormat(metadataFormatJSON); err != nil {
					msg += fmt.Sprintf(" (save failed: %s)", err)
				}
				m.appendTranscript(dimStyle.Render(msg) + "\n\n")
			}
			return m, nil
		case "Y":
			if m.metadataFormat != metadataFormatYAML {
				m.metadataFormat = metadataFormatYAML
				msg := "metadata format → yaml"
				if err := saveMetadataFormat(metadataFormatYAML); err != nil {
					msg += fmt.Sprintf(" (save failed: %s)", err)
				}
				m.appendTranscript(dimStyle.Render(msg) + "\n\n")
			}
			return m, nil
		case "s":
			m.cycleSort()
			return m, nil
		case "r":
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

	case "purge":
		switch msg.Type {
		case tea.KeyEsc, tea.KeyCtrlC:
			m.promptActive = false
			m.input.Focus()
			return m, nil
		case tea.KeyEnter:
			if m.session == nil || m.session.spec.ManageSpec == nil || m.session.spec.ManageSpec.Purge == nil {
				m.promptActive = false
				m.input.Focus()
				return m, nil
			}
			name := m.promptName
			desc := fmt.Sprintf("▶ purge %s \"%s\"", singular(ow.label), name)
			m.appendTranscript(histCmdStyle.Render(desc) + "\n")
			m.promptActive = false
			m.state = tuiExecuting
			purge := m.session.spec.ManageSpec.Purge
			return m, func() tea.Msg {
				count, err := purge(name)
				if err != nil {
					return sideActionMsg{err: err}
				}
				if count > 0 {
					return sideActionMsg{action: fmt.Sprintf("   └ purged %d messages", count)}
				}
				return sideActionMsg{action: "   └ purged"}
			}
		}

	case "purge-subscription":
		switch msg.Type {
		case tea.KeyEsc, tea.KeyCtrlC:
			m.promptActive = false
			m.input.Focus()
			return m, nil
		case tea.KeyEnter:
			if m.session == nil || m.session.spec.ManageSpec == nil || m.session.spec.ManageSpec.PurgeSubscription == nil {
				m.promptActive = false
				m.input.Focus()
				return m, nil
			}
			sub := m.promptName
			topic := m.promptTarget
			desc := fmt.Sprintf("▶ purge Subscription \"%s\"", sub)
			m.appendTranscript(histCmdStyle.Render(desc) + "\n")
			m.promptActive = false
			m.state = tuiExecuting
			purge := m.session.spec.ManageSpec.PurgeSubscription
			return m, func() tea.Msg {
				count, err := purge(topic, sub)
				if err != nil {
					return sideActionMsg{err: err}
				}
				if count > 0 {
					return sideActionMsg{action: fmt.Sprintf("   └ purged %d messages", count)}
				}
				return sideActionMsg{action: "   └ purged"}
			}
		}

	case "send":
		switch msg.Type {
		case tea.KeyEsc, tea.KeyCtrlC:
			m.promptActive = false
			m.input.Focus()
			return m, nil
		case tea.KeyEnter:
			payload := m.promptName
			if payload == "" {
				return m, nil
			}
			target := m.promptTarget
			useTopic := sidebarSendViaTopic(ow.label, m.promptNodeKind)
			verb := "send"
			if useTopic {
				verb = "publish"
			}
			desc := fmt.Sprintf("▶ %s %s \"%s\"", verb, singular(ow.label), target)
			m.appendTranscript(histCmdStyle.Render(desc) + "\n")
			m.promptActive = false
			m.state = tuiExecuting
			session := m.session
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
			}
		case tea.KeyBackspace:
			if len(m.promptName) > 0 {
				m.promptName = m.promptName[:len(m.promptName)-1]
			}
			return m, nil
		case tea.KeyRunes, tea.KeySpace:
			m.promptName += msg.String()
			return m, nil
		}

	case "publish":
		switch msg.Type {
		case tea.KeyEsc, tea.KeyCtrlC:
			m.promptActive = false
			m.input.Focus()
			return m, nil
		case tea.KeyEnter:
			payload := m.promptName
			if payload == "" {
				return m, nil
			}
			target := m.promptTarget
			desc := fmt.Sprintf("▶ publish %s \"%s\"", singular(ow.label), target)
			m.appendTranscript(histCmdStyle.Render(desc) + "\n")
			m.promptActive = false
			m.state = tuiExecuting
			session := m.session
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				ta, err := session.getTopicAdapter()
				if err != nil {
					return sideActionMsg{err: fmt.Errorf("adapter: %w", err)}
				}
				if err := ta.Publish(ctx, backends.PublishOptions{Topic: target, Message: []byte(payload)}); err != nil {
					return sideActionMsg{err: err}
				}
				return sideActionMsg{action: "   └ published"}
			}
		case tea.KeyBackspace:
			if len(m.promptName) > 0 {
				m.promptName = m.promptName[:len(m.promptName)-1]
			}
			return m, nil
		case tea.KeyRunes, tea.KeySpace:
			m.promptName += msg.String()
			return m, nil
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

func (m aiTUIModel) handleKeyExecuting(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyEsc || msg.Type == tea.KeyCtrlC {
		if m.execCancel != nil {
			m.execCancel()
		}
		return m, nil
	}
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
		rows := m.sidebarRows(i)
		if m.objTypes[i].sel >= len(rows) {
			m.objTypes[i].sel = max(0, len(rows)-1)
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
	wi := int(m.focus) - 1
	if wi < 0 || wi >= len(m.objTypes) {
		return
	}
	rows := m.sidebarRows(wi)
	if len(rows) == 0 {
		return
	}
	m.objTypes[wi].sel = clampInt(m.objTypes[wi].sel+delta, 0, len(rows)-1)
}

// selectedNode returns the full ObjectNode for the currently selected row in
// the focused pane — top-level or child (ok=false when nothing is selected or
// the pane is empty). Most callers that dispatch an action keyed by window
// label (not node kind) should use selectedTopLevelNode instead, since a
// child row's name is not a valid target for those actions.
func (m *aiTUIModel) selectedNode() (backends.ObjectNode, bool) {
	wi := int(m.focus) - 1
	if wi < 0 || wi >= len(m.objTypes) {
		return backends.ObjectNode{}, false
	}
	rows := m.sidebarRows(wi)
	if m.objTypes[wi].sel < len(rows) {
		return rows[m.objTypes[wi].sel].node, true
	}
	return backends.ObjectNode{}, false
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
	wi := int(m.focus) - 1
	if wi < 0 || wi >= len(m.objTypes) {
		return backends.ObjectNode{}, false
	}
	rows := m.sidebarRows(wi)
	if m.objTypes[wi].sel >= len(rows) {
		return backends.ObjectNode{}, false
	}
	row := rows[m.objTypes[wi].sel]
	if row.parentName != "" {
		return backends.ObjectNode{}, false
	}
	return row.node, true
}

// selectedChildNode returns the currently selected node and its parent's name
// only when the selection is a child row (ok=false for a top-level row).
func (m *aiTUIModel) selectedChildNode() (node backends.ObjectNode, parentName string, ok bool) {
	wi := int(m.focus) - 1
	if wi < 0 || wi >= len(m.objTypes) {
		return backends.ObjectNode{}, "", false
	}
	rows := m.sidebarRows(wi)
	if m.objTypes[wi].sel >= len(rows) {
		return backends.ObjectNode{}, "", false
	}
	row := rows[m.objTypes[wi].sel]
	if row.parentName == "" {
		return backends.ObjectNode{}, "", false
	}
	return row.node, row.parentName, true
}

// sidebarWindowSupportsSRP reports whether Purge/Send/Receive apply, in
// principle, to a window with this label. Per-node Kind gating (Artemis
// Addresses) is applied separately via sidebarPurgeReceiveAllowed/sidebarSendViaTopic.
func sidebarWindowSupportsSRP(label string) bool {
	switch label {
	case "Queues", "Streams", "Addresses", "Exchanges":
		return true
	}
	return false
}

// sidebarPurgeReceiveAllowed reports whether Purge/Receive apply to node.
// Artemis's Addresses window and RabbitMQ's Exchanges window never allow
// them, regardless of Kind: both are routing entities with no reliable 1:1
// mapping to a queue of the same name — an address is merely the default
// when a queue is created without an explicit --address (and may equally be
// bound to differently-named or multiple queues), and an exchange routes to
// whatever queues its bindings name. Purging/receiving "by address/exchange
// name" would then silently target the wrong queue (or nothing). Use the
// Queues window, which purges/receives by an actual queue name, instead.
func sidebarPurgeReceiveAllowed(label string, _ backends.ObjectNode) bool {
	if label == "Addresses" || label == "Exchanges" {
		return false
	}
	return true // Queues/Streams — already gated by sidebarWindowSupportsSRP
}

// sidebarSendViaTopic reports whether Send should dispatch through
// TopicBackend.Publish instead of QueueBackend.Send. True for a
// pure-MULTICAST Artemis address (no anycast component) or any RabbitMQ
// exchange (always routing-only); everything else (Queues, Streams,
// anycast/any-multi/unknown-Kind Addresses) sends via the queue adapter.
func sidebarSendViaTopic(label, kind string) bool {
	return (label == "Addresses" && kind == "multicast") || label == "Exchanges"
}

// sidebarSubscriptionEligible reports whether a Topics-window child node is a
// genuine message-storing subscription (Purge/peek/Receive apply), as opposed
// to a routing pointer. Azure and Google both tag real subscriptions with
// Kind == "subscription"; AWS's SNS subscription children use Kind == <SNS
// protocol> (e.g. "sqs") instead, so this naturally and correctly excludes
// them without any broker-identity check — their backing SQS queue is
// already independently reachable (and already S/R/P-enabled) via the flat
// Queues window.
func sidebarSubscriptionEligible(label, kind string) bool {
	return label == "Topics" && kind == "subscription"
}

// isNoMessage reports whether err means "the source was empty this read" —
// either the explicit sentinel or a bare receive/poll timeout. The AMQP
// backends (Artemis, RabbitMQ) surface an empty queue as the raw
// context.DeadlineExceeded from their own per-call wait deadline rather than
// ErrNoMessageAvailable (unlike their Browse/peek-cursor path, which already
// maps it); without this, the sidebar showed a confusing
// "✗ context deadline exceeded" instead of "no message available".
func isNoMessage(err error) bool {
	return errors.Is(err, backends.ErrNoMessageAvailable) || errors.Is(err, context.DeadlineExceeded)
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
	cmd = stripKnownBinaryPrefix(cmd)
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

func stripKnownBinaryPrefix(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return cmd
	}
	fields := strings.Fields(cmd)
	if len(fields) < 2 {
		return cmd
	}
	first := strings.TrimPrefix(filepath.Base(fields[0]), "./")
	switch first {
	case "xmc", "amc", "awsmc", "azmc", "gmc", "imc", "kmc", "mmc", "nmc", "pmc", "rmc", "redmc":
		return strings.Join(fields[1:], " ")
	default:
		return cmd
	}
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
			b.WriteString(pickerSelectedStyle.Render("▸ "+label) + "\n")
		} else {
			b.WriteString(dimStyle.Render("  "+label) + "\n")
		}
	}
	b.WriteString(dimStyle.Render("\n↑/↓ select · Enter confirm · Esc cancel") + "\n")
	return b.String()
}
