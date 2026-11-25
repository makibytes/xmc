//go:build redis

package redis

import (
	"context"
	"fmt"
	"strings"

	"github.com/makibytes/xmc/broker/backends"
	redis "github.com/redis/go-redis/v9"
)

// ListQueues returns the Redis-backed queues visible to the caller.
func ListQueues(connArgs ConnArguments, prefix string) ([]backends.QueueInfo, error) {
	return listByPattern(connArgs, prefix+":queue:")
}

// ListTopics returns the Redis-backed topics visible to the caller.
func ListTopics(connArgs ConnArguments, prefix string) ([]backends.TopicInfo, error) {
	client, err := Connect(connArgs)
	if err != nil {
		return nil, err
	}
	defer client.Close() //nolint:errcheck

	topicPrefix := prefix + ":topic:"
	keys, err := scanKeys(context.Background(), client, topicPrefix+"*")
	if err != nil {
		return nil, fmt.Errorf("scanning topics: %w", err)
	}

	var topics []backends.TopicInfo
	for _, k := range keys {
		topics = append(topics, backends.TopicInfo{Name: strings.TrimPrefix(k, topicPrefix)})
	}
	return topics, nil
}

// scanKeys returns every key matching pattern, iterating SCAN to completion.
func scanKeys(ctx context.Context, client *redis.Client, pattern string) ([]string, error) {
	var all []string
	var cursor uint64
	for {
		keys, next, err := client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, err
		}
		all = append(all, keys...)
		if cursor = next; cursor == 0 {
			return all, nil
		}
	}
}

// CreateQueue creates a Redis stream for a queue and its consumer group.
func CreateQueue(connArgs ConnArguments, prefix, queue string) error {
	client, err := Connect(connArgs)
	if err != nil {
		return err
	}
	defer client.Close() //nolint:errcheck

	ctx := context.Background()
	key := prefix + ":queue:" + queue

	return client.XGroupCreateMkStream(ctx, key, xmcQueueGroup, "0").Err()
}

// DeleteQueue deletes the Redis stream for a queue.
func DeleteQueue(connArgs ConnArguments, prefix, queue string) error {
	client, err := Connect(connArgs)
	if err != nil {
		return err
	}
	defer client.Close() //nolint:errcheck

	ctx := context.Background()
	return client.Del(ctx, prefix+":queue:"+queue).Err()
}

// CreateTopic creates a Redis stream for a topic.
func CreateTopic(connArgs ConnArguments, prefix, topic string) error {
	client, err := Connect(connArgs)
	if err != nil {
		return err
	}
	defer client.Close() //nolint:errcheck

	ctx := context.Background()
	key := prefix + ":topic:" + topic

	_, err = client.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		Values: map[string]any{"_init": "1"},
	}).Result()
	if err != nil {
		return fmt.Errorf("creating topic stream: %w", err)
	}
	return client.XTrimMaxLen(ctx, key, 0).Err()
}

// DeleteTopic deletes the Redis stream for a topic.
func DeleteTopic(connArgs ConnArguments, prefix, topic string) error {
	client, err := Connect(connArgs)
	if err != nil {
		return err
	}
	defer client.Close() //nolint:errcheck

	ctx := context.Background()
	return client.Del(ctx, prefix+":topic:"+topic).Err()
}

// PurgeQueue removes all messages from a Redis stream queue.
func PurgeQueue(connArgs ConnArguments, prefix, queue string) (int64, error) {
	client, err := Connect(connArgs)
	if err != nil {
		return 0, err
	}
	defer client.Close() //nolint:errcheck

	ctx := context.Background()
	key := prefix + ":queue:" + queue

	count, err := client.XLen(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("getting queue length: %w", err)
	}

	if err := client.Del(ctx, key).Err(); err != nil {
		return 0, fmt.Errorf("purging queue %s: %w", queue, err)
	}

	return count, nil
}

// GetQueueStats returns message and consumer counts for a Redis stream queue.
func GetQueueStats(connArgs ConnArguments, prefix, queue string) (*backends.QueueStats, error) {
	client, err := Connect(connArgs)
	if err != nil {
		return nil, err
	}
	defer client.Close() //nolint:errcheck

	ctx := context.Background()
	key := prefix + ":queue:" + queue

	length, err := client.XLen(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("getting queue length: %w", err)
	}

	var consumerCount int
	groups, err := client.XInfoGroups(ctx, key).Result()
	if err == nil {
		for _, g := range groups {
			consumerCount += int(g.Consumers)
		}
	}

	return &backends.QueueStats{
		Name:          queue,
		MessageCount:  length,
		ConsumerCount: consumerCount,
	}, nil
}

func listByPattern(connArgs ConnArguments, prefix string) ([]backends.QueueInfo, error) {
	client, err := Connect(connArgs)
	if err != nil {
		return nil, err
	}
	defer client.Close() //nolint:errcheck

	ctx := context.Background()
	keys, err := scanKeys(ctx, client, prefix+"*")
	if err != nil {
		return nil, fmt.Errorf("scanning %s: %w", prefix, err)
	}

	// One round trip for all lengths. A key that is not a stream fails its own
	// XLEN (counted as 0, like before) without failing the others.
	pipe := client.Pipeline()
	lengths := make([]*redis.IntCmd, len(keys))
	for i, k := range keys {
		lengths[i] = pipe.XLen(ctx, k)
	}
	_, _ = pipe.Exec(ctx)

	var queues []backends.QueueInfo
	for i, k := range keys {
		queues = append(queues, backends.QueueInfo{
			Name:         strings.TrimPrefix(k, prefix),
			MessageCount: lengths[i].Val(),
		})
	}
	return queues, nil
}
