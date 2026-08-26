package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type collectedTurn struct {
	Text       string
	Thinking   string
	ToolCalls  []toolCall
	Tokens     int
	DoneReason string
}

type toolCall struct {
	ID   string
	Name string
	Args map[string]any
}

// PreparedStream holds a Cursor stream after the upstream has produced its
// first deliverable or successful terminal event. Errors received before that
// boundary remain synchronous so CLIProxyAPI can fail over to another auth.
type PreparedStream struct {
	headers http.Header
	events  <-chan TurnEvent
	pending []TurnEvent
	state   *streamState
}

func (s *Service) Execute(ctx context.Context, req pluginapi.ExecutorRequest) (pluginapi.ExecutorResponse, error) {
	storage, err := decodeAuth(req.StorageJSON)
	if err != nil {
		return pluginapi.ExecutorResponse{}, err
	}
	parsed, err := parseExecutorRequest(req, s.Config())
	if err != nil {
		return pluginapi.ExecutorResponse{}, err
	}
	if err := ensureModel(parsed.Model); err != nil {
		return pluginapi.ExecutorResponse{}, &StatusError{Code: "invalid_model", Message: err.Error(), HTTPStatus: http.StatusBadRequest}
	}
	model, err := s.resolveModel(ctx, storage, parsed.Model, parsed.Effort)
	if err != nil {
		return pluginapi.ExecutorResponse{}, err
	}
	result, err := retryTransient(ctx, s.Config(), func() (collectedTurn, error) {
		events, runErr := s.runTurn(ctx, storage, model, parsed.Input)
		if runErr != nil {
			return collectedTurn{}, runErr
		}
		return collectTurn(events)
	})
	if err != nil {
		return pluginapi.ExecutorResponse{}, err
	}
	payload, err := nonStreamingPayload(parsed.ResponseFormat, parsed.Model, result, s.now().Unix())
	if err != nil {
		return pluginapi.ExecutorResponse{}, err
	}
	return pluginapi.ExecutorResponse{Payload: payload, Headers: jsonHeaders(), Metadata: map[string]any{"cursor_model": model, "reasoning_effort": parsed.Effort}}, nil
}

