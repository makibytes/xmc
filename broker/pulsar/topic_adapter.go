//go:build pulsar

package pulsar

import (
	"context"
	"errors"
	"strconv"

	pulsar "github.com/apache/pulsar-client-go/pulsar"
	"github.com/makibytes/xmc/broker/backends"
)

// TopicAdapter adapts Pulsar pub/sub to the TopicBackend interface.
type TopicAdapter struct {
	*clientCache
	ephemeralSub string // per-adapter name for group-less, non-durable subscriptions
}

// NewTopicAdapter creates a new Pulsar topic adapter.
func NewTopicAdapter(connArgs ConnArguments) (*TopicAdapter, error) {
	client, err := Connect(connArgs)
	if err != nil {
		return nil, err
	}
	return &TopicAdapter{
		clientCache:  newClientCache(client),
		ephemeralSub: "xmc-sub-" + backends.RandomSuffix(),
	}, nil
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
	if opts.TTL > 0 {
		msg.Properties[propTTLMs] = strconv.FormatInt(opts.TTL, 10)
	}

	_, err = producer.Send(ctx, msg)
	return err
}

// Subscribe implements backends.TopicBackend.
// GroupID selects a Shared subscription named after the group (competing
// consumers); --durable an Exclusive durable one. Without either, the
// subscription is NonDurable with a per-adapter unique name, so no cursor is
// left behind on the topic and concurrent group-less subscribers don't
// collide on an Exclusive subscription name.
func (a *TopicAdapter) Subscribe(ctx context.Context, opts backends.SubscribeOptions) (*backends.Message, error) {
	subType := pulsar.Exclusive
	subMode := pulsar.Durable
	var subName string
	switch {
	case opts.GroupID != "":
		subType = pulsar.Shared
		subName = opts.GroupID
	case opts.Durable:
		subName = "xmc-durable"
	default:
		subName = a.ephemeralSub
		subMode = pulsar.NonDurable
	}

	consumer, err := a.getConsumer(pulsar.ConsumerOptions{
		Topic:            opts.Topic,
		SubscriptionName: subName,
		Type:             subType,
		SubscriptionMode: subMode,
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
