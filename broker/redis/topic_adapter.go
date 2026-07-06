//go:build redis

package redis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/makibytes/xmc/broker/backends"
)

type TopicAdapter struct {
	client  *redis.Client
	maxLen  int64
	lastID  map[string]string
	ensured map[string]struct{}
}

func NewTopicAdapter(connArgs ConnArguments, maxLen int64) (*TopicAdapter, error) {
	client, err := Connect(connArgs)
	if err != nil {
		return nil, err
	}
	return &TopicAdapter{
		client:  client,
		maxLen:  maxLen,
		lastID:  make(map[string]string),
		ensured: make(map[string]struct{}),
	}, nil
}

func (a *TopicAdapter) Publish(ctx context.Context, opts backends.PublishOptions) error {
	fields := buildFields(opts.Message, opts.Properties,
		opts.MessageID, opts.CorrelationID, opts.ReplyTo, opts.ContentType)

	args := &redis.XAddArgs{
		Stream: opts.Topic,
		Values: fields,
	}
	if a.maxLen > 0 {
		args.MaxLen = a.maxLen
		args.Approx = true
	}

	return a.client.XAdd(ctx, args).Err()
}

func (a *TopicAdapter) Subscribe(ctx context.Context, opts backends.SubscribeOptions) (*backends.Message, error) {
	key := opts.Topic
	timeout := backends.TimeoutDuration(opts.Timeout, opts.Wait)

	if opts.GroupID != "" {
		return a.subscribeGroup(ctx, key, opts.GroupID, opts.Durable, timeout)
	}
	return a.subscribeIndependent(ctx, key, timeout)
}

func (a *TopicAdapter) subscribeGroup(ctx context.Context, key, group string, durable bool, timeout time.Duration) (*backends.Message, error) {
	if err := a.ensureTopicGroup(ctx, key, group); err != nil {
		return nil, err
	}

	result, err := a.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    group,
		Consumer: "xmc",
		Streams:  []string{key, ">"},
		Count:    1,
		Block:    timeout,
	}).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, backends.ErrNoMessageAvailable
		}
		return nil, fmt.Errorf("subscribing to topic group %s/%s: %w", key, group, err)
	}

	if len(result) == 0 || len(result[0].Messages) == 0 {
		return nil, backends.ErrNoMessageAvailable
	}

	entry := result[0].Messages[0]
	a.client.XAck(ctx, key, group, entry.ID) //nolint:errcheck

	return streamToMessage(entry.ID, entry.Values), nil
}

func (a *TopicAdapter) subscribeIndependent(ctx context.Context, key string, timeout time.Duration) (*backends.Message, error) {
	startID, ok := a.lastID[key]
	if !ok {
		// Resolve "now" to a concrete ID once: XRead's "$" is re-evaluated on
		// every call, so a polling loop (-n 0, --for) would drop entries
		// published between two calls.
		info, err := a.client.XInfoStream(ctx, key).Result()
		switch {
		case err == nil:
			startID = info.LastGeneratedID
		case strings.Contains(err.Error(), "no such key"):
			startID = "0-0" // stream doesn't exist yet: deliver from its beginning
		default:
			return nil, fmt.Errorf("resolving stream position for %s: %w", key, err)
		}
		a.lastID[key] = startID
	}

	result, err := a.client.XRead(ctx, &redis.XReadArgs{
		Streams: []string{key, startID},
		Count:   1,
		Block:   timeout,
	}).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, backends.ErrNoMessageAvailable
		}
		return nil, fmt.Errorf("subscribing to topic %s: %w", key, err)
	}

	if len(result) == 0 || len(result[0].Messages) == 0 {
		return nil, backends.ErrNoMessageAvailable
	}

	entry := result[0].Messages[0]
	a.lastID[key] = entry.ID

	return streamToMessage(entry.ID, entry.Values), nil
}

func (a *TopicAdapter) Close() error {
	if a.client != nil {
		return a.client.Close()
	}
	return nil
}

func (a *TopicAdapter) ensureTopicGroup(ctx context.Context, key, group string) error {
	cacheKey := key + "/" + group
	if _, ok := a.ensured[cacheKey]; ok {
		return nil
	}
	err := a.client.XGroupCreateMkStream(ctx, key, group, "$").Err()
	if err != nil && !isBusyGroupError(err) {
		return fmt.Errorf("creating topic consumer group %s on %s: %w", group, key, err)
	}
	a.ensured[cacheKey] = struct{}{}
	return nil
}
