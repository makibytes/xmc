package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/makibytes/xmc/broker/backends"
)

// newTestRoot builds a full CLI tree (queue + topic + manage + ping) over
// in-memory fakes, the way a broker entry file would.
func newTestRoot(qb backends.QueueBackend, tb backends.TopicBackend) (BrokerSpec, *shellSession) {
	spec := BrokerSpec{
		Use:   "xmc",
		Queue: func() (backends.QueueBackend, error) { return qb, nil },
		Topic: func() (backends.TopicBackend, error) { return tb, nil },
		Ping:  func() (Closeable, error) { return &fakeCloser{}, nil },
		ManageSpec: &ManageSpec{
			Objects: []ObjectType{{Label: "Queues", List: func() ([]backends.ObjectNode, error) { return nil, nil }}},
		},
	}
	sess := &shellSession{
		spec:         spec,
		queueFactory: spec.Queue,
		topicFactory: spec.Topic,
	}
	return spec, sess
}

// notShellVerbs are the root commands that intentionally cannot run inside
// the shell (they start their own UI/server or are meta commands).
var notShellVerbs = map[string]bool{
	"shell": true, "ai": true, "version": true, "completion": true, "help": true,
}

// TestShellVerbs_CoverCobraTree pins the shell's verb registry to the commands
// NewRootCommand actually registers: every runnable verb (and each of its
// cobra aliases) must classify as an in-process verb with the right canonical
// name, and the session must be able to build it. This is what used to drift:
// "put" (send's alias) fell through to the system shell, and "ping" classified
// as a verb that buildVerbCommand could not build.
func TestShellVerbs_CoverCobraTree(t *testing.T) {
	spec, sess := newTestRoot(&mockQueueBackend{}, &mockTopicBackend{})
	root := NewRootCommand(spec)

	for _, c := range root.Commands() {
		if notShellVerbs[c.Name()] {
			continue
		}
		for _, name := range append([]string{c.Name()}, c.Aliases...) {
			if got := canonicalVerb(name); got != c.Name() {
				t.Errorf("canonicalVerb(%q) = %q, want %q", name, got, c.Name())
			}
		}
		if _, err := sess.buildVerbCommand(c.Name(), root); err != nil {
			t.Errorf("buildVerbCommand(%q): %v", c.Name(), err)
		}
	}
}

func TestExecutePipelineIO_PutAliasSends(t *testing.T) {
	qb := &mockQueueBackend{}
	spec, sess := newTestRoot(qb, &mockTopicBackend{})
	root := NewRootCommand(spec)

	var out, errw bytes.Buffer
	if err := sess.executePipelineIO(context.Background(), "put orders hello", root, strings.NewReader(""), &out, &errw); err != nil {
		t.Fatalf("put: %v (stderr %q)", err, errw.String())
	}
	if qb.sendCount != 1 || qb.lastSendOpts.Queue != "orders" || string(qb.lastSendOpts.Message) != "hello" {
		t.Errorf("put did not send: count=%d opts=%+v", qb.sendCount, qb.lastSendOpts)
	}
}

func TestExecutePipelineIO_PingWritesToOut(t *testing.T) {
	spec, sess := newTestRoot(&mockQueueBackend{}, &mockTopicBackend{})
	root := NewRootCommand(spec)

	var out, errw bytes.Buffer
	if err := sess.executePipelineIO(context.Background(), "ping -i 0", root, strings.NewReader(""), &out, &errw); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if !containsAll(out.String(), "PING", "connected: seq=1", "statistics") {
		t.Errorf("ping output not routed to the pipeline writer: %q", out.String())
	}
}

