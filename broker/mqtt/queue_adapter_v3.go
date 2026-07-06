//go:build mqtt

package mqtt

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/makibytes/xmc/broker/backends"
)

// QueueAdapterV3 adapts MQTT to QueueBackend using shared subscriptions.
type QueueAdapterV3 struct {
	connArgs ConnArguments
	client   pahomqtt.Client
	subs     subscriptionCacheV3
}

// NewQueueAdapterV3 creates a connected QueueAdapterV3.
func NewQueueAdapterV3(args ConnArguments) (*QueueAdapterV3, error) {
	client, err := ConnectV3(args)
	if err != nil {
		return nil, err
	}
	return &QueueAdapterV3{connArgs: args, client: client}, nil
}

// Send implements backends.QueueBackend.
// It publishes the message to topic "queue/{queue-name}" with the configured QoS.
func (a *QueueAdapterV3) Send(ctx context.Context, opts backends.SendOptions) error {
	if err := rejectV3Metadata(len(opts.Properties) > 0, opts.MessageID, opts.CorrelationID,
		opts.ReplyTo, opts.ContentType, opts.TTL); err != nil {
		return err
	}
	topic := queueTopicPrefix + opts.Queue
	qos := byte(1)
	if v, err := strconv.Atoi(opts.Extra["qos"]); err == nil {
		qos = byte(v)
	}
	token := a.client.Publish(topic, qos, false, opts.Message)
	if !token.WaitTimeout(30 * time.Second) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("MQTT publish to %q timed out", topic)
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("MQTT publish to %q: %w", topic, err)
	}
	return nil
}

// Receive implements backends.QueueBackend.
// It subscribes to "$share/xmc/queue/{queue-name}" (shared subscription) for
// a destructive read, or to "queue/{queue-name}" with a fresh session for a
// peek (Acknowledge=false). The subscription is kept open for the adapter's
// lifetime so consecutive reads (-n, --for) don't drop messages that arrive
// between calls.
func (a *QueueAdapterV3) Receive(ctx context.Context, opts backends.ReceiveOptions) (*backends.Message, error) {
	qos := byte(1)
	if v, err := strconv.Atoi(opts.Extra["qos"]); err == nil {
		qos = byte(v)
	}

	if !opts.Acknowledge {
		// Peek: use a fresh client with a unique clientID so the broker
		// delivers a copy while shared-group consumers keep theirs.
		peekArgs := a.connArgs
		peekArgs.ClientID = fmt.Sprintf("xmc-peek-%s-%d", opts.Queue, os.Getpid())
		peekArgs.ClientID = peekArgs.ClientID[:min(len(peekArgs.ClientID), 23)] // MQTT max 23 chars
		client, err := ConnectV3(peekArgs)
		if err != nil {
			return nil, fmt.Errorf("peek connect: %w", err)
		}
		defer client.Disconnect(250)
		topic := queueTopicPrefix + opts.Queue

		msgCh := make(chan pahomqtt.Message, 1)
		token := client.Subscribe(topic, qos, func(_ pahomqtt.Client, msg pahomqtt.Message) {
			select {
			case msgCh <- msg:
			default:
			}
		})
		if !token.WaitTimeout(tokenTimeout) {
			return nil, fmt.Errorf("MQTT subscribe to %q timed out", topic)
		}
		if err := token.Error(); err != nil {
			return nil, fmt.Errorf("MQTT subscribe to %q: %w", topic, err)
		}
		return waitForMessageV3(ctx, msgCh, opts.Timeout, opts.Wait)
	}

	// Destructive read via shared subscription – competing consumers.
	group := "xmc"
	if g := opts.Extra["group"]; g != "" {
		group = g
	}
	topic := "$share/" + group + "/" + queueTopicPrefix + opts.Queue

	msgCh, err := a.subs.channelFor(a.client, topic, qos)
	if err != nil {
		return nil, err
	}
	return waitForMessageV3(ctx, msgCh, opts.Timeout, opts.Wait)
}

// Close implements backends.QueueBackend.
func (a *QueueAdapterV3) Close() error {
	a.client.Disconnect(250)
	return nil
}

// waitForMessageV3 waits on msgCh respecting timeout/wait semantics, using the
// shared TimeoutDuration helper so MQTT follows the same contract as all other
// brokers. Buffered messages from a still-open subscription are drained first.
func waitForMessageV3(ctx context.Context, msgCh <-chan pahomqtt.Message, timeout float32, wait bool) (*backends.Message, error) {
	dur := backends.TimeoutDuration(timeout, wait)
	timer := time.NewTimer(dur)
	defer timer.Stop()

	select {
	case msg := <-msgCh:
		return convertMessageV3(msg), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, backends.ErrNoMessageAvailable
	}
}

// convertMessageV3 converts a paho MQTT message to a backends.Message.
//
// MQTT 3.1.1 has no user properties at the protocol level, so application
// properties, correlation ID, reply-to, content-type, and message ID cannot be
// carried through an MQTT broker. The default MQTT 5 adapters carry all of
// them; this legacy path exists for --mqtt-version 3 brokers only.
func convertMessageV3(msg pahomqtt.Message) *backends.Message {
	payload := msg.Payload()
	data := make([]byte, len(payload))
	copy(data, payload)
	return &backends.Message{Data: data}
}
