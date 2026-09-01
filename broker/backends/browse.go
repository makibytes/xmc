// Package backends defines the broker-neutral interfaces (QueueBackend,
// TopicBackend, and their optional capability extensions) and shared helpers
// that every broker adapter implements against.
package backends

import (
	"context"
	"errors"
)

// Browse returns a non-destructive forward cursor over qb. If qb implements
// BrowseBackend, its native cursor is used (each Next call advances through
// the queue). Otherwise a stateless fallback is returned: Next just calls
// Receive with Acknowledge forced to false, so — with no cursor to advance —
// it repeats the queue's head message on every call. This mirrors Request's
// native-with-neutral-default pattern; see its doc for the rationale.
func Browse(ctx context.Context, qb QueueBackend, opts ReceiveOptions) (Browser, error) {
	if bb, ok := qb.(BrowseBackend); ok {
		browser, err := bb.Browse(ctx, opts)
		switch {
		case err == nil:
			return browser, nil
		case !errors.Is(err, ErrBrowseUnsupported):
			return nil, err
		}
		// ErrBrowseUnsupported: fall through to the stateless default below.
	}
	return &statelessBrowser{qb: qb, opts: opts}, nil
}

// statelessBrowser implements Browser via repeated Receive(ack=false) calls,
// for backends without a true browse cursor (see Browse).
type statelessBrowser struct {
	qb   QueueBackend
	opts ReceiveOptions
}

func (b *statelessBrowser) Next(ctx context.Context) (*Message, error) {
	opts := b.opts
	opts.Acknowledge = false
	return b.qb.Receive(ctx, opts)
}

func (b *statelessBrowser) Close() error { return nil }
