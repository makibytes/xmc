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
	cmd.Flags().BoolP("wait", "w", true, "Wait (endless) for a message to arrive")
	cmd.Flags().IntP("count", "n", 1, "Number of messages to receive")
	cmd.Flags().BoolP("json", "J", false, "Output messages as JSON")
	cmd.Flags().StringP("selector", "S", "", "Filter messages by property expression (e.g. \"color='red'\")")
	cmd.Flags().BoolP("durable", "D", false, "Create a durable subscription that survives disconnection")

	return cmd
}

func doSubscribe(cmd *cobra.Command, args []string, backend backends.TopicBackend) error {
	groupID, _ := cmd.Flags().GetString("group")
	timeout, _ := cmd.Flags().GetFloat32("timeout")
	wait, _ := cmd.Flags().GetBool("wait")
	quiet, _ := cmd.Flags().GetBool("quiet")
	count, _ := cmd.Flags().GetInt("count")
	jsonOutput, _ := cmd.Flags().GetBool("json")
	selector, _ := cmd.Flags().GetString("selector")
	durable, _ := cmd.Flags().GetBool("durable")

	withHeaderAndProperties := log.IsVerbose
	withApplicationProperties := !quiet || log.IsVerbose

	opts := backends.SubscribeOptions{
		Topic:                     args[0],
		GroupID:                   groupID,
		Timeout:                   timeout,
		Wait:                      wait,
		WithHeaderAndProperties:   withHeaderAndProperties,
		WithApplicationProperties: withApplicationProperties,
		Selector:                  selector,
		Durable:                   durable,
	}

	received := 0
	for received < count {
		message, err := backend.Subscribe(context.Background(), opts)
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
			if err := displayMessage(message, opts.WithHeaderAndProperties, !quiet); err != nil {
				return err
			}
		}
		received++
	}

	return nil
}
