package log

import (
	"os"

	"golang.org/x/term"
)

// IsStdout reports whether stdout is a terminal (not redirected to a file or pipe).
var IsStdout = !isStdoutRedirected()

func isStdoutRedirected() bool {
	return !term.IsTerminal(int(os.Stdout.Fd()))
}
