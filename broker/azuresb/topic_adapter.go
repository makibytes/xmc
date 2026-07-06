//go:build azure

package azuresb

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus/admin"

	"github.com/makibytes/xmc/broker/backends"
)

type ephemeralSub struct {
	topic string
	sub   string
}

type TopicAdapter struct {
	senderCache
	adm           *admin.Client
	ephemeralSubs []ephemeralSub
	ensured       map[string]struct{} // topics/subscriptions confirmed to exist
	ephemeralName string              // per-adapter name for group-less subscriptions
}

// ensureTopicCached ensures the topic exists once per adapter.
func (a *TopicAdapter) ensureTopicCached(ctx context.Context, topic string) error {
	if _, ok := a.ensured[topic]; ok {
		return nil
	}
	if err := ensureTopic(ctx, a.adm, topic); err != nil {
		return err
	}
	a.ensured[topic] = struct{}{}
	return nil
}

// ensureTopicAndSubCached ensures the (topic, subscription) pair exists once
// per adapter.
func (a *TopicAdapter) ensureTopicAndSubCached(ctx context.Context, topic, sub string) error {
	key := topic + "|" + sub
	if _, ok := a.ensured[key]; ok {
		return nil
	}
	if err := ensureTopicAndSub(ctx, a.adm, topic, sub); err != nil {
		return err
	}
	a.ensured[key] = struct{}{}
	a.ensured[topic] = struct{}{}
	return nil
}

func NewTopicAdapter(args ConnArguments) (*TopicAdapter, error) {
	client, err := Connect(args)
	if err != nil {
		return nil, err
	}
	adm, err := AdminClient(args)
	if err != nil {
		client.Close(context.Background()) //nolint:errcheck
		return nil, err
	}
	return &TopicAdapter{
		senderCache:   newSenderCache(client),
		adm:           adm,
		ensured:       make(map[string]struct{}),
		ephemeralName: "xmc-sub-" + backends.RandomSuffix(),
	}, nil
}

func (a *TopicAdapter) Publish(ctx context.Context, opts backends.PublishOptions) error {
	if err := a.ensureTopicCached(ctx, opts.Topic); err != nil {
		return err
	}

	sender, err := a.getSender(opts.Topic)
	if err != nil {
		return err
	}

	msg := toSBMessage(opts.Message, opts.Properties,
		opts.MessageID, opts.CorrelationID, opts.ReplyTo, opts.ContentType, opts.TTL)

	return sender.SendMessage(ctx, msg, nil)
}

func (a *TopicAdapter) Subscribe(ctx context.Context, opts backends.SubscribeOptions) (*backends.Message, error) {
	var subName string
	var ephemeral bool
	if opts.Extra != nil && opts.Extra["subscription"] != "" {
		subName = opts.Extra["subscription"]
	} else {
		subName, ephemeral = backends.SubscriptionName(opts)
		if ephemeral {
			// One stable name per adapter, so repeated reads (-n, --for)
			// reuse a single subscription instead of creating one per call.
			subName = a.ephemeralName
		}
	}

	firstUse := false
	if _, ok := a.ensured[opts.Topic+"|"+subName]; !ok {
		firstUse = true
	}
	if err := a.ensureTopicAndSubCached(ctx, opts.Topic, subName); err != nil {
		return nil, err
	}
	if ephemeral && firstUse {
		a.ephemeralSubs = append(a.ephemeralSubs, ephemeralSub{topic: opts.Topic, sub: subName})
	}

	recv, err := a.senderCache.client.NewReceiverForSubscription(opts.Topic, subName, nil)
	if err != nil {
		return nil, fmt.Errorf("creating subscription receiver %s/%s: %w", opts.Topic, subName, err)
	}
	defer recv.Close(ctx) //nolint:errcheck

	timeout := backends.TimeoutDuration(opts.Timeout, opts.Wait)
	receiveCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if !opts.Acknowledge {
		// Non-destructive: PeekMessages never acks/completes, mirroring the
		// queue-side peek() path — genuinely no consumption, not Nack-based.
		msgs, err := recv.PeekMessages(receiveCtx, 1, nil)
		if err != nil {
			if receiveCtx.Err() != nil {
				return nil, backends.ErrNoMessageAvailable
			}
			return nil, fmt.Errorf("peeking subscription %s/%s: %w", opts.Topic, subName, err)
		}
		if len(msgs) == 0 {
			return nil, backends.ErrNoMessageAvailable
		}
		return sbToBackendMessage(msgs[0]), nil
	}

	msgs, err := recv.ReceiveMessages(receiveCtx, 1, nil)
	if err != nil {
		if receiveCtx.Err() != nil {
			return nil, backends.ErrNoMessageAvailable
		}
		return nil, fmt.Errorf("receiving from subscription %s/%s: %w", opts.Topic, subName, err)
	}
	if len(msgs) == 0 {
		return nil, backends.ErrNoMessageAvailable
	}

	if err := recv.CompleteMessage(ctx, msgs[0], nil); err != nil {
		return nil, fmt.Errorf("acknowledging message: %w", err)
	}

	return sbToBackendMessage(msgs[0]), nil
}

func (a *TopicAdapter) Close() error {
	ctx := context.Background()

	for _, es := range a.ephemeralSubs {
		a.adm.DeleteSubscription(ctx, es.topic, es.sub, nil) //nolint:errcheck
	}

	a.closeSenders(ctx)
	return a.senderCache.client.Close(ctx)
}
