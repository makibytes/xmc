package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAnthropicClient_RequestShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %q, want /v1/messages", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("x-api-key = %q", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("anthropic-version = %q", r.Header.Get("anthropic-version"))
		}

		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)

		if req["model"] != "claude-sonnet-4-6" {
			t.Errorf("model = %v", req["model"])
		}

		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{
				{"text": "receive q -n 5"},
			},
		})
	}))
	defer srv.Close()

	c := &anthropicClient{apiKey: "test-key", model: "claude-sonnet-4-6", baseURL: srv.URL}
	result, _, err := c.Complete(context.Background(), "system prompt", []aiMessage{{Role: "user", Content: "user input"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result != "receive q -n 5" {
		t.Errorf("result = %q", result)
	}
}

func TestOpenAIClient_RequestShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer oai-key" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}

		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "send q hello"}},
			},
		})
	}))
	defer srv.Close()

	c := &openaiClient{apiKey: "oai-key", model: "gpt-4o", baseURL: srv.URL}
	result, _, err := c.Complete(context.Background(), "sys", []aiMessage{{Role: "user", Content: "user"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result != "send q hello" {
		t.Errorf("result = %q", result)
	}
}

func TestGeminiClient_RequestShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") != "gem-key" {
			t.Errorf("key = %q", r.URL.Query().Get("key"))
		}

		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{"content": map[string]any{
					"parts": []map[string]string{
						{"text": "publish topic msg"},
					},
				}},
			},
		})
	}))
	defer srv.Close()

	c := &geminiClient{apiKey: "gem-key", model: "gemini-2.0-flash", baseURL: srv.URL}
	result, _, err := c.Complete(context.Background(), "sys", []aiMessage{{Role: "user", Content: "user"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result != "publish topic msg" {
		t.Errorf("result = %q", result)
	}
}

func TestAIClient_CancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Never respond — let the context cancel
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	c := &anthropicClient{apiKey: "k", model: "m", baseURL: srv.URL}
	_, _, err := c.Complete(ctx, "s", []aiMessage{{Role: "user", Content: "u"}}, nil)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestAIClient_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": "invalid api key"}`))
	}))
	defer srv.Close()

	c := &anthropicClient{apiKey: "bad", model: "m", baseURL: srv.URL}
	_, _, err := c.Complete(context.Background(), "s", []aiMessage{{Role: "user", Content: "u"}}, nil)
	if err == nil {
		t.Fatal("expected error for 401")
	}
}

func TestNewAIClient_Dispatch(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"anthropic", "*cmd.anthropicClient"},
		{"openai", "*cmd.openaiClient"},
		{"gemini", "*cmd.geminiClient"},
		{"xai", "*cmd.openaiClient"},
		{"deepseek", "*cmd.openaiClient"},
		{"mistral", "*cmd.openaiClient"},
	}
	for _, tt := range tests {
		c := newAIClient(providerSpec{name: tt.name})
		got := typeString(c)
		if got != tt.want {
			t.Errorf("newAIClient(%q) type = %s, want %s", tt.name, got, tt.want)
		}
	}
}

func TestAnthropicClient_MultiTurnMessages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)

		msgs, ok := req["messages"].([]any)
		if !ok || len(msgs) != 3 {
			t.Errorf("expected 3 messages, got %v", req["messages"])
		}

		// Verify temperature is set to 0.
		temp, ok := req["temperature"]
		if !ok {
			t.Error("temperature should be set")
		} else if temp != float64(0) {
			t.Errorf("temperature = %v, want 0", temp)
		}

		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{{"text": "send q refined"}},
		})
	}))
	defer srv.Close()

	c := &anthropicClient{apiKey: "k", model: "m", baseURL: srv.URL}
	msgs := []aiMessage{
		{Role: "user", Content: "send hello to queue A"},
		{Role: "assistant", Content: "send A hello"},
		{Role: "user", Content: "make it queue B instead"},
	}
	result, _, err := c.Complete(context.Background(), "sys", msgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result != "send q refined" {
		t.Errorf("result = %q", result)
	}
}

