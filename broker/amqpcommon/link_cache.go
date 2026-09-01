package amqpcommon

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Azure/go-amqp"
	"github.com/makibytes/xmc/log"
)

// nextLinkID names every AMQP link this process attaches (send or receive,
// either broker), so link names stay unique without each caller keeping its
// own counter.
var nextLinkID atomic.Uint64

// closeLinkCtx returns a context for closing a link (DETACH). A fresh
// background-derived context is used so the handshake always completes even
// if the operation's own ctx was already cancelled or timed out.
func closeLinkCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// SendOptions configures the sender a SenderCache attaches: everything that
// determines the AMQP link's identity, plus the broker-specific per-message
// extras (Artemis's ANYCAST/MULTICAST delivery annotation) that a plain
// destination string can't carry.
type SendOptions struct {
	Address             string
	TargetCapabilities  []string
	Durable             bool
	LinkPrefix          string // link-name prefix, e.g. "amc", "rmc"
	DeliveryAnnotations amqp.Annotations
}

func sendCacheKey(o SendOptions) string {
	return o.Address + "\x00" + strconv.FormatBool(o.Durable) + "\x00" + strings.Join(o.TargetCapabilities, ",")
}

// SenderCache holds one AMQP sender per adapter, re-attaching only when the
// destination or its link properties change. A single command invocation
// typically sends many messages to the same destination with identical
// properties (send -l/--ndjson/-n, forward, bridge), so caching collapses
// what would otherwise be one ATTACH/DETACH round-trip per message into one
// for the whole run — the same idea QueueBrowser already applies by holding
// one long-lived receiver across its Next() calls.
//
// The zero value is ready to use. Not safe for concurrent Send calls (no
// caller in this codebase drives one adapter from multiple goroutines); Close
// may be called concurrently with nothing in flight.
type SenderCache struct {
	mu     sync.Mutex
	key    string
	sender *amqp.Sender
}

// Send sends message via the cached sender for opts, (re)attaching first if
// opts describes a different link than the one currently cached, or if
// nothing is cached yet.
func (c *SenderCache) Send(ctx context.Context, session *amqp.Session, opts SendOptions, message *amqp.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(opts.DeliveryAnnotations) > 0 {
		message.DeliveryAnnotations = opts.DeliveryAnnotations
	}

	if key := sendCacheKey(opts); c.sender == nil || c.key != key {
		_ = c.closeLocked()

		durability := LinkDurability(opts.Durable)
		sender, err := session.NewSender(ctx, opts.Address, &amqp.SenderOptions{
			Durability:         durability,
			TargetCapabilities: opts.TargetCapabilities,
			TargetDurability:   durability,
			Name:               fmt.Sprintf("%s-%d", opts.LinkPrefix, nextLinkID.Add(1)),
		})
		if err != nil {
			return err
		}
		c.sender, c.key = sender, key
	}

	if err := c.sender.Send(ctx, message, nil); err != nil {
		// The link may have been detached from under us (e.g. the broker
		// closed it); drop the cache so the next call attaches fresh instead
		// of repeatedly hitting a dead link.
		c.sender, c.key = nil, ""
		return err
	}
	return nil
}

// Close detaches the cached sender, if any. Safe to call when nothing is cached.
func (c *SenderCache) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeLocked()
}

func (c *SenderCache) closeLocked() error {
	if c.sender == nil {
		return nil
	}
	closeCtx, cancel := closeLinkCtx()
	defer cancel()
	err := c.sender.Close(closeCtx)
	c.sender, c.key = nil, ""
	return err
}

func recvCacheKey(o ReceiveOptions) string {
	return o.Queue + "\x00" + strings.Join(o.SourceCapabilities, ",") + "\x00" + o.Selector + "\x00" +
		strconv.FormatBool(o.DurableSubscription) + "\x00" + o.SubscriptionName
}

// ReceiverCache holds one AMQP receiver per adapter, re-attaching only when
// the source or its link properties change. Mirrors SenderCache; see its doc
// for the round-trip rationale, which applies identically here. For an
// ephemeral (non-durable, non-durable-subscription) topic subscription,
// caching also means messages published while the receiver is idle between
// polls are captured instead of lost: the broker only tears down an
// ephemeral subscription on DETACH, and the old per-call reattachment
// DETACHed after every single message.
//
// The zero value is ready to use. Not safe for concurrent Receive calls (no
// caller in this codebase drives one adapter from multiple goroutines); Close
// may be called concurrently with nothing in flight.
type ReceiverCache struct {
	mu       sync.Mutex
	key      string
	receiver *amqp.Receiver
}

