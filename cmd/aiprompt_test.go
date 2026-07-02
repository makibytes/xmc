package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestBuildCapabilities_IncludesVerbs(t *testing.T) {
	root := &cobra.Command{Use: "amc"}
	root.AddCommand(&cobra.Command{Use: "send", Short: "Send a message to a queue"})
	root.AddCommand(&cobra.Command{Use: "receive", Short: "Receive a message from a queue"})
	root.AddCommand(&cobra.Command{Use: "shell", Short: "Interactive shell"})
	root.AddCommand(&cobra.Command{Use: "version", Short: "Print version"})

	caps := buildCapabilities(root)

	if !strings.Contains(caps, "send") {
		t.Error("capabilities should include 'send'")
	}
	if !strings.Contains(caps, "receive") {
		t.Error("capabilities should include 'receive'")
	}
	if strings.Contains(caps, "## shell") {
		t.Error("capabilities should exclude 'shell'")
	}
	if strings.Contains(caps, "## version") {
		t.Error("capabilities should exclude 'version'")
	}
}

func TestBuildCapabilities_IncludesFlags(t *testing.T) {
	root := &cobra.Command{Use: "amc"}
	send := &cobra.Command{Use: "send", Short: "Send a message"}
	send.Flags().IntP("count", "n", 1, "Number of messages to send")
	send.Flags().StringP("property", "P", "", "Set application property key=value")
	root.AddCommand(send)

	caps := buildCapabilities(root)

	if !strings.Contains(caps, "--count") {
		t.Error("capabilities should include --count flag")
	}
	if !strings.Contains(caps, "-n") {
		t.Error("capabilities should include -n shorthand")
	}
}

func TestBuildCapabilities_ManageSubcommands(t *testing.T) {
	root := &cobra.Command{Use: "amc"}
	manage := &cobra.Command{Use: "manage", Short: "Management commands"}
	manage.AddCommand(&cobra.Command{Use: "list", Short: "List queues"})
	manage.AddCommand(&cobra.Command{Use: "purge", Short: "Purge queue"})
	root.AddCommand(manage)

	caps := buildCapabilities(root)

	if !strings.Contains(caps, "manage list") {
		t.Error("should include 'manage list' subcommand")
	}
	if !strings.Contains(caps, "manage purge") {
		t.Error("should include 'manage purge' subcommand")
	}
}

func TestExtractCommand_Plain(t *testing.T) {
	if got := extractCommand("receive q -n 5"); got != "receive q -n 5" {
		t.Errorf("got %q", got)
	}
}

func TestExtractCommand_CodeFence(t *testing.T) {
	input := "```\nreceive q -n 5\n```"
	if got := extractCommand(input); got != "receive q -n 5" {
		t.Errorf("got %q", got)
	}
}

func TestExtractCommand_CodeFenceWithLang(t *testing.T) {
	input := "```bash\nreceive q -n 5\n```"
	if got := extractCommand(input); got != "receive q -n 5" {
		t.Errorf("got %q", got)
	}
}

func TestExtractCommand_DollarPrefix(t *testing.T) {
	if got := extractCommand("$ receive q -n 5"); got != "receive q -n 5" {
		t.Errorf("got %q", got)
	}
}

func TestExtractCommand_Backticks(t *testing.T) {
	if got := extractCommand("`receive q -n 5`"); got != "receive q -n 5" {
		t.Errorf("got %q", got)
	}
}

func TestExtractCommand_Whitespace(t *testing.T) {
	if got := extractCommand("  \n  receive q -n 5  \n  "); got != "receive q -n 5" {
		t.Errorf("got %q", got)
	}
}

func TestExtractCommand_ProseAndCodeFence(t *testing.T) {
	input := "Here's the command:\n```bash\nreceive q -n 5\n```"
	if got := extractCommand(input); got != "receive q -n 5" {
		t.Errorf("got %q, want 'receive q -n 5'", got)
	}
}

func TestExtractCommand_ProseAndCodeFenceMultiLine(t *testing.T) {
	input := "I suggest:\n```\nreceive q -n 5 | jq .\n```\nThis will format the output."
	if got := extractCommand(input); got != "receive q -n 5 | jq ." {
		t.Errorf("got %q", got)
	}
}

func TestSystemPrompt_ContainsCapabilities(t *testing.T) {
	prompt := systemPrompt("## send\nSend a message\n", "", "", "", nil)
	if !strings.Contains(prompt, "## send") {
		t.Error("system prompt should include capabilities")
	}
	if !strings.Contains(prompt, "EXACTLY ONE") {
		t.Error("system prompt should instruct single command output")
	}
}

func TestSystemPrompt_ContainsNDJSONSchema(t *testing.T) {
	prompt := systemPrompt("## send\n", "", "", "", nil)
	for _, field := range []string{`"data"`, `"dataBase64"`, `"properties"`, `"messageId"`, `"correlationId"`, `"key"`} {
		if !strings.Contains(prompt, field) {
			t.Errorf("system prompt should document NDJSON field %s", field)
		}
	}
	if !strings.Contains(prompt, "Never use") {
		t.Error("system prompt should warn against wrong field names")
	}
}

