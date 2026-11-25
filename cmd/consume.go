package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"time"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
)

// registerConsumeFlags adds the flags shared by receive, peek, and subscribe.
// waitDefault differs (subscribe defaults to true, wait indefinitely; receive
// and peek default to false); countHelp's flag description differs slightly
// per command; omit registers -o/--omit, which only receive and peek have.
func registerConsumeFlags(cmd *cobra.Command, waitDefault bool, countHelp string, omit bool) {
	cmd.Flags().VarP(newDurationValue(100*time.Millisecond, time.Second), "timeout", "t", "Time to wait for a message (e.g. \"100ms\", \"5s\")")
	cmd.Flags().BoolP("quiet", "q", false, "Quiet about properties, show data only")
	cmd.Flags().BoolP("wait", "w", waitDefault, "Wait (endless) for a message to arrive")
	cmd.Flags().IntP("count", "n", 1, countHelp)
	cmd.Flags().BoolP("json", "J", false, "Output messages as JSON")
	cmd.Flags().StringP("format", "F", "", "Output format string, e.g. \"%i %s\\n\" (overrides --json)")
	cmd.Flags().Bool("ndjson", false, "Output one lossless JSON record per line (overrides --format/--json)")
	cmd.Flags().StringP("selector", "S", "", "Filter messages by property expression (e.g. \"color='red'\")")
	cmd.Flags().String("for", "", "Stream for a bounded duration then stop (e.g. \"30s\", \"5m\")")
	cmd.Flags().Bool("forever", false, "Stream until interrupted / until xmc quits (no time bound)")
	cmd.Flags().Bool("stats", false, "Print live throughput statistics to stderr while streaming")
	if omit {
		cmd.Flags().IntP("omit", "o", 0, "Skip (offset past) the first N messages before reading")
	}
}

// consumeFlagValues holds the flag values shared by receive, peek, and
// subscribe (everything except the destination and command-specific extras
// like subscribe's --group/--durable).
type consumeFlagValues struct {
	timeout    float32
	wait       bool
	quiet      bool
	count      int
	jsonOutput bool
	selector   string
	format     string
	ndjson     bool
	omit       int // 0 when the command doesn't register --omit (subscribe)
	streaming  StreamingFlags
}

// parseConsumeFlags reads the flags registerConsumeFlags adds, plus the
// streaming flags (--for/--forever/--stats), applying the shared "count=0
// when time-bounded and --count wasn't set explicitly" rule. hasOmit is false
// for subscribe, which doesn't register --omit.
func parseConsumeFlags(cmd *cobra.Command, hasOmit bool) (consumeFlagValues, error) {
	var v consumeFlagValues
	v.timeout = float32(getDuration(cmd, "timeout").Seconds())
	v.wait, _ = cmd.Flags().GetBool("wait")
	v.quiet, _ = cmd.Flags().GetBool("quiet")
	v.count, _ = cmd.Flags().GetInt("count")
	v.jsonOutput, _ = cmd.Flags().GetBool("json")
	v.selector, _ = cmd.Flags().GetString("selector")
	v.format, _ = cmd.Flags().GetString("format")
	v.ndjson, _ = cmd.Flags().GetBool("ndjson")
	if hasOmit {
		v.omit, _ = cmd.Flags().GetInt("omit")
	}

	sf, err := ParseStreamingFlags(cmd)
	if err != nil {
		return consumeFlagValues{}, err
	}
	v.streaming = sf
	v.count = sf.effectiveCount(cmd, v.count)
	return v, nil
}

type messageReceiver func(context.Context) (*backends.Message, error)

type consumeConfig struct {
	count      int
	jsonOutput bool
	verbosity  backends.Verbosity
	format     string // optional kcat-style output template; overrides jsonOutput
	ndjson     bool   // emit one lossless JSON record per line; overrides format/json
	follow     bool   // streaming: keep polling across empty reads until ctx ends
	omit       int    // skip (offset past) the first N messages before outputting
	stats      *streamStats
	dataOut    io.Writer // message payload output
	metaOut    io.Writer // metadata/properties output
}

