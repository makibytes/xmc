package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/makibytes/xmc/broker/backends"
)

// fakeQueue is an in-memory QueueBackend for exercising the tools without a
// real broker. Sends are recorded; Receive returns queued replies.
type fakeQueue struct {
	sent     []backends.SendOptions
	toReturn []*backends.Message
	closed   bool
}

func (f *fakeQueue) Send(_ context.Context, opts backends.SendOptions) error {
	f.sent = append(f.sent, opts)
	return nil
}

func (f *fakeQueue) Receive(_ context.Context, _ backends.ReceiveOptions) (*backends.Message, error) {
	if len(f.toReturn) == 0 {
		return nil, backends.ErrNoMessageAvailable
	}
	m := f.toReturn[0]
	f.toReturn = f.toReturn[1:]
	return m, nil
}

func (f *fakeQueue) Close() error { f.closed = true; return nil }

func testServer(fq *fakeQueue) *Server {
	return NewServerFromDeps(Deps{
		ServerName:    "xmc-test",
		ServerVersion: "test",
		Target:        "amqp://localhost:5672",
		NewQueue:      func() (backends.QueueBackend, error) { return fq, nil },
	})
}

// call drives one JSON-RPC request through the server and returns the decoded
// response.
func call(t *testing.T, s *Server, method string, params any) rpcResponse {
	t.Helper()
	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal params: %v", err)
		}
		rawParams = b
	}
	req, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: method, Params: rawParams})
	respBytes, notification := s.handle(context.Background(), req)
	if notification {
		t.Fatalf("unexpected notification for method %s", method)
	}
	var resp rpcResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func TestInitializeNegotiatesClientVersion(t *testing.T) {
	s := testServer(&fakeQueue{})
	resp := call(t, s, "initialize", map[string]any{"protocolVersion": "2025-03-26"})
	if resp.Error != nil {
		t.Fatalf("initialize error: %+v", resp.Error)
	}
	result := resp.Result.(map[string]any) // handle returns encoded bytes; re-decode below
	_ = result
	// Re-decode strongly since Result came back through json round-trip:
	b, _ := json.Marshal(resp.Result)
	var got struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	json.Unmarshal(b, &got)
	if got.ProtocolVersion != "2025-03-26" {
		t.Errorf("expected echoed protocol version 2025-03-26, got %q", got.ProtocolVersion)
	}
	if got.ServerInfo.Name != "xmc-test" {
		t.Errorf("expected serverInfo.name xmc-test, got %q", got.ServerInfo.Name)
	}
}

func TestToolsListIncludesCoreToolsWithAnnotations(t *testing.T) {
	s := testServer(&fakeQueue{})
	resp := call(t, s, "tools/list", nil)
	b, _ := json.Marshal(resp.Result)
	var got struct {
		Tools []struct {
			Name        string         `json:"name"`
			InputSchema map[string]any `json:"inputSchema"`
			Annotations map[string]any `json:"annotations"`
		} `json:"tools"`
	}
	json.Unmarshal(b, &got)

	byName := map[string]struct {
		schema      map[string]any
		annotations map[string]any
	}{}
	for _, tool := range got.Tools {
		byName[tool.Name] = struct {
			schema      map[string]any
			annotations map[string]any
		}{tool.InputSchema, tool.Annotations}
	}

	for _, want := range []string{"send", "request", "peek", "receive", "ping"} {
		if _, ok := byName[want]; !ok {
			t.Errorf("expected tool %q in tools/list", want)
		}
	}
	// receive must be flagged destructive; peek must be read-only.
	if d, _ := byName["receive"].annotations["destructiveHint"].(bool); !d {
		t.Error("receive should carry destructiveHint=true")
	}
	if ro, _ := byName["peek"].annotations["readOnlyHint"].(bool); !ro {
		t.Error("peek should carry readOnlyHint=true")
	}
	// Management tools must be absent when no hooks are provided.
	if _, ok := byName["manage_purge_queue"]; ok {
		t.Error("manage_purge_queue should not be registered without a PurgeQueue hook")
	}
}

