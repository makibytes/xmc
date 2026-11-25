//go:build nats

package nats

import (
	"context"
	"errors"
	"fmt"
	"sync"

	natsclient "github.com/nats-io/nats.go"

	"github.com/makibytes/xmc/broker/backends"
)

// TopicAdapter adapts core NATS pub/sub to the TopicBackend interface.
type TopicAdapter struct {
	nc *natsclient.Conn

	// subs keeps one subscription per (topic, group) open for the adapter's
	// lifetime. Core NATS delivers only to live subscribers, so subscribing
	// per Subscribe call would drop every message published between two calls
	// of a streaming read (-n 0, --for); the subscription buffers them.
	mu   sync.Mutex
	subs map[string]*natsclient.Subscription
}

// NewTopicAdapter creates a new NATS topic adapter.
func NewTopicAdapter(connArgs ConnArguments) (*TopicAdapter, error) {
	nc, err := Connect(connArgs)
	if err != nil {
		return nil, err
	}

	return &TopicAdapter{
		nc:   nc,
		subs: make(map[string]*natsclient.Subscription),
	}, nil
}

// Publish implements backends.TopicBackend.
func (a *TopicAdapter) Publish(_ context.Context, opts backends.PublishOptions) error {
	msg := natsclient.NewMsg(opts.Topic)
	msg.Data = opts.Message

	if opts.MessageID != "" {
		msg.Header.Set(backends.PropMessageID, opts.MessageID)
	}
	if opts.CorrelationID != "" {
		msg.Header.Set(backends.PropCorrelationID, opts.CorrelationID)
	}
	if opts.ReplyTo != "" {
		msg.Header.Set(backends.PropReplyTo, opts.ReplyTo)
	}
	if opts.ContentType != "" {
		msg.Header.Set(backends.PropContentType, opts.ContentType)
	}
	for k, v := range backends.StringifyProps(opts.Properties) {
		msg.Header.Set(k, v)
	}

	return a.nc.PublishMsg(msg)
}

// subscription returns the open subscription for (topic, group), creating it
// on first use. A non-empty group makes it a queue subscription (competing
// consumers).
func (a *TopicAdapter) subscription(topic, group string) (*natsclient.Subscription, error) {
	key := topic + "|" + group
	a.mu.Lock()
	defer a.mu.Unlock()
	if sub, ok := a.subs[key]; ok {
		return sub, nil
	}
	var (
		sub *natsclient.Subscription
		err error
	)
	if group != "" {
		sub, err = a.nc.QueueSubscribeSync(topic, group)
	} else {
		sub, err = a.nc.SubscribeSync(topic)
	}
	if err != nil {
		return nil, fmt.Errorf("subscribing to topic %s: %w", topic, err)
	}
	a.subs[key] = sub
	return sub, nil
}

// Subscribe implements backends.TopicBackend.
func (a *TopicAdapter) Subscribe(ctx context.Context, opts backends.SubscribeOptions) (*backends.Message, error) {
	sub, err := a.subscription(opts.Topic, opts.GroupID)
	if err != nil {
		return nil, err
	}

	waitCtx, cancel := context.WithTimeout(ctx, backends.TimeoutDuration(opts.Timeout, opts.Wait))
	defer cancel()
	msg, err := sub.NextMsgWithContext(waitCtx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err() // the caller gave up (Ctrl-C, Esc)
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, natsclient.ErrTimeout) {
			return nil, backends.ErrNoMessageAvailable
		}
		return nil, err
	}

	return natsToBackendMessage(msg), nil
}

// Close implements backends.TopicBackend.
func (a *TopicAdapter) Close() error {
	if a.nc != nil {
		a.nc.Close()
	}
	return nil
}
