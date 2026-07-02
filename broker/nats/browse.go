//go:build nats

package nats

import (
	"context"
	"errors"

	natsclient "github.com/nats-io/nats.go"

	"github.com/makibytes/xmc/broker/backends"
)

// Browse implements backends.BrowseBackend. JetStream allows reading a stored
// message by sequence number without consuming it, so the browser walks the
// stream's current sequence range with GetMsg — a true non-destructive cursor
// (unlike Receive with Acknowledge=false, which NAKs for redelivery and keeps
// yielding the same head message).
func (a *QueueAdapter) Browse(ctx context.Context, opts backends.ReceiveOptions) (backends.Browser, error) {
	sn := streamName(opts.Queue)
	if opts.Extra != nil && opts.Extra["stream"] != "" {
		sn = opts.Extra["stream"]
	}
	if _, err := a.ensureStreamWithName(sn, queueSubject(opts.Queue)); err != nil {
		return nil, err
	}

	info, err := a.js.StreamInfo(sn)
	if err != nil {
		return nil, err
	}
	return &queueBrowser{
		js:     a.js,
		stream: sn,
		seq:    info.State.FirstSeq,
		last:   info.State.LastSeq,
	}, nil
}

// queueBrowser is a forward-only cursor over a stream's sequence range as it
// was when Browse was called; messages stored later are not picked up.
type queueBrowser struct {
	js     natsclient.JetStreamContext
	stream string
	seq    uint64 // next sequence to read
	last   uint64 // final sequence of the snapshot
}

func (b *queueBrowser) Next(_ context.Context) (*backends.Message, error) {
	for b.seq != 0 && b.seq <= b.last {
		raw, err := b.js.GetMsg(b.stream, b.seq)
		b.seq++
		if err != nil {
			// Sequence gaps are normal: consumed work-queue messages leave
			// holes in the range.
			if errors.Is(err, natsclient.ErrMsgNotFound) {
				continue
			}
			return nil, err
		}
		return headersToBackendMessage(raw.Data, raw.Header), nil
	}
	return nil, backends.ErrNoMessageAvailable
}

func (b *queueBrowser) Close() error { return nil }
