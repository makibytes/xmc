package cmd

import (
	"fmt"
	"strings"
	"testing"
)

func TestTrimHistory_UnderLimit(t *testing.T) {
	h := []aiMessage{
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
	}
	trimHistory(&h, 12)
	if len(h) != 2 {
		t.Errorf("len = %d, want 2", len(h))
	}
}

func TestTrimHistory_AtLimit(t *testing.T) {
	h := make([]aiMessage, 12)
	for i := range h {
		h[i] = aiMessage{Role: "user", Content: fmt.Sprintf("msg%d", i)}
	}
	trimHistory(&h, 12)
	if len(h) != 12 {
		t.Errorf("len = %d, want 12", len(h))
	}
}

func TestTrimHistory_OverLimit(t *testing.T) {
	h := make([]aiMessage, 20)
	for i := range h {
		h[i] = aiMessage{Role: "user", Content: fmt.Sprintf("msg%d", i)}
	}
	trimHistory(&h, 12)
	if len(h) != 12 {
		t.Errorf("len = %d, want 12", len(h))
	}
	// Should keep the newest messages.
	if h[0].Content != "msg8" {
		t.Errorf("first = %q, want msg8", h[0].Content)
	}
	if h[11].Content != "msg19" {
		t.Errorf("last = %q, want msg19", h[11].Content)
	}
}