func (s *Service) ExecuteStream(ctx context.Context, req pluginapi.ExecutorRequest, emit func([]byte) error) (http.Header, error) {
	prepared, err := s.PrepareStream(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := prepared.Pump(emit); err != nil {
		return nil, err
	}
	return prepared.Headers(), nil
}

// PrepareStream validates the request and waits until Cursor either rejects it
// or produces an event that commits the response stream.
func (s *Service) PrepareStream(ctx context.Context, req pluginapi.ExecutorRequest) (*PreparedStream, error) {
	storage, err := decodeAuth(req.StorageJSON)
	if err != nil {
		return nil, err
	}
	parsed, err := parseExecutorRequest(req, s.Config())
	if err != nil {
		return nil, err
	}
	model, err := s.resolveModel(ctx, storage, parsed.Model, parsed.Effort)
	if err != nil {
		return nil, err
	}
	return retryTransient(ctx, s.Config(), func() (*PreparedStream, error) {
		events, runErr := s.runTurn(ctx, storage, model, parsed.Input)
		if runErr != nil {
			return nil, runErr
		}
		return prepareEventStream(parsed.ResponseFormat, parsed.Model, s.now().Unix(), events)
	})
}

func retryTransient[T any](ctx context.Context, cfg Config, operation func() (T, error)) (T, error) {
	var zero T
	if ctx == nil {
		ctx = context.Background()
	}
	for attempt := 0; ; attempt++ {
		result, err := operation()
		if err == nil {
			return result, nil
		}
		if attempt >= cfg.TransientRetryCount || !isTransientCursorError(ctx, err) {
			return zero, err
		}
		delay := cfg.TransientRetryDelay() * time.Duration(1<<attempt)
		if delay <= 0 {
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
}

func isTransientCursorError(ctx context.Context, err error) bool {
	if err == nil || (ctx != nil && ctx.Err() != nil) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var statusErr *StatusError
	if errors.As(err, &statusErr) {
		return isTransientHTTPStatus(statusErr.HTTPStatus)
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

func prepareEventStream(format, model string, created int64, events <-chan TurnEvent) (*PreparedStream, error) {
	pending := make([]TurnEvent, 0, 2)
	for event := range events {
		if event.Type == "done" && event.Err != nil {
			return nil, event.Err
		}
		pending = append(pending, event)
		if streamEventCommits(format, event) {
			headers := make(http.Header)
			headers.Set("Content-Type", "text/event-stream")
			headers.Set("Cache-Control", "no-cache")
			return &PreparedStream{
				headers: headers,
				events:  events,
				pending: pending,
				state:   newStreamState(format, model, created),
			}, nil
		}
	}
	return nil, &StatusError{
		Code:       "cursor_stream_empty",
		Message:    "Cursor stream closed before producing a response",
		HTTPStatus: http.StatusBadGateway,
		Retryable:  true,
	}
}

func streamEventCommits(format string, event TurnEvent) bool {
	switch event.Type {
	case "text", "tool_call", "done":
		return true
	case "thinking":
		return format != "openai-response"
	default:
		return false
	}
}

// Headers returns a defensive copy of the downstream stream headers.
func (p *PreparedStream) Headers() http.Header {
	if p == nil {
		return nil
	}
	return p.headers.Clone()
}

// Pump converts all buffered and subsequent Cursor events to downstream SSE.
func (p *PreparedStream) Pump(emit func([]byte) error) error {
	if p == nil || p.state == nil {
		return &StatusError{Code: "cursor_stream_unprepared", Message: "Cursor stream was not prepared", HTTPStatus: http.StatusInternalServerError}
	}
	if emit == nil {
		return &StatusError{Code: "cursor_stream_emitter_missing", Message: "Cursor stream emitter is required", HTTPStatus: http.StatusInternalServerError}
	}
	consume := func(event TurnEvent) (bool, error) {
		if event.Type == "done" && event.Err != nil {
			return true, event.Err
		}
		frames, frameErr := p.state.frames(event)
		if frameErr != nil {
			return true, frameErr
		}
		for _, frame := range frames {
			if err := emit(frame); err != nil {
				return true, err
			}
		}
		if event.Type == "done" {
			return true, nil
		}
		return false, nil
	}
	for _, event := range p.pending {
		done, err := consume(event)
		if done || err != nil {
			p.pending = nil
			return err
		}
	}
	p.pending = nil
	for event := range p.events {
		done, err := consume(event)
		if done || err != nil {
			return err
		}
	}
	return &StatusError{
		Code:       "cursor_stream_incomplete",
		Message:    "Cursor stream ended without a terminal event",
		HTTPStatus: http.StatusBadGateway,
		Retryable:  true,
	}
}

func (s *Service) CountTokens(_ context.Context, req pluginapi.ExecutorRequest) (pluginapi.ExecutorResponse, error) {
	raw := req.OriginalRequest
	if len(raw) == 0 {
		raw = req.Payload
	}
	// Cursor does not expose a tokenizer endpoint. This deterministic approximation is used only for client preflight.
	tokens := (len(raw) + 3) / 4
	payload, _ := json.Marshal(map[string]any{"total_tokens": tokens})
	return pluginapi.ExecutorResponse{Payload: payload, Headers: jsonHeaders()}, nil
}

func (s *Service) HttpRequest(context.Context, pluginapi.ExecutorHTTPRequest) (pluginapi.ExecutorHTTPResponse, error) {
	return pluginapi.ExecutorHTTPResponse{}, &StatusError{Code: "unsupported_http", Message: "Cursor plugin does not proxy arbitrary HTTP requests", HTTPStatus: http.StatusForbidden}
}

func collectTurn(events <-chan TurnEvent) (collectedTurn, error) {
	result := collectedTurn{DoneReason: "stop"}
	for event := range events {
		switch event.Type {
		case "text":
			result.Text += event.Text
		case "thinking":
			result.Thinking += event.Text
		case "tool_call":
			result.ToolCalls = append(result.ToolCalls, toolCall{ID: event.ToolCallID, Name: event.ToolName, Args: event.ToolArgs})
		case "usage":
			result.Tokens = event.Tokens
		case "done":
			if event.Err != nil {
				return collectedTurn{}, event.Err
			}
			result.DoneReason = event.DoneReason
		}
	}
	return result, nil
}

func nonStreamingPayload(format, model string, result collectedTurn, created int64) ([]byte, error) {
	switch format {
	case "openai-chat":
		message := map[string]any{"role": "assistant", "content": nullableText(result.Text)}
		if result.Thinking != "" {
			message["reasoning_content"] = result.Thinking
		}
		if len(result.ToolCalls) > 0 {
			message["tool_calls"] = openAIToolCalls(result.ToolCalls)
		}
		return json.Marshal(map[string]any{
			"id": "chatcmpl-" + mustUUID(), "object": "chat.completion", "created": created, "model": model,
			"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finishReason(result)}},
			"usage":   usageBlock(result.Tokens),
		})
	case "openai-response":
		outputs := make([]any, 0, 1+len(result.ToolCalls))
		if result.Text != "" || result.Thinking != "" {
			content := []any{map[string]any{"type": "output_text", "text": result.Text, "annotations": []any{}}}
			outputs = append(outputs, map[string]any{"id": "msg_" + mustUUID(), "type": "message", "status": "completed", "role": "assistant", "content": content})
		}
		for _, call := range result.ToolCalls {
			arguments, _ := json.Marshal(call.Args)
			outputs = append(outputs, map[string]any{"id": call.ID, "call_id": call.ID, "type": "function_call", "name": call.Name, "arguments": string(arguments), "status": "completed"})
		}
		return json.Marshal(map[string]any{
			"id": "resp_" + mustUUID(), "object": "response", "created_at": created, "status": "completed", "model": model,
			"output": outputs, "usage": responseUsageBlock(result.Tokens),
		})
	case "claude":
		content := make([]any, 0, 2+len(result.ToolCalls))
		if result.Thinking != "" {
			content = append(content, map[string]any{"type": "thinking", "thinking": result.Thinking})
		}
		if result.Text != "" {
			content = append(content, map[string]any{"type": "text", "text": result.Text})
		}
		for _, call := range result.ToolCalls {
			content = append(content, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": call.Args})
		}
		return json.Marshal(map[string]any{
			"id": "msg_" + mustUUID(), "type": "message", "role": "assistant", "model": model,
			"content": content, "stop_reason": map[bool]string{true: "tool_use", false: "end_turn"}[len(result.ToolCalls) > 0],
			"usage": map[string]any{"input_tokens": 0, "output_tokens": result.Tokens},
		})
	default:
		return nil, fmt.Errorf("unsupported response format %q", format)
	}
}

type streamState struct {
	format    string
	model     string
	created   int64
	id        string
	opened    bool
	toolIdx   int
	seq       int
	text      string
	reasoning string
	messageID string
	toolCalls []toolCall
}

func newStreamState(format, model string, created int64) *streamState {
	return &streamState{format: format, model: model, created: created, id: mustUUID(), messageID: "msg_" + mustUUID()}
}

func (s *streamState) frames(event TurnEvent) ([][]byte, error) {
	switch s.format {
	case "openai-chat":
		return s.openAIChatFrames(event)
	case "openai-response":
		return s.openAIResponseFrames(event)
	case "claude":
		return s.claudeFrames(event)
	default:
		return nil, fmt.Errorf("unsupported stream format %q", s.format)
	}
}

func (s *streamState) openAIChatFrames(event TurnEvent) ([][]byte, error) {
	frames := make([][]byte, 0, 2)
	if !s.opened {
		s.opened = true
		frames = append(frames, sseData(map[string]any{"id": "chatcmpl-" + s.id, "object": "chat.completion.chunk", "created": s.created, "model": s.model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant"}, "finish_reason": nil}}}))
	}
	switch event.Type {
	case "text":
		frames = append(frames, sseData(chatChunk(s, map[string]any{"content": event.Text}, nil)))
	case "thinking":
		frames = append(frames, sseData(chatChunk(s, map[string]any{"reasoning_content": event.Text}, nil)))
	case "tool_call":
		arguments, _ := json.Marshal(event.ToolArgs)
		frames = append(frames, sseData(chatChunk(s, map[string]any{"tool_calls": []any{map[string]any{"index": s.toolIdx, "id": event.ToolCallID, "type": "function", "function": map[string]any{"name": event.ToolName, "arguments": string(arguments)}}}}, nil)))
		s.toolIdx++
	case "done":
		frames = append(frames, sseData(chatChunk(s, map[string]any{}, map[bool]string{true: "tool_calls", false: "stop"}[event.DoneReason == "tool_calls"])), []byte("data: [DONE]\n\n"))
	}
	return frames, nil
}

func (s *streamState) openAIResponseFrames(event TurnEvent) ([][]byte, error) {
	frames := make([][]byte, 0, 5)
	responseID := "resp_" + s.id
	if !s.opened {
		s.opened = true
		frames = append(frames,
			s.responseEvent("response.created", map[string]any{"response": map[string]any{"id": responseID, "object": "response", "created_at": s.created, "status": "in_progress", "model": s.model, "output": []any{}}}),
			s.responseEvent("response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"id": s.messageID, "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}}}),
			s.responseEvent("response.content_part.added", map[string]any{"item_id": s.messageID, "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}}),
		)
	}
	switch event.Type {
	case "text":
		s.text += event.Text
		frames = append(frames, s.responseEvent("response.output_text.delta", map[string]any{"item_id": s.messageID, "output_index": 0, "content_index": 0, "delta": event.Text, "logprobs": []any{}}))
	case "thinking":
		// Cursor exposes thinking deltas, but Responses clients require a complete
		// reasoning-item lifecycle. Keep them for the terminal response metadata
		// instead of emitting orphan summary deltas.
		s.reasoning += event.Text
	case "tool_call":
		s.toolCalls = append(s.toolCalls, toolCall{ID: event.ToolCallID, Name: event.ToolName, Args: event.ToolArgs})
		arguments, _ := json.Marshal(event.ToolArgs)
		item := map[string]any{"id": event.ToolCallID, "call_id": event.ToolCallID, "type": "function_call", "name": event.ToolName, "arguments": string(arguments), "status": "completed"}
		frames = append(frames,
			s.responseEvent("response.output_item.added", map[string]any{"output_index": len(s.toolCalls), "item": item}),
			s.responseEvent("response.output_item.done", map[string]any{"output_index": len(s.toolCalls), "item": item}),
		)
	case "done":
		message := map[string]any{"id": s.messageID, "type": "message", "status": "completed", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": s.text, "annotations": []any{}}}}
		outputs := []any{message}
		for _, call := range s.toolCalls {
			arguments, _ := json.Marshal(call.Args)
			outputs = append(outputs, map[string]any{"id": call.ID, "call_id": call.ID, "type": "function_call", "name": call.Name, "arguments": string(arguments), "status": "completed"})
		}
		frames = append(frames,
			s.responseEvent("response.output_text.done", map[string]any{"item_id": s.messageID, "output_index": 0, "content_index": 0, "text": s.text, "logprobs": []any{}}),
			s.responseEvent("response.content_part.done", map[string]any{"item_id": s.messageID, "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": s.text, "annotations": []any{}}}),
			s.responseEvent("response.output_item.done", map[string]any{"output_index": 0, "item": message}),
			s.responseEvent("response.completed", map[string]any{"response": map[string]any{"id": responseID, "object": "response", "created_at": s.created, "status": "completed", "model": s.model, "output": outputs, "usage": responseUsageBlock(event.Tokens)}}),
		)
	}
	return frames, nil
}

func (s *streamState) responseEvent(name string, fields map[string]any) []byte {
	fields["type"] = name
	fields["sequence_number"] = s.seq
	s.seq++
	return sseEvent(name, fields)
}

func (s *streamState) claudeFrames(event TurnEvent) ([][]byte, error) {
	frames := make([][]byte, 0, 2)
	if !s.opened {
		s.opened = true
		frames = append(frames, sseEvent("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_" + s.id, "type": "message", "role": "assistant", "model": s.model, "content": []any{}, "usage": map[string]any{"input_tokens": 0, "output_tokens": 0}}}))
	}
	switch event.Type {
	case "text":
		frames = append(frames, sseEvent("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": event.Text}}))
	case "thinking":
		frames = append(frames, sseEvent("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "thinking_delta", "thinking": event.Text}}))
	case "tool_call":
		frames = append(frames, sseEvent("content_block_start", map[string]any{"type": "content_block_start", "index": s.toolIdx + 1, "content_block": map[string]any{"type": "tool_use", "id": event.ToolCallID, "name": event.ToolName, "input": event.ToolArgs}}))
		s.toolIdx++
	case "done":
		frames = append(frames, sseEvent("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": map[bool]string{true: "tool_use", false: "end_turn"}[event.DoneReason == "tool_calls"]}, "usage": map[string]any{"output_tokens": event.Tokens}}), sseEvent("message_stop", map[string]any{"type": "message_stop"}))
	}
	return frames, nil
}

func chatChunk(state *streamState, delta map[string]any, finish any) map[string]any {
	return map[string]any{"id": "chatcmpl-" + state.id, "object": "chat.completion.chunk", "created": state.created, "model": state.model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
}

func sseData(payload any) []byte {
	raw, _ := json.Marshal(payload)
	return []byte("data: " + string(raw) + "\n\n")
}

func sseEvent(name string, payload any) []byte {
	raw, _ := json.Marshal(payload)
	return []byte("event: " + name + "\ndata: " + string(raw) + "\n\n")
}

func openAIToolCalls(calls []toolCall) []any {
	out := make([]any, 0, len(calls))
	for _, call := range calls {
		arguments, _ := json.Marshal(call.Args)
		out = append(out, map[string]any{"id": call.ID, "type": "function", "function": map[string]any{"name": call.Name, "arguments": string(arguments)}})
	}
	return out
}

func finishReason(result collectedTurn) string {
	if len(result.ToolCalls) > 0 || result.DoneReason == "tool_calls" {
		return "tool_calls"
	}
	return "stop"
}

func nullableText(text string) any {
	if text == "" {
		return nil
	}
	return text
}

func usageBlock(tokens int) map[string]any {
	return map[string]any{"prompt_tokens": 0, "completion_tokens": tokens, "total_tokens": tokens}
}

func responseUsageBlock(tokens int) map[string]any {
	return map[string]any{"input_tokens": 0, "output_tokens": tokens, "total_tokens": tokens}
}

func jsonHeaders() http.Header {
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	return headers
}

func retryAfterSeconds(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	seconds := int64(duration / time.Second)
	if duration%time.Second != 0 {
		seconds++
	}
	return seconds
}

func errorMessage(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}
