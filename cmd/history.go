package cmd

import (
	"bufio"
	"fmt"
	"os"
)

// shellHistory is the shared shell command history store.
var shellHistory = &historyStore{path: shellHistoryPath}

// askHistory is the AI prompt history store.
var askHistory = &historyStore{path: askHistoryPath}

// historyStore provides load/append access to a line-oriented history file.
// The path function is called each time to resolve the file path, allowing
// different instances to share different history files (e.g. shell vs ask).
type historyStore struct {
	path func() (string, error)
}

// Load reads all non-empty lines from the history file. Returns nil on error.
func (h *historyStore) Load() []string {
	path, err := h.path()
	if err != nil {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if line := scanner.Text(); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// Append writes a line to the history file, creating it if necessary.
func (h *historyStore) Append(line string) {
	path, err := h.path()
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}
