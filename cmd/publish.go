package cmd

import (
	"context"
	"os"
	"time"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
)

// NewPublishCommand creates a publish command for topic-based brokers
func NewPublishCommand(backend backends.TopicBackend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "publish <topic> [message]",
		Short: "Publish a message to a topic",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doPublish(cmd, args, backend)
		},
	}

	cmd.Flags().StringP("content-type", "T", "text/plain", "MIME type of message data")
	cmd.Flags().StringP("correlation-id", "C", "", "Correlation ID for request/response")
	cmd.Flags().StringP("key", "K", "", "Message key for partitioning")
	cmd.Flags().StringP("message-id", "I", "", "Message ID")
	cmd.Flags().IntP("priority", "Y", 4, "Priority of the message (0-9)")
	cmd.Flags().BoolP("persistent", "d", false, "Make message persistent")
	cmd.Flags().StringP("reply-to", "R", "", "Reply to address for request/response")
	cmd.Flags().StringSliceP("property", "P", []string{}, "Message properties in key=value format")
	cmd.Flags().IntP("count", "n", 1, "Number of times to publish the message")
	cmd.Flags().VarP(newDurationValue(0, time.Millisecond), "ttl", "E", "Message time-to-live (e.g. \"5s\", \"1m\"; 0 = no expiry)")
	cmd.Flags().BoolP("lines", "l", false, "Read stdin line by line, publish each line as a separate message")
	cmd.Flags().Bool("ndjson", false, "Read newline-delimited JSON records from stdin and publish each (lossless import)")
	cmd.Flags().Float64("rate", 0, "Throttle to at most this many messages per second (0 = unlimited)")
	// Accept legacy concatenated spellings (--contenttype) as aliases of the
	// kebab-case names (--content-type).
	cmd.Flags().SetNormalizeFunc(aliasNormalize)

	return cmd
}

func doPublish(cmd *cobra.Command, args []string, backend backends.TopicBackend) error {
	// Parse command flags
	contenttype, _ := cmd.Flags().GetString("content-type")
	correlationid, _ := cmd.Flags().GetString("correlation-id")
	key, _ := cmd.Flags().GetString("key")
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
		return publishNDJSON(backend, args[0], key, limiter)
	}

	// Line-delimited mode
	if lines {
		return publishLines(backend, args[0], key, properties, contenttype, correlationid, messageid, replyto, priority, persistent, ttl, limiter)
	}

	data, err := readCommandMessage(args)
	if err != nil {
		return err
	}

	// Create publish options
	opts := backends.PublishOptions{
		Topic:         args[0],
		Message:       data,
		Key:           key,
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
		if err := backend.Publish(context.Background(), opts); err != nil {
			return err
		}
		if count > 1 {
			log.Verbose("published message %d/%d", i+1, count)
		}
	}

	return nil
}

func publishLines(backend backends.TopicBackend, topic, key string, properties map[string]any, contenttype, correlationid, messageid, replyto string, priority int, persistent bool, ttl int64, limiter *rateLimiter) error {
	sent, err := forEachInputLine(func(line string) error {
		opts := backends.PublishOptions{
			Topic:         topic,
			Message:       []byte(line),
			Key:           key,
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
		if err := backend.Publish(context.Background(), opts); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	log.Verbose("published %d messages", sent)
	return nil
}

// publishNDJSON reads NDJSON message records from stdin and publishes each one,
// restoring the metadata stored in the record (including the partition key). It
// is the import counterpart to `subscribe --ndjson`.
func publishNDJSON(backend backends.TopicBackend, topic, key string, limiter *rateLimiter) error {
	sent, err := forEachRecord(os.Stdin, func(rec messageRecord) error {
		data, err := rec.payload()
		if err != nil {
			return err
		}
		// A key on the record takes precedence; otherwise fall back to --key.
		recordKey := rec.Key
		if recordKey == "" {
			recordKey = key
		}
		limiter.wait()
		return backend.Publish(context.Background(), backends.PublishOptions{
			Topic:         topic,
			Message:       data,
			Key:           recordKey,
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
	log.Verbose("published %d messages", sent)
	return nil
}
