//go:build windows

package log

import (
	"os"
)

// IsStdout reports whether stdout is a terminal (not redirected to a file or pipe).
var IsStdout = true

func init() {
	if isStdoutRedirected() {
		IsStdout = false
	}
}

func isStdoutRedirected() bool {
	fileInfo, _ := os.Stdout.Stat()
	// available on windows, but does it work?
	if (fileInfo.Mode() & os.ModeCharDevice) == 0 {
		return true
	}
	return false
}
