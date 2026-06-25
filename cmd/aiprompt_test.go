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
