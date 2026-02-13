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
	cmd.Flags().IntP("count", "n", 1, "Number of messages to peek")
	cmd.Flags().BoolP("json", "J", false, "Output messages as JSON")
	cmd.Flags().StringP("selector", "S", "", "Filter messages by property expression (e.g. \"color='red'\")")

	return cmd
}

func doPeek(cmd *cobra.Command, args []string, backend backends.QueueBackend) error {
	timeout, _ := cmd.Flags().GetFloat32("timeout")
	wait, _ := cmd.Flags().GetBool("wait")
	count, _ := cmd.Flags().GetInt("count")
	jsonOutput, _ := cmd.Flags().GetBool("json")
	selector, _ := cmd.Flags().GetString("selector")

	opts := backends.ReceiveOptions{
		Queue:                     args[0],
		Timeout:                   timeout,
		Wait:                      wait,
		Acknowledge:               false, // peek = non-destructive
		WithHeaderAndProperties:   true,
		WithApplicationProperties: true,
		Selector:                  selector,
	}

	received := 0
	for received < count {
		message, err := backend.Receive(context.Background(), opts)
		if err != nil {
			if err == context.DeadlineExceeded {
				return nil
			}
			return err
		}
		if message == nil {
			if received == 0 {
				return fmt.Errorf("no message available")
			}
			return nil
		}

		if jsonOutput {
			if err := displayMessageJSON(message); err != nil {
				return err
			}
		} else {
			if err := displayMessage(message, opts.WithHeaderAndProperties, opts.WithApplicationProperties); err != nil {
				return err
			}
		}
		received++
	}

	return nil
}