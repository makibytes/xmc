//go:build redis

package redis

import (
	"context"
	"fmt"
	"strings"

	"github.com/makibytes/xmc/broker/backends"
	redis "github.com/redis/go-redis/v9"
)

func ListQueues(connArgs ConnArguments) ([]backends.QueueInfo, error) {
	return listByPrefix(connArgs, "xmc:queue:")
}

func ListTopics(connArgs ConnArguments) ([]backends.TopicInfo, error) {
	client, err := Connect(connArgs)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	ctx := context.Background()
	var topics []backends.TopicInfo
	var cursor uint64

	for {
		keys, next, err := client.Scan(ctx, cursor, "xmc:topic:*", 100).Result()
		if err != nil {
			return nil, fmt.Errorf("scanning topics: %w", err)
		}
		for _, k := range keys {
			topics = append(topics, backends.TopicInfo{
				Name: strings.TrimPrefix(k, "xmc:topic:"),
			})
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}

	return topics, nil
}

// CreateQueue creates a Redis stream for a queue and its consumer group.
func CreateQueue(connArgs ConnArguments, queue string) error {
	client, err := Connect(connArgs)
	if err != nil {
		return err
	}
	defer client.Close()

	ctx := context.Background()
	key := queueKey(queue)

	// XGROUP CREATE with MKSTREAM creates both the stream and the consumer group.
	return client.XGroupCreateMkStream(ctx, key, xmcQueueGroup, "0").Err()
}

// DeleteQueue deletes the Redis stream for a queue.
func DeleteQueue(connArgs ConnArguments, queue string) error {
	client, err := Connect(connArgs)
	if err != nil {
		return err
	}
	defer client.Close()

	ctx := context.Background()
	return client.Del(ctx, queueKey(queue)).Err()
}

// CreateTopic creates a Redis stream for a topic.
func CreateTopic(connArgs ConnArguments, topic string) error {
	client, err := Connect(connArgs)
	if err != nil {
		return err
	}
	defer client.Close()

	ctx := context.Background()
	key := topicKey(topic)

	// Add and immediately trim to create an empty stream with MAXLEN ~ 10000
	// (matching the topic adapter's trimming policy).
	_, err = client.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		MaxLen: 10000,
		Approx: true,
		Values: map[string]any{"_init": "1"},
	}).Result()
	if err != nil {
		return fmt.Errorf("creating topic stream: %w", err)
	}
	// Trim the init message — XTRIM MAXLEN 0 removes everything.
	return client.XTrimMaxLen(ctx, key, 0).Err()
}

// DeleteTopic deletes the Redis stream for a topic.
func DeleteTopic(connArgs ConnArguments, topic string) error {
	client, err := Connect(connArgs)
	if err != nil {
		return err
	}
	defer client.Close()

	ctx := context.Background()
	return client.Del(ctx, topicKey(topic)).Err()
}

func PurgeQueue(connArgs ConnArguments, queue string) (int64, error) {
	client, err := Connect(connArgs)
	if err != nil {
		return 0, err
	}
	defer client.Close()

	ctx := context.Background()
	key := queueKey(queue)

	count, err := client.XLen(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("getting queue length: %w", err)
	}

	if err := client.Del(ctx, key).Err(); err != nil {
		return 0, fmt.Errorf("purging queue %s: %w", queue, err)
	}

	return count, nil
}

func GetQueueStats(connArgs ConnArguments, queue string) (*backends.QueueStats, error) {
	client, err := Connect(connArgs)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	ctx := context.Background()
	key := queueKey(queue)

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

func listByPrefix(connArgs ConnArguments, prefix string) ([]backends.QueueInfo, error) {
	client, err := Connect(connArgs)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	ctx := context.Background()
	var queues []backends.QueueInfo
	var cursor uint64

	for {
		keys, next, err := client.Scan(ctx, cursor, prefix+"*", 100).Result()
		if err != nil {
			return nil, fmt.Errorf("scanning %s: %w", prefix, err)
		}
		for _, k := range keys {
			length, _ := client.XLen(ctx, k).Result()
			queues = append(queues, backends.QueueInfo{
				Name:         strings.TrimPrefix(k, prefix),
				MessageCount: length,
			})
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}

	return queues, nil
}
