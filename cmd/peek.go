package cmd

import (
	"context"
	"fmt"

	"github.com/makibytes/amc/broker/backends"
	"github.com/spf13/cobra"
)

// NewPeekCommand creates a peek command for queue-based brokers
func NewPeekCommand(backend backends.QueueBackend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "peek <queue>",
		Short: "Peek at a message in the queue without removing it (non-destructive read)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doPeek(cmd, args, backend)
		},
	}

	cmd.Flags().Float32P("timeout", "t", 0.1, "Seconds to wait")
	cmd.Flags().BoolP("wait", "w", false, "Wait (endless) for a message to arrive")

	return cmd
}

func doPeek(cmd *cobra.Command, args []string, backend backends.QueueBackend) error {
	timeout, _ := cmd.Flags().GetFloat32("timeout")
	wait, _ := cmd.Flags().GetBool("wait")

	opts := backends.ReceiveOptions{
		Queue:                     args[0],
		Timeout:                   timeout,
		Wait:                      wait,
		Acknowledge:               false, // peek = non-destructive
		WithHeaderAndProperties:   true,
		WithApplicationProperties: true,
	}

	message, err := backend.Receive(context.Background(), opts)
	if err != nil {
		if err == context.DeadlineExceeded {
			return nil // no message when timeout occurs -> no error
		}
		return err
	}
	if message == nil {
		return fmt.Errorf("no message available")
	}

	return displayMessage(message, opts.WithHeaderAndProperties, opts.WithApplicationProperties)
}