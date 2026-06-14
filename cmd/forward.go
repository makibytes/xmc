package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
)

// NewForwardCommand creates the queue variant of forward: a continuous streaming
// relay that consumes messages from a source queue and sends them to a
// destination queue on the same broker, running until interrupted (or until a
// --for window or --count limit is reached).
//
// Unlike move, which drains whatever is currently present and stops, forward
// keeps streaming as new messages arrive — useful for live bridging, mirroring
// traffic for debugging, or continuously redriving a dead-letter queue. An
// optional --command pipes each message through a shell command, turning
// forward into a streaming transformer.
//
// Message metadata is preserved; the destination assigns a fresh message ID. The
// relay is destructive on the source (like move): if a downstream command or
// send fails, the consumed message is written to stdout so it can be recovered.
func NewForwardCommand(backend backends.QueueBackend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "forward <source> <destination>",
		Short: "Continuously relay messages from one queue to another (streaming)",
		Long: `Streams messages from a source queue to a destination queue on the same broker,
running until interrupted.

It differs from move (which drains what is present and stops) by continuing to
relay messages as they arrive. Use --for to relay for a bounded time, --count to
cap the number of messages, and --command to pipe each message through a shell
command (its stdout becomes the forwarded payload). --stats prints live
throughput to stderr.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doForwardQueue(cmd, args, backend)
		},
	}

	addForwardFlags(cmd)
	return cmd
}

// NewForwardTopicCommand creates the topic variant of forward (subscribe on the
// source topic, publish to the destination topic). It is registered for
// topic-only brokers such as Kafka so that every broker offers forward.
func NewForwardTopicCommand(backend backends.TopicBackend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "forward <source-topic> <destination-topic>",
		Short: "Continuously relay messages from one topic to another (streaming)",
		Long: `Streams messages from a source topic to a destination topic on the same broker,
running until interrupted. Use --for, --count, --command and --stats as with
the queue variant.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doForwardTopic(cmd, args, backend)
		},
	}

	cmd.Flags().StringP("group", "g", "xmc-consumer-group", "Consumer group ID for the source subscription")
	addForwardFlags(cmd)
	return cmd
}

func addForwardFlags(cmd *cobra.Command) {
	cmd.Flags().StringP("command", "x", "", "Pipe each message through a shell command; its stdout is forwarded")
	cmd.Flags().VarP(newDurationValue(100*time.Millisecond, time.Second), "timeout", "t", "Time to wait for the next source message per poll (e.g. \"100ms\")")
	cmd.Flags().IntP("count", "n", 0, "Maximum messages to forward (0 = until interrupted)")
	cmd.Flags().String("for", "", "Relay for a bounded duration then stop (e.g. \"30s\", \"5m\")")
	cmd.Flags().Bool("stats", false, "Print live throughput statistics to stderr")
	cmd.Flags().StringP("selector", "S", "", "Only forward messages matching this selector expression")
	cmd.Flags().BoolP("quiet", "q", false, "Suppress the per-message log; print only the final summary")
}

func doForwardQueue(cmd *cobra.Command, args []string, backend backends.QueueBackend) error {
	source, destination := args[0], args[1]
	if source == destination {
		return fmt.Errorf("source and destination must differ")
	}

	command, _ := cmd.Flags().GetString("command")
	timeout := float32(getDuration(cmd, "timeout").Seconds())
	count, _ := cmd.Flags().GetInt("count")
	selector, _ := cmd.Flags().GetString("selector")
	quiet, _ := cmd.Flags().GetBool("quiet")
	forStr, _ := cmd.Flags().GetString("for")
	statsOn, _ := cmd.Flags().GetBool("stats")

	duration, err := parseDurationFlag(forStr)
	if err != nil {
		return err
	}

	ctx, cancel := streamContext(duration)
	defer cancel()
	st, stopStats := startForwardStats(statsOn)
	defer stopStats()

	forwarded := 0
	for count <= 0 || forwarded < count {
		if ctx.Err() != nil {
			break
		}

		message, err := backend.Receive(ctx, backends.ReceiveOptions{
			Queue:       source,
			Timeout:     timeout,
			Wait:        false,
			Acknowledge: true,
			Verbosity:   backends.VerbosityNormal,
			Selector:    selector,
		})
		switch {
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return summarizeForward(forwarded, source, destination)
		case errors.Is(err, backends.ErrNoMessageAvailable), message == nil && err == nil:
			continue
		case err != nil:
			return err
		}

		body, ok := runCommandOrRecover(command, message.Data)
		if !ok {
			continue
		}

		if err := backend.Send(ctx, backends.SendOptions{
			Queue:         destination,
			Message:       body,
			Properties:    message.Properties,
			CorrelationID: message.CorrelationID,
			ReplyTo:       message.ReplyTo,
			ContentType:   message.ContentType,
			Priority:      message.Priority,
			Persistent:    message.Persistent,
		}); err != nil {
			emitUndelivered(message.Data)
			return fmt.Errorf("forward to %s failed: %w", destination, err)
		}

		forwarded++
		st.record(len(body))
		if !quiet {
			log.Verbose("forwarded message %d to %s", forwarded, destination)
		}
	}

	return summarizeForward(forwarded, source, destination)
}

