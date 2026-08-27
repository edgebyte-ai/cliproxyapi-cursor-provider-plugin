package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/edgebyte-ai/cliproxyapi-cursor-provider-plugin/internal/pb"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type parsedRequest struct {
	Model          string
	Effort         string
	Stream         bool
	Input          TurnInput
	ResponseFormat string
}

func parseExecutorRequest(req pluginapi.ExecutorRequest, cfg Config) (parsedRequest, error) {
	raw := req.OriginalRequest
	if len(raw) == 0 {
		raw = req.Payload
	}
	if len(raw) == 0 || len(raw) > cfg.MaxRequestBytes {
		return parsedRequest{}, &StatusError{Code: "invalid_request", Message: "request body is empty or too large", HTTPStatus: http.StatusBadRequest}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		return parsedRequest{}, &StatusError{Code: "invalid_json", Message: "request body is not valid JSON", HTTPStatus: http.StatusBadRequest}
	}
	format := normalizeFormat(firstNonEmpty(req.SourceFormat, req.Format))
	if format == "" {
		return parsedRequest{}, &StatusError{Code: "unsupported_format", Message: "unsupported request format", HTTPStatus: http.StatusUnprocessableEntity}
	}
	model := firstNonEmpty(req.Model, stringValue(root["model"]))
	parsed := parsedRequest{Model: model, Effort: reasoningEffort(root), Stream: req.Stream || boolValue(root["stream"]), ResponseFormat: normalizeFormat(firstNonEmpty(req.Format, req.SourceFormat))}
	if parsed.ResponseFormat == "" {
		parsed.ResponseFormat = format
	}
	input, err := turnInput(format, root, cfg)
	if err != nil {
		return parsedRequest{}, err
	}
	parsed.Input = input
	return parsed, nil
}

func turnInput(format string, root map[string]any, cfg Config) (TurnInput, error) {
	messages := make([]any, 0)
	if system, ok := root["system"]; ok && system != nil {
		messages = append(messages, map[string]any{"role": "system", "content": system})
	}
	switch format {
	case "openai-chat", "claude":
		if list, ok := root["messages"].([]any); ok {
			messages = append(messages, list...)
		}
	case "openai-response":
		switch input := root["input"].(type) {
		case string:
			messages = append(messages, map[string]any{"role": "user", "content": input})
		case []any:
			messages = append(messages, input...)
		}
	}
	tools, err := parseTools(root["tools"])
	if err != nil {
		return TurnInput{}, err
	}
	userText := ""
	activeIndex := -1
	for index := len(messages) - 1; index >= 0; index-- {
		message, ok := messages[index].(map[string]any)
		if !ok {
			continue
		}
		if strings.EqualFold(stringValue(message["type"]), "agent_message") {
			var opaque bool
			userText, opaque = agentMessageText(message["content"])
			if opaque {
				return TurnInput{}, &StatusError{
					Code:       "unsupported_encrypted_agent_message",
					Message:    "Codex agent task content is encrypted and cannot be forwarded to Cursor",
					HTTPStatus: http.StatusUnprocessableEntity,
				}
			}
			activeIndex = index
			break
		}
		if strings.EqualFold(stringValue(message["role"]), "user") {
			userText = contentText(message["content"])
			activeIndex = index
			break
		}
	}
	if activeIndex >= 0 && activeIndex == len(messages)-1 && strings.TrimSpace(userText) != "" {
		messages = messages[:activeIndex]
	} else {
		userText = cfg.ContinuationPrompt
	}
	historyMessages := normalizeHistoryMessages(messages)
	rootMessages := make([][]byte, 0, len(historyMessages))
	for _, message := range historyMessages {
		raw, marshalErr := json.Marshal(message)
		if marshalErr != nil {
			return TurnInput{}, &StatusError{Code: "invalid_message", Message: "message could not be encoded", HTTPStatus: http.StatusBadRequest}
		}
		if len(raw) > cfg.MaxHistoryMessageBytes {
			return TurnInput{}, &StatusError{Code: "message_too_large", Message: "one history message is too large", HTTPStatus: http.StatusRequestEntityTooLarge}
		}
		rootMessages = append(rootMessages, raw)
	}
	return TurnInput{RootMessages: rootMessages, UserText: userText, Tools: tools}, nil
}