func consumeMessages(ctx context.Context, receive messageReceiver, cfg consumeConfig) error {
	// count <= 0 means "drain": keep consuming until the source is exhausted
	// (or, with --wait, until interrupted). This is consistent with the reply
	// and move commands, where 0 also means "no fixed limit".
	//
	// In follow mode (streaming: --for or --stats), empty reads do not end the
	// loop; it keeps polling until the context is cancelled or its deadline
	// passes, which is what makes time-bounded and continuous streaming work.
	unbounded := cfg.count <= 0
	received := 0
	omitted := 0
	for unbounded || received < cfg.count {
		// Always check for cancellation — not just in follow mode. This lets
		// Ctrl-C in the shell (SIGINT → streamContext) and Esc in the AI TUI
		// (execCancel → cobra ctx → streamContext parent) stop any loop,
		// including unbounded non-streaming ones like peek -n 0.
		if ctx.Err() != nil {
			return nil
		}

		message, err := receive(ctx)
		switch {
		case errors.Is(err, context.Canceled):
			return nil
		case errors.Is(err, context.DeadlineExceeded):
			if cfg.follow {
				continue
			}
			return nil
		case errors.Is(err, backends.ErrNoMessageAvailable), message == nil && err == nil:
			if cfg.follow {
				continue
			}
			if received == 0 && !unbounded {
				return backends.ErrNoMessageAvailable
			}
			return nil
		case err != nil:
			return err
		}

		// --omit / -o: skip the first N messages (offset style).
		// For peek this advances the browse cursor non-destructively;
		// for receive the skipped messages are consumed and discarded.
		// Skipped messages are not counted toward --count / -n.
		if omitted < cfg.omit {
			omitted++
			continue
		}

		if err := outputMessage(message, cfg); err != nil {
			return err
		}
		cfg.stats.record(len(message.Data))
		received++
	}

	return nil
}

// runConsume wraps consumeMessages with the streaming context (--for) and the
// optional live throughput reporter (--stats), printing a final summary when the
// stream ends. When neither streaming option is active it behaves exactly like a
// plain consumeMessages call.
//
// parent is typically cmd.Context(), so that external cancellation (e.g. the AI
// TUI's Esc handler) propagates into the receive loop alongside SIGINT.
func runConsume(parent context.Context, receive messageReceiver, cfg consumeConfig, duration time.Duration, stats bool) error {
	// streamContext merges SIGINT with the parent so that both Ctrl-C (shell)
	// and Esc (AI TUI) cleanly stop the consume loop.
	ctx, cancel := streamContext(parent, duration)
	defer cancel()

	var stopStats func()
	cfg.stats, stopStats = startStats(stats, cfg.metaOut)
	defer stopStats()

	return consumeMessages(ctx, receive, cfg)
}

func outputMessage(message *backends.Message, cfg consumeConfig) error {
	w := cfg.dataOut
	switch {
	case cfg.ndjson:
		return displayMessageNDJSON(w, message)
	case cfg.format != "":
		return displayMessageFormat(w, message, cfg.format)
	case cfg.jsonOutput:
		return displayMessageJSON(w, message, cfg.verbosity)
	default:
		return displayMessage(w, cfg.metaOut, message, cfg.verbosity)
	}
}

// commandVerbosity derives Verbosity from the common --quiet flag and
// the global log.IsVerbose toggle.
func commandVerbosity(quiet bool) backends.Verbosity {
	switch {
	case log.IsVerbose:
		return backends.VerbosityVerbose
	case quiet:
		return backends.VerbosityQuiet
	default:
		return backends.VerbosityNormal
	}
}

func displayMessage(dataOut, metaOut io.Writer, message *backends.Message, verbosity backends.Verbosity) error {
	if verbosity >= backends.VerbosityVerbose {
		for _, key := range slices.Sorted(maps.Keys(message.InternalMetadata)) {
			if _, err := fmt.Fprintf(metaOut, "%s: %v\n", key, message.InternalMetadata[key]); err != nil {
				return err
			}
		}
	}

	if verbosity >= backends.VerbosityNormal && len(message.Properties) > 0 {
		if _, err := fmt.Fprintf(metaOut, "Properties: %s\n", formatProperties(message.Properties)); err != nil {
			return err
		}
	}

	_, _ = dataOut.Write(message.Data)
	if shouldAddNewline(dataOut) {
		_, _ = fmt.Fprintln(dataOut)
	}

	return nil
}

// shouldAddNewline reports whether a trailing newline should be appended after
// message data. When writing to the real os.Stdout it honours the original
// log.IsStdout heuristic (true when stdout is a terminal, false when redirected
// to a file). For any other writer (pipe, buffer) a newline is always added so
// that line-oriented tools like grep and jq work correctly in pipelines.
func shouldAddNewline(w io.Writer) bool {
	if w == os.Stdout {
		return log.IsStdout
	}
	return true
}

// displayMessageJSON outputs the message as a JSON object, using the same
// messageRecord schema as --ndjson (see recordForDisplay). Verbose mode
// (-v) also includes internalMetadata, matching what verbose text output
// (displayMessage) shows as sorted key: value lines.
func displayMessageJSON(w io.Writer, message *backends.Message, verbosity backends.Verbosity) error {
	rec := recordForDisplay(message, true, verbosity >= backends.VerbosityVerbose)
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("failed to marshal message to JSON: %w", err)
	}

	data = append(data, '\n')
	_, _ = w.Write(data)
	return nil
}
