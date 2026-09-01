package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
)

// ReconnectOptions controls the auto-reconnect wrapper behaviour.
type ReconnectOptions struct {
	// MaxElapsed caps how long the wrapper will keep retrying before giving up.
	// Zero means use the default (5 minutes).
	MaxElapsed time.Duration
}

func (o ReconnectOptions) maxElapsed() time.Duration {
	if o.MaxElapsed > 0 {
		return o.MaxElapsed
	}
	return 5 * time.Minute
}

func newBackoffPolicy(opts ReconnectOptions) backoff.BackOff {
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = 500 * time.Millisecond
	b.MaxInterval = 30 * time.Second
	b.MaxElapsedTime = opts.maxElapsed()
	return b
}

// isConnectionError returns true if the error indicates a genuine transport or
// network failure (connection drop, reset, timeout, etc.) that warrants a
// reconnect attempt. Application-level errors (bad address, protocol violations,
// auth failures, etc.) return false so they surface immediately to the user.
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}

	// Context errors are not connection errors — they indicate intentional
	// cancellation or timeouts set by the caller.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// The single most common result in a polling loop (forward, bridge, --for):
	// "nothing to read this poll" is not a connection error. Checked before
	// the substring scan below, which it would otherwise run on every poll.
	if errors.Is(err, backends.ErrNoMessageAvailable) {
		return false
	}

	// Standard I/O sentinels produced when a connection is closed or broken.
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	// Syscall-level transport errors.
	if errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNABORTED) {
		return true
	}

	// net.Error covers dial/read/write timeouts and other network failures.
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	// Substring fallback for driver-wrapped connection errors that don't
	// implement standard error interfaces. gRPC status errors are matched by
	// string on purpose: importing google.golang.org/grpc in cmd/ would pull
	// gRPC+protobuf into every broker binary just to classify errors that only
	// the gRPC-based brokers (Google Pub/Sub) can produce.
	msg := strings.ToLower(err.Error())
	connectionSubstrings := []string{
		"connection refused",
		"connection reset",
		"connection closed",
		"broken pipe",
		"use of closed network connection",
		"no such host",
		"i/o timeout",
		"unexpected eof",
		// gRPC transport failures ("rpc error: code = Unavailable desc = ...").
		// Unavailable is the canonical retryable transport code; other codes
		// (NotFound, PermissionDenied, ...) are application errors.
		"code = unavailable",
		"transport is closing",
		// NATS initial-connect / reconnect exhaustion.
		"no servers available",
		// Paho MQTT op on a dropped connection.
		"not currently connected",
	}
	for _, sub := range connectionSubstrings {
		if strings.Contains(msg, sub) {
			return true
		}
	}

	return false
}

// --- generic reconnecting adapter ---

// reconnectingAdapter provides shared reconnect logic for any adapter type.
// The factory is called to create a fresh adapter on initial connect and on
// reconnection. Embed this in a type-specific wrapper that adds the domain
// methods (Send/Receive, Publish/Subscribe). Closeable (declared in ping.go)
// is reused here rather than redeclaring the same one-method interface.
type reconnectingAdapter[T Closeable] struct {
	mu      sync.Mutex
	factory func() (T, error)
	opts    ReconnectOptions
	adapter T
	closed  bool
}

func (r *reconnectingAdapter[T]) ensureConnected() error {
	if r.closed {
		return fmt.Errorf("adapter closed")
	}
	if any(r.adapter) != nil {
		return nil
	}
	a, err := r.factory()
	if err != nil {
		return err
	}
	r.adapter = a
	return nil
}

func (r *reconnectingAdapter[T]) reconnect() error {
	if any(r.adapter) != nil {
		_ = r.adapter.Close()
		var zero T
		r.adapter = zero
	}
	a, err := r.factory()
	if err != nil {
		return err
	}
	r.adapter = a
	return nil
}

func (r *reconnectingAdapter[T]) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	if any(r.adapter) != nil {
		err := r.adapter.Close()
		var zero T
		r.adapter = zero
		return err
	}
	return nil
}

