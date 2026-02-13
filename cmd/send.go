package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
)

// NewSendCommand creates a send command for queue-based brokers
func NewSendCommand(backend backends.QueueBackend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send <queue> [message]",
		Short: "Send a message to a queue",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doSend(cmd, args, backend)
		},
	}

	cmd.Flags().StringP("contenttype", "T", "text/plain", "MIME type of message data")
	cmd.Flags().StringP("correlationid", "C", "", "Correlation ID for request/response")
	cmd.Flags().StringP("messageid", "I", "", "Message ID")
	cmd.Flags().IntP("priority", "Y", 4, "Priority of the message (0-9)")
	cmd.Flags().BoolP("persistent", "d", false, "Make message persistent")
	cmd.Flags().StringP("replyto", "R", "", "Reply to queue for request/response")
	cmd.Flags().StringSliceP("property", "P", []string{}, "Message properties in key=value format")
	cmd.Flags().IntP("count", "n", 1, "Number of times to send the message")
	cmd.Flags().Int64P("ttl", "E", 0, "Message time-to-live in milliseconds (0 = no expiry)")
	cmd.Flags().BoolP("lines", "l", false, "Read stdin line by line, send each line as a separate message")

	return cmd
}

func doSend(cmd *cobra.Command, args []string, backend backends.QueueBackend) error {
	// Parse command flags
	contenttype, _ := cmd.Flags().GetString("contenttype")
	correlationid, _ := cmd.Flags().GetString("correlationid")
	messageid, _ := cmd.Flags().GetString("messageid")
	priority, _ := cmd.Flags().GetInt("priority")
	persistent, _ := cmd.Flags().GetBool("persistent")
	replyto, _ := cmd.Flags().GetString("replyto")
	count, _ := cmd.Flags().GetInt("count")
	ttl, _ := cmd.Flags().GetInt64("ttl")
	lines, _ := cmd.Flags().GetBool("lines")

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

	// Line-delimited mode: read stdin line by line, send each as a separate message
	if lines {
		return sendLines(backend, args[0], properties, contenttype, correlationid, messageid, replyto, priority, persistent, ttl)
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

	// Create send options
	opts := backends.SendOptions{
		Queue:         args[0],
		Message:       data,
		Properties:    properties,
		MessageID:     messageid,
		CorrelationID: correlationid,
		ReplyTo:       replyto,
		ContentType:   contenttype,
		Priority:      priority,
		Persistent:    persistent,
		TTL:           ttl,
	}

	for i := 0; i < count; i++ {
		if err := backend.Send(context.Background(), opts); err != nil {
			return err
		}
		if count > 1 {
			log.Verbose("sent message %d/%d", i+1, count)
		}
	}

	return nil
}

func sendLines(backend backends.QueueBackend, queue string, properties map[string]any, contenttype, correlationid, messageid, replyto string, priority int, persistent bool, ttl int64) error {
	scanner := bufio.NewScanner(os.Stdin)
	sent := 0
	for scanner.Scan() {
		line := scanner.Text()
		opts := backends.SendOptions{
			Queue:         queue,
			Message:       []byte(line),
			Properties:    properties,
			MessageID:     messageid,
			CorrelationID: correlationid,
			ReplyTo:       replyto,
			ContentType:   contenttype,
			Priority:      priority,
			Persistent:    persistent,
			TTL:           ttl,
		}
		if err := backend.Send(context.Background(), opts); err != nil {
			return err
		}
		sent++
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading stdin: %w", err)
	}
	log.Verbose("sent %d messages", sent)
	return nil
}

func readFromStdin() ([]byte, error) {
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) != 0 {
		return nil, errors.New("no message provided and no data in stdin")
	}

	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, err
	}

	return data, nil
}