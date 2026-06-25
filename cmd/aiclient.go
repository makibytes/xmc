package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
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

// aiRequestTimeout is the maximum time an AI API call may take before being
// cancelled. The user can still cancel earlier via ESC.
const aiRequestTimeout = 60 * time.Second

type aiClient interface {
	// Complete sends a chat completion request. When onToken is non-nil the
	// client uses the provider's streaming API and calls onToken for each
	// text chunk as it arrives; when nil, the non-streaming endpoint is used.
	Complete(ctx context.Context, system string, messages []aiMessage, onToken func(string)) (string, TokenUsage, error)
}

type modelSettable interface {
	SetModel(string)
	SetTemperature(float64)
	Temperature() float64
}

type modelLister interface {
	ListModels(ctx context.Context) ([]string, error)
}

// aiListTimeout is the maximum time for a model-listing API call.
const aiListTimeout = 15 * time.Second

func newAIClient(spec providerSpec) aiClient {
	switch spec.name {
	case "anthropic":
		return &anthropicClient{apiKey: spec.apiKey, model: spec.model, baseURL: spec.baseURL, maxTokens: spec.maxTokens}
	case "gemini":
		return &geminiClient{apiKey: spec.apiKey, model: spec.model, baseURL: spec.baseURL, maxTokens: spec.maxTokens}
	default:
		return &openaiClient{apiKey: spec.apiKey, model: spec.model, baseURL: spec.baseURL, maxTokens: spec.maxTokens}
	}
}

// parseSSE reads Server-Sent Events from r and calls handler for each data
// payload. Lines prefixed "data: [DONE]" signal end-of-stream.
func parseSSE(r io.Reader, handler func(data string) error) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			return nil
		}
		if err := handler(data); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// fetchModelIDs executes an HTTP request and parses the OpenAI-style
