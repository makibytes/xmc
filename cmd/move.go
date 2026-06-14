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

// NewMoveCommand creates a move command that transfers messages from a source
// queue to a destination queue on the same broker. Its primary use is redriving
// a dead-letter queue back onto a processing queue after the cause of failure
// has been fixed, but it equally serves to drain or relocate a queue.
//
// The move is destructive: each message is consumed from the source and then
// sent to the destination. Message metadata (correlation ID, content type,
// reply-to, priority, persistence and application properties) is preserved; the
// destination assigns a fresh message ID, mirroring how brokers treat redriven
// messages.
//
// If a send fails, the in-flight message — already consumed from the source — is
// written to stdout so it is not lost, and the command stops with an error.
func NewMoveCommand(backend backends.QueueBackend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "move <source> <destination>",
		Short: "Move messages from one queue to another (e.g. dead-letter redrive)",
		Long: `Transfers messages from a source queue to a destination queue on the same broker.

The typical use is redriving a dead-letter queue back onto its processing queue
after fixing the cause of failure. By default every currently available message
is moved; use --count to limit how many.

The move is destructive: a message is consumed from the source before it is sent
to the destination, and message metadata is preserved. If a send fails, the
in-flight message is written to stdout so it can be recovered, and the command
stops.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doMove(cmd, args, backend)
		},
	}

	cmd.Flags().IntP("count", "n", 0, "Maximum number of messages to move (0 = move all available)")
	cmd.Flags().StringP("selector", "S", "", "Only move messages matching this selector expression")
	cmd.Flags().VarP(newDurationValue(100*time.Millisecond, time.Second), "timeout", "t", "Time to wait for the next source message before stopping (e.g. \"100ms\")")
	cmd.Flags().BoolP("quiet", "q", false, "Suppress the per-message log; print only the final summary")

	return cmd
}

func doMove(cmd *cobra.Command, args []string, backend backends.QueueBackend) error {
	source, destination := args[0], args[1]
	count, _ := cmd.Flags().GetInt("count")
	selector, _ := cmd.Flags().GetString("selector")
	timeout := float32(getDuration(cmd, "timeout").Seconds())
	quiet, _ := cmd.Flags().GetBool("quiet")

	if source == destination {
		return fmt.Errorf("source and destination must differ")
	}

	ctx, stop := interruptContext()
	defer stop()

	moved := 0
	for count == 0 || moved < count {
		message, err := backend.Receive(ctx, backends.ReceiveOptions{
			Queue:       source,
			Timeout:     timeout,
			Wait:        false,
			Acknowledge: true, // destructive read: remove from source
			Verbosity:   backends.VerbosityNormal,
			Selector:    selector,
		})

		switch {
		case errors.Is(err, context.Canceled),
			errors.Is(err, context.DeadlineExceeded),
			errors.Is(err, backends.ErrNoMessageAvailable),
			message == nil && err == nil:
			return summarizeMove(moved, source, destination)
		case err != nil:
			return err
		}

		sendErr := backend.Send(ctx, backends.SendOptions{
			Queue:         destination,
			Message:       message.Data,
			Properties:    message.Properties,
			CorrelationID: message.CorrelationID,
			ReplyTo:       message.ReplyTo,
			ContentType:   message.ContentType,
			Priority:      message.Priority,
			Persistent:    message.Persistent,
		})
		if sendErr != nil {
			// The message was already consumed from the source; surface it so
			// the operator can recover the one in-flight message.
			fmt.Fprintf(os.Stderr, "send to %s failed after %d moved; undelivered message follows on stdout:\n", destination, moved)
			fmt.Fprint(os.Stdout, string(message.Data))
			if log.IsStdout {
				fmt.Fprintln(os.Stdout)
			}
			return fmt.Errorf("send to %s failed: %w", destination, sendErr)
		}

		moved++
		if !quiet {
			log.Verbose("moved message %d to %s", moved, destination)
		}
	}

	return summarizeMove(moved, source, destination)
}

func summarizeMove(moved int, source, destination string) error {
	fmt.Printf("Moved %d message(s) from %s to %s\n", moved, source, destination)
	return nil
}