// TestExecutePipelineIO_HelpVerb_DoesNotRunVerb guards against the old
// behaviour where "help send" executed rootCmd with args ["send"] — i.e. ran
// the real send command — instead of printing its help.
func TestExecutePipelineIO_HelpVerb_DoesNotRunVerb(t *testing.T) {
	qb := &mockQueueBackend{}
	spec, sess := newTestRoot(qb, &mockTopicBackend{})
	root := NewRootCommand(spec)

	var out, errw bytes.Buffer
	if err := sess.executePipelineIO(context.Background(), "help send", root, strings.NewReader(""), &out, &errw); err != nil {
		t.Fatalf("help send: %v", err)
	}
	if qb.sendCount != 0 {
		t.Errorf("help send ran the send command (%d sends)", qb.sendCount)
	}
	if !containsAll(out.String(), "send <queue>", "--content-type") {
		t.Errorf("help send output = %q, want send usage", out.String())
	}

	// Aliases resolve too.
	out.Reset()
	if err := sess.executePipelineIO(context.Background(), "help put", root, strings.NewReader(""), &out, &errw); err != nil {
		t.Fatalf("help put: %v", err)
	}
	if !strings.Contains(out.String(), "send <queue>") {
		t.Errorf("help put output = %q, want send usage", out.String())
	}
}

func TestExecutePipelineIO_HelpLists_OnlyShellVerbs(t *testing.T) {
	spec, sess := newTestRoot(&mockQueueBackend{}, &mockTopicBackend{})
	sess.aliases = map[string]string{"qstat": "manage stats $1"}
	root := NewRootCommand(spec)

	var out bytes.Buffer
	if err := sess.executePipelineIO(context.Background(), "help", root, strings.NewReader(""), &out, &out); err != nil {
		t.Fatalf("help: %v", err)
	}
	got := out.String()
	if !containsAll(got, "send (put)", "receive (get)", "ping", "manage", "qstat", "!cmd") {
		t.Errorf("help listing incomplete:\n%s", got)
	}
	for _, notRunnable := range []string{"  ai ", "  shell", "  version"} {
		if strings.Contains(got, notRunnable) {
			t.Errorf("help lists %q, which cannot run inside the shell:\n%s", notRunnable, got)
		}
	}
}

func TestExecutePipelineIO_HelpUnknownCommand(t *testing.T) {
	spec, sess := newTestRoot(&mockQueueBackend{}, &mockTopicBackend{})
	root := NewRootCommand(spec)
	var out bytes.Buffer
	err := sess.executePipelineIO(context.Background(), "help nope", root, strings.NewReader(""), &out, &out)
	if err == nil || !strings.Contains(err.Error(), "unknown command: nope") {
		t.Errorf("help nope: err = %v, want unknown command", err)
	}
}

// Non-verb lines run verbatim in the system shell, so shell syntax the
// pipeline parser does not model keeps working — in both the shell and the
// AI shell's command mode (which share executePipelineIO).
func TestExecutePipelineIO_PlainShellLine(t *testing.T) {
	_, sess := newTestRoot(&mockQueueBackend{}, &mockTopicBackend{})
	var out, errw bytes.Buffer
	if err := sess.executePipelineIO(context.Background(), "false || echo recovered", nil, strings.NewReader(""), &out, &errw); err != nil {
		t.Fatalf("shell line: %v (stderr %q)", err, errw.String())
	}
	if strings.TrimSpace(out.String()) != "recovered" {
		t.Errorf("out = %q, want %q", out.String(), "recovered")
	}
}

func TestExecutePipelineIO_BangEscape(t *testing.T) {
	_, sess := newTestRoot(&mockQueueBackend{}, &mockTopicBackend{})
	var out, errw bytes.Buffer
	// "send" would be an xmc verb; "!" forces the system shell.
	if err := sess.executePipelineIO(context.Background(), "!echo send q1", nil, strings.NewReader(""), &out, &errw); err != nil {
		t.Fatalf("! line: %v (stderr %q)", err, errw.String())
	}
	if strings.TrimSpace(out.String()) != "send q1" {
		t.Errorf("out = %q, want %q", out.String(), "send q1")
	}
}

func TestExecutePipelineIO_ExternalCancelledByContext(t *testing.T) {
	_, sess := newTestRoot(&mockQueueBackend{}, &mockTopicBackend{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	if err := sess.executePipelineIO(ctx, "sleep 5", nil, strings.NewReader(""), &out, &out); err == nil {
		t.Error("a cancelled context must stop the external command")
	}
}

func TestContainsVerb_AcrossSemicolons(t *testing.T) {
	if !containsVerb("echo start; send q x") {
		t.Error("a verb after ';' must be detected")
	}
	if !containsVerb("put q x") {
		t.Error("verb aliases must be detected")
	}
	if containsVerb("echo a; ls") {
		t.Error("no verb in any segment")
	}
}
