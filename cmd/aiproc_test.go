package cmd

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

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
		{"", ""},                     // empty input
		{"send q1 hello", "send q1"}, // non-background verb
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := processName(tt.input, nil)
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
			got := commandHasFor(tt.input, nil)
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

// With the command tree, processName knows which flags take a value, so a flag
// placed before the positional doesn't become the process name.
func TestProcessName_FlagAware(t *testing.T) {
	spec, _ := newTestRoot(&mockQueueBackend{}, &mockTopicBackend{})
	root := NewRootCommand(spec)
	tests := map[string]string{
		"receive --for 1h q1":                      "receive q1",
		"receive -n 5 --for=10m q1":                "receive q1",
		"receive --wait q1 --for 1h":               "receive q1", // bool flag: no value skipped
		"get -t 2s orders --for 1m":                "get orders",
		"bridge --to 'rmc receive out' q --for 1h": "bridge q",
		"forward --for 5m src dst":                 "forward src",
		"subscribe --for 1h":                       "subscribe",
	}
	for in, want := range tests {
		if got := processName(in, root); got != want {
			t.Errorf("processName(%q) = %q, want %q", in, got, want)
		}
	}
}

func newTestModelWithProcs(n int) aiTUIModel {
	m := newTestModel()
	for i := range n {
		p := &bgProcess{id: i, name: "receive q" + string(rune('0'+i)), command: "receive q --for 1h"}
		m.procs = append(m.procs, p)
	}
	m.ensureProcWindow()
	m.focus = focusTarget(m.procWinIdx + 1)
	m.enterProcessView()
	return m
}

func pressRune(t *testing.T, m aiTUIModel, key string) aiTUIModel {
	t.Helper()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return updated.(aiTUIModel)
}

// j/k navigate the Processes window like every other sidebar window — k must
// never kill (it used to), K does.
func TestProcessPane_VimKeysNavigate_KKills(t *testing.T) {
	m := newTestModelWithProcs(3)
	killed := map[int]bool{}
	for _, p := range m.procs {
		id := p.id
		p.cancel = func() { killed[id] = true }
	}

	m = pressRune(t, m, "j")
	m = pressRune(t, m, "j")
	if m.procSel != 2 {
		t.Fatalf("after j j, procSel = %d, want 2", m.procSel)
	}
	m = pressRune(t, m, "k")
	if m.procSel != 1 {
		t.Fatalf("after k, procSel = %d, want 1", m.procSel)
	}
	if len(killed) != 0 {
		t.Fatalf("navigation must not kill anything, killed %v", killed)
	}

	m = pressRune(t, m, "K")
	if !killed[1] || len(killed) != 1 {
		t.Errorf("K should kill only the selected process, killed %v", killed)
	}
	if len(m.procs) != 3 {
		t.Errorf("K keeps the entry; procs = %d", len(m.procs))
	}
}

func TestProcessPane_PeekShowsOutput_PPurgesFinished(t *testing.T) {
	m := newTestModelWithProcs(2)
	_, _ = m.procs[0].out.Write([]byte("hello from q0\n"))
	m.procs[1].done = true

	m = pressRune(t, m, "p")
	if !strings.Contains(m.transcript.String(), "hello from q0") {
		t.Errorf("p should show the selected process's output, transcript:\n%s", m.transcript.String())
	}

	m = pressRune(t, m, "P")
	if len(m.procs) != 1 || m.procs[0].id != 0 {
		t.Errorf("P should purge only finished processes, left %d", len(m.procs))
	}
}

func TestProcessDone_TranscriptNotice(t *testing.T) {
	m := newTestModelWithProcs(1)
	updated, _ := m.Update(procDoneMsg{id: 0})
	m = updated.(aiTUIModel)
	if !strings.Contains(m.transcript.String(), "background process finished: receive q0") {
		t.Errorf("finishing process should be announced, transcript:\n%s", m.transcript.String())
	}
}

func TestProcessDone_AfterKill_SaysStopped(t *testing.T) {
	m := newTestModelWithProcs(1)
	m.procs[0].cancel = func() {}
	m = pressRune(t, m, "K")
	updated, _ := m.Update(procDoneMsg{id: 0})
	m = updated.(aiTUIModel)
	if !strings.Contains(m.transcript.String(), "(stopped)") {
		t.Errorf("a killed process should be announced as stopped:\n%s", m.transcript.String())
	}
}

func TestProcessPane_KillBeforeCancelFuncArrives(t *testing.T) {
	m := newTestModelWithProcs(1)
	m = pressRune(t, m, "K") // no cancel func yet
	cancelled := false
	_, _ = m.Update(procCancelMsg{id: 0, cancel: func() { cancelled = true }})
	if !cancelled {
		t.Error("a kill requested before the cancel func arrived must fire it on arrival")
	}
}
