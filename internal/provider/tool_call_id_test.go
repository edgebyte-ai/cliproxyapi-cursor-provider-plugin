package provider

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/edgebyte-ai/cliproxyapi-cursor-provider-plugin/internal/pb"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestToolCallIDsRoundTripWithoutLoss(t *testing.T) {
	for _, original := range []string{
		"call-1", "call-1\nfc_2", " call-1 ", "call/1", "call_1",
		"call_01a15b311bb5726d918ae17b7b415290\nfc_01a15b311bb5726d918ae17b7b415290",
		"call_01a15b311bb5726d918ae17b7b415290_fc_01a15b311bb5726d918ae17b7b415290",
		strings.Repeat("x", 160) + "\nmetadata", encodedToolCallIDPrefix + "Y2FsbC0x",
	} {
		t.Run(original, func(t *testing.T) {
			encoded := normalizeToolCallID(original)
			if !safeToolCallID.MatchString(encoded) {
				t.Fatalf("caller ID contains unsafe characters: %q", encoded)
			}
			if restored := restoreToolCallID(encoded); restored != original {
				t.Fatalf("restored ID = %q, want %q", restored, original)
			}
		})
	}
	if normalizeToolCallID("call/1") == normalizeToolCallID("call_1") {
		t.Fatal("distinct provider IDs collided")
	}
	if got := normalizeToolCallID(""); !strings.HasPrefix(got, "call_") || !safeToolCallID.MatchString(got) {
		t.Fatalf("empty ID did not receive a valid fallback: %q", got)
	}
}

func TestRestoreLegacyMuseToolCallID(t *testing.T) {
	const part = "01a15b311bb5726d918ae17b7b415290"
	legacy := "call_" + part + "_fc_" + part
	if got := restoreToolCallID(legacy); got != "call_"+part+"\nfc_"+part {
		t.Fatalf("legacy ID was not recovered: %q", got)
	}
	for _, unchanged := range []string{
		"call-1_fc_2", encodedToolCallIDPrefix, encodedToolCallIDPrefix + "invalid!",
		"call_" + part + "_fc_" + strings.Repeat("a", 32),
	} {
		if got := restoreToolCallID(unchanged); got != unchanged {
			t.Fatalf("unrecognized caller ID changed: %q -> %q", unchanged, got)
		}
	}
}

func TestCursorToolIDSurvivesCallerHistory(t *testing.T) {
	const providerID = "call_01a15b311bb5726d918ae17b7b415290\nfc_01a15b311bb5726d918ae17b7b415290"
	events := make(chan TurnEvent, 2)
	err := handleServerMessage(&pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{
		ExecServerMessage: &pb.ExecServerMessage{Message: &pb.ExecServerMessage_McpArgs{McpArgs: &pb.McpArgs{
			ToolName: "get_weather", ToolCallId: providerID,
		}}},
	}}, nil, nil, map[string]toolIdentity{"get_weather": {Name: "get_weather"}}, nil, events)
	if !errors.Is(err, errTurnFinished) {
		t.Fatal(err)
	}
	call := toolCallFromEvent(<-events)
	for _, format := range []string{"responses", "openai"} {
		for _, legacy := range []bool{false, true} {
			name := format
			if legacy {
				name += "-legacy"
			}
			t.Run(name, func(t *testing.T) {
				callerCall := call
				if legacy {
					callerCall.ID = strings.ReplaceAll(providerID, "\n", "_")
				}
				body := map[string]any{"model": "muse-spark-1.3"}
				if format == "responses" {
					body["input"] = []any{
						map[string]any{"role": "user", "content": "Get the weather."},
						responseToolItem(callerCall),
						map[string]any{"type": "function_call_output", "call_id": callerCall.ID, "output": "23C"},
					}
				} else {
					body["messages"] = []any{
						map[string]any{"role": "user", "content": "Get the weather."},
						map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
							"id": callerCall.ID, "type": "function", "function": map[string]any{"name": callerCall.Name, "arguments": `{}`},
						}}},
						map[string]any{"role": "tool", "tool_call_id": callerCall.ID, "content": "23C"},
					}
				}
				raw, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				parsed, err := parseExecutorRequest(pluginapi.ExecutorRequest{SourceFormat: format, Format: format, OriginalRequest: raw}, DefaultConfig())
				if err != nil {
					t.Fatal(err)
				}
				if len(parsed.Input.RootMessages) != 3 {
					t.Fatalf("unexpected history: %q", parsed.Input.RootMessages)
				}
				for i, wantType := range []string{"tool-call", "tool-result"} {
					var message struct {
						Content []struct {
							Type       string `json:"type"`
							ToolCallID string `json:"toolCallId"`
							ToolName   string `json:"toolName"`
						} `json:"content"`
					}
					if err := json.Unmarshal(parsed.Input.RootMessages[i+1], &message); err != nil {
						t.Fatal(err)
					}
					if len(message.Content) != 1 || message.Content[0].Type != wantType || message.Content[0].ToolCallID != providerID || message.Content[0].ToolName != "CallDynamicTool" {
						t.Fatalf("provider history lost call identity: %+v", message)
					}
				}
			})
		}
	}
}
