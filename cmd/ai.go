// Package cmd implements the shared CLI skeleton, commands, shell, and AI TUI used by every broker binary.
package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
)

// maxHistory is the maximum number of messages kept in the AI conversation.
// ~12 messages ≈ 6 user/assistant turns, keeping context bounded.
const maxHistory = 12

// maxCapture is the maximum bytes of each output stream fed back to the AI
// after a command runs. Only the tail is kept to stay within token budgets.
const maxCapture = 2048

// maxDisplayCapture bounds how much of a foreground command's output the
// transcript shows. Deliberately much larger than maxCapture: the user wants
// to see e.g. all 50 messages of "receive q -n 50", while the model only
// needs the tail to judge success.
const maxDisplayCapture = 64 * 1024

// maxFixAttempts is how many times the AI auto-retries after a command error
// before giving up and returning to idle.
const maxFixAttempts = 2

// aiSession is shared between the Bubble Tea UI goroutine and the background
// goroutines spawned as tea.Cmd closures (AI requests, command execution).
// Two different concurrency disciplines apply to its fields:
//
//   - history is UI-goroutine-owned: it is only ever read or mutated from
//     aiTUIModel.Update() and its direct callees (handleKey*, handleAIDone,
//     handleExecDone, ...). Background closures that need it (startAIRequest)
//     must receive a snapshot captured synchronously on the UI goroutine
//     before the closure is spawned, never read ai.history themselves — the
//     user can mutate history via Esc-to-cancel while a request is in flight,
//     and an unsynchronized read from the background goroutine would race
//     with that write.
//   - sysPrompt/topology are guarded by mu, since rebuildPrompt/refreshTopology
//     can run from a background goroutine (topology refresh during an AI
//     request or command execution).
type aiSession struct {
	mu                 sync.Mutex
	client             aiClient
	sysPrompt          string // guarded by mu
	brokerContext      string
	topology           string          // guarded by mu
	capabilities       string          // cached buildCapabilities result (command tree is static)
	destructive        []string        // cached destructiveCommands result (command tree is static)
	verbSet            map[string]bool // cached verb set for extractCommandWithVerbs
	aliases            map[string]string
	session            *shellSession
	rootCmd            *cobra.Command
	history            []aiMessage // UI-goroutine-owned; see struct doc above
	initOnce           sync.Once
	initErr            error
	providerName       string
	modelName          string
	effort             aiEffort      // chosen effort; "" until init() reads ai.effort (or /effort sets it)
	autoUpdateObjects  bool          // refresh sidebar on create/delete/bind topology changes
	autoUpdateMessages bool          // refresh sidebar on send/publish/receive/purge message changes
	refreshPeriod      time.Duration // base periodic refresh interval (floor before adaptive scaling)
	refreshEnabled     bool          // whether periodic sidebar refresh is active
}

func (a *aiSession) init() error {
	a.initOnce.Do(func() {
		cfg, err := loadConfig()
		if err != nil {
			a.initErr = err
			return
		}
		spec, err := resolveProvider(cfg, os.Getenv)
		if err != nil {
			a.initErr = err
			return
		}
		a.client = newAIClient(spec)
		a.providerName = spec.name
		a.modelName = spec.model
		// An /effort chosen before the first request (when no client existed
		// yet) wins over the configured one.
		if a.effort == "" {
			a.effort, _ = parseEffort(cfg.AI.Effort)
		}
		if setter, ok := a.client.(modelSettable); ok && a.effort != "" {
			setter.SetEffort(a.effort)
		}
		a.verbSet = buildVerbSet(a.rootCmd)
		a.mu.Lock()
		a.rebuildPrompt()
		a.mu.Unlock()
		log.Verbose("AI provider: %s, model: %s", spec.name, spec.model)
	})
	return a.initErr
}

// currentEffort returns the effort in effect: the client's once it exists,
// otherwise the pending choice, otherwise the default (low).
func (a *aiSession) currentEffort() aiEffort {
	if setter, ok := a.client.(modelSettable); ok {
		return setter.Effort()
	}
	return normalizeEffort(a.effort)
}