func TestSystemPrompt_ContainsFramingRules(t *testing.T) {
	prompt := systemPrompt("## send\n", "", "", "", nil)
	if !strings.Contains(prompt, "auto-injected") {
		t.Error("system prompt should describe verb|verb auto-injection")
	}
	if !strings.Contains(prompt, "--ndjson explicitly") {
		t.Error("system prompt should explain when --ndjson must be explicit")
	}
}

func TestSystemPrompt_ContainsPipelineExamples(t *testing.T) {
	prompt := systemPrompt("## send\n", "", "", "", nil)
	if !strings.Contains(prompt, "jq -cs") {
		t.Error("system prompt should include a jq pipeline example")
	}
	if !strings.Contains(prompt, "max_by") {
		t.Error("system prompt should include the largest-payload example")
	}
}

func TestSystemPrompt_ContainsBrokerContext(t *testing.T) {
	prompt := systemPrompt("## send\n", "RabbitMQ uses exchanges for routing.", "", "", nil)
	if !strings.Contains(prompt, "exchanges for routing") {
		t.Error("system prompt should include broker context when provided")
	}
	if !strings.Contains(prompt, "Broker-specific documentation") {
		t.Error("system prompt should have broker documentation header")
	}
}

func TestSystemPrompt_OmitsBrokerContextWhenEmpty(t *testing.T) {
	prompt := systemPrompt("## send\n", "", "", "", nil)
	if strings.Contains(prompt, "Broker-specific documentation") {
		t.Error("system prompt should not include broker section when context is empty")
	}
}

func TestSystemPrompt_ContainsServerURL(t *testing.T) {
	prompt := systemPrompt("## send\n", "", "amqp://broker.example.com:5672", "", nil)
	if !strings.Contains(prompt, "amqp://broker.example.com:5672") {
		t.Error("system prompt should include the server URL")
	}
	if !strings.Contains(prompt, "Connection") {
		t.Error("system prompt should have Connection section header")
	}
}

func TestSystemPrompt_OmitsServerWhenEmpty(t *testing.T) {
	prompt := systemPrompt("## send\n", "", "", "", nil)
	if strings.Contains(prompt, "## Connection") {
		t.Error("system prompt should not include connection section when server is empty")
	}
}

func TestSystemPrompt_ContainsTopology(t *testing.T) {
	prompt := systemPrompt("## send\n", "", "", "Queues:\n  orders (5 messages)\n  events (0 messages)", nil)
	if !strings.Contains(prompt, "orders") {
		t.Error("system prompt should include topology queue names")
	}
	if !strings.Contains(prompt, "Current topology") {
		t.Error("system prompt should have topology section header")
	}
}

func TestSystemPrompt_OmitsTopologyWhenEmpty(t *testing.T) {
	prompt := systemPrompt("## send\n", "", "", "", nil)
	if strings.Contains(prompt, "Current topology") {
		t.Error("system prompt should not include topology section when empty")
	}
}

func TestSystemPrompt_ContainsAskDirective(t *testing.T) {
	prompt := systemPrompt("## send\n", "", "", "", nil)
	if !strings.Contains(prompt, "# ask:") {
		t.Error("system prompt should document the # ask: output form")
	}
}

func TestSystemPrompt_ContainsMultiTurnGuidance(t *testing.T) {
	prompt := systemPrompt("## send\n", "", "", "", nil)
	if !strings.Contains(prompt, "multi-turn") {
		t.Error("system prompt should explain multi-turn conversation support")
	}
}

// ---------- capTopologyLines ----------

func TestCapTopologyLines_UnderLimit(t *testing.T) {
	topo := "line1\nline2\nline3"
	got := capTopologyLines(topo, 10)
	if got != topo {
		t.Errorf("under limit: got %q, want unchanged", got)
	}
}

func TestCapTopologyLines_ExactLimit(t *testing.T) {
	topo := "line1\nline2\nline3"
	got := capTopologyLines(topo, 3)
	if got != topo {
		t.Errorf("exact limit: got %q, want unchanged", got)
	}
}

func TestCapTopologyLines_OverLimit(t *testing.T) {
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = "queue" + string(rune('A'+i))
	}
	topo := strings.Join(lines, "\n")
	got := capTopologyLines(topo, 3)
	if !strings.Contains(got, "queueA") || !strings.Contains(got, "queueC") {
		t.Errorf("should contain first 3 lines, got %q", got)
	}
	if strings.Contains(got, "queueD") {
		t.Errorf("should NOT contain 4th line, got %q", got)
	}
	if !strings.Contains(got, "7 more lines") {
		t.Errorf("should note 7 more lines, got %q", got)
	}
}

// ---------- buildVerbSet ----------

