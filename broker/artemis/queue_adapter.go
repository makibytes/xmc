//go:build artemis

package artemis

import (
	"context"

	"github.com/makibytes/xmc/broker/amqpcommon"
	"github.com/makibytes/xmc/broker/backends"

	"github.com/Azure/go-amqp"
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
	multicast := false // Queue = ANYCAST by default
	if rt, ok := opts.Extra["routing-type"]; ok {
		multicast = rt == "multicast"
	}

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
		Multicast:     multicast,
		TTL:           opts.TTL,
	}

	return SendMessage(ctx, a.session, args)
}

// Receive implements backends.QueueBackend
func (a *QueueAdapter) Receive(ctx context.Context, opts backends.ReceiveOptions) (*backends.Message, error) {
	args := ReceiveArguments{
		Acknowledge: opts.Acknowledge,
		Multicast:   false, // Queue = ANYCAST
		Queue:       opts.Queue,
		Selector:    opts.Selector,
		Timeout:     opts.Timeout,
		Wait:        opts.Wait,
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

// Browse implements backends.BrowseBackend.
// It opens a single AMQP receiver in distribution-mode "copy" so that
// successive calls to Next advance through the queue without consuming messages.
func (a *QueueAdapter) Browse(ctx context.Context, opts backends.ReceiveOptions) (backends.Browser, error) {
	b, err := amqpcommon.NewQueueBrowser(a.session, amqpcommon.ReceiveOptions{
		Queue:              opts.Queue,
		Timeout:            opts.Timeout,
		Selector:           opts.Selector,
		SourceCapabilities: []string{"queue"}, // ANYCAST routing for Artemis queues
	})
	if err != nil {
		return nil, err
	}
	return &queueBrowser{inner: b, verbosity: opts.Verbosity}, nil
}

// queueBrowser wraps amqpcommon.QueueBrowser and implements backends.Browser,
// converting raw AMQP messages to the broker-agnostic backends.Message type.
type queueBrowser struct {
	inner     *amqpcommon.QueueBrowser
	verbosity backends.Verbosity
}

func (br *queueBrowser) Next(ctx context.Context) (*backends.Message, error) {
	msg, err := br.inner.Next(ctx)
	if err != nil {
		return nil, err
	}
	if msg == nil {
		return nil, backends.ErrNoMessageAvailable
	}
	return amqpcommon.ConvertAMQPToBackendMessage(msg, br.verbosity >= backends.VerbosityVerbose), nil
}

func (br *queueBrowser) Close() error {
	return br.inner.Close()
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
