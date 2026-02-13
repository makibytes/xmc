package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/makibytes/amc/broker/backends"
	"github.com/makibytes/amc/log"
	"github.com/spf13/cobra"
)

// NewRequestCommand creates a request-reply command for queue-based brokers
func NewRequestCommand(backend backends.QueueBackend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "request <queue> [message]",
		Short: "Send a message and wait for a reply (request-reply pattern)",
		Long: `Sends a message to the specified queue with a reply-to address,
then waits for a response on the reply queue.

Uses the correlation ID from the request as the message ID for matching.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doRequest(cmd, args, backend)
		},
	}

	cmd.Flags().StringP("contenttype", "T", "text/plain", "MIME type of message data")
	cmd.Flags().StringP("correlationid", "C", "", "Correlation ID for request/response")
	cmd.Flags().StringP("messageid", "I", "", "Message ID")
	cmd.Flags().IntP("priority", "Y", 4, "Priority of the message (0-9)")
	cmd.Flags().BoolP("persistent", "d", false, "Make message persistent")
	cmd.Flags().StringP("replyto", "R", "", "Reply queue (auto-generated if not specified)")
	cmd.Flags().StringSliceP("property", "P", []string{}, "Message properties in key=value format")
	cmd.Flags().Float32P("timeout", "t", 30, "Seconds to wait for reply")
	cmd.Flags().BoolP("quiet", "q", false, "Quiet about properties, show data only")
	cmd.Flags().BoolP("json", "J", false, "Output reply as JSON")

	return cmd
}

func doRequest(cmd *cobra.Command, args []string, backend backends.QueueBackend) error {
	// Parse command flags
	contenttype, _ := cmd.Flags().GetString("contenttype")
	correlationid, _ := cmd.Flags().GetString("correlationid")
	messageid, _ := cmd.Flags().GetString("messageid")
	priority, _ := cmd.Flags().GetInt("priority")
	persistent, _ := cmd.Flags().GetBool("persistent")
	replyto, _ := cmd.Flags().GetString("replyto")
	timeout, _ := cmd.Flags().GetFloat32("timeout")
	quiet, _ := cmd.Flags().GetBool("quiet")
	jsonOutput, _ := cmd.Flags().GetBool("json")

	// Parse properties
	properties := make(map[string]any)
	propertySlice, _ := cmd.Flags().GetStringSlice("property")
	for _, property := range propertySlice {
		keyValue := strings.SplitN(property, "=", 2)
		if len(keyValue) == 2 {
			properties[keyValue[0]] = keyValue[1]
		} else {
			return fmt.Errorf("invalid property: %s", property)
		}
	}

	// Get message content (from args or stdin)
	var data []byte
	if len(args) > 1 {
		data = []byte(args[1])
	} else {
		var err error
		data, err = readFromStdin()
		if err != nil {
			return err
		}
	}

	// Reply queue is required for request-reply pattern
	if replyto == "" {
		replyto = "amc.reply"
	}

	// Set correlation ID if not provided
	if correlationid == "" && messageid != "" {
		correlationid = messageid
	}

	// Send the request
	sendOpts := backends.SendOptions{
		Queue:         args[0],
		Message:       data,
		Properties:    properties,
		MessageID:     messageid,
		CorrelationID: correlationid,
		ReplyTo:       replyto,
		ContentType:   contenttype,
		Priority:      priority,
		Persistent:    persistent,
	}

	log.Verbose("sending request to %s, expecting reply on %s...", args[0], replyto)
	if err := backend.Send(context.Background(), sendOpts); err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}

	// Wait for the reply on the reply queue
	withHeaderAndProperties := log.IsVerbose
	withApplicationProperties := !quiet || log.IsVerbose

	receiveOpts := backends.ReceiveOptions{
		Queue:                     replyto,
		Timeout:                   timeout,
		Wait:                      false,
		Acknowledge:               true,
		WithHeaderAndProperties:   withHeaderAndProperties,
		WithApplicationProperties: withApplicationProperties,
	}

	log.Verbose("waiting for reply on %s (timeout: %.1fs)...", replyto, timeout)
	message, err := backend.Receive(context.Background(), receiveOpts)
	if err != nil {
		if err == context.DeadlineExceeded {
			return fmt.Errorf("no reply received within %.1f seconds", timeout)
		}
		return fmt.Errorf("failed to receive reply: %w", err)
	}
	if message == nil {
		return fmt.Errorf("no reply received")
	}

	if jsonOutput {
		return displayMessageJSON(message)
	}
	return displayMessage(message, withHeaderAndProperties, !quiet)
}
