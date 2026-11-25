package cmd

import (
	"strings"
	"testing"
)

func TestForEachInputLine_AcceptsLinesBeyondTheDefaultScannerLimit(t *testing.T) {
	long := strings.Repeat("x", 200*1024) // bufio.Scanner's default limit is 64 KB
	var got []int
	n, err := forEachInputLine(strings.NewReader("short\n"+long+"\nend\n"), func(line string) error {
		got = append(got, len(line))
		return nil
	})
	if err != nil {
		t.Fatalf("forEachInputLine: %v", err)
	}
	if n != 3 || len(got) != 3 || got[1] != len(long) {
		t.Errorf("processed %d lines with lengths %v, want 3 lines with the long one intact", n, got)
	}
}
