package amqpcommon

import (
	"context"
	"errors"
	"time"

	"github.com/Azure/go-amqp"
)

// ReceiveOptions configures an AMQP receive operation
type ReceiveOptions struct {
	Queue               string
	Timeout             float32
	Wait                bool     // true = wait indefinitely for a message
	Acknowledge         bool     // true = accept (destructive), false = release (peek)
	SourceCapabilities  []string // e.g. ["queue"] or ["topic"] for Artemis routing
	Selector            string   // JMS-style message selector (AMQP filter)
	DurableSubscription bool     // create a durable subscription
	SubscriptionName    string   // name for durable subscription
}

// QueueBrowser holds a long-lived AMQP receiver opened in distribution-mode
// "copy".  The broker sends copies of messages without removing them; each
// successive Next call advances to the next message in the queue.
type QueueBrowser struct {
	ctx      context.Context
	cancel   context.CancelFunc
	receiver *amqp.Receiver
	timeout  time.Duration
}

// NewQueueBrowser opens a browse cursor over opts.Queue.  The receiver is kept
// open for the lifetime of the browser so the broker can track the position.
func NewQueueBrowser(session *amqp.Session, opts ReceiveOptions) (*QueueBrowser, error) {
	ctx, cancel := context.WithCancel(context.Background())

	receiverOptions := &amqp.ReceiverOptions{
		SourceCapabilities:     opts.SourceCapabilities,
		SourceDistributionMode: amqp.SourceDistributionModeCopy,
		SettlementMode:         amqp.ReceiverSettleModeFirst.Ptr(),
	}
	if opts.Selector != "" {
		receiverOptions.Filters = []amqp.LinkFilter{
			amqp.NewSelectorFilter(opts.Selector),
		}
	}

	receiver, err := session.NewReceiver(ctx, opts.Queue, receiverOptions)
	if err != nil {
		cancel()
		return nil, err
	}

	timeout := time.Duration(float64(opts.Timeout) * float64(time.Second))
	if timeout <= 0 {
		timeout = 200 * time.Millisecond // default end-of-queue detection window
	}

	return &QueueBrowser{ctx: ctx, cancel: cancel, receiver: receiver, timeout: timeout}, nil
}

// Next returns the next message from the queue.  It returns (nil, nil) when the
// queue appears exhausted (timeout with no new message), which the caller should
// map to ErrNoMessageAvailable.  The caller's ctx is honoured for cancellation;
// the browser's own per-call timeout determines how long to wait.
func (b *QueueBrowser) Next(ctx context.Context) (*amqp.Message, error) {
	tctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()

	msg, err := b.receiver.Receive(tctx, nil)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, nil // end of queue (or caller cancelled)
		}
		return nil, err
	}
	// Settle the copy-mode delivery so the broker advances its internal cursor.
	// This does NOT remove the original message from the queue.
	_ = b.receiver.AcceptMessage(ctx, msg)
	return msg, nil
}

// Close tears down the browse receiver and releases all resources.
func (b *QueueBrowser) Close() error {
	defer b.cancel()
	return b.receiver.Close(b.ctx)
}
