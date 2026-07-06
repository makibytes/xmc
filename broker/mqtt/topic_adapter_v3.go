//go:build mqtt

package mqtt

import (
	"context"
	"fmt"
	"strconv"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/makibytes/xmc/broker/backends"
)

// TopicAdapterV3 adapts MQTT to TopicBackend.
type TopicAdapterV3 struct {
	connArgs ConnArguments
	client   pahomqtt.Client
	subs     subscriptionCacheV3
}

// NewTopicAdapterV3 creates a connected TopicAdapterV3.
func NewTopicAdapterV3(args ConnArguments) (*TopicAdapterV3, error) {
	client, err := ConnectV3(args)
	if err != nil {
		return nil, err
	}
	return &TopicAdapterV3{connArgs: args, client: client}, nil
}

// Publish implements backends.TopicBackend.
// Publishes to the topic with the configured QoS (--qos, default 1) and
// retain flag.
func (a *TopicAdapterV3) Publish(_ context.Context, opts backends.PublishOptions) error {
	if err := rejectV3Metadata(len(opts.Properties) > 0, opts.MessageID, opts.CorrelationID,
		opts.ReplyTo, opts.ContentType, opts.TTL); err != nil {
		return err
	}
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
func (a *TopicAdapterV3) Subscribe(ctx context.Context, opts backends.SubscribeOptions) (*backends.Message, error) {
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
	return waitForMessageV3(ctx, msgCh, opts.Timeout, opts.Wait)
}

// Close implements backends.TopicBackend.
func (a *TopicAdapterV3) Close() error {
	a.client.Disconnect(250)
	return nil
}
