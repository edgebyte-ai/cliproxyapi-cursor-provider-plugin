//go:build registryvalidation

package main

import (
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
)

type storeRegistry struct {
	SchemaVersion int `json:"schema_version"`
	Plugins       []struct {
		ID      string `json:"id"`
		Version string `json:"version"`
		Install struct {
			Type      string          `json:"type"`
			Artifacts []storeArtifact `json:"artifacts"`
		} `json:"install"`
	} `json:"plugins"`
}

type storeArtifact struct {
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

func TestRegistry(t *testing.T) {
	raw, err := os.ReadFile("../plugin-store/registry.json")
	if err != nil {
		t.Fatal(err)
	}
	var registry storeRegistry
	if err := json.Unmarshal(raw, &registry); err != nil {
		t.Fatal(err)
	}
	if registry.SchemaVersion != 2 || len(registry.Plugins) != 1 || registry.Plugins[0].ID != "cliproxyapi-cursor-native" || registry.Plugins[0].Install.Type != "direct" {
		t.Fatalf("unexpected registry: %+v", registry)
	}
	for _, artifact := range registry.Plugins[0].Install.Artifacts {
		parsed, err := url.Parse(artifact.URL)
		if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			t.Fatalf("invalid artifact URL: %q", artifact.URL)
		}
		checksum, err := hex.DecodeString(strings.ToLower(artifact.SHA256))
		if err != nil || len(checksum) != 32 || artifact.Size <= 0 || artifact.GOOS == "" || artifact.GOARCH == "" {
			t.Fatalf("invalid artifact: %+v", artifact)
		}
	}
}
