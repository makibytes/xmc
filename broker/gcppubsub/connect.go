//go:build google

package gcppubsub

import (
	"context"
	"fmt"
	"os"
	"sync"

	"cloud.google.com/go/pubsub"
	"google.golang.org/api/option"
)

type ConnArguments struct {
	Project     string
	Credentials string
	Endpoint    string
}

func Connect(ctx context.Context, args ConnArguments) (*pubsub.Client, error) {
	var opts []option.ClientOption

	if args.Credentials != "" {
		opts = append(opts, option.WithCredentialsFile(args.Credentials))
	}
	if args.Endpoint != "" {
		os.Setenv("PUBSUB_EMULATOR_HOST", args.Endpoint)
	}

	client, err := pubsub.NewClient(ctx, args.Project, opts...)
	if err != nil {
		return nil, fmt.Errorf("creating Pub/Sub client for project %s: %w", args.Project, err)
	}

	return client, nil
}

func ensureTopic(ctx context.Context, client *pubsub.Client, name string) (*pubsub.Topic, error) {
	topic := client.Topic(name)
	exists, err := topic.Exists(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking topic %s: %w", name, err)
	}
	if exists {
		return topic, nil
	}

	topic, err = client.CreateTopic(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("creating topic %s: %w", name, err)
	}
	return topic, nil
}

func ensureSubscription(ctx context.Context, client *pubsub.Client, subName string, topic *pubsub.Topic) (*pubsub.Subscription, error) {
	sub := client.Subscription(subName)
	exists, err := sub.Exists(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking subscription %s: %w", subName, err)
	}
	if exists {
		// Subscription names are global in a project, but each subscription is
		// bound to exactly one topic. Reusing a same-named subscription bound
		// to a different topic would silently deliver that other topic's
		// messages, so verify the binding.
		cfg, err := sub.Config(ctx)
		if err != nil {
			return nil, fmt.Errorf("reading subscription %s: %w", subName, err)
		}
		if cfg.Topic != nil && cfg.Topic.ID() != topic.ID() {
			return nil, fmt.Errorf("subscription %s already exists on topic %s, not %s; use a different --group or --subscription",
				subName, cfg.Topic.ID(), topic.ID())
		}
		return sub, nil
	}

	sub, err = client.CreateSubscription(ctx, subName, pubsub.SubscriptionConfig{
		Topic: topic,
		// Honor ordering keys (-K) on delivery. Key-less messages are
		// unaffected; the field is immutable after creation, so subscriptions
		// created by older xmc versions keep unordered delivery.
		EnableMessageOrdering: true,
	})
	if err != nil {
		return nil, fmt.Errorf("creating subscription %s: %w", subName, err)
	}
	return sub, nil
}

// ensureCache memoizes ensured topics and subscriptions for one adapter, so
// bulk operations don't pay existence-check admin RPCs on every message.
type ensureCache struct {
	mu     sync.Mutex
	topics map[string]*pubsub.Topic
	subs   map[string]*pubsub.Subscription
}

// topic returns the (ensured) topic named name, creating it on first use.
func (c *ensureCache) topic(ctx context.Context, client *pubsub.Client, name string) (*pubsub.Topic, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t, ok := c.topics[name]; ok {
		return t, nil
	}
	t, err := ensureTopic(ctx, client, name)
	if err != nil {
		return nil, err
	}
	// The client rejects a Publish carrying an OrderingKey (-K) unless the
	// publisher handle opts in; key-less messages are unaffected by this.
	t.EnableMessageOrdering = true
	if c.topics == nil {
		c.topics = make(map[string]*pubsub.Topic)
	}
	c.topics[name] = t
	return t, nil
}

// subscription returns the (ensured) subscription subName on topic, creating
// and verifying it on first use.
func (c *ensureCache) subscription(ctx context.Context, client *pubsub.Client, subName string, topic *pubsub.Topic) (*pubsub.Subscription, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.subs[subName]; ok {
		return s, nil
	}
	s, err := ensureSubscription(ctx, client, subName, topic)
	if err != nil {
		return nil, err
	}
	if c.subs == nil {
		c.subs = make(map[string]*pubsub.Subscription)
	}
	c.subs[subName] = s
	return s, nil
}
