//go:build mqtt

package mqtt

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"

	"github.com/makibytes/xmc/broker/backends"
)

const queueTopicPrefix = "queue/"

// QueueAdapter adapts MQTT 5 to QueueBackend using shared subscriptions.
type QueueAdapter struct {
	connArgs ConnArguments
	cm       *autopaho.ConnectionManager
	subs     subCache5
}

// NewQueueAdapter creates a connected MQTT 5 QueueAdapter.
func NewQueueAdapter(args ConnArguments) (*QueueAdapter, error) {
	cm, err := Connect5(args)
	if err != nil {
		return nil, err
	}
	return &QueueAdapter{connArgs: args, cm: cm}, nil
}

// Send implements backends.QueueBackend.
// It publishes the message to topic "queue/{queue-name}" with the configured
// QoS; metadata rides in the native MQTT 5 property slots. A reply-to queue
// name is published as response topic "queue/{reply-to}" so native MQTT 5
// responders and xmc's reply command land in the same place.
func (a *QueueAdapter) Send(ctx context.Context, opts backends.SendOptions) error {
	topic := queueTopicPrefix + opts.Queue
	responseTopic := opts.ReplyTo
	if responseTopic != "" {
		responseTopic = queueTopicPrefix + responseTopic
	}

	pubCtx, cancel := context.WithTimeout(ctx, tokenTimeout)
	defer cancel()
	_, err := a.cm.Publish(pubCtx, &paho.Publish{
		Topic:   topic,
		QoS:     qosFromExtra(opts.Extra),
		Payload: opts.Message,
		Properties: buildPublishProperties(opts.Properties,
			opts.MessageID, opts.CorrelationID, responseTopic, opts.ContentType, opts.TTL),
	})
	if err != nil {
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
func (a *QueueAdapter) Receive(ctx context.Context, opts backends.ReceiveOptions) (*backends.Message, error) {
	qos := qosFromExtra(opts.Extra)

	if !opts.Acknowledge {
		// Peek: use a fresh client with a unique clientID so the broker
		// delivers a copy while shared-group consumers keep theirs.
		peekArgs := a.connArgs
		peekArgs.ClientID = fmt.Sprintf("xmc-peek-%d-%s", os.Getpid(), backends.RandomSuffix())
		cm, err := Connect5(peekArgs)
		if err != nil {
			return nil, fmt.Errorf("peek connect: %w", err)
		}
		defer cm.Disconnect(context.Background()) //nolint:errcheck

		var peekSubs subCache5
		msgCh, err := peekSubs.channelFor(ctx, cm, queueTopicPrefix+opts.Queue, qos)
		if err != nil {
			return nil, err
		}
		return waitForMessage(ctx, msgCh, opts.Timeout, opts.Wait, true)
	}

	// Destructive read via shared subscription – competing consumers.
	group := "xmc"
	if g := opts.Extra["group"]; g != "" {
		group = g
	}
	topic := "$share/" + group + "/" + queueTopicPrefix + opts.Queue

	msgCh, err := a.subs.channelFor(ctx, a.cm, topic, qos)
	if err != nil {
		return nil, err
	}
	return waitForMessage(ctx, msgCh, opts.Timeout, opts.Wait, true)
}

// Close implements backends.QueueBackend.
func (a *QueueAdapter) Close() error {
	return a.cm.Disconnect(context.Background())
}

// qosFromExtra reads the --qos flag value (default 1).
func qosFromExtra(extra map[string]string) byte {
	if v, err := strconv.Atoi(extra["qos"]); err == nil {
		return byte(v)
	}
	return 1
}

// waitForMessage waits on msgCh respecting timeout/wait semantics, using the
// shared TimeoutDuration helper so MQTT follows the same contract as all other
// brokers. Buffered messages from a still-open subscription are drained first.
func waitForMessage(ctx context.Context, msgCh <-chan *paho.Publish, timeout float32, wait bool, stripQueuePrefix bool) (*backends.Message, error) {
	dur := backends.TimeoutDuration(timeout, wait)
	timer := time.NewTimer(dur)
	defer timer.Stop()

	select {
	case msg := <-msgCh:
		return convertPublish(msg, stripQueuePrefix), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, backends.ErrNoMessageAvailable
	}
}
