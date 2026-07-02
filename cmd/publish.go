package cmd

import (
	"context"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/spf13/cobra"
)

// NewPublishCommand creates a publish command for topic-based brokers.
// When resolver is non-nil, it maps the positional <to> (and optionally
// -e/-q flags when exchRouting is true) into a destination.
func NewPublishCommand(backend backends.TopicBackend, resolver TargetResolver, produceExtra func(*cobra.Command) map[string]string, exchRouting ...bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "publish <topic> [message]",
		Short: "Publish a message to a topic",
		RunE: func(cmd *cobra.Command, args []string) error {
			return doPublish(cmd, args, backend, resolver, produceExtra)
		},
	}

	registerProduceFlags(cmd)
	cmd.Flags().StringP("key", "K", "", "Message key for partitioning")

	hasExchRouting := len(exchRouting) > 0 && exchRouting[0]
	if hasExchRouting {
		cmd.Use = "publish [-e <exchange> [--routing-key <key>] | -q <queue>] [message]"
		cmd.Flags().StringP("exchange", "e", "", "Exchange to publish to (default: amq.topic)")
		cmd.Flags().String("routing-key", "", "Routing key for the exchange (omit for fanout/headers)")
		cmd.Flags().StringP("queue", "q", "", "Queue to publish to (AMQP 1.0 v2: /queues/<name>)")
		cmd.Args = cobra.MaximumNArgs(2)
	} else if resolver != nil {
		cmd.Args = cobra.MinimumNArgs(1)
	} else {
		cmd.Args = cobra.MinimumNArgs(1)
	}

	return cmd
}

func doPublish(cmd *cobra.Command, args []string, backend backends.TopicBackend, resolver TargetResolver, extraFn func(*cobra.Command) map[string]string) error {
	pf, err := parseProduceFlags(cmd)
	if err != nil {
		return err
	}

	topic, msgArgs, err := resolveProduceTarget(cmd, args, resolver, true)
	if err != nil {
		return err
	}

	key, _ := cmd.Flags().GetString("key")

	var extra map[string]string
	if extraFn != nil {
		extra = extraFn(cmd)
	}

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
			Extra:         extra,
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
			// See cmd/send.go's emitRecord: messageRecord has no TTL field, so
			// --ndjson publishes fall back to the --ttl flag as a per-batch default.
			TTL: pf.ttl,
		})
	}

	// Reconstruct args so that readCommandMessage sees args[1] as the message.
	runArgs := append([]string{topic}, msgArgs...)
	return runProduce(cmd.Context(), cmd.InOrStdin(), runArgs, pf, emit, emitRecord, "published")
}