// setEffort applies effort to the session (and its client, if created yet).
func (a *aiSession) setEffort(effort aiEffort) {
	a.effort = normalizeEffort(effort)
	if setter, ok := a.client.(modelSettable); ok {
		setter.SetEffort(a.effort)
	}
}

// rebuildPrompt rebuilds the system prompt from the current state (capabilities,
// broker docs, server URL, and cached topology). Callers must hold a.mu, since
// it writes a.sysPrompt (rebuildPrompt itself does not lock, so it can be
// called from within a section that already holds the lock, e.g. refreshTopology).
func (a *aiSession) rebuildPrompt() {
	if a.capabilities == "" {
		a.capabilities = buildCapabilities(a.rootCmd)
		a.destructive = destructiveCommands(a.rootCmd)
	}

	var server string
	if f := a.rootCmd.PersistentFlags().Lookup("server"); f != nil {
		server = f.Value.String()
	}

	a.sysPrompt = systemPrompt(a.capabilities, a.brokerContext, server, a.topology, a.aliases, a.destructive)
	log.Verbose("AI system prompt: %d bytes", len(a.sysPrompt))
}

// refreshTopology runs "manage list" to discover queues/topics on the broker
// and caches the result for the system prompt. Errors are swallowed —
// topology is a nice-to-have, not a requirement.
func (a *aiSession) refreshTopology() {
	if a.session.spec.ManageSpec == nil {
		return
	}

	var buf bytes.Buffer
	err := a.session.executePipelineIO(context.Background(), "manage list", a.rootCmd, strings.NewReader(""), &buf, io.Discard)
	if err != nil {
		log.Verbose("AI topology refresh: %s", err)
		return
	}

	result := strings.TrimSpace(buf.String())
	if result == "" {
		return
	}
	a.mu.Lock()
	if result != a.topology {
		a.topology = result
		a.rebuildPrompt()
	}
	a.mu.Unlock()
}

// trimHistory keeps the last maxLen messages, preserving conversation order.
// It ensures the retained slice starts with a "user" message so that
// user/assistant pairs remain intact.
func trimHistory(history *[]aiMessage, maxLen int) {
	if len(*history) <= maxLen {
		return
	}
	*history = (*history)[len(*history)-maxLen:]
	*history = leadingUserHistory(*history)
}

// leadingUserHistory drops any leading non-"user" messages so the returned
// slice always starts with a "user" turn. Anthropic and Gemini both reject
// requests whose first message has a different role; that role mismatch can
// occur even with a short history — e.g. a cmd-mode command run before the
// first AI prompt appends an "assistant" message first (see startExecution /
// startBackgroundProcess), and trimHistory alone only fixes this once the
// history grows past maxHistory. Callers should apply this to the slice sent
// to the AI client, not necessarily to the stored ai.history.
func leadingUserHistory(history []aiMessage) []aiMessage {
	for len(history) > 0 && history[0].Role != "user" {
		history = history[1:]
	}
	return history
}

// buildFeedback formats the execution result as a history message so the AI
// can see what happened and self-correct on the next turn.
func buildFeedback(err error, stdout, stderr string) string {
	var b strings.Builder
	b.WriteString("[execution result] ")
	if err != nil {
		fmt.Fprintf(&b, "error: %s", err)
	} else {
		b.WriteString("ok")
	}
	writeFeedbackStream(&b, "stdout", stdout)
	writeFeedbackStream(&b, "stderr", stderr)
	return b.String()
}

