package cmd

import (
	"fmt"
	"testing"
)

type fakeCloser struct{ closed bool }

func (f *fakeCloser) Close() error { f.closed = true; return nil }

func TestPingCommand_Success(t *testing.T) {
	var calls int
	cmd := NewPingCommand(func() (Closeable, error) {
		calls++
		return &fakeCloser{}, nil
	})
	cmd.SetArgs([]string{"-n", "3", "-i", "0"})

	out := captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if calls != 3 {
		t.Errorf("connect attempts = %d, want 3", calls)
	}
	if !containsAll(out, "PING", "connected", "statistics") {
		t.Errorf("unexpected ping output: %q", out)
	}
}

func TestPingCommand_Failure(t *testing.T) {
	cmd := NewPingCommand(func() (Closeable, error) {
		return nil, fmt.Errorf("connection refused")
	})
	cmd.SetArgs([]string{"-n", "1", "-i", "0"})

	captureStdout(t, func() {
		if err := cmd.Execute(); err == nil {
			t.Fatal("expected non-zero exit when ping fails")
		}
	})
}

func TestPingCommand_PartialFailureExitsNonZero(t *testing.T) {
	var calls int
	cmd := NewPingCommand(func() (Closeable, error) {
		calls++
		if calls == 2 {
			return nil, fmt.Errorf("flaky")
		}
		return &fakeCloser{}, nil
	})
	cmd.SetArgs([]string{"-n", "3", "-i", "0"})

	captureStdout(t, func() {
		if err := cmd.Execute(); err == nil {
			t.Fatal("expected error when any attempt fails")
		}
	})
	if calls != 3 {
		t.Errorf("connect attempts = %d, want 3", calls)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
