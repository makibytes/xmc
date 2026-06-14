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
	stats      *streamStats
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
	for unbounded || received < cfg.count {
		if cfg.follow && ctx.Err() != nil {
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
func runConsume(receive messageReceiver, cfg consumeConfig, duration time.Duration, stats bool) error {
	ctx := context.Background()
	cancel := func() {}
	if cfg.follow {
		ctx, cancel = streamContext(duration)
	}
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
	switch {
	case cfg.ndjson:
		return displayMessageNDJSON(message)
	case cfg.format != "":
		return displayMessageFormat(message, cfg.format)
	case cfg.jsonOutput:
		return displayMessageJSON(message)
	default:
		return displayMessage(message, cfg.verbosity)
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

func displayMessage(message *backends.Message, verbosity backends.Verbosity) error {
	if verbosity >= backends.VerbosityVerbose {
		if err := writeKeyValueMap(os.Stderr, message.InternalMetadata, "", ": %v\n"); err != nil {
			return err
		}
	}

	if verbosity >= backends.VerbosityNormal {
		if err := writeProperties(os.Stderr, message.Properties); err != nil {
			return err
		}
	}

	fmt.Print(string(message.Data))
	if log.IsStdout {
		fmt.Println()
	}

	return nil
}

// displayMessageJSON outputs the message as a JSON object
func displayMessageJSON(message *backends.Message) error {
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

	fmt.Println(string(data))
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
