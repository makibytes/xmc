package cmd

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/makibytes/xmc/broker/backends"
)

// messageRecord is the lossless, round-trippable representation of a message
// used by the NDJSON (newline-delimited JSON) export/import feature. One record
// is emitted per line by `receive`/`peek`/`subscribe --ndjson` and consumed per
// line by `send`/`publish --ndjson`, enabling queue backup, restore, migration
// between brokers, and offline processing.
//
// Payloads that are valid UTF-8 are stored as a plain string in Data; binary
// payloads are base64-encoded into DataBase64 so the JSON stays well-formed and
// the bytes survive a round-trip exactly.
type messageRecord struct {
	Data          string         `json:"data,omitempty"`
	DataBase64    string         `json:"dataBase64,omitempty"`
	Key           string         `json:"key,omitempty"`
	MessageID     string         `json:"messageId,omitempty"`
	CorrelationID string         `json:"correlationId,omitempty"`
	ReplyTo       string         `json:"replyTo,omitempty"`
	ContentType   string         `json:"contentType,omitempty"`
	Priority      int            `json:"priority,omitempty"`
	Persistent    bool           `json:"persistent,omitempty"`
	Properties    map[string]any `json:"properties,omitempty"`
}

// newMessageRecord captures a received message as a lossless record.
func newMessageRecord(m *backends.Message) messageRecord {
	rec := messageRecord{
		Key:           m.Key,
		MessageID:     m.MessageID,
		CorrelationID: m.CorrelationID,
		ReplyTo:       m.ReplyTo,
		ContentType:   m.ContentType,
		Priority:      m.Priority,
		Persistent:    m.Persistent,
		Properties:    m.Properties,
	}
	if utf8.Valid(m.Data) {
		rec.Data = string(m.Data)
	} else {
		rec.DataBase64 = base64.StdEncoding.EncodeToString(m.Data)
	}
	return rec
}

// payload reconstructs the raw message bytes from a record, decoding base64 when
// the binary form was used.
func (r messageRecord) payload() ([]byte, error) {
	if r.DataBase64 != "" {
		data, err := base64.StdEncoding.DecodeString(r.DataBase64)
		if err != nil {
			return nil, fmt.Errorf("decode base64 payload: %w", err)
		}
		return data, nil
	}
	return []byte(r.Data), nil
}

// displayMessageNDJSON writes a single message as one NDJSON record line. A
// trailing newline is always written so records remain line-delimited
// regardless of whether stdout is a terminal.
func displayMessageNDJSON(message *backends.Message) error {
	data, err := json.Marshal(newMessageRecord(message))
	if err != nil {
		return fmt.Errorf("failed to marshal message record: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

// forEachRecord scans NDJSON records from r, invoking visit for each. Blank
// lines are skipped so files can be concatenated freely. The scan buffer is
// enlarged to tolerate large (e.g. base64) payloads on a single line.
func forEachRecord(r io.Reader, visit func(messageRecord) error) (int, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	processed := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec messageRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return processed, fmt.Errorf("parse record on line %d: %w", processed+1, err)
		}
		if err := visit(rec); err != nil {
			return processed, err
		}
		processed++
	}
	if err := scanner.Err(); err != nil {
		return processed, fmt.Errorf("error reading input: %w", err)
	}
	return processed, nil
}
