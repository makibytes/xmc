package mcp

import (
	"encoding/base64"
	"encoding/json"
	"unicode/utf8"

	"github.com/makibytes/xmc/broker/backends"
)

// messageJSON is the structured representation of a broker message returned by
// the peek/receive/request tools. Its field names mirror xmc's NDJSON record
// (`receive --ndjson`) so an agent sees one consistent message shape across the
// CLI and the MCP server. UTF-8 payloads are returned as plain text in Data;
// binary payloads are base64-encoded into DataBase64 so the JSON stays valid
// and the bytes round-trip exactly.
type messageJSON struct {
	Data          string         `json:"data,omitempty"`
	DataBase64    string         `json:"dataBase64,omitempty"`
	MessageID     string         `json:"messageId,omitempty"`
	CorrelationID string         `json:"correlationId,omitempty"`
	ReplyTo       string         `json:"replyTo,omitempty"`
	ContentType   string         `json:"contentType,omitempty"`
	Priority      int            `json:"priority,omitempty"`
	Persistent    bool           `json:"persistent,omitempty"`
	Properties    map[string]any `json:"properties,omitempty"`
}

func toMessageJSON(m *backends.Message) messageJSON {
	rec := messageJSON{
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

// jsonResult wraps a structured value as a tool result. The value is emitted
// both as pretty-printed text (which the model reads directly) and as
// structuredContent (for clients that consume typed output).
func jsonResult(v any) (*ToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return &ToolResult{
		Content:           []ContentBlock{{Type: "text", Text: string(b)}},
		StructuredContent: v,
	}, nil
}

// ---- JSON-Schema builders --------------------------------------------------
// Small helpers to keep tool input schemas readable. additionalProperties is
// set false on objects so an agent gets a clear validation signal instead of
// silently-ignored typos.

func object(props map[string]any, required ...string) map[string]any {
	m := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func stringProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func intProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func numberProp(desc string) map[string]any {
	return map[string]any{"type": "number", "description": desc}
}

func boolProp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

func mapProp(desc string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"description":          desc,
		"additionalProperties": true,
	}
}
