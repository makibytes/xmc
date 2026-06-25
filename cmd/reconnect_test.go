package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/makibytes/xmc/broker/backends"
)

// failingQueueBackend fails the first N operations, then succeeds.
type failingQueueBackend struct {
	failCount   int32 // atomic: how many more ops should fail
	sendCount   atomic.Int32
	recvCount   atomic.Int32
	lastSendMsg []byte
}

func (f *failingQueueBackend) Send(_ context.Context, opts backends.SendOptions) error {
	f.sendCount.Add(1)
	if atomic.AddInt32(&f.failCount, -1) >= 0 {
		return fmt.Errorf("connection reset by peer")
	}
	f.lastSendMsg = opts.Message
	return nil
}

func (f *failingQueueBackend) Receive(_ context.Context, opts backends.ReceiveOptions) (*backends.Message, error) {
	f.recvCount.Add(1)
	if atomic.AddInt32(&f.failCount, -1) >= 0 {
		return nil, fmt.Errorf("connection reset by peer")
	}
	return &backends.Message{Data: []byte("msg")}, nil
}

func (f *failingQueueBackend) Close() error { return nil }

func TestReconnectingQueue_RetriesOnError(t *testing.T) {
	mock := &failingQueueBackend{failCount: 2} // fail twice then succeed
	callCount := 0

	factory := func() (backends.QueueBackend, error) {
		callCount++
		return mock, nil
	}

	rq := &reconnectingQueue{
		factory: factory,
		opts:    ReconnectOptions{MaxElapsed: 10 * time.Second},
	}

	err := rq.Send(context.Background(), backends.SendOptions{
		Queue:   "q",
		Message: []byte("hello"),
	})
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}

	// The factory should have been called at least once for reconnect
	// (initial ensureConnected + reconnect attempts).
	if callCount < 2 {
		t.Errorf("factory called %d times, expected at least 2 (initial + reconnect)", callCount)
	}
}

func TestReconnectingQueue_SentinelNotRetried(t *testing.T) {
	mock := &mockQueueBackend{receiveErr: backends.ErrNoMessageAvailable}
	factory := func() (backends.QueueBackend, error) {
		return mock, nil
	}

	rq := &reconnectingQueue{
		factory: factory,
		opts:    ReconnectOptions{MaxElapsed: 5 * time.Second},
	}

	_, err := rq.Receive(context.Background(), backends.ReceiveOptions{Queue: "q"})
	if err != backends.ErrNoMessageAvailable {
		t.Fatalf("expected ErrNoMessageAvailable, got: %v", err)
	}
	// Should NOT have retried — only 1 call.
	if mock.receiveCount != 1 {
		t.Errorf("receiveCount = %d, want 1 (sentinel should not trigger retry)", mock.receiveCount)
	}
}

func TestReconnectingQueue_ExhaustsBackoff(t *testing.T) {
	alwaysFail := &failingQueueBackend{failCount: 1000}
	factory := func() (backends.QueueBackend, error) {
		return alwaysFail, nil
	}

	rq := &reconnectingQueue{
		factory: factory,
		opts:    ReconnectOptions{MaxElapsed: 1 * time.Second}, // short window
	}

	err := rq.Send(context.Background(), backends.SendOptions{
		Queue:   "q",
		Message: []byte("x"),
	})
	if err == nil {
		t.Fatal("expected error after backoff exhaustion, got nil")
	}
	if !strings.Contains(err.Error(), "reconnect exhausted") {
		t.Errorf("error = %q, expected to contain 'reconnect exhausted'", err.Error())
	}
}

func TestReconnectingQueue_FactoryError(t *testing.T) {
	factory := func() (backends.QueueBackend, error) {
		return nil, fmt.Errorf("auth failed")
	}

	rq := &reconnectingQueue{
		factory: factory,
		opts:    ReconnectOptions{MaxElapsed: 1 * time.Second},
	}

	err := rq.Send(context.Background(), backends.SendOptions{Queue: "q"})
	if err == nil {
		t.Fatal("expected error from factory, got nil")
	}
}

func TestReconnectingQueue_Close(t *testing.T) {
	mock := &mockQueueBackend{receiveMsg: &backends.Message{Data: []byte("x")}}
	factory := func() (backends.QueueBackend, error) {
		return mock, nil
	}

	rq := &reconnectingQueue{factory: factory, opts: ReconnectOptions{}}

	// Use it to establish the connection.
	_, _ = rq.Receive(context.Background(), backends.ReceiveOptions{Queue: "q"})

	// Close should work.
	if err := rq.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	// Adapter should be nil after close.
	if rq.adapter != nil {
		t.Error("adapter should be nil after Close")
	}
}

