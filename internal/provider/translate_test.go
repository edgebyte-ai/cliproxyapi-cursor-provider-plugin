package provider

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestNormalizeHistoryBuildsToolCallResultPair(t *testing.T) {
	messages := []any{
		map[string]any{"role": "user", "content": "weather"},
		map[string]any{"role": "assistant", "content": "checking", "tool_calls": []any{map[string]any{"id": "call-1", "function": map[string]any{"name": "get_weather", "arguments": `{"city":"Tokyo"}`}}}},
		map[string]any{"role": "tool", "tool_call_id": "call-1", "content": `{"temp":31}`},
	}
	normalized := normalizeHistoryMessages(messages)
	if len(normalized) != 3 {
		t.Fatalf("unexpected normalized history: %#v", normalized)
	}
	raw, _ := json.Marshal(normalized)
	text := string(raw)
	for _, expected := range []string{`"type":"tool-call"`, `"type":"tool-result"`, `"toolCallId":"call-1"`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing %s in %s", expected, text)
		}
	}
}

func TestResponsesHistoryKeepsToolCallAndCommentaryTogether(t *testing.T) {
	raw := []byte(`{"model":"claude-opus-5-5","input":[
		{"role":"user","content":"Check the weather."},
		{"type":"function_call","call_id":"call-1","name":"get_weather","arguments":"{\"city\":\"Tokyo\"}"},
		{"role":"assistant","content":[{"type":"output_text","text":"Checking the forecast."}]},
		{"type":"function_call_output","call_id":"call-1","output":"Sunny"}
	]}`)
	parsed, err := parseExecutorRequest(pluginapi.ExecutorRequest{
		SourceFormat: "responses", Format: "responses", OriginalRequest: raw,
	}, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Input.RootMessages) != 3 {
		t.Fatalf("history has %d messages, want user, assistant, tool", len(parsed.Input.RootMessages))
	}
	var assistant, result map[string]any
	if err := json.Unmarshal(parsed.Input.RootMessages[1], &assistant); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(parsed.Input.RootMessages[2], &result); err != nil {
		t.Fatal(err)
	}
	parts := assistant["content"].([]any)
	if assistant["role"] != "assistant" || len(parts) != 2 {
		t.Fatalf("tool call and commentary were not combined: %#v", assistant)
	}
	call, commentary := parts[0].(map[string]any), parts[1].(map[string]any)
	args := call["args"].(map[string]any)
	if call["type"] != "tool-call" || call["toolCallId"] != "call-1" || call["toolName"] != "CallDynamicTool" || args["namespace"] != "openai-caller" || args["toolName"] != "get_weather" || args["arguments"].(map[string]any)["city"] != "Tokyo" {
		t.Fatalf("tool call changed: %#v", call)
	}
	if commentary["type"] != "text" || commentary["text"] != "Checking the forecast." {
		t.Fatalf("commentary order or content changed: %#v", commentary)
	}
	resultPart := result["content"].([]any)[0].(map[string]any)
	if result["role"] != "tool" || resultPart["toolCallId"] != "call-1" || resultPart["toolName"] != "CallDynamicTool" || resultPart["result"] != "Sunny" {
		t.Fatalf("tool result changed: %#v", result)
	}
}

