package cmd

import (
	"context"

	"github.com/makibytes/xmc/broker/backends"
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
	cmd.Flags().BoolP("quiet", "q", false, "Quiet about properties, show data only")
	cmd.Flags().BoolP("wait", "w", false, "Wait (endless) for a message to arrive")
	cmd.Flags().IntP("count", "n", 1, "Number of messages to peek")
	cmd.Flags().BoolP("json", "J", false, "Output messages as JSON")
	cmd.Flags().StringP("selector", "S", "", "Filter messages by property expression (e.g. \"color='red'\")")

	return cmd
}

func doPeek(cmd *cobra.Command, args []string, backend backends.QueueBackend) error {
	timeout, _ := cmd.Flags().GetFloat32("timeout")
	wait, _ := cmd.Flags().GetBool("wait")
	quiet, _ := cmd.Flags().GetBool("quiet")
	count, _ := cmd.Flags().GetInt("count")
	jsonOutput, _ := cmd.Flags().GetBool("json")
	selector, _ := cmd.Flags().GetString("selector")

	opts := backends.ReceiveOptions{
		Queue:       args[0],
		Timeout:     timeout,
		Wait:        wait,
		Acknowledge: false, // peek = non-destructive
		Verbosity:   commandVerbosity(quiet),
		Selector:    selector,
	}

	return consumeMessages(context.Background(), func(ctx context.Context) (*backends.Message, error) {
		return backend.Receive(ctx, opts)
	}, consumeConfig{
		count:      count,
		jsonOutput: jsonOutput,
		verbosity:  opts.Verbosity,
	})
}
