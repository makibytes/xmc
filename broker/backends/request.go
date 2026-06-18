package backends

import (
	"context"
	"errors"
	"fmt"
)

// Request/reply defaults.
const (
	// DefaultReplyQueue is the reply destination used when a request does not
	// specify one.
	DefaultReplyQueue = "xmc.reply"
	// DefaultRequestTimeout is the reply wait used when none is specified.
	DefaultRequestTimeout float32 = 30
)

// ErrReplyMismatch is returned when a reply arrives carrying a correlation id
// that does not match the request's. On brokers without server-side filtering
// this is the signal that a foreign reply was read from a shared reply queue.
var ErrReplyMismatch = errors.New("reply correlation id does not match request")

// RequestSendError wraps a send-phase error so callers can distinguish it from
// a receive-phase error or timeout.
type RequestSendError struct {
	Err error
}

func (e *RequestSendError) Error() string { return "failed to send request: " + e.Err.Error() }
func (e *RequestSendError) Unwrap() error { return e.Err }

// RequestOptions describes a request/reply exchange. The correlation id is the
// portable contract: it is carried on the request and expected to be echoed by
// the responder onto the reply, and it is what the requestor matches on. If it
// is empty it is filled in automatically (see EnsureCorrelationID).
type RequestOptions struct {
	Address       string
	Message       []byte
	Properties    map[string]any
	ContentType   string
	MessageID     string
	CorrelationID string
	ReplyTo       string
	Priority      int
	Persistent    bool
	Timeout       float32 // seconds; <= 0 uses DefaultRequestTimeout
}

// RequestReplyBackend is an optional capability for brokers that implement
// request/reply natively (e.g. Artemis filtering the reply server-side by
// correlation id, IBM MQ matching on CorrelId, NATS using an inbox). Backends
// that do not implement it fall back to the broker-neutral default in Request.
//
// Keeping this optional means request/reply is only offered where it is natural
// for the broker, rather than forcing one messaging pattern onto all of them.
type RequestReplyBackend interface {
	Request(ctx context.Context, opts RequestOptions) (*Message, error)
}

// EnsureCorrelationID fills in opts.CorrelationID when empty and returns the
// effective value. Precedence: an explicit correlation id wins; otherwise the
// message id is reused (the long-standing convention); otherwise a fresh id is
// generated so every request is always correlatable.
func EnsureCorrelationID(opts *RequestOptions) string {
	if opts.CorrelationID == "" {
		if opts.MessageID != "" {
			opts.CorrelationID = opts.MessageID
		} else {
			opts.CorrelationID = newCorrelationID()
		}
	}
	return opts.CorrelationID
}

func newCorrelationID() string {
	return "xmc-" + RandomSuffix() + RandomSuffix()
}

// Request performs a request/reply exchange against qb. If qb implements
// RequestReplyBackend, its native implementation is used; otherwise a
// broker-neutral default sends the request and waits for the reply on the reply
// destination.
//
// The default cannot filter server-side, so on a shared reply queue it relies
// on the requestor having a private reply destination or on the responder
// echoing the correlation id: a reply whose correlation id does not match is
// reported as ErrReplyMismatch rather than returned as if it were ours. A reply
// that carries no correlation id at all is accepted (not every responder echoes
// it). Brokers that need true concurrency-safe matching on a shared queue
// should implement RequestReplyBackend with their native mechanism.
func Request(ctx context.Context, qb QueueBackend, opts RequestOptions) (*Message, error) {
	if rr, ok := qb.(RequestReplyBackend); ok {
		return rr.Request(ctx, opts)
	}
	return defaultRequest(ctx, qb, opts)
}

func defaultRequest(ctx context.Context, qb QueueBackend, opts RequestOptions) (*Message, error) {
	correlationID := EnsureCorrelationID(&opts)
	if opts.ReplyTo == "" {
		opts.ReplyTo = DefaultReplyQueue
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultRequestTimeout
	}

	if err := qb.Send(ctx, SendOptions{
		Queue:         opts.Address,
		Message:       opts.Message,
		Properties:    opts.Properties,
		MessageID:     opts.MessageID,
		CorrelationID: correlationID,
		ReplyTo:       opts.ReplyTo,
		ContentType:   opts.ContentType,
		Priority:      opts.Priority,
		Persistent:    opts.Persistent,
	}); err != nil {
		return nil, &RequestSendError{Err: err}
	}

	msg, err := qb.Receive(ctx, ReceiveOptions{
		Queue:       opts.ReplyTo,
		Timeout:     timeout,
		Acknowledge: true,
		Verbosity:   VerbosityNormal,
	})
	if err != nil {
		return nil, err
	}
	if msg == nil {
		return nil, ErrNoMessageAvailable
	}
	if msg.CorrelationID != "" && msg.CorrelationID != correlationID {
		return nil, fmt.Errorf("%w (expected %q, got %q)", ErrReplyMismatch, correlationID, msg.CorrelationID)
	}
	return msg, nil
}
