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

func TestReceiveCommand_DisplaysMessage(t *testing.T) {
	msg := &backends.Message{
		Data:       []byte("hello"),
		Properties: map[string]any{"env": "test"},
	}
	mock := &mockQueueBackend{receiveMsg: msg}
	cmd := NewReceiveCommand(mock)
	cmd.SetArgs([]string{"test-queue"})

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Force IsStdout to false so no newline is added (pipe mode)
	origIsStdout := log.IsStdout
	log.IsStdout = false
	defer func() { log.IsStdout = origIsStdout }()

	err := cmd.Execute()
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if output != "hello" {
		t.Errorf("output = %q, want %q", output, "hello")
	}

	opts := mock.lastReceiveOpts
	if opts.Queue != "test-queue" {
		t.Errorf("queue = %q, want %q", opts.Queue, "test-queue")
	}
	if !opts.Acknowledge {
		t.Error("acknowledge = false, want true (receive is destructive)")
	}
}

func TestReceiveCommand_TimeoutReturnsNil(t *testing.T) {
	mock := &mockQueueBackend{receiveErr: context.DeadlineExceeded}
	cmd := NewReceiveCommand(mock)
	cmd.SetArgs([]string{"test-queue"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("expected nil error on timeout, got: %v", err)
	}
}

func TestReceiveCommand_NilMessageReturnsError(t *testing.T) {
	mock := &mockQueueBackend{receiveMsg: nil}
	cmd := NewReceiveCommand(mock)
	cmd.SetArgs([]string{"test-queue"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for nil message, got nil")
	}
}

func TestReceiveCommand_CountFlag(t *testing.T) {
	msgs := []*backends.Message{
		{Data: []byte("msg1")},
		{Data: []byte("msg2")},
		{Data: []byte("msg3")},
	}
	mock := &mockQueueBackend{receiveMsgs: msgs}
	cmd := NewReceiveCommand(mock)
	cmd.SetArgs([]string{"test-queue", "-n", "3"})

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
	if mock.receiveCount != 3 {
		t.Errorf("receiveCount = %d, want 3", mock.receiveCount)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()
	if !strings.Contains(output, "msg1") || !strings.Contains(output, "msg3") {
		t.Errorf("output missing expected messages: %q", output)
	}
}

func TestReceiveCommand_JSONOutput(t *testing.T) {
	msg := &backends.Message{
		Data:          []byte("hello json"),
		MessageID:     "id-123",
		CorrelationID: "corr-456",
		ContentType:   "text/plain",
		Properties:    map[string]any{"env": "test"},
	}
	mock := &mockQueueBackend{receiveMsg: msg}
	cmd := NewReceiveCommand(mock)
	cmd.SetArgs([]string{"test-queue", "-J"})

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
	output := buf.String()

	var result map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &result); err != nil {
		t.Fatalf("failed to parse JSON output: %v\noutput: %s", err, output)
	}
	if result["data"] != "hello json" {
		t.Errorf("data = %v, want %q", result["data"], "hello json")
	}
	if result["messageId"] != "id-123" {
		t.Errorf("messageId = %v, want %q", result["messageId"], "id-123")
	}
	if result["correlationId"] != "corr-456" {
		t.Errorf("correlationId = %v, want %q", result["correlationId"], "corr-456")
	}
	props, ok := result["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties not present or not a map")
	}
	if props["env"] != "test" {
		t.Errorf("property env = %v, want %q", props["env"], "test")
	}
}

func TestReceiveCommand_SelectorFlag(t *testing.T) {
	msg := &backends.Message{Data: []byte("filtered")}
	mock := &mockQueueBackend{receiveMsg: msg}
	cmd := NewReceiveCommand(mock)
	cmd.SetArgs([]string{"test-queue", "-S", "color='red'"})

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
	if mock.lastReceiveOpts.Selector != "color='red'" {
		t.Errorf("selector = %q, want %q", mock.lastReceiveOpts.Selector, "color='red'")
	}
}

func TestReceiveCommand_QuietFlag(t *testing.T) {
	msg := &backends.Message{
		Data:       []byte("data only"),
		Properties: map[string]any{"key": "val"},
	}
	mock := &mockQueueBackend{receiveMsg: msg}
	cmd := NewReceiveCommand(mock)
	cmd.SetArgs([]string{"test-queue", "-q"})

	// Capture stderr to verify properties are NOT printed
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
	if bufOut.String() != "data only" {
		t.Errorf("stdout = %q, want %q", bufOut.String(), "data only")
	}

	var bufErr bytes.Buffer
	bufErr.ReadFrom(rErr)
	if strings.Contains(bufErr.String(), "Properties") {
		t.Errorf("stderr should not contain properties in quiet mode, got: %q", bufErr.String())
	}
}

func TestReceiveCommand_AcknowledgeTrue(t *testing.T) {
	msg := &backends.Message{Data: []byte("ack me")}
	mock := &mockQueueBackend{receiveMsg: msg}
	cmd := NewReceiveCommand(mock)
	cmd.SetArgs([]string{"test-queue"})

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
	if !mock.lastReceiveOpts.Acknowledge {
		t.Error("receive should acknowledge (destructive read)")
	}
}
