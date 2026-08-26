package provider

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ProviderID is deliberately shorter than the plugin ID. It is the auth/provider
// key shown by CLIProxyAPI in OAuth configuration and Auth Files, while the
// separately registered plugin remains "cliproxyapi-cursor-provider".
const ProviderID = "cursor"

type Config struct {
	Enabled                bool              `yaml:"enabled" json:"enabled"`
	ProviderID             string            `yaml:"provider_id" json:"provider_id"`
	ModelPrefix            string            `yaml:"model_prefix" json:"model_prefix"`
	ModelMode              string            `yaml:"model_mode" json:"model_mode"`
	DefaultReasoningEffort string            `yaml:"default_reasoning_effort" json:"default_reasoning_effort"`
	ModelDisplayNames      map[string]string `yaml:"model_display_names" json:"model_display_names"`
	CursorBaseURL          string            `yaml:"cursor_base_url" json:"cursor_base_url"`
	ClientVersion          string            `yaml:"client_version" json:"client_version"`
	AllowedNativeTools     []string          `yaml:"allowed_native_tools" json:"allowed_native_tools"`
	RequestTimeoutSeconds  int               `yaml:"request_timeout_seconds" json:"request_timeout_seconds"`
	TransientRetryCount    int               `yaml:"transient_retry_count" json:"transient_retry_count"`
	TransientRetryDelayMS  int               `yaml:"transient_retry_delay_ms" json:"transient_retry_delay_ms"`
	ModelCacheTTLSeconds   int               `yaml:"model_cache_ttl_seconds" json:"model_cache_ttl_seconds"`
	ContinuationPrompt     string            `yaml:"continuation_prompt" json:"continuation_prompt"`
	MaxRequestBytes        int               `yaml:"max_request_bytes" json:"max_request_bytes"`
	MaxHistoryMessageBytes int               `yaml:"max_history_message_bytes" json:"max_history_message_bytes"`
}

func DefaultConfig() Config {
	return Config{
		Enabled:                true,
		ProviderID:             ProviderID,
		ModelPrefix:            "",
		ModelMode:              "normalized",
		DefaultReasoningEffort: "high",
		ModelDisplayNames:      map[string]string{},
		CursorBaseURL:          "https://api2.cursor.sh",
		ClientVersion:          "cli-2026.08.11-e8db854",
		AllowedNativeTools:     []string{"mcp_tool_call"},
		RequestTimeoutSeconds:  300,
		TransientRetryCount:    1,
		TransientRetryDelayMS:  250,
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
	displayNames := make(map[string]string, len(cfg.ModelDisplayNames))
	for modelID, displayName := range cfg.ModelDisplayNames {
		modelID = strings.ToLower(strings.TrimSpace(modelID))
		displayName = strings.TrimSpace(displayName)
		if modelID == "" || displayName == "" {
			continue
		}
		displayNames[modelID] = displayName
	}
	cfg.ModelDisplayNames = displayNames
	if strings.TrimSpace(cfg.CursorBaseURL) == "" {
		return Config{}, fmt.Errorf("cursor_base_url is required")
	}
	if cfg.RequestTimeoutSeconds < 1 || cfg.RequestTimeoutSeconds > 1800 {
		return Config{}, fmt.Errorf("request_timeout_seconds must be between 1 and 1800")
	}
	if cfg.TransientRetryCount < 0 || cfg.TransientRetryCount > 3 {
		return Config{}, fmt.Errorf("transient_retry_count must be between 0 and 3")
	}
	if cfg.TransientRetryDelayMS < 0 || cfg.TransientRetryDelayMS > 5000 {
		return Config{}, fmt.Errorf("transient_retry_delay_ms must be between 0 and 5000")
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

func (c Config) ModelDisplayName(modelID, fallback string) string {
	if displayName := strings.TrimSpace(c.ModelDisplayNames[strings.ToLower(strings.TrimSpace(modelID))]); displayName != "" {
		return displayName
	}
	return fallback
}

func (c Config) RequestTimeout() time.Duration {
	return time.Duration(c.RequestTimeoutSeconds) * time.Second
}

func (c Config) TransientRetryDelay() time.Duration {
	return time.Duration(c.TransientRetryDelayMS) * time.Millisecond
}

func (c Config) ModelCacheTTL() time.Duration {
	return time.Duration(c.ModelCacheTTLSeconds) * time.Second
}
