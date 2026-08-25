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
