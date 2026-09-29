package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/chzyer/readline"
	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// NewShellCommand creates the interactive shell command. It receives the full
// BrokerSpec so it can reach both queue and topic adapters, as well as the
// management subcommand.
func NewShellCommand(spec BrokerSpec) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "shell",
		Aliases: []string{"sh"},
		Short:   "Start an interactive shell with a persistent broker connection",
		Long: `Opens an interactive REPL that holds a persistent, auto-reconnecting broker
connection for the entire session. All xmc verbs (send, receive, subscribe,
publish, ...) are available at the prompt.

Pipelines are supported: xmc verbs pipe through each other in-process (using
NDJSON framing for lossless metadata transfer), and external commands (grep, jq,
xxd, ...) run in your real shell.

For AI-assisted command generation, use the separate "ai" command.

Examples:
  subscribe events | send archive-queue
  receive dlq | grep -i error | jq .
  !ls -la              # escape to a full shell command

Type "exit", "quit", or press Ctrl-D to leave the shell.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runShell(cmd, spec)
		},
	}
	return cmd
}

func runShell(cmd *cobra.Command, spec BrokerSpec) error {
	if err := ensureXMCDir(); err != nil {
		log.Error("warning: %s\n", err)
	}

	normalPrompt := binBaseName() + "> "

	shHistPath, _ := shellHistoryPath()

	rootCmd := cmd.Root()

	cfg, cfgErr := loadConfig()
	if cfgErr != nil {
		// A malformed config must not take the shell down (cfg is nil on a
		// parse error); warn and continue with defaults, like the AI shell.
		log.Error("warning: %s\n", cfgErr)
		cfg = &xmcConfig{}
	}

	completer := newShellCompleter(rootCmd, cfg.Aliases)

	rl, err := readline.NewEx(&readline.Config{
		Prompt:          normalPrompt,
		HistoryFile:     shHistPath,
		InterruptPrompt: "^C",
		EOFPrompt:       "",
		AutoComplete:    completer,
	})
	if err != nil {
		return fmt.Errorf("initialize readline: %w", err)
	}
	defer rl.Close() //nolint:errcheck
	session := &shellSession{
		spec:         spec,
		queueFactory: wrapReconnectQueue(spec.Queue, ReconnectOptions{}),
		topicFactory: wrapReconnectTopic(spec.Topic, ReconnectOptions{}),
		aliases:      cfg.Aliases,
	}
	defer session.close()

	fmt.Fprintf(os.Stderr, "%s shell — type \"help\" for commands, \"exit\" to quit\n", binBaseName())

	for {
		line, err := rl.Readline()

		if err != nil {
			if err == readline.ErrInterrupt {
				continue
			}
			if err == io.EOF {
				fmt.Fprintln(os.Stderr, "exit")
				return nil
			}
			return err
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		switch line {
		case "exit", "quit":
			return nil
		}

		// Everything else — xmc verbs, pipelines, aliases, "help [verb]",
		// "!cmd" and plain system commands — goes through the same executor
		// the AI shell's command mode uses, so the two behave identically.
		if err := session.executePipeline(line, rootCmd); err != nil {
			log.Error("%s\n", err)
		}
	}
}

// containsVerb checks whether any top-level stage of any ';'-separated
// command in the line starts with an xmc verb (or verb alias).
func containsVerb(line string) bool {
	for _, command := range splitCommands(line) {
		for _, stage := range splitPipeline(command) {
			if classifyStage(stage).isVerb {
				return true
			}
		}
	}
	return false
}

// newShellCompleter builds a readline completer by walking the cobra command
// tree. Top-level verbs get nested completions for their subcommands and flags,
// so "manage <Tab>" shows "list", "purge", "create-queue", etc. and
// "receive <Tab>" shows "-n", "--ndjson", etc.
func newShellCompleter(rootCmd *cobra.Command, aliases map[string]string) *readline.PrefixCompleter {
	skip := map[string]bool{"shell": true, "sh": true, "ai": true, "version": true, "completion": true}

	var items []readline.PrefixCompleterInterface

	for _, cmd := range rootCmd.Commands() {
		name := cmd.Name()
		if cmd.Hidden || skip[name] {
			continue
		}
		items = append(items, buildCmdCompleter(cmd))
		for _, alias := range cmd.Aliases {
			items = append(items, buildCmdCompleter(cmd, alias))
		}
	}
	for name := range aliases {
		items = append(items, readline.PcItem(name))
	}
	items = append(items, readline.PcItem("exit"))
	items = append(items, readline.PcItem("quit"))

	return readline.NewPrefixCompleter(items...)
}

// buildCmdCompleter returns a PcItem for a cobra command, including its
// subcommands and flags as nested completions. When nameOverride is given it
// uses that instead of cmd.Name() (used for aliases).
func buildCmdCompleter(cmd *cobra.Command, nameOverride ...string) readline.PrefixCompleterInterface {
	name := cmd.Name()
	if len(nameOverride) > 0 {
		name = nameOverride[0]
	}

	var children []readline.PrefixCompleterInterface

	// Subcommands (e.g. manage list, manage purge, ...).
	for _, sub := range cmd.Commands() {
		if sub.Hidden || sub.Name() == "help" {
			continue
		}
		children = append(children, buildCmdCompleter(sub))
	}

	// Flags.
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		children = append(children, readline.PcItem("--"+f.Name))
		if f.Shorthand != "" {
			children = append(children, readline.PcItem("-"+f.Shorthand))
		}
	})

	return readline.PcItem(name, children...)
}
