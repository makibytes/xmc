package cmd

import (
	"context"
	"os"
	"time"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
)

// NewSendCommand creates a send command for queue-based brokers
func NewSendCommand(backend backends.QueueBackend) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "send <queue> [message]",
		Aliases: []string{"put"},
		Short:   "Send a message to a queue",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doSend(cmd, args, backend)
		},
	}

	cmd.Flags().StringP("content-type", "T", "text/plain", "MIME type of message data")
	cmd.Flags().StringP("correlation-id", "C", "", "Correlation ID for request/response")
	cmd.Flags().StringP("message-id", "I", "", "Message ID")
	cmd.Flags().IntP("priority", "Y", 4, "Priority of the message (0-9)")
	cmd.Flags().BoolP("persistent", "d", false, "Make message persistent")
	cmd.Flags().StringP("reply-to", "R", "", "Reply to queue for request/response")
	cmd.Flags().StringSliceP("property", "P", []string{}, "Message properties in key=value format")
	cmd.Flags().IntP("count", "n", 1, "Number of times to send the message")
	cmd.Flags().VarP(newDurationValue(0, time.Millisecond), "ttl", "E", "Message time-to-live (e.g. \"5s\", \"1m\"; 0 = no expiry)")
	cmd.Flags().BoolP("lines", "l", false, "Read stdin line by line, send each line as a separate message")
	cmd.Flags().Bool("ndjson", false, "Read newline-delimited JSON records from stdin and send each (lossless import)")
	cmd.Flags().Float64("rate", 0, "Throttle to at most this many messages per second (0 = unlimited)")
	// Accept legacy concatenated spellings (--contenttype) as aliases of the
	// kebab-case names (--content-type).
	cmd.Flags().SetNormalizeFunc(aliasNormalize)

	return cmd
}

func doSend(cmd *cobra.Command, args []string, backend backends.QueueBackend) error {
	// Parse command flags
	contenttype, _ := cmd.Flags().GetString("content-type")
	correlationid, _ := cmd.Flags().GetString("correlation-id")
	messageid, _ := cmd.Flags().GetString("message-id")
	priority, _ := cmd.Flags().GetInt("priority")
	persistent, _ := cmd.Flags().GetBool("persistent")
	replyto, _ := cmd.Flags().GetString("reply-to")
	count, _ := cmd.Flags().GetInt("count")
	ttl := getDuration(cmd, "ttl").Milliseconds()
	lines, _ := cmd.Flags().GetBool("lines")
	ndjson, _ := cmd.Flags().GetBool("ndjson")
	rate, _ := cmd.Flags().GetFloat64("rate")

	properties, err := parsePropertiesFlag(cmd.Flags())
	if err != nil {
		return err
	}

	limiter := newRateLimiter(rate)

	// NDJSON import: each stdin record carries its own metadata.
	if ndjson {
		return sendNDJSON(backend, args[0], limiter)
	}

	// Line-delimited mode: read stdin line by line, send each as a separate message
	if lines {
		return sendLines(backend, args[0], properties, contenttype, correlationid, messageid, replyto, priority, persistent, ttl, limiter)
	}

	data, err := readCommandMessage(args)
	if err != nil {
		return err
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
		limiter.wait()
		if err := backend.Send(context.Background(), opts); err != nil {
			return err
		}
		if count > 1 {
			log.Verbose("sent message %d/%d", i+1, count)
		}
	}

	return nil
}

func sendLines(backend backends.QueueBackend, queue string, properties map[string]any, contenttype, correlationid, messageid, replyto string, priority int, persistent bool, ttl int64, limiter *rateLimiter) error {
	sent, err := forEachInputLine(func(line string) error {
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
		limiter.wait()
		if err := backend.Send(context.Background(), opts); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	log.Verbose("sent %d messages", sent)
	return nil
}

// sendNDJSON reads NDJSON message records from stdin and sends each one,
// restoring the metadata stored in the record. It is the import counterpart to
// `receive --ndjson`, enabling queue restore and cross-broker migration.
func sendNDJSON(backend backends.QueueBackend, queue string, limiter *rateLimiter) error {
	sent, err := forEachRecord(os.Stdin, func(rec messageRecord) error {
		data, err := rec.payload()
		if err != nil {
			return err
		}
		limiter.wait()
		return backend.Send(context.Background(), backends.SendOptions{
			Queue:         queue,
			Message:       data,
			Properties:    rec.Properties,
			MessageID:     rec.MessageID,
			CorrelationID: rec.CorrelationID,
			ReplyTo:       rec.ReplyTo,
			ContentType:   rec.ContentType,
			Priority:      rec.Priority,
			Persistent:    rec.Persistent,
		})
	})
	if err != nil {
		return err
	}
	log.Verbose("sent %d messages", sent)
	return nil
}
