package cmd

import (
	"runtime"
	"strings"
	"testing"

	"github.com/makibytes/xmc/broker/backends"
)

// The target subprocess is real (bridge spawns it via the shell), so tests use
// "sh -c cat" as a target that tolerates the auto-appended --ndjson (an inner
// shell absorbs it as an ignored positional arg) and simply echoes stdin to
// stdout, letting us assert on the streamed NDJSON.

func TestBridgeCommand_QueueSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("command uses a POSIX shell")
	}
	msgs := []*backends.Message{{Data: []byte("a")}, {Data: []byte("b")}}
	mock := &mockQueueBackend{receiveMsgs: msgs}
	cmd := NewBridgeCommand(mock, nil, true, true)
	// -n caps the loop at exactly len(msgs): bridge (unlike forward) has no
	// special-case for a terminal error, so a scripted receiveErr would
	// surface as a command failure instead of a clean stop.
	cmd.SetArgs([]string{"src", "--to", "sh -c cat", "-n", "2"})

	out := captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if mock.receiveCount == 0 {
		t.Error("expected Receive to be called for a queue source")
	}
	if !strings.Contains(out, `"data":"a"`) || !strings.Contains(out, `"data":"b"`) {
		t.Errorf("expected bridged NDJSON payloads in output, got %q", out)
	}
}

func TestBridgeCommand_TopicSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("command uses a POSIX shell")
	}
	msgs := []*backends.Message{{Data: []byte("e1")}}
	mock := &mockTopicBackend{subscribeMsgs: msgs}
	// queueBackend is nil: a plain NewBridgeCommand caller must pass exactly
	// one of queueBackend/topicBackend (WrapBridgeCommand and the shell's
	// pipeline builder both enforce this at the call site).
	cmd := NewBridgeCommand(nil, mock, true, true)
	cmd.SetArgs([]string{"src", "--to", "sh -c cat", "-n", "1"})

	out := captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if mock.subscribeCount == 0 {
		t.Error("expected Subscribe to be called for a topic source")
	}
	if !strings.Contains(out, `"data":"e1"`) {
		t.Errorf("expected bridged NDJSON payload in output, got %q", out)
	}
}

func TestBridgeCommand_FlagPresence(t *testing.T) {
	dual := NewBridgeCommand(nil, nil, true, true)
	if dual.Flags().Lookup("topic") == nil {
		t.Error("expected --topic flag on a dual-capable broker")
	}
	if dual.Flags().Lookup("group") == nil {
		t.Error("expected --group flag when topic-capable")
	}

	queueOnly := NewBridgeCommand(nil, nil, true, false)
	if queueOnly.Flags().Lookup("topic") != nil {
		t.Error("did not expect --topic flag on a queue-only broker")
	}
	if queueOnly.Flags().Lookup("group") != nil {
		t.Error("did not expect --group flag on a queue-only broker")
	}

	topicOnly := NewBridgeCommand(nil, nil, false, true)
	if topicOnly.Flags().Lookup("topic") != nil {
		t.Error("did not expect --topic flag on a topic-only broker (topology is forced)")
	}
	if topicOnly.Flags().Lookup("group") == nil {
		t.Error("expected --group flag on a topic-only broker")
	}
}

func TestResolveBridgeTopology(t *testing.T) {
	// Single-capability brokers force their sole topology regardless of flags.
	if got := resolveBridgeTopology(NewBridgeCommand(nil, nil, true, false), true, false); got {
		t.Error("queue-only broker should force useTopic = false")
	}
	if got := resolveBridgeTopology(NewBridgeCommand(nil, nil, false, true), false, true); !got {
		t.Error("topic-only broker should force useTopic = true")
	}

	// Dual broker reads the --topic flag.
	dual := NewBridgeCommand(nil, nil, true, true)
	if err := dual.ParseFlags([]string{"src", "--to", "x", "--topic"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if got := resolveBridgeTopology(dual, true, true); !got {
		t.Error("dual broker with --topic should resolve useTopic = true")
	}
}