// failingTopicBackend fails the first N operations, then succeeds.
type failingTopicBackend struct {
	failCount int32
	pubCount  atomic.Int32
	subCount  atomic.Int32
}

func (f *failingTopicBackend) Publish(_ context.Context, opts backends.PublishOptions) error {
	f.pubCount.Add(1)
	if atomic.AddInt32(&f.failCount, -1) >= 0 {
		return fmt.Errorf("connection reset by peer")
	}
	return nil
}

func (f *failingTopicBackend) Subscribe(_ context.Context, opts backends.SubscribeOptions) (*backends.Message, error) {
	f.subCount.Add(1)
	if atomic.AddInt32(&f.failCount, -1) >= 0 {
		return nil, fmt.Errorf("connection reset by peer")
	}
	return &backends.Message{Data: []byte("topic-msg")}, nil
}

func (f *failingTopicBackend) Close() error { return nil }

func TestReconnectingTopic_RetriesOnError(t *testing.T) {
	mock := &failingTopicBackend{failCount: 1}
	factory := func() (backends.TopicBackend, error) {
		return mock, nil
	}

	rt := &reconnectingTopic{factory: factory, opts: ReconnectOptions{MaxElapsed: 10 * time.Second}}

	msg, err := rt.Subscribe(context.Background(), backends.SubscribeOptions{Topic: "t"})
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if string(msg.Data) != "topic-msg" {
		t.Errorf("msg.Data = %q, want %q", msg.Data, "topic-msg")
	}
}

// --- isConnectionError tests ---

func TestIsConnectionError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"io.EOF", io.EOF, true},
		{"io.ErrUnexpectedEOF", io.ErrUnexpectedEOF, true},
		{"ECONNRESET", syscall.ECONNRESET, true},
		{"ECONNREFUSED", syscall.ECONNREFUSED, true},
		{"EPIPE", syscall.EPIPE, true},
		{"ECONNABORTED", syscall.ECONNABORTED, true},
		{"net.Error timeout", &net.DNSError{Err: "timeout", IsTimeout: true}, true},
		{"wrapped EOF", fmt.Errorf("read: %w", io.EOF), true},
		{"connection reset substring", fmt.Errorf("connection reset by peer"), true},
		{"connection refused substring", fmt.Errorf("dial tcp: connection refused"), true},
		{"connection closed substring", fmt.Errorf("connection closed"), true},
		{"broken pipe substring", fmt.Errorf("write: broken pipe"), true},
		{"closed network conn", fmt.Errorf("use of closed network connection"), true},
		{"no such host", fmt.Errorf("lookup foo: no such host"), true},
		{"i/o timeout", fmt.Errorf("read tcp: i/o timeout"), true},
		{"unexpected eof", fmt.Errorf("unexpected eof in data"), true},
		{"amqp not found", fmt.Errorf("amqp:not-found: no queue 'foo'"), false},
		{"precondition failed", fmt.Errorf("amqp:precondition-failed"), false},
		{"auth failure", fmt.Errorf("SASL PLAIN auth failed"), false},
		{"generic app error", errors.New("invalid argument"), false},
		{"context canceled", context.Canceled, false},
		{"context deadline", context.DeadlineExceeded, false},
		{"no message available", backends.ErrNoMessageAvailable, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isConnectionError(tt.err)
			if got != tt.want {
				t.Errorf("isConnectionError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestReconnectingQueue_AppErrorNotRetried verifies that non-connection
// errors (e.g. bad address, protocol errors) surface immediately without retry.
func TestReconnectingQueue_AppErrorNotRetried(t *testing.T) {
	sendErr := fmt.Errorf("amqp:not-found: no queue 'bad'")
	mock := &mockQueueBackend{sendErr: sendErr}
	callCount := 0
	factory := func() (backends.QueueBackend, error) {
		callCount++
		return mock, nil
	}

	rq := &reconnectingQueue{
		factory: factory,
		opts:    ReconnectOptions{MaxElapsed: 5 * time.Second},
	}

	err := rq.Send(context.Background(), backends.SendOptions{Queue: "bad", Message: []byte("x")})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "amqp:not-found") {
		t.Errorf("error = %q, want to contain 'amqp:not-found'", err.Error())
	}
	// Should NOT have reconnected — factory called once for initial connect only.
	if callCount > 1 {
		t.Errorf("factory called %d times, want 1 (app error should not trigger reconnect)", callCount)
	}
}

func TestConditionalReconnectQueue_Nil(t *testing.T) {
	result := conditionalReconnectQueue(nil, nil)
	if result != nil {
		t.Error("expected nil factory for nil input")
	}
}

func TestConditionalReconnectTopic_Nil(t *testing.T) {
	result := conditionalReconnectTopic(nil, nil)
	if result != nil {
		t.Error("expected nil factory for nil input")
	}
}

