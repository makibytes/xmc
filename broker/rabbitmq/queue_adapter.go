//go:build rabbitmq

package rabbitmq

import (
	"context"
	"errors"
	"strings"

	"github.com/Azure/go-amqp"
	"github.com/makibytes/xmc/broker/amqpcommon"
	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/log"
)

// queueAddress returns the AMQP 1.0 v2 address for a queue on RabbitMQ 4.x.
// Names already using an absolute path prefix are returned as-is.
func queueAddress(name string) string {
	if strings.HasPrefix(name, "/") {
		return name
	}
	return "/queues/" + escapeName(name)
}

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
		Queue:         queueAddress(opts.Queue),
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
		Queue:       queueAddress(opts.Queue),
		Acknowledge: opts.Acknowledge,
		Selector:    opts.Selector,
		Timeout:     opts.Timeout,
		Wait:        opts.Wait,
	}

	message, err := ReceiveMessage(ctx, a.session, args)
	if err != nil {
		if !opts.Wait && errors.Is(err, context.DeadlineExceeded) {
			return nil, backends.ErrNoMessageAvailable
		}
		return nil, err
	}
	if message == nil {
		return nil, backends.ErrNoMessageAvailable
	}

	return amqpcommon.ConvertAMQPToBackendMessage(message, opts.Verbosity >= backends.VerbosityVerbose), nil
}

// Browse implements backends.BrowseBackend using the RabbitMQ Management API.
// It POSTs to /api/queues/%2F/{queue}/get with ackmode=ack_requeue_true to
// fetch up to 10 000 messages non-destructively, then returns an in-memory
// cursor over them. Falls back to backends.ErrBrowseUnsupported if the
// Management API is unavailable or fails (e.g. management plugin not loaded).
func (a *QueueAdapter) Browse(ctx context.Context, opts backends.ReceiveOptions) (backends.Browser, error) {
	if opts.Selector != "" {
		// The Management API's get endpoint cannot filter by selector; fall
		// back to the plain receive loop, which applies the selector filter.
		return nil, backends.ErrBrowseUnsupported
	}
	msgs, err := peekQueueMessages(ManagementArgs{
		Server:   a.connArgs.Server,
		User:     a.connArgs.User,
		Password: a.connArgs.Password,
	}, opts.Queue, 10000)
	if err != nil {
		log.Verbose("RabbitMQ browse via management API failed: %s — falling through to receive loop", err)
		return nil, backends.ErrBrowseUnsupported
	}
	return &mgmtBrowser{messages: msgs}, nil
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
