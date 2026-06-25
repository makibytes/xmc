package cmd

import (
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

func TestResolveProvider_NoKey(t *testing.T) {
	cfg := &xmcConfig{}
	getenv := func(k string) string { return "" }

	_, err := resolveProvider(cfg, getenv)
	if err == nil {
		t.Fatal("expected error when no API key is set")
	}
}

func TestResolveProvider_GeminiAlternateKey(t *testing.T) {
	cfg := &xmcConfig{}
	env := map[string]string{"GOOGLE_API_KEY": "goog-key"}
	getenv := func(k string) string { return env[k] }

	spec, err := resolveProvider(cfg, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if spec.name != "gemini" {
		t.Errorf("expected gemini, got %q", spec.name)
	}
}

func TestResolveProvider_OpenCode(t *testing.T) {
	cfg := &xmcConfig{}
	env := map[string]string{"OPENCODE_API_KEY": "oc-key"}
	getenv := func(k string) string { return env[k] }

	spec, err := resolveProvider(cfg, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if spec.name != "opencode" {
		t.Errorf("expected opencode, got %q", spec.name)
	}
	if spec.baseURL != "https://opencode.ai/zen" {
		t.Errorf("baseURL = %q", spec.baseURL)
	}
}

func TestResolveProvider_OpenCodeZenKey(t *testing.T) {
	cfg := &xmcConfig{AI: aiConfig{Provider: "opencode"}}
	env := map[string]string{"OPENCODE_ZEN_API_KEY": "zen-key"}
	getenv := func(k string) string { return env[k] }

	spec, err := resolveProvider(cfg, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if spec.name != "opencode" {
		t.Errorf("expected opencode, got %q", spec.name)
	}
	if spec.apiKey != "zen-key" {
		t.Errorf("apiKey = %q, want zen-key", spec.apiKey)
	}
}

func TestResolveProvider_UnknownProvider(t *testing.T) {
	cfg := &xmcConfig{AI: aiConfig{Provider: "bogus"}}
	getenv := func(k string) string { return "some-key" }

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
		// Unsupported units
		{"3h", 0, false, true},
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
