//go:build mqtt

package mqtt

import (
	"context"
	"fmt"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/makibytes/xmc/broker/backends"
)

// TopicAdapter adapts MQTT to TopicBackend.
type TopicAdapter struct {
	connArgs ConnArguments
	client   pahomqtt.Client
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
// Publishes to the topic with QoS 1 (QoS 0 if not persistent).
func (a *TopicAdapter) Publish(_ context.Context, opts backends.PublishOptions) error {
	qos := byte(1)
	if !opts.Persistent {
		qos = 0
	}
	token := a.client.Publish(opts.Topic, qos, false, opts.Message)
	token.Wait()
	if err := token.Error(); err != nil {
		return fmt.Errorf("MQTT publish to %q: %w", opts.Topic, err)
	}
	return nil
}

// Subscribe implements backends.TopicBackend.
// Subscribes to the topic (or a shared subscription when GroupID is set)
// and returns the first message received.
func (a *TopicAdapter) Subscribe(_ context.Context, opts backends.SubscribeOptions) (*backends.Message, error) {
	topic := opts.Topic
	if opts.GroupID != "" {
		topic = "$share/" + opts.GroupID + "/" + opts.Topic
	}

	msgCh := make(chan pahomqtt.Message, 1)
	token := a.client.Subscribe(topic, 1, func(_ pahomqtt.Client, msg pahomqtt.Message) {
		select {
		case msgCh <- msg:
		default:
		}
	})
	token.Wait()
	if err := token.Error(); err != nil {
		return nil, fmt.Errorf("MQTT subscribe to %q: %w", topic, err)
	}
	defer a.client.Unsubscribe(topic) //nolint:errcheck

	return waitForMessage(msgCh, opts.Timeout, opts.Wait)
}

// Close implements backends.TopicBackend.
func (a *TopicAdapter) Close() error {
	a.client.Disconnect(250)
	return nil
}
