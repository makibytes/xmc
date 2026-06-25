package cmd

import (
	"bytes"
	"os"
	"testing"

	"github.com/makibytes/xmc/broker/backends"
)

func TestFormatMessage_Tokens(t *testing.T) {
	msg := &backends.Message{
		Data:             []byte("hello"),
		MessageID:        "id-1",
		CorrelationID:    "corr-1",
		ReplyTo:          "reply-q",
		ContentType:      "text/plain",
		Priority:         7,
		Persistent:       true,
		Properties:       map[string]any{"env": "prod", "n": 42},
		InternalMetadata: map[string]any{"offset": 99},
	}

	tests := []struct {
		name   string
		format string
		want   string
	}{
		{"payload", "%s", "hello"},
		{"length", "%S", "5"},
		{"messageid", "%i", "id-1"},
		{"correlationid", "%c", "corr-1"},
		{"replyto", "%r", "reply-q"},
		{"contenttype", "%y", "text/plain"},
		{"priority", "%P", "7"},
		{"persistent", "%u", "true"},
		{"property", "%p{env}", "prod"},
		{"property-int", "%p{n}", "42"},
		{"property-missing", "%p{nope}", ""},
		{"metadata", "%m{offset}", "99"},
		{"all-props", "%h", "env=prod,n=42"},
		{"percent", "100%%", "100%"},
		{"escapes", "a\\nb\\tc", "a\nb\tc"},
		{"combined", "%i: %s\\n", "id-1: hello\n"},
		{"unknown-token", "%z", "%z"},
		{"unknown-prop-nobrace", "%p", "%p"},
		{"trailing-percent", "abc%", "abc%"},
		{"trailing-backslash", "abc\\", "abc\\"},
		{"literal", "no tokens here", "no tokens here"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatMessage(msg, tt.format)
			if got != tt.want {
				t.Errorf("formatMessage(%q) = %q, want %q", tt.format, got, tt.want)
			}
		})
	}
}

func TestReceiveCommand_FormatOutput(t *testing.T) {
	msg := &backends.Message{
		Data:       []byte("payload"),
		MessageID:  "m-9",
		Properties: map[string]any{"k": "v"},
	}
	mock := &mockQueueBackend{receiveMsg: msg}
	cmd := NewReceiveCommand(mock, nil, nil)
	cmd.SetArgs([]string{"q", "-F", "%i|%p{k}|%s"})

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := cmd.Execute()
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	if buf.String() != "m-9|v|payload" {
		t.Errorf("output = %q, want %q", buf.String(), "m-9|v|payload")
	}
}

func TestReceiveCommand_FormatOverridesJSON(t *testing.T) {
	msg := &backends.Message{Data: []byte("data")}
	mock := &mockQueueBackend{receiveMsg: msg}
	cmd := NewReceiveCommand(mock, nil, nil)
	cmd.SetArgs([]string{"q", "-J", "-F", "%s"})

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := cmd.Execute()
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	// Format wins: output is the raw payload, not JSON.
	if buf.String() != "data" {
		t.Errorf("output = %q, want %q (format should override --json)", buf.String(), "data")
	}
}
