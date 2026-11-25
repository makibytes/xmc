package cmd

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// aiMessage is a single turn in a conversation (role = "user" or "assistant").
type aiMessage struct {
	Role    string
	Content string
}

// TokenUsage reports token consumption for a single AI API call.
type TokenUsage struct {
	InputTokens  int
	OutputTokens int
}

// defaultRequestTimeout is the maximum time an AI API call may take before
// being cancelled. The user can still cancel earlier via ESC. Set generously
// to accommodate slow reasoning models. Configurable via ai.request-timeout.
const defaultRequestTimeout = 120 * time.Second

// maxRetryAttempts is the total number of attempts (1 initial + N-1 retries).
const maxRetryAttempts = 4

// maxRetryAfter is the longest server-requested wait (Retry-After) a request
// sits through; a provider asking for more is reported to the user instead.
const maxRetryAfter = 20 * time.Second

// maxErrorBody bounds how much of an error response body is read.
const maxErrorBody = 64 << 10

// retryInitialInterval is the starting backoff interval between retry attempts.
// It is a var (not a const) so tests can set it to a small value to stay fast.
var retryInitialInterval = 1 * time.Second

// shouldRetry reports whether the HTTP response or transport error warrants a
// retry. Context cancellation is never retried — that is deliberate user action
// — and neither is a certificate failure, which a retry cannot fix.
func shouldRetry(statusCode int, err error) bool {
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false
		}
		if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
			return false
		}
		// Retry other transient network errors (DNS, TCP, etc.).
		return true
	}
	return statusCode == http.StatusRequestTimeout || // 408
		statusCode == http.StatusTooManyRequests || // 429
		statusCode >= http.StatusInternalServerError // 5xx
}

// apiError is an HTTP error answer from an AI provider, reduced to one line:
// the provider, the status, the provider's own error code, its message and —
// where the cause is recognisable — what to do about it.
type apiError struct {
	provider   string // set by the client that issued the request
	status     int
	kind       string // the provider's error code/type/status, e.g. API_KEY_INVALID
	message    string
	retryAfter time.Duration // the server's Retry-After, if any
	permanent  bool          // retrying cannot help (credit or quota exhausted)
	hint       string
}

func (e *apiError) Error() string {
	var b strings.Builder
	if e.provider != "" {
		b.WriteString(e.provider + ": ")
	}
	fmt.Fprintf(&b, "HTTP %d", e.status)
	if e.kind != "" {
		b.WriteString(" " + e.kind)
	}
	if e.message != "" {
		b.WriteString(": " + e.message)
	}
	if e.hint != "" {
		b.WriteString(" — " + e.hint)
	}
	return b.String()
}

// parseAPIError decodes an error response body. Providers disagree on the
// shape — {"error":{"message","type"|"status"|"code"}} (OpenAI, Google,
// OpenRouter), {"type":"error","error":{…}} (Anthropic, OpenCode),
// {"error":"…"}, {"message":"…"} — and some answer with HTML or nothing; in
// every case the result is a single readable line.
func parseAPIError(status int, h http.Header, body []byte) *apiError {
	e := &apiError{status: status, retryAfter: parseRetryAfter(h.Get("Retry-After"))}
	var reason string
	var env struct {
		Error     json.RawMessage `json:"error"`
		Message   string          `json:"message"`
		ErrorType string          `json:"error_type"`
	}
	if json.Unmarshal(body, &env) == nil {
		var obj struct {
			Message string          `json:"message"`
			Type    string          `json:"type"`
			Status  string          `json:"status"`
			Code    json.RawMessage `json:"code"`
			Details []struct {
				Reason string `json:"reason"`
			} `json:"details"`
		}
		var str string
		switch {
		case json.Unmarshal(env.Error, &obj) == nil:
			e.message = obj.Message
			e.kind = cmp.Or(obj.Type, obj.Status, env.ErrorType)
			if code := strings.Trim(string(obj.Code), `"`); code != "" && code != "null" && code != strconv.Itoa(status) {
				e.kind = cmp.Or(code, e.kind)
			}
			for _, d := range obj.Details {
				reason = cmp.Or(reason, d.Reason)
			}
		case json.Unmarshal(env.Error, &str) == nil:
			e.message = str
		}
		e.message = cmp.Or(e.message, env.Message)
	}
	if reason != "" {
		e.kind = reason
	}
	if e.message == "" {
		e.message = string(body) // not JSON, or nothing recognisable in it
	}
	e.message = truncate(strings.Join(strings.Fields(e.message), " "), 300)
	if e.message == "" && e.kind == "" {
		e.message = strings.ToLower(http.StatusText(status))
	}
	low := strings.ToLower(e.kind + " " + e.message)
	e.permanent = e.kind == "FreeUsageLimitError" || strings.Contains(low, "insufficient_quota") ||
		strings.Contains(low, "billing_hard_limit") || strings.Contains(low, "limit: 0")
	return e
}