// writeFeedbackStream appends one captured stream to the feedback message,
// saying so when only its tail (maxCapture bytes) was kept.
func writeFeedbackStream(b *strings.Builder, name, text string) {
	if len(text) >= maxCapture { // a full tail: the stream was longer
		name = fmt.Sprintf("%s (last %d bytes)", name, maxCapture)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	fmt.Fprintf(b, "\n%s:\n%s", name, text)
}

// stageVerb parses one pipeline stage into its canonical xmc verb (aliases
// such as put/get/respond resolved) and, for "manage", its subcommand. Both
// are lower-cased — classification errs on the side of recognising a verb
// (e.g. a destructive "MANAGE PURGE q" is still flagged). A leading binary
// name ("amc send …") is ignored; verb is "" for an external command.
func stageVerb(stage string) (verb, sub string) {
	fields := strings.Fields(strings.ToLower(stripBinaryPrefix(strings.TrimSpace(stage))))
	if len(fields) == 0 {
		return "", ""
	}
	verb = canonicalVerb(fields[0])
	if verb == "manage" && len(fields) > 1 {
		sub = fields[1]
	}
	return verb, sub
}

// isDestructive returns true if a single pipeline stage would permanently
// destroy broker objects or purge message storage: every manage delete-*,
// unbind-* and purge* subcommand. Fetching or relaying messages (receive,
// move, forward, subscribe) is NOT destructive. Prefix rules rather than a
// list, so a newly added "manage delete-…" subcommand is covered without
// anyone remembering to register it (TestManageDestructiveCoverage).
func isDestructive(stage string) bool {
	verb, sub := stageVerb(stage)
	return verb == "manage" &&
		(strings.HasPrefix(sub, "delete-") || strings.HasPrefix(sub, "unbind-") || strings.HasPrefix(sub, "purge"))
}

// mutatesObjects returns true if the stage creates, deletes, reconfigures, or
// rebinds broker entities (queues, topics, exchanges, addresses, bindings,
// consumer groups) — i.e. anything that changes what the sidebar lists.
func mutatesObjects(stage string) bool {
	verb, sub := stageVerb(stage)
	if verb != "manage" {
		return false
	}
	for _, p := range []string{"create-", "delete-", "update-", "enable-", "disable-", "bind-", "unbind-"} {
		if strings.HasPrefix(sub, p) {
			return true
		}
	}
	return false
}

// messageVerbs are the verbs whose execution can change the message counts
// the sidebar shows (peek included: on some brokers a browse/nack updates
// delivery counters).
var messageVerbs = map[string]bool{
	"send": true, "receive": true, "peek": true, "request": true, "reply": true,
	"move": true, "forward": true, "bridge": true, "publish": true, "subscribe": true,
}

// mutatesMessages returns true if the stage changes message counts (send,
// receive, purge, move, ...).
func mutatesMessages(stage string) bool {
	verb, sub := stageVerb(stage)
	if verb == "manage" {
		return strings.HasPrefix(sub, "purge")
	}
	return messageVerbs[verb]
}

// isManageList returns true if the stage is a "manage list" invocation.
func isManageList(stage string) bool {
	verb, sub := stageVerb(stage)
	return verb == "manage" && sub == "list"
}

// isMessageRead returns true if the stage prints message payloads (receive,
// peek, subscribe, and request's reply).
func isMessageRead(stage string) bool {
	switch verb, _ := stageVerb(stage); verb {
	case "receive", "peek", "subscribe", "request":
		return true
	}
	return false
}

// anyCommand reports whether predicate matches any stage of line — every
// ';'-separated command and every '|'-separated stage within it — after
// expanding aliases the same way the executor does (executePipelineIO), so a
// user alias for "manage purge $1" is recognised as destructive, and
// "grep x | send q" as message-mutating.
func anyCommand(line string, aliases map[string]string, predicate func(string) bool) bool {
	for _, command := range splitCommands(expandAlias(strings.TrimSpace(line), aliases)) {
		for _, stage := range splitPipeline(command) {
			if predicate(stage) {
				return true
			}
		}
	}
	return false
}

// destructiveCommands lists the "manage …" subcommands this binary actually
// registers that isDestructive flags, for the AI system prompt — derived
// from the command tree so the prompt can never disagree with the
// confirmation logic, and never names a subcommand this broker lacks.
func destructiveCommands(rootCmd *cobra.Command) []string {
	if rootCmd == nil {
		return nil
	}
	var out []string
	for _, c := range rootCmd.Commands() {
		if c.Name() != "manage" {
			continue
		}
		for _, sub := range c.Commands() {
			if full := "manage " + sub.Name(); isDestructive(full) {
				out = append(out, full)
			}
		}
	}
	return out
}

// cappedBuffer is a bytes.Buffer that only keeps the last `max` bytes.
// max <= 0 (including the zero value) means unbounded — Write/String never
// trim. Callers that need a cap must set max explicitly; this guards against
// the zero-value footgun of silently discarding all written output.
type cappedBuffer struct {
	buf bytes.Buffer
	max int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	c.buf.Write(p)
	if c.max > 0 && c.buf.Len() > 2*c.max {
		b := c.buf.Bytes()
		c.buf.Reset()
		c.buf.Write(b[len(b)-c.max:])
	}
	return n, nil
}

func (c *cappedBuffer) String() string {
	b := c.buf.Bytes()
	if c.max > 0 && len(b) > c.max {
		return string(b[len(b)-c.max:])
	}
	return c.buf.String()
}

// outChunk is one contiguous run of output written to a single stream.
type outChunk struct {
	stderr bool
	text   []byte
}

// execCapture records a foreground command's stdout and stderr for the AI
// shell. Every stage of a pipeline may write concurrently (stderr is shared
// by all of them), so it is mutex-guarded. It keeps two views: the
// interleaved chunks in write order for the transcript — so a receive's
// "Properties: …" lines (stderr) stay next to the payload they describe
// (stdout), as in a terminal — bounded by maxDisplayCapture with the oldest
// output dropped first; and each stream's tail for the AI feedback message.
type execCapture struct {
	mu        sync.Mutex
	chunks    []outChunk
	size      int
	gen       int // bumped on every write; lets the UI repaint the live preview only on change
	truncated bool
	stdout    cappedBuffer
	stderr    cappedBuffer
}

func newExecCapture() *execCapture {
	c := &execCapture{}
	c.stdout.max = maxCapture
	c.stderr.max = maxCapture
	return c
}

// writer returns the io.Writer for one of the two streams.
func (c *execCapture) writer(stderr bool) io.Writer { return captureWriter{c: c, stderr: stderr} }

type captureWriter struct {
	c      *execCapture
	stderr bool
}

func (w captureWriter) Write(p []byte) (int, error) {
	c := w.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if w.stderr {
		_, _ = c.stderr.Write(p)
	} else {
		_, _ = c.stdout.Write(p)
	}
	if n := len(c.chunks); n > 0 && c.chunks[n-1].stderr == w.stderr {
		c.chunks[n-1].text = append(c.chunks[n-1].text, p...)
	} else {
		c.chunks = append(c.chunks, outChunk{stderr: w.stderr, text: append([]byte(nil), p...)})
	}
	c.size += len(p)
	c.gen++
	c.trim()
	return len(p), nil
}

// generation returns the write counter (see gen).
func (c *execCapture) generation() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen
}