func TestOpenAIClient_MultiTurnMessages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)

		msgs := req["messages"].([]any)
		// OpenAI: 1 system + 2 user + 1 assistant = 4
		if len(msgs) != 4 {
			t.Errorf("expected 4 messages (system + 3 turns), got %d", len(msgs))
		}
		first := msgs[0].(map[string]any)
		if first["role"] != "system" {
			t.Errorf("first message role = %v, want system", first["role"])
		}

		temp := req["temperature"]
		if temp != float64(0) {
			t.Errorf("temperature = %v, want 0", temp)
		}

		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "ok"}},
			},
		})
	}))
	defer srv.Close()

	c := &openaiClient{apiKey: "k", model: "m", baseURL: srv.URL}
	msgs := []aiMessage{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "cmd1"},
		{Role: "user", Content: "second"},
	}
	_, _, err := c.Complete(context.Background(), "sys", msgs, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestGeminiClient_RoleMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)

		contents := req["contents"].([]any)
		if len(contents) != 2 {
			t.Errorf("expected 2 contents, got %d", len(contents))
		}
		// Second message should have role "model" (not "assistant").
		second := contents[1].(map[string]any)
		if second["role"] != "model" {
			t.Errorf("assistant role mapped to %v, want model", second["role"])
		}

		// Verify generationConfig.temperature = 0.
		genConfig, ok := req["generationConfig"].(map[string]any)
		if !ok {
			t.Error("generationConfig should be set")
		} else if genConfig["temperature"] != float64(0) {
			t.Errorf("temperature = %v, want 0", genConfig["temperature"])
		}

		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{"content": map[string]any{
					"parts": []map[string]string{{"text": "ok"}},
				}},
			},
		})
	}))
	defer srv.Close()

	c := &geminiClient{apiKey: "k", model: "m", baseURL: srv.URL}
	msgs := []aiMessage{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "cmd1"},
	}
	_, _, err := c.Complete(context.Background(), "sys", msgs, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestAnthropicClient_Streaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)

		if req["stream"] != true {
			t.Error("stream should be true when onToken is provided")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)

		fmt.Fprintf(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":25}}}\n\n")
		flusher.Flush()

		fmt.Fprintf(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"receive\"}}\n\n")
		flusher.Flush()

		fmt.Fprintf(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\" q -n 5\"}}\n\n")
		flusher.Flush()

		fmt.Fprintf(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":8}}\n\n")
		flusher.Flush()

		fmt.Fprintf(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	c := &anthropicClient{apiKey: "k", model: "m", baseURL: srv.URL}
	var tokens []string
	result, usage, err := c.Complete(context.Background(), "sys", []aiMessage{{Role: "user", Content: "test"}}, func(token string) {
		tokens = append(tokens, token)
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != "receive q -n 5" {
		t.Errorf("result = %q, want 'receive q -n 5'", result)
	}
	if len(tokens) != 2 || tokens[0] != "receive" || tokens[1] != " q -n 5" {
		t.Errorf("tokens = %v", tokens)
	}
	if usage.InputTokens != 25 {
		t.Errorf("input tokens = %d, want 25", usage.InputTokens)
	}
	if usage.OutputTokens != 8 {
		t.Errorf("output tokens = %d, want 8", usage.OutputTokens)
	}
}

func TestOpenAIClient_Streaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)

		if req["stream"] != true {
			t.Error("stream should be true")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)

		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"send\"}}]}\n\n")
		flusher.Flush()

		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\" q hello\"}}]}\n\n")
		flusher.Flush()

		fmt.Fprintf(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":15,\"completion_tokens\":6}}\n\n")
		flusher.Flush()

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	c := &openaiClient{apiKey: "k", model: "m", baseURL: srv.URL}
	var tokens []string
	result, usage, err := c.Complete(context.Background(), "sys", []aiMessage{{Role: "user", Content: "test"}}, func(token string) {
		tokens = append(tokens, token)
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != "send q hello" {
		t.Errorf("result = %q", result)
	}
	if len(tokens) != 2 {
		t.Errorf("got %d tokens, want 2", len(tokens))
	}
	if usage.InputTokens != 15 || usage.OutputTokens != 6 {
		t.Errorf("usage = %+v", usage)
	}
}

func TestGeminiClient_Streaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "streamGenerateContent") {
			t.Errorf("path = %q, want streamGenerateContent", r.URL.Path)
		}
		if r.URL.Query().Get("alt") != "sse" {
			t.Error("alt should be sse")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)

		fmt.Fprintf(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"publish\"}]}}]}\n\n")
		flusher.Flush()

		fmt.Fprintf(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\" topic msg\"}]}}],\"usageMetadata\":{\"promptTokenCount\":20,\"candidatesTokenCount\":5}}\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	c := &geminiClient{apiKey: "k", model: "m", baseURL: srv.URL}
	var tokens []string
	result, usage, err := c.Complete(context.Background(), "sys", []aiMessage{{Role: "user", Content: "test"}}, func(token string) {
		tokens = append(tokens, token)
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != "publish topic msg" {
		t.Errorf("result = %q", result)
	}
	if len(tokens) != 2 {
		t.Errorf("got %d tokens, want 2", len(tokens))
	}
	if usage.InputTokens != 20 || usage.OutputTokens != 5 {
		t.Errorf("usage = %+v", usage)
	}
}

func TestParseSSE(t *testing.T) {
	input := "event: start\ndata: {\"a\":1}\n\ndata: {\"b\":2}\n\ndata: [DONE]\n\ndata: {\"c\":3}\n\n"
	var collected []string
	err := parseSSE(strings.NewReader(input), func(data string) error {
		collected = append(collected, data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(collected) != 2 {
		t.Errorf("got %d events, want 2 (should stop at [DONE])", len(collected))
	}
}

func typeString(v any) string {
	return "*cmd." + typeName(v)
}

func typeName(v any) string {
	switch v.(type) {
	case *anthropicClient:
		return "anthropicClient"
	case *openaiClient:
		return "openaiClient"
	case *geminiClient:
		return "geminiClient"
	default:
		return "unknown"
	}
}

func TestOpenAIClient_ListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q, want /v1/models", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{
				{"id": "gpt-4o"},
				{"id": "gpt-3.5-turbo"},
				{"id": "gpt-4o-mini"},
			},
		})
	}))
	defer srv.Close()

	c := &openaiClient{apiKey: "test-key", baseURL: srv.URL}
	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 3 {
		t.Fatalf("got %d models, want 3", len(models))
	}
	// Should be sorted
	if models[0] != "gpt-3.5-turbo" {
		t.Errorf("models[0] = %q, want gpt-3.5-turbo (sorted)", models[0])
	}
}

