package provider

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/edgebyte-ai/cliproxyapi-cursor-provider-plugin/internal/pb"
	"google.golang.org/protobuf/proto"
)

func TestExecutorUsageFromCursorWire(t *testing.T) {
	fullUsage := []byte{0x08, 0xe8, 0x07, 0x10, 0x28, 0x18, 0xbc, 0x05, 0x20, 0x64, 0x28, 0x0c}
	for _, tc := range []struct {
		name   string
		before []TurnEvent
		wire   []byte
		after  []TurnEvent
		want   map[string]map[string]int64
	}{
		{
			name: "terminal counts replace deltas after text",
			before: []TurnEvent{
				{Type: "usage", Tokens: 5}, {Type: "text", Text: "hello"}, {Type: "usage", Tokens: 7},
			},
			wire:  fullUsage,
			after: []TurnEvent{{Type: "usage", Tokens: 99}, {Type: "done", DoneReason: "stop"}},
			want:  exactUsageExpectations(),
		},
		{
			name: "terminal usage before done without text", wire: fullUsage,
			after: []TurnEvent{{Type: "done", DoneReason: "stop"}}, want: exactUsageExpectations(),
		},
		{
			name: "delta fallback survives empty legacy terminal",
			before: []TurnEvent{
				{Type: "usage", Tokens: 3}, {Type: "text", Text: "hello"}, {Type: "usage", Tokens: 4},
			},
			wire: []byte{}, after: []TurnEvent{{Type: "done", DoneReason: "stop"}},
			want: fallbackUsageExpectations(7),
		},
		{
			name: "delta fallback before and after text",
			before: []TurnEvent{
				{Type: "usage", Tokens: 3}, {Type: "text", Text: "hello"}, {Type: "usage", Tokens: 4},
			},
			after: []TurnEvent{{Type: "done", DoneReason: "stop"}}, want: fallbackUsageExpectations(7),
		},
		{
			name: "signed delta correction",
			before: []TurnEvent{
				{Type: "usage", Tokens: 7}, {Type: "text", Text: "hello"}, {Type: "usage", Tokens: -2},
			},
			after: []TurnEvent{{Type: "done", DoneReason: "stop"}}, want: fallbackUsageExpectations(5),
		},
		{
			name: "negative delta balance clamps output",
			before: []TurnEvent{
				{Type: "usage", Tokens: 2}, {Type: "text", Text: "hello"}, {Type: "usage", Tokens: -5},
			},
			after: []TurnEvent{{Type: "done", DoneReason: "stop"}}, want: fallbackUsageExpectations(0),
		},
		{
			name: "tool call delta fallback",
			before: []TurnEvent{
				{Type: "usage", Tokens: 3},
				{Type: "tool_call", ToolCallID: "call_weather", ToolName: "weather", ToolArgs: map[string]any{"city": "Paris"}},
				{Type: "usage", Tokens: 4},
			},
			after: []TurnEvent{{Type: "done", DoneReason: "tool_calls"}}, want: fallbackUsageExpectations(7),
		},
		{
			name:   "explicit zero replaces and suppresses deltas",
			before: []TurnEvent{{Type: "usage", Tokens: 5}, {Type: "text", Text: "hello"}},
			wire:   []byte{0x08, 0x00, 0x10, 0x00},
			after:  []TurnEvent{{Type: "usage", Tokens: 9}, {Type: "done", DoneReason: "stop"}},
			want:   fallbackUsageExpectations(0),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := append([]TurnEvent(nil), tc.before...)
			if tc.wire != nil {
				events = append(events, turnEndedUsageEvent(t, tc.wire))
			}
			events = append(events, tc.after...)
			for _, format := range []string{"openai-chat", "openai-response", "claude"} {
				t.Run(format, func(t *testing.T) {
					result, err := collectTurn(usageEventChannel(events))
					if err != nil {
						t.Fatal(err)
					}
					raw, err := nonStreamingPayload(format, "cursor-model", result, 1)
					if err != nil {
						t.Fatal(err)
					}
					var response map[string]any
					if err := json.Unmarshal(raw, &response); err != nil {
						t.Fatal(err)
					}
					assertUsageFields(t, response["usage"], tc.want[format])

					prepared, err := prepareEventStream(format, "cursor-model", 1, usageEventChannel(events))
					if err != nil {
						t.Fatal(err)
					}
					var stream strings.Builder
					if err := prepared.Pump(func(frame []byte) error { _, err := stream.Write(frame); return err }); err != nil {
						t.Fatal(err)
					}
					usages := terminalStreamUsages(t, format, stream.String())
					if len(usages) != 1 {
						t.Fatalf("terminal usage count = %d; stream = %s", len(usages), stream.String())
					}
					assertUsageFields(t, usages[0], tc.want[format])
				})
			}
		})
	}
}

