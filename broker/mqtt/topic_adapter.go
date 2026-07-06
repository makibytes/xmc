//go:build mqtt

package mqtt

import (
	"context"
	"fmt"
	"strconv"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/makibytes/xmc/broker/backends"
)

// TopicAdapter adapts MQTT to TopicBackend.
type TopicAdapter struct {
	connArgs ConnArguments
	client   pahomqtt.Client
	subs     subscriptionCache
}

// NewTopicAdapter creates a connected TopicAdapter.
func NewTopicAdapter(args ConnArguments) (*TopicAdapter, error) {
	client, err := Connect(args)
	if err != nil {
		return nil, err
	}
	return &TopicAdapter{connArgs: args, client: client}, nil
}

// Publish implements backends.TopicBackend.
// Publishes to the topic with the configured QoS (--qos, default 1) and
// retain flag.
func (a *TopicAdapter) Publish(_ context.Context, opts backends.PublishOptions) error {
	qos := byte(1)
	if v, err := strconv.Atoi(opts.Extra["qos"]); err == nil {
		qos = byte(v)
	}
	retain := opts.Extra["retain"] == "true"
	token := a.client.Publish(opts.Topic, qos, retain, opts.Message)
	if !token.WaitTimeout(tokenTimeout) {
		return fmt.Errorf("MQTT publish to %q timed out", opts.Topic)
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("MQTT publish to %q: %w", opts.Topic, err)
	}
	return nil
}

// Subscribe implements backends.TopicBackend.
// Subscribes to the topic (or a shared subscription when GroupID is set) and
// returns the first message received. The subscription is kept open for the
// adapter's lifetime so consecutive reads (-n, --for) don't drop messages
// that arrive between calls.
func (a *TopicAdapter) Subscribe(ctx context.Context, opts backends.SubscribeOptions) (*backends.Message, error) {
	topic := opts.Topic
	if opts.GroupID != "" {
		topic = "$share/" + opts.GroupID + "/" + opts.Topic
	}

	qos := byte(1)
	if v, err := strconv.Atoi(opts.Extra["qos"]); err == nil {
		qos = byte(v)
	}

	msgCh, err := a.subs.channelFor(a.client, topic, qos)
	if err != nil {
		return nil, err
	}
	return waitForMessage(ctx, msgCh, opts.Timeout, opts.Wait)
}

// Close implements backends.TopicBackend.
func (a *TopicAdapter) Close() error {
	a.client.Disconnect(250)
	return nil
}
