//go:build pulsar

package pulsar

import (
	"context"
	"errors"
	"fmt"
	"time"

	pulsar "github.com/apache/pulsar-client-go/pulsar"
	"github.com/makibytes/xmc/broker/backends"
)

const queueSubscription = "xmc-queue"

// QueueAdapter adapts Pulsar to the QueueBackend interface using Shared subscriptions.
type QueueAdapter struct {
	connArgs ConnArguments
	client   pulsar.Client
}

// NewQueueAdapter creates a new Pulsar queue adapter.
func NewQueueAdapter(connArgs ConnArguments) (*QueueAdapter, error) {
	client, err := Connect(connArgs)
	if err != nil {
		return nil, err
	}
	return &QueueAdapter{connArgs: connArgs, client: client}, nil
}

// Send implements backends.QueueBackend.
// Topic: persistent://public/default/{queue}
func (a *QueueAdapter) Send(ctx context.Context, opts backends.SendOptions) error {
	topic := queueTopic(opts.Queue)
	producer, err := a.client.CreateProducer(pulsar.ProducerOptions{
		Topic: topic,
	})
	if err != nil {
		return fmt.Errorf("creating producer for %s: %w", topic, err)
	}
	defer producer.Close()

	msg := &pulsar.ProducerMessage{
		Payload:    opts.Message,
		Properties: stringifyProps(opts.Properties),
	}
	if opts.MessageID != "" {
		msg.Key = opts.MessageID
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
	if opts.TTL > 0 {
		msg.Properties["ttl-ms"] = fmt.Sprintf("%d", opts.TTL)
	}

	_, err = producer.Send(ctx, msg)
	return err
}

// Receive implements backends.QueueBackend.
// Uses Shared subscription for queue semantics (each message delivered to one consumer).
func (a *QueueAdapter) Receive(ctx context.Context, opts backends.ReceiveOptions) (*backends.Message, error) {
	topic := queueTopic(opts.Queue)
	consumer, err := a.client.Subscribe(pulsar.ConsumerOptions{
		Topic:                       topic,
		SubscriptionName:            queueSubscription,
		Type:                        pulsar.Shared,
		SubscriptionInitialPosition: pulsar.SubscriptionPositionEarliest,
		NackRedeliveryDelay:         1 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("subscribing to %s: %w", topic, err)
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

	if opts.Acknowledge {
		consumer.Ack(msg) //nolint:errcheck
	} else {
		consumer.Nack(msg)
	}

	return pulsarToBackendMessage(msg), nil
}

// Close implements backends.QueueBackend.
func (a *QueueAdapter) Close() error {
	if a.client != nil {
		a.client.Close()
	}
	return nil
}

func queueTopic(queue string) string {
	return fmt.Sprintf("persistent://public/default/%s", queue)
}

func receiveTimeout(timeout float32, wait bool) time.Duration {
	if wait {
		return 24 * time.Hour
	}
	if timeout <= 0 {
		return 5 * time.Second
	}
	return time.Duration(float64(timeout) * float64(time.Second))
}

func stringifyProps(props map[string]any) map[string]string {
	result := make(map[string]string, len(props))
	for k, v := range props {
		result[k] = fmt.Sprintf("%v", v)
	}
	return result
}

func pulsarToBackendMessage(msg pulsar.Message) *backends.Message {
	props := make(map[string]any, len(msg.Properties()))
	for k, v := range msg.Properties() {
		props[k] = v
	}
	return &backends.Message{
		Data:          msg.Payload(),
		Properties:    props,
		MessageID:     msg.Key(),
		CorrelationID: msg.Properties()["correlation-id"],
		ReplyTo:       msg.Properties()["reply-to"],
		ContentType:   msg.Properties()["content-type"],
	}
}
