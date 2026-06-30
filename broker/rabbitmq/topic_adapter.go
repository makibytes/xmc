//go:build rabbitmq

package rabbitmq

import (
	"context"
	"strings"

	"github.com/Azure/go-amqp"
	"github.com/makibytes/xmc/broker/amqpcommon"
	"github.com/makibytes/xmc/broker/backends"
)

// TopicAdapter adapts RabbitMQ to the TopicBackend interface using exchange-based routing
type TopicAdapter struct {
	connArgs   ConnArguments
	connection *amqp.Conn
	session    *amqp.Session
}

// NewTopicAdapter creates a new RabbitMQ topic adapter
func NewTopicAdapter(connArgs ConnArguments) (*TopicAdapter, error) {
	connection, session, err := Connect(connArgs)
	if err != nil {
		return nil, err
	}

	return &TopicAdapter{
		connArgs:   connArgs,
		connection: connection,
		session:    session,
	}, nil
}

// exchangeAddress returns the AMQP 1.0 v2 address for an exchange on RabbitMQ 4.x.
// Names already using an absolute path prefix are returned as-is.
// A bare name is treated as an exchange: /exchanges/<name>.
// When key is non-empty it is appended as the routing key: /exchanges/<name>/<key>.
func exchangeAddress(name, key string) string {
	if strings.HasPrefix(name, "/") {
		return name
	}
	if key != "" {
		return "/exchanges/" + name + "/" + key
	}
	return "/exchanges/" + name
}

// Publish implements backends.TopicBackend
func (a *TopicAdapter) Publish(ctx context.Context, opts backends.PublishOptions) error {
	properties := opts.Properties
	if properties == nil {
		properties = make(map[string]any)
	}

	args := SendArguments{
		Queue:         exchangeAddress(opts.Topic, opts.Key),
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
		Queue:               exchangeAddress(opts.Topic, ""),
		Acknowledge:         true,
		Durable:             false,
		DurableSubscription: opts.Durable,
		Number:              1,
		Selector:            opts.Selector,
		SubscriptionName:    opts.GroupID,
		Timeout:             opts.Timeout,
		Wait:                opts.Wait,
	}

	message, err := ReceiveMessage(ctx, a.session, args)
	if err != nil {
		return nil, err
	}
	if message == nil {
		return nil, backends.ErrNoMessageAvailable
	}

	return amqpcommon.ConvertAMQPToBackendMessage(message, opts.Verbosity >= backends.VerbosityVerbose), nil
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
