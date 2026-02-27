package cmd

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/makibytes/xmc/broker/backends"
)

func TestWrapQueueCommand_CreatesAdapter(t *testing.T) {
	mock := &mockQueueBackend{}
	factory := QueueAdapterFactory(func() (backends.QueueBackend, error) {
		return mock, nil
	})

	cmd := WrapQueueCommand(NewSendCommand, factory)
	cmd.SetArgs([]string{"test-queue", "hello"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.lastSendOpts.Queue != "test-queue" {
		t.Errorf("queue = %q, want %q", mock.lastSendOpts.Queue, "test-queue")
	}
}

func TestWrapQueueCommand_FactoryError(t *testing.T) {
	factory := QueueAdapterFactory(func() (backends.QueueBackend, error) {
		return nil, fmt.Errorf("connection failed")
	})

	cmd := WrapQueueCommand(NewSendCommand, factory)
	cmd.SetArgs([]string{"test-queue", "hello"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error from factory, got nil")
	}
}

func TestWrapTopicCommand_CreatesAdapter(t *testing.T) {
	mock := &mockTopicBackend{}
	factory := TopicAdapterFactory(func() (backends.TopicBackend, error) {
		return mock, nil
	})

	cmd := WrapTopicCommand(NewPublishCommand, factory)
	cmd.SetArgs([]string{"test-topic", "hello"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.lastPublishOpts.Topic != "test-topic" {
		t.Errorf("topic = %q, want %q", mock.lastPublishOpts.Topic, "test-topic")
	}
}

func TestWrapTopicCommand_FactoryError(t *testing.T) {
	factory := TopicAdapterFactory(func() (backends.TopicBackend, error) {
		return nil, fmt.Errorf("connection failed")
	})

	cmd := WrapTopicCommand(NewPublishCommand, factory)
	cmd.SetArgs([]string{"test-topic", "hello"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error from factory, got nil")
	}
}

func TestVersionCommand_Execute(t *testing.T) {
cmd := NewVersionCommand()

old := os.Stdout
r, w, _ := os.Pipe()
os.Stdout = w

cmd.Execute()
w.Close()
os.Stdout = old

var buf bytes.Buffer
buf.ReadFrom(r)
got := strings.TrimSpace(buf.String())
if got != "dev" {
t.Errorf("version = %q, want %q", got, "dev")
}
}