func normalizeHistoryMessages(messages []any) []any {
	const systemLeadIn = "Configuration for this session, set by the operator of this API gateway:"
	const systemAck = "Understood. I will follow that configuration for the rest of this conversation."
	out := make([]any, 0, len(messages)+2)
	for _, item := range messages {
		message, ok := item.(map[string]any)
		if !ok {
			continue
		}
		itemType := strings.ToLower(stringValue(message["type"]))
		role := strings.ToLower(stringValue(message["role"]))
		switch itemType {
		case "agent_message":
			text, opaque := agentMessageText(message["content"])
			if text != "" && !opaque {
				out = append(out, map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": text}}})
			}
			continue
		case "function_call":
			callID := firstNonEmpty(stringValue(message["call_id"]), stringValue(message["id"]), normalizeToolCallID(""))
			out = append(out, map[string]any{"role": "assistant", "content": []any{map[string]any{
				"type": "tool-call", "toolCallId": callID, "toolName": stringValue(message["name"]), "args": parseArgumentValue(message["arguments"]),
			}}})
			continue
		case "function_call_output":
			callID := firstNonEmpty(stringValue(message["call_id"]), stringValue(message["id"]))
			out = append(out, map[string]any{"role": "tool", "id": callID, "content": []any{map[string]any{
				"type": "tool-result", "toolCallId": callID, "result": contentText(message["output"]),
			}}})
			continue
		}
		switch role {
		case "system", "developer":
			text := contentText(message["content"])
			if text != "" {
				out = append(out,
					map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": systemLeadIn + "\n\n" + text}}},
					map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": systemAck}}},
				)
			}
		case "user":
			if text := contentText(message["content"]); text != "" {
				out = append(out, map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": text}}})
			}
		case "assistant":
			parts := make([]any, 0)
			if text := contentText(message["content"]); text != "" {
				parts = append(parts, map[string]any{"type": "text", "text": text})
			}
			if calls, ok := message["tool_calls"].([]any); ok {
				for _, rawCall := range calls {
					call, _ := rawCall.(map[string]any)
					function, _ := call["function"].(map[string]any)
					parts = append(parts, map[string]any{
						"type": "tool-call", "toolCallId": stringValue(call["id"]), "toolName": stringValue(function["name"]), "args": parseArgumentValue(function["arguments"]),
					})
				}
			}
			if len(parts) > 0 {
				out = append(out, map[string]any{"role": "assistant", "content": parts})
			}
		case "tool":
			callID := firstNonEmpty(stringValue(message["tool_call_id"]), stringValue(message["id"]))
			out = append(out, map[string]any{"role": "tool", "id": callID, "content": []any{map[string]any{
				"type": "tool-result", "toolName": stringValue(message["name"]), "toolCallId": callID, "result": contentText(message["content"]),
			}}})
		}
	}
	return out
}

// agentMessageText decodes Codex native-subagent messages. Codex currently
// places the task payload in a content part named encrypted_content even when
// the value delivered to an OpenAI-compatible provider is plain text. Opaque
// values are rejected instead of silently dropping the task and asking Cursor
// to continue an unrelated workspace conversation.
func agentMessageText(value any) (string, bool) {
	list, _ := value.([]any)
	parts := make([]string, 0, len(list))
	opaque := false
	for _, item := range list {
		part, ok := item.(map[string]any)
		if !ok {
			continue
		}
		switch strings.ToLower(stringValue(part["type"])) {
		case "encrypted_content":
			payload := stringValue(part["encrypted_content"])
			if payload == "" {
				continue
			}
			if !looksLikePlaintextTask(payload) {
				opaque = true
				continue
			}
			parts = append(parts, payload)
		default:
			if text := firstNonEmpty(stringValue(part["text"]), stringValue(part["content"])); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n"), opaque
}

func looksLikePlaintextTask(value string) bool {
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" {
		return false
	}
	base64Like := true
	hasSpace := false
	for _, r := range value {
		if !unicode.IsPrint(r) && !unicode.IsSpace(r) {
			return false
		}
		if unicode.IsSpace(r) {
			hasSpace = true
		}
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '+' && r != '/' && r != '-' && r != '_' && r != '=' {
			base64Like = false
		}
	}
	if !hasSpace && base64Like && (strings.HasSuffix(value, "=") || len(value) >= 48) {
		return false
	}
	return true
}

func parseArgumentValue(value any) any {
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		if value == nil {
			return map[string]any{}
		}
		return value
	}
	var parsed any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return map[string]any{"_raw": text}
	}
	return parsed
}

func parseTools(value any) ([]*pb.McpToolDefinition, error) {
	list, _ := value.([]any)
	tools := make([]*pb.McpToolDefinition, 0, len(list))
	for _, item := range list {
		tool, ok := item.(map[string]any)
		if !ok {
			continue
		}
		definition := tool
		if nested, ok := tool["function"].(map[string]any); ok {
			definition = nested
		}
		name := firstNonEmpty(stringValue(definition["name"]), stringValue(tool["name"]))
		if name == "" {
			// Built-in Responses tools such as web_search may not have a caller-owned
			// function name. Cursor can only surface MCP-style caller functions, so
			// skip those capability declarations instead of rejecting the whole turn.
			continue
		}
		schema := definition["parameters"]
		if schema == nil {
			schema = definition["input_schema"]
		}
		if schema == nil {
			schema = map[string]any{"type": "object"}
		}
		schemaJSON, _ := json.Marshal(schema)
		schemaText := string(schemaJSON)
		tools = append(tools, &pb.McpToolDefinition{
			Name: name, ToolName: name, ProviderIdentifier: "openai-caller",
			Description: stringValue(definition["description"]), InputSchemaJson: &schemaText,
		})
	}
	return tools, nil
}

func reasoningEffort(root map[string]any) string {
	if value := stringValue(root["reasoning_effort"]); value != "" {
		return strings.ToLower(value)
	}
	if reasoning, ok := root["reasoning"].(map[string]any); ok {
		if value := stringValue(reasoning["effort"]); value != "" {
			return strings.ToLower(value)
		}
	}
	return ""
}

func normalizeFormat(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "openai", "openai-chat", "chat", "chat-completions":
		return "openai-chat"
	case "responses", "openai-response", "openai-responses":
		return "openai-response"
	case "claude", "anthropic", "anthropic-messages":
		return "claude"
	default:
		return ""
	}
}

func contentText(value any) string {
	switch content := value.(type) {
	case string:
		return content
	case []any:
		parts := make([]string, 0, len(content))
		for _, item := range content {
			switch part := item.(type) {
			case string:
				parts = append(parts, part)
			case map[string]any:
				if text := firstNonEmpty(stringValue(part["text"]), stringValue(part["content"])); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		return firstNonEmpty(stringValue(content["text"]), stringValue(content["content"]))
	default:
		return ""
	}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func boolValue(value any) bool {
	flag, _ := value.(bool)
	return flag
}

func ensureModel(model string) error {
	if strings.TrimSpace(model) == "" {
		return fmt.Errorf("model is required")
	}
	return nil
}
