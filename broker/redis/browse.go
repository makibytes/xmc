//go:build redis

package redis

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/makibytes/xmc/broker/backends"
)

// Browse implements backends.BrowseBackend. Redis Streams are naturally
// browsable: XRANGE reads entries by ID without touching the consumer group,
// so the cursor walks the whole stream non-destructively (the stateless peek
// path always re-reads the first entry, which made "peek -n 0" repeat it
// forever).
func (a *QueueAdapter) Browse(ctx context.Context, opts backends.ReceiveOptions) (backends.Browser, error) {
	return &queueBrowser{client: a.client, key: opts.Queue, cursor: "-"}, nil
}

// queueBrowser is a forward-only cursor over a stream. cursor holds the next
// XRANGE start: "-" initially, then "(<last-id>" (exclusive, Redis 6.2+ — the
// same baseline go-redis v9 targets) after each entry.
type queueBrowser struct {
	client *redis.Client
	key    string
	cursor string
}

func (b *queueBrowser) Next(ctx context.Context) (*backends.Message, error) {
	entries, err := b.client.XRangeN(ctx, b.key, b.cursor, "+", 1).Result()
	if err != nil {
		return nil, fmt.Errorf("browsing queue %s: %w", b.key, err)
	}
	if len(entries) == 0 {
		return nil, backends.ErrNoMessageAvailable
	}
	entry := entries[0]
	b.cursor = "(" + entry.ID
	return streamToMessage(entry.ID, entry.Values), nil
}

func (b *queueBrowser) Close() error { return nil }
