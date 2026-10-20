package provider

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/edgebyte-ai/cliproxyapi-cursor-provider-plugin/internal/pb"
	"google.golang.org/protobuf/proto"
)

const codexTools = `[
 {"type":"function","name":"exec_command","parameters":{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"]}},
 {"type":"namespace","name":"functions","tools":[
  {"type":"custom","name":"exec","description":"Run JavaScript code.","format":{"type":"text"}},
  {"type":"function","name":"read_file","parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}
 ]},
 {"type":"namespace","name":"mcp__other","tools":[{"type":"function","name":"read_file","parameters":{"type":"object"}}]}
]`

func parseTestTools(t *testing.T) ([]*pb.McpToolDefinition, map[string]toolIdentity) {
	t.Helper()
	var raw any
	if err := json.Unmarshal([]byte(codexTools), &raw); err != nil {
		t.Fatal(err)
	}
	tools, identities, err := parseTools(raw)
	if err != nil {
		t.Fatal(err)
	}
	return tools, identities
}

func TestParseCodexNamespacesAndCustomTools(t *testing.T) {
	tools, identities := parseTestTools(t)
	if len(tools) != 4 || len(identities) != 4 {
		t.Fatalf("unexpected caller catalog: %v %v", tools, identities)
	}
	if _, ok := identities["functions"]; ok {
		t.Fatal("namespace container became a callable tool")
	}
	if identities["functions__exec"] != (toolIdentity{Name: "exec", Namespace: "functions", Custom: true}) {
		t.Fatalf("custom identity lost: %+v", identities)
	}
	if identities["functions__read_file"].Namespace == identities["mcp__other__read_file"].Namespace {
		t.Fatal("same-named tools in different namespaces collided")
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(tools[1].GetInputSchemaJson()), &schema); err != nil {
		t.Fatal(err)
	}
	input := schema["properties"].(map[string]any)["input"].(map[string]any)
	if input["type"] != "string" || schema["required"].([]any)[0] != "input" || schema["additionalProperties"] != false {
		t.Fatalf("custom input schema = %v", schema)
	}
	if !strings.Contains(tools[1].Description, "functions.exec") {
		t.Fatal("tool description omitted its caller identity")
	}
}

func TestParseToolsRejectsEncodedNameCollision(t *testing.T) {
	var raw any
	if err := json.Unmarshal([]byte(`[{"type":"function","name":"functions__exec"},{"type":"namespace","name":"functions","tools":[{"type":"custom","name":"exec"}]}]`), &raw); err != nil {
		t.Fatal(err)
	}
	if _, _, err := parseTools(raw); err == nil {
		t.Fatal("ambiguous caller tool identities were accepted")
	}
}

func TestMCPRestoresDeclaredToolIdentity(t *testing.T) {
	tools, identities := parseTestTools(t)
	for _, name := range []string{"exec_command", "functions__read_file", "functions__exec", "undeclared"} {
		t.Run(name, func(t *testing.T) {
			args := &pb.McpArgs{ToolName: name, ToolCallId: "call-1"}
			if name == "functions__exec" {
				raw, err := proto.Marshal(&pb.Value{Kind: &pb.Value_StringValue{StringValue: `text("probe")`}})
				if err != nil {
					t.Fatal(err)
				}
				args.Args = map[string][]byte{"input": raw}
			}
			message := &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Message: &pb.ExecServerMessage_McpArgs{McpArgs: args}}}}
			events := make(chan TurnEvent, 2)
			err := handleServerMessage(message, nil, tools, identities, nil, events)
			if name == "undeclared" {
				var status *StatusError
				if !errors.As(err, &status) || status.HTTPStatus != http.StatusBadGateway || len(events) != 0 {
					t.Fatalf("undeclared call reached the client: %v", err)
				}
				return
			}
			if !errors.Is(err, errTurnFinished) {
				t.Fatal(err)
			}
			call := <-events
			identity := identities[name]
			if call.ToolName != identity.Name || call.ToolNamespace != identity.Namespace || call.ToolCustom != identity.Custom {
				t.Fatalf("wrong caller identity: %+v", call)
			}
			if call.ToolCustom && call.ToolArgs["input"] != `text("probe")` {
				t.Fatalf("custom input changed: %+v", call)
			}
		})
	}
}

