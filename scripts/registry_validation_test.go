//go:build registryvalidation

package main

import (
	"encoding/json"
	"os"
	"testing"
)

type storeRegistry struct {
	SchemaVersion int `json:"schema_version"`
	Plugins       []struct {
		ID         string `json:"id"`
		Version    string `json:"version"`
		Repository string `json:"repository"`
	} `json:"plugins"`
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
	if registry.SchemaVersion != 1 || len(registry.Plugins) != 1 || registry.Plugins[0].ID != "cliproxyapi-cursor-provider" || registry.Plugins[0].Version == "" || registry.Plugins[0].Repository == "" {
		t.Fatalf("unexpected registry: %+v", registry)
	}
}