func TestIsDestructive(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		// Destructive: deleting broker objects and purging message storage.
		{"manage delete-queue orders", true},
		{"manage delete-topic events", true},
		{"manage delete-exchange amq.topic", true},
		{"manage unbind-queue orders", true},
		{"manage purge orders", true},
		{"MANAGE DELETE-QUEUE orders", true},
		{"  manage purge q", true},
		// Non-destructive: fetching/relaying messages is NOT destructive,
		// regardless of drain mode. Only object deletion is destructive.
		{"move dlq orders", false},
		{"forward dlq orders", false},
		{"forward src dst --for 30s", false},
		{"receive orders -n 0", false},
		{"receive orders --count 0", false},
		{"move dlq orders -n 0", false},
		{"subscribe events -n 0", false},
		{"forward src dst -n 0", false},
		{"receive orders -n 5", false},
		{"send orders hello", false},
		{"peek orders", false},
		{"manage list", false},
		{"manage stats orders", false},
		{"manage create-queue test", false},
		{"publish events hello", false},
	}
	for _, tt := range tests {
		got := isDestructive(tt.cmd)
		if got != tt.want {
			t.Errorf("isDestructive(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestBuildFeedback_Success(t *testing.T) {
	fb := buildFeedback(nil, "hello world", "")
	if !strings.Contains(fb, "[execution result] ok") {
		t.Errorf("feedback = %q, want ok", fb)
	}
	if !strings.Contains(fb, "hello world") {
		t.Errorf("feedback should contain stdout")
	}
}

func TestMutatesObjects(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		// Constructive
		{"manage create-queue orders", true},
		{"manage create-topic events", true},
		{"manage create-exchange amq.direct", true},
		{"manage bind-queue orders", true},
		// Destructive
		{"manage delete-queue orders", true},
		{"manage delete-topic events", true},
		{"manage delete-exchange amq.topic", true},
		{"manage unbind-queue orders", true},
		// Message-count changes (not objects)
		{"manage purge orders", false},
		{"move dlq orders", false},
		{"send myqueue hello", false},
		{"publish events hello", false},
		{"receive orders -n 5", false},
		// Non-mutating
		{"peek orders", false},
		{"subscribe events", false},
		{"manage list", false},
		{"manage stats orders", false},
		{"ping", false},
	}
	for _, tt := range tests {
		got := mutatesObjects(tt.cmd)
		if got != tt.want {
			t.Errorf("mutatesObjects(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestMutatesMessages(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"manage purge orders", true},
		{"move dlq orders", true},
		{"send myqueue hello", true},
		{"publish events hello", true},
		{"receive orders -n 5", true},
		{"peek orders", true},
		{"request q hello", true},
		{"reply q", true},
		{"forward q1 q2", true},
		{"subscribe events", true},
		// Not message-count changes
		{"manage create-queue orders", false},
		{"manage delete-queue orders", false},
		{"manage list", false},
		{"manage stats orders", false},
		{"ping", false},
	}
	for _, tt := range tests {
		got := mutatesMessages(tt.cmd)
		if got != tt.want {
			t.Errorf("mutatesMessages(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestIsManageList(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"manage list", true},
		{"manage list -v", true},
		{"manage stats orders", false},
		{"manage create-queue q", false},
		{"send q hello", false},
	}
	for _, tt := range tests {
		got := isManageList(tt.cmd)
		if got != tt.want {
			t.Errorf("isManageList(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestAnyCommand_Destructive(t *testing.T) {
	if !anyCommand("manage create-queue a ; manage delete-queue b", isDestructive) {
		t.Error("should detect destructive segment in multi-command")
	}
	if anyCommand("manage create-queue a ; send a hi", isDestructive) {
		t.Error("create + send should not be destructive")
	}
}

func TestAnyCommand_MutatesObjectsOrMessages(t *testing.T) {
	if !anyCommand("manage create-queue a ; manage list", mutatesObjects) {
		t.Error("should detect create-queue as object-mutating")
	}
	if !anyCommand("receive q ; send q2 hi", mutatesMessages) {
		t.Error("should detect send as message-mutating")
	}
	if anyCommand("manage list ; peek q", mutatesObjects) {
		t.Error("list + peek should not mutate objects")
	}
	if !anyCommand("manage list ; peek q", mutatesMessages) {
		t.Error("peek should mutate messages (it reads)")
	}
}

func TestBuildFeedback_Error(t *testing.T) {
	fb := buildFeedback(fmt.Errorf("queue not found"), "", "error: queue not found")
	if !strings.Contains(fb, "[execution result] error:") {
		t.Errorf("feedback = %q, want error", fb)
	}
	if !strings.Contains(fb, "queue not found") {
		t.Errorf("feedback should contain error message")
	}
}

func TestBuildFeedback_EmptyOutput(t *testing.T) {
	fb := buildFeedback(nil, "", "")
	if !strings.Contains(fb, "ok") {
		t.Errorf("feedback = %q, want ok", fb)
	}
	if strings.Contains(fb, "stdout") {
		t.Errorf("feedback should not mention stdout when empty")
	}
}

func TestTrimHistory_PairBoundary(t *testing.T) {
	// 13 messages alternating user/assistant, starting with user.
	// After trimming to 12, the naive trim starts at an assistant message.
	// The pair-aware trim should skip it to start on a user message.
	h := make([]aiMessage, 13)
	for i := range h {
		if i%2 == 0 {
			h[i] = aiMessage{Role: "user", Content: fmt.Sprintf("u%d", i)}
		} else {
			h[i] = aiMessage{Role: "assistant", Content: fmt.Sprintf("a%d", i)}
		}
	}
	trimHistory(&h, 12)
	if len(h) == 0 {
		t.Fatal("history should not be empty")
	}
	if h[0].Role != "user" {
		t.Errorf("first message role = %q, want user", h[0].Role)
	}
	// Should be 11 messages (dropped the orphan assistant at position 1).
	if len(h) != 11 {
		t.Errorf("len = %d, want 11", len(h))
	}
}

func TestIsDestructive_Cannot(t *testing.T) {
	// "# cannot:" should NOT be considered destructive.
	if isDestructive("# cannot: no such command") {
		t.Error("# cannot should not be destructive")
	}
}

func TestCappedBuffer_UnderLimit(t *testing.T) {
	var buf cappedBuffer
	buf.max = 100
	buf.Write([]byte("hello"))
	if buf.String() != "hello" {
		t.Errorf("got %q", buf.String())
	}
}

func TestCappedBuffer_OverLimit(t *testing.T) {
	var buf cappedBuffer
	buf.max = 10
	buf.Write([]byte("abcdefghijklmnop"))
	got := buf.String()
	if len(got) != 10 {
		t.Errorf("len = %d, want 10", len(got))
	}
	// Should keep the tail.
	if got != "ghijklmnop" {
		t.Errorf("got %q, want tail", got)
	}
}

func TestCappedBuffer_MultipleWrites(t *testing.T) {
	var buf cappedBuffer
	buf.max = 8
	buf.Write([]byte("aaaa"))
	buf.Write([]byte("bbbb"))
	buf.Write([]byte("cccc"))
	got := buf.String()
	if len(got) > 8 {
		t.Errorf("len = %d, want <= 8", len(got))
	}
	// Should end with cccc.
	if !strings.HasSuffix(got, "cccc") {
		t.Errorf("got %q, should end with cccc", got)
	}
}
