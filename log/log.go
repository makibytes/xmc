package log

import (
	"fmt"
	"io"
	"os"
	"sync"
)

var IsVerbose bool

// out/errOut, when non-nil, override the default os.Stdout/os.Stderr sinks.
// Used to redirect (or suppress) log output while a full-screen TUI owns the
// terminal — see SetOutput/SetErrorOutput. Guarded by outMu: background
// goroutines (reconnect loops, background processes) log concurrently with
// the goroutine that swaps the sinks around a TUI session.
var (
	outMu       sync.Mutex
	out, errOut io.Writer
)

// SetOutput redirects Info/Verbose output. Pass nil to restore the default
// (os.Stdout). Returns the previous override (nil if none was set) so the
// caller can restore it later.
func SetOutput(w io.Writer) io.Writer {
	outMu.Lock()
	defer outMu.Unlock()
	prev := out
	out = w
	return prev
}

// SetErrorOutput redirects Error output. Pass nil to restore the default
// (os.Stderr). Returns the previous override (nil if none was set) so the
// caller can restore it later.
func SetErrorOutput(w io.Writer) io.Writer {
	outMu.Lock()
	defer outMu.Unlock()
	prev := errOut
	errOut = w
	return prev
}

func stdout() io.Writer {
	outMu.Lock()
	defer outMu.Unlock()
	if out != nil {
		return out
	}
	return os.Stdout
}

func stderr() io.Writer {
	outMu.Lock()
	defer outMu.Unlock()
	if errOut != nil {
		return errOut
	}
	return os.Stderr
}

func Info(s string, args ...any) {
	if len(args) > 0 {
		fmt.Fprintf(stdout(), s, args...)
	} else {
		fmt.Fprintln(stdout(), s)
	}
}

func Error(s string, args ...any) {
	if len(args) > 0 {
		fmt.Fprintf(stderr(), s, args...)
	} else {
		fmt.Fprintln(stderr(), s)
	}
}

func Verbose(s string, args ...any) {
	if IsVerbose {
		if len(args) > 0 {
			fmt.Fprintf(stdout(), s, args...)
		} else {
			fmt.Fprintln(stdout(), s)
		}
	}
}