// parseRetryAfter reads a Retry-After header given in seconds or as a date.
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
		return time.Duration(max(secs, 0)) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(time.Until(t), 0)
	}
	return 0
}

// explain adds the provider name and, where the cause is recognisable, a hint
// to act on: a rejected key names the environment variable it came from, an
// unknown model points at /model, and so on.
func (e *apiError) explain(provider, keyEnv, model string) {
	e.provider = provider
	low := strings.ToLower(e.kind)
	switch {
	case e.kind == "FreeUsageLimitError":
		e.hint = "OpenCode's free models only work inside the OpenCode app; pick a paid model with /model (e.g. deepseek-v4.1-flash) or subscribe to OpenCode Go"
	case e.status == http.StatusUnauthorized || e.status == http.StatusForbidden ||
		strings.HasPrefix(e.kind, "API_KEY") || strings.Contains(low, "authentication") ||
		strings.Contains(low, "permission") || strings.Contains(low, "invalid_api_key"):
		switch {
		case provider == "kilo" && keyEnv == "":
			e.hint = "this model needs an API key — use kilo-auto/free or set KILO_API_KEY"
		case keyEnv != "":
			e.hint = "check " + keyEnv + " (set ai.provider in the config to use a different provider)"
		default:
			e.hint = "check the provider's API key"
		}
	case e.status == http.StatusNotFound || strings.Contains(low, "model_not_found") || strings.Contains(low, "not_found"):
		e.hint = fmt.Sprintf("model %q is not available from %s — pick another with /model, or remove ai.model from the config", model, provider)
	case e.permanent || e.status == http.StatusPaymentRequired:
		e.hint = "out of credit or quota — check the provider's billing page"
	case e.status == http.StatusTooManyRequests:
		e.hint = "rate limited"
		if e.retryAfter > 0 {
			e.hint += fmt.Sprintf(", retry after %s", e.retryAfter.Round(time.Second))
		} else {
			e.hint += " — wait a bit and try again"
		}
	}
}

// doWithRetry executes buildReq → HTTP round trip with capped exponential
// backoff. Only 408, 429 and 5xx statuses (and transient transport errors) are
// retried — not a 429 that says the quota or credit is gone, and not one whose
// Retry-After exceeds maxRetryAfter — and a server's Retry-After is honoured.
// A cancelled or timed-out ctx returns its own error immediately. An error
// status comes back as *apiError; the last attempt's error is the one
// reported. The caller must close the response body.
func doWithRetry(ctx context.Context, buildReq func() (*http.Request, error)) (*http.Response, error) {
	delay := retryInitialInterval
	for attempt := 1; ; attempt++ {
		req, err := buildReq()
		if err != nil {
			return nil, err
		}
		var wait time.Duration
		var failure error
		resp, err := http.DefaultClient.Do(req)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if !shouldRetry(0, err) {
				return nil, err
			}
			failure = err
		case resp.StatusCode < http.StatusBadRequest:
			return resp, nil
		default:
			body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
			_ = resp.Body.Close()
			ae := parseAPIError(resp.StatusCode, resp.Header, body)
			if !shouldRetry(resp.StatusCode, nil) || ae.permanent || ae.retryAfter > maxRetryAfter {
				return nil, ae
			}
			failure, wait = ae, ae.retryAfter
		}
		if attempt >= maxRetryAttempts {
			return nil, failure
		}
		wait = max(wait, delay)
		wait += rand.N(wait/4 + 1) // jitter
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		delay = min(delay*2, 8*time.Second)
	}
}

