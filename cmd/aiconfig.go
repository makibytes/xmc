package cmd

import (
	"errors"
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
	Effort             string `yaml:"effort"`               // reasoning effort ("low"|"medium"|"high"; default: "low")
	MaxTokens          int    `yaml:"max-tokens"`           // max output tokens per AI call (default: 4096)
	AutoUpdateObjects  *bool  `yaml:"auto-update-objects"`  // refresh sidebar on create/delete/bind (default: true)
	AutoUpdateMessages *bool  `yaml:"auto-update-messages"` // refresh sidebar on send/publish/receive/purge (default: true)
	RefreshInterval    string `yaml:"refresh-interval"`     // periodic sidebar refresh interval (e.g. "5s", "3m", "off"; default: "5s")
	RequestTimeout     string `yaml:"request-timeout"`      // max time for a single AI API call (e.g. "120s", "2m"; default: "120s")
	MetadataFormat     string `yaml:"metadata-format"`      // AI-shell metadata peek format ("yaml"|"json"; default: "yaml")
}

type metadataFormat string

const (
	metadataFormatYAML metadataFormat = "yaml"
	metadataFormatJSON metadataFormat = "json"
)

func parseMetadataFormat(s string) metadataFormat {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case string(metadataFormatJSON):
		return metadataFormatJSON
	default:
		return metadataFormatYAML
	}
}

func (c aiConfig) metadataFormat() metadataFormat {
	return parseMetadataFormat(c.MetadataFormat)
}

