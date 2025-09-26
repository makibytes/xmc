package cmd

import (
	"context"
	"fmt"

	"github.com/makibytes/amc/broker/backends"
	"github.com/makibytes/amc/log"
	"github.com/spf13/cobra"
)

// NewSubscribeCommand creates a subscribe command for topic-based brokers
func NewSubscribeCommand(backend backends.TopicBackend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "subscribe <topic>",
		Short: "Subscribe and receive a message from a topic",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doSubscribe(cmd, args, backend)
		},
	}

	cmd.Flags().StringP("group", "g", "amc-consumer-group", "Consumer group ID")
	cmd.Flags().Float32P("timeout", "t", 0.1, "Seconds to wait")
	cmd.Flags().BoolP("quiet", "q", false, "Quiet about properties, show data only")
	cmd.Flags().BoolP("wait", "w", false, "Wait (endless) for a message to arrive")

	return cmd
}

func doSubscribe(cmd *cobra.Command, args []string, backend backends.TopicBackend) error {
	groupID, _ := cmd.Flags().GetString("group")
	timeout, _ := cmd.Flags().GetFloat32("timeout")
	wait, _ := cmd.Flags().GetBool("wait")
	quiet, _ := cmd.Flags().GetBool("quiet")

	withHeaderAndProperties := log.IsVerbose
	withApplicationProperties := !quiet || log.IsVerbose

	opts := backends.SubscribeOptions{
		Topic:                     args[0],
		GroupID:                   groupID,
		Timeout:                   timeout,
		Wait:                      wait,
		WithHeaderAndProperties:   withHeaderAndProperties,
		WithApplicationProperties: withApplicationProperties,
	}

	message, err := backend.Subscribe(context.Background(), opts)
	if err != nil {
		if err == context.DeadlineExceeded {
			return nil // no message when timeout occurs -> no error
		}
		return err
	}
	if message == nil {
		return fmt.Errorf("no message available")
	}

	// Display: internal metadata only if verbose, properties always (unless quiet)
	return displayMessage(message, opts.WithHeaderAndProperties, !quiet)
}
