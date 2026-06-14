package cmd

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/makibytes/xmc/broker/backends"
)

// Forward streams until interrupted; in tests we make the mock return
// context.Canceled once its scripted messages are exhausted so the loop
// terminates deterministically.

func TestForwardCommand_RelaysAll(t *testing.T) {
	msgs := []*backends.Message{{Data: []byte("a")}, {Data: []byte("b")}, {Data: []byte("c")}}
	mock := &mockQueueBackend{receiveMsgs: msgs, receiveErr: context.Canceled}
	cmd := NewForwardCommand(mock)
	cmd.SetArgs([]string{"src", "dst"})

	captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if mock.sendCount != 3 {
		t.Errorf("sendCount = %d, want 3", mock.sendCount)
	}
	if mock.lastSendOpts.Queue != "dst" {
		t.Errorf("destination = %q, want dst", mock.lastSendOpts.Queue)
	}
	if !mock.lastReceiveOpts.Acknowledge {
		t.Error("forward should consume (acknowledge) the source queue")
	}
}

func TestForwardCommand_CountLimit(t *testing.T) {
	msgs := []*backends.Message{{Data: []byte("a")}, {Data: []byte("b")}, {Data: []byte("c")}}
	mock := &mockQueueBackend{receiveMsgs: msgs, receiveErr: context.Canceled}
	cmd := NewForwardCommand(mock)
	cmd.SetArgs([]string{"src", "dst", "-n", "2"})

	captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if mock.sendCount != 2 {
		t.Errorf("sendCount = %d, want 2 (count limit)", mock.sendCount)
	}
}

func TestForwardCommand_PreservesMetadata(t *testing.T) {
	msg := &backends.Message{
		Data:          []byte("payload"),
		MessageID:     "orig",
		CorrelationID: "c1",
		ReplyTo:       "rq",
		ContentType:   "application/json",
		Priority:      6,
		Persistent:    true,
		Properties:    map[string]any{"env": "prod"},
	}
	mock := &mockQueueBackend{receiveMsgs: []*backends.Message{msg}, receiveErr: context.Canceled}
	cmd := NewForwardCommand(mock)
	cmd.SetArgs([]string{"src", "dst"})

	captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	o := mock.lastSendOpts
	if string(o.Message) != "payload" || o.CorrelationID != "c1" || o.ReplyTo != "rq" ||
		o.ContentType != "application/json" || o.Priority != 6 || !o.Persistent || o.Properties["env"] != "prod" {
		t.Errorf("metadata not preserved: %+v", o)
	}
	if o.MessageID != "" {
		t.Errorf("messageID = %q, want empty (destination assigns a fresh one)", o.MessageID)
	}
}

func TestForwardCommand_SameSourceAndDestination(t *testing.T) {
	mock := &mockQueueBackend{}
	cmd := NewForwardCommand(mock)
	cmd.SetArgs([]string{"same", "same"})

	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when source == destination")
	}
	if mock.receiveCount != 0 {
		t.Errorf("receiveCount = %d, want 0 (should fail before consuming)", mock.receiveCount)
	}
}

func TestForwardCommand_Transform(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("command uses a POSIX shell")
	}
	mock := &mockQueueBackend{
		receiveMsgs: []*backends.Message{{Data: []byte("hello")}},
		receiveErr:  context.Canceled,
	}
	cmd := NewForwardCommand(mock)
	cmd.SetArgs([]string{"src", "dst", "-x", "tr a-z A-Z"})

	captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if string(mock.lastSendOpts.Message) != "HELLO" {
		t.Errorf("payload via -x = %q, want HELLO", mock.lastSendOpts.Message)
	}
}

func TestForwardCommand_TransformFailureRecovers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("command uses a POSIX shell")
	}
	mock := &mockQueueBackend{
		receiveMsgs: []*backends.Message{{Data: []byte("lost")}},
		receiveErr:  context.Canceled,
	}
	cmd := NewForwardCommand(mock)
	cmd.SetArgs([]string{"src", "dst", "-x", "exit 1"})

	out := captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if mock.sendCount != 0 {
		t.Errorf("sendCount = %d, want 0 (command failed)", mock.sendCount)
	}
	if !strings.Contains(out, "lost") {
		t.Errorf("failed message should be recovered to stdout, got %q", out)
	}
}

func TestForwardCommand_SendFailureRecovers(t *testing.T) {
	mock := &mockQueueBackend{
		receiveMsgs: []*backends.Message{{Data: []byte("undelivered")}},
		receiveErr:  context.Canceled,
		sendErr:     fmt.Errorf("broker down"),
	}
	cmd := NewForwardCommand(mock)
	cmd.SetArgs([]string{"src", "dst"})

	out := captureStdout(t, func() {
		if err := cmd.Execute(); err == nil {
			t.Fatal("expected error when send fails")
		}
	})
	if !strings.Contains(out, "undelivered") {
		t.Errorf("undelivered message should be written to stdout for recovery, got %q", out)
	}
}

func TestForwardTopicCommand_RelaysAll(t *testing.T) {
	msgs := []*backends.Message{{Data: []byte("e1")}, {Data: []byte("e2")}}
	mock := &mockTopicBackend{subscribeMsgs: msgs, subscribeErr: context.Canceled}
	cmd := NewForwardTopicCommand(mock)
	cmd.SetArgs([]string{"src", "dst"})

	captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if mock.publishCount != 2 {
		t.Errorf("publishCount = %d, want 2", mock.publishCount)
	}
	if mock.lastPublishOpts.Topic != "dst" {
		t.Errorf("destination topic = %q, want dst", mock.lastPublishOpts.Topic)
	}
}
