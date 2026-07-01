package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
)

func TestWarnUnsupportedFlags(t *testing.T) {
	var buf bytes.Buffer
	prev := log.SetErrorOutput(&buf)
	defer log.SetErrorOutput(prev)

	c := &cobra.Command{Use: "send"}
	c.Flags().Int("priority", 4, "")
	c.Flags().Bool("persistent", false, "")
	if err := c.Flags().Set("priority", "9"); err != nil {
		t.Fatal(err)
	}

	warnUnsupportedFlags(c, []string{"priority", "persistent", "selector"})

	out := buf.String()
	if !strings.Contains(out, "--priority") {
		t.Errorf("expected a note about --priority, got %q", out)
	}
	// persistent was registered but not set; selector is not registered at
	// all — neither should produce a note.
	if strings.Contains(out, "--persistent") || strings.Contains(out, "--selector") {
		t.Errorf("unexpected note for unchanged/unregistered flag: %q", out)
	}
}
