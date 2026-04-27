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
}

func consumeMessages(ctx context.Context, receive messageReceiver, cfg consumeConfig) error {
	for received := 0; received < cfg.count; received++ {
		message, err := receive(ctx)
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil
		case errors.Is(err, backends.ErrNoMessageAvailable):
			if received == 0 {
				return backends.ErrNoMessageAvailable
			}
			return nil
		case err != nil:
			return err
		case message == nil:
			if received == 0 {
				return backends.ErrNoMessageAvailable
			}
			return nil
		}

		if err := outputMessage(message, cfg); err != nil {
			return err
		}
	}

	return nil
}

func outputMessage(message *backends.Message, cfg consumeConfig) error {
	if cfg.jsonOutput {
		return displayMessageJSON(message)
	}
	return displayMessage(message, cfg.verbosity)
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
