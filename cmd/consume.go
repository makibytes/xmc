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
	"strings"
	"time"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/log"
)

// dataWriter returns the configured data output writer, defaulting to os.Stdout.
func (c consumeConfig) dataWriter() io.Writer {
	if c.dataOut != nil {
		return c.dataOut
	}
	return os.Stdout
}

// metaWriter returns the configured metadata output writer, defaulting to os.Stderr.
func (c consumeConfig) metaWriter() io.Writer {
	if c.metaOut != nil {
		return c.metaOut
	}
	return os.Stderr
}

type flagValueGetter interface {
	GetString(name string) (string, error)
	GetStringSlice(name string) ([]string, error)
	GetBool(name string) (bool, error)
	GetInt(name string) (int, error)
	GetInt64(name string) (int64, error)
	GetFloat32(name string) (float32, error)
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
	dataOut    io.Writer // message payload output; nil defaults to os.Stdout
	metaOut    io.Writer // metadata/properties output; nil defaults to os.Stderr
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
		if cfg.stats != nil {
			cfg.stats.record(len(message.Data))
		}
		received++
	}

	return nil
}

// runConsume wraps consumeMessages with the streaming context (--for) and the
// optional live throughput reporter (--stats), printing a final summary when the
// stream ends. When neither streaming option is active it behaves exactly like a
// plain consumeMessages call on a background context.
//
// An optional parent context may be supplied so that external cancellation
// (e.g. the AI TUI's Esc handler or cobra's ExecuteContext ctx) propagates
// into the receive loop alongside SIGINT.
func runConsume(receive messageReceiver, cfg consumeConfig, duration time.Duration, stats bool, parents ...context.Context) error {
	var parent context.Context
	if len(parents) > 0 && parents[0] != nil {
		parent = parents[0]
	}
	// streamContext merges SIGINT with the optional parent so that both
	// Ctrl-C (shell) and Esc (AI TUI) cleanly stop the consume loop.
	ctx, cancel := streamContext(duration, parent)
	defer cancel()

	if stats {
		st := newStreamStats()
		cfg.stats = st
		stop := startStatsReporter(st, time.Second)
		defer func() {
			stop()
			fmt.Fprintln(os.Stderr, st.summary())
		}()
	}

	return consumeMessages(ctx, receive, cfg)
}

func outputMessage(message *backends.Message, cfg consumeConfig) error {
	w := cfg.dataWriter()
	switch {
	case cfg.ndjson:
		return displayMessageNDJSON(w, message)
	case cfg.format != "":
		return displayMessageFormat(w, message, cfg.format)
	case cfg.jsonOutput:
		return displayMessageJSON(w, message)
	default:
		return displayMessage(w, cfg.metaWriter(), message, cfg.verbosity)
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
		if err := writeKeyValueMap(metaOut, message.InternalMetadata, "", ": %v\n"); err != nil {
			return err
		}
	}

	if verbosity >= backends.VerbosityNormal {
		if err := writeProperties(metaOut, message.Properties); err != nil {
			return err
		}
	}

	fmt.Fprint(dataOut, string(message.Data))
	if shouldAddNewline(dataOut) {
		fmt.Fprintln(dataOut)
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

// displayMessageJSON outputs the message as a JSON object
func displayMessageJSON(w io.Writer, message *backends.Message) error {
	output := map[string]any{
		"data": string(message.Data),
	}
	if message.MessageID != "" {
		output["messageId"] = message.MessageID
	}
	if message.CorrelationID != "" {
		output["correlationId"] = message.CorrelationID
	}
	if message.ReplyTo != "" {
		output["replyTo"] = message.ReplyTo
	}
	if message.ContentType != "" {
		output["contentType"] = message.ContentType
	}
	if message.Priority != 0 {
		output["priority"] = message.Priority
	}
	if message.Persistent {
		output["persistent"] = message.Persistent
	}
	if len(message.Properties) > 0 {
		output["properties"] = message.Properties
	}
	if len(message.InternalMetadata) > 0 {
		output["metadata"] = message.InternalMetadata
	}

	data, err := json.Marshal(output)
	if err != nil {
		return fmt.Errorf("failed to marshal message to JSON: %w", err)
	}

	fmt.Fprintln(w, string(data))
	return nil
}

func writeProperties(w ioWriter, properties map[string]any) error {
	if len(properties) == 0 {
		return nil
	}

	names := slices.Sorted(maps.Keys(properties))
	values := make([]string, 0, len(names))
	for _, name := range names {
		values = append(values, fmt.Sprintf("%s=%v", name, properties[name]))
	}

	_, err := fmt.Fprintf(w, "Properties: %s\n", strings.Join(values, ","))
	return err
}

func writeKeyValueMap(w ioWriter, values map[string]any, prefix, format string) error {
	if len(values) == 0 {
		return nil
	}

	for _, key := range slices.Sorted(maps.Keys(values)) {
		if _, err := fmt.Fprintf(w, prefix+format, key, values[key]); err != nil {
			return err
		}
	}

	return nil
}

type ioWriter = io.Writer
