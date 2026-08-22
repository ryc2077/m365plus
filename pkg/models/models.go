// Package models provides constants, data structures, and configuration for M365 Copilot integration.
// It includes model definitions, environment configuration, and message type mappings.
package models

import (
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ryc2077/m365plus/pkg/logging"
)

// Version is the application version, shared across all binaries.
// Overridable at build time via ldflags: -X github.com/ryc2077/m365plus/pkg/models.Version=x.y.z
var Version = "1.3.12"

const (
	// DefaultClientID is the default Microsoft 365 Copilot client ID.
	DefaultClientID = "4765445b-32c6-49b0-83e6-1d93765276ca"

	// DefaultScope is the OAuth2 scope for M365 Copilot access.
	DefaultScope = "https://substrate.office.com/sydney/.default openid profile offline_access"
)

// ModelConfig represents the configuration for a specific model variant.
type ModelConfig struct {
	Tone        string // The tone/style parameter sent to the backend
	Override    string // Optional GPT model override identifier
	OpenAIID    string // OpenAI-compatible model identifier
	Owner       string
	DisplayName string
	Thinking    bool
}

const (
	OwnerMicrosoft = "microsoft-365"
	OwnerAnthropic = "anthropic-via-microsoft-365"
)

func (c ModelConfig) OwnerOrDefault() string {
	if c.Owner != "" {
		return c.Owner
	}
	return OwnerMicrosoft
}

func (c ModelConfig) DisplayNameOrDefault() string {
	if c.DisplayName != "" {
		return c.DisplayName
	}
	return c.OpenAIID
}

type ReasoningEffortPreset struct {
	Effort      string `json:"effort"`
	Description string `json:"description"`
}

var ReasoningEffortPresets = []ReasoningEffortPreset{
	{Effort: "none", Description: "Disable additional reasoning."},
	{Effort: "minimal", Description: "Fast responses with minimal reasoning."},
	{Effort: "low", Description: "Fast responses with lighter reasoning."},
	{Effort: "medium", Description: "Balances speed and reasoning depth."},
	{Effort: "high", Description: "Greater reasoning depth for complex problems."},
	{Effort: "xhigh", Description: "Extra high reasoning depth."},
	{Effort: "max", Description: "Maximum reasoning depth."},
}

// ModelRegistry maps model keys to their configurations.
var ModelRegistry = map[string]ModelConfig{
	"auto": {
		Tone:        "Magic",
		Override:    "",
		OpenAIID:    "gpt-4-auto",
		DisplayName: "GPT Auto",
	},
	"quick": {
		Tone:        "Chat",
		Override:    "",
		OpenAIID:    "gpt-4-quick",
		DisplayName: "GPT Quick",
	},
	"reasoning": {
		Tone:        "Magic",
		Override:    "",
		OpenAIID:    "gpt-4-reasoning",
		DisplayName: "GPT Reasoning",
		Thinking:    true,
	},
	"gpt5.5": {
		Tone:        "Gpt_5_5_Chat",
		Override:    "",
		OpenAIID:    "gpt-5.5",
		DisplayName: "GPT-5.5",
	},
	"gpt5.5-reasoning": {
		Tone:        "Gpt_5_5_Reasoning",
		Override:    "",
		OpenAIID:    "gpt-5.5-reasoning",
		DisplayName: "GPT-5.5 Reasoning",
		Thinking:    true,
	},
	"gpt5.6-reasoning": {
		Tone:        "Gpt_5_6_Reasoning",
		Override:    "",
		OpenAIID:    "gpt-5.6-reasoning",
		DisplayName: "GPT-5.6 Reasoning",
		Thinking:    true,
	},
	"gpt5.6-sol": {
		Tone:        "Gpt_5_6_Reasoning",
		Override:    "",
		OpenAIID:    "gpt-5.6-sol",
		DisplayName: "GPT-5.6 Sol",
	},
	"gpt5.6-terra": {
		Tone:        "Gpt_5_6_Reasoning",
		Override:    "",
		OpenAIID:    "gpt-5.6-terra",
		DisplayName: "GPT-5.6 Terra",
	},
	"gpt5.6-luna": {
		Tone:        "Gpt_5_6_Reasoning",
		Override:    "",
		OpenAIID:    "gpt-5.6-luna",
		DisplayName: "GPT-5.6 Luna",
	},
	// Claude — real Anthropic models (verified via tone test, July 2026)
	"claude": {
		Tone:        "Claude_Sonnet",
		Override:    "",
		OpenAIID:    "claude-sonnet-4.6",
		DisplayName: "Claude Sonnet 4.6",
		Owner:       OwnerAnthropic,
	},
	"claude-sonnet": {
		Tone:        "Claude_Sonnet",
		Override:    "",
		OpenAIID:    "claude-sonnet-4.6",
		DisplayName: "Claude Sonnet 4.6",
		Owner:       OwnerAnthropic,
	},
	"claude-opus": {
		Tone:        "Claude_Opus",
		Override:    "",
		OpenAIID:    "claude-opus-4.6",
		DisplayName: "Claude Opus 4.6",
		Thinking:    true,
		Owner:       OwnerAnthropic,
	},
	"claude-fable": {
		Tone:        "Claude_Fable",
		Override:    "",
		OpenAIID:    "claude-fable-5",
		DisplayName: "Claude Fable 5",
		Owner:       OwnerAnthropic,
	},
	"claude-sonnet-4-20250514": {
		Tone:        "Claude_Sonnet",
		Override:    "",
		OpenAIID:    "claude-sonnet-4.6",
		DisplayName: "Claude Sonnet 4.6",
		Owner:       OwnerAnthropic,
	},
}

