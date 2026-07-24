// Package mcp implements a small, dependency-free Model Context Protocol (MCP)
// server that exposes xmc's broker operations as tools an AI agent can call.
//
// Why a hand-rolled server instead of an SDK: MCP is JSON-RPC 2.0 carried over
// either a stdio stream or "Streamable HTTP". The surface xmc needs is tiny
// (initialize, tools/list, tools/call, ping), the protocol is stable, and
// staying on the standard library keeps xmc's single-static-binary, low-
// dependency character intact and avoids coupling to a fast-moving SDK.
//
// The server is transport-agnostic: the same tool registry is served over
// stdio (for agents that spawn the binary as a co-located subprocess) and over
// Streamable HTTP (for a long-lived in-cluster Deployment that remote agents
// reach over the network).
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// protocolVersion is the latest MCP revision this server implements. During
// initialize the server echoes back the client's requested version when it is
// one of supportedVersions (per the spec's negotiation rule: same version if
// supported, otherwise the server's latest).
const protocolVersion = "2025-06-18"

// supportedVersions are the MCP revisions this server can honestly claim: the
// tools-only surface it exposes is identical across all three.
var supportedVersions = map[string]bool{
	"2024-11-05": true,
	"2025-03-26": true,
	"2025-06-18": true,
}

// ---- JSON-RPC 2.0 envelope -------------------------------------------------

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // absent => notification
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// JSON-RPC 2.0 reserved error codes.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// ---- Tool model ------------------------------------------------------------

// Tool is one callable exposed via tools/list and invoked via tools/call.
type Tool struct {
	Name        string
	Description string
	// InputSchema is a JSON Schema object describing arguments. Constraining it
	// well (enums, required, descriptions) is what keeps an agent from inventing
	// values, so handlers can trust their inputs.
	InputSchema map[string]any
	// Annotations carry behavioural hints (readOnlyHint, destructiveHint,
	// idempotentHint, title) so the client can gate dangerous tools.
	Annotations map[string]any
	// Handler runs the tool. Returning a non-nil error is reported back to the
	// model as an isError tool result (recoverable), not as a transport fault.
	Handler func(ctx context.Context, args json.RawMessage) (*ToolResult, error)
}

// ToolResult is the payload of a successful tools/call.
type ToolResult struct {
	Content           []ContentBlock `json:"content"`
	IsError           bool           `json:"isError,omitempty"`
	StructuredContent any            `json:"structuredContent,omitempty"`
}

// ContentBlock is a single piece of tool output. Only text blocks are used.
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// ---- Server ----------------------------------------------------------------

// Server holds the tool registry and serves it over a chosen transport.
type Server struct {
	name    string
	version string
	tools   []*Tool
	byName  map[string]*Tool
}

// NewServer creates an empty server identified by name/version (surfaced to the
// client via serverInfo during initialize).
func NewServer(name, version string) *Server {
	return &Server{name: name, version: version, byName: map[string]*Tool{}}
}

// AddTool registers a tool. A later registration with the same name overrides
// the earlier one, which keeps wiring order forgiving.
func (s *Server) AddTool(t *Tool) {
	if _, exists := s.byName[t.Name]; !exists {
		s.tools = append(s.tools, t)
	}
	s.byName[t.Name] = t
}

// ---- Dispatch --------------------------------------------------------------

// handle processes one raw JSON-RPC message. It returns the encoded response
// bytes, or notification=true (no response is owed) for notifications and other
// id-less messages.
func (s *Server) handle(ctx context.Context, raw []byte) (response []byte, notification bool) {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return s.encodeError(nil, codeParseError, "parse error"), false
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		if req.ID == nil {
			return nil, true
		}
		return s.encodeError(req.ID, codeInvalidRequest, "invalid request"), false
	}

	// Notifications (no id) get no response. We accept and ignore the lifecycle
	// notifications a client may send.
	if req.ID == nil {
		return nil, true
	}

	result, rpcErr := s.route(ctx, req.Method, req.Params)
	if rpcErr != nil {
		return s.encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: rpcErr}), false
	}
	return s.encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}), false
}

func (s *Server) route(ctx context.Context, method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		return s.initialize(params), nil
	case "ping":
		// MCP utility ping: an empty result is a valid pong.
		return map[string]any{}, nil
	case "tools/list":
		return s.toolsList(), nil
	case "tools/call":
		return s.toolsCall(ctx, params)
	default:
		return nil, &rpcError{Code: codeMethodNotFound, Message: fmt.Sprintf("method not found: %s", method)}
	}
}