// requestTimeoutDuration returns the configured AI request timeout. An empty
// or invalid value yields 0, which clients read as "use defaultRequestTimeout".
func (c aiConfig) requestTimeoutDuration() time.Duration {
	d, _, err := parseRefreshInterval(c.RequestTimeout) // reuse the same duration parser
	if c.RequestTimeout == "" || err != nil || d <= 0 {
		return 0
	}
	return d
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
// or any duration accepted by time.ParseDuration ("5s", "3m", "1h", etc.).
// Returns an error for values < 1s or unparseable input.
func parseRefreshInterval(s string) (time.Duration, bool, error) {
	s = strings.TrimSpace(s)
	if low := strings.ToLower(s); low == "off" || low == "none" {
		return 0, false, nil
	}

	var d time.Duration
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		d = time.Duration(v * float64(time.Second))
	} else if pd, err := time.ParseDuration(strings.ToLower(s)); err == nil {
		d = pd
	} else {
		return 0, false, fmt.Errorf("cannot parse %q as a refresh interval (e.g. 3, 5s, 3m, off)", s)
	}
	if d < minRefreshInterval {
		return 0, false, fmt.Errorf("minimum refresh interval is %s", minRefreshInterval)
	}
	return d, true, nil
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
func saveRefreshInterval(value string) error { return saveAIConfigKey("refresh-interval", value) }

// saveMetadataFormat persists the metadata-format value to the config file.
func saveMetadataFormat(value metadataFormat) error {
	return saveAIConfigKey("metadata-format", string(value))
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
	name           string
	apiKey         string // empty for a keyless provider used without a key
	keyEnv         string // environment variable the key came from ("" when none)
	baseURL        string
	model          string
	maxTokens      int
	requestTimeout time.Duration // 0 means use defaultRequestTimeout
	notice         string        // shown once in the AI shell before the first answer
}

type providerDef struct {
	name         string
	envKeys      []string
	baseURL      string
	defaultModel string
	// aggregator marks a gateway serving many vendors' models under its own
	// names. A model chosen while another provider was in effect is
	// meaningless there (see resolveProvider).
	aggregator bool
	// keyless providers work without an API key (rate-limited per IP) and are
	// the fallback when no key is found.
	keyless bool
	// notice is shown when the provider is used without a key.
	notice string
}

// providerOrder is the auto-detection order: the first provider whose API key
// is set wins (ai.provider overrides). The keyless provider comes last.
var providerOrder = []providerDef{
	// OpenRouter's "openrouter/free" router picks a currently available free
	// model that supports the request, so the default doesn't depend on any
	// single :free model surviving. Pin one with ai.model to get a
	// reproducible model.
	{name: "openrouter", envKeys: []string{"OPENROUTER_API_KEY"}, baseURL: "https://openrouter.ai/api", defaultModel: "openrouter/free", aggregator: true},
	// OpenCode's console API (formerly Zen). Its free models are served to the
	// OpenCode app only (HTTP 429 FreeUsageLimitError elsewhere), so the
	// default is the paid, cheap DeepSeek model.
	{name: "opencode", envKeys: []string{"OPENCODE_API_KEY"}, baseURL: "https://opencode.ai/inference/openai", defaultModel: "deepseek-v4.1-flash", aggregator: true},
	{name: "anthropic", envKeys: []string{"ANTHROPIC_API_KEY"}, baseURL: "https://api.anthropic.com", defaultModel: "claude-sonnet-5"},
	{name: "openai", envKeys: []string{"OPENAI_API_KEY"}, baseURL: "https://api.openai.com", defaultModel: "gpt-5.6-luna"},
	{name: "gemini", envKeys: []string{"GEMINI_API_KEY"}, baseURL: "https://generativelanguage.googleapis.com", defaultModel: "gemini-3.8-flash"},
	{name: "xai", envKeys: []string{"XAI_API_KEY"}, baseURL: "https://api.x.ai", defaultModel: "grok-4.5"},
	{name: "deepseek", envKeys: []string{"DEEPSEEK_API_KEY"}, baseURL: "https://api.deepseek.com", defaultModel: "deepseek-v4-flash"},
	{name: "mistral", envKeys: []string{"MISTRAL_API_KEY"}, baseURL: "https://api.mistral.ai", defaultModel: "mistral-small-latest"},
	// Kilo Code Gateway: its kilo-auto/free router needs no key and no login,
	// only an IP-based rate limit (about 200 requests/hour). KILO_API_KEY is
	// optional and, when set, lifts that limit and unlocks paid models.
	{
		name: "kilo", envKeys: []string{"KILO_API_KEY"}, baseURL: "https://api.kilo.ai/api/gateway", defaultModel: "kilo-auto/free",
		aggregator: true, keyless: true,
		notice: "No AI API key found — using the free Kilo gateway (kilo-auto/free): about 200 requests/hour per IP, and prompts may be logged by the upstream providers, so keep sensitive data out. Set OPENROUTER_API_KEY (or another provider's key) to use your own account.",
	},
}

type envLookup func(string) string

func providerNames() []string {
	names := make([]string, len(providerOrder))
	for i, def := range providerOrder {
		names[i] = def.name
	}
	return names
}

func resolveProvider(cfg *xmcConfig, getenv envLookup) (providerSpec, error) {
	maxTok := cfg.AI.MaxTokens
	if maxTok <= 0 {
		maxTok = defaultMaxTokens
	}
	reqTimeout := cfg.AI.requestTimeoutDuration()
	pinned := cfg.AI.Provider != ""

	// retiredKey names an environment variable xmc used to read but doesn't
	// any more, so a user who still has only that one set learns why their
	// Gemini key is ignored instead of silently losing the provider.
	retiredKey := ""
	if getenv("GEMINI_API_KEY") == "" && getenv("GOOGLE_API_KEY") != "" {
		retiredKey = "GOOGLE_API_KEY is no longer read; set GEMINI_API_KEY to use Gemini."
	}

	specFor := func(def providerDef, key, keyEnv string) providerSpec {
		model := def.defaultModel
		var notices []string
		if m := cfg.AI.Model; m != "" {
			// With no ai.provider the provider is whichever key is found, so a
			// model saved while another provider was in effect (before /model
			// stored the provider next to the model) may not exist on a
			// gateway; ignore it there rather than fail every request.
			if pinned || !def.aggregator {
				model = m
			} else {
				notices = append(notices, fmt.Sprintf("ai.model %q is ignored because ai.provider is not set and %s was picked automatically; /model saves both.", m, def.name))
			}
		}
		if key == "" && def.notice != "" {
			notices = append(notices, def.notice)
		}
		if retiredKey != "" {
			notices = append(notices, retiredKey)
		}
		return providerSpec{
			name:           def.name,
			apiKey:         key,
			keyEnv:         keyEnv,
			baseURL:        def.baseURL,
			model:          model,
			maxTokens:      maxTok,
			requestTimeout: reqTimeout,
			notice:         strings.Join(notices, "\n"),
		}
	}

	if pinned {
		for _, def := range providerOrder {
			if !strings.EqualFold(def.name, cfg.AI.Provider) {
				continue
			}
			key, keyEnv := findKey(def.envKeys, getenv)
			if key == "" && !def.keyless {
				msg := fmt.Sprintf("provider %q selected in config but no API key found (set %s)", def.name, strings.Join(def.envKeys, " or "))
				if def.name == "gemini" && retiredKey != "" {
					msg += "; " + retiredKey
				}
				return providerSpec{}, errors.New(msg)
			}
			return specFor(def, key, keyEnv), nil
		}
		return providerSpec{}, fmt.Errorf("unknown AI provider %q (supported: %s)", cfg.AI.Provider, strings.Join(providerNames(), ", "))
	}

	for _, def := range providerOrder {
		if key, keyEnv := findKey(def.envKeys, getenv); key != "" {
			return specFor(def, key, keyEnv), nil
		}
	}
	for _, def := range providerOrder {
		if def.keyless {
			return specFor(def, "", ""), nil
		}
	}
	keys := make([]string, len(providerOrder))
	for i, def := range providerOrder {
		keys[i] = def.envKeys[0]
	}
	return providerSpec{}, fmt.Errorf("no AI API key found; set one of: %s", strings.Join(keys, ", "))
}

// findKey returns the first non-empty key among envKeys and the variable it
// came from.
func findKey(envKeys []string, getenv envLookup) (key, env string) {
	for _, k := range envKeys {
		if v := getenv(k); v != "" {
			return v, k
		}
	}
	return "", ""
}

// saveAIModel writes the chosen provider and model to the config file under
// ai.provider and ai.model. A model name only means something for the provider
// it was chosen on, so the two are stored together — otherwise a later change
// of API keys would send this model to a different provider.
func saveAIModel(provider, model string) error {
	return saveAIConfigKeys("provider", provider, "model", model)
}

// saveAIEffort writes the reasoning effort to the config file under ai.effort.
func saveAIEffort(effort aiEffort) error { return saveAIConfigKey("effort", string(effort)) }

// parseEffort parses a user-supplied effort level ("low"/"l",
// "medium"/"med"/"m", "high"/"h"; case-insensitive).
func parseEffort(s string) (aiEffort, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "low", "l":
		return effortLow, true
	case "medium", "med", "m":
		return effortMedium, true
	case "high", "h":
		return effortHigh, true
	}
	return "", false
}

// saveAIConfigKey persists key=value under the "ai" section of the config file.
func saveAIConfigKey(key, value string) error { return saveAIConfigKeys(key, value) }

// saveAIConfigKeys persists key/value pairs (alternating arguments) under the
// "ai" section of the config file in one write, preserving all other keys and
// comments via yaml.Node round-trip.
func saveAIConfigKeys(pairs ...string) error {
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

	aiNode := yamlFindOrCreateMapping(doc.Content[0], "ai")
	for i := 0; i+1 < len(pairs); i += 2 {
		yamlSetScalar(aiNode, pairs[i], pairs[i+1])
	}

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
