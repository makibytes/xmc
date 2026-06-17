//go:build gmc

package gcppubsub

import (
	"context"
	"fmt"
	"os"

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
		return sub, nil
	}

	sub, err = client.CreateSubscription(ctx, subName, pubsub.SubscriptionConfig{
		Topic: topic,
	})
	if err != nil {
		return nil, fmt.Errorf("creating subscription %s: %w", subName, err)
	}
	return sub, nil
}