func TestAnthropicClient_ListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q, want /v1/models", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "ant-key" {
			t.Errorf("x-api-key = %q", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("anthropic-version = %q", r.Header.Get("anthropic-version"))
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{
				{"id": "claude-sonnet-4-6"},
				{"id": "claude-haiku-4-5"},
			},
		})
	}))
	defer srv.Close()

	c := &anthropicClient{apiKey: "ant-key", baseURL: srv.URL}
	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
}

func TestGeminiClient_ListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if r.URL.Path != "/v1beta/models" {
			t.Errorf("path = %q, want /v1beta/models", r.URL.Path)
		}
		if r.URL.Query().Get("key") != "gem-key" {
			t.Errorf("key = %q", r.URL.Query().Get("key"))
		}
		json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]string{
				{"name": "models/gemini-2.0-flash"},
				{"name": "models/gemini-1.5-pro"},
			},
		})
	}))
	defer srv.Close()

	c := &geminiClient{apiKey: "gem-key", baseURL: srv.URL}
	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
	// Should strip "models/" prefix and be sorted
	if models[0] != "gemini-1.5-pro" {
		t.Errorf("models[0] = %q, want gemini-1.5-pro (stripped + sorted)", models[0])
	}
}

func TestListModels_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer srv.Close()

	c := &openaiClient{apiKey: "bad-key", baseURL: srv.URL}
	_, err := c.ListModels(context.Background())
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %q, should contain 401", err.Error())
	}
}

