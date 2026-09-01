package log

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestInfo_WithArgs(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	Info("hello %s %d", "world", 42)

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if got := buf.String(); got != "hello world 42" {
		t.Errorf("Info() = %q, want %q", got, "hello world 42")
	}
}

func TestInfo_WithoutArgs(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	Info("simple message")

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if got := strings.TrimSpace(buf.String()); got != "simple message" {
		t.Errorf("Info() = %q, want %q", got, "simple message")
	}
}

func TestError_WithArgs(t *testing.T) {
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	Error("error: %s", "something failed")

	_ = w.Close()
	os.Stderr = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if got := buf.String(); got != "error: something failed" {
		t.Errorf("Error() = %q, want %q", got, "error: something failed")
	}
}

func TestError_WithoutArgs(t *testing.T) {
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	Error("plain error")

	_ = w.Close()
	os.Stderr = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if got := strings.TrimSpace(buf.String()); got != "plain error" {
		t.Errorf("Error() = %q, want %q", got, "plain error")
	}
}

func TestVerbose_WhenEnabled(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	origVerbose := IsVerbose
	IsVerbose = true
	defer func() { IsVerbose = origVerbose }()

	Verbose("debug: %d", 123)

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if got := buf.String(); got != "debug: 123" {
		t.Errorf("Verbose() = %q, want %q", got, "debug: 123")
	}
}

func TestVerbose_WhenDisabled(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	origVerbose := IsVerbose
	IsVerbose = false
	defer func() { IsVerbose = origVerbose }()

	Verbose("should not appear")

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if got := buf.String(); got != "" {
		t.Errorf("Verbose() when disabled = %q, want empty", got)
	}
}

func TestVerbose_WithoutArgs_WhenEnabled(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	origVerbose := IsVerbose
	IsVerbose = true
	defer func() { IsVerbose = origVerbose }()

	Verbose("plain verbose message")

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if got := strings.TrimSpace(buf.String()); got != "plain verbose message" {
		t.Errorf("Verbose() = %q, want %q", got, "plain verbose message")
	}
}

func TestIsStdoutRedirected_WithPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close() //nolint:errcheck

	old := os.Stdout
	os.Stdout = w
	defer func() {
		os.Stdout = old
		_ = w.Close()
	}()

	if !isStdoutRedirected() {
		t.Error("expected isStdoutRedirected() = true when stdout is a pipe")
	}
}

func TestIsStdoutRedirected_WithCharDevice(t *testing.T) {
	tty, err := os.Open("/dev/tty")
	if err != nil {
		t.Skip("cannot open /dev/tty (headless environment): " + err.Error())
	}
	defer tty.Close() //nolint:errcheck

	old := os.Stdout
	os.Stdout = tty
	defer func() { os.Stdout = old }()

	// Calling with a char device exercises the syscall.Stat_t path
	_ = isStdoutRedirected()
}
