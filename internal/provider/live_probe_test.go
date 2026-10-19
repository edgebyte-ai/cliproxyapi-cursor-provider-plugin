package provider

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestLiveCursorToolCall(t *testing.T) {
	path := os.Getenv("CURSOR_PLUGIN_LIVE_AUTH")
	if path == "" {
		t.Skip("CURSOR_PLUGIN_LIVE_AUTH is not set")
	}
	storage, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	model := firstNonEmpty(os.Getenv("CURSOR_PLUGIN_LIVE_MODEL"), "grok-4.7")
	requestBody, err := json.Marshal(map[string]any{
		"model": model, "reasoning_effort": "high",
		"messages": []map[string]string{{"role": "user", "content": "Use the get_weather tool to get the current weather for Tokyo."}},
		"tools": []map[string]any{{"type": "function", "function": map[string]any{
			"name": "get_weather", "description": "Get current weather for a city.",
			"parameters": map[string]any{"type": "object", "properties": map[string]any{"city": map[string]string{"type": "string"}}, "required": []string{"city"}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := New().Execute(context.Background(), pluginapi.ExecutorRequest{
		AuthID: "live-tool", AuthProvider: ProviderID, Model: model,
		Format: "openai", SourceFormat: "openai", OriginalRequest: requestBody, Payload: requestBody, StorageJSON: storage,
	})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Choices) != 1 || result.Choices[0].FinishReason != "tool_calls" || len(result.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("missing tool call: %+v", result)
	}
	call := result.Choices[0].Message.ToolCalls[0]
	var args map[string]any
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		t.Fatal(err)
	}
	if call.ID == "" || call.Function.Name != "get_weather" || args["city"] != "Tokyo" {
		t.Fatalf("unexpected tool call: %+v, %v", call, args)
	}
	t.Logf("tool=%s arguments=%s", call.Function.Name, call.Function.Arguments)
}

func TestLiveCursorCodexTools(t *testing.T) {
	path := os.Getenv("CURSOR_PLUGIN_LIVE_AUTH")
	if path == "" {
		t.Skip("CURSOR_PLUGIN_LIVE_AUTH is not set")
	}
	storage, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	custom := os.Getenv("CURSOR_PLUGIN_LIVE_CUSTOM_TOOL") == "1"
	tool := map[string]any{"type": "function", "name": "read_file", "description": "Read the named file from the caller workspace.", "parameters": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]string{"type": "string"}}, "required": []string{"path"}}}
	prompt := "Use functions.read_file to read global.json, then report sdk.version from that file."
	wantType, wantName := "function_call", "read_file"
	if custom {
		tool = map[string]any{"type": "custom", "name": "exec", "description": "Run JavaScript code. Use text(await tools.exec_command({cmd: \"cat global.json\"})) to read global.json from the caller workspace.", "format": map[string]string{"type": "text"}}
		prompt = "Use functions.exec to read global.json, then report sdk.version from that file."
		wantType, wantName = "custom_tool_call", "exec"
	}
	input := []any{map[string]any{"role": "user", "content": prompt}}
	body := map[string]any{"model": "grok-4.7", "reasoning": map[string]string{"effort": "high"}, "input": input, "tools": []any{map[string]any{"type": "namespace", "name": "functions", "tools": []any{tool}}}}
	service := New()
	execute := func() map[string]any {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		response, err := service.Execute(context.Background(), pluginapi.ExecutorRequest{AuthID: "live-codex-tool", AuthProvider: ProviderID, Model: "grok-4.7", Format: "responses", SourceFormat: "responses", OriginalRequest: raw, Payload: raw, StorageJSON: storage})
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		if err := json.Unmarshal(response.Payload, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := execute()
	var call map[string]any
	for _, item := range first["output"].([]any) {
		item := item.(map[string]any)
		if item["type"] == wantType {
			call = item
		}
	}
	if call == nil || call["name"] != wantName || call["namespace"] != "functions" || stringValue(call["call_id"]) == "" {
		t.Fatalf("wrong caller tool identity: %v", first)
	}
	if custom && !strings.Contains(stringValue(call["input"]), "global.json") {
		t.Fatalf("custom input does not read the requested fixture: %v", call)
	}
	body["input"] = append(append(input, first["output"].([]any)...), map[string]any{"type": wantType + "_output", "call_id": call["call_id"], "output": `{"sdk":{"version":"10.0.100"}}`})
	second := execute()
	var answer strings.Builder
	for _, item := range second["output"].([]any) {
		item := item.(map[string]any)
		if item["type"] == "function_call" || item["type"] == "custom_tool_call" {
			t.Fatalf("tool result was not consumed: %v", item)
		}
		if item["type"] == "message" {
			for _, part := range item["content"].([]any) {
				fmt.Fprint(&answer, part.(map[string]any)["text"])
			}
		}
	}
	if !strings.Contains(answer.String(), "10.0.100") {
		t.Fatalf("tool-result answer = %q", answer.String())
	}
	t.Logf("%s functions.%s completed round trip: %s", wantType, wantName, answer.String())
}
