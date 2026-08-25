package provider

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const ProviderID = "cursor-native"

type Config struct {
	Enabled                bool     `yaml:"enabled" json:"enabled"`
	ProviderID             string   `yaml:"provider_id" json:"provider_id"`
	ModelPrefix            string   `yaml:"model_prefix" json:"model_prefix"`
	ModelMode              string   `yaml:"model_mode" json:"model_mode"`
	DefaultReasoningEffort string   `yaml:"default_reasoning_effort" json:"default_reasoning_effort"`
	CursorBaseURL          string   `yaml:"cursor_base_url" json:"cursor_base_url"`
	ClientVersion          string   `yaml:"client_version" json:"client_version"`
	AllowedNativeTools     []string `yaml:"allowed_native_tools" json:"allowed_native_tools"`
	RequestTimeoutSeconds  int      `yaml:"request_timeout_seconds" json:"request_timeout_seconds"`
	ModelCacheTTLSeconds   int      `yaml:"model_cache_ttl_seconds" json:"model_cache_ttl_seconds"`
	ContinuationPrompt     string   `yaml:"continuation_prompt" json:"continuation_prompt"`
	MaxRequestBytes        int      `yaml:"max_request_bytes" json:"max_request_bytes"`
	MaxHistoryMessageBytes int      `yaml:"max_history_message_bytes" json:"max_history_message_bytes"`
}

func DefaultConfig() Config {
	return Config{
		Enabled:                true,
		ProviderID:             ProviderID,
		ModelPrefix:            "",
		ModelMode:              "normalized",
		DefaultReasoningEffort: "high",
		CursorBaseURL:          "https://api2.cursor.sh",
		ClientVersion:          "cli-2026.08.11-e8db854",
		AllowedNativeTools:     []string{"mcp_tool_call"},
		RequestTimeoutSeconds:  300,
		ModelCacheTTLSeconds:   600,
		ContinuationPrompt:     "Continue, using the tool results above.",
		MaxRequestBytes:        64 << 20,
		MaxHistoryMessageBytes: 4 << 20,
	}
}

func ParseConfig(raw []byte) (Config, error) {
	cfg := DefaultConfig()
	if len(raw) > 0 {
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return Config{}, fmt.Errorf("decode Cursor plugin config: %w", err)
		}
	}
	cfg.ProviderID = strings.TrimSpace(cfg.ProviderID)
	if cfg.ProviderID == "" {
		cfg.ProviderID = ProviderID
	}
	if cfg.ProviderID != ProviderID {
		return Config{}, fmt.Errorf("provider_id must be %q", ProviderID)
	}
	cfg.ModelMode = strings.ToLower(strings.TrimSpace(cfg.ModelMode))
	if cfg.ModelMode != "raw" && cfg.ModelMode != "normalized" && cfg.ModelMode != "both" {
		return Config{}, fmt.Errorf("model_mode must be raw, normalized, or both")
	}
	if strings.TrimSpace(cfg.CursorBaseURL) == "" {
		return Config{}, fmt.Errorf("cursor_base_url is required")
	}
	if cfg.RequestTimeoutSeconds < 1 || cfg.RequestTimeoutSeconds > 1800 {
		return Config{}, fmt.Errorf("request_timeout_seconds must be between 1 and 1800")
	}
	if cfg.ModelCacheTTLSeconds < 0 || cfg.ModelCacheTTLSeconds > 86400 {
		return Config{}, fmt.Errorf("model_cache_ttl_seconds must be between 0 and 86400")
	}
	if cfg.MaxRequestBytes < 1024 {
		return Config{}, fmt.Errorf("max_request_bytes must be at least 1024")
	}
	if cfg.MaxHistoryMessageBytes < 1024 {
		return Config{}, fmt.Errorf("max_history_message_bytes must be at least 1024")
	}
	return cfg, nil
}

func (c Config) RequestTimeout() time.Duration {
	return time.Duration(c.RequestTimeoutSeconds) * time.Second
}

func (c Config) ModelCacheTTL() time.Duration {
	return time.Duration(c.ModelCacheTTLSeconds) * time.Second
}
