//go:build google

package gcppubsub

import (
	"context"
	"slices"
	"sync"

	"cloud.google.com/go/pubsub"

	"github.com/makibytes/xmc/broker/backends"
)

type TopicAdapter struct {
	client        *pubsub.Client
	cache         ensureCache
	ephemeral     []string
	ephemeralName string // per-adapter name for group-less subscriptions
}

func NewTopicAdapter(args ConnArguments) (*TopicAdapter, error) {
	client, err := Connect(context.Background(), args)
	if err != nil {
		return nil, err
	}
	return &TopicAdapter{
		client:        client,
		ephemeralName: "xmc-sub-" + backends.RandomSuffix(),
	}, nil
}

func (a *TopicAdapter) Publish(ctx context.Context, opts backends.PublishOptions) error {
	topic, err := a.cache.topic(ctx, a.client, opts.Topic)
	if err != nil {
		return err
	}

	attrs := buildAttributes(opts.Properties,
		opts.MessageID, opts.CorrelationID, opts.ReplyTo, opts.ContentType)

	result := topic.Publish(ctx, &pubsub.Message{
		Data:        opts.Message,
		Attributes:  attrs,
		OrderingKey: opts.Key,
	})
	_, err = result.Get(ctx)
	return err
}

func (a *TopicAdapter) Subscribe(ctx context.Context, opts backends.SubscribeOptions) (*backends.Message, error) {
	topic, err := a.cache.topic(ctx, a.client, opts.Topic)
	if err != nil {
		return nil, err
	}

	var subName string
	var ephemeral bool
	if opts.Extra != nil && opts.Extra["subscription"] != "" {
		subName = opts.Extra["subscription"]
	} else {
		// Scope the group form by topic: Pub/Sub subscription names are
		// project-global, and ensureSubscription rejects a same-named
		// subscription bound to another topic.
		subName, ephemeral = backends.ScopedSubscriptionName(opts, "-")
		if ephemeral {
			// One stable name per adapter, so repeated reads (-n, --for)
			// reuse a single subscription instead of creating one per call.
			subName = a.ephemeralName
		}
	}

	sub, err := a.cache.subscription(ctx, a.client, subName, topic)
	if err != nil {
		return nil, err
	}
	if ephemeral && !slices.Contains(a.ephemeral, subName) {
		a.ephemeral = append(a.ephemeral, subName)
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
			m.Nack() // non-destructive: leave it for redelivery (peek)
		}
		msg = pubsubToBackendMessage(m)
		cancel()
	})
	mu.Lock()
	if msg != nil {
		mu.Unlock()
		return msg, nil
	}
	mu.Unlock()
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
