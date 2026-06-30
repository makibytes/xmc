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

func NewBridgeCommand(backend backends.QueueBackend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bridge <source> --to '<target command>'",
		Short: "Stream messages from a queue to an external command (cross-broker relay)",
		Long: `Reads messages from a source queue and streams them as NDJSON to an external
command's stdin. The target command is typically another xmc binary's "send"
or "publish" with --ndjson, enabling lossless cross-broker forwarding.

The target command runs as a long-lived subprocess; messages are streamed
one-by-one. --ndjson is auto-appended to the target if not present.

Example: bridge orders --to 'kmc send orders-mirror'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doBridgeQueue(cmd, args, backend)
		},
	}

	cmd.Flags().String("to", "", "Target command to stream NDJSON to (required)")
	cmd.MarkFlagRequired("to") //nolint:errcheck
	addForwardFlags(cmd)
	return cmd
}

func NewBridgeTopicCommand(backend backends.TopicBackend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bridge <source-topic> --to '<target command>'",
		Short: "Stream messages from a topic to an external command (cross-broker relay)",
		Long: `Reads messages from a source topic and streams them as NDJSON to an external
command's stdin. Use for cross-broker topic mirroring.

Example: bridge events --to 'kmc publish events-mirror' --for 1h`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doBridgeTopic(cmd, args, backend)
		},
	}

	cmd.Flags().String("to", "", "Target command to stream NDJSON to (required)")
	cmd.MarkFlagRequired("to") //nolint:errcheck
	cmd.Flags().StringP("group", "g", "xmc-consumer-group", "Consumer group ID for the source subscription")
	addForwardFlags(cmd)
	return cmd
}

func doBridgeQueue(cmd *cobra.Command, args []string, backend backends.QueueBackend) error {
	source := args[0]
	target, _ := cmd.Flags().GetString("to")
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

	bridged := 0
	for count == 0 || bridged < count {
		if ctx.Err() != nil {
			break
		}
		msg, err := backend.Receive(ctx, backends.ReceiveOptions{
			Queue:       source,
			Timeout:     timeout,
			Wait:        false,
			Acknowledge: true,
			Selector:    selector,
		})
		if err != nil {
			// ctx.Err() catches the outer --for deadline or kill signal.
			if ctx.Err() != nil {
				break
			}
			// DeadlineExceeded is the AMQP backend's internal poll timeout
			// (100ms, from its own Background context), not the outer deadline.
			// ErrNoMessageAvailable is its logical equivalent. Both mean "empty
			// queue this poll" — keep looping.
			if errors.Is(err, backends.ErrNoMessageAvailable) || errors.Is(err, context.DeadlineExceeded) {
				continue
			}
			return fmt.Errorf("receive from %s: %w", source, err)
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

func doBridgeTopic(cmd *cobra.Command, args []string, backend backends.TopicBackend) error {
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

	bridged := 0
	for count == 0 || bridged < count {
		if ctx.Err() != nil {
			break
		}
		msg, err := backend.Subscribe(ctx, backends.SubscribeOptions{
			Topic:    source,
			GroupID:  groupID,
			Timeout:  timeout,
			Wait:     false,
			Selector: selector,
		})
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			if errors.Is(err, backends.ErrNoMessageAvailable) || errors.Is(err, context.DeadlineExceeded) {
				continue
			}
			return fmt.Errorf("subscribe from %s: %w", source, err)
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
