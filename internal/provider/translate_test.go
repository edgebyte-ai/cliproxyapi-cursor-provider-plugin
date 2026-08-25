package provider

import (
	"encoding/json"
	"strings"
	"testing"
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

func TestNormalizeToolCallIDRemovesWhitespace(t *testing.T) {
	got := normalizeToolCallID("call-1\nfc_2")
	if got != "call-1_fc_2" {
		t.Fatalf("normalized id = %q", got)
	}
}