func TestResponsesToolIdentityAndCustomInputSurviveStreaming(t *testing.T) {
	for _, custom := range []bool{false, true} {
		event := TurnEvent{Type: "tool_call", ToolCallID: "call-1", ToolName: "exec", ToolNamespace: "functions", ToolCustom: custom, ToolArgs: map[string]any{"input": `text("probe")`}}
		payload, err := nonStreamingPayload("openai-response", "grok-4.7", collectedTurn{ToolCalls: []toolCall{toolCallFromEvent(event)}}, 1)
		if err != nil {
			t.Fatal(err)
		}
		var response struct{ Output []map[string]any }
		if err := json.Unmarshal(payload, &response); err != nil {
			t.Fatal(err)
		}
		wantType, inputField, eventPrefix := "function_call", "arguments", "response.function_call_arguments"
		if custom {
			wantType, inputField, eventPrefix = "custom_tool_call", "input", "response.custom_tool_call_input"
		}
		check := func(item map[string]any) {
			t.Helper()
			if item["type"] != wantType || item["namespace"] != "functions" || item["name"] != "exec" || item[inputField] == "" {
				t.Fatalf("wrong tool item: %v", item)
			}
			if custom && item["input"] != `text("probe")` {
				t.Fatalf("custom input wrapped in JSON: %v", item)
			}
		}
		check(response.Output[0])
		state := newStreamState("openai-response", "grok-4.7", 1)
		frames, err := state.frames(event)
		if err != nil {
			t.Fatal(err)
		}
		done, err := state.frames(TurnEvent{Type: "done", DoneReason: "tool_calls"})
		if err != nil {
			t.Fatal(err)
		}
		types := map[string]bool{}
		for _, frame := range append(frames, done...) {
			for _, line := range strings.Split(string(frame), "\n") {
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				var data map[string]any
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data); err != nil {
					t.Fatal(err)
				}
				types[data["type"].(string)] = true
				if data["type"] == "response.output_item.done" {
					item := data["item"].(map[string]any)
					if item["type"] != "message" {
						check(item)
					}
				}
				if data["type"] == "response.completed" {
					items := data["response"].(map[string]any)["output"].([]any)
					check(items[1].(map[string]any))
				}
			}
		}
		if !types[eventPrefix+".delta"] || !types[eventPrefix+".done"] || !types["response.completed"] {
			t.Fatalf("incomplete tool stream: %v", types)
		}
	}
}

func TestResponsesToolHistoryKeepsNamespaceAndRawInput(t *testing.T) {
	messages := []any{
		map[string]any{"type": "function_call", "call_id": "function-1", "namespace": "functions", "name": "read_file", "arguments": `{"path":"global.json"}`},
		map[string]any{"type": "function_call_output", "call_id": "function-1", "output": "SDK version"},
		map[string]any{"type": "custom_tool_call", "call_id": "custom-1", "namespace": "functions", "name": "exec", "input": `text("probe")`},
		map[string]any{"type": "custom_tool_call_output", "call_id": "custom-1", "output": "probe"},
	}
	got := normalizeHistoryMessages(messages)
	if len(got) != 4 {
		t.Fatalf("lost tool history: %v", got)
	}
	for i, name := range []string{"functions__read_file", "functions__read_file", "functions__exec", "functions__exec"} {
		part := got[i].(map[string]any)["content"].([]any)[0].(map[string]any)
		if part["toolName"] != name {
			t.Fatalf("history item %d lost tool identity: %v", i, part)
		}
		if i == 2 && part["args"].(map[string]any)["input"] != `text("probe")` {
			t.Fatalf("custom history input changed: %v", part)
		}
	}
	got = cursorToolHistory(got)
	for i, name := range []string{"functions__read_file", "", "functions__exec", ""} {
		part := got[i].(map[string]any)["content"].([]any)[0].(map[string]any)
		if part["toolName"] != "CallDynamicTool" {
			t.Fatalf("history item %d omitted Cursor's native tool wrapper: %v", i, part)
		}
		if name == "" {
			if part["result"] != messages[i].(map[string]any)["output"] {
				t.Fatalf("tool result changed: %v", part)
			}
			continue
		}
		args := part["args"].(map[string]any)
		if args["namespace"] != "openai-caller" || args["toolName"] != name {
			t.Fatalf("history item %d lost its MCP destination: %v", i, part)
		}
		arguments := args["arguments"].(map[string]any)
		if (i == 0 && arguments["path"] != "global.json") || (i == 2 && arguments["input"] != `text("probe")`) {
			t.Fatalf("history arguments changed: %v", part)
		}
	}
}
