//go:build rabbitmq

package rabbitmq

import (
	"context"
	"errors"

	"github.com/Azure/go-amqp"
	"github.com/makibytes/amc/broker/backends"
)

// TopicAdapter adapts RabbitMQ to the TopicBackend interface
// Uses exchange-based routing for pub/sub
type TopicAdapter struct {
	connArgs   ConnArguments
	connection *amqp.Conn
	session    *amqp.Session
	exchange   string // Exchange name for topic operations
}

// NewTopicAdapter creates a new RabbitMQ topic adapter
// The exchange name should be provided (e.g., "amq.topic", "amq.fanout", or custom exchange)
func NewTopicAdapter(connArgs ConnArguments, exchange string) (*TopicAdapter, error) {
	connection, session, err := Connect(connArgs)
	if err != nil {
		return nil, err
	}

	// Default to "amq.topic" if no exchange specified
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

	// Use opts.Key as routing key for RabbitMQ topic exchange
	routingKey := opts.Key
	if routingKey == "" {
		// If no key provided, use the topic name as routing key
		routingKey = opts.Topic
	}

	args := SendArguments{
		Queue:         "",                // Not used for exchanges
		Exchange:      a.exchange,        // Use configured exchange
		RoutingKey:    routingKey,        // Routing key for topic routing
		Message:       opts.Message,
		Properties:    properties,
		MessageID:     opts.MessageID,
		CorrelationID: opts.CorrelationID,
		ContentType:   opts.ContentType,
		Priority:      4, // Default priority
		Durable:       false,
	}

	return SendMessage(ctx, a.session, args)
}

// Subscribe implements backends.TopicBackend
func (a *TopicAdapter) Subscribe(ctx context.Context, opts backends.SubscribeOptions) (*backends.Message, error) {
	// For RabbitMQ, the topic name is the queue name
	// The queue should be bound to the exchange externally
	args := ReceiveArguments{
		Queue:                     opts.Topic,
		Acknowledge:               true, // Always acknowledge for topics
		Durable:                   false,
		Number:                    1,
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

	return convertAMQPToBackendMessage(message), nil
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