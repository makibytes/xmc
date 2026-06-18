package cmd

import (
	"context"

	"github.com/makibytes/xmc/broker/backends"
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

	registerProduceFlags(cmd)
	cmd.Flags().StringP("key", "K", "", "Message key for partitioning")

	return cmd
}

func doPublish(cmd *cobra.Command, args []string, backend backends.TopicBackend) error {
	pf, err := parseProduceFlags(cmd)
	if err != nil {
		return err
	}

	topic := args[0]
	key, _ := cmd.Flags().GetString("key")

	emit := func(ctx context.Context, data []byte) error {
		return backend.Publish(ctx, backends.PublishOptions{
			Topic:         topic,
			Message:       data,
			Key:           key,
			Properties:    pf.properties,
			MessageID:     pf.messageID,
			CorrelationID: pf.correlationID,
			ReplyTo:       pf.replyTo,
			ContentType:   pf.contentType,
			Priority:      pf.priority,
			Persistent:    pf.persistent,
			TTL:           pf.ttl,
		})
	}

	emitRecord := func(ctx context.Context, rec messageRecord) error {
		data, err := rec.payload()
		if err != nil {
			return err
		}
		recordKey := rec.Key
		if recordKey == "" {
			recordKey = key
		}
		return backend.Publish(ctx, backends.PublishOptions{
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
	}

	return runProduce(cmd, args, pf, emit, emitRecord, "published")
}
