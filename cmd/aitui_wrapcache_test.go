package cmd

import (
	"strings"
	"testing"
)

// The wrap cache must be invisible: whatever sequence of appends, resets and
// width changes happened, the cached result equals wrapping everything afresh.
func TestWrappedTranscript_MatchesFullWrap(t *testing.T) {
	m := newTestModel()
	chunks := []string{
		"you: hello there, this is a fairly long line that must wrap at narrow widths\n\n",
		dimStyle.Render("styled ") + warnStyle.Render("segments that carry ANSI sequences across a wrap boundary") + "\n",
		strings.Repeat("x", 120) + "\n", // one unbreakable word
		"日本語のテキスト、全角文字を含む行がここにあります。\n",
		"partial line without newline",
		" ...continued\n",
		"\n\n",
	}
	overlays := []string{"", "streaming…", dimStyle.Render("thinking about it") + "\n"}

	var transcript strings.Builder
	for _, width := range []int{20, 37, 80} {
		for i, chunk := range chunks {
			transcript.WriteString(chunk)
			for _, overlay := range overlays {
				got := m.wrappedTranscript(transcript.String(), overlay, width)
				want := wrapText(transcript.String()+overlay, width)
				if got != want {
					t.Fatalf("width %d after chunk %d, overlay %q:\n got  %q\n want %q", width, i, overlay, got, want)
				}
			}
		}
	}

	// A reset followed by different content of any length is detected too.
	transcript.Reset()
	transcript.WriteString("fresh start\n")
	got := m.wrappedTranscript(transcript.String(), "", 37)
	if want := wrapText("fresh start\n", 37); got != want {
		t.Errorf("after reset: got %q, want %q", got, want)
	}
}

// Repainting without a content change (typing, blinking) must not touch the
// viewport content, while a real change must.
func TestSetViewportContent_SkipsUnchangedContent(t *testing.T) {
	m := newTestModel()
	m.appendTranscript("hello\n")
	before := m.wrappedContentLines
	m.setViewportContent()
	if len(m.wrappedContentLines) != len(before) || &m.wrappedContentLines[0] != &before[0] {
		t.Error("an unchanged repaint re-split the content")
	}
	m.appendTranscript("world\n")
	if !strings.Contains(m.viewport.View(), "world") {
		t.Errorf("appended text missing from the viewport:\n%s", m.viewport.View())
	}
}
