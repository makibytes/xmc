//go:build redis

package redis

import (
	"context"
	"fmt"
	"strings"

	"github.com/makibytes/xmc/broker/backends"
	redis "github.com/redis/go-redis/v9"
)

func ListQueues(connArgs ConnArguments, prefix string) ([]backends.QueueInfo, error) {
	return listByPattern(connArgs, prefix+":queue:")
}

func ListTopics(connArgs ConnArguments, prefix string) ([]backends.TopicInfo, error) {
	client, err := Connect(connArgs)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	topicPrefix := prefix + ":topic:"
	ctx := context.Background()
	var topics []backends.TopicInfo
	var cursor uint64

	for {
		keys, next, err := client.Scan(ctx, cursor, topicPrefix+"*", 100).Result()
		if err != nil {
			return nil, fmt.Errorf("scanning topics: %w", err)
		}
		for _, k := range keys {
			topics = append(topics, backends.TopicInfo{
				Name: strings.TrimPrefix(k, topicPrefix),
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
func CreateQueue(connArgs ConnArguments, prefix, queue string) error {
	client, err := Connect(connArgs)
	if err != nil {
		return err
	}
	defer client.Close()

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
	defer client.Close()

	ctx := context.Background()
	return client.Del(ctx, prefix+":queue:"+queue).Err()
}

// CreateTopic creates a Redis stream for a topic.
func CreateTopic(connArgs ConnArguments, prefix, topic string) error {
	client, err := Connect(connArgs)
	if err != nil {
		return err
	}
	defer client.Close()

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
	defer client.Close()

	ctx := context.Background()
	return client.Del(ctx, prefix+":topic:"+topic).Err()
}

func PurgeQueue(connArgs ConnArguments, prefix, queue string) (int64, error) {
	client, err := Connect(connArgs)
	if err != nil {
		return 0, err
	}
	defer client.Close()

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

func GetQueueStats(connArgs ConnArguments, prefix, queue string) (*backends.QueueStats, error) {
	client, err := Connect(connArgs)
	if err != nil {
		return nil, err
	}
	defer client.Close()

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
