//go:build redis

package redis

import (
	"context"
	"fmt"
	"strings"

	"github.com/makibytes/xmc/broker/backends"
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
