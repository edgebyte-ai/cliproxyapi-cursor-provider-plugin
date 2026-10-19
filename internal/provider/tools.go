package provider

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/edgebyte-ai/cliproxyapi-cursor-provider-plugin/internal/pb"
)

type toolIdentity struct {
	Name      string
	Namespace string
	Custom    bool
}

func callerToolName(namespace, name string) string {
	if namespace == "" {
		return name
	}
	return namespace + "__" + name
}

func parseTools(value any) ([]*pb.McpToolDefinition, map[string]toolIdentity, error) {
	tools := make([]*pb.McpToolDefinition, 0)
	identities := make(map[string]toolIdentity)
	var visit func(any, string) error
	visit = func(value any, namespace string) error {
		list, _ := value.([]any)
		for _, item := range list {
			tool, ok := item.(map[string]any)
			if !ok {
				continue
			}
			kind := stringValue(tool["type"])
			if kind == "namespace" {
				name := stringValue(tool["name"])
				if name == "" {
					return &StatusError{Code: "invalid_tool", Message: "tool namespace has no name", HTTPStatus: http.StatusBadRequest}
				}
				if err := visit(tool["tools"], callerToolName(namespace, name)); err != nil {
					return err
				}
				continue
			}
			// Only caller-owned functions can be forwarded through MCP.
			if kind != "" && kind != "function" && kind != "custom" {
				continue
			}
			definition := tool
			if nested, ok := tool["function"].(map[string]any); ok {
				definition = nested
			}
			name := firstNonEmpty(stringValue(definition["name"]), stringValue(tool["name"]))
			if name == "" {
				continue
			}
			wireName := callerToolName(namespace, name)
			if _, exists := identities[wireName]; exists {
				return &StatusError{Code: "duplicate_tool", Message: "caller tool names collide after namespace encoding", HTTPStatus: http.StatusBadRequest}
			}
			schema := definition["parameters"]
			if schema == nil {
				schema = definition["input_schema"]
			}
			custom := kind == "custom" && schema == nil
			description := stringValue(definition["description"])
			if namespace != "" {
				description = namespace + "." + name + ": " + description
			}
			if custom {
				schema = map[string]any{"type": "object", "properties": map[string]any{"input": map[string]any{"type": "string"}}, "required": []string{"input"}, "additionalProperties": false}
				if format := definition["format"]; format != nil {
					raw, err := json.Marshal(format)
					if err != nil {
						return err
					}
					description += "\nPass the raw tool input in the input string. Input format: " + string(raw)
				}
			}
			if schema == nil {
				schema = map[string]any{"type": "object"}
			}
			raw, err := json.Marshal(schema)
			if err != nil {
				return &StatusError{Code: "invalid_tool", Message: "tool schema is not valid JSON", HTTPStatus: http.StatusBadRequest}
			}
			text := string(raw)
			tools = append(tools, &pb.McpToolDefinition{Name: wireName, ToolName: wireName, ProviderIdentifier: "openai-caller", Description: description, InputSchemaJson: &text})
			identities[wireName] = toolIdentity{Name: name, Namespace: namespace, Custom: custom}
		}
		return nil
	}
	if err := visit(value, ""); err != nil {
		return nil, nil, err
	}
	return tools, identities, nil
}

func resolveToolIdentity(args *pb.McpArgs, identities map[string]toolIdentity) (toolIdentity, error) {
	for _, name := range []string{args.GetToolName(), args.GetName()} {
		if identity, ok := identities[name]; ok {
			return identity, nil
		}
	}
	return toolIdentity{}, &StatusError{Code: "unknown_tool", Message: "Cursor requested a tool not declared by the caller", HTTPStatus: http.StatusBadGateway}
}

func responseToolItem(call toolCall) map[string]any {
	item := map[string]any{"id": call.ID, "call_id": call.ID, "name": call.Name, "status": "completed"}
	if call.Namespace != "" {
		item["namespace"] = call.Namespace
	}
	if call.Custom {
		item["type"] = "custom_tool_call"
		item["input"], _ = call.Args["input"].(string)
	} else {
		item["type"] = "function_call"
		arguments, _ := json.Marshal(call.Args)
		item["arguments"] = string(arguments)
	}
	return item
}

func toolCallFromEvent(event TurnEvent) toolCall {
	return toolCall{ID: event.ToolCallID, Name: event.ToolName, Namespace: event.ToolNamespace, Custom: event.ToolCustom, Args: event.ToolArgs}
}

func historyToolName(message map[string]any) string {
	return callerToolName(strings.TrimSpace(stringValue(message["namespace"])), stringValue(message["name"]))
}
