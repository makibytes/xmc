package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type xmcConfig struct {
	Connection connectionConfig  `yaml:"connection"`
	AI         aiConfig          `yaml:"ai"`
	Aliases    map[string]string `yaml:"aliases"`
}

type connectionConfig struct {
	Server   string `yaml:"server"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
}

type aiConfig struct {
	Provider           string `yaml:"provider"`
	Model              string `yaml:"model"`
	MaxTokens          int    `yaml:"max-tokens"`            // max output tokens per AI call (default: 4096)
	AutoUpdateObjects  *bool  `yaml:"auto-update-objects"`   // refresh sidebar on create/delete/bind (default: true)
	AutoUpdateMessages *bool  `yaml:"auto-update-messages"`  // refresh sidebar on send/publish/receive/purge (default: true)
	RefreshInterval    string `yaml:"refresh-interval"`      // periodic sidebar refresh interval (e.g. "5s", "3m", "off"; default: "5s")
}

// autoUpdateObjectsEnabled returns true unless the config explicitly disables it.
func (c aiConfig) autoUpdateObjectsEnabled() bool {
	return c.AutoUpdateObjects == nil || *c.AutoUpdateObjects
}

// autoUpdateMessagesEnabled returns true unless the config explicitly disables it.
func (c aiConfig) autoUpdateMessagesEnabled() bool {
	return c.AutoUpdateMessages == nil || *c.AutoUpdateMessages
}

// refreshIntervalDuration returns the configured refresh period and whether
// periodic refresh is enabled. An empty value yields the default (5s, true);
// an invalid stored value is treated the same way (lenient on config).
func (c aiConfig) refreshIntervalDuration() (time.Duration, bool) {
	if c.RefreshInterval == "" {
		return baseRefreshPeriod, true
	}
	d, enabled, err := parseRefreshInterval(c.RefreshInterval)
	if err != nil {
		return baseRefreshPeriod, true
	}
	return d, enabled
}

// parseRefreshInterval parses a user-provided refresh interval string.
// Accepted forms: "off"/"none" (disable), bare number "3" (seconds),
// "<n>s" (seconds), "<n>m" (minutes). Returns an error for values < 1s
// or unparseable input.
func parseRefreshInterval(s string) (time.Duration, bool, error) {
	s = strings.TrimSpace(s)
	low := strings.ToLower(s)
	if low == "off" || low == "none" {
		return 0, false, nil
	}

	// Try bare number (seconds).
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		d := time.Duration(v * float64(time.Second))
		if d < minRefreshInterval {
			return 0, false, fmt.Errorf("minimum refresh interval is %s", minRefreshInterval)
		}
		return d, true, nil
	}

	// Try number + unit suffix (s or m only).
	if len(s) > 1 {
		unit := s[len(s)-1]
		numStr := s[:len(s)-1]
		v, err := strconv.ParseFloat(numStr, 64)
		if err == nil {
			var d time.Duration
			switch unit {
			case 's', 'S':
				d = time.Duration(v * float64(time.Second))
			case 'm', 'M':
				d = time.Duration(v * float64(time.Minute))
			default:
				return 0, false, fmt.Errorf("unsupported unit %q (use s or m)", string(unit))
			}
			if d < minRefreshInterval {
				return 0, false, fmt.Errorf("minimum refresh interval is %s", minRefreshInterval)
			}
			return d, true, nil
		}
	}

	return 0, false, fmt.Errorf("cannot parse %q as a refresh interval (e.g. 3, 3s, 3m, off)", s)
}

// formatRefreshInterval produces a canonical string for persisting a refresh
// interval: "3m" when minute-aligned, "5s" otherwise, "off" when disabled.
func formatRefreshInterval(d time.Duration, enabled bool) string {
	if !enabled {
		return "off"
	}
	if d%time.Minute == 0 && d >= time.Minute {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%gs", d.Seconds())
}

// saveRefreshInterval persists the refresh-interval value to the config file.
func saveRefreshInterval(value string) error {
	if err := ensureXMCDir(); err != nil {
		return err
	}
	path, err := configFilePath()
	if err != nil {
		return err
	}

	var doc yaml.Node
	data, readErr := os.ReadFile(path)
	if readErr == nil && len(data) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{
			{Kind: yaml.MappingNode},
		}}
	}
	root := doc.Content[0]
	aiNode := yamlFindOrCreateMapping(root, "ai")
	yamlSetScalar(aiNode, "refresh-interval", value)

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

func loadConfig() (*xmcConfig, error) {
	path, err := configFilePath()
	if err != nil {
		return &xmcConfig{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &xmcConfig{}, nil
		}
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg xmcConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return &cfg, nil
}

// defaultMaxTokens is the output-token budget for AI calls. Reasoning models
// (DeepSeek, o1, etc.) need headroom for chain-of-thought before the answer,
// so we set this generously — non-reasoning models simply stop at finish_reason=stop.
const defaultMaxTokens = 4096

type providerSpec struct {
	name      string
	apiKey    string
	baseURL   string
	model     string
	maxTokens int
}

type providerDef struct {
	name       string
	envKeys    []string
	baseURL    string
	defaultModel string
}

var providerOrder = []providerDef{
	{"anthropic", []string{"ANTHROPIC_API_KEY"}, "https://api.anthropic.com", "claude-sonnet-4-6"},
	{"openai", []string{"OPENAI_API_KEY"}, "https://api.openai.com", "gpt-4o"},
	{"gemini", []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}, "https://generativelanguage.googleapis.com", "gemini-2.0-flash"},
	{"xai", []string{"XAI_API_KEY"}, "https://api.x.ai", "grok-2-latest"},
	{"deepseek", []string{"DEEPSEEK_API_KEY"}, "https://api.deepseek.com", "deepseek-chat"},
	{"mistral", []string{"MISTRAL_API_KEY"}, "https://api.mistral.ai", "mistral-large-latest"},
	{"opencode", []string{"OPENCODE_API_KEY", "OPENCODE_ZEN_API_KEY"}, "https://opencode.ai/zen", "mimo-v2.5-free"},
}

type envLookup func(string) string

func resolveProvider(cfg *xmcConfig, getenv envLookup) (providerSpec, error) {
	maxTok := cfg.AI.MaxTokens
	if maxTok <= 0 {
		maxTok = defaultMaxTokens
	}

	if cfg.AI.Provider != "" {
		for _, def := range providerOrder {
			if !strings.EqualFold(def.name, cfg.AI.Provider) {
				continue
			}
			key := findKey(def.envKeys, getenv)
			if key == "" {
				return providerSpec{}, fmt.Errorf("provider %q selected in config but no API key found (set %s)",
					def.name, strings.Join(def.envKeys, " or "))
			}
			model := cfg.AI.Model
			if model == "" {
				model = def.defaultModel
			}
			return providerSpec{name: def.name, apiKey: key, baseURL: def.baseURL, model: model, maxTokens: maxTok}, nil
		}
		return providerSpec{}, fmt.Errorf("unknown AI provider %q (supported: anthropic, openai, gemini, xai, deepseek, mistral, opencode)", cfg.AI.Provider)
	}

	for _, def := range providerOrder {
		key := findKey(def.envKeys, getenv)
		if key == "" {
			continue
		}
		model := cfg.AI.Model
		if model == "" {
			model = def.defaultModel
		}
		return providerSpec{name: def.name, apiKey: key, baseURL: def.baseURL, model: model, maxTokens: maxTok}, nil
	}

	return providerSpec{}, fmt.Errorf("no AI API key found; set one of: ANTHROPIC_API_KEY, OPENAI_API_KEY, GEMINI_API_KEY, XAI_API_KEY, DEEPSEEK_API_KEY, MISTRAL_API_KEY, OPENCODE_API_KEY")
}

func findKey(envKeys []string, getenv envLookup) string {
	for _, k := range envKeys {
		if v := getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// saveAIModel writes the model name to the config file under ai.model,
// preserving all other keys and comments via yaml.Node round-trip.
func saveAIModel(model string) error {
	if err := ensureXMCDir(); err != nil {
		return err
	}
	path, err := configFilePath()
	if err != nil {
		return err
	}

	var doc yaml.Node
	data, readErr := os.ReadFile(path)
	if readErr == nil && len(data) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	}

	// Ensure we have a document → mapping structure.
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{
			{Kind: yaml.MappingNode},
		}}
	}
	root := doc.Content[0]

	// Find or create the "ai" mapping.
	aiNode := yamlFindOrCreateMapping(root, "ai")
	// Set "model" inside the "ai" mapping.
	yamlSetScalar(aiNode, "model", model)

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

// yamlFindOrCreateMapping finds a mapping child by key, or creates one.
func yamlFindOrCreateMapping(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: key}
	valNode := &yaml.Node{Kind: yaml.MappingNode}
	mapping.Content = append(mapping.Content, keyNode, valNode)
	return valNode
}

// yamlSetScalar sets a scalar key=value inside a mapping node, creating or updating.
func yamlSetScalar(mapping *yaml.Node, key, value string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1].Value = value
			mapping.Content[i+1].Kind = yaml.ScalarNode
			mapping.Content[i+1].Tag = ""
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Value: value},
	)
}
