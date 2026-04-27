//go:build pulsar

package pulsar

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	pulsar "github.com/apache/pulsar-client-go/pulsar"
	"github.com/makibytes/xmc/broker/backends"
)

const (
	queueSubscription = "xmc-queue"
	propTTLMs         = "ttl-ms"
)

// QueueAdapter adapts Pulsar to the QueueBackend interface using Shared subscriptions.
type QueueAdapter struct {
	*clientCache
}

// NewQueueAdapter creates a new Pulsar queue adapter.
func NewQueueAdapter(connArgs ConnArguments) (*QueueAdapter, error) {
	client, err := Connect(connArgs)
	if err != nil {
		return nil, err
	}
	return &QueueAdapter{clientCache: newClientCache(client)}, nil
}

// Send implements backends.QueueBackend.
func (a *QueueAdapter) Send(ctx context.Context, opts backends.SendOptions) error {
	topic := queueTopic(opts.Queue)
	producer, err := a.getProducer(topic)
	if err != nil {
		return err
	}

	msg := &pulsar.ProducerMessage{
		Payload:    opts.Message,
		Properties: backends.StringifyProps(opts.Properties),
	}
	if opts.MessageID != "" {
		msg.Key = opts.MessageID
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

// Receive implements backends.QueueBackend.
// Uses Shared subscription for queue semantics (each message delivered to one consumer).
func (a *QueueAdapter) Receive(ctx context.Context, opts backends.ReceiveOptions) (*backends.Message, error) {
	topic := queueTopic(opts.Queue)
	consumer, err := a.getConsumer(pulsar.ConsumerOptions{
		Topic:                       topic,
		SubscriptionName:            queueSubscription,
		Type:                        pulsar.Shared,
		SubscriptionInitialPosition: pulsar.SubscriptionPositionEarliest,
		NackRedeliveryDelay:         1 * time.Second,
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

	if opts.Acknowledge {
		consumer.Ack(msg) //nolint:errcheck
	} else {
		consumer.Nack(msg)
	}

	return pulsarToBackendMessage(msg), nil
}

// Close implements backends.QueueBackend.
func (a *QueueAdapter) Close() error {
	a.close()
	return nil
}

func queueTopic(queue string) string {
	return fmt.Sprintf("persistent://public/default/%s", queue)
}

func pulsarToBackendMessage(msg pulsar.Message) *backends.Message {
	rawProps := msg.Properties()
	props := make(map[string]any, len(rawProps))
	for k, v := range rawProps {
		props[k] = v
	}
	return &backends.Message{
		Data:          msg.Payload(),
		Properties:    props,
		MessageID:     msg.Key(),
		CorrelationID: rawProps[backends.PropCorrelationID],
		ReplyTo:       rawProps[backends.PropReplyTo],
		ContentType:   rawProps[backends.PropContentType],
	}
}
