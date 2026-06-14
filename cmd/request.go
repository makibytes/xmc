package cmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/log"
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

	cmd.Flags().StringP("content-type", "T", "text/plain", "MIME type of message data")
	cmd.Flags().StringP("correlation-id", "C", "", "Correlation ID for request/response")
	cmd.Flags().StringP("message-id", "I", "", "Message ID")
	cmd.Flags().IntP("priority", "Y", 4, "Priority of the message (0-9)")
	cmd.Flags().BoolP("persistent", "d", false, "Make message persistent")
	cmd.Flags().StringP("reply-to", "R", "", "Reply queue (auto-generated if not specified)")
	cmd.Flags().StringSliceP("property", "P", []string{}, "Message properties in key=value format")
	cmd.Flags().VarP(newDurationValue(30*time.Second, time.Second), "timeout", "t", "Time to wait for the reply (e.g. \"30s\", \"500ms\")")
	cmd.Flags().BoolP("quiet", "q", false, "Quiet about properties, show data only")
	cmd.Flags().BoolP("json", "J", false, "Output reply as JSON")
	cmd.Flags().StringP("format", "F", "", "Output format string, e.g. \"%i %s\\n\" (overrides --json)")
	// Accept legacy concatenated spellings (--contenttype) as aliases of the
	// kebab-case names (--content-type).
	cmd.Flags().SetNormalizeFunc(aliasNormalize)

	return cmd
}

func doRequest(cmd *cobra.Command, args []string, backend backends.QueueBackend) error {
	// Parse command flags
	contenttype, _ := cmd.Flags().GetString("content-type")
	correlationid, _ := cmd.Flags().GetString("correlation-id")
	messageid, _ := cmd.Flags().GetString("message-id")
	priority, _ := cmd.Flags().GetInt("priority")
	persistent, _ := cmd.Flags().GetBool("persistent")
	replyto, _ := cmd.Flags().GetString("reply-to")
	timeout := float32(getDuration(cmd, "timeout").Seconds())
	quiet, _ := cmd.Flags().GetBool("quiet")
	jsonOutput, _ := cmd.Flags().GetBool("json")
	format, _ := cmd.Flags().GetString("format")

	properties, err := parsePropertiesFlag(cmd.Flags())
	if err != nil {
		return err
	}

	data, err := readCommandMessage(args)
	if err != nil {
		return err
	}

	// Reply queue is required for request-reply pattern
	if replyto == "" {
		replyto = "xmc.reply"
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

	receiveOpts := backends.ReceiveOptions{
		Queue:       replyto,
		Timeout:     timeout,
		Wait:        false,
		Acknowledge: true,
		Verbosity:   commandVerbosity(quiet),
	}

	log.Verbose("waiting for reply on %s (timeout: %.1fs)...", replyto, timeout)
	message, err := backend.Receive(context.Background(), receiveOpts)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("no reply received within %.1f seconds", timeout)
		}
		if errors.Is(err, backends.ErrNoMessageAvailable) {
			return fmt.Errorf("no reply received")
		}
		return fmt.Errorf("failed to receive reply: %w", err)
	}
	if message == nil {
		return fmt.Errorf("no reply received")
	}

	if format != "" {
		return displayMessageFormat(message, format)
	}
	if jsonOutput {
		return displayMessageJSON(message)
	}
	return displayMessage(message, receiveOpts.Verbosity)
}
