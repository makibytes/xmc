//go:build google

package gcppubsub

import (
	"context"
	"sync"

	"cloud.google.com/go/pubsub"

	"github.com/makibytes/xmc/broker/backends"
)

type TopicAdapter struct {
	client    *pubsub.Client
	ephemeral []string
}

func NewTopicAdapter(args ConnArguments) (*TopicAdapter, error) {
	client, err := Connect(context.Background(), args)
	if err != nil {
		return nil, err
	}
	return &TopicAdapter{client: client}, nil
}

func (a *TopicAdapter) Publish(ctx context.Context, opts backends.PublishOptions) error {
	topic, err := ensureTopic(ctx, a.client, opts.Topic)
	if err != nil {
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

func (a *TopicAdapter) Subscribe(ctx context.Context, opts backends.SubscribeOptions) (*backends.Message, error) {
	topic, err := ensureTopic(ctx, a.client, opts.Topic)
	if err != nil {
		return nil, err
	}

	var subName string
	var ephemeral bool
	if opts.Extra != nil && opts.Extra["subscription"] != "" {
		subName = opts.Extra["subscription"]
	} else {
		subName, ephemeral = backends.SubscriptionName(opts)
		if ephemeral {
			a.ephemeral = append(a.ephemeral, subName)
		}
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
		m.Ack()
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

func (a *TopicAdapter) Close() error {
	if a.client != nil {
		ctx := context.Background()
		for _, name := range a.ephemeral {
			sub := a.client.Subscription(name)
			sub.Delete(ctx) //nolint:errcheck
		}
		return a.client.Close()
	}
	return nil
}

