//go:build artemis

package artemis

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/go-amqp"
	"github.com/makibytes/amc/broker/backends"
	"github.com/makibytes/amc/log"
)

// QueueAdapter adapts Artemis to the QueueBackend interface
type QueueAdapter struct {
	connArgs   ConnArguments
	connection *amqp.Conn
	session    *amqp.Session
}

// NewQueueAdapter creates a new Artemis queue adapter
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
		Address:       opts.Queue,
		Message:       opts.Message,
		Properties:    opts.Properties,
		MessageID:     opts.MessageID,
		CorrelationID: opts.CorrelationID,
		ReplyTo:       opts.ReplyTo,
		ContentType:   opts.ContentType,
		Priority:      uint8(opts.Priority),
		Durable:       opts.Persistent,
		Multicast:     false, // Queue = ANYCAST
	}

	return SendMessage(ctx, a.session, args)
}

// Receive implements backends.QueueBackend
func (a *QueueAdapter) Receive(ctx context.Context, opts backends.ReceiveOptions) (*backends.Message, error) {
	args := ReceiveArguments{
		Acknowledge:               opts.Acknowledge,
		Durable:                   false,
		Multicast:                 false, // Queue = ANYCAST
		Number:                    1,
		Queue:                     opts.Queue,
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

func convertAMQPToBackendMessage(msg *amqp.Message) *backends.Message {
	result := &backends.Message{
		Data:             msg.GetData(),
		Properties:       msg.ApplicationProperties,
		InternalMetadata: make(map[string]any),
	}

	if msg.Properties != nil {
		if msg.Properties.MessageID != nil {
			result.MessageID = fmt.Sprintf("%v", msg.Properties.MessageID)
		}
		if msg.Properties.CorrelationID != nil {
			result.CorrelationID = fmt.Sprintf("%v", msg.Properties.CorrelationID)
		}
		if msg.Properties.ReplyTo != nil {
			result.ReplyTo = *msg.Properties.ReplyTo
		}
		if msg.Properties.ContentType != nil {
			result.ContentType = *msg.Properties.ContentType
		}

		// Add to internal metadata for verbose display
		if log.IsVerbose {
			result.InternalMetadata["Header"] = fmt.Sprintf("%+v", msg.Header)
			result.InternalMetadata["MessageProperties"] = fmt.Sprintf("%+v", msg.Properties)
		}
	}

	if msg.Header != nil {
		result.Priority = int(msg.Header.Priority)
		result.Persistent = msg.Header.Durable
	}

	return result
}