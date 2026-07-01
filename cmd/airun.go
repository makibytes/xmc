package cmd

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/makibytes/xmc/log"
)

// ---------- AI request ----------

func (m aiTUIModel) startAIRequest() tea.Cmd {
	ai := m.ai
	pptr := m.program

	// Snapshot the request history on the UI goroutine, before handing off to
	// the background tea.Cmd below. ai.history is UI-goroutine-owned (see the
	// aiSession doc comment) — Esc-to-cancel (handleKeyThinking) mutates it
	// while this very request is in flight, so the background closure must
	// never read ai.history itself; that would race with the cancel path.
	// leadingUserHistory also ensures the payload starts with a "user" turn
	// regardless of how the history got here (Anthropic/Gemini reject a
	// leading "assistant" message — e.g. after a cmd-mode command run before
	// the first AI prompt, which trimHistory alone doesn't catch until
	// history exceeds maxHistory).
	history := append([]aiMessage(nil), leadingUserHistory(ai.history)...)
	// topology is mu-guarded (refreshTopology writes it from background
	// goroutines), so the read needs the lock even here on the UI goroutine.
	ai.mu.Lock()
	needsTopology := ai.topology == "" && len(ai.history) <= 1
	ai.mu.Unlock()

	return func() tea.Msg {
		if err := ai.init(); err != nil {
			return aiDoneMsg{err: err}
		}

		if needsTopology {
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

		ai.mu.Lock()
		sysPrompt := ai.sysPrompt
		ai.mu.Unlock()

		text, usage, err := ai.client.Complete(ctx, sysPrompt, history, onToken)
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
	isAsk := strings.HasPrefix(command, "# ask:")
	isCannot := strings.HasPrefix(command, "# cannot:")
	// A "#" line that isn't one of the two canonical forms is not a command —
	// e.g. the model inventing its own confirmation question instead of just
	// emitting the (possibly destructive) command. Treat it the same as an
	// empty response rather than letting it fall through to the proposal
	// flow, where it would be shown as a runnable command and, if accepted,
	// silently no-op as a shell comment.
	if command == "" || (strings.HasPrefix(command, "#") && !isAsk && !isCannot) {
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
	m.ai.mu.Lock()
	m.ai.rebuildPrompt()
	m.ai.mu.Unlock()
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
		// The feedback message is appended to ai.history in handleExecDone,
		// on the UI goroutine — not here (see aiSession doc comment on history).
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

	// Feed the execution result back into the AI conversation so it can see
	// what happened (and self-correct on the next turn / auto-fix retry).
	// This must happen on the UI goroutine — see the aiSession doc comment on
	// history — hence it lives here rather than in startExecution's closure.
	feedback := buildFeedback(msg.err, msg.stdout, msg.stderr)
	m.ai.history = append(m.ai.history, aiMessage{Role: "user", Content: feedback})
	trimHistory(&m.ai.history, maxHistory)

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
