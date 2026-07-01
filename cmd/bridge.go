package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
)

// NewBridgeCommand creates the bridge command: reads from a source and streams
// the messages as NDJSON to an external command's stdin (cross-broker relay).
//
// The source defaults to a queue. On a broker that also supports topics,
// --topic reads the source as a topic (subscribe) instead. The target's
// topology is chosen freely by the caller through the --to command itself
// (e.g. "... send q" for a queue, "... publish t" for a topic), so bridge
// already supports queue->topic and topic->queue relays; --topic only
// disambiguates the *source* side. queueBackend/topicBackend may be nil — only
// the one actually needed for the resolved source topology is used;
// queueCapable/topicCapable reflect what the broker supports at all (used to
// decide which flags to register) and may differ from backend-nilness when
// this is called for flag-registration only (see WrapBridgeCommand).
func NewBridgeCommand(queueBackend backends.QueueBackend, topicBackend backends.TopicBackend, queueCapable, topicCapable bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bridge <source> --to '<target command>'",
		Short: "Stream messages from a queue or topic to an external command (cross-broker relay)",
		Long: `Reads messages from a source and streams them as NDJSON to an external
command's stdin. The target command is typically another xmc binary's "send"
or "publish" with --ndjson, enabling lossless cross-broker forwarding in
either direction (queue->queue, queue->topic, topic->queue, topic->topic).

The source defaults to a queue; on brokers that also support topics, --topic
reads it as a topic (subscribe) instead. The target's topology is simply
whichever verb the --to command uses.

The target command runs as a long-lived subprocess; messages are streamed
one-by-one. --ndjson is auto-appended to the target if not present.

Examples:
  bridge orders --to 'kmc send orders-mirror'
  bridge events --topic --to 'kmc send events-archive'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doBridge(cmd, args, queueBackend, topicBackend)
		},
	}

	cmd.Flags().String("to", "", "Target command to stream NDJSON to (required)")
	cmd.MarkFlagRequired("to") //nolint:errcheck
	if queueCapable && topicCapable {
		cmd.Flags().Bool("topic", false, "Read the source as a topic (subscribe) instead of a queue")
	}
	if topicCapable {
		cmd.Flags().StringP("group", "g", "xmc-consumer-group", "Consumer group ID for the source subscription (topic source only)")
	}
	addForwardFlags(cmd)
	return cmd
}

// WrapBridgeCommand builds the real bridge command wired to lazy adapter
// factories: the source topology is resolved from --topic (or forced when the
// broker only supports one model) before the corresponding adapter is
// created, so only one connection is ever opened.
func WrapBridgeCommand(queueFactory QueueAdapterFactory, topicFactory TopicAdapterFactory) *cobra.Command {
	queueCapable, topicCapable := queueFactory != nil, topicFactory != nil
	cmd := NewBridgeCommand(nil, nil, queueCapable, topicCapable)
	cmd.RunE = func(c *cobra.Command, args []string) error {
		useTopic := resolveBridgeTopology(c, queueCapable, topicCapable)

		if useTopic {
			if topicFactory == nil {
				return fmt.Errorf("this broker does not support topic operations")
			}
			a, err := topicFactory()
			if err != nil {
				return err
			}
			defer closeAdapter(a)
			return doBridge(c, args, nil, a)
		}

		if queueFactory == nil {
			return fmt.Errorf("this broker does not support queue operations")
		}
		a, err := queueFactory()
		if err != nil {
			return err
		}
		defer closeAdapter(a)
		return doBridge(c, args, a, nil)
	}
	return cmd
}

// resolveBridgeTopology determines whether the bridge source is a queue or a
// topic. On a single-capability broker the sole available topology is forced;
// on a dual broker, --topic is read (it is only registered when both
// capabilities exist).
func resolveBridgeTopology(cmd *cobra.Command, queueCapable, topicCapable bool) bool {
	switch {
	case topicCapable && !queueCapable:
		return true
	case queueCapable && !topicCapable:
		return false
	default:
		useTopic, _ := cmd.Flags().GetBool("topic")
		return useTopic
	}
}

func doBridge(cmd *cobra.Command, args []string, queueBackend backends.QueueBackend, topicBackend backends.TopicBackend) error {
	source := args[0]
	target, _ := cmd.Flags().GetString("to")
	groupID, _ := cmd.Flags().GetString("group")
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

	ctx, cancel := timedOrInterruptCtx(cmd.Context(), sf.Duration)
	defer cancel()

	st, stopStats := startForwardStats(sf.Stats, errw)
	defer stopStats()

	proc, stdinPipe, err := startTargetProcess(ctx, target, out, errw)
	if err != nil {
		return err
	}
	defer func() {
		stdinPipe.Close()
		proc.Wait() //nolint:errcheck
	}()

	// readFn abstracts over Receive (queue) / Subscribe (topic) for the source.
	var readFn func(context.Context) (*backends.Message, error)
	var readErrLabel string
	if topicBackend != nil {
		readErrLabel = "subscribe from"
		readFn = func(ctx context.Context) (*backends.Message, error) {
			return topicBackend.Subscribe(ctx, backends.SubscribeOptions{
				Topic:    source,
				GroupID:  groupID,
				Timeout:  timeout,
				Wait:     false,
				Selector: selector,
			})
		}
	} else {
		readErrLabel = "receive from"
		readFn = func(ctx context.Context) (*backends.Message, error) {
			return queueBackend.Receive(ctx, backends.ReceiveOptions{
				Queue:       source,
				Timeout:     timeout,
				Wait:        false,
				Acknowledge: true,
				Selector:    selector,
			})
		}
	}

	bridged := 0
	for count == 0 || bridged < count {
		if ctx.Err() != nil {
			break
		}
		msg, err := readFn(ctx)
		if err != nil {
			// ctx.Err() catches the outer --for deadline or kill signal.
			if ctx.Err() != nil {
				break
			}
			// DeadlineExceeded is the AMQP backend's internal poll timeout
			// (100ms, from its own Background context), not the outer deadline.
			// ErrNoMessageAvailable is its logical equivalent. Both mean "empty
			// source this poll" — keep looping.
			if errors.Is(err, backends.ErrNoMessageAvailable) || errors.Is(err, context.DeadlineExceeded) {
				continue
			}
			return fmt.Errorf("%s %s: %w", readErrLabel, source, err)
		}

		if err := displayMessageNDJSON(stdinPipe, msg); err != nil {
			emitUndelivered(out, msg.Data)
			return fmt.Errorf("write to target: %w", err)
		}

		bridged++
		st.record(len(msg.Data))
		if !quiet && log.IsVerbose {
			fmt.Fprintf(errw, "bridged message %d from %s\n", bridged, source)
		}
	}

	return summarizeForward(out, bridged, source, target)
}

func startTargetProcess(ctx context.Context, target string, out, errw io.Writer) (*exec.Cmd, io.WriteCloser, error) {
	if !strings.Contains(target, "--ndjson") {
		target = target + " --ndjson"
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "sh"
	}

	proc := exec.CommandContext(ctx, shell, "-c", target)
	proc.Stdout = out
	proc.Stderr = errw

	stdinPipe, err := proc.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("create stdin pipe: %w", err)
	}

	if err := proc.Start(); err != nil {
		return nil, nil, fmt.Errorf("start target command %q: %w", target, err)
	}

	return proc, stdinPipe, nil
}

// timedOrInterruptCtx returns a context that cancels on interrupt or after
// duration (if > 0).
func timedOrInterruptCtx(parent context.Context, duration time.Duration) (context.Context, context.CancelFunc) {
	if duration > 0 {
		return context.WithTimeout(parent, duration)
	}
	return context.WithCancel(parent)
}
