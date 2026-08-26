package provider

import (
	"strings"
	"testing"
	"time"
)

func TestProviderIDAndConfigAreCursor(t *testing.T) {
	if ProviderID != "cursor" {
		t.Fatalf("ProviderID = %q, want cursor", ProviderID)
	}
	cfg, err := ParseConfig([]byte("provider_id: cursor\n"))
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	if cfg.ProviderID != "cursor" {
		t.Fatalf("ProviderID = %q, want cursor", cfg.ProviderID)
	}
	if _, err := ParseConfig([]byte("provider_id: cursor-provider\n")); err == nil || !strings.Contains(err.Error(), `provider_id must be "cursor"`) {
		t.Fatalf("ParseConfig() error = %v, want cursor validation error", err)
	}
}

func TestParseConfigValidatesTransientRetrySettings(t *testing.T) {
	cfg, err := ParseConfig([]byte("transient_retry_count: 3\ntransient_retry_delay_ms: 5000\n"))
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	if cfg.TransientRetryCount != 3 || cfg.TransientRetryDelay() != 5*time.Second {
		t.Fatalf("retry config = count %d, delay %v", cfg.TransientRetryCount, cfg.TransientRetryDelay())
	}
	for _, raw := range []string{
		"transient_retry_count: -1\n",
		"transient_retry_count: 4\n",
		"transient_retry_delay_ms: -1\n",
		"transient_retry_delay_ms: 5001\n",
	} {
		if _, err := ParseConfig([]byte(raw)); err == nil {
			t.Fatalf("ParseConfig(%q) succeeded, want error", raw)
		}
	}
}

func TestParseConfigNormalizesModelDisplayNames(t *testing.T) {
	cfg, err := ParseConfig([]byte("model_display_names:\n  ' Cursor-Grok-4.6 ': ' Grok 4.6 '\n  '': ignored\n  empty: '  '\n"))
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	if len(cfg.ModelDisplayNames) != 1 || cfg.ModelDisplayNames["cursor-grok-4.6"] != "Grok 4.6" {
		t.Fatalf("ModelDisplayNames = %#v", cfg.ModelDisplayNames)
	}
	if got := cfg.ModelDisplayName("CURSOR-GROK-4.6", "fallback"); got != "Grok 4.6" {
		t.Fatalf("ModelDisplayName() = %q", got)
	}
	if got := cfg.ModelDisplayName("composer-2.5", "Composer"); got != "Composer" {
		t.Fatalf("fallback ModelDisplayName() = %q", got)
	}
}
