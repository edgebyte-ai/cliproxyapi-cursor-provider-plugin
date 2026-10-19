package provider

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestLiveCursorTurn(t *testing.T) {
	path := os.Getenv("CURSOR_PLUGIN_LIVE_AUTH")
	if path == "" {
		t.Skip("CURSOR_PLUGIN_LIVE_AUTH is not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var storage AuthStorage
	if err := json.Unmarshal(raw, &storage); err != nil {
		t.Fatal(err)
	}
	service := New()
	ctx := context.Background()
	model, err := service.resolveModel(ctx, storage, "composer-2.5", "")
	if err != nil {
		t.Fatal(err)
	}
	events, err := service.runTurn(ctx, storage, model, TurnInput{UserText: "Reply with exactly LIVE_CURSOR_PLUGIN_OK"})
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for event := range events {
		t.Logf("event type=%s text=%q reason=%s err=%v tokens=%d", event.Type, event.Text, event.DoneReason, event.Err, event.Tokens)
		seen = true
	}
	if !seen {
		t.Fatal("Cursor stream returned no events")
	}
}

func TestLiveCursorExecutor(t *testing.T) {
	path := os.Getenv("CURSOR_PLUGIN_LIVE_AUTH")
	if path == "" {
		t.Skip("CURSOR_PLUGIN_LIVE_AUTH is not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	service := New()
	model := firstNonEmpty(os.Getenv("CURSOR_PLUGIN_LIVE_MODEL"), "composer-2.5")
	requestBody, err := json.Marshal(map[string]any{
		"model": model, "messages": []map[string]string{{"role": "user", "content": "What is 2 + 2?"}},
		"stream": false, "reasoning_effort": firstNonEmpty(os.Getenv("CURSOR_PLUGIN_LIVE_EFFORT"), "low"),
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := service.Execute(context.Background(), pluginapi.ExecutorRequest{
		AuthID: "live", AuthProvider: ProviderID, Model: model,
		Format: "openai", SourceFormat: "openai", OriginalRequest: requestBody, Payload: requestBody, StorageJSON: raw,
	})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Choices) != 1 || result.Choices[0].FinishReason != "stop" || !strings.Contains(result.Choices[0].Message.Content, "4") {
		t.Fatalf("unexpected final answer: %+v", result)
	}
	t.Logf("model=%s answer=%q", model, result.Choices[0].Message.Content)
}
