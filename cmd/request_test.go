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

func TestRequestCommand_BasicRequestReply(t *testing.T) {
	reply := &backends.Message{Data: []byte("reply-data")}
	mock := &mockQueueBackend{receiveMsg: reply}
	cmd := NewRequestCommand(mock)
	cmd.SetArgs([]string{"request-queue", "hello"})

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

	// Verify the send was made
	if mock.sendCount != 1 {
		t.Errorf("sendCount = %d, want 1", mock.sendCount)
	}
	if mock.lastSendOpts.Queue != "request-queue" {
		t.Errorf("queue = %q, want %q", mock.lastSendOpts.Queue, "request-queue")
	}
	if !bytes.Equal(mock.lastSendOpts.Message, []byte("hello")) {
		t.Errorf("message = %q, want %q", mock.lastSendOpts.Message, "hello")
	}

	// Verify default reply-to queue
	if mock.lastSendOpts.ReplyTo != "xmc.reply" {
		t.Errorf("replyTo = %q, want %q", mock.lastSendOpts.ReplyTo, "xmc.reply")
	}

	// Verify reply was received on the reply queue
	if mock.lastReceiveOpts.Queue != "xmc.reply" {
		t.Errorf("receive queue = %q, want %q", mock.lastReceiveOpts.Queue, "xmc.reply")
	}
	if !mock.lastReceiveOpts.Acknowledge {
		t.Error("reply receive should acknowledge")
	}

	// Verify output
	var buf bytes.Buffer
	buf.ReadFrom(r)
	if buf.String() != "reply-data" {
		t.Errorf("output = %q, want %q", buf.String(), "reply-data")
	}
}

func TestRequestCommand_CustomReplyTo(t *testing.T) {
	reply := &backends.Message{Data: []byte("custom reply")}
	mock := &mockQueueBackend{receiveMsg: reply}
	cmd := NewRequestCommand(mock)
	cmd.SetArgs([]string{"req-queue", "msg", "-R", "my-reply-queue"})

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

	if mock.lastSendOpts.ReplyTo != "my-reply-queue" {
		t.Errorf("replyTo = %q, want %q", mock.lastSendOpts.ReplyTo, "my-reply-queue")
	}
	if mock.lastReceiveOpts.Queue != "my-reply-queue" {
		t.Errorf("receive queue = %q, want %q", mock.lastReceiveOpts.Queue, "my-reply-queue")
	}
}

func TestRequestCommand_WithFlags(t *testing.T) {
	reply := &backends.Message{Data: []byte("ok")}
	mock := &mockQueueBackend{receiveMsg: reply}
	cmd := NewRequestCommand(mock)
	cmd.SetArgs([]string{
		"req-queue", "payload",
		"-T", "application/json",
		"-C", "corr-001",
		"-I", "msg-001",
		"-Y", "9",
		"-d",
		"-P", "env=prod",
	})

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

	opts := mock.lastSendOpts
	if opts.ContentType != "application/json" {
		t.Errorf("contenttype = %q, want %q", opts.ContentType, "application/json")
	}
	if opts.CorrelationID != "corr-001" {
		t.Errorf("correlationid = %q, want %q", opts.CorrelationID, "corr-001")
	}
	if opts.MessageID != "msg-001" {
		t.Errorf("messageid = %q, want %q", opts.MessageID, "msg-001")
	}
	if opts.Priority != 9 {
		t.Errorf("priority = %d, want %d", opts.Priority, 9)
	}
	if !opts.Persistent {
		t.Error("persistent = false, want true")
	}
	if opts.Properties["env"] != "prod" {
		t.Errorf("property env = %v, want %q", opts.Properties["env"], "prod")
	}
}

func TestRequestCommand_JSONOutput(t *testing.T) {
	reply := &backends.Message{
		Data:       []byte("json reply"),
		MessageID:  "reply-id",
		Properties: map[string]any{"status": "ok"},
	}
	mock := &mockQueueBackend{receiveMsg: reply}
	cmd := NewRequestCommand(mock)
	cmd.SetArgs([]string{"req-queue", "msg", "-J"})

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
	if result["data"] != "json reply" {
		t.Errorf("data = %v, want %q", result["data"], "json reply")
	}
	if result["messageId"] != "reply-id" {
		t.Errorf("messageId = %v, want %q", result["messageId"], "reply-id")
	}
}

func TestRequestCommand_TimeoutError(t *testing.T) {
	mock := &mockQueueBackend{receiveErr: context.DeadlineExceeded}
	cmd := NewRequestCommand(mock)
	cmd.SetArgs([]string{"req-queue", "msg"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error on timeout, got nil")
	}
	if !strings.Contains(err.Error(), "no reply received") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "no reply received")
	}
}

func TestRequestCommand_NilReplyReturnsError(t *testing.T) {
	mock := &mockQueueBackend{receiveMsg: nil}
	cmd := NewRequestCommand(mock)
	cmd.SetArgs([]string{"req-queue", "msg"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for nil reply, got nil")
	}
}

func TestRequestCommand_SendFailure(t *testing.T) {
	mock := &mockQueueBackend{sendErr: context.DeadlineExceeded}
	cmd := NewRequestCommand(mock)
	cmd.SetArgs([]string{"req-queue", "msg"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when send fails, got nil")
	}
	if !strings.Contains(err.Error(), "failed to send request") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "failed to send request")
	}
}

func TestRequestCommand_CorrelationFromMessageID(t *testing.T) {
	reply := &backends.Message{Data: []byte("ok")}
	mock := &mockQueueBackend{receiveMsg: reply}
	cmd := NewRequestCommand(mock)
	cmd.SetArgs([]string{"req-queue", "msg", "-I", "my-msg-id"})

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

	// When no correlation ID given but message ID is set, correlation ID should be derived
	if mock.lastSendOpts.CorrelationID != "my-msg-id" {
		t.Errorf("correlationID = %q, want %q", mock.lastSendOpts.CorrelationID, "my-msg-id")
	}
}

func TestRequestCommand_InvalidProperty(t *testing.T) {
	mock := &mockQueueBackend{}
	cmd := NewRequestCommand(mock)
	cmd.SetArgs([]string{"req-queue", "msg", "-P", "no-equals-sign"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid property, got nil")
	}
}
