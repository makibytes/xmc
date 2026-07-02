package cmd

import (
	"bytes"
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/makibytes/xmc/broker/backends"
)

func TestReplyCommand_StaticResponse(t *testing.T) {
	request := &backends.Message{
		Data:    []byte("ping"),
		ReplyTo: "reply-q",
	}
	mock := &mockQueueBackend{receiveMsg: request}
	cmd := NewReplyCommand(mock)
	cmd.SetArgs([]string{"requests", "pong", "-n", "1"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.sendCount != 1 {
		t.Fatalf("sendCount = %d, want 1", mock.sendCount)
	}
	if mock.lastSendOpts.Queue != "reply-q" {
		t.Errorf("reply queue = %q, want %q", mock.lastSendOpts.Queue, "reply-q")
	}
	if string(mock.lastSendOpts.Message) != "pong" {
		t.Errorf("reply body = %q, want %q", mock.lastSendOpts.Message, "pong")
	}
	if !mock.lastReceiveOpts.Acknowledge {
		t.Error("reply should acknowledge (consume) the request")
	}
}

func TestReplyCommand_Echo(t *testing.T) {
	request := &backends.Message{Data: []byte("mirror me"), ReplyTo: "r"}
	mock := &mockQueueBackend{receiveMsg: request}
	cmd := NewReplyCommand(mock)
	cmd.SetArgs([]string{"requests", "-e", "-n", "1"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(mock.lastSendOpts.Message) != "mirror me" {
		t.Errorf("echo body = %q, want %q", mock.lastSendOpts.Message, "mirror me")
	}
}

func TestReplyCommand_CorrelationFromCorrelationID(t *testing.T) {
	request := &backends.Message{Data: []byte("x"), ReplyTo: "r", CorrelationID: "corr-7", MessageID: "msg-7"}
	mock := &mockQueueBackend{receiveMsg: request}
	cmd := NewReplyCommand(mock)
	cmd.SetArgs([]string{"requests", "ok", "-n", "1"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.lastSendOpts.CorrelationID != "corr-7" {
		t.Errorf("correlation = %q, want %q", mock.lastSendOpts.CorrelationID, "corr-7")
	}
}

func TestReplyCommand_CorrelationFallsBackToMessageID(t *testing.T) {
	request := &backends.Message{Data: []byte("x"), ReplyTo: "r", MessageID: "msg-9"}
	mock := &mockQueueBackend{receiveMsg: request}
	cmd := NewReplyCommand(mock)
	cmd.SetArgs([]string{"requests", "ok", "-n", "1"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.lastSendOpts.CorrelationID != "msg-9" {
		t.Errorf("correlation = %q, want %q (should fall back to message ID)", mock.lastSendOpts.CorrelationID, "msg-9")
	}
}

func TestReplyCommand_NoReplyToSkips(t *testing.T) {
	request := &backends.Message{Data: []byte("x")} // no reply-to
	mock := &mockQueueBackend{receiveMsg: request}
	cmd := NewReplyCommand(mock)
	cmd.SetArgs([]string{"requests", "ok", "-n", "1"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.sendCount != 0 {
		t.Errorf("sendCount = %d, want 0 (request without reply-to should be skipped)", mock.sendCount)
	}
}

func TestReplyCommand_FallbackReplyTo(t *testing.T) {
	request := &backends.Message{Data: []byte("x")} // no reply-to
	mock := &mockQueueBackend{receiveMsg: request}
	cmd := NewReplyCommand(mock)
	cmd.SetArgs([]string{"requests", "ok", "-R", "fallback-q", "-n", "1"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.sendCount != 1 {
		t.Fatalf("sendCount = %d, want 1", mock.sendCount)
	}
	if mock.lastSendOpts.Queue != "fallback-q" {
		t.Errorf("reply queue = %q, want %q", mock.lastSendOpts.Queue, "fallback-q")
	}
}

func TestReplyCommand_CountBounded(t *testing.T) {
	msgs := []*backends.Message{
		{Data: []byte("a"), ReplyTo: "r"},
		{Data: []byte("b"), ReplyTo: "r"},
		{Data: []byte("c"), ReplyTo: "r"},
	}
	mock := &mockQueueBackend{receiveMsgs: msgs}
	cmd := NewReplyCommand(mock)
	cmd.SetArgs([]string{"requests", "ok", "-n", "2"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.receiveCount != 2 {
		t.Errorf("receiveCount = %d, want 2", mock.receiveCount)
	}
	if mock.sendCount != 2 {
		t.Errorf("sendCount = %d, want 2", mock.sendCount)
	}
}

func TestReplyCommand_EchoAndCommandMutuallyExclusive(t *testing.T) {
	mock := &mockQueueBackend{}
	cmd := NewReplyCommand(mock)
	cmd.SetArgs([]string{"requests", "-e", "-x", "cat", "-n", "1"})

	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for --echo with --command, got nil")
	}
}

func TestReplyCommand_SelectorPassed(t *testing.T) {
	request := &backends.Message{Data: []byte("x"), ReplyTo: "r"}
	mock := &mockQueueBackend{receiveMsg: request}
	cmd := NewReplyCommand(mock)
	cmd.SetArgs([]string{"requests", "ok", "-S", "type='order'", "-n", "1"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.lastReceiveOpts.Selector != "type='order'" {
		t.Errorf("selector = %q, want %q", mock.lastReceiveOpts.Selector, "type='order'")
	}
}

func TestReplyCommand_CommandMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("command mode uses a POSIX shell")
	}
	request := &backends.Message{Data: []byte("shell-in"), ReplyTo: "r"}
	mock := &mockQueueBackend{receiveMsg: request}
	cmd := NewReplyCommand(mock)
	// `cat` echoes stdin to stdout, so the reply body should equal the request.
	cmd.SetArgs([]string{"requests", "-x", "cat", "-n", "1"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(mock.lastSendOpts.Message) != "shell-in" {
		t.Errorf("command reply = %q, want %q", mock.lastSendOpts.Message, "shell-in")
	}
}

func TestReplyCommand_CommandStderrGoesToCommandErrStream(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("command mode uses a POSIX shell")
	}
	request := &backends.Message{Data: []byte("boom"), ReplyTo: "r"}
	mock := &mockQueueBackend{receiveMsg: request}
	cmd := NewReplyCommand(mock)
	// The shell command writes to stderr and fails: both its stderr and the
	// "reply command failed" diagnostic must land on the cobra command's error
	// stream (captured in shell/AI background-process mode), not os.Stderr.
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"requests", "-x", "echo oh-no >&2; exit 3", "-n", "1"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("a failing -x command must not tear down the responder: %v", err)
	}
	if mock.sendCount != 0 {
		t.Errorf("sendCount = %d, want 0 (failed command must not reply)", mock.sendCount)
	}
	got := errBuf.String()
	if !strings.Contains(got, "oh-no") {
		t.Errorf("command stderr not captured on err stream: %q", got)
	}
	if !strings.Contains(got, "reply command failed") {
		t.Errorf("failure diagnostic not captured on err stream: %q", got)
	}
}

func TestReplyCommand_CancellationIsClean(t *testing.T) {
	mock := &mockQueueBackend{receiveErr: context.Canceled}
	cmd := NewReplyCommand(mock)
	cmd.SetArgs([]string{"requests", "ok", "-n", "1"})

	// context.Canceled is treated as a clean shutdown, not an error.
	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected nil on cancellation, got: %v", err)
	}
	if mock.sendCount != 0 {
		t.Errorf("sendCount = %d, want 0", mock.sendCount)
	}
}
