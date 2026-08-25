package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	events, err := service.runTurn(ctx, storage, "composer-2.5", TurnInput{UserText: "Reply with exactly LIVE_CURSOR_PLUGIN_OK"})
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
	requestBody := []byte(`{"model":"composer-2.5","messages":[{"role":"user","content":"Reply with exactly LIVE_EXECUTOR_OK"}],"stream":false}`)
	response, err := service.Execute(context.Background(), pluginapi.ExecutorRequest{
		AuthID: "live", AuthProvider: ProviderID, Model: "composer-2.5",
		Format: "openai", SourceFormat: "openai", OriginalRequest: requestBody, Payload: requestBody, StorageJSON: raw,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("payload=%s", response.Payload)
	if !bytes.Contains(response.Payload, []byte("LIVE_EXECUTOR_OK")) {
		t.Fatal("executor response did not contain expected text")
	}
}
