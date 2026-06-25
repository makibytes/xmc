package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
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
		RunE: func(cmd *cobra.Command, args []string) error {
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

	cfg, _ := loadConfig()

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
	defer rl.Close()
	session := &shellSession{
		spec:         spec,
		queueFactory: wrapReconnectQueue(spec.Queue, ReconnectOptions{}),
		topicFactory: wrapReconnectTopic(spec.Topic, ReconnectOptions{}),
		aliases:      cfg.Aliases,
	}
	defer session.close()

	fmt.Fprintf(os.Stderr, "xmc shell — type \"help\" for commands, \"exit\" to quit\n")

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

		// Handle "help" and "help <verb>" explicitly so that cobra prints
		// real usage instead of the pipeline executor treating args as flags.
		if line == "help" || strings.HasPrefix(line, "help ") {
			shellHelp(rootCmd, line)
			continue
		}

		if strings.HasPrefix(line, "!") {
			if err := runSystemShell(line[1:]); err != nil {
				log.Error("shell: %s\n", err)
			}
			continue
		}

		if !containsVerb(line) && !isAlias(line, session.aliases) {
			if err := runSystemShell(line); err != nil {
				log.Error("%s\n", err)
			}
			continue
		}

		if err := session.executePipeline(line, rootCmd); err != nil {
			log.Error("%s\n", err)
		}
	}
}

// shellHelp prints cobra usage for the given help command. "help" alone prints
// the root command's available-commands table; "help <verb>" and "help manage
// <subcmd>" print the specific command's help.
func shellHelp(rootCmd *cobra.Command, line string) {
	args := shellSplit(strings.TrimSpace(line))
	// Drop the leading "help" token.
	args = args[1:]

	target := rootCmd
	for _, arg := range args {
		sub, _, err := target.Find([]string{arg})
		if err != nil || sub == target {
			fmt.Fprintf(os.Stderr, "unknown command: %s\n", strings.Join(args, " "))
			return
		}
		target = sub
	}

	target.Help()
}

// containsVerb checks whether any top-level stage in the line starts with an
// xmc verb.
func containsVerb(line string) bool {
	for _, stage := range splitPipeline(line) {
		s := classifyStage(stage)
		if s.isVerb {
			return true
		}
	}
	return false
}

// runSystemShell executes a command line in the user's login shell.
func runSystemShell(cmdLine string) error {
	cmdLine = strings.TrimSpace(cmdLine)
	if cmdLine == "" {
		return nil
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "sh"
	}

	c := exec.Command(shell, "-c", cmdLine)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
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

