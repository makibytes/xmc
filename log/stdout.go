//go:build !windows

package log

import (
	"os"
	"syscall"
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
	// linux
	if (fileInfo.Mode() & os.ModeCharDevice) == 0 {
		return true
	}

	// macos
	stat, ok := fileInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}

	// macos
	return stat.Rdev == 0
}
