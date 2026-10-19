package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/edgebyte-ai/cliproxyapi-cursor-provider-plugin/internal/pb"
)

const parameterizedCatalog = `{
  "useModelParameters": true,
  "models": [{
    "name": "grok-4.7", "clientDisplayName": "Grok 4.7", "supportsAgent": true,
    "variants": [
      {"legacySlug":"grok-4.7-low", "parameterValues":[{"id":"context","value":"256k"},{"id":"reasoning_effort","value":"low"},{"id":"fast","value":"false"}]},
      {"legacySlug":"grok-4.7-high", "isDefaultNonMaxConfig":true, "parameterValues":[{"id":"context","value":"256k"},{"id":"reasoning_effort","value":"high"},{"id":"fast","value":"false"}]},
      {"legacySlug":"grok-4.7-high", "isMaxMode":true, "parameterValues":[{"id":"context","value":"500k"},{"id":"reasoning_effort","value":"high"}]}
    ]
  }, {
    "name":"auto-smart", "variants":[
      {"legacySlug":"auto-smart","parameterValues":[{"id":"optimize_for","value":"intelligence"}]},
      {"legacySlug":"auto-smart","isDefaultNonMaxConfig":true,"parameterValues":[{"id":"optimize_for","value":"balanced"}]},
      {"legacySlug":"auto-smart","parameterValues":[{"id":"optimize_for","value":"speed"}]}
    ]
  }, {"name":"hidden", "isHidden":true}, {"name":"chat-only", "supportsAgent":false}]
}`

func decodeTestCatalog(t *testing.T, raw string) []CursorModel {
	t.Helper()
	var response availableModelsResponse
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		t.Fatal(err)
	}
	return parameterizedModels(response)
}

func TestFetchAvailableModelsUsesParameterizedCatalogOnly(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		ok     bool
	}{
		{"parameterized", 200, parameterizedCatalog, true},
		{"upstream failure", 404, `{"error":{"code":"not_found","message":"unavailable"}}`, false},
		{"non-parameterized response", 200, `{"models":[{"name":"grok-4.7"}]}`, false},
		{"invalid response", 200, `{`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != "/aiserver.v1.AiService/AvailableModels" {
					t.Errorf("unexpected catalog request: %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("Connect-Protocol-Version") != "1" || r.Header.Get("X-Cursor-Client-Type") != "cli" {
					t.Error("catalog request omitted authentication or Connect headers")
				}
				if r.Header.Get("X-Cursor-Client-Version") != DefaultConfig().ClientVersion {
					t.Error("catalog request omitted configured client version")
				}
				var body map[string]bool
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !body["useModelParameters"] || !body["doNotUseMarkdown"] {
					t.Errorf("parameterized catalog flags missing: %v, %v", body, err)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			cfg := DefaultConfig()
			cfg.CursorBaseURL = server.URL
			models, err := fetchAvailableModels(context.Background(), "test-token", cfg, server.Client())
			if (err == nil) != test.ok {
				t.Fatalf("catalog error = %v, want success %v", err, test.ok)
			}
			if calls != 1 {
				t.Fatalf("made %d requests, want one without fallback", calls)
			}
			if test.ok && len(models) != 3 {
				t.Fatalf("catalog models = %+v", models)
			}
			if test.status == http.StatusNotFound {
				var status *StatusError
				if !errors.As(err, &status) || status.HTTPStatus != http.StatusNotFound {
					t.Fatalf("lost upstream HTTP status: %v", err)
				}
			}
		})
	}
}

func TestParameterizedModelsPreserveDefaultsAndContext(t *testing.T) {
	models := decodeTestCatalog(t, parameterizedCatalog)
	if len(models) != 3 || models[1].ID != "grok-4.7-high" {
		t.Fatalf("unexpected variants: %+v", models)
	}
	if got := modelParameters(models[1].Selection); got["context"] != "256k" || got["fast"] != "false" {
		t.Fatalf("selected a max/fast variant: %v", got)
	}
	if got := modelParameters(models[2].Selection); got["optimize_for"] != "balanced" {
		t.Fatalf("Auto default = %v", got)
	}
}

func TestResolveParameterizedModelAppliesEffortAndAccountPolicy(t *testing.T) {
	service := New()
	models := decodeTestCatalog(t, parameterizedCatalog)
	storage := AuthStorage{ID: "account", DeniedModels: []string{"*-fast"}}
	service.modelCache[storage.ID] = cachedModels{models: filterModels(models, storage), expiresAt: service.now().Add(service.Config().ModelCacheTTL())}
	for _, effort := range []string{"", "low", "high"} {
		model, err := service.resolveModel(context.Background(), storage, "grok-4.7", effort)
		if err != nil {
			t.Fatal(err)
		}
		wantEffort := firstNonEmpty(effort, "high")
		want := map[string]string{"context": "256k", "reasoning_effort": wantEffort, "fast": "false"}
		if model.ModelId != "grok-4.7" || !reflect.DeepEqual(modelParameters(model), want) {
			t.Fatalf("selection = %v", model)
		}
		model.Parameters[0].Value = "changed"
	}
	if _, err := service.resolveModel(context.Background(), storage, "grok-4.7", "max"); err == nil {
		t.Fatal("unsupported effort was accepted")
	}
	if _, err := service.resolveModel(context.Background(), storage, "grok-4.7-high", ""); err == nil {
		t.Fatal("raw effort-suffixed ID was accepted in normalized mode")
	}
	denied := AuthStorage{ID: "denied", DeniedModels: []string{"grok-*"}}
	service.modelCache[denied.ID] = cachedModels{models: filterModels(models, denied), expiresAt: service.now().Add(service.Config().ModelCacheTTL())}
	if _, err := service.resolveModel(context.Background(), denied, "grok-4.7", "high"); err == nil {
		t.Fatal("another account's catalog bypassed the deny rules")
	}
}

func modelParameters(model *pb.RequestedModel) map[string]string {
	values := make(map[string]string)
	for _, parameter := range model.GetParameters() {
		values[parameter.GetId()] = parameter.GetValue()
	}
	return values
}
