//go:build pulsar

package pulsar

import (
	"context"
	"errors"

	pulsar "github.com/apache/pulsar-client-go/pulsar"
	"github.com/makibytes/xmc/broker/backends"
)

// TopicAdapter adapts Pulsar pub/sub to the TopicBackend interface.
type TopicAdapter struct {
	*clientCache
}

// NewTopicAdapter creates a new Pulsar topic adapter.
func NewTopicAdapter(connArgs ConnArguments) (*TopicAdapter, error) {
	client, err := Connect(connArgs)
	if err != nil {
		return nil, err
	}
	return &TopicAdapter{clientCache: newClientCache(client)}, nil
}

// Publish implements backends.TopicBackend.
func (a *TopicAdapter) Publish(ctx context.Context, opts backends.PublishOptions) error {
	producer, err := a.getProducer(opts.Topic)
	if err != nil {
		return err
	}

	msg := &pulsar.ProducerMessage{
		Payload:    opts.Message,
		Properties: backends.StringifyProps(opts.Properties),
	}
	if opts.Key != "" {
		msg.Key = opts.Key
	}
	if opts.MessageID != "" {
		msg.Properties[backends.PropMessageID] = opts.MessageID
	}
	if opts.CorrelationID != "" {
		msg.Properties[backends.PropCorrelationID] = opts.CorrelationID
	}
	if opts.ReplyTo != "" {
		msg.Properties[backends.PropReplyTo] = opts.ReplyTo
	}
	if opts.ContentType != "" {
		msg.Properties[backends.PropContentType] = opts.ContentType
	}

	_, err = producer.Send(ctx, msg)
	return err
}

// Subscribe implements backends.TopicBackend.
// Uses Exclusive subscription by default; Shared if GroupID is set.
func (a *TopicAdapter) Subscribe(ctx context.Context, opts backends.SubscribeOptions) (*backends.Message, error) {
	subType := pulsar.Exclusive
	subName := "xmc-sub"
	if opts.Durable {
		subName = "xmc-durable"
	}
	if opts.GroupID != "" {
		subType = pulsar.Shared
		subName = opts.GroupID
	}

	consumer, err := a.getConsumer(pulsar.ConsumerOptions{
		Topic:            opts.Topic,
		SubscriptionName: subName,
		Type:             subType,
	})
	if err != nil {
		return nil, err
	}

	timeout := backends.TimeoutDuration(opts.Timeout, opts.Wait)
	receiveCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	msg, err := consumer.Receive(receiveCtx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, backends.ErrNoMessageAvailable
		}
		return nil, err
	}

	consumer.Ack(msg) //nolint:errcheck
	return pulsarToBackendMessage(msg), nil
}

// Close implements backends.TopicBackend.
func (a *TopicAdapter) Close() error {
	a.close()
	return nil
}
