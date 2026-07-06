//go:build mqtt

package mqtt

import (
	"context"
	"fmt"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"

	"github.com/makibytes/xmc/broker/backends"
)

// TopicAdapter adapts MQTT 5 to TopicBackend.
type TopicAdapter struct {
	connArgs ConnArguments
	cm       *autopaho.ConnectionManager
	subs     subCache5
}

// NewTopicAdapter creates a connected MQTT 5 TopicAdapter.
func NewTopicAdapter(args ConnArguments) (*TopicAdapter, error) {
	cm, err := Connect5(args)
	if err != nil {
		return nil, err
	}
	return &TopicAdapter{connArgs: args, cm: cm}, nil
}

// Publish implements backends.TopicBackend.
// Publishes to the topic with the configured QoS (--qos, default 1) and
// retain flag; metadata rides in the native MQTT 5 property slots. Unlike the
// queue side, a reply-to is used verbatim as the response topic.
func (a *TopicAdapter) Publish(ctx context.Context, opts backends.PublishOptions) error {
	pubCtx, cancel := context.WithTimeout(ctx, tokenTimeout)
	defer cancel()
	_, err := a.cm.Publish(pubCtx, &paho.Publish{
		Topic:   opts.Topic,
		QoS:     qosFromExtra(opts.Extra),
		Retain:  opts.Extra["retain"] == "true",
		Payload: opts.Message,
		Properties: buildPublishProperties(opts.Properties,
			opts.MessageID, opts.CorrelationID, opts.ReplyTo, opts.ContentType, opts.TTL),
	})
	if err != nil {
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

	msgCh, err := a.subs.channelFor(ctx, a.cm, topic, qosFromExtra(opts.Extra))
	if err != nil {
		return nil, err
	}
	return waitForMessage(ctx, msgCh, opts.Timeout, opts.Wait, false)
}

// Close implements backends.TopicBackend.
func (a *TopicAdapter) Close() error {
	return a.cm.Disconnect(context.Background())
}