// ToolMessageType maps WebSocket message types to tool function names.
var ToolMessageType = map[string]string{
	"InternalSearchQuery": "search",
	"GeneratedCode":       "code_interpreter",
	"TriggerPlugin":       "trigger_plugin",
	"InvokeAction":        "invoke_action",
}

// Config holds environment-based configuration.
type Config struct {
	TenantID              string
	UserOID               string
	ClientID              string
	Scope                 string
	APIKeys               []string
	EnableCodeTools       bool
	AutoExposeTools       bool
	WorkspaceDir          string
	CodeToolTimeout       time.Duration
	CodeToolMaxOutput     int64
	CodeToolMaxReadBytes  int64
	CodeToolMaxIterations int
	ImageHostAllowlist    []string
	ContextWindowTokens   int
	MaxOutputTokens       int
	MaxToolRounds         int
}

const (
	DefaultMaxToolRounds = 32
	MaxToolRoundsCeiling = 512
)

var DefaultImageHostAllowlist = []string{".officeapps.live.com"}

// LoadConfig loads configuration from .env file and environment variables.
// Returns configuration with defaults for missing values.
func LoadConfig() *Config {
	// Load .env file if it exists
	loadDotEnv()

	cfg := &Config{
		TenantID:              os.Getenv("M365_TENANT_ID"),
		UserOID:               os.Getenv("M365_USER_OID"),
		ClientID:              getEnvWithDefault("M365_CLIENT_ID", DefaultClientID),
		Scope:                 DefaultScope,
		APIKeys:               parseAPIKeys(os.Getenv("M365_API_KEYS"), os.Getenv("M365_API_KEY")),
		EnableCodeTools:       getEnvBool("M365_ENABLE_CODE_TOOLS", false),
		AutoExposeTools:       getEnvBool("M365_AUTO_EXPOSE_TOOLS", false),
		WorkspaceDir:          getEnvWithDefault("M365_WORKSPACE_DIR", "."),
		CodeToolTimeout:       getEnvDuration("M365_CODE_TOOL_TIMEOUT", 30*time.Second),
		CodeToolMaxOutput:     getEnvInt64("M365_CODE_TOOL_MAX_OUTPUT", 1<<20),
		CodeToolMaxReadBytes:  getEnvInt64("M365_CODE_TOOL_MAX_READ_BYTES", 1<<20),
		CodeToolMaxIterations: getEnvInt("M365_CODE_TOOL_MAX_ITERATIONS", 10),
		ImageHostAllowlist:    getEnvHostList("M365_IMAGE_HOST_ALLOWLIST", DefaultImageHostAllowlist),
		ContextWindowTokens:   getEnvInt("M365_CONTEXT_WINDOW", 1_000_000),
		MaxOutputTokens:       getEnvInt("M365_MAX_OUTPUT_TOKENS", 1_000_000),
		MaxToolRounds:         min(getEnvInt("M365_MAX_TOOL_ROUNDS", DefaultMaxToolRounds), MaxToolRoundsCeiling),
	}

	logging.Infof("LoadConfig: tenantID=%s userOID=%s clientID=%s apiKeys=%d", cfg.TenantID, cfg.UserOID, cfg.ClientID[:min(8, len(cfg.ClientID))]+"...", len(cfg.APIKeys))
	return cfg
}

