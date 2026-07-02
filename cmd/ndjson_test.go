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
	rec := newMessageRecord(m, true)
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
	rec := newMessageRecord(&backends.Message{Data: bin}, true)
	if rec.DataBase64 == "" || rec.Data != "" {
		t.Fatalf("binary should use DataBase64, got %+v", rec)
	}
	got, err := rec.payload()
	if err != nil || !bytes.Equal(got, bin) {
		t.Fatalf("binary round-trip failed: got %v, err %v", got, err)
	}
}

// TestMessageRecord_IncludePayloadFalse_OmitsData guards the /simplify fix:
// newMessageRecord must skip the UTF-8 scan and base64 encode entirely when
// includePayload is false (used by the AI shell's metadata view, which never
// wants the payload), rather than doing the conversion and discarding it.
func TestMessageRecord_IncludePayloadFalse_OmitsData(t *testing.T) {
	rec := newMessageRecord(&backends.Message{Data: []byte("hello"), MessageID: "id1"}, false)
	if rec.Data != "" || rec.DataBase64 != "" {
		t.Fatalf("includePayload=false should omit both payload fields, got %+v", rec)
	}
	if rec.MessageID != "id1" {
		t.Fatalf("metadata fields should still be populated, got %+v", rec)
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
		InternalMetadata: map[string]any{
			"Header": "native-debug",
		},
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
	var generic map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &generic); err != nil {
		t.Fatalf("output is not valid JSON object: %v (%q)", err, out)
	}
	if _, ok := generic["internalMetadata"]; ok {
		t.Fatalf("NDJSON record must not include internalMetadata: %v", generic)
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

// TestSendCommand_NDJSONImport_PreservesKey guards against the regression
// where SendOptions had no Key field: a Pulsar/Kafka queue receive populates
// Message.Key, so a `receive --ndjson | send --ndjson` round trip must not
// silently drop it (the NDJSON record is documented as lossless — see
// docs/BRIDGE_AND_FORWARD.md's "Partition key | Yes" row).
func TestSendCommand_NDJSONImport_PreservesKey(t *testing.T) {
	input := `{"data":"evt","key":"partition-1"}` + "\n"

	withStdin(t, input, func() {
		mock := &mockQueueBackend{}
		cmd := NewSendCommand(mock, nil, nil)
		cmd.SetArgs([]string{"q", "--ndjson"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mock.sendCount != 1 {
			t.Fatalf("sendCount = %d, want 1", mock.sendCount)
		}
		if mock.lastSendOpts.Key != "partition-1" {
			t.Errorf("key = %q, want %q (dropped on send --ndjson)", mock.lastSendOpts.Key, "partition-1")
		}
	})
}

// TestSendCommand_KeyFlag_FallsBackWhenRecordKeyEmpty verifies --key acts as
// a per-batch default for --ndjson records that don't carry their own key,
// mirroring publish's existing -K fallback behavior.
func TestSendCommand_KeyFlag_FallsBackWhenRecordKeyEmpty(t *testing.T) {
	input := `{"data":"evt"}` + "\n"

	withStdin(t, input, func() {
		mock := &mockQueueBackend{}
		cmd := NewSendCommand(mock, nil, nil)
		cmd.SetArgs([]string{"q", "--ndjson", "-K", "default-key"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mock.lastSendOpts.Key != "default-key" {
			t.Errorf("key = %q, want %q (fallback to --key)", mock.lastSendOpts.Key, "default-key")
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

func TestReceiveCommand_NDJSON_PrunesEmptyMetadataValues(t *testing.T) {
	msg := &backends.Message{
		Data: []byte("hi"),
		Properties: map[string]any{
			"foo":    "bar",
			"blank":  "",
			"nilish": "<nil>",
			"empty":  []any{},
		},
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
	if rec.Properties["foo"] != "bar" {
		t.Fatalf("expected foo=bar in properties, got %+v", rec.Properties)
	}
	for _, banned := range []string{"blank", "nilish", "empty"} {
		if _, ok := rec.Properties[banned]; ok {
			t.Fatalf("property %q must be pruned from NDJSON output: %+v", banned, rec.Properties)
		}
	}
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
