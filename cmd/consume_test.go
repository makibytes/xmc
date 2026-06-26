package cmd

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/makibytes/xmc/broker/backends"
)

// TestOmitFlag_PeekLargerQueue verifies offset semantics: -o N skips the first
// N messages and -n M caps how many are shown afterwards (independent counts).
// Queue: 5 messages; -n 3 -o 2 → show msg-3, msg-4, msg-5.
func TestOmitFlag_PeekLargerQueue(t *testing.T) {
	msgs := []*backends.Message{
		{Data: []byte("msg-1")},
		{Data: []byte("msg-2")},
		{Data: []byte("msg-3")},
		{Data: []byte("msg-4")},
		{Data: []byte("msg-5")},
	}
	mock := &mockQueueBackend{receiveMsgs: msgs}
	cmd := NewPeekCommand(mock)
	cmd.SetArgs([]string{"q", "-n", "3", "-o", "2"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(io.Discard)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"msg-3", "msg-4", "msg-5"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %q", want, out)
		}
	}
	for _, skip := range []string{"msg-1", "msg-2"} {
		if strings.Contains(out, skip) {
			t.Errorf("output contains skipped message %q: %q", skip, out)
		}
	}
}

// TestOmitFlag_PeekSmallerQueue verifies that when the queue exhausts before
// all -n messages are shown, the command exits cleanly (nil error).
// Queue: 3 messages; -n 3 -o 2 → show msg-3 only (queue ends).
func TestOmitFlag_PeekSmallerQueue(t *testing.T) {
	msgs := []*backends.Message{
		{Data: []byte("msg-1")},
		{Data: []byte("msg-2")},
		{Data: []byte("msg-3")},
	}
	mock := &mockQueueBackend{receiveMsgs: msgs}
	cmd := NewPeekCommand(mock)
	cmd.SetArgs([]string{"q", "-n", "3", "-o", "2"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(io.Discard)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected nil error when queue exhausts, got: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "msg-3") {
		t.Errorf("output missing msg-3: %q", out)
	}
	for _, skip := range []string{"msg-1", "msg-2"} {
		if strings.Contains(out, skip) {
			t.Errorf("output contains skipped message %q: %q", skip, out)
		}
	}
}

// TestOmitFlag_OmitExceedsAvailable verifies that when all available messages
// are skipped before any output, a bounded (-n > 0) command returns
// ErrNoMessageAvailable.
func TestOmitFlag_OmitExceedsAvailable(t *testing.T) {
	msgs := []*backends.Message{
		{Data: []byte("msg-1")},
		{Data: []byte("msg-2")},
	}
	mock := &mockQueueBackend{receiveMsgs: msgs}
	cmd := NewPeekCommand(mock)
	cmd.SetArgs([]string{"q", "-n", "5", "-o", "5"})
	cmd.SilenceUsage = true // prevent cobra printing help text on error

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(io.Discard)

	err := cmd.Execute()
	if !errors.Is(err, backends.ErrNoMessageAvailable) {
		t.Errorf("expected ErrNoMessageAvailable, got: %v", err)
	}
	if buf.Len() > 0 {
		t.Errorf("expected no output when all messages skipped, got: %q", buf.String())
	}
}

// TestOmitFlag_ZeroIsNoop verifies that -o 0 (the default) produces the same
// output as not specifying the flag at all.
func TestOmitFlag_ZeroIsNoop(t *testing.T) {
	msgs := []*backends.Message{
		{Data: []byte("msg-1")},
		{Data: []byte("msg-2")},
	}
	mock := &mockQueueBackend{receiveMsgs: msgs}
	cmd := NewPeekCommand(mock)
	cmd.SetArgs([]string{"q", "-n", "2", "-o", "0"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(io.Discard)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "msg-1") || !strings.Contains(out, "msg-2") {
		t.Errorf("expected both messages with -o 0, got: %q", out)
	}
}

// TestOmitFlag_ReceiveDestructive verifies that receive --omit sets
// Acknowledge=true on all reads (the skipped messages are consumed and
// discarded, consistent with receive being a destructive operation).
func TestOmitFlag_ReceiveDestructive(t *testing.T) {
	msgs := []*backends.Message{
		{Data: []byte("msg-1")},
		{Data: []byte("msg-2")},
		{Data: []byte("msg-3")},
	}
	mock := &mockQueueBackend{receiveMsgs: msgs}
	cmd := NewReceiveCommand(mock, nil, nil)
	cmd.SetArgs([]string{"q", "-n", "1", "-o", "2"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(io.Discard)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "msg-3") {
		t.Errorf("output missing msg-3: %q", out)
	}
	for _, skip := range []string{"msg-1", "msg-2"} {
		if strings.Contains(out, skip) {
			t.Errorf("output contains skipped message %q: %q", skip, out)
		}
	}
	if !mock.lastReceiveOpts.Acknowledge {
		t.Error("receive --omit should set Acknowledge=true (destructive read)")
	}
}