// parseAPIKeys builds the API key list from M365_API_KEYS (comma-separated)
// and M365_API_KEY (singular, for backward compatibility).
// M365_API_KEYS takes precedence; M365_API_KEY is used only if M365_API_KEYS is empty.
func parseAPIKeys(keysCSV, singleKey string) []string {
	if keysCSV != "" {
		var keys []string
		for k := range strings.SplitSeq(keysCSV, ",") {
			k = strings.TrimSpace(k)
			if k != "" {
				keys = append(keys, k)
			}
		}
		return keys
	}
	if singleKey != "" {
		return []string{strings.TrimSpace(singleKey)}
	}
	return nil
}

// loadDotEnv reads a .env file and sets environment variables.
// Checks data/.env first, then falls back to .env in the current directory.
// Lines starting with # are comments. Format: KEY=VALUE
func loadDotEnv() {
	data, err := os.ReadFile("data/.env")
	if err != nil {
		data, err = os.ReadFile(".env")
		if err != nil {
			return
		}
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// Only set if not already in environment (env vars take precedence)
		if os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}
}

// LookupModel finds a model configuration by key or OpenAI ID.
// Returns the "auto" model configuration if not found.
func LookupModel(key string) ModelConfig {
	if cfg, ok := ModelRegistry[key]; ok {
		return cfg
	}
	// Try to find by OpenAI ID
	for _, cfg := range ModelRegistry {
		if cfg.OpenAIID == key {
			return cfg
		}
	}
	return ModelRegistry["auto"]
}

// FindModel finds a model by registry key or advertised OpenAI ID and reports
// whether the requested name is served by this instance.
func FindModel(key string) (ModelConfig, bool) {
	if cfg, ok := ModelRegistry[key]; ok {
		return cfg, true
	}
	for _, cfg := range ModelRegistry {
		if cfg.OpenAIID == key {
			return cfg, true
		}
	}
	return ModelConfig{}, false
}

// RegistryKeysFor returns registry keys matching either a key or an OpenAI ID.
func RegistryKeysFor(model string) []string {
	if _, ok := ModelRegistry[model]; ok {
		return []string{model}
	}
	keys := make([]string, 0, 1)
	for key, cfg := range ModelRegistry {
		if cfg.OpenAIID == model {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

// ReasoningEffortNames lists the reasoning effort values accepted by the API.
func ReasoningEffortNames() []string {
	names := make([]string, len(ReasoningEffortPresets))
	for i, preset := range ReasoningEffortPresets {
		names[i] = preset.Effort
	}
	return names
}

// getEnvWithDefault returns an environment variable value or a default fallback.
func getEnvWithDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvHostList(key string, defaults []string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return append([]string(nil), defaults...)
	}
	hosts := make([]string, 0)
	for host := range strings.SplitSeq(value, ",") {
		host = strings.ToLower(strings.TrimSpace(host))
		if host != "" {
			hosts = append(hosts, host)
		}
	}
	if len(hosts) == 0 {
		return append([]string(nil), defaults...)
	}
	return hosts
}

// getEnvBool returns true for "true", "1", "yes", "on" (case-insensitive).
func getEnvBool(key string, defaultValue bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch value {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	default:
		return defaultValue
	}
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return defaultValue
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return defaultValue
	}
	return parsed
}

func getEnvInt64(key string, defaultValue int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return defaultValue
	}
	return parsed
}

func getEnvInt(key string, defaultValue int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return defaultValue
	}
	return parsed
}
