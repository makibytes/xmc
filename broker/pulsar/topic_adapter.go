//go:build pulsar

package pulsar

import (
	"context"
	"errors"
	"fmt"

	pulsar "github.com/apache/pulsar-client-go/pulsar"
	"github.com/makibytes/xmc/broker/backends"
)

// TopicAdapter adapts Pulsar pub/sub to the TopicBackend interface.
type TopicAdapter struct {
	connArgs ConnArguments
	client   pulsar.Client
}

// NewTopicAdapter creates a new Pulsar topic adapter.
func NewTopicAdapter(connArgs ConnArguments) (*TopicAdapter, error) {
	client, err := Connect(connArgs)
	if err != nil {
		return nil, err
	}
	return &TopicAdapter{connArgs: connArgs, client: client}, nil
}

// Publish implements backends.TopicBackend.
func (a *TopicAdapter) Publish(ctx context.Context, opts backends.PublishOptions) error {
	producer, err := a.client.CreateProducer(pulsar.ProducerOptions{
		Topic: opts.Topic,
	})
	if err != nil {
		return fmt.Errorf("creating producer for topic %s: %w", opts.Topic, err)
	}
	defer producer.Close()

	msg := &pulsar.ProducerMessage{
		Payload:    opts.Message,
		Properties: stringifyProps(opts.Properties),
	}
	if opts.Key != "" {
		msg.Key = opts.Key
	}
	if opts.MessageID != "" {
		msg.Properties["message-id"] = opts.MessageID
	}
	if opts.CorrelationID != "" {
		msg.Properties["correlation-id"] = opts.CorrelationID
	}
	if opts.ReplyTo != "" {
		msg.Properties["reply-to"] = opts.ReplyTo
	}
	if opts.ContentType != "" {
		msg.Properties["content-type"] = opts.ContentType
	}

	_, err = producer.Send(ctx, msg)
	return err
}

// Subscribe implements backends.TopicBackend.
// Uses Exclusive subscription by default; Shared if GroupID is set.
func (a *TopicAdapter) Subscribe(ctx context.Context, opts backends.SubscribeOptions) (*backends.Message, error) {
	subType := pulsar.Exclusive
	subName := "xmc-sub"
	if opts.GroupID != "" {
		subType = pulsar.Shared
		subName = opts.GroupID
	}
	if opts.Durable {
		subName = "xmc-durable"
		if opts.GroupID != "" {
			subName = opts.GroupID
		}
	}

	consumer, err := a.client.Subscribe(pulsar.ConsumerOptions{
		Topic:            opts.Topic,
		SubscriptionName: subName,
		Type:             subType,
	})
	if err != nil {
		return nil, fmt.Errorf("subscribing to topic %s: %w", opts.Topic, err)
	}
	defer consumer.Close()

	timeout := receiveTimeout(opts.Timeout, opts.Wait)
	receiveCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	msg, err := consumer.Receive(receiveCtx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, errors.New("no message available")
		}
		return nil, err
	}

	consumer.Ack(msg) //nolint:errcheck
	return pulsarToBackendMessage(msg), nil
}

// Close implements backends.TopicBackend.
func (a *TopicAdapter) Close() error {
	if a.client != nil {
		a.client.Close()
	}
	return nil
}
