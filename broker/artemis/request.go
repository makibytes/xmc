//go:build artemis

package artemis

import (
	"context"
	"fmt"
	"strings"

	"github.com/makibytes/xmc/broker/backends"
)

// correlationSelectorField is the selector identifier Artemis matches against a
// message's correlation id. For JMS- and AMQP-produced messages Artemis exposes
// the correlation id under JMSCorrelationID in selector expressions.
//
// NOTE: AMQP-native correlation-id selector semantics can be version-sensitive;
// this is the field to adjust if server-side matching ever misses on a live
// broker. It is isolated here so the change is a one-liner.
const correlationSelectorField = "JMSCorrelationID"

// Request implements backends.RequestReplyBackend natively for Artemis. It sends
// the request carrying a correlation id, then consumes the reply filtered
// server-side by that correlation id via a JMS selector. Because the broker does
// the matching, a single shared reply address is concurrency-safe: simultaneous
// requests never read each other's replies, and no per-request temporary queue
// is needed.
func (a *QueueAdapter) Request(ctx context.Context, opts backends.RequestOptions) (*backends.Message, error) {
	correlationID := backends.EnsureCorrelationID(&opts)

	replyTo := opts.ReplyTo
	if replyTo == "" {
		replyTo = backends.DefaultReplyQueue
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = backends.DefaultRequestTimeout
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
		Selector:    correlationSelector(correlationID),
	})
	if err != nil {
		return nil, err
	}
	if msg == nil {
		return nil, backends.ErrNoMessageAvailable
	}
	return msg, nil
}

// correlationSelector builds a JMS selector that matches a single correlation
// id. Single quotes in the id are escaped per JMS string-literal rules.
func correlationSelector(id string) string {
	return fmt.Sprintf("%s='%s'", correlationSelectorField, strings.ReplaceAll(id, "'", "''"))
}
