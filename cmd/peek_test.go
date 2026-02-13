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

func TestPeekCommand_NonDestructive(t *testing.T) {
	msg := &backends.Message{
		Data:       []byte("peeked"),
		Properties: map[string]any{"key": "val"},
	}
	mock := &mockQueueBackend{receiveMsg: msg}
	cmd := NewPeekCommand(mock)
	cmd.SetArgs([]string{"test-queue"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.lastReceiveOpts.Acknowledge {
		t.Error("acknowledge = true, want false (peek is non-destructive)")
	}
	if !mock.lastReceiveOpts.WithHeaderAndProperties {
		t.Error("WithHeaderAndProperties = false, want true for peek")
	}
	if !mock.lastReceiveOpts.WithApplicationProperties {
		t.Error("WithApplicationProperties = false, want true for peek")
	}
}

func TestPeekCommand_TimeoutReturnsNil(t *testing.T) {
	mock := &mockQueueBackend{receiveErr: context.DeadlineExceeded}
	cmd := NewPeekCommand(mock)
	cmd.SetArgs([]string{"test-queue"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("expected nil error on timeout, got: %v", err)
	}
}

func TestPeekCommand_CountFlag(t *testing.T) {
	msgs := []*backends.Message{
		{Data: []byte("peek1")},
		{Data: []byte("peek2")},
	}
	mock := &mockQueueBackend{receiveMsgs: msgs}
	cmd := NewPeekCommand(mock)
	cmd.SetArgs([]string{"test-queue", "-n", "2"})

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
	if mock.receiveCount != 2 {
		t.Errorf("receiveCount = %d, want 2", mock.receiveCount)
	}
	// Peek should never acknowledge
	if mock.lastReceiveOpts.Acknowledge {
		t.Error("peek with count should still not acknowledge")
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()
	if !strings.Contains(output, "peek1") || !strings.Contains(output, "peek2") {
		t.Errorf("output missing expected messages: %q", output)
	}
}

func TestPeekCommand_JSONOutput(t *testing.T) {
	msg := &backends.Message{
		Data:       []byte("peek json"),
		MessageID:  "peek-id",
		Properties: map[string]any{"color": "blue"},
	}
	mock := &mockQueueBackend{receiveMsg: msg}
	cmd := NewPeekCommand(mock)
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

	var result map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &result); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if result["data"] != "peek json" {
		t.Errorf("data = %v, want %q", result["data"], "peek json")
	}
	if result["messageId"] != "peek-id" {
		t.Errorf("messageId = %v, want %q", result["messageId"], "peek-id")
	}
}

func TestPeekCommand_SelectorFlag(t *testing.T) {
	msg := &backends.Message{Data: []byte("filtered")}
	mock := &mockQueueBackend{receiveMsg: msg}
	cmd := NewPeekCommand(mock)
	cmd.SetArgs([]string{"test-queue", "-S", "priority > 5"})

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
	if mock.lastReceiveOpts.Selector != "priority > 5" {
		t.Errorf("selector = %q, want %q", mock.lastReceiveOpts.Selector, "priority > 5")
	}
}
