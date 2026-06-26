package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func buildCapabilities(rootCmd *cobra.Command) string {
	var b strings.Builder
	skip := map[string]bool{"shell": true, "sh": true, "ai": true, "help": true, "version": true, "completion": true}

	type subInfo struct {
		name, short string
		flags       []flagInfo
	}
	type cmdInfo struct {
		name  string
		short string
		flags []flagInfo
		subs  []subInfo
	}

	var cmds []cmdInfo
	// Use the full formatted line as key so flags with different descriptions
	// or defaults are not merged (e.g. --count on send vs forward).
	flagCount := map[string]int{}

	for _, cmd := range rootCmd.Commands() {
		if skip[cmd.Name()] || cmd.Hidden {
			continue
		}
		ci := cmdInfo{name: cmd.Name(), short: cmd.Short}
		subs := cmd.Commands()
		if len(subs) > 0 {
			for _, sub := range subs {
				if sub.Hidden || sub.Name() == "help" {
					continue
				}
				si := subInfo{name: sub.Name(), short: sub.Short}
				si.flags = collectFlags(sub)
				for _, f := range si.flags {
					flagCount[f.line]++
				}
				ci.subs = append(ci.subs, si)
			}
		} else {
			ci.flags = collectFlags(cmd)
			for _, f := range ci.flags {
				flagCount[f.line]++
			}
		}
		cmds = append(cmds, ci)
	}

	// Flags with identical description+defaults on 3+ commands go into a shared section.
	shared := map[string]bool{}
	for line, count := range flagCount {
		if count >= 3 {
			shared[line] = true
		}
	}

	// Write shared flags section.
	if len(shared) > 0 {
		b.WriteString("## Shared flags (available on multiple commands)\n")
		seen := map[string]bool{}
		for _, ci := range cmds {
			for _, f := range ci.flags {
				if shared[f.line] && !seen[f.line] {
					seen[f.line] = true
					b.WriteString("  " + f.line + "\n")
				}
			}
			for _, si := range ci.subs {
				for _, f := range si.flags {
					if shared[f.line] && !seen[f.line] {
						seen[f.line] = true
						b.WriteString("  " + f.line + "\n")
					}
				}
			}
		}
		b.WriteString("\n")
	}

	// Write per-command sections with only unique flags.
	for _, ci := range cmds {
		fmt.Fprintf(&b, "## %s\n", ci.name)
		if ci.short != "" {
			fmt.Fprintf(&b, "%s\n", ci.short)
		}
		if len(ci.subs) > 0 {
			for _, si := range ci.subs {
				fmt.Fprintf(&b, "  %s %s — %s\n", ci.name, si.name, si.short)
				for _, f := range si.flags {
					if !shared[f.line] {
						b.WriteString("    " + f.line + "\n")
					}
				}
			}
		} else {
			for _, f := range ci.flags {
				if !shared[f.line] {
					b.WriteString("  " + f.line + "\n")
				}
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

type flagInfo struct {
	line string // formatted line without indent (also used as dedup key)
}

func collectFlags(cmd *cobra.Command) []flagInfo {
	var flags []flagInfo
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		short := ""
		if f.Shorthand != "" {
			short = fmt.Sprintf("-%s, ", f.Shorthand)
		}
		line := fmt.Sprintf("%s--%s: %s", short, f.Name, f.Usage)
		if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" && f.DefValue != "0s" && f.DefValue != "[]" {
			line += fmt.Sprintf(" (default: %s)", f.DefValue)
		}
		flags = append(flags, flagInfo{line: line})
	})
	return flags
}

func systemPrompt(caps, brokerContext, server, topology string, aliases map[string]string) string {
	var brokerSection string
	if brokerContext != "" {
		brokerSection = fmt.Sprintf(`
## Broker-specific documentation

The following is the documentation for the broker this binary is built for.
Use it to understand broker-specific concepts (exchanges, routing, topology,
subscriptions, etc.) and to produce correct commands.

%s
`, brokerContext)
	}

	var connectionSection string
	if server != "" {
		connectionSection = fmt.Sprintf("\n## Connection\n\nConnected to: %s\n", server)
	}

	var topologySection string
	if topology != "" {
		topologySection = fmt.Sprintf("\n## Current topology\n\nThese queues and topics exist on the broker right now. Use these real names\ninstead of guessing:\n\n%s\n", capTopologyLines(topology, 100))
	}

	var aliasesSection string
	if len(aliases) > 0 {
		var ab strings.Builder
		ab.WriteString("\n## Saved aliases\n\nThe user has defined these command shortcuts. You may output an alias name\n(with arguments) instead of the full command:\n\n")
		for name, tmpl := range aliases {
			fmt.Fprintf(&ab, "  %s → %s\n", name, tmpl)
		}
		aliasesSection = ab.String()
	}

	return fmt.Sprintf(`You are an AI assistant embedded in xmc, a unified CLI for message brokers. Respond with EXACTLY ONE xmc shell command (or pipeline). No prose, no explanation, no code fences — just the raw command line.

This is a multi-turn conversation. You see previous requests, commands, and execution results. Use context for refinements ("make it 10 instead", "same but for queue B").

## Shell syntax

- Pipelines: verb | verb (NDJSON auto-injected on both sides) or verb | external (raw output)
- Semicolons: cmd1 ; cmd2 (sequential, stop on first error)
- verb | external: add --ndjson explicitly for structured JSON, e.g.: receive q --ndjson | jq '.data'
- external | verb: add --ndjson to the producer for lossless replay
- Example: receive A -n 5 --ndjson | jq -cs 'max_by(.data | length)' | send B --ndjson

## NDJSON record fields

Never use "body", "payload", or "message" — the exact field names are:
"data", "dataBase64", "key", "messageId", "correlationId", "replyTo", "contentType", "priority", "persistent", "properties"

## Strict command syntax rules

- Do NOT prefix with the binary name: write "send q1 hi", NOT "rmc send q1 hi"
- By default the first positional is the destination, the second is the message payload
- With -e <exchange> the single positional is the message body (no routing key); use --routing-key for direct/topic exchanges — see broker docs below for details
- Use ONLY the exact flags listed below — do not invent flags
- Destination names and address formats are broker-specific — see broker docs below
- When broker objects are listed below, use those exact names — do not guess
- Queue commands: send, receive, peek, request, reply, move, forward (point-to-point)
- Topic commands: publish, subscribe (pub/sub fan-out)

## Destructive operations

ONLY these commands are destructive (require explicit user confirmation):
  manage delete-queue, manage delete-topic, manage delete-exchange, manage unbind-queue, manage purge

All other commands — including receive, peek, move, forward, subscribe (even with -n 0 to drain a queue) — are NON-DESTRUCTIVE read or relay operations. Do NOT warn about or ask confirmation for these.

Available commands and flags:

%s
%s%s%s%s
## Output rules

- Output ONLY the command. Nothing else.
- If impossible: # cannot: <brief reason>
- If ambiguous: # ask: <clarifying question>
`, caps, brokerSection, connectionSection, topologySection, aliasesSection)
}

func extractCommand(response string) string {
	s := strings.TrimSpace(response)
	if idx := strings.Index(s, "```"); idx >= 0 {
		lines := strings.Split(s[idx:], "\n")
		var inner []string
		inBlock := false
		for _, l := range lines {
			if strings.HasPrefix(l, "```") {
				inBlock = !inBlock
				continue
			}
			if inBlock {
				inner = append(inner, l)
			}
		}
		if len(inner) > 0 {
			s = strings.Join(inner, "\n")
		}
	}
	s = strings.TrimPrefix(s, "$ ")
	s = strings.TrimPrefix(s, "`")
	s = strings.TrimSuffix(s, "`")
	s = strings.TrimSpace(s)

	// Strip a leading binary-name prefix so that commands like "./rmc send q1 hi"
	// or "rmc send q1 hi" are recognised as in-process verbs.
	s = stripBinaryPrefix(s)

	return s
}

// stripBinaryPrefix removes a leading invocation token that matches the current
// binary name (e.g. "rmc", "./rmc", "xmc", "./xmc") so the command is
// recognised as an xmc verb and runs over the persistent connection.
func stripBinaryPrefix(s string) string {
	first, rest, hasRest := strings.Cut(s, " ")
	if !hasRest {
		return s
	}
	// Normalise: strip leading "./" and trailing ".exe".
	name := first
	name = strings.TrimPrefix(name, "./")
	name = strings.TrimPrefix(name, ".\\")
	name = strings.TrimSuffix(name, ".exe")
	if name == binBaseName() || name == "xmc" {
		return strings.TrimSpace(rest)
	}
	return s
}

// capTopologyLines returns the topology string truncated to at most maxLines
// lines, appending a "(N more)" note if truncated.
func capTopologyLines(topology string, maxLines int) string {
	lines := strings.Split(topology, "\n")
	if len(lines) <= maxLines {
		return topology
	}
	kept := strings.Join(lines[:maxLines], "\n")
	remaining := len(lines) - maxLines
	return fmt.Sprintf("%s\n… (%d more lines)", kept, remaining)
}

// buildVerbSet returns the set of known top-level xmc verbs from the cobra tree.
// Shell/AI/help are excluded since they cannot be run as inline commands.
func buildVerbSet(rootCmd *cobra.Command) map[string]bool {
	skip := map[string]bool{"shell": true, "sh": true, "ai": true, "help": true, "version": true, "completion": true}
	verbs := make(map[string]bool)
	for _, cmd := range rootCmd.Commands() {
		if !skip[cmd.Name()] && !cmd.Hidden {
			verbs[cmd.Name()] = true
			for _, sub := range cmd.Commands() {
				if !sub.Hidden && sub.Name() != "help" {
					// "manage list", "manage purge", etc.
					verbs[cmd.Name()+" "+sub.Name()] = true
				}
			}
		}
	}
	return verbs
}

// extractCommandWithVerbs extends extractCommand with a prose-fallback: if the
// stripped response is not a known xmc verb (and not a # comment), it scans
// each line for the first whose first token is in the verb set.
func extractCommandWithVerbs(response string, verbs map[string]bool) string {
	cmd := extractCommand(response)
	if cmd == "" {
		return cmd
	}
	// Already a comment or a known verb — return as-is.
	if strings.HasPrefix(cmd, "#") {
		return cmd
	}
	first := strings.Fields(cmd)[0]
	if verbs[first] {
		return cmd
	}
	// Prose response: scan lines for a line beginning with a known verb.
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		line = stripBinaryPrefix(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if verbs[fields[0]] {
			return line
		}
		// Also check two-word verbs like "manage list".
		if len(fields) >= 2 && verbs[fields[0]+" "+fields[1]] {
			return line
		}
	}
	// No verb found — return the extracted command unchanged (caller handles it).
	return cmd
}
