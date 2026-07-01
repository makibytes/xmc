//go:build nats

package nats

import (
	"context"
	"fmt"

	"github.com/makibytes/xmc/broker/backends"
)

// Request implements backends.RequestReplyBackend natively for NATS JetStream.
// JetStream has no server-side selectors, so a shared reply queue cannot be
// filtered by correlation id the way Artemis does it. Concurrency safety comes
// from a private reply queue instead: when the caller does not name one, a
// per-request queue is created and its backing stream (plus the cached pull
// consumer) is deleted again after the exchange.
func (a *QueueAdapter) Request(ctx context.Context, opts backends.RequestOptions) (*backends.Message, error) {
	correlationID := backends.EnsureCorrelationID(&opts)

	replyTo := opts.ReplyTo
	private := replyTo == ""
	if private {
		replyTo = "xmc-reply-" + backends.RandomSuffix()
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = backends.DefaultRequestTimeout
	}

	// Create the reply stream before sending the request: if the responder
	// answers before the requestor is ready, the reply must not be dropped
	// for lack of a stream routing the reply subject.
	if err := a.ensureStreamWithName(streamName(replyTo), queueSubject(replyTo)); err != nil {
		return nil, &backends.RequestSendError{Err: err}
	}
	if private {
		defer a.dropReplyQueue(replyTo)
	}

	if err := a.Send(ctx, backends.SendOptions{
		Queue:         opts.Address,
		Message:       opts.Message,
		Properties:    opts.Properties,
		MessageID:     opts.MessageID,
		CorrelationID: correlationID,
		ReplyTo:       replyTo,
		ContentType:   opts.ContentType,
		Priority:      opts.Priority,
		Persistent:    opts.Persistent,
	}); err != nil {
		return nil, &backends.RequestSendError{Err: err}
	}

	msg, err := a.Receive(ctx, backends.ReceiveOptions{
		Queue:       replyTo,
		Timeout:     timeout,
		Acknowledge: true,
		Verbosity:   backends.VerbosityNormal,
	})
	if err != nil {
		return nil, err
	}
	if msg == nil {
		return nil, backends.ErrNoMessageAvailable
	}
	// On a private reply queue a mismatch cannot happen; on a caller-named
	// (potentially shared) one this mirrors the broker-neutral default.
	if msg.CorrelationID != "" && msg.CorrelationID != correlationID {
		return nil, fmt.Errorf("%w (expected %q, got %q)", backends.ErrReplyMismatch, correlationID, msg.CorrelationID)
	}
	return msg, nil
}

// dropReplyQueue removes a per-request reply queue: the cached pull consumer,
// the backing stream, and the ensured-stream memo, so a later queue with the
// same name starts clean.
func (a *QueueAdapter) dropReplyQueue(queue string) {
	if sub, ok := a.consumers[queue]; ok {
		sub.Unsubscribe() //nolint:errcheck
		delete(a.consumers, queue)
	}
	a.js.DeleteStream(streamName(queue)) //nolint:errcheck
	delete(a.ensured, streamName(queue))
}
