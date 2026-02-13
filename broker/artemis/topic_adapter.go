//go:build artemis

package artemis

import (
	"context"
	"errors"

	"github.com/Azure/go-amqp"
	"github.com/makibytes/amc/broker/amqpcommon"
	"github.com/makibytes/amc/broker/backends"
)

// TopicAdapter adapts Artemis to the TopicBackend interface
type TopicAdapter struct {
	connArgs   ConnArguments
	connection *amqp.Conn
	session    *amqp.Session
}

// NewTopicAdapter creates a new Artemis topic adapter
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

// Publish implements backends.TopicBackend
func (a *TopicAdapter) Publish(ctx context.Context, opts backends.PublishOptions) error {
	properties := opts.Properties
	if properties == nil {
		properties = make(map[string]any)
	}

	args := SendArguments{
		Address:       opts.Topic,
		Message:       opts.Message,
		Properties:    properties,
		MessageID:     opts.MessageID,
		CorrelationID: opts.CorrelationID,
		ReplyTo:       opts.ReplyTo,
		ContentType:   opts.ContentType,
		Priority:      uint8(opts.Priority),
		Durable:       opts.Persistent,
		Multicast:     true, // Topic = MULTICAST
		TTL:           opts.TTL,
	}

	return SendMessage(ctx, a.session, args)
}

// Subscribe implements backends.TopicBackend
func (a *TopicAdapter) Subscribe(ctx context.Context, opts backends.SubscribeOptions) (*backends.Message, error) {
	args := ReceiveArguments{
		Acknowledge:               true, // Always acknowledge for topics
		Durable:                   false,
		DurableSubscription:       opts.Durable,
		Multicast:                 true, // Topic = MULTICAST
		Number:                    1,
		Queue:                     opts.Topic,
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
