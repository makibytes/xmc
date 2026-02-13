//go:build rabbitmq

package rabbitmq

import (
	"context"
	"errors"

	"github.com/Azure/go-amqp"
	"github.com/makibytes/xmc/broker/amqpcommon"
	"github.com/makibytes/xmc/broker/backends"
)

// TopicAdapter adapts RabbitMQ to the TopicBackend interface using exchange-based routing
type TopicAdapter struct {
	connArgs   ConnArguments
	connection *amqp.Conn
	session    *amqp.Session
	exchange   string
}

// NewTopicAdapter creates a new RabbitMQ topic adapter
func NewTopicAdapter(connArgs ConnArguments, exchange string) (*TopicAdapter, error) {
	connection, session, err := Connect(connArgs)
	if err != nil {
		return nil, err
	}

	if exchange == "" {
		exchange = "amq.topic"
	}

	return &TopicAdapter{
		connArgs:   connArgs,
		connection: connection,
		session:    session,
		exchange:   exchange,
	}, nil
}

// Publish implements backends.TopicBackend
func (a *TopicAdapter) Publish(ctx context.Context, opts backends.PublishOptions) error {
	properties := opts.Properties
	if properties == nil {
		properties = make(map[string]any)
	}

	routingKey := opts.Key
	if routingKey == "" {
		routingKey = opts.Topic
	}

	args := SendArguments{
		Exchange:      a.exchange,
		RoutingKey:    routingKey,
		Message:       opts.Message,
		Properties:    properties,
		MessageID:     opts.MessageID,
		CorrelationID: opts.CorrelationID,
		ReplyTo:       opts.ReplyTo,
		ContentType:   opts.ContentType,
		Priority:      uint8(opts.Priority),
		Durable:       opts.Persistent,
		TTL:           opts.TTL,
	}

	return SendMessage(ctx, a.session, args)
}

// Subscribe implements backends.TopicBackend
func (a *TopicAdapter) Subscribe(ctx context.Context, opts backends.SubscribeOptions) (*backends.Message, error) {
	args := ReceiveArguments{
		Queue:                     opts.Topic,
		Acknowledge:               true,
		Durable:                   false,
		DurableSubscription:       opts.Durable,
		Number:                    1,
		Selector:                  opts.Selector,
		SubscriptionName:          opts.GroupID,
		Timeout:                   opts.Timeout,
		Wait:                      opts.Wait,
		WithHeaderAndProperties:   opts.WithHeaderAndProperties,
		WithApplicationProperties: opts.WithApplicationProperties,
	}

	message, err := ReceiveMessage(a.session, args)
	if err != nil {
		return nil, err
	}
	if message == nil {
		return nil, errors.New("no message available")
	}

	return amqpcommon.ConvertAMQPToBackendMessage(message), nil
}

// Close implements backends.TopicBackend
func (a *TopicAdapter) Close() error {
	if a.session != nil {
		a.session.Close(context.Background())
	}
	if a.connection != nil {
		return a.connection.Close()
	}
	return nil
}
