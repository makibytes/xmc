package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/makibytes/amc/broker/backends"
	"github.com/makibytes/amc/log"
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

	cmd.Flags().StringP("contenttype", "T", "text/plain", "MIME type of message data")
	cmd.Flags().StringP("correlationid", "C", "", "Correlation ID for request/response")
	cmd.Flags().StringP("key", "K", "", "Message key for partitioning")
	cmd.Flags().StringP("messageid", "I", "", "Message ID")
	cmd.Flags().IntP("priority", "Y", 4, "Priority of the message (0-9)")
	cmd.Flags().BoolP("persistent", "d", false, "Make message persistent")
	cmd.Flags().StringP("replyto", "R", "", "Reply to address for request/response")
	cmd.Flags().StringSliceP("property", "P", []string{}, "Message properties in key=value format")
	cmd.Flags().IntP("count", "n", 1, "Number of times to publish the message")
	cmd.Flags().Int64P("ttl", "E", 0, "Message time-to-live in milliseconds (0 = no expiry)")
	cmd.Flags().BoolP("lines", "l", false, "Read stdin line by line, publish each line as a separate message")

	return cmd
}

func doPublish(cmd *cobra.Command, args []string, backend backends.TopicBackend) error {
	// Parse command flags
	contenttype, _ := cmd.Flags().GetString("contenttype")
	correlationid, _ := cmd.Flags().GetString("correlationid")
	key, _ := cmd.Flags().GetString("key")
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

	// Line-delimited mode
	if lines {
		return publishLines(backend, args[0], key, properties, contenttype, correlationid, messageid, replyto, priority, persistent, ttl)
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
		if err := backend.Publish(context.Background(), opts); err != nil {
			return err
		}
		if count > 1 {
			log.Verbose("published message %d/%d", i+1, count)
		}
	}

	return nil
}

func publishLines(backend backends.TopicBackend, topic, key string, properties map[string]any, contenttype, correlationid, messageid, replyto string, priority int, persistent bool, ttl int64) error {
	scanner := bufio.NewScanner(os.Stdin)
	sent := 0
	for scanner.Scan() {
		line := scanner.Text()
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
		if err := backend.Publish(context.Background(), opts); err != nil {
			return err
		}
		sent++
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading stdin: %w", err)
	}
	log.Verbose("published %d messages", sent)
	return nil
}
