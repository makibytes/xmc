//go:build mqtt

package mqtt

import (
	"context"
	"fmt"
	"os"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/makibytes/xmc/broker/backends"
)

const queueTopicPrefix = "queue/"
const sharedSubPrefix = "$share/xmc/"

// QueueAdapter adapts MQTT to QueueBackend using shared subscriptions.
type QueueAdapter struct {
	connArgs ConnArguments
	client   pahomqtt.Client
}

// NewQueueAdapter creates a connected QueueAdapter.
func NewQueueAdapter(args ConnArguments) (*QueueAdapter, error) {
	client, err := Connect(args)
	if err != nil {
		return nil, err
	}
	return &QueueAdapter{connArgs: args, client: client}, nil
}

// Send implements backends.QueueBackend.
// It publishes the message to topic "queue/{queue-name}" with QoS 1.
func (a *QueueAdapter) Send(_ context.Context, opts backends.SendOptions) error {
	topic := queueTopicPrefix + opts.Queue
	token := a.client.Publish(topic, 1, false, opts.Message)
	token.Wait()
	if err := token.Error(); err != nil {
		return fmt.Errorf("MQTT publish to %q: %w", topic, err)
	}
	return nil
}

// Receive implements backends.QueueBackend.
// It subscribes to "$share/xmc/queue/{queue-name}" (shared subscription) for
// a destructive read, or to "queue/{queue-name}" with a fresh session for a
// peek (Acknowledge=false). Returns one message then unsubscribes.
func (a *QueueAdapter) Receive(_ context.Context, opts backends.ReceiveOptions) (*backends.Message, error) {
	var topic string
	var client pahomqtt.Client

	if opts.Acknowledge {
		// Destructive read via shared subscription – competing consumers.
		topic = sharedSubPrefix + queueTopicPrefix + opts.Queue
		client = a.client
	} else {
		// Peek: use a fresh client with a unique clientID so the broker
		// delivers a copy without advancing the queue offset.
		peekArgs := a.connArgs
		peekArgs.ClientID = fmt.Sprintf("xmc-peek-%s-%d", opts.Queue, os.Getpid())
		peekArgs.ClientID = peekArgs.ClientID[:min(len(peekArgs.ClientID), 23)] // MQTT max 23 chars
		var err error
		client, err = Connect(peekArgs)
		if err != nil {
			return nil, fmt.Errorf("peek connect: %w", err)
		}
		defer client.Disconnect(250)
		topic = queueTopicPrefix + opts.Queue
	}

	msgCh := make(chan pahomqtt.Message, 1)
	token := client.Subscribe(topic, 1, func(_ pahomqtt.Client, msg pahomqtt.Message) {
		select {
		case msgCh <- msg:
		default:
		}
	})
	token.Wait()
	if err := token.Error(); err != nil {
		return nil, fmt.Errorf("MQTT subscribe to %q: %w", topic, err)
	}
	defer client.Unsubscribe(topic) //nolint:errcheck

	return waitForMessage(msgCh, opts.Timeout, opts.Wait)
}

// Close implements backends.QueueBackend.
func (a *QueueAdapter) Close() error {
	a.client.Disconnect(250)
	return nil
}

// waitForMessage waits on msgCh respecting timeout/wait semantics.
func waitForMessage(msgCh <-chan pahomqtt.Message, timeout float32, wait bool) (*backends.Message, error) {
	if wait {
		msg := <-msgCh
		return convertMessage(msg), nil
	}

	dur := time.Duration(timeout * float32(time.Second))
	if dur <= 0 {
		dur = 5 * time.Second
	}

	select {
	case msg := <-msgCh:
		return convertMessage(msg), nil
	case <-time.After(dur):
		return nil, fmt.Errorf("receive timed out after %.1fs", float64(timeout))
	}
}

// convertMessage converts a paho MQTT message to a backends.Message.
func convertMessage(msg pahomqtt.Message) *backends.Message {
	payload := msg.Payload()
	data := make([]byte, len(payload))
	copy(data, payload)
	return &backends.Message{Data: data}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
