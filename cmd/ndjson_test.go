package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/makibytes/xmc/broker/backends"
)

func TestMessageRecord_RoundTripUTF8(t *testing.T) {
	m := &backends.Message{
		Data:          []byte("hëllo"),
		MessageID:     "id1",
		CorrelationID: "c1",
		Properties:    map[string]any{"k": "v"},
	}
	rec := newMessageRecord(m)
	if rec.Data != "hëllo" || rec.DataBase64 != "" {
		t.Fatalf("valid UTF-8 should use Data field, got %+v", rec)
	}
	got, err := rec.payload()
	if err != nil || string(got) != "hëllo" {
		t.Fatalf("payload = %q, err = %v", got, err)
	}
}

func TestMessageRecord_RoundTripBinary(t *testing.T) {
	bin := []byte{0xff, 0xfe, 0x00, 0x01}
	rec := newMessageRecord(&backends.Message{Data: bin})
	if rec.DataBase64 == "" || rec.Data != "" {
		t.Fatalf("binary should use DataBase64, got %+v", rec)
	}
	got, err := rec.payload()
	if err != nil || !bytes.Equal(got, bin) {
		t.Fatalf("binary round-trip failed: got %v, err %v", got, err)
	}
}

func TestForEachRecord(t *testing.T) {
	in := `{"data":"a"}` + "\n" +
		"   \n" + // blank line should be skipped
		`{"dataBase64":"AAE="}` + "\n"

	var got [][]byte
	n, err := forEachRecord(strings.NewReader(in), func(r messageRecord) error {
		p, perr := r.payload()
		if perr != nil {
			return perr
		}
		got = append(got, p)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		t.Fatalf("processed = %d, want 2 (blank line skipped)", n)
	}
	if string(got[0]) != "a" || !bytes.Equal(got[1], []byte{0x00, 0x01}) {
		t.Errorf("decoded records wrong: %q, %v", got[0], got[1])
	}
}

func TestForEachRecord_InvalidJSON(t *testing.T) {
	_, err := forEachRecord(strings.NewReader("{not json}\n"), func(messageRecord) error { return nil })
	if err == nil {
		t.Fatal("expected error for malformed record")
	}
}

func TestReceiveCommand_NDJSONOutput(t *testing.T) {
	msg := &backends.Message{
		Data:          []byte("hi"),
		MessageID:     "m1",
		CorrelationID: "c1",
		Properties:    map[string]any{"k": "v"},
	}
	mock := &mockQueueBackend{receiveMsg: msg}
	cmd := NewReceiveCommand(mock, nil, nil)
	cmd.SetArgs([]string{"q", "--ndjson"})

	out := captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	var rec messageRecord
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &rec); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, out)
	}
	if rec.Data != "hi" || rec.MessageID != "m1" || rec.CorrelationID != "c1" || rec.Properties["k"] != "v" {
		t.Errorf("record mismatch: %+v", rec)
	}
}

func TestReceiveCommand_NDJSONOverridesFormat(t *testing.T) {
	mock := &mockQueueBackend{receiveMsg: &backends.Message{Data: []byte("x"), MessageID: "m1"}}
	cmd := NewReceiveCommand(mock, nil, nil)
	cmd.SetArgs([]string{"q", "--ndjson", "-F", "SHOULD-NOT-APPEAR"})

	out := captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if strings.Contains(out, "SHOULD-NOT-APPEAR") {
		t.Error("--ndjson should override --format")
	}
	var rec messageRecord
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &rec); err != nil {
		t.Fatalf("expected JSON record, got %q", out)
	}
}

func TestReceiveCommand_DrainAll(t *testing.T) {
	msgs := []*backends.Message{
		{Data: []byte("a")},
		{Data: []byte("b")},
		{Data: []byte("c")},
	}
	mock := &mockQueueBackend{receiveMsgs: msgs}
	cmd := NewReceiveCommand(mock, nil, nil)
	cmd.SetArgs([]string{"q", "-n", "0", "--ndjson"})

	out := captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Errorf("drained %d records, want 3", len(lines))
	}
	if mock.receiveCount != 4 {
		t.Errorf("receiveCount = %d, want 4 (3 messages + 1 empty poll)", mock.receiveCount)
	}
}

func TestSendCommand_NDJSONImport(t *testing.T) {
	input := `{"data":"hello","contentType":"text/x"}` + "\n" +
		`{"dataBase64":"AAE=","messageId":"m2","correlationId":"c2","priority":5}` + "\n" +
		"   \n" // blank line skipped

	withStdin(t, input, func() {
		mock := &mockQueueBackend{}
		cmd := NewSendCommand(mock, nil, nil)
		cmd.SetArgs([]string{"q", "--ndjson"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mock.sendCount != 2 {
			t.Fatalf("sendCount = %d, want 2", mock.sendCount)
		}
		// Last record was the binary one; verify metadata + payload restored.
		if !bytes.Equal(mock.lastSendOpts.Message, []byte{0x00, 0x01}) {
			t.Errorf("binary payload not restored: %v", mock.lastSendOpts.Message)
		}
		if mock.lastSendOpts.MessageID != "m2" || mock.lastSendOpts.CorrelationID != "c2" || mock.lastSendOpts.Priority != 5 {
			t.Errorf("metadata not restored: %+v", mock.lastSendOpts)
		}
	})
}

func TestPublishCommand_NDJSONImport(t *testing.T) {
	input := `{"data":"evt","key":"partition-1","priority":3}` + "\n"

	withStdin(t, input, func() {
		mock := &mockTopicBackend{}
		cmd := NewPublishCommand(mock, nil, nil)
		cmd.SetArgs([]string{"topic", "--ndjson"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mock.publishCount != 1 {
			t.Fatalf("publishCount = %d, want 1", mock.publishCount)
		}
		if string(mock.lastPublishOpts.Message) != "evt" || mock.lastPublishOpts.Key != "partition-1" || mock.lastPublishOpts.Priority != 3 {
			t.Errorf("record not restored: %+v", mock.lastPublishOpts)
		}
	})
}

// captureStdout redirects os.Stdout for the duration of fn and returns what was
// written. Output here is small, so reading after fn returns is safe.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	return buf.String()
}

// withStdin feeds input as os.Stdin for the duration of fn.
func withStdin(t *testing.T, input string, fn func()) {
	t.Helper()
	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdin = r
	if _, err := io.WriteString(w, input); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	w.Close()
	fn()
	os.Stdin = old
}
