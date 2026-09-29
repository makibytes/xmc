package cmd

import (
	"fmt"
	"os"
	"testing"
)

// TestMain points HOME (and LOCALAPPDATA on Windows) at a throwaway directory
// for the whole package: several code paths under test persist state —
// shell history on every executed command, /model, /effort, /refresh and
// metadata-format settings — and must never write into the developer's real
// ~/.xmc config.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "xmc-cmd-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create test home:", err)
		os.Exit(1)
	}
	_ = os.Setenv("HOME", dir)
	_ = os.Setenv("LOCALAPPDATA", dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
