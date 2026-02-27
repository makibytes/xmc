//go:build nats

package nats

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	natsclient "github.com/nats-io/nats.go"

	"github.com/makibytes/xmc/broker/backends"
)

// QueueAdapter adapts NATS JetStream to the QueueBackend interface.
type QueueAdapter struct {
	connArgs ConnArguments
	nc       *natsclient.Conn
	js       natsclient.JetStreamContext
}

// NewQueueAdapter creates a new NATS JetStream queue adapter.
func NewQueueAdapter(connArgs ConnArguments) (*QueueAdapter, error) {
	nc, js, err := ConnectWithJetStream(connArgs)
	if err != nil {
		return nil, err
	}

	return &QueueAdapter{
		connArgs: connArgs,
		nc:       nc,
		js:       js,
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
	for k, v := range opts.Properties {
		msg.Header.Set(k, fmt.Sprintf("%v", v))
	}

	_, err := a.js.PublishMsg(msg)
	return err
}

// Receive implements backends.QueueBackend.
func (a *QueueAdapter) Receive(ctx context.Context, opts backends.ReceiveOptions) (*backends.Message, error) {
	if err := a.ensureStream(opts.Queue); err != nil {
		return nil, err
	}

	sub, err := a.js.PullSubscribe(queueSubject(opts.Queue), "xmc-consumer",
		natsclient.BindStream(streamName(opts.Queue)),
	)
	if err != nil {
		return nil, fmt.Errorf("creating pull subscriber: %w", err)
	}
	defer sub.Unsubscribe() //nolint:errcheck

	timeout := receiveTimeout(opts.Timeout, opts.Wait)

	msgs, err := sub.Fetch(1, natsclient.MaxWait(timeout))
	if err != nil {
		if errors.Is(err, natsclient.ErrTimeout) {
			return nil, errors.New("no message available")
		}
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, errors.New("no message available")
	}

	m := msgs[0]
	if opts.Acknowledge {
		if err := m.Ack(); err != nil {
			return nil, fmt.Errorf("acknowledging message: %w", err)
		}
	} else {
		if err := m.Nak(); err != nil {
			return nil, fmt.Errorf("nacking message: %w", err)
		}
	}

	return natsToBackendMessage(m), nil
}

// Close implements backends.QueueBackend.
func (a *QueueAdapter) Close() error {
	if a.nc != nil {
		a.nc.Close()
	}
	return nil
}

func (a *QueueAdapter) ensureStream(queue string) error {
	name := streamName(queue)
	subject := queueSubject(queue)

	_, err := a.js.StreamInfo(name)
	if err == nil {
		return nil
	}
	if !errors.Is(err, natsclient.ErrStreamNotFound) {
		return fmt.Errorf("checking stream %s: %w", name, err)
	}

	_, err = a.js.AddStream(&natsclient.StreamConfig{
		Name:      name,
		Subjects:  []string{subject},
		Retention: natsclient.WorkQueuePolicy,
	})
	if err != nil {
		return fmt.Errorf("creating stream %s: %w", name, err)
	}

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

// receiveTimeout converts ReceiveOptions timeout fields to a time.Duration.
func receiveTimeout(timeout float32, wait bool) time.Duration {
	if wait {
		return 24 * time.Hour
	}
	if timeout <= 0 {
		return 5 * time.Second
	}
	return time.Duration(float64(timeout) * float64(time.Second))
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
