package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
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
	cmd := NewSendCommand(mock, nil, nil)
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
	cmd := NewSendCommand(mock, nil, nil)
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
	cmd := NewSendCommand(mock, nil, nil)
	cmd.SetArgs([]string{"my-queue", "msg", "-P", "invalid-no-equals"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid property, got nil")
	}
}

func TestSendCommand_NoMessage(t *testing.T) {
	mock := &mockQueueBackend{}
	cmd := NewSendCommand(mock, nil, nil)
	cmd.SetArgs([]string{"my-queue"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing message, got nil")
	}
}

func TestSendCommand_CountFlag(t *testing.T) {
	mock := &mockQueueBackend{}
	cmd := NewSendCommand(mock, nil, nil)
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
	cmd := NewSendCommand(mock, nil, nil)
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
	cmd := NewSendCommand(mock, nil, nil)
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
	cmd := NewSendCommand(mock, nil, nil)
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
	cmd := NewSendCommand(mock, nil, nil)
	cmd.SetArgs([]string{"test-queue", "hello"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestSendCommand_StdinData(t *testing.T) {
	mock := &mockQueueBackend{}
	cmd := NewSendCommand(mock, nil, nil)
	cmd.SetArgs([]string{"test-queue"})

	// Pipe data into stdin
	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	w.WriteString("stdin message")
	w.Close()

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(mock.lastSendOpts.Message) != "stdin message" {
		t.Errorf("message = %q, want %q", mock.lastSendOpts.Message, "stdin message")
	}
}

func TestSendCommand_LinesMode(t *testing.T) {
	mock := &mockQueueBackend{}
	cmd := NewSendCommand(mock, nil, nil)
	cmd.SetArgs([]string{"test-queue", "-l"})

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	w.WriteString("line one\nline two\nline three\n")
	w.Close()

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.sendCount != 3 {
		t.Errorf("sendCount = %d, want 3", mock.sendCount)
	}
	if string(mock.lastSendOpts.Message) != "line three" {
		t.Errorf("last message = %q, want %q", mock.lastSendOpts.Message, "line three")
	}
}

func TestSendCommand_LinesModeWithError(t *testing.T) {
	mock := &mockQueueBackend{sendErr: fmt.Errorf("broker error")}
	cmd := NewSendCommand(mock, nil, nil)
	cmd.SetArgs([]string{"test-queue", "-l"})

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	w.WriteString("line one\n")
	w.Close()

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// stubResolver mimics RabbitMQ address resolution for exchange routing tests.
func stubResolver(spec TargetSpec) (string, error) {
	if spec.Queue != "" {
		return "/queues/" + spec.Queue, nil
	}
	if spec.Exchange != "" {
		if spec.To != "" {
			return "/exchanges/" + spec.Exchange + "/" + spec.To, nil
		}
		return "/exchanges/" + spec.Exchange, nil
	}
	if spec.IsTopic {
		return "/exchanges/amq.topic/" + spec.To, nil
	}
	return "/queues/" + spec.To, nil
}

func TestSendExchangeRouting_SinglePositionalIsBody(t *testing.T) {
	// send -e fo1 "hello" → body "hello", address /exchanges/fo1 (no routing key)
	mock := &mockQueueBackend{}
	cmd := NewSendCommand(mock, stubResolver, nil, true)
	cmd.SetArgs([]string{"-e", "fo1", "hello"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.lastSendOpts.Queue != "/exchanges/fo1" {
		t.Errorf("queue = %q, want %q", mock.lastSendOpts.Queue, "/exchanges/fo1")
	}
	if string(mock.lastSendOpts.Message) != "hello" {
		t.Errorf("message = %q, want %q", mock.lastSendOpts.Message, "hello")
	}
}

func TestSendExchangeRouting_TwoPositionalsLegacy(t *testing.T) {
	// send -e amq.direct key1 "hi" → routing key "key1", body "hi"
	mock := &mockQueueBackend{}
	cmd := NewSendCommand(mock, stubResolver, nil, true)
	cmd.SetArgs([]string{"-e", "amq.direct", "key1", "hi"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.lastSendOpts.Queue != "/exchanges/amq.direct/key1" {
		t.Errorf("queue = %q, want %q", mock.lastSendOpts.Queue, "/exchanges/amq.direct/key1")
	}
	if string(mock.lastSendOpts.Message) != "hi" {
		t.Errorf("message = %q, want %q", mock.lastSendOpts.Message, "hi")
	}
}

func TestSendExchangeRouting_RoutingKeyFlag(t *testing.T) {
	// send -e amq.direct --routing-key key1 "hi" → routing key via flag, body "hi"
	mock := &mockQueueBackend{}
	cmd := NewSendCommand(mock, stubResolver, nil, true)
	cmd.SetArgs([]string{"-e", "amq.direct", "--routing-key", "key1", "hi"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.lastSendOpts.Queue != "/exchanges/amq.direct/key1" {
		t.Errorf("queue = %q, want %q", mock.lastSendOpts.Queue, "/exchanges/amq.direct/key1")
	}
	if string(mock.lastSendOpts.Message) != "hi" {
		t.Errorf("message = %q, want %q", mock.lastSendOpts.Message, "hi")
	}
}

func TestSendExchangeRouting_NoArgsStdin(t *testing.T) {
	// send -e fo1 (no args) → no routing key, body from stdin
	mock := &mockQueueBackend{}
	cmd := NewSendCommand(mock, stubResolver, nil, true)
	cmd.SetArgs([]string{"-e", "fo1"})

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	w.WriteString("stdin msg")
	w.Close()

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.lastSendOpts.Queue != "/exchanges/fo1" {
		t.Errorf("queue = %q, want %q", mock.lastSendOpts.Queue, "/exchanges/fo1")
	}
	if string(mock.lastSendOpts.Message) != "stdin msg" {
		t.Errorf("message = %q, want %q", mock.lastSendOpts.Message, "stdin msg")
	}
}
