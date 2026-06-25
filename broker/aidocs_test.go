package broker

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func TestAIDoc_ReturnsContent(t *testing.T) {
	fake := fstest.MapFS{
		"docs/rabbitmq.md": &fstest.MapFile{Data: []byte("# RabbitMQ\nExchanges and bindings.\n")},
		"docs/artemis.md":  &fstest.MapFile{Data: []byte("# Artemis\nANYCAST and MULTICAST.\n")},
	}
	RegisterAIDocs(fs.FS(fake))
	defer RegisterAIDocs(nil)

	got := AIDoc("rabbitmq")
	if !strings.Contains(got, "Exchanges") {
		t.Errorf("AIDoc(rabbitmq) = %q, want content containing 'Exchanges'", got)
	}

	got = AIDoc("artemis")
	if !strings.Contains(got, "ANYCAST") {
		t.Errorf("AIDoc(artemis) = %q, want content containing 'ANYCAST'", got)
	}
}

func TestAIDoc_MissingFile(t *testing.T) {
	fake := fstest.MapFS{
		"docs/rabbitmq.md": &fstest.MapFile{Data: []byte("content")},
	}
	RegisterAIDocs(fs.FS(fake))
	defer RegisterAIDocs(nil)

	got := AIDoc("nonexistent")
	if got != "" {
		t.Errorf("AIDoc(nonexistent) = %q, want empty string", got)
	}
}

func TestAIDoc_NilFS(t *testing.T) {
	RegisterAIDocs(nil)

	got := AIDoc("rabbitmq")
	if got != "" {
		t.Errorf("AIDoc with nil FS = %q, want empty string", got)
	}
}