// tail returns up to maxLines trailing lines of the interleaved output, for
// the live preview of a still-running command.
func (c *execCapture) tail(maxLines int) []string {
	c.mu.Lock()
	var all []byte
	for _, ch := range c.chunks {
		all = append(all, ch.text...)
	}
	c.mu.Unlock()
	lines := strings.Split(strings.TrimRight(string(all), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return lines
}

// trim drops the oldest output until the display budget fits, cutting a
// partially dropped chunk at a line boundary where it has one.
func (c *execCapture) trim() {
	for c.size > maxDisplayCapture && len(c.chunks) > 0 {
		c.truncated = true
		excess := c.size - maxDisplayCapture
		first := c.chunks[0].text
		if len(first) <= excess {
			c.size -= len(first)
			c.chunks = c.chunks[1:]
			continue
		}
		cut := excess
		if nl := bytes.IndexByte(first[cut:], '\n'); nl >= 0 {
			cut += nl + 1
		}
		c.size -= cut
		c.chunks[0].text = first[cut:]
	}
}

// snapshot returns the captured output: the display chunks (copied), whether
// older output was dropped, and the two streams' tails for the AI.
func (c *execCapture) snapshot() (chunks []outChunk, truncated bool, stdout, stderr string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	chunks = make([]outChunk, len(c.chunks))
	copy(chunks, c.chunks)
	return chunks, c.truncated, c.stdout.String(), c.stderr.String()
}