func testVerbRoot() *cobra.Command {
	root := &cobra.Command{Use: "amc"}
	root.AddCommand(&cobra.Command{Use: "send", Short: "Send"})
	root.AddCommand(&cobra.Command{Use: "receive", Short: "Receive"})
	root.AddCommand(&cobra.Command{Use: "peek", Short: "Peek"})
	root.AddCommand(&cobra.Command{Use: "shell", Short: "Shell"}) // excluded
	root.AddCommand(&cobra.Command{Use: "ai", Short: "AI"})       // excluded
	root.AddCommand(&cobra.Command{Use: "help", Short: "Help"})   // excluded
	manage := &cobra.Command{Use: "manage", Short: "Manage"}
	manage.AddCommand(&cobra.Command{Use: "list", Short: "List"})
	manage.AddCommand(&cobra.Command{Use: "purge", Short: "Purge"})
	root.AddCommand(manage)
	return root
}

func TestBuildVerbSet(t *testing.T) {
	verbs := buildVerbSet(testVerbRoot())

	for _, v := range []string{"send", "receive", "peek", "manage"} {
		if !verbs[v] {
			t.Errorf("verb %q should be in set", v)
		}
	}
	// Two-word manage subcommands.
	for _, v := range []string{"manage list", "manage purge"} {
		if !verbs[v] {
			t.Errorf("two-word verb %q should be in set", v)
		}
	}
	// Excluded verbs.
	for _, v := range []string{"shell", "ai", "help"} {
		if verbs[v] {
			t.Errorf("verb %q should NOT be in set", v)
		}
	}
}

// ---------- extractCommandWithVerbs ----------

func TestExtractCommandWithVerbs_ProseFallback(t *testing.T) {
	verbs := buildVerbSet(testVerbRoot())
	input := "Sure, here's how to do it:\nreceive orders -n 5\nThis reads 5 messages."
	got := extractCommandWithVerbs(input, verbs)
	if got != "receive orders -n 5" {
		t.Errorf("got %q, want 'receive orders -n 5'", got)
	}
}

func TestExtractCommandWithVerbs_FencedPassthrough(t *testing.T) {
	verbs := buildVerbSet(testVerbRoot())
	input := "```\nreceive orders -n 5\n```"
	got := extractCommandWithVerbs(input, verbs)
	if got != "receive orders -n 5" {
		t.Errorf("got %q, want 'receive orders -n 5'", got)
	}
}

func TestExtractCommandWithVerbs_KnownVerbDirect(t *testing.T) {
	verbs := buildVerbSet(testVerbRoot())
	got := extractCommandWithVerbs("send q hello", verbs)
	if got != "send q hello" {
		t.Errorf("got %q, want 'send q hello'", got)
	}
}

func TestExtractCommandWithVerbs_CommentPassthrough(t *testing.T) {
	verbs := buildVerbSet(testVerbRoot())
	for _, input := range []string{"# ask: what queue?", "# cannot: no such command"} {
		got := extractCommandWithVerbs(input, verbs)
		if got != input {
			t.Errorf("extractCommandWithVerbs(%q) = %q, want unchanged", input, got)
		}
	}
}

func TestExtractCommandWithVerbs_TwoWordProse(t *testing.T) {
	verbs := buildVerbSet(testVerbRoot())
	// "manage list" must appear as the first tokens on its own line for the
	// prose fallback to recognise it.
	input := "Here's what to run:\nmanage list\nThat lists all queues."
	got := extractCommandWithVerbs(input, verbs)
	if got != "manage list" {
		t.Errorf("got %q, want 'manage list'", got)
	}
}

func TestExtractCommandWithVerbs_StrayCommentPassesThroughUnrecognized(t *testing.T) {
	// A "#" line that is NOT "# ask:"/"# cannot:" (e.g. the model inventing its
	// own confirmation question) has no verb anywhere in the response, so it
	// should come back unchanged — the caller (handleAIDone) is responsible
	// for refusing to propose/execute anything starting with an unrecognized "#".
	verbs := buildVerbSet(testVerbRoot())
	input := "# This will permanently delete 5 queues. Proceed? (yes/no)"
	got := extractCommandWithVerbs(input, verbs)
	if got != input {
		t.Errorf("got %q, want unchanged %q", got, input)
	}
}

func TestExtractCommandWithVerbs_StrayCommentThenRealCommand(t *testing.T) {
	// If the model prefixes a real command with a stray explanatory comment,
	// the actual command on a later line must still be found — a non-canonical
	// "#" prefix must not short-circuit the prose scan the way "# ask:"/
	// "# cannot:" intentionally do.
	verbs := buildVerbSet(testVerbRoot())
	input := "# This deletes the queue\nmanage delete-queue orders"
	got := extractCommandWithVerbs(input, verbs)
	if got != "manage delete-queue orders" {
		t.Errorf("got %q, want 'manage delete-queue orders'", got)
	}
}

func TestExtractCommandWithVerbs_NilVerbSet(t *testing.T) {
	// With a nil verb set, falls back to extractCommand behaviour.
	got := extractCommandWithVerbs("receive q -n 5", nil)
	if got != "receive q -n 5" {
		t.Errorf("got %q, want 'receive q -n 5'", got)
	}
}

