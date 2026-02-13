package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
)

// NewReceiveCommand creates a receive command for queue-based brokers
func NewReceiveCommand(backend backends.QueueBackend) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "receive <queue>",
		Aliases: []string{"get"},
		Short:   "Receive a message from a queue (destructive read)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doReceive(cmd, args, backend, true)
		},
	}

	cmd.Flags().Float32P("timeout", "t", 0.1, "Seconds to wait")
	cmd.Flags().BoolP("quiet", "q", false, "Quiet about properties, show data only")
	cmd.Flags().BoolP("wait", "w", false, "Wait (endless) for a message to arrive")
	cmd.Flags().IntP("count", "n", 1, "Number of messages to receive")
	cmd.Flags().BoolP("json", "J", false, "Output messages as JSON")
	cmd.Flags().StringP("selector", "S", "", "Filter messages by property expression (e.g. \"color='red'\")")

	return cmd
}

func doReceive(cmd *cobra.Command, args []string, backend backends.QueueBackend, acknowledge bool) error {
	timeout, _ := cmd.Flags().GetFloat32("timeout")
	wait, _ := cmd.Flags().GetBool("wait")
	quiet, _ := cmd.Flags().GetBool("quiet")
	count, _ := cmd.Flags().GetInt("count")
	jsonOutput, _ := cmd.Flags().GetBool("json")
	selector, _ := cmd.Flags().GetString("selector")

	withHeaderAndProperties := log.IsVerbose
	withApplicationProperties := !quiet || log.IsVerbose

	opts := backends.ReceiveOptions{
		Queue:                     args[0],
		Timeout:                   timeout,
		Wait:                      wait,
		Acknowledge:               acknowledge,
		WithHeaderAndProperties:   withHeaderAndProperties,
		WithApplicationProperties: withApplicationProperties,
		Selector:                  selector,
	}

	received := 0
	for received < count {
		message, err := backend.Receive(context.Background(), opts)
		if err != nil {
			if err == context.DeadlineExceeded {
				return nil // no message when timeout occurs -> no error
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

func displayMessage(message *backends.Message, withHeader, withProps bool) error {
	if withHeader && len(message.InternalMetadata) > 0 {
		for k, v := range message.InternalMetadata {
			fmt.Fprintf(os.Stderr, "%s: %v\n", k, v)
		}
	}

	if withProps && len(message.Properties) > 0 {
		propertiesString := ""
		for k, v := range message.Properties {
			if propertiesString != "" {
				propertiesString += ","
			}
			propertiesString += fmt.Sprintf("%s=%v", k, v)
		}
		fmt.Fprintf(os.Stderr, "Properties: %s\n", propertiesString)
	}

	// Always print message data
	fmt.Print(string(message.Data))
	// Add newline if stdout just for better readability
	if log.IsStdout {
		fmt.Println()
	}

	return nil
}

// displayMessageJSON outputs the message as a JSON object
func displayMessageJSON(message *backends.Message) error {
	output := map[string]any{
		"data": string(message.Data),
	}
	if message.MessageID != "" {
		output["messageId"] = message.MessageID
	}
	if message.CorrelationID != "" {
		output["correlationId"] = message.CorrelationID
	}
	if message.ReplyTo != "" {
		output["replyTo"] = message.ReplyTo
	}
	if message.ContentType != "" {
		output["contentType"] = message.ContentType
	}
	if message.Priority != 0 {
		output["priority"] = message.Priority
	}
	if message.Persistent {
		output["persistent"] = message.Persistent
	}
	if len(message.Properties) > 0 {
		output["properties"] = message.Properties
	}
	if len(message.InternalMetadata) > 0 {
		output["metadata"] = message.InternalMetadata
	}

	data, err := json.Marshal(output)
	if err != nil {
		return fmt.Errorf("failed to marshal message to JSON: %w", err)
	}

	fmt.Println(string(data))
	return nil
}
