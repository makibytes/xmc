//go:build azure

package azuresb

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"

	"github.com/makibytes/xmc/broker/backends"
)

// Browse implements backends.BrowseBackend with Service Bus's native peek
// cursor: successive PeekMessages calls on the same receiver advance through
// the queue without locking or removing messages. The stateless peek path
// creates a fresh receiver per call and therefore always re-reads the head,
// which made "peek -n 0" repeat the first message forever.
func (a *QueueAdapter) Browse(ctx context.Context, opts backends.ReceiveOptions) (backends.Browser, error) {
	if err := a.ensureQueueCached(ctx, opts.Queue); err != nil {
		return nil, err
	}
	recv, err := a.senderCache.client.NewReceiverForQueue(opts.Queue, nil)
	if err != nil {
		return nil, fmt.Errorf("creating receiver for queue %s: %w", opts.Queue, err)
	}
	return &queueBrowser{
		recv:    recv,
		queue:   opts.Queue,
		timeout: backends.TimeoutDuration(opts.Timeout, opts.Wait),
	}, nil
}

type queueBrowser struct {
	recv    *azservicebus.Receiver
	queue   string
	timeout time.Duration
}

func (b *queueBrowser) Next(ctx context.Context) (*backends.Message, error) {
	peekCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()

	msgs, err := b.recv.PeekMessages(peekCtx, 1, nil)
	if err != nil {
		if peekCtx.Err() != nil {
			return nil, backends.ErrNoMessageAvailable
		}
		return nil, fmt.Errorf("browsing queue %s: %w", b.queue, err)
	}
	if len(msgs) == 0 {
		return nil, backends.ErrNoMessageAvailable
	}
	return sbToBackendMessage(msgs[0]), nil
}

func (b *queueBrowser) Close() error {
	return b.recv.Close(context.Background())
}
