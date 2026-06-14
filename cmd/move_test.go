package cmd

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/makibytes/xmc/broker/backends"
)

func TestMoveCommand_MovesAll(t *testing.T) {
	msgs := []*backends.Message{
		{Data: []byte("m1")},
		{Data: []byte("m2")},
		{Data: []byte("m3")},
	}
	mock := &mockQueueBackend{receiveMsgs: msgs}
	cmd := NewMoveCommand(mock)
	cmd.SetArgs([]string{"source", "dest"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.sendCount != 3 {
		t.Errorf("sendCount = %d, want 3", mock.sendCount)
	}
	if mock.lastSendOpts.Queue != "dest" {
		t.Errorf("destination = %q, want %q", mock.lastSendOpts.Queue, "dest")
	}
	if !mock.lastReceiveOpts.Acknowledge {
		t.Error("move should acknowledge (destructive read) the source")
	}
	if mock.lastReceiveOpts.Queue != "source" {
		t.Errorf("source = %q, want %q", mock.lastReceiveOpts.Queue, "source")
	}
}

func TestMoveCommand_CountBounded(t *testing.T) {
	msgs := []*backends.Message{
		{Data: []byte("m1")},
		{Data: []byte("m2")},
		{Data: []byte("m3")},
	}
	mock := &mockQueueBackend{receiveMsgs: msgs}
	cmd := NewMoveCommand(mock)
	cmd.SetArgs([]string{"source", "dest", "-n", "2"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.sendCount != 2 {
		t.Errorf("sendCount = %d, want 2", mock.sendCount)
	}
	if mock.receiveCount != 2 {
		t.Errorf("receiveCount = %d, want 2", mock.receiveCount)
	}
}

func TestMoveCommand_PreservesMetadata(t *testing.T) {
	msg := &backends.Message{
		Data:          []byte("payload"),
		MessageID:     "orig-id",
		CorrelationID: "corr-1",
		ReplyTo:       "rq",
		ContentType:   "application/json",
		Priority:      6,
		Persistent:    true,
		Properties:    map[string]any{"env": "prod"},
	}
	mock := &mockQueueBackend{receiveMsgs: []*backends.Message{msg}}
	cmd := NewMoveCommand(mock)
	cmd.SetArgs([]string{"source", "dest"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	opts := mock.lastSendOpts
	if string(opts.Message) != "payload" {
		t.Errorf("data = %q, want %q", opts.Message, "payload")
	}
	if opts.CorrelationID != "corr-1" {
		t.Errorf("correlationID = %q, want %q", opts.CorrelationID, "corr-1")
	}
	if opts.ContentType != "application/json" {
		t.Errorf("contentType = %q, want %q", opts.ContentType, "application/json")
	}
	if opts.ReplyTo != "rq" {
		t.Errorf("replyTo = %q, want %q", opts.ReplyTo, "rq")
	}
	if opts.Priority != 6 {
		t.Errorf("priority = %d, want 6", opts.Priority)
	}
	if !opts.Persistent {
		t.Error("persistent = false, want true")
	}
	if opts.Properties["env"] != "prod" {
		t.Errorf("property env = %v, want %q", opts.Properties["env"], "prod")
	}
	// A fresh message ID is assigned by the destination; the original is not copied.
	if opts.MessageID != "" {
		t.Errorf("messageID = %q, want empty (destination assigns a fresh ID)", opts.MessageID)
	}
}

func TestMoveCommand_EmptySource(t *testing.T) {
	mock := &mockQueueBackend{receiveErr: backends.ErrNoMessageAvailable}
	cmd := NewMoveCommand(mock)
	cmd.SetArgs([]string{"source", "dest"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected nil for empty source, got: %v", err)
	}
	if mock.sendCount != 0 {
		t.Errorf("sendCount = %d, want 0", mock.sendCount)
	}
}

func TestMoveCommand_SelectorPassed(t *testing.T) {
	mock := &mockQueueBackend{receiveMsgs: []*backends.Message{{Data: []byte("m")}}}
	cmd := NewMoveCommand(mock)
	cmd.SetArgs([]string{"source", "dest", "-S", "priority > 5"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.lastReceiveOpts.Selector != "priority > 5" {
		t.Errorf("selector = %q, want %q", mock.lastReceiveOpts.Selector, "priority > 5")
	}
}

func TestMoveCommand_SameSourceAndDestination(t *testing.T) {
	mock := &mockQueueBackend{}
	cmd := NewMoveCommand(mock)
	cmd.SetArgs([]string{"same", "same"})

	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when source == destination, got nil")
	}
	if mock.receiveCount != 0 {
		t.Errorf("receiveCount = %d, want 0 (should fail before consuming)", mock.receiveCount)
	}
}

func TestMoveCommand_SendFailureSurfacesMessage(t *testing.T) {
	mock := &mockQueueBackend{
		receiveMsgs: []*backends.Message{{Data: []byte("lost-msg")}},
		sendErr:     fmt.Errorf("broker rejected"),
	}
	cmd := NewMoveCommand(mock)
	cmd.SetArgs([]string{"source", "dest"})

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := cmd.Execute()
	w.Close()
	os.Stdout = old

	if err == nil {
		t.Fatal("expected error when send fails, got nil")
	}
	if mock.sendCount != 1 {
		t.Errorf("sendCount = %d, want 1 (one send attempt)", mock.sendCount)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "lost-msg") {
		t.Errorf("undelivered message should be written to stdout for recovery, got: %q", buf.String())
	}
}
