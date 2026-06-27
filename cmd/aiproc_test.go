package cmd

import "testing"

// ---------- processName ----------

func TestProcessName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"receive q1 --for 1h", "receive q1"},
		{"forward q1 q2 --for 5m", "forward q1"},
		{"subscribe t1", "subscribe t1"},
		{"peek q --for 5s", "peek q"},
		// shellSplit strips quotes, so --to's value "rmc receive out" becomes the
		// first non-flag token and is used as the object name.
		{"bridge --to 'rmc receive out' q --for 1h", "bridge rmc receive out"},
		// no positional: --for's value "1h" is a flag value (starts with -? no, but --for
		// itself is skipped; "1h" starts with a digit so it becomes the object)
		{"receive --for 1h", "receive 1h"},
		{"", ""},                            // empty input
		{"send q1 hello", "send q1"},        // non-background verb
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := processName(tt.input)
			if got != tt.want {
				t.Errorf("processName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------- commandHasFor ----------

func TestCommandHasFor(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"receive q1 --for 1h", true},
		{"peek q --for 5s", true},
		{"subscribe t1 --for 30m", true},
		{"forward q1 q2 --for 10m", true},
		{"bridge --to 'rmc receive out' q --for 1h", true},
		// --for= syntax
		{"receive q1 --for=1h", true},
		// --forever flag
		{"receive q1 --forever", true},
		{"forward q1 q2 --forever", true},
		{"peek q --forever", true},
		{"subscribe t1 --forever", true},
		{"bridge --to 'rmc receive out' q --forever", true},
		// non-background verbs
		{"send q1 hello", false},
		{"publish t1 hello", false},
		// background verb but no --for/--forever
		{"receive q1", false},
		{"receive q1 --count 5", false},
		// --for with empty value (next token is another flag)
		{"receive q1 --for --count 5", false},
		// pipeline with a --for segment
		{"send q1 hello; receive q1 --for 1h", true},
		// pipeline with --forever segment
		{"send q1 hello; receive q1 --forever", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := commandHasFor(tt.input)
			if got != tt.want {
				t.Errorf("commandHasFor(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// ---------- commandWellFormed ----------

func TestCommandWellFormed(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		// Safe: '&' inside double quotes
		{`send q "a & b"`, true},
		// Safe: '&' inside single quotes
		{"send q 'a & b'", true},
		// Safe: no '&' at all
		{"receive q1 --for 1h", true},
		{"send q hello", true},
		// Safe: & in the middle (pipeline) — we only reject a trailing bare &
		{"send q hello; receive q1", true},
		// Bad: bare trailing '&'
		{"receive q1 &", false},
		{"receive q1 --for 1h &", false},
		// Bad: trailing & with no space (immediately after last token)
		{"receive q1&", false},
		// Safe: empty string
		{"", true},
		// '&' inside quotes but also bare trailing '&' — should reject
		{`send q "a & b" &`, false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := commandWellFormed(tt.input)
			if got != tt.want {
				t.Errorf("commandWellFormed(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
