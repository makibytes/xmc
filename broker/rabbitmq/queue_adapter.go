//go:build rabbitmq

package rabbitmq

import (
	"context"
	"errors"

	"github.com/Azure/go-amqp"
	"github.com/makibytes/xmc/broker/amqpcommon"
	"github.com/makibytes/xmc/broker/backends"
)

// QueueAdapter adapts RabbitMQ to the QueueBackend interface using direct queue routing
type QueueAdapter struct {
	connArgs   ConnArguments
	connection *amqp.Conn
	session    *amqp.Session
}

// NewQueueAdapter creates a new RabbitMQ queue adapter
func NewQueueAdapter(connArgs ConnArguments) (*QueueAdapter, error) {
	connection, session, err := Connect(connArgs)
	if err != nil {
		return nil, err
	}

	return &QueueAdapter{
		connArgs:   connArgs,
		connection: connection,
		session:    session,
	}, nil
}

// Send implements backends.QueueBackend
func (a *QueueAdapter) Send(ctx context.Context, opts backends.SendOptions) error {
	args := SendArguments{
		Queue:         opts.Queue,
		Message:       opts.Message,
		Properties:    opts.Properties,
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

// Receive implements backends.QueueBackend
func (a *QueueAdapter) Receive(ctx context.Context, opts backends.ReceiveOptions) (*backends.Message, error) {
	args := ReceiveArguments{
		Queue:                     opts.Queue,
		Acknowledge:               opts.Acknowledge,
		Durable:                   false,
		Number:                    1,
		Selector:                  opts.Selector,
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

// Close implements backends.QueueBackend
func (a *QueueAdapter) Close() error {
	if a.session != nil {
		a.session.Close(context.Background())
	}
	if a.connection != nil {
		return a.connection.Close()
	}
	return nil
}
