//go:build azure

package azuresb

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus/admin"

	"github.com/makibytes/xmc/broker/backends"
)

type QueueAdapter struct {
	senderCache
	adm *admin.Client
}

func NewQueueAdapter(args ConnArguments) (*QueueAdapter, error) {
	client, err := Connect(args)
	if err != nil {
		return nil, err
	}
	adm, err := AdminClient(args)
	if err != nil {
		client.Close(context.Background()) //nolint:errcheck
		return nil, err
	}
	return &QueueAdapter{
		senderCache: newSenderCache(client),
		adm:         adm,
	}, nil
}

func (a *QueueAdapter) Send(ctx context.Context, opts backends.SendOptions) error {
	if err := ensureQueue(ctx, a.adm, opts.Queue); err != nil {
		return err
	}

	sender, err := a.getSender(opts.Queue)
	if err != nil {
		return err
	}

	msg := toSBMessage(opts.Message, opts.Properties,
		opts.MessageID, opts.CorrelationID, opts.ReplyTo, opts.ContentType, opts.TTL)

	return sender.SendMessage(ctx, msg, nil)
}

func (a *QueueAdapter) Receive(ctx context.Context, opts backends.ReceiveOptions) (*backends.Message, error) {
	if err := ensureQueue(ctx, a.adm, opts.Queue); err != nil {
		return nil, err
	}

	if !opts.Acknowledge {
		return a.peek(ctx, opts)
	}

	recv, err := a.senderCache.client.NewReceiverForQueue(opts.Queue, nil)
	if err != nil {
		return nil, fmt.Errorf("creating receiver for queue %s: %w", opts.Queue, err)
	}
	defer recv.Close(ctx) //nolint:errcheck

	timeout := backends.TimeoutDuration(opts.Timeout, opts.Wait)
	receiveCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	msgs, err := recv.ReceiveMessages(receiveCtx, 1, nil)
	if err != nil {
		if receiveCtx.Err() != nil {
			return nil, backends.ErrNoMessageAvailable
		}
		return nil, fmt.Errorf("receiving from queue %s: %w", opts.Queue, err)
	}
	if len(msgs) == 0 {
		return nil, backends.ErrNoMessageAvailable
	}

	if err := recv.CompleteMessage(ctx, msgs[0], nil); err != nil {
		return nil, fmt.Errorf("acknowledging message: %w", err)
	}

	return sbToBackendMessage(msgs[0]), nil
}

func (a *QueueAdapter) peek(ctx context.Context, opts backends.ReceiveOptions) (*backends.Message, error) {
	recv, err := a.senderCache.client.NewReceiverForQueue(opts.Queue, nil)
	if err != nil {
		return nil, fmt.Errorf("creating receiver for queue %s: %w", opts.Queue, err)
	}
	defer recv.Close(ctx) //nolint:errcheck

	timeout := backends.TimeoutDuration(opts.Timeout, opts.Wait)
	peekCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	msgs, err := recv.PeekMessages(peekCtx, 1, nil)
	if err != nil {
		if peekCtx.Err() != nil {
			return nil, backends.ErrNoMessageAvailable
		}
		return nil, fmt.Errorf("peeking queue %s: %w", opts.Queue, err)
	}
	if len(msgs) == 0 {
		return nil, backends.ErrNoMessageAvailable
	}

	return sbToBackendMessage(msgs[0]), nil
}

func (a *QueueAdapter) Close() error {
	ctx := context.Background()
	a.closeSenders(ctx)
	return a.senderCache.client.Close(ctx)
}
