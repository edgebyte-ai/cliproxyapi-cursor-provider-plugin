package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/edgebyte-ai/cliproxyapi-cursor-provider-plugin/internal/provider"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

func TestPluginRegistrationName(t *testing.T) {
	registration := pluginRegistration()
	if registration.Metadata.Name != "Cursor Provider" {
		t.Fatalf("plugin name = %q, want Cursor Provider", registration.Metadata.Name)
	}
}

func TestDecodeCursorAuthStorageRejectsOtherProviders(t *testing.T) {
	raw := json.RawMessage(`{"type":"codex","access_token":"secret"}`)
	if _, ok := decodeCursorAuthStorage(raw); ok {
		t.Fatal("decodeCursorAuthStorage() accepted a non-Cursor credential")
	}
	raw = json.RawMessage(`{"type":"cursor","label":"Cursor One"}`)
	storage, ok := decodeCursorAuthStorage(raw)
	if !ok || storage.Label != "Cursor One" {
		t.Fatalf("decodeCursorAuthStorage() = %#v, %t", storage, ok)
	}
}

func TestManagementRegistrationIncludesReadableAccountPolicy(t *testing.T) {
	result, err := dispatch(pluginabi.MethodManagementRegister, nil)
	if err != nil {
		t.Fatalf("dispatch() error = %v", err)
	}
	registration, ok := result.(rpcManagementRegistrationResponse)
	if !ok {
		t.Fatalf("dispatch() result = %T, want rpcManagementRegistrationResponse", result)
	}
	methods := make(map[string]bool)
	for _, route := range registration.Routes {
		if route.Path == "/plugins/cursor-provider/account-policy" {
			methods[route.Method] = true
		}
	}
	if !methods["GET"] || methods["PATCH"] {
		t.Fatalf("account policy methods = %#v, want GET only", methods)
	}
}

func TestAccountPolicyResponseCopiesModelRules(t *testing.T) {
	storage := provider.AuthStorage{
		Label: "Cursor One", Prefix: "cursor1", Priority: 10,
		AllowedModels: []string{"composer-*"}, DeniedModels: []string{"*-fast"},
	}
	response := newAccountPolicyResponse("index-1", "cursor-one.json", storage)
	storage.AllowedModels[0] = "changed"
	storage.DeniedModels[0] = "changed"
	if response.AuthIndex != "index-1" || response.Name != "cursor-one.json" ||
		response.Label != "Cursor One" || response.Prefix != "cursor1" || response.Priority != 10 {
		t.Fatalf("response identity = %#v", response)
	}
	if response.AllowedModels[0] != "composer-*" || response.DeniedModels[0] != "*-fast" {
		t.Fatalf("response rules were not copied: %#v", response)
	}
	empty := newAccountPolicyResponse("index-2", "cursor-two.json", provider.AuthStorage{})
	raw, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if strings.Contains(string(raw), `"allowed_models":null`) || strings.Contains(string(raw), `"denied_models":null`) {
		t.Fatalf("empty policy arrays encoded as null: %s", raw)
	}
}

func TestQuotaPageProvidesGenericPolicyEditorWithoutPresets(t *testing.T) {
	for _, want := range []string{
		"Allowed model rules", "Denied model rules", "Save account policy",
		"/plugins/cursor-provider/account-policy", "/v0/management/auth-files/fields",
	} {
		if !strings.Contains(cursorQuotaPage, want) {
			t.Fatalf("quota page missing %q", want)
		}
	}
	if strings.Contains(strings.ToLower(cursorQuotaPage), "preset") {
		t.Fatal("quota page must not include policy presets")
	}
}