func (s *Server) initialize(params json.RawMessage) any {
	negotiated := protocolVersion
	if len(params) > 0 {
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := json.Unmarshal(params, &p); err == nil && supportedVersions[p.ProtocolVersion] {
			negotiated = p.ProtocolVersion
		}
	}
	return map[string]any{
		"protocolVersion": negotiated,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": s.name, "version": s.version},
	}
}

func (s *Server) toolsList() any {
	list := make([]map[string]any, 0, len(s.tools))
	for _, t := range s.tools {
		entry := map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": t.InputSchema,
		}
		if len(t.Annotations) > 0 {
			entry["annotations"] = t.Annotations
		}
		list = append(list, entry)
	}
	return map[string]any{"tools": list}
}

func (s *Server) toolsCall(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "invalid tools/call params"}
	}
	tool, ok := s.byName[call.Name]
	if !ok {
		return nil, &rpcError{Code: codeInvalidParams, Message: fmt.Sprintf("unknown tool: %s", call.Name)}
	}

	res, err := runTool(ctx, tool, call.Arguments)
	if err != nil {
		// Tool execution failures are reported to the model as an isError
		// result so it can read the reason and recover, not as a JSON-RPC fault.
		return &ToolResult{
			Content: []ContentBlock{{Type: "text", Text: err.Error()}},
			IsError: true,
		}, nil
	}
	return res, nil
}

// runTool invokes the handler and converts a panic into an error, so one
// misbehaving broker adapter can't take down the long-lived server (and the
// agent still gets a readable failure to react to).
func runTool(ctx context.Context, tool *Tool, args json.RawMessage) (res *ToolResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			res, err = nil, fmt.Errorf("tool %s panicked: %v", tool.Name, r)
		}
	}()
	return tool.Handler(ctx, args)
}

func (s *Server) encode(resp rpcResponse) []byte {
	b, err := json.Marshal(resp)
	if err != nil {
		return s.encodeError(resp.ID, codeInternalError, "failed to encode response")
	}
	return b
}

func (s *Server) encodeError(id json.RawMessage, code int, msg string) []byte {
	b, _ := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
	return b
}

// ---- stdio transport -------------------------------------------------------

// ServeStdio runs the server over newline-delimited JSON on stdin/stdout, the
// MCP stdio transport. Nothing but protocol messages may be written to stdout,
// so all diagnostics go to stderr. Returns when stdin reaches EOF or ctx ends.
func (s *Server) ServeStdio(ctx context.Context) error {
	reader := bufio.NewReaderSize(os.Stdin, 64*1024)
	writer := bufio.NewWriter(os.Stdout)
	defer writer.Flush()

	lines := make(chan []byte)
	scanErr := make(chan error, 1)
	go func() {
		defer close(lines)
		for {
			line, err := readLine(reader)
			if len(line) > 0 {
				lines <- line
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					scanErr <- err
				}
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-scanErr:
			return err
		case line, ok := <-lines:
			if !ok {
				return nil
			}
			resp, notification := s.handle(ctx, line)
			if notification || resp == nil {
				continue
			}
			writer.Write(resp)
			writer.WriteByte('\n')
			if err := writer.Flush(); err != nil {
				return err
			}
		}
	}
}

// readLine reads a single newline-terminated message. bufio.Reader.ReadBytes
// grows its result as needed, so large messages (e.g. base64 payloads) are
// returned whole; the trailing newline is stripped. A non-nil error (including
// io.EOF for a final unterminated line) is returned alongside any bytes read.
func readLine(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadBytes('\n')
	return trimNewline(line), err
}

func trimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

// ---- Streamable HTTP transport ---------------------------------------------

// Handler returns an http.Handler implementing a stateless Streamable HTTP MCP
// endpoint at path, plus a /healthz probe for Kubernetes. The server is
// stateless (no sessions, no server-initiated SSE stream): each POST carries
// one JSON-RPC message and receives one JSON response, which is sufficient for
// request/response tool use and keeps horizontal scaling trivial.
func (s *Server) Handler(path string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "ok")
	})
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			s.serveHTTPMessage(w, r)
		case http.MethodGet:
			// We do not open a server->client notification stream.
			http.Error(w, "streaming not supported", http.StatusMethodNotAllowed)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	return mux
}

func (s *Server) serveHTTPMessage(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 32*1024*1024))
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}
	resp, notification := s.handle(r.Context(), body)
	if notification || resp == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}

// ServeHTTP starts an HTTP server on addr serving the MCP endpoint at path and
// blocks until ctx is cancelled, then shuts down gracefully.
func (s *Server) ServeHTTP(ctx context.Context, addr, path string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(path),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MiB
	}
	errc := make(chan error, 1)
	go func() {
		fmt.Fprintf(os.Stderr, "xmc mcp: serving Streamable HTTP on %s%s (health: /healthz)\n", addr, path)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errc:
		return err
	}
}