// retryOp reconnects and retries op with exponential backoff. The caller must
// hold r.mu.
func (r *reconnectingAdapter[T]) retryOp(ctx context.Context, desc string, op func() error) error {
	b := newBackoffPolicy(r.opts)
	var lastErr error

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if r.closed {
			return fmt.Errorf("%s: adapter closed", desc)
		}

		wait := b.NextBackOff()
		if wait == backoff.Stop {
			return fmt.Errorf("reconnect exhausted for %s: %w", desc, lastErr)
		}

		log.Error("connection lost (%s), reconnecting in %s...\n", desc, wait.Round(time.Millisecond))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}

		if err := r.reconnect(); err != nil {
			lastErr = err
			log.Error("reconnect failed: %s\n", err)
			continue
		}
		log.Verbose("reconnected successfully (%s)", desc)

		err := op()
		if err == nil || !isConnectionError(err) {
			return err
		}
		lastErr = err
	}
}

// adapterDo runs op against the connected adapter of r, retrying with
// reconnect backoff if op fails with a connection error. Shared by the
// wrapper methods that return only an error (Send, Publish).
func adapterDo[T Closeable](ctx context.Context, r *reconnectingAdapter[T], desc string, op func(T) error) error {
	r.mu.Lock()
	if err := r.ensureConnected(); err != nil {
		r.mu.Unlock()
		return err
	}
	adapter := r.adapter
	r.mu.Unlock()

	err := op(adapter)
	if err == nil || !isConnectionError(err) {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	return r.retryOp(ctx, desc, func() error {
		return op(r.adapter)
	})
}

// adapterCall runs op against the connected adapter of r, retrying with
// reconnect backoff if op fails with a connection error. Shared by the
// wrapper methods that return a value plus an error (Receive, Request,
// Subscribe). Methods can't take type parameters, so this — like adapterDo —
// is a free function rather than a method on reconnectingAdapter.
func adapterCall[T Closeable, R any](ctx context.Context, r *reconnectingAdapter[T], desc string, op func(T) (R, error)) (R, error) {
	r.mu.Lock()
	if err := r.ensureConnected(); err != nil {
		r.mu.Unlock()
		var zero R
		return zero, err
	}
	adapter := r.adapter
	r.mu.Unlock()

	result, err := op(adapter)
	if err == nil || !isConnectionError(err) {
		return result, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	var final R
	retryErr := r.retryOp(ctx, desc, func() error {
		var innerErr error
		final, innerErr = op(r.adapter)
		return innerErr
	})
	return final, retryErr
}

// --- reconnecting queue adapter ---

// reconnectingQueue wraps a QueueAdapterFactory and transparently reconnects
// on connection-loss errors.
type reconnectingQueue struct {
	reconnectingAdapter[backends.QueueBackend]
}

func (r *reconnectingQueue) Send(ctx context.Context, opts backends.SendOptions) error {
	return adapterDo(ctx, &r.reconnectingAdapter, fmt.Sprintf("send to %s", opts.Queue), func(a backends.QueueBackend) error {
		return a.Send(ctx, opts)
	})
}

func (r *reconnectingQueue) Receive(ctx context.Context, opts backends.ReceiveOptions) (*backends.Message, error) {
	return adapterCall(ctx, &r.reconnectingAdapter, fmt.Sprintf("receive from %s", opts.Queue), func(a backends.QueueBackend) (*backends.Message, error) {
		return a.Receive(ctx, opts)
	})
}

// Request implements backends.RequestReplyBackend by dispatching through
// backends.Request on the underlying adapter, so a broker's native
// request/reply (Artemis selector matching, NATS private reply queue) is not
// hidden by the wrapper in shell/AI mode; adapters without native support get
// the broker-neutral default. Like Send, a retried request is re-sent, so
// reconnect keeps at-least-once semantics.
func (r *reconnectingQueue) Request(ctx context.Context, opts backends.RequestOptions) (*backends.Message, error) {
	return adapterCall(ctx, &r.reconnectingAdapter, fmt.Sprintf("request to %s", opts.Address), func(a backends.QueueBackend) (*backends.Message, error) {
		return backends.Request(ctx, a, opts)
	})
}

// Browse implements backends.BrowseBackend by delegating to the underlying
// adapter if it supports browsing. Returns errBrowseUnsupported if the
// adapter does not implement BrowseBackend, allowing doReceive to fall back
// to the normal Receive loop for brokers that don't have a browse cursor.
func (r *reconnectingQueue) Browse(ctx context.Context, opts backends.ReceiveOptions) (backends.Browser, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.ensureConnected(); err != nil {
		return nil, err
	}

	bb, ok := r.adapter.(backends.BrowseBackend)
	if !ok {
		return nil, backends.ErrBrowseUnsupported
	}
	return bb.Browse(ctx, opts)
}

// --- reconnecting topic adapter ---

// reconnectingTopic wraps a TopicAdapterFactory and transparently reconnects
// on connection-loss errors.
type reconnectingTopic struct {
	reconnectingAdapter[backends.TopicBackend]
}

func (r *reconnectingTopic) Publish(ctx context.Context, opts backends.PublishOptions) error {
	return adapterDo(ctx, &r.reconnectingAdapter, fmt.Sprintf("publish to %s", opts.Topic), func(a backends.TopicBackend) error {
		return a.Publish(ctx, opts)
	})
}

func (r *reconnectingTopic) Subscribe(ctx context.Context, opts backends.SubscribeOptions) (*backends.Message, error) {
	return adapterCall(ctx, &r.reconnectingAdapter, fmt.Sprintf("subscribe on %s", opts.Topic), func(a backends.TopicBackend) (*backends.Message, error) {
		return a.Subscribe(ctx, opts)
	})
}

// --- factory wrappers ---

// wrapReconnectQueue returns a new factory that produces a reconnecting queue
// adapter. The wrapper lazily connects on first use and transparently
// reconnects with exponential backoff on connection-loss errors.
// A nil factory (broker without queue support) yields nil, so callers'
// nil-capability checks keep working on the wrapped factory.
func wrapReconnectQueue(factory QueueAdapterFactory, opts ReconnectOptions) QueueAdapterFactory {
	if factory == nil {
		return nil
	}
	rq := &reconnectingQueue{factory: factory, opts: opts}
	return func() (backends.QueueBackend, error) {
		return rq, nil
	}
}

// wrapReconnectTopic returns a new factory that produces a reconnecting topic
// adapter. A nil factory (broker without topic support) yields nil.
func wrapReconnectTopic(factory TopicAdapterFactory, opts ReconnectOptions) TopicAdapterFactory {
	if factory == nil {
		return nil
	}
	rt := &reconnectingTopic{factory: factory, opts: opts}
	return func() (backends.TopicBackend, error) {
		return rt, nil
	}
}

// conditionalReconnectQueue returns a factory that checks the --reconnect flag
// at call time and wraps the adapter with auto-reconnect if set.
func conditionalReconnectQueue(factory QueueAdapterFactory, rootCmd *cobra.Command) QueueAdapterFactory {
	if factory == nil {
		return nil
	}
	return func() (backends.QueueBackend, error) {
		reconnect, _ := rootCmd.Flags().GetBool("reconnect")
		if !reconnect {
			return factory()
		}
		window, _ := rootCmd.Flags().GetDuration("reconnect-window")
		rq := &reconnectingQueue{factory: factory, opts: ReconnectOptions{MaxElapsed: window}}
		return rq, nil
	}
}

// conditionalReconnectTopic returns a factory that checks the --reconnect flag
// at call time and wraps the adapter with auto-reconnect if set.
func conditionalReconnectTopic(factory TopicAdapterFactory, rootCmd *cobra.Command) TopicAdapterFactory {
	if factory == nil {
		return nil
	}
	return func() (backends.TopicBackend, error) {
		reconnect, _ := rootCmd.Flags().GetBool("reconnect")
		if !reconnect {
			return factory()
		}
		window, _ := rootCmd.Flags().GetDuration("reconnect-window")
		rt := &reconnectingTopic{factory: factory, opts: ReconnectOptions{MaxElapsed: window}}
		return rt, nil
	}
}
