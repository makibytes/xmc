package cmd

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/makibytes/xmc/broker/backends"
)

func TestDurationValue_AcceptsDurationsAndNumbers(t *testing.T) {
	cases := []struct {
		in       string
		bareUnit time.Duration
		want     time.Duration
		err      bool
	}{
		{"100ms", time.Second, 100 * time.Millisecond, false},
		{"5s", time.Second, 5 * time.Second, false},
		{"1m30s", time.Second, 90 * time.Second, false},
		{"0", time.Second, 0, false},
		{"0.1", time.Second, 100 * time.Millisecond, false}, // bare number -> seconds
		{"30", time.Second, 30 * time.Second, false},        // bare number -> seconds
		{"5000", time.Millisecond, 5 * time.Second, false},  // bare number -> ms (ttl unit)
		{"bad", time.Second, 0, true},
		{"-5s", time.Second, 0, true},
		{"-1", time.Second, 0, true},
	}
	for _, c := range cases {
		v := newDurationValue(0, c.bareUnit)
		err := v.Set(c.in)
		if c.err {
			if err == nil {
				t.Errorf("Set(%q): expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("Set(%q): unexpected error %v", c.in, err)
			continue
		}
		if v.d != c.want {
			t.Errorf("Set(%q) bareUnit=%v => %v, want %v", c.in, c.bareUnit, v.d, c.want)
		}
	}
}

func TestSendCommand_TTLAcceptsDurationString(t *testing.T) {
	mock := &mockQueueBackend{}
	cmd := NewSendCommand(mock, nil, nil)
	cmd.SetArgs([]string{"q", "hi", "--ttl", "5s"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.lastSendOpts.TTL != 5000 {
		t.Errorf("TTL = %d ms, want 5000 (5s)", mock.lastSendOpts.TTL)
	}
}

func TestSendCommand_TTLLegacyNumberIsMillis(t *testing.T) {
	mock := &mockQueueBackend{}
	cmd := NewSendCommand(mock, nil, nil)
	cmd.SetArgs([]string{"q", "hi", "-E", "1500"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.lastSendOpts.TTL != 1500 {
		t.Errorf("TTL = %d ms, want 1500 (legacy bare number = milliseconds)", mock.lastSendOpts.TTL)
	}
}

func TestSendCommand_ContentTypeAliases(t *testing.T) {
	// Both the kebab-case name and the legacy concatenated name must work.
	for _, name := range []string{"--content-type", "--contenttype"} {
		mock := &mockQueueBackend{}
		cmd := NewSendCommand(mock, nil, nil)
		cmd.SetArgs([]string{"q", "hi", name, "application/json"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		if mock.lastSendOpts.ContentType != "application/json" {
			t.Errorf("%s: ContentType = %q, want application/json", name, mock.lastSendOpts.ContentType)
		}
	}
}

func TestForwardCommand_LongCommandFlag(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("command uses a POSIX shell")
	}
	mock := &mockQueueBackend{
		receiveMsgs: []*backends.Message{{Data: []byte("hello")}},
		receiveErr:  context.Canceled,
	}
	cmd := NewForwardCommand(mock)
	cmd.SetArgs([]string{"src", "dst", "--command", "tr a-z A-Z"})

	captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if string(mock.lastSendOpts.Message) != "HELLO" {
		t.Errorf("payload via --command = %q, want HELLO", mock.lastSendOpts.Message)
	}
}