func TestChatHistoryGroupsParallelToolResults(t *testing.T) {
	messages := []any{
		map[string]any{"role": "user", "content": "Check both cities."},
		map[string]any{"role": "assistant", "tool_calls": []any{
			map[string]any{"id": "call-1", "function": map[string]any{"name": "first_weather", "arguments": `{}`}},
			map[string]any{"id": "call-2", "function": map[string]any{"name": "second_weather", "arguments": `{}`}},
		}},
		map[string]any{"role": "tool", "tool_call_id": "call-1", "content": "Sunny"},
		map[string]any{"role": "tool", "tool_call_id": "call-2", "content": "Cloudy"},
		map[string]any{"role": "assistant", "content": "Both forecasts are ready."},
		map[string]any{"role": "user", "content": "Thanks."},
		map[string]any{"role": "assistant", "content": "You're welcome."},
	}
	normalized := normalizeHistoryMessages(messages)
	if len(normalized) != 6 {
		t.Fatalf("history has %d messages, want 6 with grouped results", len(normalized))
	}
	for i, role := range []string{"user", "assistant", "tool", "assistant", "user", "assistant"} {
		if normalized[i].(map[string]any)["role"] != role {
			t.Fatalf("message %d crossed a conversation boundary: %#v", i, normalized[i])
		}
	}
	results := normalized[2].(map[string]any)["content"].([]any)
	if len(results) != 2 {
		t.Fatalf("parallel results were not combined: %#v", results)
	}
	for i, want := range []struct{ id, name, result string }{
		{"call-1", "first_weather", "Sunny"},
		{"call-2", "second_weather", "Cloudy"},
	} {
		part := results[i].(map[string]any)
		if part["type"] != "tool-result" || part["toolCallId"] != want.id || part["toolName"] != want.name || part["result"] != want.result {
			t.Fatalf("parallel result %d changed: %#v", i, part)
		}
	}
}

func TestParseResponsesUsesCodexAgentMessageAsActiveTask(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"model":  "grok-4.6",
		"stream": true,
		"input": []any{
			map[string]any{"role": "developer", "content": "Follow the workspace rules."},
			map[string]any{
				"type": "agent_message",
				"content": []any{
					map[string]any{"type": "input_text", "text": "Message Type: NEW_TASK\nPayload:"},
					map[string]any{"type": "encrypted_content", "encrypted_content": "Review issue #7 and return Score: N/10 immediately."},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseExecutorRequest(pluginapi.ExecutorRequest{
		Model: "grok-4.6", SourceFormat: "responses", Format: "responses", OriginalRequest: raw,
	}, DefaultConfig())
	if err != nil {
		t.Fatalf("parseExecutorRequest() error = %v", err)
	}
	for _, expected := range []string{"Message Type: NEW_TASK", "Review issue #7", "Score: N/10"} {
		if !strings.Contains(parsed.Input.UserText, expected) {
			t.Fatalf("UserText omitted %q: %q", expected, parsed.Input.UserText)
		}
	}
	if strings.Contains(parsed.Input.UserText, DefaultConfig().ContinuationPrompt) {
		t.Fatalf("UserText used continuation prompt: %q", parsed.Input.UserText)
	}
	if len(parsed.Input.RootMessages) != 2 {
		t.Fatalf("RootMessages = %d, want developer user/assistant pair only", len(parsed.Input.RootMessages))
	}
}

func TestParseResponsesRejectsOpaqueCodexAgentTask(t *testing.T) {
	raw := []byte(`{"model":"grok-4.6","input":[{"type":"agent_message","content":[{"type":"input_text","text":"Message Type: NEW_TASK\\nPayload:"},{"type":"encrypted_content","encrypted_content":"YWJjZGVmZ2hpamtsbW5vcA=="}]}]}`)
	_, err := parseExecutorRequest(pluginapi.ExecutorRequest{
		Model: "grok-4.6", SourceFormat: "responses", Format: "responses", OriginalRequest: raw,
	}, DefaultConfig())
	var statusErr *StatusError
	if !errors.As(err, &statusErr) || statusErr.Code != "unsupported_encrypted_agent_message" || statusErr.HTTPStatus != http.StatusUnprocessableEntity {
		t.Fatalf("parseExecutorRequest() error = %#v", err)
	}
}

func TestAgentMessageTextAcceptsPlainChineseTask(t *testing.T) {
	text, opaque := agentMessageText([]any{map[string]any{
		"type": "encrypted_content", "encrypted_content": "立即审查第七个问题并返回评分。",
	}})
	if opaque || text != "立即审查第七个问题并返回评分。" {
		t.Fatalf("agentMessageText() = %q, %t", text, opaque)
	}
}

func TestParseToolsSkipsUnnamedBuiltins(t *testing.T) {
	tools, _, err := parseTools([]any{
		map[string]any{"type": "web_search"},
		map[string]any{"type": "function", "name": "shell", "parameters": map[string]any{"type": "object"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].GetName() != "shell" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
}
