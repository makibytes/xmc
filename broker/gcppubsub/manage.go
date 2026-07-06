//go:build google

package gcppubsub

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/pubsub"
	"google.golang.org/api/iterator"

	"github.com/makibytes/xmc/broker/backends"
)

// ListTopicsWithSubscriptions returns topics as ObjectNodes with their
// subscriptions as children (for the AI TUI hierarchical sidebar window).
func ListTopicsWithSubscriptions(args ConnArguments) ([]backends.ObjectNode, error) {
	client, err := Connect(context.Background(), args)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	ctx := context.Background()
	var nodes []backends.ObjectNode

	topicIt := client.Topics(ctx)
	for {
		t, err := topicIt.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("listing topics: %w", err)
		}
		node := backends.ObjectNode{Name: t.ID()}

		// List subscriptions for this topic.
		subIt := t.Subscriptions(ctx)
		for {
			sub, err := subIt.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				break // still show the topic
			}
			node.Children = append(node.Children, backends.ObjectNode{
				Name: sub.ID(),
				Kind: "subscription",
			})
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// ListQueues returns xmc-emulated queues: Pub/Sub subscriptions whose name
// starts with "xmc-queue-", reported by their logical queue name.
func ListQueues(args ConnArguments) ([]backends.QueueInfo, error) {
	client, err := Connect(context.Background(), args)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	const prefix = "xmc-queue-"
	var queues []backends.QueueInfo
	it := client.Subscriptions(context.Background())
	for {
		s, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("listing subscriptions: %w", err)
		}
		if strings.HasPrefix(s.ID(), prefix) {
			queues = append(queues, backends.QueueInfo{Name: strings.TrimPrefix(s.ID(), prefix)})
		}
	}
	return queues, nil
}

// CreateTopic creates a Pub/Sub topic (idempotent).
func CreateTopic(args ConnArguments, name string) error {
	client, err := Connect(context.Background(), args)
	if err != nil {
		return err
	}
	defer client.Close()
	_, err = ensureTopic(context.Background(), client, name)
	return err
}

// DeleteTopic deletes a Pub/Sub topic.
func DeleteTopic(args ConnArguments, name string) error {
	client, err := Connect(context.Background(), args)
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Topic(name).Delete(context.Background()); err != nil {
		return fmt.Errorf("deleting topic %s: %w", name, err)
	}
	return nil
}

// CreateQueue creates the Pub/Sub topic and its xmc-queue subscription that
// together emulate a queue.
func CreateQueue(args ConnArguments, name string) error {
	client, err := Connect(context.Background(), args)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx := context.Background()
	topic, err := ensureTopic(ctx, client, name)
	if err != nil {
		return err
	}
	_, err = ensureSubscription(ctx, client, "xmc-queue-"+name, topic)
	return err
}

// DeleteQueue removes the xmc-queue subscription and its backing topic.
func DeleteQueue(args ConnArguments, name string) error {
	client, err := Connect(context.Background(), args)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx := context.Background()
	subName := "xmc-queue-" + name
	if err := client.Subscription(subName).Delete(ctx); err != nil {
		return fmt.Errorf("deleting queue subscription %s: %w", subName, err)
	}
	if err := client.Topic(name).Delete(ctx); err != nil {
		return fmt.Errorf("deleting queue topic %s: %w", name, err)
	}
	return nil
}

// PurgeQueue seeks the xmc-queue subscription to the current time, which
// causes all backlogged messages to be acknowledged (dropped).
func PurgeQueue(args ConnArguments, name string) (int64, error) {
	client, err := Connect(context.Background(), args)
	if err != nil {
		return 0, err
	}
	defer client.Close()
	return purgeSubscriptionByFullName(context.Background(), client, "xmc-queue-"+name)
}

// PurgeSubscription seeks an arbitrary subscription (not necessarily an
// xmc-queue one — e.g. one selected as a Topics-window child in the AI shell
// sidebar) to the current time, dropping its backlog. topic is accepted for
// signature parity with Azure's compound-key PurgeSubscription but unused:
// Pub/Sub resolves a subscription by its own name alone.
func PurgeSubscription(args ConnArguments, topic, subscription string) (int64, error) {
	client, err := Connect(context.Background(), args)
	if err != nil {
		return 0, err
	}
	defer client.Close()
	return purgeSubscriptionByFullName(context.Background(), client, subscription)
}

// purgeSubscriptionByFullName seeks the subscription named fullName to the
// current time, which causes all backlogged messages to be acknowledged
// (dropped). Pub/Sub does not report a message count for this operation.
func purgeSubscriptionByFullName(ctx context.Context, client *pubsub.Client, fullName string) (int64, error) {
	sub := client.Subscription(fullName)
	if err := sub.SeekToTime(ctx, time.Now()); err != nil {
		return 0, fmt.Errorf("purging subscription %s: %w", fullName, err)
	}
	return 0, nil
}