type aiClient interface {
	// Complete sends a chat completion request over the provider's streaming
	// API, calling onToken (when non-nil) for each text chunk as it arrives,
	// and returns the full text.
	Complete(ctx context.Context, system string, messages []aiMessage, onToken func(string)) (string, TokenUsage, error)
}

type aiEffort string

const (
	effortLow    aiEffort = "low"
	effortMedium aiEffort = "medium"
	effortHigh   aiEffort = "high"
)

func normalizeEffort(effort aiEffort) aiEffort {
	switch effort {
	case effortMedium, effortHigh:
		return effort
	default:
		return effortLow
	}
}

func effortTemperature(effort aiEffort) float64 {
	switch normalizeEffort(effort) {
	case effortMedium:
		return 0.3
	case effortHigh:
		return 0.7
	default:
		return 0
	}
}

type modelSettable interface {
	SetModel(string)
	SetEffort(aiEffort)
	Effort() aiEffort
}

type modelLister interface {
	ListModels(ctx context.Context) ([]string, error)
}

// aiListTimeout is the maximum time for a model-listing API call.
const aiListTimeout = 15 * time.Second

func newAIClient(spec providerSpec) aiClient {
	base := baseAIClient{apiKey: spec.apiKey, keyEnv: spec.keyEnv, model: spec.model, baseURL: spec.baseURL, maxTokens: spec.maxTokens, requestTimeout: spec.requestTimeout}
	switch spec.name {
	case "anthropic":
		return &anthropicClient{baseAIClient: base}
	case "gemini":
		return &geminiClient{baseAIClient: base}
	default:
		return &openaiClient{baseAIClient: base, provider: spec.name}
	}
}

// maxSSELine bounds one event line; a single streamed chunk is far smaller.
const maxSSELine = 4 << 20

// parseSSE reads Server-Sent Events from r and calls handler for each data
// payload ("data:" with or without the space after the colon). A "[DONE]"
// payload signals end-of-stream. When the stream carries no event at all it
// returns what was read instead — a provider that answers an error (or a
// non-streamed completion) with status 200 and a plain body.
func parseSSE(r io.Reader, handler func(data string) error) (stray string, err error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), maxSSELine)
	var other strings.Builder
	events := 0
	for scanner.Scan() {
		line := scanner.Text()
		rest, ok := strings.CutPrefix(line, "data:")
		if !ok {
			if events == 0 && other.Len() < maxErrorBody {
				other.WriteString(line + "\n")
			}
			continue
		}
		events++
		data := strings.TrimPrefix(rest, " ")
		if data == "[DONE]" {
			return "", nil
		}
		if err := handler(data); err != nil {
			return "", err
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if events == 0 {
		return other.String(), nil
	}
	return "", nil
}

// strayError turns a body that arrived where an event stream was expected
// into the error to report: the provider's own error (some send one with
// status 200), or a short excerpt of whatever it was.
func strayError(provider, keyEnv, model, stray string) error {
	stray = strings.TrimSpace(stray)
	if stray == "" {
		return nil
	}
	ae := parseAPIError(http.StatusOK, nil, []byte(stray))
	ae.explain(provider, keyEnv, model)
	if ae.kind != "" || strings.HasPrefix(stray, "{") {
		return ae
	}
	return fmt.Errorf("%s: unexpected response (not an event stream): %s", provider, truncate(strings.Join(strings.Fields(stray), " "), 200))
}

// fetchModelIDs executes an HTTP request, decodes the response with extract
// (which knows the provider's model-listing JSON shape), and returns the
// non-empty ids sorted. Used by anthropicClient, openaiClient and geminiClient.
func fetchModelIDs(req *http.Request, label, keyEnv string, extract func(io.Reader) ([]string, error)) ([]string, error) {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		ae := parseAPIError(resp.StatusCode, resp.Header, b)
		ae.explain(label, keyEnv, "")
		return nil, ae
	}
	ids, err := extract(resp.Body)
	if err != nil {
		return nil, err
	}
	ids = slices.DeleteFunc(ids, func(id string) bool { return id == "" })
	slices.Sort(ids)
	return ids, nil
}

