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

// maxCapture is the maximum bytes captured from command output for the
// feedback loop. Only the tail is kept to stay within token budgets.
const maxCapture = 2048

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
	verbSet            map[string]bool // cached verb set for extractCommandWithVerbs
	aliases            map[string]string
	session            *shellSession
	rootCmd            *cobra.Command
	history            []aiMessage // UI-goroutine-owned; see struct doc above
	initOnce           sync.Once
	initErr            error
	providerName       string
	modelName          string
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
		a.verbSet = buildVerbSet(a.rootCmd)
		a.mu.Lock()
		a.rebuildPrompt()
		a.mu.Unlock()
		log.Verbose("AI provider: %s, model: %s", spec.name, spec.model)
	})
	return a.initErr
}

// rebuildPrompt rebuilds the system prompt from the current state (capabilities,
// broker docs, server URL, and cached topology). Callers must hold a.mu, since
// it writes a.sysPrompt (rebuildPrompt itself does not lock, so it can be
// called from within a section that already holds the lock, e.g. refreshTopology).
func (a *aiSession) rebuildPrompt() {
	if a.capabilities == "" {
		a.capabilities = buildCapabilities(a.rootCmd)
	}

	var server string
	if f := a.rootCmd.PersistentFlags().Lookup("server"); f != nil {
		server = f.Value.String()
	}

	a.sysPrompt = systemPrompt(a.capabilities, a.brokerContext, server, a.topology, a.aliases)
	log.Verbose("AI system prompt: %d bytes", len(a.sysPrompt))
}

// refreshTopology runs "manage list" to discover queues/topics on the broker
// and caches the result for the system prompt. Errors are swallowed —
// topology is a nice-to-have, not a requirement.
func (a *aiSession) refreshTopology() {
	if a.session.spec.Manage == nil && a.session.spec.ManageSpec == nil {
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

// resetHistory clears the conversation history and refreshes topology.
// The caller (the /reset slash command) is responsible for notifying the
// user via the transcript — this must not write to stderr, since the AI TUI
// owns the terminal's alternate screen for the duration of the session.
func (a *aiSession) resetHistory() {
	a.mu.Lock()
	a.history = nil
	a.mu.Unlock()
	a.refreshTopology()
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
	out := strings.TrimSpace(stdout)
	if out != "" {
		fmt.Fprintf(&b, "\nstdout (last %d bytes):\n%s", len(out), out)
	}
	errOut := strings.TrimSpace(stderr)
	if errOut != "" {
		fmt.Fprintf(&b, "\nstderr:\n%s", errOut)
	}
	return b.String()
}

// isDestructive returns true if a single command would permanently destroy
// broker objects or purge message storage. Fetching or relaying messages
// (receive, move, forward, subscribe) is NOT destructive — only deleting
// broker entities and purging queues is.
var destructivePrefixes = []string{
	"manage delete-queue",
	"manage delete-topic",
	"manage delete-exchange",
	"manage unbind-queue",
	"manage purge",
}

// objectPrefixes lists commands that create, delete, or rebind broker entities
// (topology changes). Refreshing the sidebar shows new/removed objects.
var objectPrefixes = []string{
	"manage create-queue",
	"manage delete-queue",
	"manage create-topic",
	"manage delete-topic",
	"manage create-exchange",
	"manage delete-exchange",
	"manage bind-queue",
	"manage unbind-queue",
}

// messagePrefixes lists commands that change message counts in queues/topics
// (send, receive, purge, etc.). Refreshing the sidebar shows updated counts.
var messagePrefixes = []string{
	"manage purge",
	"move ",
	"send ",
	"publish ",
	"receive ",
	"peek ",
	"request ",
	"reply ",
	"forward ",
	"subscribe ",
}

func isDestructive(command string) bool {
	return matchesPrefix(command, destructivePrefixes)
}

// mutatesObjects returns true if the command creates, deletes, or rebinds
// broker entities (queues, topics, exchanges, bindings).
func mutatesObjects(command string) bool {
	return matchesPrefix(command, objectPrefixes)
}

// mutatesMessages returns true if the command changes message counts
// (send, receive, purge, move, etc.).
func mutatesMessages(command string) bool {
	return matchesPrefix(command, messagePrefixes)
}

// isManageList returns true if the command is a "manage list" invocation.
func isManageList(command string) bool {
	return matchesPrefix(command, []string{"manage list"})
}

func matchesPrefix(command string, prefixes []string) bool {
	lower := strings.ToLower(strings.TrimSpace(command))
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

// anyCommand splits line on top-level ';' and returns true if predicate
// matches any segment.
func anyCommand(line string, predicate func(string) bool) bool {
	for _, seg := range splitCommands(line) {
		if predicate(seg) {
			return true
		}
	}
	return false
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
