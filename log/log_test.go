package log

import (
	"bytes"
	"os"
	"testing"
)

// capture runs fn with the log sink redirected and returns what was logged.
func capture(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := SetErrorOutput(&buf)
	defer SetErrorOutput(prev)
	fn()
	return buf.String()
}

func TestError_EndsWithExactlyOneNewline(t *testing.T) {
	cases := []struct {
		name string
		log  func()
		want string
	}{
		{"formatted", func() { Error("error: %s", "something failed") }, "error: something failed\n"},
		{"plain", func() { Error("plain error") }, "plain error\n"},
		{"already terminated", func() { Error("warning: %s\n", "x") }, "warning: x\n"},
		{"plain already terminated", func() { Error("done\n") }, "done\n"},
		{"percent without args is literal", func() { Error("100% sure") }, "100% sure\n"},
	}
	for _, tc := range cases {
		if got := capture(t, tc.log); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestVerbose_WhenEnabled(t *testing.T) {
	origVerbose := IsVerbose
	IsVerbose = true
	defer func() { IsVerbose = origVerbose }()

	if got := capture(t, func() { Verbose("debug: %d", 123) }); got != "debug: 123\n" {
		t.Errorf("Verbose() = %q, want %q", got, "debug: 123\n")
	}
	if got := capture(t, func() { Verbose("plain verbose message") }); got != "plain verbose message\n" {
		t.Errorf("Verbose() = %q, want %q", got, "plain verbose message\n")
	}
}

// Verbose output must never reach stdout, which carries message data.
func TestVerbose_DoesNotWriteToStdout(t *testing.T) {
	origVerbose := IsVerbose
	IsVerbose = true
	defer func() { IsVerbose = origVerbose }()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	_ = capture(t, func() { Verbose("diagnostic %d", 1) })
	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	if buf.Len() != 0 {
		t.Errorf("Verbose wrote %q to stdout", buf.String())
	}
}

func TestVerbose_WhenDisabled(t *testing.T) {
	origVerbose := IsVerbose
	IsVerbose = false
	defer func() { IsVerbose = origVerbose }()

	if got := capture(t, func() { Verbose("should not appear") }); got != "" {
		t.Errorf("Verbose() when disabled = %q, want empty", got)
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

	if isStdoutRedirected() {
		t.Error("expected isStdoutRedirected() = false when stdout is a terminal")
	}
}
