package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/makibytes/xmc/log"
)

// ---------- AI request ----------

// beginAIRequest enters the thinking state and starts a new AI request with
// a fresh generation; every AI request goes through here.
func (m *aiTUIModel) beginAIRequest() tea.Cmd {
	m.aiGen++
	m.state = tuiThinking
	m.streamBuf.Reset()
	return m.startAIRequest(m.aiGen)
}

func (m aiTUIModel) startAIRequest(gen int) tea.Cmd {
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
			return aiDoneMsg{err: err, gen: gen}
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
				prog.Send(tokenMsg{text: token, gen: gen})
			}
		}

		ai.mu.Lock()
		sysPrompt := ai.sysPrompt
		ai.mu.Unlock()

		text, usage, err := ai.client.Complete(ctx, sysPrompt, history, onToken)
		return aiDoneMsg{text: text, usage: usage, err: err, gen: gen}
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
	// A response for a request the user already cancelled (Esc while
	// thinking) — possibly one that completed in the same instant, or that
	// arrives while a newer request is in flight — must not resurface.
	if m.state != tuiThinking || msg.gen != m.aiGen {
		return m, nil
	}
	m.execCancel = nil
	if msg.err != nil {
		if errors.Is(msg.err, context.Canceled) {
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
	m.proposedDestructive = anyCommand(command, m.aliases(), isDestructive)
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

// applyEffort parses and applies a provider-aware effort level, persisting it
// to the config like /model does. Returns false if the argument is invalid.
func (m *aiTUIModel) applyEffort(arg string) bool {
	effort, ok := parseEffort(arg)
	if !ok {
		m.appendTranscript(warnStyle.Render("effort must be low, medium, or high") + "\n\n")
		return false
	}
	m.ai.setEffort(effort)
	msg := "effort → " + string(effort)
	if err := saveAIEffort(effort); err != nil {
		msg += fmt.Sprintf(" (save failed: %s)", err)
	}
	m.appendTranscript(dimStyle.Render(msg) + "\n\n")
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
	capture := newExecCapture()
	m.liveCapture = capture
	m.liveGen = 0

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

		execErr := sess.executePipelineIO(ctx, command, rootCmd, strings.NewReader(""), capture.writer(false), capture.writer(true))

		refresh := anyCommand(command, sess.aliases, isManageList) ||
			(ai.autoUpdateObjects && anyCommand(command, sess.aliases, mutatesObjects)) ||
			(ai.autoUpdateMessages && anyCommand(command, sess.aliases, mutatesMessages))
		if refresh {
			ai.refreshTopology()
		}

		chunks, truncated, stdout, stderr := capture.snapshot()
		// The feedback message is appended to ai.history in handleExecDone,
		// on the UI goroutine — not here (see aiSession doc comment on history).
		return execDoneMsg{err: execErr, stdout: stdout, stderr: stderr, chunks: chunks, truncated: truncated}
	}
}

func (m aiTUIModel) handleExecDone(msg execDoneMsg) (tea.Model, tea.Cmd) {
	cancelled := m.execCancelled
	m.execCancelled = false
	m.execCancel = nil
	m.liveCapture = nil

	// The command itself is already in the transcript directly above (the
	// cmd-mode echo or the accepted proposal, each with its ⧉ marker), so
	// only its output and outcome follow.
	var result strings.Builder
	result.WriteString(m.renderExecOutput(msg))

	switch {
	case cancelled:
		result.WriteString(dimStyle.Render("(cancelled)") + "\n")
	case msg.err != nil:
		result.WriteString(warnStyle.Render("✗ "+msg.err.Error()) + "\n")
	default:
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
	feedbackErr := msg.err
	if cancelled {
		feedbackErr = errors.New("cancelled by the user")
	}
	feedback := buildFeedback(feedbackErr, msg.stdout, msg.stderr)
	m.ai.history = append(m.ai.history, aiMessage{Role: "user", Content: feedback})
	trimHistory(&m.ai.history, maxHistory)

	// Auto-fix: on error, ask the AI to correct the command (up to
	// maxFixAttempts). Never after a user cancel — the interrupted command
	// (e.g. "signal: killed" from an external one) isn't a mistake to fix.
	if msg.err != nil && !cancelled && m.fixAttempts < maxFixAttempts && m.mode == modeAI {
		m.fixAttempts++
		m.appendTranscript(dimStyle.Render("↻ auto-fixing…") + "\n\n")
		return m, m.beginAIRequest()
	}

	m.state = tuiIdle
	m.input.Focus()
	return m, nil
}

// renderExecOutput renders a finished command's output for the transcript:
// stdout and stderr interleaved in the order they were written, stdout as a
// message-payload block for commands that print messages (receive, peek,
// subscribe, request) and dim text otherwise, stderr (message properties,
// --stats, warnings) in its own colour — so the AI shell shows what the same
// command prints in a terminal. A payload gets one ⧉ copy marker.
func (m *aiTUIModel) renderExecOutput(msg execDoneMsg) string {
	chunks := msg.chunks
	if chunks == nil {
		chunks = []outChunk{{text: []byte(msg.stdout)}, {stderr: true, text: []byte(msg.stderr)}}
	}
	read := anyCommand(m.proposedCmd, m.aliases(), isMessageRead)

	var b strings.Builder
	if msg.truncated {
		b.WriteString(dimStyle.Render(fmt.Sprintf("… earlier output dropped (showing the last %d KB) …", maxDisplayCapture/1024)) + "\n")
	}
	var payload []string
	for _, ch := range chunks {
		text := strings.TrimRight(string(ch.text), "\n")
		if strings.TrimSpace(text) == "" {
			continue
		}
		switch {
		case ch.stderr:
			b.WriteString(stderrStyle.Render(text) + "\n")
		case read:
			b.WriteString(renderMessagePayload(text))
			payload = append(payload, text)
		default:
			b.WriteString(dimStyle.Render(text) + "\n")
		}
	}
	if len(payload) > 0 {
		m.copyItems = append(m.copyItems, strings.Join(payload, "\n"))
		b.WriteString(copyHintStyle.Render("  ⧉") + "\n")
	}
	return b.String()
}
