package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/log"
)

// mockTopicBackend is a test double for TopicBackend
type mockTopicBackend struct {
	lastPublishOpts   backends.PublishOptions
	lastSubscribeOpts backends.SubscribeOptions
	publishErr        error
	publishCount      int // number of times Publish was called
	subscribeMsg      *backends.Message
	subscribeMsgs     []*backends.Message // multiple messages for count testing
	subscribeErr      error
	subscribeCount    int // number of times Subscribe was called
}

func (m *mockTopicBackend) Publish(_ context.Context, opts backends.PublishOptions) error {
	m.lastPublishOpts = opts
	m.publishCount++
	return m.publishErr
}

func (m *mockTopicBackend) Subscribe(_ context.Context, opts backends.SubscribeOptions) (*backends.Message, error) {
	m.lastSubscribeOpts = opts
	m.subscribeCount++
	if len(m.subscribeMsgs) > 0 {
		idx := m.subscribeCount - 1
		if idx < len(m.subscribeMsgs) {
			return m.subscribeMsgs[idx], nil
		}
		return nil, m.subscribeErr
	}
	return m.subscribeMsg, m.subscribeErr
}

func (m *mockTopicBackend) Close() error { return nil }

func TestPublishCommand_WithMessageArg(t *testing.T) {
	mock := &mockTopicBackend{}
	cmd := NewPublishCommand(mock)
	cmd.SetArgs([]string{"test-topic", "hello world"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.lastPublishOpts.Topic != "test-topic" {
		t.Errorf("topic = %q, want %q", mock.lastPublishOpts.Topic, "test-topic")
	}
	if !bytes.Equal(mock.lastPublishOpts.Message, []byte("hello world")) {
		t.Errorf("message = %q, want %q", mock.lastPublishOpts.Message, "hello world")
	}
}

func TestPublishCommand_WithAllFlags(t *testing.T) {
	mock := &mockTopicBackend{}
	cmd := NewPublishCommand(mock)
	cmd.SetArgs([]string{
		"my-topic", "test-msg",
		"-T", "application/json",
		"-C", "corr-123",
		"-K", "partition-key",
		"-I", "msg-456",
		"-Y", "8",
		"-d",
		"-R", "reply-topic",
		"-P", "env=prod",
	})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	opts := mock.lastPublishOpts
	if opts.ContentType != "application/json" {
		t.Errorf("contenttype = %q, want %q", opts.ContentType, "application/json")
	}
	if opts.CorrelationID != "corr-123" {
		t.Errorf("correlationid = %q, want %q", opts.CorrelationID, "corr-123")
	}
	if opts.Key != "partition-key" {
		t.Errorf("key = %q, want %q", opts.Key, "partition-key")
	}
	if opts.MessageID != "msg-456" {
		t.Errorf("messageid = %q, want %q", opts.MessageID, "msg-456")
	}
	if opts.Priority != 8 {
		t.Errorf("priority = %d, want %d", opts.Priority, 8)
	}
	if !opts.Persistent {
		t.Error("persistent = false, want true")
	}
	if opts.ReplyTo != "reply-topic" {
		t.Errorf("replyto = %q, want %q", opts.ReplyTo, "reply-topic")
	}
	if opts.Properties["env"] != "prod" {
		t.Errorf("property env = %v, want %q", opts.Properties["env"], "prod")
	}
}

func TestSubscribeCommand_DisplaysMessage(t *testing.T) {
	msg := &backends.Message{
		Data: []byte("topic message"),
	}
	mock := &mockTopicBackend{subscribeMsg: msg}
	cmd := NewSubscribeCommand(mock)
	cmd.SetArgs([]string{"test-topic"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.lastSubscribeOpts.Topic != "test-topic" {
		t.Errorf("topic = %q, want %q", mock.lastSubscribeOpts.Topic, "test-topic")
	}
}

func TestSubscribeCommand_TimeoutReturnsNil(t *testing.T) {
	mock := &mockTopicBackend{subscribeErr: context.DeadlineExceeded}
	cmd := NewSubscribeCommand(mock)
	cmd.SetArgs([]string{"test-topic"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("expected nil error on timeout, got: %v", err)
	}
}

func TestPublishCommand_CountFlag(t *testing.T) {
	mock := &mockTopicBackend{}
	cmd := NewPublishCommand(mock)
	cmd.SetArgs([]string{"test-topic", "hello", "-n", "3"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.publishCount != 3 {
		t.Errorf("publishCount = %d, want 3", mock.publishCount)
	}
}

func TestPublishCommand_TTLFlag(t *testing.T) {
	mock := &mockTopicBackend{}
	cmd := NewPublishCommand(mock)
	cmd.SetArgs([]string{"test-topic", "hello", "-E", "10000"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.lastPublishOpts.TTL != 10000 {
		t.Errorf("TTL = %d, want 10000", mock.lastPublishOpts.TTL)
	}
}

func TestPublishCommand_InvalidProperty(t *testing.T) {
	mock := &mockTopicBackend{}
	cmd := NewPublishCommand(mock)
	cmd.SetArgs([]string{"test-topic", "msg", "-P", "no-equals"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid property, got nil")
	}
}

func TestSubscribeCommand_CountFlag(t *testing.T) {
	msgs := []*backends.Message{
		{Data: []byte("sub1")},
		{Data: []byte("sub2")},
		{Data: []byte("sub3")},
	}
	mock := &mockTopicBackend{subscribeMsgs: msgs}
	cmd := NewSubscribeCommand(mock)
	cmd.SetArgs([]string{"test-topic", "-n", "3"})

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	origIsStdout := log.IsStdout
	log.IsStdout = false
	defer func() { log.IsStdout = origIsStdout }()

	err := cmd.Execute()
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.subscribeCount != 3 {
		t.Errorf("subscribeCount = %d, want 3", mock.subscribeCount)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()
	if !strings.Contains(output, "sub1") || !strings.Contains(output, "sub3") {
		t.Errorf("output missing expected messages: %q", output)
	}
}

func TestSubscribeCommand_JSONOutput(t *testing.T) {
	msg := &backends.Message{
		Data:       []byte("topic json"),
		MessageID:  "sub-id-1",
		Properties: map[string]any{"env": "staging"},
	}
	mock := &mockTopicBackend{subscribeMsg: msg}
	cmd := NewSubscribeCommand(mock)
	cmd.SetArgs([]string{"test-topic", "-J"})

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := cmd.Execute()
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)

	var result map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &result); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if result["data"] != "topic json" {
		t.Errorf("data = %v, want %q", result["data"], "topic json")
	}
	if result["messageId"] != "sub-id-1" {
		t.Errorf("messageId = %v, want %q", result["messageId"], "sub-id-1")
	}
}

func TestSubscribeCommand_SelectorFlag(t *testing.T) {
	msg := &backends.Message{Data: []byte("filtered")}
	mock := &mockTopicBackend{subscribeMsg: msg}
	cmd := NewSubscribeCommand(mock)
	cmd.SetArgs([]string{"test-topic", "-S", "type='order'"})

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w
	origIsStdout := log.IsStdout
	log.IsStdout = false
	defer func() { log.IsStdout = origIsStdout }()

	err := cmd.Execute()
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.lastSubscribeOpts.Selector != "type='order'" {
		t.Errorf("selector = %q, want %q", mock.lastSubscribeOpts.Selector, "type='order'")
	}
}

func TestSubscribeCommand_DurableFlag(t *testing.T) {
	msg := &backends.Message{Data: []byte("durable msg")}
	mock := &mockTopicBackend{subscribeMsg: msg}
	cmd := NewSubscribeCommand(mock)
	cmd.SetArgs([]string{"test-topic", "-D"})

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w
	origIsStdout := log.IsStdout
	log.IsStdout = false
	defer func() { log.IsStdout = origIsStdout }()

	err := cmd.Execute()
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mock.lastSubscribeOpts.Durable {
		t.Error("durable = false, want true")
	}
}

func TestSubscribeCommand_QuietFlag(t *testing.T) {
	msg := &backends.Message{
		Data:       []byte("quiet data"),
		Properties: map[string]any{"key": "val"},
	}
	mock := &mockTopicBackend{subscribeMsg: msg}
	cmd := NewSubscribeCommand(mock)
	cmd.SetArgs([]string{"test-topic", "-q"})

	oldStderr := os.Stderr
	rErr, wErr, _ := os.Pipe()
	os.Stderr = wErr

	oldStdout := os.Stdout
	rOut, wOut, _ := os.Pipe()
	os.Stdout = wOut
	origIsStdout := log.IsStdout
	log.IsStdout = false
	defer func() { log.IsStdout = origIsStdout }()

	err := cmd.Execute()
	wOut.Close()
	wErr.Close()
	os.Stdout = oldStdout
	os.Stderr = oldStderr

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var bufOut bytes.Buffer
	bufOut.ReadFrom(rOut)
	if bufOut.String() != "quiet data" {
		t.Errorf("stdout = %q, want %q", bufOut.String(), "quiet data")
	}

	var bufErr bytes.Buffer
	bufErr.ReadFrom(rErr)
	if strings.Contains(bufErr.String(), "Properties") {
		t.Errorf("stderr should not contain properties in quiet mode, got: %q", bufErr.String())
	}
}

func TestSubscribeCommand_NilMessageReturnsError(t *testing.T) {
	mock := &mockTopicBackend{subscribeMsg: nil}
	cmd := NewSubscribeCommand(mock)
	cmd.SetArgs([]string{"test-topic"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for nil message, got nil")
	}
}
