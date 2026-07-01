package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

// xmcVerbs is the set of first tokens that identify an xmc verb (as opposed to
// an external command). The map is built once during init.
var xmcVerbs = map[string]bool{
	"send": true, "receive": true, "get": true, "peek": true,
	"request": true, "reply": true, "respond": true,
	"move": true, "forward": true, "bridge": true,
	"publish": true, "subscribe": true,
	"manage": true, "ping": true,
	"help": true,
}

// pipelineStage is a single stage (one element between | delimiters) in a user
// pipeline. It is either an xmc verb or an external command.
type pipelineStage struct {
	isVerb bool     // true → xmc verb; false → external command
	verb   string   // the verb name (only when isVerb)
	args   []string // parsed arguments (only when isVerb)
	raw    string   // original text of this stage (always set; external stages use this)
}

// splitCommands splits a raw line on top-level ';' characters, respecting
// single and double quotes. Each segment is trimmed; empty segments are dropped.
func splitCommands(line string) []string {
	raw := splitOnDelim(line, ';')
	out := raw[:0]
	for _, s := range raw {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// splitPipeline splits a raw REPL line into pipeline stages on top-level '|'
// characters, respecting single and double quotes.
func splitPipeline(line string) []string {
	return splitOnDelim(line, '|')
}

// splitOnDelim splits a string on a top-level delimiter rune, respecting single
// and double quotes and backslash escapes.
func splitOnDelim(line string, delim rune) []string {
	var stages []string
	var current strings.Builder
	inSingle, inDouble, escaped := false, false, false

	for _, r := range line {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		switch {
		case r == '\\':
			escaped = true
			current.WriteRune(r)
		case r == '\'' && !inDouble:
			inSingle = !inSingle
			current.WriteRune(r)
		case r == '"' && !inSingle:
			inDouble = !inDouble
			current.WriteRune(r)
		case r == delim && !inSingle && !inDouble:
			stages = append(stages, strings.TrimSpace(current.String()))
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	if s := strings.TrimSpace(current.String()); s != "" {
		stages = append(stages, s)
	}
	return stages
}

// classifyStage determines whether a stage text is an xmc verb or an external
// command. It tokenizes just enough to extract the first word.
func classifyStage(text string) pipelineStage {
	trimmed := strings.TrimLeftFunc(text, unicode.IsSpace)
	first := trimmed
	if idx := strings.IndexFunc(trimmed, unicode.IsSpace); idx > 0 {
		first = trimmed[:idx]
	}

	if xmcVerbs[first] {
		args := shellSplit(trimmed)
		return pipelineStage{isVerb: true, verb: first, args: args, raw: text}
	}
	return pipelineStage{isVerb: false, raw: text}
}

// shellSplit is a simple quote-aware word splitter. It handles single and
// double quotes but not backslash escapes inside quotes (those are passed
// through verbatim, which matches cobra's flag parsing).
func shellSplit(s string) []string {
	var words []string
	var word strings.Builder
	inSingle, inDouble := false, false

	for _, r := range s {
		switch {
		case r == '\'' && !inDouble:
			inSingle = !inSingle
		case r == '"' && !inSingle:
			inDouble = !inDouble
		case unicode.IsSpace(r) && !inSingle && !inDouble:
			if word.Len() > 0 {
				words = append(words, word.String())
				word.Reset()
			}
		default:
			word.WriteRune(r)
		}
	}
	if word.Len() > 0 {
		words = append(words, word.String())
	}
	return words
}

// pipelineBlock is a coalesced run of either verb stages or external stages.
// Adjacent external stages are merged into a single sh -c invocation so that
// shell syntax (redirects, &&, globs) inside the external part works naturally.
type pipelineBlock struct {
	isVerb bool
	stages []pipelineStage // for verb blocks: one stage per block; for external: coalesced raw texts
}

// coalesceStages groups adjacent external stages into single blocks while
// keeping each verb stage as its own block.
func coalesceStages(stages []pipelineStage) []pipelineBlock {
	var blocks []pipelineBlock
	for _, s := range stages {
		if s.isVerb {
			blocks = append(blocks, pipelineBlock{isVerb: true, stages: []pipelineStage{s}})
		} else {
			if len(blocks) > 0 && !blocks[len(blocks)-1].isVerb {
				blocks[len(blocks)-1].stages = append(blocks[len(blocks)-1].stages, s)
			} else {
				blocks = append(blocks, pipelineBlock{isVerb: false, stages: []pipelineStage{s}})
			}
		}
	}
	return blocks
}

// shellSession holds the long-lived adapters and spec for executing pipeline
// verbs in-process.
type shellSession struct {
	spec         BrokerSpec
	mu           sync.Mutex
	queueAdapter backends.QueueBackend
	topicAdapter backends.TopicBackend
	queueFactory QueueAdapterFactory
	topicFactory TopicAdapterFactory
	aliases      map[string]string
}

func (s *shellSession) getQueueAdapter() (backends.QueueBackend, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.queueAdapter != nil {
		return s.queueAdapter, nil
	}
	if s.queueFactory == nil {
		return nil, fmt.Errorf("this broker does not support queue operations")
	}
	a, err := s.queueFactory()
	if err != nil {
		return nil, err
	}
	s.queueAdapter = a
	return a, nil
}

func (s *shellSession) getTopicAdapter() (backends.TopicBackend, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.topicAdapter != nil {
		return s.topicAdapter, nil
	}
	if s.topicFactory == nil {
		return nil, fmt.Errorf("this broker does not support topic operations")
	}
	a, err := s.topicFactory()
	if err != nil {
		return nil, err
	}
	s.topicAdapter = a
	return a, nil
}

func (s *shellSession) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.queueAdapter != nil {
		s.queueAdapter.Close()
		s.queueAdapter = nil
	}
	if s.topicAdapter != nil {
		s.topicAdapter.Close()
		s.topicAdapter = nil
	}
}

// executePipeline parses, classifies, coalesces, and runs a pipeline line
// using os.Stdin/os.Stdout/os.Stderr.
func (s *shellSession) executePipeline(line string, rootCmd *cobra.Command) error {
	return s.executePipelineIO(context.Background(), line, rootCmd, os.Stdin, os.Stdout, os.Stderr)
}

// executePipelineIO is like executePipeline but with explicit IO streams.
// It supports semicolon-separated commands: each segment runs sequentially,
// stopping on the first error. Within a segment, pipe stages run concurrently.
func (s *shellSession) executePipelineIO(ctx context.Context, line string, rootCmd *cobra.Command, in io.Reader, out io.Writer, errw io.Writer) error {
	line = expandAlias(line, s.aliases)
	commands := splitCommands(line)
	if len(commands) == 0 {
		return nil
	}
	for _, cmd := range commands {
		if err := s.runOnePipeline(ctx, cmd, rootCmd, in, out, errw); err != nil {
			return err
		}
	}
	return nil
}

// runOnePipeline executes a single pipeline (no semicolons) with the given IO.
func (s *shellSession) runOnePipeline(ctx context.Context, line string, rootCmd *cobra.Command, in io.Reader, out io.Writer, errw io.Writer) error {
	rawStages := splitPipeline(line)
	if len(rawStages) == 0 {
		return nil
	}

	stages := make([]pipelineStage, len(rawStages))
	for i, raw := range rawStages {
		stages[i] = classifyStage(raw)
	}

	blocks := coalesceStages(stages)

	// Single stage, no piping needed.
	if len(blocks) == 1 {
		return s.executeBlock(ctx, blocks[0], in, out, errw, rootCmd, false, false)
	}

	// Multi-block pipeline: wire up os.Pipe between blocks.
	g, ctx := errgroup.WithContext(ctx)
	var prevReader io.Reader = in

	for i, block := range blocks {
		isLast := i == len(blocks)-1
		isFirst := i == 0

		var nextWriter io.WriteCloser
		var reader io.Reader

		if isLast {
			reader = prevReader
			// Last block writes to out.
			g.Go(func() error {
				return s.executeBlock(ctx, block, reader, out, errw, rootCmd,
					!isFirst && block.isVerb && prevBlockIsVerb(blocks, i),
					false)
			})
		} else {
			pr, pw, err := os.Pipe()
			if err != nil {
				return fmt.Errorf("create pipe: %w", err)
			}
			reader = prevReader
			nextWriter = pw

			nextIsVerb := blocks[i+1].isVerb
			currentBlock := block
			currentReader := reader
			currentWriter := nextWriter

			g.Go(func() error {
				defer currentWriter.Close()
				return s.executeBlock(ctx, currentBlock, currentReader, currentWriter, errw, rootCmd,
					false,
					currentBlock.isVerb && nextIsVerb)
			})

			prevReader = pr
		}
	}

	return g.Wait()
}

// prevBlockIsVerb checks whether the block before index i is a verb block.
func prevBlockIsVerb(blocks []pipelineBlock, i int) bool {
	return i > 0 && blocks[i-1].isVerb
}

// executeBlock runs a single pipeline block (verb or external).
// verbInput means the input comes from another verb (expect NDJSON).
// verbOutput means the output goes to another verb (emit NDJSON).
func (s *shellSession) executeBlock(ctx context.Context, block pipelineBlock, in io.Reader, out io.Writer, errw io.Writer, rootCmd *cobra.Command, verbInput, verbOutput bool) error {
	if block.isVerb {
		stage := block.stages[0]
		return s.executeVerb(ctx, stage, in, out, errw, rootCmd, verbInput, verbOutput)
	}
	return s.executeExternal(block, in, out, errw)
}

// executeVerb builds a fresh cobra command for the verb and runs it with the
// given IO, against the session's persistent adapters.
func (s *shellSession) executeVerb(ctx context.Context, stage pipelineStage, in io.Reader, out io.Writer, errw io.Writer, rootCmd *cobra.Command, verbInput, verbOutput bool) error {
	args := stage.args

	// When receiving from a verb upstream, default to NDJSON input mode.
	if verbInput && isProducer(stage.verb) {
		args = ensureFlag(args, "--ndjson")
	}
	// When sending to a verb downstream, default to NDJSON output mode.
	if verbOutput && isConsumer(stage.verb) {
		args = ensureFlag(args, "--ndjson")
	}

	cmd, err := s.buildVerbCommand(stage.verb, rootCmd)
	if err != nil {
		return err
	}

	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errw)
	cmd.SetArgs(args[1:]) // skip the verb itself
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	return cmd.ExecuteContext(ctx)
}

// buildVerbCommand creates a cobra command for the given verb, wired to the
// session's persistent adapters.
func (s *shellSession) buildVerbCommand(verb string, rootCmd *cobra.Command) (*cobra.Command, error) {
	resolver := s.spec.ResolveTarget
	exchRouting := s.spec.ExchangeRouting
	produceFlags := s.spec.ProduceFlags
	consumeFlags := s.spec.ConsumeFlags
	produceExtra := s.spec.ProduceExtra
	consumeExtra := s.spec.ConsumeExtra

	applyProduce := func(c *cobra.Command) *cobra.Command {
		if produceFlags != nil {
			produceFlags(c)
		}
		return c
	}
	applyConsume := func(c *cobra.Command) *cobra.Command {
		if consumeFlags != nil {
			consumeFlags(c)
		}
		return c
	}

	queueVerbBuilders := map[string]func(backends.QueueBackend) *cobra.Command{
		"send": func(b backends.QueueBackend) *cobra.Command {
			return applyProduce(NewSendCommand(b, resolver, produceExtra, exchRouting))
		},
		"receive": func(b backends.QueueBackend) *cobra.Command {
			return applyConsume(NewReceiveCommand(b, resolver, consumeExtra, exchRouting))
		},
		"get": func(b backends.QueueBackend) *cobra.Command {
			return applyConsume(NewReceiveCommand(b, resolver, consumeExtra, exchRouting))
		},
		"peek": func(b backends.QueueBackend) *cobra.Command {
			return applyConsume(NewPeekCommand(b, resolver, consumeExtra, exchRouting))
		},
		"request": NewRequestCommand,
		"reply":   NewReplyCommand, "respond": NewReplyCommand,
		"move": NewMoveCommand,
	}

	// Topic verbs — publish and subscribe get the target resolver.
	topicVerbBuilders := map[string]func(backends.TopicBackend) *cobra.Command{
		"publish": func(b backends.TopicBackend) *cobra.Command {
			return applyProduce(NewPublishCommand(b, resolver, produceExtra, exchRouting))
		},
		"subscribe": func(b backends.TopicBackend) *cobra.Command {
			return applyConsume(NewSubscribeCommand(b, resolver, consumeExtra, exchRouting))
		},
	}

	// forward/bridge may need a queue adapter, a topic adapter, or both,
	// depending on flags that are only parsed once the command executes — so,
	// unlike the verbs above, the adapter(s) are fetched lazily inside RunE
	// (from the session's cache, reusing the persistent connection like every
	// other verb here) rather than eagerly before the topology is known.
	if verb == "forward" || verb == "bridge" {
		queueCapable, topicCapable := s.queueFactory != nil, s.topicFactory != nil
		if verb == "forward" {
			cmd := NewForwardCommand(nil, nil, queueCapable, topicCapable)
			cmd.RunE = func(c *cobra.Command, args []string) error {
				fromTopic, toTopic := resolveForwardTopology(c, queueCapable, topicCapable)
				var qb backends.QueueBackend
				var tb backends.TopicBackend
				var err error
				if !fromTopic || !toTopic {
					if qb, err = s.getQueueAdapter(); err != nil {
						return err
					}
				}
				if fromTopic || toTopic {
					if tb, err = s.getTopicAdapter(); err != nil {
						return err
					}
				}
				return doForward(c, args, qb, tb)
			}
			return cmd, nil
		}
		cmd := NewBridgeCommand(nil, nil, queueCapable, topicCapable)
		cmd.RunE = func(c *cobra.Command, args []string) error {
			if resolveBridgeTopology(c, queueCapable, topicCapable) {
				tb, err := s.getTopicAdapter()
				if err != nil {
					return err
				}
				return doBridge(c, args, nil, tb)
			}
			qb, err := s.getQueueAdapter()
			if err != nil {
				return err
			}
			return doBridge(c, args, qb, nil)
		}
		return cmd, nil
	}

	if newCmd, ok := queueVerbBuilders[verb]; ok {
		adapter, err := s.getQueueAdapter()
		if err != nil {
			return nil, err
		}
		return newCmd(adapter), nil
	}

	if newCmd, ok := topicVerbBuilders[verb]; ok {
		adapter, err := s.getTopicAdapter()
		if err != nil {
			return nil, err
		}
		return newCmd(adapter), nil
	}

	// Management — build a fresh command per invocation so IO routing and arg
	// state are clean (the other verbs already get fresh commands above).
	if verb == "manage" {
		if s.spec.ManageSpec != nil {
			return NewManageCommand(*s.spec.ManageSpec), nil
		}
		if s.spec.Manage != nil {
			return s.spec.Manage, nil
		}
	}

	// Help: print help for the root command.
	if verb == "help" {
		return rootCmd, nil
	}

	return nil, fmt.Errorf("unknown verb: %s", verb)
}

// executeExternal runs a coalesced external block by joining the raw stage
// texts with ' | ' and handing them to sh -c. This preserves the user's shell
// syntax (quoting, redirects, globs, env) without us re-implementing it.
func (s *shellSession) executeExternal(block pipelineBlock, in io.Reader, out io.Writer, errw io.Writer) error {
	parts := make([]string, len(block.stages))
	for i, st := range block.stages {
		parts[i] = st.raw
	}
	cmdLine := strings.Join(parts, " | ")

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "sh"
	}

	c := exec.Command(shell, "-c", cmdLine)
	c.Stdin = in
	c.Stdout = out
	c.Stderr = errw
	return c.Run()
}

// isProducer returns true if the verb reads input and sends it to the broker.
func isProducer(verb string) bool {
	switch verb {
	case "send", "publish":
		return true
	}
	return false
}

// isConsumer returns true if the verb reads from the broker and writes output.
func isConsumer(verb string) bool {
	switch verb {
	case "receive", "get", "peek", "subscribe", "request":
		return true
	}
	return false
}

// ensureFlag appends a flag to args if it is not already present.
func ensureFlag(args []string, flag string) []string {
	for _, a := range args {
		if a == flag {
			return args
		}
	}
	return append(args, flag)
}

// expandAlias checks if the first word of line matches an alias name. If so,
// positional arguments ($1, $2, …) and $@ (all remaining) are substituted
// into the alias template. Non-alias lines pass through unchanged.
func expandAlias(line string, aliases map[string]string) string {
	if len(aliases) == 0 {
		return line
	}
	tokens := shellSplit(line)
	if len(tokens) == 0 {
		return line
	}
	tmpl, ok := aliases[tokens[0]]
	if !ok {
		return line
	}

	args := tokens[1:]
	var result strings.Builder
	i := 0
	for i < len(tmpl) {
		if tmpl[i] == '$' && i+1 < len(tmpl) {
			if tmpl[i+1] == '@' || tmpl[i+1] == '*' {
				result.WriteString(strings.Join(args, " "))
				i += 2
				continue
			}
			j := i + 1
			for j < len(tmpl) && tmpl[j] >= '0' && tmpl[j] <= '9' {
				j++
			}
			if j > i+1 {
				n, _ := strconv.Atoi(tmpl[i+1 : j])
				if n >= 1 && n <= len(args) {
					result.WriteString(args[n-1])
				}
				i = j
				continue
			}
		}
		result.WriteByte(tmpl[i])
		i++
	}

	return result.String()
}

// isAlias checks whether the first word of a line matches an alias name.
func isAlias(line string, aliases map[string]string) bool {
	if len(aliases) == 0 {
		return false
	}
	tokens := shellSplit(line)
	if len(tokens) == 0 {
		return false
	}
	_, ok := aliases[tokens[0]]
	return ok
}
