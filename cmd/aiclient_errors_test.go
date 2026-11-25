package cmd

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// These tests pin what the AI shell shows when a provider misbehaves: every
// failure is one readable line that names the provider and, where the cause is
// recognisable, what to do — never a raw multi-line JSON body, never silence.

func TestParseAPIError_Shapes(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantKind   string
		wantMsg    string
		wantPerm   bool
		wantOneLn  bool
		wantNoJSON bool
	}{
		{
			name: "google pretty-printed", status: 400, wantKind: "API_KEY_INVALID", wantMsg: "API key not valid. Please pass a valid API key.", wantOneLn: true,
			body: "{\n  \"error\": {\n    \"code\": 400,\n    \"message\": \"API key not valid. Please pass a valid API key.\",\n    \"status\": \"INVALID_ARGUMENT\",\n    \"details\": [\n      {\"@type\": \"type.googleapis.com/google.rpc.ErrorInfo\", \"reason\": \"API_KEY_INVALID\"}\n    ]\n  }\n}\n",
		},
		{
			name: "anthropic envelope", status: 401, wantKind: "authentication_error", wantMsg: "invalid x-api-key", wantOneLn: true,
			body: `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`,
		},
		{
			name: "opencode free tier", status: 429, wantKind: "FreeUsageLimitError", wantPerm: true, wantOneLn: true,
			wantMsg: "Free usage limit reached. Free models work in OpenCode (https://opencode.ai); subscribe to OpenCode Go to keep going: https://opencode.ai/go",
			body:    `{"type":"error","error":{"type":"FreeUsageLimitError","message":"Free usage limit reached. Free models work in OpenCode (https://opencode.ai); subscribe to OpenCode Go to keep going: https://opencode.ai/go"}}`,
		},
		{
			name: "kilo string code", status: 401, wantKind: "PAID_MODEL_AUTH_REQUIRED", wantMsg: "You need to sign in to use this model.", wantOneLn: true,
			body: `{"error":{"code":"PAID_MODEL_AUTH_REQUIRED","message":"You need to sign in to use this model."},"error_type":"paid_model_auth_required"}`,
		},
		{
			name: "openai quota", status: 429, wantKind: "insufficient_quota", wantPerm: true, wantOneLn: true, wantMsg: "You exceeded your current quota",
			body: `{"error":{"message":"You exceeded your current quota","type":"insufficient_quota","code":"insufficient_quota"}}`,
		},
		{
			name: "error is a string", status: 401, wantMsg: "invalid api key", wantOneLn: true,
			body: `{"error": "invalid api key"}`,
		},
		{
			name: "top-level message", status: 400, wantMsg: "bad things", wantOneLn: true,
			body: `{"message":"bad things"}`,
		},
		{
			name: "html page", status: 502, wantOneLn: true, wantNoJSON: true,
			wantMsg: "<html> <body>Bad Gateway</body> </html>",
			body:    "<html>\n<body>Bad Gateway</body>\n</html>\n",
		},
		{name: "empty body", status: 503, wantMsg: "service unavailable", wantOneLn: true, body: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := parseAPIError(tt.status, http.Header{}, []byte(tt.body))
			if e.kind != tt.wantKind || e.message != tt.wantMsg || e.permanent != tt.wantPerm {
				t.Errorf("got kind=%q message=%q permanent=%v\nwant kind=%q message=%q permanent=%v",
					e.kind, e.message, e.permanent, tt.wantKind, tt.wantMsg, tt.wantPerm)
			}
			if line := e.Error(); tt.wantOneLn && strings.Contains(line, "\n") {
				t.Errorf("error spans several lines: %q", line)
			}
		})
	}

	long := parseAPIError(500, http.Header{}, []byte(strings.Repeat("x", 5000)))
	if n := len([]rune(long.message)); n > 310 {
		t.Errorf("message is %d runes, want it cut near 300", n)
	}
}

// completeAgainst runs one request of c against a server running handler and
// returns the error.
func completeAgainst(t *testing.T, c aiClient) error {
	t.Helper()
	_, _, err := c.Complete(context.Background(), "s", []aiMessage{{Role: "user", Content: "u"}}, nil)
	return err
}

func TestGeminiClient_BadKeyIsOneReadableLine(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("{\n  \"error\": {\n    \"code\": 400,\n    \"message\": \"API key not valid. Please pass a valid API key.\",\n    \"status\": \"INVALID_ARGUMENT\",\n    \"details\": [{\"reason\": \"API_KEY_INVALID\"}]\n  }\n}\n"))
	}))
	defer srv.Close()

	c := newAIClient(providerSpec{name: "gemini", apiKey: "bad", keyEnv: "GEMINI_API_KEY", baseURL: srv.URL, model: "gemini-3.8-flash"})
	err := completeAgainst(t, c)
	if err == nil {
		t.Fatal("want an error")
	}
	want := "gemini: HTTP 400 API_KEY_INVALID: API key not valid. Please pass a valid API key. — check GEMINI_API_KEY"
	if !strings.HasPrefix(err.Error(), want) || strings.Contains(err.Error(), "\n") {
		t.Errorf("error = %q\nwant one line starting %q", err, want)
	}
	if calls.Load() != 1 {
		t.Errorf("made %d requests, want 1 (a rejected key is not retried)", calls.Load())
	}
}