func TestAllClients_ImplementModelLister(t *testing.T) {
	clients := []aiClient{
		&anthropicClient{},
		&openaiClient{},
		&geminiClient{},
	}
	for _, c := range clients {
		if _, ok := c.(modelLister); !ok {
			t.Errorf("%T does not implement modelLister", c)
		}
	}
}

// ---------- shouldRetry ----------

func TestShouldRetry(t *testing.T) {
	tests := []struct {
		name   string
		status int
		err    error
		want   bool
	}{
		{"429 rate-limited", http.StatusTooManyRequests, nil, true},
		{"500 internal error", http.StatusInternalServerError, nil, true},
		{"502 bad gateway", 502, nil, true},
		{"503 unavailable", http.StatusServiceUnavailable, nil, true},
		{"200 ok", http.StatusOK, nil, false},
		{"400 bad request", http.StatusBadRequest, nil, false},
		{"401 unauthorized", http.StatusUnauthorized, nil, false},
		{"404 not found", http.StatusNotFound, nil, false},
		{"transport error", 0, fmt.Errorf("dial tcp: connection refused"), true},
		{"context canceled", 0, context.Canceled, false},
		{"context deadline exceeded", 0, context.DeadlineExceeded, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldRetry(tt.status, tt.err)
			if got != tt.want {
				t.Errorf("shouldRetry(%d, %v) = %v, want %v", tt.status, tt.err, got, tt.want)
			}
		})
	}
}

// ---------- doWithRetry ----------

// fastRetry overrides the backoff interval so retry tests don't sleep 1s.
func fastRetry(t *testing.T) {
	t.Helper()
	old := retryInitialInterval
	retryInitialInterval = time.Millisecond
	t.Cleanup(func() { retryInitialInterval = old })
}

func TestDoWithRetry_429ThenOK(t *testing.T) {
	fastRetry(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"rate limit"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	resp, err := doWithRetry(context.Background(), func() (*http.Request, error) {
		return http.NewRequestWithContext(context.Background(), "GET", srv.URL, nil)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
	if calls != 2 {
		t.Errorf("handler called %d times, want 2", calls)
	}
}

func TestDoWithRetry_ExhaustsOn500(t *testing.T) {
	fastRetry(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"server error"}`))
	}))
	defer srv.Close()

	_, err := doWithRetry(context.Background(), func() (*http.Request, error) {
		return http.NewRequestWithContext(context.Background(), "GET", srv.URL, nil)
	})
	if err == nil {
		t.Fatal("expected error after all retries exhausted")
	}
	if calls != maxRetryAttempts {
		t.Errorf("handler called %d times, want %d", calls, maxRetryAttempts)
	}
}

func TestDoWithRetry_NonRetryableStatusImmediate(t *testing.T) {
	fastRetry(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer srv.Close()

	// 401 is non-retryable: doWithRetry returns the response for the caller to
	// inspect; it does NOT return an error itself.
	resp, err := doWithRetry(context.Background(), func() (*http.Request, error) {
		return http.NewRequestWithContext(context.Background(), "GET", srv.URL, nil)
	})
	if err != nil {
		t.Fatalf("doWithRetry should not error for 401 (let caller decide): %v", err)
	}
	resp.Body.Close()
	// Handler must be called exactly once — no retries.
	if calls != 1 {
		t.Errorf("handler called %d times, want 1 (401 is non-retryable)", calls)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestDoWithRetry_ContextCancelImmediate(t *testing.T) {
	fastRetry(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	_, err := doWithRetry(ctx, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
