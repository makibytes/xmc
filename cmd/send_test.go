package cmd

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/makibytes/xmc/broker/backends"
)

// mockQueueBackend is a test double for QueueBackend
type mockQueueBackend struct {
	lastSendOpts    backends.SendOptions
	lastReceiveOpts backends.ReceiveOptions
	sendErr         error
	sendCount       int // number of times Send was called
	receiveMsg      *backends.Message
	receiveMsgs     []*backends.Message // multiple messages for count testing
	receiveErr      error
	receiveCount    int // number of times Receive was called
}

func (m *mockQueueBackend) Send(_ context.Context, opts backends.SendOptions) error {
	m.lastSendOpts = opts
	m.sendCount++
	return m.sendErr
}

func (m *mockQueueBackend) Receive(_ context.Context, opts backends.ReceiveOptions) (*backends.Message, error) {
	m.lastReceiveOpts = opts
	m.receiveCount++
	if len(m.receiveMsgs) > 0 {
		idx := m.receiveCount - 1
		if idx < len(m.receiveMsgs) {
			return m.receiveMsgs[idx], nil
		}
		return nil, m.receiveErr
	}
	return m.receiveMsg, m.receiveErr
}

func (m *mockQueueBackend) Close() error { return nil }

func TestSendCommand_WithMessageArg(t *testing.T) {
	mock := &mockQueueBackend{}
	cmd := NewSendCommand(mock)
	cmd.SetArgs([]string{"test-queue", "hello world"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.lastSendOpts.Queue != "test-queue" {
		t.Errorf("queue = %q, want %q", mock.lastSendOpts.Queue, "test-queue")
	}
	if !bytes.Equal(mock.lastSendOpts.Message, []byte("hello world")) {
		t.Errorf("message = %q, want %q", mock.lastSendOpts.Message, "hello world")
	}
	if mock.lastSendOpts.ContentType != "text/plain" {
		t.Errorf("contenttype = %q, want %q", mock.lastSendOpts.ContentType, "text/plain")
	}
	if mock.lastSendOpts.Priority != 4 {
		t.Errorf("priority = %d, want %d", mock.lastSendOpts.Priority, 4)
	}
}

func TestSendCommand_WithFlags(t *testing.T) {
	mock := &mockQueueBackend{}
	cmd := NewSendCommand(mock)
	cmd.SetArgs([]string{
		"my-queue", "test-msg",
		"-T", "application/json",
		"-C", "corr-123",
		"-I", "msg-456",
		"-Y", "7",
		"-d",
		"-R", "reply-queue",
		"-P", "key1=value1",
		"-P", "key2=value2",
	})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	opts := mock.lastSendOpts
	if opts.ContentType != "application/json" {
		t.Errorf("contenttype = %q, want %q", opts.ContentType, "application/json")
	}
	if opts.CorrelationID != "corr-123" {
		t.Errorf("correlationid = %q, want %q", opts.CorrelationID, "corr-123")
	}
	if opts.MessageID != "msg-456" {
		t.Errorf("messageid = %q, want %q", opts.MessageID, "msg-456")
	}
	if opts.Priority != 7 {
		t.Errorf("priority = %d, want %d", opts.Priority, 7)
	}
	if !opts.Persistent {
		t.Error("persistent = false, want true")
	}
	if opts.ReplyTo != "reply-queue" {
		t.Errorf("replyto = %q, want %q", opts.ReplyTo, "reply-queue")
	}
	if len(opts.Properties) != 2 {
		t.Fatalf("properties count = %d, want 2", len(opts.Properties))
	}
	if opts.Properties["key1"] != "value1" {
		t.Errorf("property key1 = %v, want %q", opts.Properties["key1"], "value1")
	}
	if opts.Properties["key2"] != "value2" {
		t.Errorf("property key2 = %v, want %q", opts.Properties["key2"], "value2")
	}
}

func TestSendCommand_InvalidProperty(t *testing.T) {
	mock := &mockQueueBackend{}
	cmd := NewSendCommand(mock)
	cmd.SetArgs([]string{"my-queue", "msg", "-P", "invalid-no-equals"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid property, got nil")
	}
}

func TestSendCommand_NoMessage(t *testing.T) {
	mock := &mockQueueBackend{}
	cmd := NewSendCommand(mock)
	cmd.SetArgs([]string{"my-queue"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing message, got nil")
	}
}

func TestSendCommand_CountFlag(t *testing.T) {
	mock := &mockQueueBackend{}
	cmd := NewSendCommand(mock)
	cmd.SetArgs([]string{"test-queue", "hello", "-n", "5"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.sendCount != 5 {
		t.Errorf("sendCount = %d, want 5", mock.sendCount)
	}
}

func TestSendCommand_TTLFlag(t *testing.T) {
	mock := &mockQueueBackend{}
	cmd := NewSendCommand(mock)
	cmd.SetArgs([]string{"test-queue", "hello", "-E", "5000"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.lastSendOpts.TTL != 5000 {
		t.Errorf("TTL = %d, want 5000", mock.lastSendOpts.TTL)
	}
}

func TestSendCommand_TTLDefaultZero(t *testing.T) {
	mock := &mockQueueBackend{}
	cmd := NewSendCommand(mock)
	cmd.SetArgs([]string{"test-queue", "hello"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.lastSendOpts.TTL != 0 {
		t.Errorf("TTL = %d, want 0 (no expiry)", mock.lastSendOpts.TTL)
	}
}

func TestSendCommand_CountDefaultOne(t *testing.T) {
	mock := &mockQueueBackend{}
	cmd := NewSendCommand(mock)
	cmd.SetArgs([]string{"test-queue", "hello"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.sendCount != 1 {
		t.Errorf("sendCount = %d, want 1", mock.sendCount)
	}
}

func TestSendCommand_SendError(t *testing.T) {
	mock := &mockQueueBackend{sendErr: fmt.Errorf("broker down")}
	cmd := NewSendCommand(mock)
	cmd.SetArgs([]string{"test-queue", "hello"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