func TestAIClient_ErrorHints(t *testing.T) {
	tests := []struct {
		name     string
		spec     providerSpec
		status   int
		body     string
		wantAll  []string
		attempts int32
	}{
		{
			name: "unknown model", spec: providerSpec{name: "openai", apiKey: "k", keyEnv: "OPENAI_API_KEY", model: "gpt-nope"},
			status: 404, body: `{"error":{"message":"The model does not exist","type":"invalid_request_error","code":"model_not_found"}}`,
			wantAll: []string{"openai: HTTP 404", `model "gpt-nope" is not available from openai`, "/model"}, attempts: 1,
		},
		{
			name: "opencode free model refused", spec: providerSpec{name: "opencode", apiKey: "k", keyEnv: "OPENCODE_API_KEY", model: "deepseek-v4.1-flash-free"},
			status: 429, body: `{"type":"error","error":{"type":"FreeUsageLimitError","message":"Free usage limit reached."}}`,
			wantAll: []string{"opencode: HTTP 429 FreeUsageLimitError", "only work inside the OpenCode app", "deepseek-v4.1-flash"}, attempts: 1,
		},
		{
			name: "out of credit", spec: providerSpec{name: "openrouter", apiKey: "k", keyEnv: "OPENROUTER_API_KEY", model: "m"},
			status: 402, body: `{"error":{"message":"Insufficient credits","code":402}}`,
			wantAll: []string{"openrouter: HTTP 402", "out of credit or quota"}, attempts: 1,
		},
		{
			name: "kilo paid model without key", spec: providerSpec{name: "kilo", model: "anthropic/claude-sonnet-5"},
			status: 401, body: `{"error":{"code":"PAID_MODEL_AUTH_REQUIRED","message":"You need to sign in to use this model."}}`,
			wantAll: []string{"kilo: HTTP 401 PAID_MODEL_AUTH_REQUIRED", "use kilo-auto/free or set KILO_API_KEY"}, attempts: 1,
		},
		{
			name: "anthropic bad key", spec: providerSpec{name: "anthropic", apiKey: "k", keyEnv: "ANTHROPIC_API_KEY", model: "m"},
			status: 401, body: `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`,
			wantAll: []string{"anthropic: HTTP 401 authentication_error: invalid x-api-key", "check ANTHROPIC_API_KEY"}, attempts: 1,
		},
	}
	fastRetry(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			tt.spec.baseURL = srv.URL
			err := completeAgainst(t, newAIClient(tt.spec))
			if err == nil {
				t.Fatal("want an error")
			}
			for _, w := range tt.wantAll {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q lacks %q", err, w)
				}
			}
			if calls.Load() != tt.attempts {
				t.Errorf("made %d requests, want %d", calls.Load(), tt.attempts)
			}
		})
	}
}

// A 429 that is merely a rate limit is retried (honouring Retry-After); one that
// says the quota is gone, or asks for a long wait, is reported at once.
func TestAIClient_RateLimitRetryPolicy(t *testing.T) {
	fastRetry(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
			return
		}
		writeOpenAIStream(w, "ok")
	}))
	defer srv.Close()
	c := newAIClient(providerSpec{name: "openai", apiKey: "k", baseURL: srv.URL, model: "m"})
	if err := completeAgainst(t, c); err != nil {
		t.Fatalf("transient 429 should be retried: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("made %d requests, want 2", calls.Load())
	}

	calls.Store(0)
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"daily limit"}}`))
	}))
	defer srv2.Close()
	c = newAIClient(providerSpec{name: "openai", apiKey: "k", baseURL: srv2.URL, model: "m"})
	err := completeAgainst(t, c)
	if err == nil || !strings.Contains(err.Error(), "retry after 1h0m0s") {
		t.Errorf("error = %v, want a one-hour retry-after message", err)
	}
	if calls.Load() != 1 {
		t.Errorf("made %d requests, want 1 (a long Retry-After is not waited out)", calls.Load())
	}
}

// The final answer's error is what gets reported, not an earlier attempt's.
func TestDoWithRetry_ReportsTheLastError(t *testing.T) {
	fastRetry(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"the real problem"}}`))
	}))
	defer srv.Close()
	_, err := doWithRetry(context.Background(), func() (*http.Request, error) {
		return http.NewRequest("GET", srv.URL, nil)
	})
	if err == nil || !strings.Contains(err.Error(), "the real problem") || strings.Contains(err.Error(), "503") {
		t.Errorf("error = %v, want the 400's message, not the earlier 503", err)
	}
}

func TestShouldRetry_MoreCases(t *testing.T) {
	if !shouldRetry(http.StatusRequestTimeout, nil) {
		t.Error("408 should be retried")
	}
	if shouldRetry(0, &tls.CertificateVerificationError{}) {
		t.Error("a certificate failure cannot be fixed by retrying")
	}
	if shouldRetry(0, errors.Join(context.Canceled)) {
		t.Error("cancellation is never retried")
	}
}

