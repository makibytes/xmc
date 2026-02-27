//go:build nats

package nats

import (
	"context"
	"errors"
	"fmt"
	"time"

	natsclient "github.com/nats-io/nats.go"

	"github.com/makibytes/xmc/broker/backends"
)

// TopicAdapter adapts core NATS pub/sub to the TopicBackend interface.
type TopicAdapter struct {
	connArgs ConnArguments
	nc       *natsclient.Conn
}

// NewTopicAdapter creates a new NATS topic adapter.
func NewTopicAdapter(connArgs ConnArguments) (*TopicAdapter, error) {
	nc, err := Connect(connArgs)
	if err != nil {
		return nil, err
	}

	return &TopicAdapter{
		connArgs: connArgs,
		nc:       nc,
	}, nil
}

// Publish implements backends.TopicBackend.
func (a *TopicAdapter) Publish(ctx context.Context, opts backends.PublishOptions) error {
	msg := natsclient.NewMsg(opts.Topic)
	msg.Data = opts.Message

	if opts.MessageID != "" {
		msg.Header.Set(natsclient.MsgIdHdr, opts.MessageID)
	}
	for k, v := range opts.Properties {
		msg.Header.Set(k, fmt.Sprintf("%v", v))
	}

	return a.nc.PublishMsg(msg)
}

// Subscribe implements backends.TopicBackend.
func (a *TopicAdapter) Subscribe(ctx context.Context, opts backends.SubscribeOptions) (*backends.Message, error) {
	timeout := subscribeTimeout(opts.Timeout, opts.Wait)

	var (
		sub *natsclient.Subscription
		err error
	)

	if opts.GroupID != "" {
		sub, err = a.nc.QueueSubscribeSync(opts.Topic, opts.GroupID)
	} else {
		sub, err = a.nc.SubscribeSync(opts.Topic)
	}
	if err != nil {
		return nil, fmt.Errorf("subscribing to topic %s: %w", opts.Topic, err)
	}
	defer sub.Unsubscribe() //nolint:errcheck

	msg, err := sub.NextMsg(timeout)
	if err != nil {
		if errors.Is(err, natsclient.ErrTimeout) {
			return nil, errors.New("no message available")
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

// subscribeTimeout converts SubscribeOptions timeout fields to a time.Duration.
func subscribeTimeout(timeout float32, wait bool) time.Duration {
	if wait {
		return 24 * time.Hour
	}
	if timeout <= 0 {
		return 5 * time.Second
	}
	return time.Duration(float64(timeout) * float64(time.Second))
}
