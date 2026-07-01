package log

import (
	"fmt"
	"io"
	"os"
)

var IsVerbose bool

// out/errOut, when non-nil, override the default os.Stdout/os.Stderr sinks.
// Used to redirect (or suppress) log output while a full-screen TUI owns the
// terminal — see SetOutput/SetErrorOutput.
var out, errOut io.Writer

// SetOutput redirects Info/Verbose output. Pass nil to restore the default
// (os.Stdout). Returns the previous override (nil if none was set) so the
// caller can restore it later.
func SetOutput(w io.Writer) io.Writer {
	prev := out
	out = w
	return prev
}

// SetErrorOutput redirects Error output. Pass nil to restore the default
// (os.Stderr). Returns the previous override (nil if none was set) so the
// caller can restore it later.
func SetErrorOutput(w io.Writer) io.Writer {
	prev := errOut
	errOut = w
	return prev
}

func stdout() io.Writer {
	if out != nil {
		return out
	}
	return os.Stdout
}

func stderr() io.Writer {
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
