package provider

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestNormalizedFamiliesKeepThinkingAndFastDimensions(t *testing.T) {
	models := []CursorModel{
		{ID: "cursor-grok-4.6-low", DisplayName: "Grok Low"},
		{ID: "cursor-grok-4.6-high", DisplayName: "Grok High"},
		{ID: "claude-4.6-opus-high-thinking", DisplayName: "Claude High Thinking"},
		{ID: "claude-4.6-opus-max-thinking", DisplayName: "Claude Max Thinking"},
		{ID: "composer-2.5", DisplayName: "Composer"},
	}
	families := normalizedFamilies(models, "high")
	if families["cursor-grok-4.6"].Variants["low"] != "cursor-grok-4.6-low" || families["cursor-grok-4.6"].DefaultEffort != "high" {
		t.Fatalf("unexpected Grok family: %+v", families["cursor-grok-4.6"])
	}
	if families["claude-4.6-opus-thinking"].Variants["max"] != "claude-4.6-opus-max-thinking" {
		t.Fatalf("thinking dimension was lost: %+v", families["claude-4.6-opus-thinking"])
	}
	if len(families["composer-2.5"].Variants) != 0 {
		t.Fatal("fixed Composer model acquired fake effort support")
	}
}

func TestResolveFixedModelRejectsReasoningEffort(t *testing.T) {
	service := New()
	service.modelCache["account"] = cachedModels{models: []CursorModel{{ID: "composer-2.5"}}, expiresAt: service.now().Add(service.Config().ModelCacheTTL())}
	_, err := service.resolveModel(context.Background(), AuthStorage{ID: "account"}, "composer-2.5", "high")
	var statusErr *StatusError
	if !errors.As(err, &statusErr) || statusErr.HTTPStatus != http.StatusBadRequest || statusErr.Code != "unsupported_reasoning_effort" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFilterModelsSupportsAccountGlobs(t *testing.T) {
	models := []CursorModel{{ID: "composer-2.5"}, {ID: "cursor-grok-4.6-high"}, {ID: "gpt-5.4"}}
	filtered := filterModels(models, AuthStorage{AllowedModels: []string{"composer-*", "cursor-grok-*"}, DeniedModels: []string{"*-low"}})
	if len(filtered) != 2 || filtered[0].ID != "composer-2.5" || filtered[1].ID != "cursor-grok-4.6-high" {
		t.Fatalf("unexpected filtered models: %+v", filtered)
	}
}
