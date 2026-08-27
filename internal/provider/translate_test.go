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

func TestNormalizeToolCallIDRemovesWhitespace(t *testing.T) {
	got := normalizeToolCallID("call-1\nfc_2")
	if got != "call-1_fc_2" {
		t.Fatalf("normalized id = %q", got)
	}
}

func TestParseToolsSkipsUnnamedBuiltins(t *testing.T) {
	tools, err := parseTools([]any{
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
