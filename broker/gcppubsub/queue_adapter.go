//go:build gmc

package gcppubsub

import (
	"context"
	"fmt"
	"sync"

	"cloud.google.com/go/pubsub"

	"github.com/makibytes/xmc/broker/backends"
)

type QueueAdapter struct {
	client *pubsub.Client
}

func NewQueueAdapter(args ConnArguments) (*QueueAdapter, error) {
	client, err := Connect(context.Background(), args)
	if err != nil {
		return nil, err
	}
	return &QueueAdapter{client: client}, nil
}

func (a *QueueAdapter) Send(ctx context.Context, opts backends.SendOptions) error {
	topic, err := ensureTopic(ctx, a.client, opts.Queue)
	if err != nil {
		return err
	}

	subName := fmt.Sprintf("xmc-queue-%s", opts.Queue)
	if _, err := ensureSubscription(ctx, a.client, subName, topic); err != nil {
		return err
	}

	attrs := buildAttributes(opts.Properties,
		opts.MessageID, opts.CorrelationID, opts.ReplyTo, opts.ContentType)

	result := topic.Publish(ctx, &pubsub.Message{
		Data:       opts.Message,
		Attributes: attrs,
	})
	_, err = result.Get(ctx)
	return err
}

func (a *QueueAdapter) Receive(ctx context.Context, opts backends.ReceiveOptions) (*backends.Message, error) {
	subName := fmt.Sprintf("xmc-queue-%s", opts.Queue)
	topic, err := ensureTopic(ctx, a.client, opts.Queue)
	if err != nil {
		return nil, err
	}
	sub, err := ensureSubscription(ctx, a.client, subName, topic)
	if err != nil {
		return nil, err
	}
	sub.ReceiveSettings.MaxOutstandingMessages = 1
	sub.ReceiveSettings.NumGoroutines = 1
	sub.ReceiveSettings.Synchronous = true

	timeout := backends.TimeoutDuration(opts.Timeout, opts.Wait)
	receiveCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var (
		msg *backends.Message
		mu  sync.Mutex
	)

	err = sub.Receive(receiveCtx, func(_ context.Context, m *pubsub.Message) {
		mu.Lock()
		defer mu.Unlock()
		if msg != nil {
			m.Nack()
			return
		}
		if opts.Acknowledge {
			m.Ack()
		} else {
			m.Nack()
		}
		msg = pubsubToBackendMessage(m)
		cancel()
	})
	if msg != nil {
		return msg, nil
	}
	if err != nil && err != context.Canceled {
		return nil, err
	}
	return nil, backends.ErrNoMessageAvailable
}

func (a *QueueAdapter) Close() error {
	if a.client != nil {
		return a.client.Close()
	}
	return nil
}
