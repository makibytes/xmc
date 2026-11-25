package backends

import (
	"context"
	"errors"
)

// ErrNoMessageAvailable is returned by a non-blocking receive/browse when there is no message to return.
var ErrNoMessageAvailable = errors.New("no message available")

// IsNoMessage reports whether err means "the source was empty this read":
// either the explicit sentinel or a bare per-call wait deadline. The AMQP
// backends (Artemis, RabbitMQ) surface an empty queue as the raw
// context.DeadlineExceeded of their own wait deadline rather than
// ErrNoMessageAvailable, so every caller that polls has to treat both the same.
// A caller whose own context ended must check ctx.Err() first: this cannot tell
// a poll timeout from the caller's own deadline.
func IsNoMessage(err error) bool {
	return errors.Is(err, ErrNoMessageAvailable) || errors.Is(err, context.DeadlineExceeded)
}

// ErrBrowseUnsupported is returned by BrowseBackend.Browse when the backend
// does not support stateful browsing. Callers should fall back to the plain
// Receive loop when they see this error.
var ErrBrowseUnsupported = errors.New("browse not supported by this backend")
