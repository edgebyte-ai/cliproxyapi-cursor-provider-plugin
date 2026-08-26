package provider

import (
	"strings"
	"testing"
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