// newReceiver attaches a receiver for opts. Factored out so the cached and
// uncached paths build identical link options.
func newReceiver(ctx context.Context, session *amqp.Session, opts ReceiveOptions) (*amqp.Receiver, error) {
	durability := LinkDurability(opts.DurableSubscription)
	expiryPolicy := amqp.ExpiryPolicyLinkDetach
	linkName := fmt.Sprintf("xmc-%d", nextLinkID.Add(1))
	if opts.DurableSubscription {
		expiryPolicy = amqp.ExpiryPolicyNever
		if opts.SubscriptionName != "" {
			linkName = opts.SubscriptionName
		}
	}

	receiverOptions := &amqp.ReceiverOptions{
		SourceCapabilities: opts.SourceCapabilities,
		SourceExpiryPolicy: expiryPolicy,
		Durability:         durability,
		Name:               linkName,
		SourceDurability:   durability,
		SettlementMode:     amqp.ReceiverSettleModeFirst.Ptr(),
	}
	if opts.Selector != "" {
		log.Verbose("applying selector filter: %s", opts.Selector)
		receiverOptions.Filters = []amqp.LinkFilter{amqp.NewSelectorFilter(opts.Selector)}
	}

	log.Verbose("generating receiver for %s...", opts.Queue)
	return session.NewReceiver(ctx, opts.Queue, receiverOptions)
}

// receiveCtx derives the per-call context a Receive waits on: unbounded
// (cancellable) when opts.Wait, otherwise bounded by opts.Timeout.
func receiveWaitCtx(ctx context.Context, opts ReceiveOptions) (context.Context, context.CancelFunc) {
	if opts.Wait {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, time.Duration(float64(opts.Timeout)*float64(time.Second)))
}

// Receive receives one message via the cached receiver for opts, (re)attaching
// first if opts describes a different link than the one currently cached, or
// if nothing is cached yet. The caller's ctx is honoured for cancellation
// (Ctrl-C/Esc); opts.Wait/opts.Timeout govern how long this call itself waits
// for a message once attached.
//
// A non-destructive read (opts.Acknowledge == false) never uses the cache:
// releasing a message while the link stays attached can make the broker
// prefer redelivering it back to this same link, so a different receiver's
// subsequent peek may not see the released message promptly (or at all)
// — confirmed against a live broker. Every release therefore still gets its
// own attach/receive/release/detach cycle, exactly as before caching existed.
// This path is inherently low-frequency for both AMQP brokers: Artemis and
// RabbitMQ each implement true cursor browsing (see QueueBrowser) for
// repeated peeks, so a plain release only happens for a single one-shot peek.
func (c *ReceiverCache) Receive(ctx context.Context, session *amqp.Session, opts ReceiveOptions) (*amqp.Message, error) {
	if !opts.Acknowledge {
		return receiveOnce(ctx, session, opts)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if key := recvCacheKey(opts); c.receiver == nil || c.key != key {
		_ = c.closeLocked()
		receiver, err := newReceiver(ctx, session, opts)
		if err != nil {
			return nil, err
		}
		c.receiver, c.key = receiver, key
	}

	receiveCtx, cancel := receiveWaitCtx(ctx, opts)
	defer cancel()

	log.Verbose("calling receive()...")
	message, err := c.receiver.Receive(receiveCtx, nil)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			// Not a link failure — an empty poll (or caller cancellation)
			// leaves the receiver perfectly usable for the next call, exactly
			// as QueueBrowser already treats its own timeout case. Keep it
			// cached.
			return nil, err
		}
		// Anything else likely means the link is no longer usable; drop the
		// cache so the next call attaches fresh instead of repeatedly hitting
		// a dead link.
		c.receiver, c.key = nil, ""
		return nil, err
	}

	if err := c.receiver.AcceptMessage(receiveCtx, message); err != nil {
		c.receiver, c.key = nil, ""
		return nil, fmt.Errorf("accepting message: %w", err)
	}

	return message, nil
}

// receiveOnce performs one full attach → receive → release → detach cycle,
// never touching a ReceiverCache. Used for non-destructive (Acknowledge=false)
// reads; see the Acknowledge==false case in Receive for why those must not be
// cached.
func receiveOnce(ctx context.Context, session *amqp.Session, opts ReceiveOptions) (*amqp.Message, error) {
	receiveCtx, cancel := receiveWaitCtx(ctx, opts)
	defer cancel()

	receiver, err := newReceiver(ctx, session, opts)
	if err != nil {
		return nil, err
	}
	defer func() {
		closeCtx, cancel := closeLinkCtx()
		defer cancel()
		_ = receiver.Close(closeCtx)
	}()

	log.Verbose("calling receive()...")
	message, err := receiver.Receive(receiveCtx, nil)
	if err != nil {
		return nil, err
	}

	if err := receiver.ReleaseMessage(receiveCtx, message); err != nil {
		return nil, fmt.Errorf("releasing message: %w", err)
	}

	return message, nil
}

// Close detaches the cached receiver, if any. Safe to call when nothing is cached.
func (c *ReceiverCache) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeLocked()
}

func (c *ReceiverCache) closeLocked() error {
	if c.receiver == nil {
		return nil
	}
	closeCtx, cancel := closeLinkCtx()
	defer cancel()
	err := c.receiver.Close(closeCtx)
	c.receiver, c.key = nil, ""
	return err
}
