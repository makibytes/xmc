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
	// implement standard error interfaces.
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
	}
	for _, sub := range connectionSubstrings {
		if strings.Contains(msg, sub) {
			return true
		}
	}

	return false
}

// --- reconnecting queue adapter ---

// reconnectingQueue wraps a QueueAdapterFactory and transparently reconnects
// on connection-loss errors.
type reconnectingQueue struct {
	mu      sync.Mutex
	factory QueueAdapterFactory
	opts    ReconnectOptions
	adapter backends.QueueBackend
}

func (r *reconnectingQueue) ensureConnected() error {
	if r.adapter != nil {
		return nil
	}
	a, err := r.factory()
	if err != nil {
		return err
	}
	r.adapter = a
	return nil
}

func (r *reconnectingQueue) reconnect() error {
	if r.adapter != nil {
		_ = r.adapter.Close()
		r.adapter = nil
	}
	a, err := r.factory()
	if err != nil {
		return err
	}
	r.adapter = a
	return nil
}

func (r *reconnectingQueue) Send(ctx context.Context, opts backends.SendOptions) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.ensureConnected(); err != nil {
		return err
	}

	err := r.adapter.Send(ctx, opts)
	if err == nil || !isConnectionError(err) {
		return err
	}

	return r.retryOp(ctx, fmt.Sprintf("send to %s", opts.Queue), func() error {
		return r.adapter.Send(ctx, opts)
	})
}

func (r *reconnectingQueue) Receive(ctx context.Context, opts backends.ReceiveOptions) (*backends.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.ensureConnected(); err != nil {
		return nil, err
	}

	msg, err := r.adapter.Receive(ctx, opts)
	if err == nil || !isConnectionError(err) {
		return msg, err
	}

	var result *backends.Message
	retryErr := r.retryOp(ctx, fmt.Sprintf("receive from %s", opts.Queue), func() error {
		var innerErr error
		result, innerErr = r.adapter.Receive(ctx, opts)
		return innerErr
	})
	return result, retryErr
}

func (r *reconnectingQueue) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.adapter != nil {
		err := r.adapter.Close()
		r.adapter = nil
		return err
	}
	return nil
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

// retryOp reconnects and retries op with exponential backoff. The caller must
// hold r.mu.
func (r *reconnectingQueue) retryOp(ctx context.Context, desc string, op func() error) error {
	b := newBackoffPolicy(r.opts)
	var lastErr error

	for {
		if ctx.Err() != nil {
			return ctx.Err()
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

// --- reconnecting topic adapter ---

// reconnectingTopic wraps a TopicAdapterFactory and transparently reconnects
// on connection-loss errors.
type reconnectingTopic struct {
	mu      sync.Mutex
	factory TopicAdapterFactory
	opts    ReconnectOptions
	adapter backends.TopicBackend
}

func (r *reconnectingTopic) ensureConnected() error {
	if r.adapter != nil {
		return nil
	}
	a, err := r.factory()
	if err != nil {
		return err
	}
	r.adapter = a
	return nil
}

func (r *reconnectingTopic) reconnect() error {
	if r.adapter != nil {
		_ = r.adapter.Close()
		r.adapter = nil
	}
	a, err := r.factory()
	if err != nil {
		return err
	}
	r.adapter = a
	return nil
}

func (r *reconnectingTopic) Publish(ctx context.Context, opts backends.PublishOptions) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.ensureConnected(); err != nil {
		return err
	}

	err := r.adapter.Publish(ctx, opts)
	if err == nil || !isConnectionError(err) {
		return err
	}

	return r.retryOp(ctx, fmt.Sprintf("publish to %s", opts.Topic), func() error {
		return r.adapter.Publish(ctx, opts)
	})
}

func (r *reconnectingTopic) Subscribe(ctx context.Context, opts backends.SubscribeOptions) (*backends.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.ensureConnected(); err != nil {
		return nil, err
	}

	msg, err := r.adapter.Subscribe(ctx, opts)
	if err == nil || !isConnectionError(err) {
		return msg, err
	}

	var result *backends.Message
	retryErr := r.retryOp(ctx, fmt.Sprintf("subscribe on %s", opts.Topic), func() error {
		var innerErr error
		result, innerErr = r.adapter.Subscribe(ctx, opts)
		return innerErr
	})
	return result, retryErr
}

func (r *reconnectingTopic) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.adapter != nil {
		err := r.adapter.Close()
		r.adapter = nil
		return err
	}
	return nil
}

func (r *reconnectingTopic) retryOp(ctx context.Context, desc string, op func() error) error {
	b := newBackoffPolicy(r.opts)
	var lastErr error

	for {
		if ctx.Err() != nil {
			return ctx.Err()
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

// --- factory wrappers ---

// wrapReconnectQueue returns a new factory that produces a reconnecting queue
// adapter. The wrapper lazily connects on first use and transparently
// reconnects with exponential backoff on connection-loss errors.
func wrapReconnectQueue(factory QueueAdapterFactory, opts ReconnectOptions) QueueAdapterFactory {
	rq := &reconnectingQueue{factory: factory, opts: opts}
	return func() (backends.QueueBackend, error) {
		return rq, nil
	}
}

// wrapReconnectTopic returns a new factory that produces a reconnecting topic
// adapter.
func wrapReconnectTopic(factory TopicAdapterFactory, opts ReconnectOptions) TopicAdapterFactory {
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
