// Package log provides Error/Verbose output helpers with a verbose toggle.
// Both write to stderr: stdout is reserved for message data, so diagnostics
// must never mix into it (or into a pipe fed from it).
package log

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// IsVerbose enables Verbose output when set.
var IsVerbose bool

// errOut, when non-nil, overrides the default os.Stderr sink. Used to redirect
// (or suppress) log output while a full-screen TUI owns the terminal — see
// SetErrorOutput. Guarded by outMu: background goroutines (reconnect loops,
// background processes) log concurrently with the goroutine that swaps the
// sink around a TUI session.
var (
	outMu  sync.Mutex
	errOut io.Writer
)

// SetErrorOutput redirects Error and Verbose output. Pass nil to restore the
// default (os.Stderr). Returns the previous override (nil if none was set) so
// the caller can restore it later.
func SetErrorOutput(w io.Writer) io.Writer {
	outMu.Lock()
	defer outMu.Unlock()
	prev := errOut
	errOut = w
	return prev
}

func stderr() io.Writer {
	outMu.Lock()
	defer outMu.Unlock()
	if errOut != nil {
		return errOut
	}
	return os.Stderr
}

// write formats s with args (when there are any) and ends the message with
// exactly one newline, whether or not s already carries one.
func write(w io.Writer, s string, args []any) {
	if len(args) > 0 {
		s = fmt.Sprintf(s, args...)
	}
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	_, _ = io.WriteString(w, s)
}

// Error writes an error message to stderr.
func Error(s string, args ...any) { write(stderr(), s, args) }

// Verbose writes a message to stderr when IsVerbose is set.
func Verbose(s string, args ...any) {
	if IsVerbose {
		write(stderr(), s, args)
	}
}
