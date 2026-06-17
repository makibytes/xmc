//go:build redmc

package redis

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/makibytes/xmc/broker/backends"
)

const xmcQueueGroup = "xmc-queue"

type QueueAdapter struct {
	client  *redis.Client
	ensured map[string]struct{}
}

func NewQueueAdapter(connArgs ConnArguments) (*QueueAdapter, error) {
	client, err := Connect(connArgs)
	if err != nil {
		return nil, err
	}
	return &QueueAdapter{
		client:  client,
		ensured: make(map[string]struct{}),
	}, nil
}

func (a *QueueAdapter) Send(ctx context.Context, opts backends.SendOptions) error {
	key := queueKey(opts.Queue)
	if err := a.ensureGroup(ctx, key); err != nil {
		return err
	}

	fields := buildFields(opts.Message, opts.Properties,
		opts.MessageID, opts.CorrelationID, opts.ReplyTo, opts.ContentType)

	return a.client.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		Values: fields,
	}).Err()
}

func (a *QueueAdapter) Receive(ctx context.Context, opts backends.ReceiveOptions) (*backends.Message, error) {
	key := queueKey(opts.Queue)

	if !opts.Acknowledge {
		return a.peek(ctx, key, opts)
	}

	if err := a.ensureGroup(ctx, key); err != nil {
		return nil, err
	}

	timeout := backends.TimeoutDuration(opts.Timeout, opts.Wait)

	result, err := a.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    xmcQueueGroup,
		Consumer: "xmc",
		Streams:  []string{key, ">"},
		Count:    1,
		Block:    timeout,
	}).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, backends.ErrNoMessageAvailable
		}
		return nil, fmt.Errorf("reading from queue %s: %w", opts.Queue, err)
	}

	if len(result) == 0 || len(result[0].Messages) == 0 {
		return nil, backends.ErrNoMessageAvailable
	}

	entry := result[0].Messages[0]

	a.client.XAck(ctx, key, xmcQueueGroup, entry.ID)  //nolint:errcheck
	a.client.XDel(ctx, key, entry.ID)                  //nolint:errcheck

	return streamToMessage(entry.ID, entry.Values), nil
}

func (a *QueueAdapter) peek(ctx context.Context, key string, opts backends.ReceiveOptions) (*backends.Message, error) {
	entries, err := a.client.XRange(ctx, key, "-", "+").Result()
	if err != nil {
		return nil, fmt.Errorf("peeking queue %s: %w", opts.Queue, err)
	}
	if len(entries) > 0 {
		return streamToMessage(entries[0].ID, entries[0].Values), nil
	}

	if !opts.Wait && opts.Timeout <= 0 {
		return nil, backends.ErrNoMessageAvailable
	}

	timeout := backends.TimeoutDuration(opts.Timeout, opts.Wait)
	result, err := a.client.XRead(ctx, &redis.XReadArgs{
		Streams: []string{key, "0"},
		Count:   1,
		Block:   timeout,
	}).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, backends.ErrNoMessageAvailable
		}
		return nil, fmt.Errorf("peeking queue %s: %w", opts.Queue, err)
	}
	if len(result) == 0 || len(result[0].Messages) == 0 {
		return nil, backends.ErrNoMessageAvailable
	}

	entry := result[0].Messages[0]
	return streamToMessage(entry.ID, entry.Values), nil
}

func (a *QueueAdapter) Close() error {
	if a.client != nil {
		return a.client.Close()
	}
	return nil
}

func (a *QueueAdapter) ensureGroup(ctx context.Context, key string) error {
	if _, ok := a.ensured[key]; ok {
		return nil
	}
	err := a.client.XGroupCreateMkStream(ctx, key, xmcQueueGroup, "0").Err()
	if err != nil && !isBusyGroupError(err) {
		return fmt.Errorf("creating consumer group on %s: %w", key, err)
	}
	a.ensured[key] = struct{}{}
	return nil
}

func isBusyGroupError(err error) bool {
	return err != nil && errors.Is(err, redis.Nil) || (err != nil && err.Error() == "BUSYGROUP Consumer Group name already exists")
}