func TestExecutorUsageDoesNotCompleteOnError(t *testing.T) {
	want := errors.New("upstream failed")
	events := []TurnEvent{
		{Type: "usage", Tokens: 3}, {Type: "text", Text: "partial"},
		turnEndedUsageEvent(t, []byte{0x08, 0x64, 0x10, 0x05}),
		{Type: "done", DoneReason: "error", Err: want},
	}
	if result, err := collectTurn(usageEventChannel(events)); !errors.Is(err, want) || result.Usage != (tokenUsage{}) {
		t.Fatalf("failed collection returned usage %+v, error %v", result.Usage, err)
	}
	for _, format := range []string{"openai-chat", "openai-response", "claude"} {
		t.Run(format, func(t *testing.T) {
			prepared, err := prepareEventStream(format, "cursor-model", 1, usageEventChannel(events))
			if err != nil {
				t.Fatal(err)
			}
			var stream strings.Builder
			err = prepared.Pump(func(frame []byte) error { _, err := stream.Write(frame); return err })
			if !errors.Is(err, want) {
				t.Fatalf("Pump error = %v, want %v", err, want)
			}
			if usages := terminalStreamUsages(t, format, stream.String()); len(usages) != 0 {
				t.Fatalf("failed stream emitted successful usage: %+v", usages)
			}
		})
	}
}

func turnEndedUsageEvent(t *testing.T, wire []byte) TurnEvent {
	t.Helper()
	var ended pb.TurnEndedUpdate
	if err := proto.Unmarshal(wire, &ended); err != nil {
		t.Fatal(err)
	}
	message := &pb.AgentServerMessage{Message: &pb.AgentServerMessage_InteractionUpdate{InteractionUpdate: &pb.InteractionUpdate{
		Message: &pb.InteractionUpdate_TurnEnded{TurnEnded: &ended},
	}}}
	events := make(chan TurnEvent, 1)
	if err := handleServerMessage(message, nil, nil, nil, nil, events); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.Type != "usage" {
			t.Fatalf("terminal produced event %q, want usage", event.Type)
		}
		return event
	default:
		t.Fatal("terminal update did not emit usage")
		return TurnEvent{}
	}
}

func usageEventChannel(events []TurnEvent) <-chan TurnEvent {
	ch := make(chan TurnEvent, len(events))
	for _, event := range events {
		ch <- event
	}
	close(ch)
	return ch
}

func terminalStreamUsages(t *testing.T, format, stream string) []any {
	t.Helper()
	var usages []any
	for _, line := range strings.Split(stream, "\n") {
		if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
			continue
		}
		var frame map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame); err != nil {
			t.Fatal(err)
		}
		switch format {
		case "openai-chat":
			if usage, ok := frame["usage"]; ok {
				usages = append(usages, usage)
			}
		case "openai-response":
			if frame["type"] == "response.completed" {
				response, _ := frame["response"].(map[string]any)
				usages = append(usages, response["usage"])
			}
		case "claude":
			if frame["type"] == "message_delta" {
				usages = append(usages, frame["usage"])
			}
		}
	}
	return usages
}

func assertUsageFields(t *testing.T, usage any, want map[string]int64) {
	t.Helper()
	for path, expected := range want {
		value := usage
		for _, key := range strings.Split(path, ".") {
			object, ok := value.(map[string]any)
			if !ok {
				t.Fatalf("usage path %s missing from %+v", path, usage)
			}
			value = object[key]
		}
		if value != float64(expected) {
			t.Errorf("usage %s = %v, want %d", path, value, expected)
		}
	}
}

func exactUsageExpectations() map[string]map[string]int64 {
	return map[string]map[string]int64{
		"openai-chat": {
			"prompt_tokens": 1000, "completion_tokens": 40, "total_tokens": 1040,
			"prompt_tokens_details.cached_tokens": 700, "prompt_tokens_details.cache_write_tokens": 100,
			"completion_tokens_details.reasoning_tokens": 12,
		},
		"openai-response": {
			"input_tokens": 1000, "output_tokens": 40, "total_tokens": 1040,
			"input_tokens_details.cached_tokens": 700, "input_tokens_details.cache_write_tokens": 100,
			"output_tokens_details.reasoning_tokens": 12,
		},
		"claude": {
			"input_tokens": 200, "output_tokens": 40, "cache_read_input_tokens": 700, "cache_creation_input_tokens": 100,
			"output_tokens_details.thinking_tokens": 12,
		},
	}
}

func fallbackUsageExpectations(output int64) map[string]map[string]int64 {
	return map[string]map[string]int64{
		"openai-chat":     {"prompt_tokens": 0, "completion_tokens": output, "total_tokens": output, "prompt_tokens_details.cached_tokens": 0, "prompt_tokens_details.cache_write_tokens": 0},
		"openai-response": {"input_tokens": 0, "output_tokens": output, "total_tokens": output, "input_tokens_details.cached_tokens": 0, "input_tokens_details.cache_write_tokens": 0},
		"claude":          {"input_tokens": 0, "output_tokens": output, "cache_read_input_tokens": 0, "cache_creation_input_tokens": 0},
	}
}
