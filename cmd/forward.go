package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
)

// NewForwardCommand creates the forward command: a continuous streaming relay
// that consumes messages from a source and sends them to a destination on the
// same broker, running until interrupted (or until a --for window or --count
// limit is reached).
//
// Source and destination each default to a queue. On a broker that supports
// both queues and topics, --from-topic/--to-topic select a topic endpoint
// instead, so a relay can cross topologies (e.g. queue -> topic) as well as
// stay within one. queueBackend/topicBackend may be nil — only the one(s)
// actually needed for the resolved topology are used; queueCapable/
// topicCapable reflect what the broker supports at all (used to decide which
// flags to register) and may differ from backend-nilness when this is called
// for flag-registration only (see WrapForwardCommand).
//
// Unlike move, which drains whatever is currently present and stops, forward
// keeps streaming as new messages arrive — useful for live bridging, mirroring
// traffic for debugging, or continuously redriving a dead-letter queue. An
// optional --command pipes each message through a shell command, turning
// forward into a streaming transformer.
//
// Message metadata (including the original message ID and, on Kafka/Pulsar,
// the partition/routing key) is preserved — see docs/BRIDGE_AND_FORWARD.md's
// "Metadata: Always preserved" claim, matched here the same way bridge's
// NDJSON path preserves it. The relay is destructive on the source (like
// move): if a downstream command or send fails, the consumed message is
// written to stdout so it can be recovered.
func NewForwardCommand(queueBackend backends.QueueBackend, topicBackend backends.TopicBackend, queueCapable, topicCapable bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "forward <source> <destination>",
		Short: "Continuously relay messages from one queue or topic to another (streaming)",
		Long: `Streams messages from a source to a destination on the same broker, running
until interrupted.

Both source and destination default to queues. On brokers that support both
queues and topics, --from-topic and --to-topic select a topic endpoint
instead, so a relay can cross topologies (e.g. a queue mirrored onto a topic)
as well as stay within one.

It differs from move (which drains what is present and stops) by continuing to
relay messages as they arrive. Use --for to relay for a bounded time, --count to
cap the number of messages, and --command to pipe each message through a shell
command (its stdout becomes the forwarded payload). --stats prints live
throughput to stderr.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doForward(cmd, args, queueBackend, topicBackend)
		},
	}

	dual := queueCapable && topicCapable
	if dual {
		cmd.Flags().Bool("from-topic", false, "Read the source as a topic (subscribe) instead of a queue")
		cmd.Flags().Bool("to-topic", false, "Write the destination as a topic (publish) instead of a queue")
	}
	if topicCapable {
		cmd.Flags().StringP("group", "g", "xmc-consumer-group", "Consumer group ID for the source subscription (topic source only)")
	}
	addForwardFlags(cmd)
	return cmd
}

// WrapForwardCommand builds the real forward command wired to lazy adapter
// factories: the source/destination topology is resolved from --from-topic/
// --to-topic (or forced when the broker only supports one model) before the
// corresponding adapter(s) are created, so a same-topology relay still opens
// only one connection, as before this command supported crossing topologies.
func WrapForwardCommand(queueFactory QueueAdapterFactory, topicFactory TopicAdapterFactory) *cobra.Command {
	queueCapable, topicCapable := queueFactory != nil, topicFactory != nil
	cmd := NewForwardCommand(nil, nil, queueCapable, topicCapable)
	cmd.RunE = func(c *cobra.Command, args []string) error {
		fromTopic, toTopic := resolveForwardTopology(c, queueCapable, topicCapable)

		var queueBackend backends.QueueBackend
		var topicBackend backends.TopicBackend

		if !fromTopic || !toTopic {
			if queueFactory == nil {
				return fmt.Errorf("this broker does not support queue operations")
			}
			a, err := queueFactory()
			if err != nil {
				return err
			}
			defer closeAdapter(a)
			queueBackend = a
		}
		if fromTopic || toTopic {
			if topicFactory == nil {
				return fmt.Errorf("this broker does not support topic operations")
			}
			a, err := topicFactory()
			if err != nil {
				return err
			}
			defer closeAdapter(a)
			topicBackend = a
		}

		return doForward(c, args, queueBackend, topicBackend)
	}
	return cmd
}

// resolveForwardTopology determines whether the forward source/destination are
// queues or topics. On a single-capability broker the sole available topology
// is forced (both source and destination); on a dual broker, --from-topic/
// --to-topic are read (they are only registered when both capabilities exist).
func resolveForwardTopology(cmd *cobra.Command, queueCapable, topicCapable bool) (fromTopic, toTopic bool) {
	switch {
	case topicCapable && !queueCapable:
		return true, true
	case queueCapable && !topicCapable:
		return false, false
	default:
		fromTopic, _ = cmd.Flags().GetBool("from-topic")
		toTopic, _ = cmd.Flags().GetBool("to-topic")
		return fromTopic, toTopic
	}
}

// closeAdapter closes a backend adapter and logs (rather than propagates) a
// close failure, matching WrapQueueCommand/WrapTopicCommand's behavior.
func closeAdapter(c interface{ Close() error }) {
	if err := c.Close(); err != nil {
		log.Verbose("close: %s", err)
	}
}

func addForwardFlags(cmd *cobra.Command) {
	cmd.Flags().StringP("command", "x", "", "Pipe each message through a shell command; its stdout is forwarded")
	cmd.Flags().VarP(newDurationValue(100*time.Millisecond, time.Second), "timeout", "t", "Time to wait for the next source message per poll (e.g. \"100ms\")")
	cmd.Flags().IntP("count", "n", 0, "Maximum messages to forward (0 = until interrupted)")
	cmd.Flags().String("for", "", "Relay for a bounded duration then stop (e.g. \"30s\", \"5m\")")
	cmd.Flags().Bool("forever", false, "Relay until interrupted / until xmc quits (no time bound)")
	cmd.Flags().Bool("stats", false, "Print live throughput statistics to stderr")
	cmd.Flags().StringP("selector", "S", "", "Only forward messages matching this selector expression")
	cmd.Flags().BoolP("quiet", "q", false, "Suppress the per-message log; print only the final summary")
}

func doForward(cmd *cobra.Command, args []string, queueBackend backends.QueueBackend, topicBackend backends.TopicBackend) error {
	source, destination := args[0], args[1]
	fromTopic, toTopic := resolveForwardTopology(cmd, queueBackend != nil, topicBackend != nil)
	if source == destination && fromTopic == toTopic {
		return fmt.Errorf("source and destination must differ")
	}

	groupID, _ := cmd.Flags().GetString("group")
	command, _ := cmd.Flags().GetString("command")
	timeout := float32(getDuration(cmd, "timeout").Seconds())
	count, _ := cmd.Flags().GetInt("count")
	selector, _ := cmd.Flags().GetString("selector")
	quiet, _ := cmd.Flags().GetBool("quiet")

	sf, err := ParseStreamingFlags(cmd)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	errw := cmd.ErrOrStderr()

	ctx, cancel := streamContext(sf.Duration, cmd.Context())
	defer cancel()
	st, stopStats := startForwardStats(sf.Stats, errw)
	defer stopStats()

	// readFn abstracts over Receive (queue) / Subscribe (topic) for the source.
	// Wait mirrors the pre-existing per-topology behavior: queue polls without
	// blocking (Wait: false), topic subscriptions block for the poll window
	// (Wait: true).
	var readFn func(context.Context) (*backends.Message, error)
	if fromTopic {
		readFn = func(ctx context.Context) (*backends.Message, error) {
			return topicBackend.Subscribe(ctx, backends.SubscribeOptions{
				Topic:       source,
				GroupID:     groupID,
				Timeout:     timeout,
				Wait:        true,
				Verbosity:   backends.VerbosityNormal,
				Selector:    selector,
				Acknowledge: true,
			})
		}
	} else {
		readFn = func(ctx context.Context) (*backends.Message, error) {
			return queueBackend.Receive(ctx, backends.ReceiveOptions{
				Queue:       source,
				Timeout:     timeout,
				Wait:        false,
				Acknowledge: true,
				Verbosity:   backends.VerbosityNormal,
				Selector:    selector,
			})
		}
	}

	// writeFn abstracts over Send (queue) / Publish (topic) for the destination.
	var writeFn func(ctx context.Context, body []byte, src *backends.Message) error
	if toTopic {
		writeFn = func(ctx context.Context, body []byte, src *backends.Message) error {
			return topicBackend.Publish(ctx, backends.PublishOptions{
				Topic:         destination,
				Message:       body,
				Key:           src.Key,
				Properties:    src.Properties,
				MessageID:     src.MessageID,
				CorrelationID: src.CorrelationID,
				ReplyTo:       src.ReplyTo,
				ContentType:   src.ContentType,
				Priority:      src.Priority,
				Persistent:    src.Persistent,
			})
		}
	} else {
		writeFn = func(ctx context.Context, body []byte, src *backends.Message) error {
			return queueBackend.Send(ctx, backends.SendOptions{
				Queue:         destination,
				Message:       body,
				Key:           src.Key,
				Properties:    src.Properties,
				MessageID:     src.MessageID,
				CorrelationID: src.CorrelationID,
				ReplyTo:       src.ReplyTo,
				ContentType:   src.ContentType,
				Priority:      src.Priority,
				Persistent:    src.Persistent,
			})
		}
	}

	forwarded := 0
	for count <= 0 || forwarded < count {
		if ctx.Err() != nil {
			break
		}

		message, err := readFn(ctx)
		switch {
		case errors.Is(err, context.Canceled):
			return summarizeForward(out, forwarded, source, destination)
		// DeadlineExceeded here is from the AMQP internal poll timeout (the
		// backend creates its own context from Background(), not from our ctx),
		// so it means "no message in this poll window" — keep looping. The
		// outer --for deadline is caught by ctx.Err() at the top of the loop.
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, backends.ErrNoMessageAvailable), message == nil && err == nil:
			continue
		case err != nil:
			return err
		}

		body, ok := runCommandOrRecover(command, message.Data, out, errw)
		if !ok {
			continue
		}

		if err := writeFn(ctx, body, message); err != nil {
			emitUndelivered(out, message.Data)
			return fmt.Errorf("forward to %s failed: %w", destination, err)
		}

		forwarded++
		st.record(len(body))
		if !quiet && log.IsVerbose {
			fmt.Fprintf(errw, "forwarded message %d to %s\n", forwarded, destination)
		}
	}

	return summarizeForward(out, forwarded, source, destination)
}

// startForwardStats returns a stats accumulator and a stop function. When stats
// is disabled it returns a non-nil accumulator (whose record is harmless) and a
// no-op stop, so callers need no nil checks. w receives live tick lines and the
// final summary (typically cmd.ErrOrStderr(); falls back to os.Stderr for CLI).
func startForwardStats(enabled bool, w io.Writer) (*streamStats, func()) {
	st := newStreamStats()
	if !enabled {
		return st, func() {}
	}
	stop := startStatsReporter(st, time.Second, w)
	return st, func() {
		stop()
		fmt.Fprintln(w, st.summary())
	}
}

// runCommandOrRecover applies the optional shell command. On success it returns
// the command's output and true. If the command fails, it logs the error,
// writes the original (already-consumed) payload to out for recovery, and
// returns false so the caller skips the message instead of losing it.
func runCommandOrRecover(command string, data []byte, out, errw io.Writer) ([]byte, bool) {
	if command == "" {
		return data, true
	}
	result, err := runShellCommand(command, data, errw)
	if err != nil {
		fmt.Fprintf(errw, "command failed: %s\n", err)
		emitUndelivered(out, data)
		return nil, false
	}
	return result, true
}

// emitUndelivered writes a consumed-but-undelivered payload to w so an
// operator can recover it after a command or send failure. Callers pass
// cmd.OutOrStdout() so that the output is captured in background-process mode.
func emitUndelivered(w io.Writer, data []byte) {
	fmt.Fprint(w, string(data))
	if shouldAddNewline(w) {
		fmt.Fprintln(w)
	}
}

func summarizeForward(w io.Writer, forwarded int, source, destination string) error {
	fmt.Fprintf(w, "Forwarded %d message(s) from %s to %s\n", forwarded, source, destination)
	return nil
}
