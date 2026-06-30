//go:build pulsar

package pulsar

import (
	"context"
	"errors"
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
	producer, err := a.getProducer(opts.Queue)
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
	consumer, err := a.getConsumer(pulsar.ConsumerOptions{
		Topic:                       opts.Queue,
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

func pulsarToBackendMessage(msg pulsar.Message) *backends.Message {
	rawProps := msg.Properties()
	props := make(map[string]any, len(rawProps))
	for k, v := range rawProps {
		props[k] = v
	}
	corrID, _ := props[backends.PropCorrelationID].(string)
	delete(props, backends.PropCorrelationID)
	replyTo, _ := props[backends.PropReplyTo].(string)
	delete(props, backends.PropReplyTo)
	contentType, _ := props[backends.PropContentType].(string)
	delete(props, backends.PropContentType)
	return &backends.Message{
		Data:          msg.Payload(),
		Properties:    props,
		MessageID:     msg.Key(),
		CorrelationID: corrID,
		ReplyTo:       replyTo,
		ContentType:   contentType,
	}
}