func TestSendToolRecordsMessage(t *testing.T) {
	fq := &fakeQueue{}
	s := testServer(fq)
	resp := call(t, s, "tools/call", map[string]any{
		"name":      "send",
		"arguments": map[string]any{"address": "A.foo", "body": "ping"},
	})
	if resp.Error != nil {
		t.Fatalf("tools/call error: %+v", resp.Error)
	}
	b, _ := json.Marshal(resp.Result)
	var res ToolResult
	json.Unmarshal(b, &res)
	if res.IsError {
		t.Fatalf("unexpected isError result: %s", res.Content[0].Text)
	}
	if len(fq.sent) != 1 || fq.sent[0].Queue != "A.foo" || string(fq.sent[0].Message) != "ping" {
		t.Fatalf("expected one send to A.foo with body ping, got %+v", fq.sent)
	}
	if !fq.closed {
		t.Error("expected queue connection to be closed after the call")
	}
}

func TestRequestReturnsReply(t *testing.T) {
	fq := &fakeQueue{toReturn: []*backends.Message{{Data: []byte("pong"), MessageID: "ID:1"}}}
	s := testServer(fq)
	resp := call(t, s, "tools/call", map[string]any{
		"name":      "request",
		"arguments": map[string]any{"address": "A.foo", "body": "ping", "timeout_seconds": 1},
	})
	b, _ := json.Marshal(resp.Result)
	var res ToolResult
	json.Unmarshal(b, &res)
	if res.IsError {
		t.Fatalf("unexpected isError: %s", res.Content[0].Text)
	}
	if !strings.Contains(res.Content[0].Text, "pong") {
		t.Errorf("expected reply body pong in result, got: %s", res.Content[0].Text)
	}
}

func TestRequestTimesOutIsError(t *testing.T) {
	fq := &fakeQueue{} // no reply queued
	s := testServer(fq)
	resp := call(t, s, "tools/call", map[string]any{
		"name":      "request",
		"arguments": map[string]any{"address": "A.foo", "body": "ping", "timeout_seconds": 1},
	})
	b, _ := json.Marshal(resp.Result)
	var res ToolResult
	json.Unmarshal(b, &res)
	if !res.IsError {
		t.Fatal("expected isError result when no reply arrives")
	}
	if !strings.Contains(res.Content[0].Text, "no reply") {
		t.Errorf("expected a 'no reply' message, got: %s", res.Content[0].Text)
	}
}

func TestUnknownToolIsRpcError(t *testing.T) {
	s := testServer(&fakeQueue{})
	resp := call(t, s, "tools/call", map[string]any{"name": "does_not_exist", "arguments": map[string]any{}})
	if resp.Error == nil {
		t.Fatal("expected a JSON-RPC error for unknown tool")
	}
	if resp.Error.Code != codeInvalidParams {
		t.Errorf("expected code %d, got %d", codeInvalidParams, resp.Error.Code)
	}
}

func TestNotificationGetsNoResponse(t *testing.T) {
	s := testServer(&fakeQueue{})
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	resp, notification := s.handle(context.Background(), req)
	if !notification {
		t.Errorf("expected notification handling, got response: %s", resp)
	}
}

func TestManagementToolsRegisteredWhenHooksProvided(t *testing.T) {
	s := NewServerFromDeps(Deps{
		NewQueue:   func() (backends.QueueBackend, error) { return &fakeQueue{}, nil },
		ListQueues: func(context.Context) ([]QueueInfo, error) { return []QueueInfo{{Name: "q1", MessageCount: 3}}, nil },
		PurgeQueue: func(context.Context, string) (int64, error) { return 5, nil },
		QueueStats: func(context.Context, string) (*QueueStats, error) { return &QueueStats{Name: "q1"}, nil },
	})
	resp := call(t, s, "tools/list", nil)
	b, _ := json.Marshal(resp.Result)
	var got struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	json.Unmarshal(b, &got)
	names := map[string]bool{}
	for _, tl := range got.Tools {
		names[tl.Name] = true
	}
	for _, want := range []string{"manage_list_queues", "manage_queue_stats", "manage_purge_queue"} {
		if !names[want] {
			t.Errorf("expected management tool %q to be registered", want)
		}
	}

	// purge without confirm must be refused
	resp = call(t, s, "tools/call", map[string]any{
		"name":      "manage_purge_queue",
		"arguments": map[string]any{"queue": "q1"},
	})
	b, _ = json.Marshal(resp.Result)
	var res ToolResult
	json.Unmarshal(b, &res)
	if !res.IsError {
		t.Error("expected purge without confirm=true to be refused")
	}
}