// {"data":[{"id":"..."}]} response. Used by anthropicClient and openaiClient.
func fetchModelIDs(req *http.Request) ([]string, error) {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("models API %d: %s", resp.StatusCode, truncate(string(b), 200))
	}
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(result.Data))
	for _, d := range result.Data {
		if d.ID != "" {
			ids = append(ids, d.ID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// --- Anthropic (POST /v1/messages) ---

type anthropicClient struct {
	apiKey      string
	model       string
	baseURL     string
	temperature float64
	maxTokens   int
}

func (c *anthropicClient) SetModel(m string)       { c.model = m }
func (c *anthropicClient) SetTemperature(t float64) { c.temperature = t }
func (c *anthropicClient) Temperature() float64     { return c.temperature }

func (c *anthropicClient) ListModels(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, aiListTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	return fetchModelIDs(req)
}

func (c *anthropicClient) Complete(ctx context.Context, system string, messages []aiMessage, onToken func(string)) (string, TokenUsage, error) {
	ctx, cancel := context.WithTimeout(ctx, aiRequestTimeout)
	defer cancel()

	msgs := make([]map[string]string, len(messages))
	for i, m := range messages {
		msgs[i] = map[string]string{"role": m.Role, "content": m.Content}
	}

	body := map[string]any{
		"model":       c.model,
		"max_tokens":  c.maxTokens,
		"temperature": c.temperature,
		"system":      system,
		"messages":    msgs,
	}
	if onToken != nil {
		body["stream"] = true
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", TokenUsage{}, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/messages", bytes.NewReader(data))
	if err != nil {
		return "", TokenUsage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", TokenUsage{}, fmt.Errorf("anthropic API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", TokenUsage{}, fmt.Errorf("anthropic API %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	if onToken != nil {
		return c.parseStream(resp.Body, onToken)
	}
	return c.parseResponse(resp.Body)
}

func (c *anthropicClient) parseResponse(r io.Reader) (string, TokenUsage, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return "", TokenUsage{}, err
	}
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", TokenUsage{}, fmt.Errorf("parse anthropic response: %w", err)
	}
	if len(result.Content) == 0 {
		return "", TokenUsage{}, fmt.Errorf("empty response from anthropic")
	}
	return result.Content[0].Text, TokenUsage{
		InputTokens:  result.Usage.InputTokens,
		OutputTokens: result.Usage.OutputTokens,
	}, nil
}

func (c *anthropicClient) parseStream(r io.Reader, onToken func(string)) (string, TokenUsage, error) {
	var text strings.Builder
	var usage TokenUsage
	var stopReason string

	err := parseSSE(r, func(data string) error {
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
			json.Unmarshal([]byte(data), &e)
			usage.InputTokens = e.Message.Usage.InputTokens

		case "content_block_delta":
			var e struct {
				Delta struct {
					Text string `json:"text"`
				} `json:"delta"`
			}
			json.Unmarshal([]byte(data), &e)
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
			json.Unmarshal([]byte(data), &e)
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
			json.Unmarshal([]byte(data), &e)
			return fmt.Errorf("anthropic stream error: %s: %s", e.Error.Type, e.Error.Message)
		}
		return nil
	})

	if err != nil {
		return text.String(), usage, err
	}
	if text.Len() == 0 {
		return "", usage, fmt.Errorf("empty response from anthropic (model %s, stop_reason=%s)", c.model, stopReason)
	}
	return text.String(), usage, nil
}

// --- OpenAI-compatible (POST /v1/chat/completions) ---
// Covers: OpenAI, xAI, DeepSeek, Mistral

type openaiClient struct {
	apiKey      string
	model       string
	baseURL     string
	temperature float64
	maxTokens   int
}

func (c *openaiClient) SetModel(m string)       { c.model = m }
func (c *openaiClient) SetTemperature(t float64) { c.temperature = t }
func (c *openaiClient) Temperature() float64     { return c.temperature }

func (c *openaiClient) ListModels(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, aiListTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	return fetchModelIDs(req)
}

func (c *openaiClient) Complete(ctx context.Context, system string, messages []aiMessage, onToken func(string)) (string, TokenUsage, error) {
	ctx, cancel := context.WithTimeout(ctx, aiRequestTimeout)
	defer cancel()

	msgs := []map[string]string{{"role": "system", "content": system}}
	for _, m := range messages {
		msgs = append(msgs, map[string]string{"role": m.Role, "content": m.Content})
	}

	body := map[string]any{
		"model":       c.model,
		"messages":    msgs,
		"max_tokens":  c.maxTokens,
		"temperature": c.temperature,
	}
	if onToken != nil {
		body["stream"] = true
		body["stream_options"] = map[string]any{"include_usage": true}
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", TokenUsage{}, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/chat/completions", bytes.NewReader(data))
	if err != nil {
		return "", TokenUsage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", TokenUsage{}, fmt.Errorf("openai-compatible API (%s): %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", TokenUsage{}, fmt.Errorf("openai-compatible API %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	if onToken != nil {
		return c.parseStream(resp.Body, onToken)
	}
	return c.parseResponse(resp.Body)
}

func (c *openaiClient) parseResponse(r io.Reader) (string, TokenUsage, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return "", TokenUsage{}, err
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", TokenUsage{}, fmt.Errorf("parse response: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", TokenUsage{}, fmt.Errorf("empty response from %s", c.baseURL)
	}
	var usage TokenUsage
	if result.Usage != nil {
		usage.InputTokens = result.Usage.PromptTokens
		usage.OutputTokens = result.Usage.CompletionTokens
	}
	content := result.Choices[0].Message.Content
	if content == "" && result.Choices[0].Message.ReasoningContent != "" {
		return "", usage, fmt.Errorf("model %s produced only reasoning, no answer (finish_reason=%s); try raising ai.max-tokens in ~/.xmc config or use a non-reasoning model",
			c.model, result.Choices[0].FinishReason)
	}
	return content, usage, nil
}

func (c *openaiClient) parseStream(r io.Reader, onToken func(string)) (string, TokenUsage, error) {
	var text strings.Builder
	var reasoning strings.Builder
	var usage TokenUsage
	var finishReason string

	err := parseSSE(r, func(data string) error {
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"` // DeepSeek reasoning models
					Reasoning        string `json:"reasoning"`         // some proxies use this field
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
			return fmt.Errorf("%s stream error: %s", c.baseURL, chunk.Error.Message)
		}
		if len(chunk.Choices) > 0 {
			d := chunk.Choices[0].Delta
			// Reasoning models stream chain-of-thought in reasoning_content
			// (or reasoning); show it live but don't include in the returned text.
			if rc := d.ReasoningContent; rc != "" {
				reasoning.WriteString(rc)
				onToken(rc)
			} else if rc := d.Reasoning; rc != "" {
				reasoning.WriteString(rc)
				onToken(rc)
			}
			if d.Content != "" {
				text.WriteString(d.Content)
				onToken(d.Content)
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
		if reasoning.Len() > 0 {
			return "", usage, fmt.Errorf("model %s produced only reasoning (%d tokens), no answer (finish_reason=%s); try raising ai.max-tokens in ~/.xmc config or use a non-reasoning model",
				c.model, usage.OutputTokens, finishReason)
		}
		return "", usage, fmt.Errorf("empty response from %s (model %s, finish_reason=%s)", c.baseURL, c.model, finishReason)
	}
	return text.String(), usage, nil
}

// --- Google Gemini (POST :generateContent / :streamGenerateContent) ---

type geminiClient struct {
	apiKey      string
	model       string
	baseURL     string
	temperature float64
	maxTokens   int
}

func (c *geminiClient) SetModel(m string)       { c.model = m }
func (c *geminiClient) SetTemperature(t float64) { c.temperature = t }
func (c *geminiClient) Temperature() float64     { return c.temperature }

func (c *geminiClient) ListModels(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, aiListTimeout)
	defer cancel()
	url := fmt.Sprintf("%s/v1beta/models?key=%s", c.baseURL, c.apiKey)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("models API %d: %s", resp.StatusCode, truncate(string(b), 200))
	}
	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(result.Models))
	for _, m := range result.Models {
		id := strings.TrimPrefix(m.Name, "models/")
		if id != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (c *geminiClient) Complete(ctx context.Context, system string, messages []aiMessage, onToken func(string)) (string, TokenUsage, error) {
	ctx, cancel := context.WithTimeout(ctx, aiRequestTimeout)
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

	body := map[string]any{
		"system_instruction": map[string]any{
			"parts": []map[string]string{
				{"text": system},
			},
		},
		"generationConfig": map[string]any{
			"temperature":     c.temperature,
			"maxOutputTokens": c.maxTokens,
		},
		"contents": contents,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return "", TokenUsage{}, err
	}

	var url string
	if onToken != nil {
		url = fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse&key=%s", c.baseURL, c.model, c.apiKey)
	} else {
		url = fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", c.baseURL, c.model, c.apiKey)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return "", TokenUsage{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", TokenUsage{}, fmt.Errorf("gemini API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", TokenUsage{}, fmt.Errorf("gemini API %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	if onToken != nil {
		return c.parseStream(resp.Body, onToken)
	}
	return c.parseResponse(resp.Body)
}

func (c *geminiClient) parseResponse(r io.Reader) (string, TokenUsage, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return "", TokenUsage{}, err
	}
	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata *struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", TokenUsage{}, fmt.Errorf("parse gemini response: %w", err)
	}
	if len(result.Candidates) == 0 || len(result.Candidates[0].Content.Parts) == 0 {
		return "", TokenUsage{}, fmt.Errorf("empty response from gemini")
	}
	var usage TokenUsage
	if result.UsageMetadata != nil {
		usage.InputTokens = result.UsageMetadata.PromptTokenCount
		usage.OutputTokens = result.UsageMetadata.CandidatesTokenCount
	}
	return result.Candidates[0].Content.Parts[0].Text, usage, nil
}

func (c *geminiClient) parseStream(r io.Reader, onToken func(string)) (string, TokenUsage, error) {
	var text strings.Builder
	var usage TokenUsage
	var finishReason string

	err := parseSSE(r, func(data string) error {
		var chunk struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
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
			return fmt.Errorf("gemini stream error: %s", chunk.Error.Message)
		}
		if len(chunk.Candidates) > 0 {
			if len(chunk.Candidates[0].Content.Parts) > 0 {
				t := chunk.Candidates[0].Content.Parts[0].Text
				if t != "" {
					text.WriteString(t)
					onToken(t)
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
		return "", usage, fmt.Errorf("empty response from gemini (model %s, finish_reason=%s)", c.model, finishReason)
	}
	return text.String(), usage, nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