// extractOpenAIModelIDs decodes the OpenAI-style {"data":[{"id":"..."}]}
// model-listing response shared by Anthropic and OpenAI-compatible providers.
func extractOpenAIModelIDs(r io.Reader) ([]string, error) {
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(r).Decode(&result); err != nil {
		return nil, err
	}
	ids := make([]string, len(result.Data))
	for i, d := range result.Data {
		ids[i] = d.ID
	}
	return ids, nil
}

// baseAIClient holds the fields and settable state common to every provider
// implementation (model/effort selection, request timeout). Each provider
// embeds it and adds only what genuinely differs (e.g. openaiClient's
// provider field for its per-vendor reasoning quirks).
type baseAIClient struct {
	apiKey         string
	keyEnv         string // environment variable apiKey came from, for error hints
	model          string
	baseURL        string
	effort         aiEffort
	maxTokens      int
	requestTimeout time.Duration
}

func (c *baseAIClient) SetModel(model string)     { c.model = model }
func (c *baseAIClient) SetEffort(effort aiEffort) { c.effort = normalizeEffort(effort) }
func (c *baseAIClient) Effort() aiEffort          { return normalizeEffort(c.effort) }

// callContext derives a request-scoped context bounded by the client's
// configured timeout (or defaultRequestTimeout when unset). The caller must
// invoke the returned cancel func.
func (c *baseAIClient) callContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := c.requestTimeout
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

// post sends body as JSON to url, authenticated by setAuth, retrying transient
// failures (see doWithRetry). Failures come back prefixed with the provider's
// name; an error answer is an *apiError with a hint where the cause is
// recognisable. The caller must close the response body.
func (c *baseAIClient) post(ctx context.Context, label, url string, body any, setAuth func(http.Header)) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	resp, err := doWithRetry(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		setAuth(req.Header)
		return req, nil
	})
	if err != nil {
		if ae, ok := errors.AsType[*apiError](err); ok {
			ae.explain(label, c.keyEnv, c.model)
			return nil, ae
		}
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	return resp, nil
}

// listModels GETs the provider's model-listing endpoint at path and decodes
// it with extract (see fetchModelIDs).
func (c *baseAIClient) listModels(ctx context.Context, label, path string, setAuth func(http.Header), extract func(io.Reader) ([]string, error)) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, aiListTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	setAuth(req.Header)
	return fetchModelIDs(req, label, c.keyEnv, extract)
}

// tokenSink returns onToken, or a no-op when the caller doesn't want tokens.
func tokenSink(onToken func(string)) func(string) {
	if onToken == nil {
		return func(string) {}
	}
	return onToken
}

// --- Anthropic (POST /v1/messages) ---

type anthropicClient struct {
	baseAIClient
}

func supportsAnthropicEffort(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	prefixes := []string{
		"claude-fable-5",
		"claude-mythos-5",
		"claude-mythos-preview",
		"claude-opus-5",
		"claude-opus-4-5",
		"claude-opus-4-6",
		"claude-opus-4-7",
		"claude-opus-4-8",
		"claude-sonnet-5",
		"claude-sonnet-4-6",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}
	return false
}

func (c *anthropicClient) setAuth(h http.Header) {
	h.Set("x-api-key", c.apiKey)
	h.Set("anthropic-version", "2023-06-01")
}

func (c *anthropicClient) ListModels(ctx context.Context) ([]string, error) {
	return c.listModels(ctx, "anthropic", "/v1/models", c.setAuth, extractOpenAIModelIDs)
}

