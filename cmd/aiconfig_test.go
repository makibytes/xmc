package cmd

import (
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestLoadConfig_MissingFile(t *testing.T) {
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() error on missing file: %v", err)
	}
	if cfg.Connection.Server != "" {
		t.Errorf("expected empty server, got %q", cfg.Connection.Server)
	}
}

func TestLoadConfig_ParseYAML(t *testing.T) {
	data := []byte(`connection:
  server: amqp://localhost:5672
  user: admin
  password: secret
ai:
  provider: openai
  model: gpt-4o-mini
`)
	var cfg xmcConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Connection.Server != "amqp://localhost:5672" {
		t.Errorf("server = %q", cfg.Connection.Server)
	}
	if cfg.Connection.User != "admin" {
		t.Errorf("user = %q", cfg.Connection.User)
	}
	if cfg.AI.Provider != "openai" {
		t.Errorf("provider = %q", cfg.AI.Provider)
	}
	if cfg.AI.Model != "gpt-4o-mini" {
		t.Errorf("model = %q", cfg.AI.Model)
	}
}

func TestResolveProvider_Precedence(t *testing.T) {
	cfg := &xmcConfig{}
	env := map[string]string{
		"ANTHROPIC_API_KEY": "ant-key",
		"OPENAI_API_KEY":    "oai-key",
	}
	getenv := func(k string) string { return env[k] }

	spec, err := resolveProvider(cfg, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if spec.name != "anthropic" {
		t.Errorf("expected anthropic (first in order), got %q", spec.name)
	}
	if spec.apiKey != "ant-key" {
		t.Errorf("apiKey = %q", spec.apiKey)
	}
}

func TestResolveProvider_ConfigForces(t *testing.T) {
	cfg := &xmcConfig{AI: aiConfig{Provider: "openai"}}
	env := map[string]string{
		"ANTHROPIC_API_KEY": "ant-key",
		"OPENAI_API_KEY":    "oai-key",
	}
	getenv := func(k string) string { return env[k] }

	spec, err := resolveProvider(cfg, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if spec.name != "openai" {
		t.Errorf("expected openai (forced), got %q", spec.name)
	}
}

func TestResolveProvider_ConfigModelOverride(t *testing.T) {
	cfg := &xmcConfig{AI: aiConfig{Model: "custom-model"}}
	env := map[string]string{"OPENAI_API_KEY": "key"}
	getenv := func(k string) string { return env[k] }

	spec, err := resolveProvider(cfg, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if spec.model != "custom-model" {
		t.Errorf("model = %q, want custom-model", spec.model)
	}
}

func TestResolveProvider_DefaultModels(t *testing.T) {
	wantModels := map[string]string{
		"anthropic":  "claude-sonnet-5",
		"openai":     "gpt-5.6-luna",
		"gemini":     "gemini-3.8-flash",
		"xai":        "grok-4.5",
		"deepseek":   "deepseek-v4-flash",
		"mistral":    "mistral-small-latest",
		"openrouter": "openrouter/free",
		"opencode":   "deepseek-v4.1-flash",
		"kilo":       "kilo-auto/free",
	}

	for _, def := range providerOrder {
		t.Run(def.name, func(t *testing.T) {
			cfg := &xmcConfig{AI: aiConfig{Provider: def.name}}
			getenv := func(key string) string {
				if slices.Contains(def.envKeys, key) {
					return "test-key"
				}
				return ""
			}

			spec, err := resolveProvider(cfg, getenv)
			if err != nil {
				t.Fatal(err)
			}
			if spec.model != wantModels[def.name] {
				t.Errorf("model = %q, want %q", spec.model, wantModels[def.name])
			}
		})
	}
}

// With no key at all, the Kilo gateway's free router is used: no key, a notice
// about its limits.
func TestResolveProvider_NoKeyFallsBackToKilo(t *testing.T) {
	spec, err := resolveProvider(&xmcConfig{}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if spec.name != "kilo" || spec.apiKey != "" || spec.model != "kilo-auto/free" {
		t.Errorf("spec = %+v, want keyless kilo with kilo-auto/free", spec)
	}
	// The client appends /v1/chat/completions; Kilo serves that path too.
	if spec.baseURL != "https://api.kilo.ai/api/gateway" {
		t.Errorf("baseURL = %q", spec.baseURL)
	}
	if !strings.Contains(spec.notice, "200 requests/hour") {
		t.Errorf("notice = %q, want the rate-limit notice", spec.notice)
	}
}

// A KILO_API_KEY is sent along (lifting the IP limit) and silences the
// no-key notice.
func TestResolveProvider_KiloWithKey(t *testing.T) {
	env := map[string]string{"KILO_API_KEY": "k"}
	spec, err := resolveProvider(&xmcConfig{}, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if spec.name != "kilo" || spec.apiKey != "k" || spec.keyEnv != "KILO_API_KEY" || spec.notice != "" {
		t.Errorf("spec = %+v", spec)
	}
}

func TestResolveProvider_KiloPinnedNeedsNoKey(t *testing.T) {
	cfg := &xmcConfig{AI: aiConfig{Provider: "kilo", Model: "kilo-auto/efficient"}}
	spec, err := resolveProvider(cfg, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if spec.name != "kilo" || spec.model != "kilo-auto/efficient" {
		t.Errorf("spec = %+v", spec)
	}
}

// OpenRouter and OpenCode lead the auto-detection order, ahead of every
// first-party key; Kilo is last.
func TestResolveProvider_AutoDetectOrder(t *testing.T) {
	names := providerNames()
	want := []string{"openrouter", "opencode"}
	if !slices.Equal(names[:2], want) || names[len(names)-1] != "kilo" {
		t.Errorf("provider order = %v, want %v first and kilo last", names, want)
	}

	env := map[string]string{
		"ANTHROPIC_API_KEY": "a", "GEMINI_API_KEY": "g", "OPENCODE_API_KEY": "oc", "OPENROUTER_API_KEY": "or",
	}
	getenv := func(k string) string { return env[k] }
	spec, err := resolveProvider(&xmcConfig{}, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if spec.name != "openrouter" {
		t.Errorf("with every key set got %q, want openrouter", spec.name)
	}
	delete(env, "OPENROUTER_API_KEY")
	if spec, _ = resolveProvider(&xmcConfig{}, getenv); spec.name != "opencode" {
		t.Errorf("without OpenRouter got %q, want opencode", spec.name)
	}
	delete(env, "OPENCODE_API_KEY")
	if spec, _ = resolveProvider(&xmcConfig{}, getenv); spec.name != "anthropic" {
		t.Errorf("first-party keys: got %q, want anthropic", spec.name)
	}
}

func TestResolveProvider_OpenCode(t *testing.T) {
	env := map[string]string{"OPENCODE_API_KEY": "oc-key"}
	spec, err := resolveProvider(&xmcConfig{}, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if spec.name != "opencode" || spec.apiKey != "oc-key" || spec.keyEnv != "OPENCODE_API_KEY" {
		t.Errorf("spec = %+v", spec)
	}
	// The client appends /v1/chat/completions, which must land on OpenCode's
	// /inference/openai/v1/chat/completions.
	if spec.baseURL != "https://opencode.ai/inference/openai" {
		t.Errorf("baseURL = %q", spec.baseURL)
	}
	// The free models only work inside the OpenCode app, so the default is the
	// paid sibling.
	if spec.model != "deepseek-v4.1-flash" {
		t.Errorf("model = %q", spec.model)
	}
	// The retired Zen variable is not read.
	env = map[string]string{"OPENCODE_ZEN_API_KEY": "old"}
	if spec, _ = resolveProvider(&xmcConfig{}, func(k string) string { return env[k] }); spec.name == "opencode" {
		t.Error("OPENCODE_ZEN_API_KEY must not select opencode")
	}
}

// GOOGLE_API_KEY is no longer an alias for Gemini, and says so instead of
// being silently ignored.
func TestResolveProvider_GoogleAPIKeyNoLongerRead(t *testing.T) {
	env := map[string]string{"GOOGLE_API_KEY": "goog-key"}
	getenv := func(k string) string { return env[k] }

	spec, err := resolveProvider(&xmcConfig{}, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if spec.name == "gemini" {
		t.Error("GOOGLE_API_KEY must not select gemini")
	}
	if !strings.Contains(spec.notice, "GOOGLE_API_KEY is no longer read") {
		t.Errorf("notice = %q, want the GOOGLE_API_KEY explanation", spec.notice)
	}

	_, err = resolveProvider(&xmcConfig{AI: aiConfig{Provider: "gemini"}}, getenv)
	if err == nil || !strings.Contains(err.Error(), "GEMINI_API_KEY") || !strings.Contains(err.Error(), "GOOGLE_API_KEY is no longer read") {
		t.Errorf("err = %v, want the GEMINI_API_KEY / GOOGLE_API_KEY explanation", err)
	}

	env["GEMINI_API_KEY"] = "gem"
	if spec, _ = resolveProvider(&xmcConfig{}, getenv); spec.name != "gemini" || spec.apiKey != "gem" || spec.notice != "" {
		t.Errorf("spec = %+v, want plain gemini", spec)
	}
}

// A model saved before /model stored the provider next to it must not be sent
// to a gateway picked automatically (it may be another vendor's name); with
// the provider pinned, or on a first-party provider, it applies.
func TestResolveProvider_LegacyModelIgnoredOnGateways(t *testing.T) {
	cfg := &xmcConfig{AI: aiConfig{Model: "gemini-3.8-flash"}}
	env := map[string]string{"OPENCODE_API_KEY": "k"}
	getenv := func(k string) string { return env[k] }

	spec, err := resolveProvider(cfg, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if spec.model != "deepseek-v4.1-flash" || !strings.Contains(spec.notice, `ai.model "gemini-3.8-flash" is ignored`) {
		t.Errorf("spec = %+v, want the default model plus an explanation", spec)
	}

	cfg.AI.Provider = "opencode"
	if spec, _ = resolveProvider(cfg, getenv); spec.model != "gemini-3.8-flash" {
		t.Errorf("pinned provider: model = %q, want the configured one", spec.model)
	}

	cfg.AI.Provider = ""
	env = map[string]string{"GEMINI_API_KEY": "k"}
	if spec, _ = resolveProvider(cfg, getenv); spec.model != "gemini-3.8-flash" || spec.notice != "" {
		t.Errorf("first-party provider: spec = %+v, want the configured model", spec)
	}
}

func TestResolveProvider_UnknownProvider(t *testing.T) {
	cfg := &xmcConfig{AI: aiConfig{Provider: "bogus"}}
	getenv := func(_ string) string { return "some-key" }

	_, err := resolveProvider(cfg, getenv)
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestAutoUpdateDefaults(t *testing.T) {
	// Default (nil pointers) should be enabled.
	cfg := aiConfig{}
	if !cfg.autoUpdateObjectsEnabled() {
		t.Error("auto-update-objects should default to enabled")
	}
	if !cfg.autoUpdateMessagesEnabled() {
		t.Error("auto-update-messages should default to enabled")
	}

	// Explicitly disabled.
	f := false
	cfg.AutoUpdateObjects = &f
	cfg.AutoUpdateMessages = &f
	if cfg.autoUpdateObjectsEnabled() {
		t.Error("auto-update-objects should be disabled")
	}
	if cfg.autoUpdateMessagesEnabled() {
		t.Error("auto-update-messages should be disabled")
	}

	// Explicitly enabled.
	tr := true
	cfg.AutoUpdateObjects = &tr
	cfg.AutoUpdateMessages = &tr
	if !cfg.autoUpdateObjectsEnabled() {
		t.Error("auto-update-objects should be enabled")
	}
	if !cfg.autoUpdateMessagesEnabled() {
		t.Error("auto-update-messages should be enabled")
	}
}

func TestParseRefreshInterval(t *testing.T) {
	tests := []struct {
		input   string
		dur     time.Duration
		enabled bool
		wantErr bool
	}{
		{"off", 0, false, false},
		{"OFF", 0, false, false},
		{"none", 0, false, false},
		{"3", 3 * time.Second, true, false},
		{"3s", 3 * time.Second, true, false},
		{"3S", 3 * time.Second, true, false},
		{"10s", 10 * time.Second, true, false},
		{"1.5s", 1500 * time.Millisecond, true, false},
		{"3m", 3 * time.Minute, true, false},
		{"1m", 1 * time.Minute, true, false},
		{"1", 1 * time.Second, true, false},
		{"  5  ", 5 * time.Second, true, false},
		// Below minimum
		{"0.5s", 0, false, true},
		{"0.5", 0, false, true},
		{"0", 0, false, true},
		// Hours accepted via time.ParseDuration
		{"1h", time.Hour, true, false},
		{"3h", 3 * time.Hour, true, false},
		// Garbage
		{"abc", 0, false, true},
		{"", 0, false, true},
	}
	for _, tt := range tests {
		d, enabled, err := parseRefreshInterval(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseRefreshInterval(%q): err=%v, wantErr=%v", tt.input, err, tt.wantErr)
			continue
		}
		if err != nil {
			continue
		}
		if d != tt.dur {
			t.Errorf("parseRefreshInterval(%q): dur=%v, want %v", tt.input, d, tt.dur)
		}
		if enabled != tt.enabled {
			t.Errorf("parseRefreshInterval(%q): enabled=%v, want %v", tt.input, enabled, tt.enabled)
		}
	}
}

func TestFormatRefreshInterval(t *testing.T) {
	tests := []struct {
		dur     time.Duration
		enabled bool
		want    string
	}{
		{0, false, "off"},
		{5 * time.Second, true, "5s"},
		{3 * time.Second, true, "3s"},
		{1500 * time.Millisecond, true, "1.5s"},
		{1 * time.Minute, true, "1m"},
		{3 * time.Minute, true, "3m"},
	}
	for _, tt := range tests {
		got := formatRefreshInterval(tt.dur, tt.enabled)
		if got != tt.want {
			t.Errorf("formatRefreshInterval(%v, %v) = %q, want %q", tt.dur, tt.enabled, got, tt.want)
		}
	}
}

func TestRefreshIntervalDuration(t *testing.T) {
	// Empty → default 5s, enabled.
	cfg := aiConfig{}
	d, on := cfg.refreshIntervalDuration()
	if d != baseRefreshPeriod || !on {
		t.Errorf("empty: got (%v, %v), want (%v, true)", d, on, baseRefreshPeriod)
	}

	// "off" → disabled.
	cfg.RefreshInterval = "off"
	d, on = cfg.refreshIntervalDuration()
	if on {
		t.Errorf("off: expected disabled, got (%v, %v)", d, on)
	}

	// "3s" → 3s, enabled.
	cfg.RefreshInterval = "3s"
	d, on = cfg.refreshIntervalDuration()
	if d != 3*time.Second || !on {
		t.Errorf("3s: got (%v, %v), want (3s, true)", d, on)
	}

	// Invalid stored value → lenient fallback to default.
	cfg.RefreshInterval = "garbage"
	d, on = cfg.refreshIntervalDuration()
	if d != baseRefreshPeriod || !on {
		t.Errorf("garbage: got (%v, %v), want (%v, true)", d, on, baseRefreshPeriod)
	}
}

func TestRefreshIntervalYAMLParsing(t *testing.T) {
	data := []byte(`ai:
  refresh-interval: "10s"
`)
	var cfg xmcConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.AI.RefreshInterval != "10s" {
		t.Errorf("refresh-interval = %q, want 10s", cfg.AI.RefreshInterval)
	}
	d, on := cfg.AI.refreshIntervalDuration()
	if d != 10*time.Second || !on {
		t.Errorf("got (%v, %v), want (10s, true)", d, on)
	}
}

func TestRequestTimeoutDuration(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		// Empty → 0 (caller uses defaultRequestTimeout).
		{"", 0},
		// Valid durations.
		{"120s", 120 * time.Second},
		{"2m", 2 * time.Minute},
		{"30s", 30 * time.Second},
		// "off", garbage, "0" → 0 (lenient fallback).
		{"off", 0},
		{"garbage", 0},
		{"0", 0},
	}
	for _, tt := range tests {
		cfg := aiConfig{RequestTimeout: tt.input}
		got := cfg.requestTimeoutDuration()
		if got != tt.want {
			t.Errorf("requestTimeoutDuration(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestResolveProvider_RequestTimeout(t *testing.T) {
	cfg := &xmcConfig{AI: aiConfig{RequestTimeout: "90s"}}
	env := map[string]string{"OPENAI_API_KEY": "key"}
	getenv := func(k string) string { return env[k] }

	spec, err := resolveProvider(cfg, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if spec.requestTimeout != 90*time.Second {
		t.Errorf("requestTimeout = %v, want 90s", spec.requestTimeout)
	}
}

func TestAutoUpdateYAMLParsing(t *testing.T) {
	data := []byte(`ai:
  auto-update-objects: false
  auto-update-messages: true
`)
	var cfg xmcConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.AI.autoUpdateObjectsEnabled() {
		t.Error("auto-update-objects should be disabled after YAML parse")
	}
	if !cfg.AI.autoUpdateMessagesEnabled() {
		t.Error("auto-update-messages should be enabled after YAML parse")
	}
}

func TestParseMetadataFormat(t *testing.T) {
	tests := []struct {
		in   string
		want metadataFormat
	}{
		{"", metadataFormatYAML},
		{"yaml", metadataFormatYAML},
		{"YAML", metadataFormatYAML},
		{"json", metadataFormatJSON},
		{"JSON", metadataFormatJSON},
		{"unknown", metadataFormatYAML},
	}
	for _, tt := range tests {
		if got := parseMetadataFormat(tt.in); got != tt.want {
			t.Errorf("parseMetadataFormat(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestMetadataFormatYAMLParsing(t *testing.T) {
	data := []byte(`ai:
  metadata-format: "json"
`)
	var cfg xmcConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if got := cfg.AI.metadataFormat(); got != metadataFormatJSON {
		t.Errorf("metadataFormat = %q, want %q", got, metadataFormatJSON)
	}
}

func TestSaveMetadataFormat(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", dir)
	}

	if err := saveMetadataFormat(metadataFormatJSON); err != nil {
		t.Fatalf("saveMetadataFormat(json): %v", err)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig after save: %v", err)
	}
	if got := cfg.AI.metadataFormat(); got != metadataFormatJSON {
		t.Fatalf("metadata format after save = %q, want %q", got, metadataFormatJSON)
	}

	if err := saveMetadataFormat(metadataFormatYAML); err != nil {
		t.Fatalf("saveMetadataFormat(yaml): %v", err)
	}
	cfg, err = loadConfig()
	if err != nil {
		t.Fatalf("loadConfig after second save: %v", err)
	}
	if got := cfg.AI.metadataFormat(); got != metadataFormatYAML {
		t.Fatalf("metadata format after second save = %q, want %q", got, metadataFormatYAML)
	}
}

func TestResolveProvider_OpenRouter(t *testing.T) {
	env := map[string]string{"OPENROUTER_API_KEY": "or-key"}
	spec, err := resolveProvider(&xmcConfig{}, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if spec.name != "openrouter" || spec.apiKey != "or-key" || spec.keyEnv != "OPENROUTER_API_KEY" {
		t.Errorf("spec = %+v", spec)
	}
	// The client appends /v1/chat/completions, which must land on
	// OpenRouter's /api/v1/chat/completions.
	if spec.baseURL != "https://openrouter.ai/api" {
		t.Errorf("baseURL = %q", spec.baseURL)
	}
	if spec.model != "openrouter/free" {
		t.Errorf("model = %q, want the free router", spec.model)
	}
}

// /model stores the provider with the model, keeping the rest of the file.
func TestSaveAIModel_StoresProviderWithModel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", dir)
	}
	if err := saveAIEffort(effortHigh); err != nil {
		t.Fatal(err)
	}
	if err := saveAIModel("opencode", "deepseek-v4.1-flash"); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AI.Provider != "opencode" || cfg.AI.Model != "deepseek-v4.1-flash" || cfg.AI.Effort != "high" {
		t.Errorf("config = %+v, want provider, model and the earlier effort", cfg.AI)
	}
}
