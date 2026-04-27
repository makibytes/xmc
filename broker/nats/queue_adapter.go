//go:build nats

package nats

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	natsclient "github.com/nats-io/nats.go"

	"github.com/makibytes/xmc/broker/backends"
)

// QueueAdapter adapts NATS JetStream to the QueueBackend interface.
type QueueAdapter struct {
	nc        *natsclient.Conn
	js        natsclient.JetStreamContext
	consumers map[string]*natsclient.Subscription // cached pull subscribers, keyed by queue name
	ensured   map[string]struct{}                 // stream names already confirmed this process
}

// NewQueueAdapter creates a new NATS JetStream queue adapter.
func NewQueueAdapter(connArgs ConnArguments) (*QueueAdapter, error) {
	nc, js, err := ConnectWithJetStream(connArgs)
	if err != nil {
		return nil, err
	}

	return &QueueAdapter{
		nc:        nc,
		js:        js,
		consumers: make(map[string]*natsclient.Subscription),
		ensured:   make(map[string]struct{}),
	}, nil
}

// Send implements backends.QueueBackend.
func (a *QueueAdapter) Send(ctx context.Context, opts backends.SendOptions) error {
	subject := queueSubject(opts.Queue)

	if err := a.ensureStream(opts.Queue); err != nil {
		return err
	}

	msg := natsclient.NewMsg(subject)
	msg.Data = opts.Message

	if opts.MessageID != "" {
		msg.Header.Set(natsclient.MsgIdHdr, opts.MessageID)
	}
	for k, v := range backends.StringifyProps(opts.Properties) {
		msg.Header.Set(k, v)
	}

	_, err := a.js.PublishMsg(msg)
	return err
}

// Receive implements backends.QueueBackend.
func (a *QueueAdapter) Receive(ctx context.Context, opts backends.ReceiveOptions) (*backends.Message, error) {
	if err := a.ensureStream(opts.Queue); err != nil {
		return nil, err
	}

	sub, err := a.getOrCreateConsumer(opts.Queue)
	if err != nil {
		return nil, err
	}

	timeout := backends.TimeoutDuration(opts.Timeout, opts.Wait)

	msgs, err := sub.Fetch(1, natsclient.MaxWait(timeout))
	if err != nil {
		if errors.Is(err, natsclient.ErrTimeout) {
			return nil, backends.ErrNoMessageAvailable
		}
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, backends.ErrNoMessageAvailable
	}

	m := msgs[0]
	if opts.Acknowledge {
		// AckSync waits for server confirmation, ensuring the message is deleted
		// before the next Fetch on the same consumer.
		if err := m.AckSync(); err != nil {
			return nil, fmt.Errorf("acknowledging message: %w", err)
		}
	} else {
		if err := m.Nak(); err != nil {
			return nil, fmt.Errorf("nacking message: %w", err)
		}
	}

	return natsToBackendMessage(m), nil
}

// getOrCreateConsumer returns a cached pull subscriber for the given queue,
// creating one if it doesn't exist.
func (a *QueueAdapter) getOrCreateConsumer(queue string) (*natsclient.Subscription, error) {
	if sub, ok := a.consumers[queue]; ok {
		return sub, nil
	}

	sub, err := a.js.PullSubscribe(queueSubject(queue), "xmc-consumer",
		natsclient.BindStream(streamName(queue)),
	)
	if err != nil {
		return nil, fmt.Errorf("creating pull subscriber: %w", err)
	}

	a.consumers[queue] = sub
	return sub, nil
}

// Close implements backends.QueueBackend.
func (a *QueueAdapter) Close() error {
	for _, sub := range a.consumers {
		sub.Unsubscribe() //nolint:errcheck
	}
	if a.nc != nil {
		a.nc.Close()
	}
	return nil
}

func (a *QueueAdapter) ensureStream(queue string) error {
	name := streamName(queue)
	if _, ok := a.ensured[name]; ok {
		return nil
	}
	subject := queueSubject(queue)

	// Validate any pre-existing stream: a non-WorkQueue retention or a stream that
	// doesn't route this subject would silently produce wrong delivery semantics.
	info, err := a.js.StreamInfo(name)
	if err == nil {
		if info.Config.Retention != natsclient.WorkQueuePolicy {
			return fmt.Errorf("stream %s has retention %v, expected WorkQueuePolicy", name, info.Config.Retention)
		}
		if !slices.Contains(info.Config.Subjects, subject) {
			return fmt.Errorf("stream %s does not route subject %s (has %v)", name, subject, info.Config.Subjects)
		}
		a.ensured[name] = struct{}{}
		return nil
	}
	if !errors.Is(err, natsclient.ErrStreamNotFound) {
		return fmt.Errorf("fetching stream %s: %w", name, err)
	}

	_, err = a.js.AddStream(&natsclient.StreamConfig{
		Name:      name,
		Subjects:  []string{subject},
		Retention: natsclient.WorkQueuePolicy,
	})
	if err != nil {
		return fmt.Errorf("creating stream %s: %w", name, err)
	}
	a.ensured[name] = struct{}{}
	return nil
}

// streamName returns the JetStream stream name for a queue.
func streamName(queue string) string {
	return fmt.Sprintf("XMC_Q_%s", strings.ToUpper(strings.ReplaceAll(queue, "-", "_")))
}

// queueSubject returns the NATS subject for a queue.
func queueSubject(queue string) string {
	return fmt.Sprintf("xmc.queue.%s", queue)
}

// natsToBackendMessage converts a NATS message to a backends.Message.
func natsToBackendMessage(msg *natsclient.Msg) *backends.Message {
	props := make(map[string]any)
	for k, vals := range msg.Header {
		if len(vals) > 0 {
			props[k] = vals[0]
		}
	}

	return &backends.Message{
		Data:       msg.Data,
		Properties: props,
	}
}