// A provider that answers 200 with something other than an event stream must
// produce an error or an answer, never an empty silence.
func TestAIClient_NonStreamBodies(t *testing.T) {
	tests := []struct {
		name    string
		spec    providerSpec
		body    string
		wantErr string // "" = success with wantText
		want    string
	}{
		{"json error with status 200", providerSpec{name: "openai", apiKey: "k", keyEnv: "OPENAI_API_KEY"},
			`{"error":{"message":"The server had an error","type":"server_error"}}`, "openai: HTTP 200 server_error: The server had an error", ""},
		{"plain completion", providerSpec{name: "openrouter", apiKey: "k"},
			`{"choices":[{"message":{"role":"assistant","content":"manage list"}}]}`, "", "manage list"},
		{"html", providerSpec{name: "openai", apiKey: "k"},
			"<html><body>Sign in</body></html>", "unexpected response (not an event stream): <html><body>Sign in</body></html>", ""},
		{"nothing at all", providerSpec{name: "openai", apiKey: "k"}, "", "empty response", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			tt.spec.baseURL, tt.spec.model = srv.URL, "m"
			text, _, err := newAIClient(tt.spec).Complete(context.Background(), "s", []aiMessage{{Role: "user", Content: "u"}}, nil)
			if tt.wantErr == "" {
				if err != nil || text != tt.want {
					t.Errorf("text, err = %q, %v; want %q", text, err, tt.want)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseSSE_Variants(t *testing.T) {
	var got []string
	collect := func(d string) error { got = append(got, d); return nil }

	// "data:" without the optional space; CRLF line ends.
	if _, err := parseSSE(strings.NewReader("data:{\"a\":1}\r\n\r\ndata: {\"b\":2}\r\n\r\n"), collect); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != `{"a":1}` {
		t.Errorf("events = %q", got)
	}

	// An event line beyond bufio's default 64 KB token limit.
	got = nil
	big := strings.Repeat("x", 200<<10)
	if _, err := parseSSE(strings.NewReader("data: "+big+"\n\n"), collect); err != nil {
		t.Fatalf("a large event must not fail the stream: %v", err)
	}
	if len(got) != 1 || len(got[0]) != len(big) {
		t.Errorf("large event lost: %d events", len(got))
	}

	// No event at all: the body is handed back.
	stray, err := parseSSE(strings.NewReader("{\"error\":\"nope\"}\n"), collect)
	if err != nil || !strings.Contains(stray, "nope") {
		t.Errorf("stray = %q, err = %v", stray, err)
	}
}

func TestGeminiClient_ThoughtPartsAndBlockReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(w,
			`{"candidates":[{"content":{"parts":[{"text":"thinking about it","thought":true},{"text":"manage "},{"text":"list"}]}}]}`,
			`{"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":4}}`)
	}))
	defer srv.Close()
	c := newAIClient(providerSpec{name: "gemini", apiKey: "k", baseURL: srv.URL, model: "gemini-3.8-flash"})
	text, usage, err := c.Complete(context.Background(), "s", []aiMessage{{Role: "user", Content: "u"}}, nil)
	if err != nil || text != "manage list" || usage.OutputTokens != 4 {
		t.Errorf("text = %q usage = %+v err = %v, want every answer part joined and no thought text", text, usage, err)
	}

	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(w, `{"promptFeedback":{"blockReason":"SAFETY"}}`)
	}))
	defer blocked.Close()
	c = newAIClient(providerSpec{name: "gemini", apiKey: "k", baseURL: blocked.URL, model: "m"})
	if _, _, err := c.Complete(context.Background(), "s", []aiMessage{{Role: "user", Content: "u"}}, nil); err == nil || !strings.Contains(err.Error(), "blocked (SAFETY)") {
		t.Errorf("error = %v, want the block reason", err)
	}
}

// A provider that stalls mid-stream ends at the request timeout with a
// deadline error — the caller turns that into the "timed out" message.
func TestAIClient_StalledStreamHitsRequestTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ma\"}}]}\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	c := newAIClient(providerSpec{name: "openai", apiKey: "k", baseURL: srv.URL, model: "m", requestTimeout: 150 * time.Millisecond})
	start := time.Now()
	_, _, err := c.Complete(context.Background(), "s", []aiMessage{{Role: "user", Content: "u"}}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want a deadline error", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("took %s, want about the 150ms timeout", time.Since(start))
	}
}

func TestListModels_ErrorIsExplained(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid credential","type":"authentication_error"}}`))
	}))
	defer srv.Close()
	c := newAIClient(providerSpec{name: "opencode", apiKey: "k", keyEnv: "OPENCODE_API_KEY", baseURL: srv.URL}).(modelLister)
	_, err := c.ListModels(context.Background())
	if err == nil || !strings.Contains(err.Error(), "opencode: HTTP 401 authentication_error: Invalid credential — check OPENCODE_API_KEY") {
		t.Errorf("error = %v", err)
	}
}
