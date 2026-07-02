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
	cmd := NewForwardCommand(mock, nil, true, false)
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
	cmd := NewForwardCommand(mock, nil, true, false)
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
		Key:           "part-1",
		MessageID:     "orig",
		CorrelationID: "c1",
		ReplyTo:       "rq",
		ContentType:   "application/json",
		Priority:      6,
		Persistent:    true,
		Properties:    map[string]any{"env": "prod"},
	}
	mock := &mockQueueBackend{receiveMsgs: []*backends.Message{msg}, receiveErr: context.Canceled}
	cmd := NewForwardCommand(mock, nil, true, false)
	cmd.SetArgs([]string{"src", "dst"})

	captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	// forward always preserves metadata (docs/BRIDGE_AND_FORWARD.md: "Metadata:
	// Always preserved"), including the original message ID and partition/routing
	// key — unlike a fresh publish, this is a same-broker relay, not a new message.
	o := mock.lastSendOpts
	if string(o.Message) != "payload" || o.Key != "part-1" || o.MessageID != "orig" ||
		o.CorrelationID != "c1" || o.ReplyTo != "rq" ||
		o.ContentType != "application/json" || o.Priority != 6 || !o.Persistent || o.Properties["env"] != "prod" {
		t.Errorf("metadata not preserved: %+v", o)
	}
}

func TestForwardCommand_SameSourceAndDestination(t *testing.T) {
	mock := &mockQueueBackend{}
	cmd := NewForwardCommand(mock, nil, true, false)
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
	cmd := NewForwardCommand(mock, nil, true, false)
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
	cmd := NewForwardCommand(mock, nil, true, false)
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
	cmd := NewForwardCommand(mock, nil, true, false)
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
	cmd := NewForwardCommand(nil, mock, false, true)
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

func TestForwardCommand_FlagPresence(t *testing.T) {
	dual := NewForwardCommand(nil, nil, true, true)
	if dual.Flags().Lookup("from-topic") == nil {
		t.Error("expected --from-topic flag on a dual-capable broker")
	}
	if dual.Flags().Lookup("to-topic") == nil {
		t.Error("expected --to-topic flag on a dual-capable broker")
	}
	if dual.Flags().Lookup("group") == nil {
		t.Error("expected --group flag when topic-capable")
	}

	queueOnly := NewForwardCommand(nil, nil, true, false)
	if queueOnly.Flags().Lookup("from-topic") != nil {
		t.Error("did not expect --from-topic flag on a queue-only broker")
	}
	if queueOnly.Flags().Lookup("group") != nil {
		t.Error("did not expect --group flag on a queue-only broker")
	}

	topicOnly := NewForwardCommand(nil, nil, false, true)
	if topicOnly.Flags().Lookup("from-topic") != nil {
		t.Error("did not expect --from-topic flag on a topic-only broker (topology is forced)")
	}
	if topicOnly.Flags().Lookup("group") == nil {
		t.Error("expected --group flag on a topic-only broker")
	}
}

func TestForwardCommand_QueueToTopic(t *testing.T) {
	msgs := []*backends.Message{{Data: []byte("a")}}
	qMock := &mockQueueBackend{receiveMsgs: msgs, receiveErr: context.Canceled}
	tMock := &mockTopicBackend{}
	cmd := NewForwardCommand(qMock, tMock, true, true)
	cmd.SetArgs([]string{"src", "dst", "--to-topic"})

	captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if tMock.publishCount != 1 {
		t.Errorf("publishCount = %d, want 1", tMock.publishCount)
	}
	if qMock.sendCount != 0 {
		t.Errorf("sendCount = %d, want 0 (destination should be the topic, not the queue)", qMock.sendCount)
	}
	if tMock.lastPublishOpts.Topic != "dst" {
		t.Errorf("destination = %q, want dst", tMock.lastPublishOpts.Topic)
	}
}

func TestForwardCommand_TopicToQueue(t *testing.T) {
	msgs := []*backends.Message{{Data: []byte("e1")}}
	qMock := &mockQueueBackend{}
	tMock := &mockTopicBackend{subscribeMsgs: msgs, subscribeErr: context.Canceled}
	cmd := NewForwardCommand(qMock, tMock, true, true)
	cmd.SetArgs([]string{"src", "dst", "--from-topic"})

	captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if qMock.sendCount != 1 {
		t.Errorf("sendCount = %d, want 1", qMock.sendCount)
	}
	if qMock.lastSendOpts.Queue != "dst" {
		t.Errorf("destination = %q, want dst", qMock.lastSendOpts.Queue)
	}
}

func TestForwardCommand_CrossTopology_SameNameAllowed(t *testing.T) {
	// A queue and a topic with the same name are distinct entities, so
	// crossing topologies must not trip the "source and destination must
	// differ" guard the way same-topology same-name does.
	qMock := &mockQueueBackend{receiveErr: context.Canceled}
	tMock := &mockTopicBackend{}
	cmd := NewForwardCommand(qMock, tMock, true, true)
	cmd.SetArgs([]string{"same", "same", "--to-topic"})

	captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("cross-topology same name should not error: %v", err)
		}
	})
}

func TestForwardCommand_SameTopologySameName_StillErrors(t *testing.T) {
	qMock := &mockQueueBackend{}
	tMock := &mockTopicBackend{}
	cmd := NewForwardCommand(qMock, tMock, true, true)
	cmd.SetArgs([]string{"same", "same"}) // both default to queue

	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when source == destination on the same topology")
	}
	if qMock.receiveCount != 0 {
		t.Errorf("receiveCount = %d, want 0 (should fail before consuming)", qMock.receiveCount)
	}
}