func (c *anthropicClient) Complete(ctx context.Context, system string, messages []aiMessage, onToken func(string)) (string, TokenUsage, error) {
	ctx, cancel := c.callContext(ctx)
	defer cancel()

	msgs := make([]map[string]string, len(messages))
	for i, m := range messages {
		msgs[i] = map[string]string{"role": m.Role, "content": m.Content}
	}

	body := map[string]any{
		"model":      c.model,
		"max_tokens": c.maxTokens,
		"system":     system,
		"messages":   msgs,
		"stream":     true,
	}
	if supportsAnthropicEffort(c.model) {
		body["output_config"] = map[string]any{"effort": c.Effort()}
	} else {
		body["temperature"] = effortTemperature(c.Effort())
	}

	resp, err := c.post(ctx, "anthropic", c.baseURL+"/v1/messages", body, c.setAuth)
	if err != nil {
		return "", TokenUsage{}, err
	}
	defer resp.Body.Close() //nolint:errcheck

	return c.parseStream(resp.Body, tokenSink(onToken))
}

func (c *anthropicClient) parseStream(r io.Reader, onToken func(string)) (string, TokenUsage, error) {
	var text strings.Builder
	var usage TokenUsage
	var stopReason string

	stray, err := parseSSE(r, func(data string) error {
		var event struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return nil
		}

		switch event.Type {
		case "message_start":
			var e struct {
				Message struct {
					Usage struct {
						InputTokens int `json:"input_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			_ = json.Unmarshal([]byte(data), &e)
			usage.InputTokens = e.Message.Usage.InputTokens

		case "content_block_delta":
			var e struct {
				Delta struct {
					Text string `json:"text"`
				} `json:"delta"`
			}
			_ = json.Unmarshal([]byte(data), &e)
			if e.Delta.Text != "" {
				text.WriteString(e.Delta.Text)
				onToken(e.Delta.Text)
			}

		case "message_delta":
			var e struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
				Usage struct {
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			_ = json.Unmarshal([]byte(data), &e)
			usage.OutputTokens = e.Usage.OutputTokens
			if e.Delta.StopReason != "" {
				stopReason = e.Delta.StopReason
			}

		case "error":
			var e struct {
				Error struct {
					Type    string `json:"type"`
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.Unmarshal([]byte(data), &e)
			return fmt.Errorf("anthropic: stream error: %s: %s", e.Error.Type, e.Error.Message)
		}
		return nil
	})

	if err != nil {
		return text.String(), usage, err
	}
	if text.Len() == 0 {
		if err := strayError("anthropic", c.keyEnv, c.model, stray); err != nil {
			return "", usage, err
		}
		return "", usage, fmt.Errorf("anthropic: empty response (model %s, stop_reason=%s)%s", c.model, stopReason, maxTokensHint(stopReason == "max_tokens"))
	}
	return text.String(), usage, nil
}

// --- OpenAI-compatible (POST /v1/chat/completions) ---
// Covers: OpenAI, xAI, DeepSeek, Mistral

type openaiClient struct {
	baseAIClient
	provider string
}

// reasoningModelPrefixes are OpenAI model names/prefixes for the o-series and
// gpt-5/codex reasoning models. These reject the standard chat-completion
// request shape with HTTP 400: max_tokens must be sent as
// max_completion_tokens, and temperature must be omitted (they only accept
// the fixed default of 1). Entries ending in "-" match by prefix (dated and
// sized variants like o1-mini, o3-mini, gpt-5-thinking); others match exactly.
var reasoningModelPrefixes = []string{"o1", "o1-", "o3", "o3-", "o4-", "gpt-5", "gpt-5-", "codex-"}

// isReasoningModel reports whether model belongs to an OpenAI reasoning-model
// family (see reasoningModelPrefixes). It only recognizes OpenAI's own naming
// scheme — OpenAI-compatible providers (xAI, DeepSeek, Mistral) that reuse
// this client accept the standard max_tokens/temperature shape.
func isReasoningModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if strings.HasPrefix(m, "gpt-5.") {
		return true
	}
	for _, p := range reasoningModelPrefixes {
		if strings.HasSuffix(p, "-") {
			if strings.HasPrefix(m, p) {
				return true
			}
		} else if m == p {
			return true
		}
	}
	return false
}

// isDeepSeekV4 reports whether the request goes to a DeepSeek V4 model
// (deepseek-v4-flash, deepseek-v4.1-flash, …), which takes a "thinking"
// switch and reasoning_effort instead of temperature. DeepSeek's own API and
// OpenCode's pass-through of it both use that shape.
func (c *openaiClient) isDeepSeekV4() bool {
	if c.provider != "deepseek" && c.provider != "opencode" {
		return false
	}
	return strings.Contains(strings.ToLower(c.model), "deepseek-v4")
}

func (c *openaiClient) isXAIReasoningModel() bool {
	model := strings.ToLower(strings.TrimSpace(c.model))
	return c.provider == "xai" && strings.HasPrefix(model, "grok-4.5")
}

func (c *openaiClient) isMistralReasoningModel() bool {
	if c.provider != "mistral" {
		return false
	}
	model := strings.ToLower(strings.TrimSpace(c.model))
	return model == "mistral-small-latest" || model == "mistral-small-2603" || model == "mistral-medium-3-5"
}

func (c *openaiClient) addGenerationConfig(body map[string]any) {
	effort := c.Effort()

	if c.isDeepSeekV4() {
		body["max_tokens"] = c.maxTokens
		if effort == effortLow {
			body["thinking"] = map[string]any{"type": "disabled"}
			return
		}

		body["thinking"] = map[string]any{"type": "enabled"}
		if effort == effortHigh {
			body["reasoning_effort"] = "max"
		} else {
			body["reasoning_effort"] = "high"
		}
		return
	}

	if isReasoningModel(c.model) {
		body["max_completion_tokens"] = c.maxTokens
		body["reasoning_effort"] = effort
		return
	}

	if c.isXAIReasoningModel() || c.isMistralReasoningModel() {
		body["max_tokens"] = c.maxTokens
		body["reasoning_effort"] = effort
		return
	}

	body["max_tokens"] = c.maxTokens
	body["temperature"] = effortTemperature(effort)
}

// setAuth sends the key as a bearer token; a keyless provider (Kilo's free
// router) is called without the header — an empty bearer is rejected there.
func (c *openaiClient) setAuth(h http.Header) {
	if c.apiKey != "" {
		h.Set("Authorization", "Bearer "+c.apiKey)
	}
}

// label names the provider in errors: the configured one, or the host of a
// custom OpenAI-compatible endpoint.
func (c *openaiClient) label() string {
	return cmp.Or(c.provider, "openai-compatible ("+c.baseURL+")")
}

func (c *openaiClient) ListModels(ctx context.Context) ([]string, error) {
	return c.listModels(ctx, c.label(), "/v1/models", c.setAuth, extractOpenAIModelIDs)
}

func (c *openaiClient) Complete(ctx context.Context, system string, messages []aiMessage, onToken func(string)) (string, TokenUsage, error) {
	ctx, cancel := c.callContext(ctx)
	defer cancel()

	msgs := []map[string]string{{"role": "system", "content": system}}
	for _, m := range messages {
		msgs = append(msgs, map[string]string{"role": m.Role, "content": m.Content})
	}

	body := map[string]any{
		"model":          c.model,
		"messages":       msgs,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	c.addGenerationConfig(body)

	resp, err := c.post(ctx, c.label(), c.baseURL+"/v1/chat/completions", body, c.setAuth)
	if err != nil {
		return "", TokenUsage{}, err
	}
	defer resp.Body.Close() //nolint:errcheck

	return c.parseStream(resp.Body, tokenSink(onToken))
}

type chatContent string

func (content *chatContent) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*content = ""
		return nil
	}

	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*content = chatContent(text)
		return nil
	}

	var chunks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &chunks); err != nil {
		return err
	}

	var result strings.Builder
	for _, chunk := range chunks {
		if chunk.Type == "text" || (chunk.Type == "" && chunk.Text != "") {
			result.WriteString(chunk.Text)
		}
	}
	*content = chatContent(result.String())
	return nil
}

func (c *openaiClient) parseStream(r io.Reader, onToken func(string)) (string, TokenUsage, error) {
	var text strings.Builder
	var reasoning strings.Builder
	var usage TokenUsage
	var finishReason string

	stray, err := parseSSE(r, func(data string) error {
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          chatContent `json:"content"`
					ReasoningContent string      `json:"reasoning_content"` // DeepSeek reasoning models
					Reasoning        string      `json:"reasoning"`         // some proxies use this field
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			return fmt.Errorf("%s: stream error: %s", c.label(), chunk.Error.Message)
		}
		if len(chunk.Choices) > 0 {
			d := chunk.Choices[0].Delta
			// Reasoning models stream chain-of-thought in reasoning_content
			// (or reasoning); show it live but don't include in the returned text.
			if rc := cmp.Or(d.ReasoningContent, d.Reasoning); rc != "" {
				reasoning.WriteString(rc)
				onToken(rc)
			}
			if content := string(d.Content); content != "" {
				text.WriteString(content)
				onToken(content)
			}
			if chunk.Choices[0].FinishReason != "" {
				finishReason = chunk.Choices[0].FinishReason
			}
		}
		if chunk.Usage != nil {
			usage.InputTokens = chunk.Usage.PromptTokens
			usage.OutputTokens = chunk.Usage.CompletionTokens
		}
		return nil
	})

	if err != nil {
		return text.String(), usage, err
	}
	if text.Len() == 0 {
		// Some gateways answer a streaming request with a plain completion.
		if content := nonStreamedContent(stray); content != "" {
			onToken(content)
			return content, usage, nil
		}
		if err := strayError(c.label(), c.keyEnv, c.model, stray); err != nil {
			return "", usage, err
		}
		if reasoning.Len() > 0 {
			return "", usage, fmt.Errorf("%s: model %s produced only reasoning (%d tokens), no answer (finish_reason=%s); try raising ai.max-tokens in ~/.xmc config or use a non-reasoning model",
				c.label(), c.model, usage.OutputTokens, finishReason)
		}
		return "", usage, fmt.Errorf("%s: empty response (model %s, finish_reason=%s)%s", c.label(), c.model, finishReason, maxTokensHint(finishReason == "length"))
	}
	return text.String(), usage, nil
}

// nonStreamedContent extracts the answer from a plain (non-streamed)
// chat-completion body, or "" when stray is not one.
func nonStreamedContent(stray string) string {
	var resp struct {
		Choices []struct {
			Message struct {
				Content chatContent `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal([]byte(stray), &resp) != nil || len(resp.Choices) == 0 {
		return ""
	}
	return string(resp.Choices[0].Message.Content)
}

// maxTokensHint is appended to an empty-response error when the output budget
// ran out before any answer text.
func maxTokensHint(hit bool) string {
	if !hit {
		return ""
	}
	return " — the output limit was reached; raise ai.max-tokens in the config"
}

// --- Google Gemini (POST :generateContent / :streamGenerateContent) ---

type geminiClient struct {
	baseAIClient
}

func supportsGeminiEffort(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "gemini-3")
}

// extractGeminiModelIDs decodes Gemini's {"models":[{"name":"models/..."}]}
// model-listing response, stripping the "models/" prefix.
func extractGeminiModelIDs(r io.Reader) ([]string, error) {
	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(r).Decode(&result); err != nil {
		return nil, err
	}
	ids := make([]string, len(result.Models))
	for i, m := range result.Models {
		ids[i] = strings.TrimPrefix(m.Name, "models/")
	}
	return ids, nil
}

// geminiKeyHeader carries the API key. Gemini also accepts it as a "?key="
// URL parameter, but a URL ends up verbatim in Go's transport errors
// ("Post \"https://…?key=…\": dial tcp …"), which the AI shell prints — so
// the key must never be part of the URL.
const geminiKeyHeader = "x-goog-api-key"

func (c *geminiClient) setAuth(h http.Header) {
	h.Set(geminiKeyHeader, c.apiKey)
}

func (c *geminiClient) ListModels(ctx context.Context) ([]string, error) {
	return c.listModels(ctx, "gemini", "/v1beta/models", c.setAuth, extractGeminiModelIDs)
}

func (c *geminiClient) Complete(ctx context.Context, system string, messages []aiMessage, onToken func(string)) (string, TokenUsage, error) {
	ctx, cancel := c.callContext(ctx)
	defer cancel()

	contents := make([]map[string]any, len(messages))
	for i, m := range messages {
		role := m.Role
		if role == "assistant" {
			role = "model"
		}
		contents[i] = map[string]any{
			"role":  role,
			"parts": []map[string]string{{"text": m.Content}},
		}
	}

	generationConfig := map[string]any{
		"maxOutputTokens": c.maxTokens,
	}
	if supportsGeminiEffort(c.model) {
		generationConfig["thinkingConfig"] = map[string]any{"thinkingLevel": c.Effort()}
	} else {
		generationConfig["temperature"] = effortTemperature(c.Effort())
	}

	body := map[string]any{
		"system_instruction": map[string]any{
			"parts": []map[string]string{
				{"text": system},
			},
		},
		"generationConfig": generationConfig,
		"contents":         contents,
	}

	apiURL := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse", c.baseURL, c.model)
	resp, err := c.post(ctx, "gemini", apiURL, body, c.setAuth)
	if err != nil {
		return "", TokenUsage{}, err
	}
	defer resp.Body.Close() //nolint:errcheck

	return c.parseStream(resp.Body, tokenSink(onToken))
}

func (c *geminiClient) parseStream(r io.Reader, onToken func(string)) (string, TokenUsage, error) {
	var text strings.Builder
	var usage TokenUsage
	var finishReason, blockReason string

	stray, err := parseSSE(r, func(data string) error {
		var chunk struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text    string `json:"text"`
						Thought bool   `json:"thought"`
					} `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
			PromptFeedback *struct {
				BlockReason        string `json:"blockReason"`
				BlockReasonMessage string `json:"blockReasonMessage"`
			} `json:"promptFeedback"`
			UsageMetadata *struct {
				PromptTokenCount     int `json:"promptTokenCount"`
				CandidatesTokenCount int `json:"candidatesTokenCount"`
			} `json:"usageMetadata"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			return fmt.Errorf("gemini: stream error: %s", chunk.Error.Message)
		}
		if chunk.PromptFeedback != nil && chunk.PromptFeedback.BlockReason != "" {
			blockReason = cmp.Or(chunk.PromptFeedback.BlockReasonMessage, chunk.PromptFeedback.BlockReason)
		}
		if len(chunk.Candidates) > 0 {
			// A chunk can carry several parts, and thinking models mark their
			// reasoning summary with thought:true — only the answer is text.
			for _, part := range chunk.Candidates[0].Content.Parts {
				if part.Text != "" && !part.Thought {
					text.WriteString(part.Text)
					onToken(part.Text)
				}
			}
			if chunk.Candidates[0].FinishReason != "" {
				finishReason = chunk.Candidates[0].FinishReason
			}
		}
		if chunk.UsageMetadata != nil {
			usage.InputTokens = chunk.UsageMetadata.PromptTokenCount
			usage.OutputTokens = chunk.UsageMetadata.CandidatesTokenCount
		}
		return nil
	})

	if err != nil {
		return text.String(), usage, err
	}
	if text.Len() == 0 {
		if err := strayError("gemini", c.keyEnv, c.model, stray); err != nil {
			return "", usage, err
		}
		if blockReason != "" {
			return "", usage, fmt.Errorf("gemini: the prompt was blocked (%s)", blockReason)
		}
		return "", usage, fmt.Errorf("gemini: empty response (model %s, finish_reason=%s)%s", c.model, finishReason, maxTokensHint(finishReason == "MAX_TOKENS"))
	}
	return text.String(), usage, nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
