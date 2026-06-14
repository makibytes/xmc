package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/makibytes/xmc/broker/backends"
)

func TestParseDurationFlag(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		err  bool
	}{
		{"", 0, false},
		{"30s", 30 * time.Second, false},
		{"5m", 5 * time.Minute, false},
		{"1h30m", 90 * time.Minute, false},
		{"bad", 0, true},
		{"-1s", 0, true},
	}
	for _, c := range cases {
		got, err := parseDurationFlag(c.in)
		if c.err {
			if err == nil {
				t.Errorf("parseDurationFlag(%q): expected error", c.in)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("parseDurationFlag(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.n); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestStreamStats_Summary(t *testing.T) {
	s := newStreamStats()
	s.record(10)
	s.record(20)
	s.record(30)
	if s.count.Load() != 3 {
		t.Errorf("count = %d, want 3", s.count.Load())
	}
	if s.bytes.Load() != 60 {
		t.Errorf("bytes = %d, want 60", s.bytes.Load())
	}
	sum := s.summary()
	if !strings.Contains(sum, "3 msgs") || !strings.Contains(sum, "60 B") {
		t.Errorf("summary = %q", sum)
	}
}

func TestStreamContext_Timeout(t *testing.T) {
	ctx, cancel := streamContext(30 * time.Millisecond)
	defer cancel()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("streamContext did not time out")
	}
	if ctx.Err() == nil {
		t.Error("expected ctx error after timeout")
	}
}

func TestStreamContext_NoLimit(t *testing.T) {
	ctx, cancel := streamContext(0)
	defer cancel()
	if ctx.Err() != nil {
		t.Error("no-limit context should not be done immediately")
	}
}

func TestReceiveCommand_StatsBounded(t *testing.T) {
	mock := &mockQueueBackend{receiveMsgs: []*backends.Message{{Data: []byte("a")}, {Data: []byte("b")}}}
	cmd := NewReceiveCommand(mock)
	cmd.SetArgs([]string{"q", "-n", "2", "--stats", "--ndjson"})

	out := captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Errorf("consumed %d records, want 2", len(lines))
	}
}

func TestReceiveCommand_ForEmptyStops(t *testing.T) {
	mock := &mockQueueBackend{} // empty source: returns (nil, nil) repeatedly
	cmd := NewReceiveCommand(mock)
	cmd.SetArgs([]string{"q", "--for", "50ms"})

	start := time.Now()
	captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("--for did not stop the stream promptly: took %v", d)
	}
}