func doForwardTopic(cmd *cobra.Command, args []string, backend backends.TopicBackend) error {
	source, destination := args[0], args[1]
	if source == destination {
		return fmt.Errorf("source and destination must differ")
	}

	groupID, _ := cmd.Flags().GetString("group")
	command, _ := cmd.Flags().GetString("command")
	timeout := float32(getDuration(cmd, "timeout").Seconds())
	count, _ := cmd.Flags().GetInt("count")
	selector, _ := cmd.Flags().GetString("selector")
	quiet, _ := cmd.Flags().GetBool("quiet")
	forStr, _ := cmd.Flags().GetString("for")
	statsOn, _ := cmd.Flags().GetBool("stats")

	duration, err := parseDurationFlag(forStr)
	if err != nil {
		return err
	}

	ctx, cancel := streamContext(duration)
	defer cancel()
	st, stopStats := startForwardStats(statsOn)
	defer stopStats()

	forwarded := 0
	for count <= 0 || forwarded < count {
		if ctx.Err() != nil {
			break
		}

		message, err := backend.Subscribe(ctx, backends.SubscribeOptions{
			Topic:     source,
			GroupID:   groupID,
			Timeout:   timeout,
			Wait:      true,
			Verbosity: backends.VerbosityNormal,
			Selector:  selector,
		})
		switch {
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return summarizeForward(forwarded, source, destination)
		case errors.Is(err, backends.ErrNoMessageAvailable), message == nil && err == nil:
			continue
		case err != nil:
			return err
		}

		body, ok := runCommandOrRecover(command, message.Data)
		if !ok {
			continue
		}

		if err := backend.Publish(ctx, backends.PublishOptions{
			Topic:         destination,
			Message:       body,
			Properties:    message.Properties,
			CorrelationID: message.CorrelationID,
			ReplyTo:       message.ReplyTo,
			ContentType:   message.ContentType,
			Priority:      message.Priority,
			Persistent:    message.Persistent,
		}); err != nil {
			emitUndelivered(message.Data)
			return fmt.Errorf("forward to %s failed: %w", destination, err)
		}

		forwarded++
		st.record(len(body))
		if !quiet {
			log.Verbose("forwarded message %d to %s", forwarded, destination)
		}
	}

	return summarizeForward(forwarded, source, destination)
}

// startForwardStats returns a stats accumulator and a stop function. When stats
// is disabled it returns a non-nil accumulator (whose record is harmless) and a
// no-op stop, so callers need no nil checks.
func startForwardStats(enabled bool) (*streamStats, func()) {
	st := newStreamStats()
	if !enabled {
		return st, func() {}
	}
	stop := startStatsReporter(st, time.Second)
	return st, func() {
		stop()
		fmt.Fprintln(os.Stderr, st.summary())
	}
}

// runCommandOrRecover applies the optional shell command. On success it returns
// the command's output and true. If the command fails, it logs the error,
// writes the original (already-consumed) payload to stdout for recovery, and
// returns false so the caller skips the message instead of losing it.
func runCommandOrRecover(command string, data []byte) ([]byte, bool) {
	if command == "" {
		return data, true
	}
	out, err := runShellCommand(command, data)
	if err != nil {
		log.Error("command failed: %s\n", err)
		emitUndelivered(data)
		return nil, false
	}
	return out, true
}

// emitUndelivered writes a consumed-but-undelivered payload to stdout so an
// operator can recover it after a command or send failure.
func emitUndelivered(data []byte) {
	fmt.Fprint(os.Stdout, string(data))
	if log.IsStdout {
		fmt.Fprintln(os.Stdout)
	}
}

func summarizeForward(forwarded int, source, destination string) error {
	fmt.Printf("Forwarded %d message(s) from %s to %s\n", forwarded, source, destination)
	return nil
}
