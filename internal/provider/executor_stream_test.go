package provider

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestPrepareEventStreamReturnsImmediateUpstreamError(t *testing.T) {
	want := &StatusError{
		Code:       "resource_exhausted",
		Message:    "Cursor Grok is busy",
		HTTPStatus: http.StatusTooManyRequests,
		Retryable:  true,
	}
	events := make(chan TurnEvent, 1)
	events <- TurnEvent{Type: "done", DoneReason: "error", Err: want}
	close(events)

	prepared, err := prepareEventStream("openai-response", "grok-4.6", 1, events)
	if prepared != nil {
		t.Fatalf("prepared = %#v, want nil", prepared)
	}
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestPrepareEventStreamBuffersHiddenResponsesThinkingUntilError(t *testing.T) {
	want := &StatusError{
		Code:       "resource_exhausted",
		Message:    "Cursor Grok is busy",
		HTTPStatus: http.StatusTooManyRequests,
		Retryable:  true,
	}
	events := make(chan TurnEvent, 2)
	events <- TurnEvent{Type: "thinking", Text: "not exposed by Responses"}
	events <- TurnEvent{Type: "done", DoneReason: "error", Err: want}
	close(events)

	prepared, err := prepareEventStream("openai-response", "grok-4.6", 1, events)
	if prepared != nil {
		t.Fatalf("prepared = %#v, want nil", prepared)
	}
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestPrepareEventStreamCommitsVisibleChatThinking(t *testing.T) {
	events := make(chan TurnEvent, 1)
	events <- TurnEvent{Type: "thinking", Text: "visible reasoning"}
	close(events)

	prepared, err := prepareEventStream("openai-chat", "grok-4.6", 1, events)
	if err != nil {
		t.Fatalf("prepareEventStream() error = %v", err)
	}
	if prepared == nil {
		t.Fatal("prepared = nil, want stream")
	}
}

func TestPreparedStreamPreservesBufferedEvents(t *testing.T) {
	events := make(chan TurnEvent, 3)
	events <- TurnEvent{Type: "usage", Tokens: 5}
	events <- TurnEvent{Type: "text", Text: "hello"}
	events <- TurnEvent{Type: "done", DoneReason: "stop", Tokens: 5}
	close(events)

	prepared, err := prepareEventStream("openai-response", "grok-4.6", 1, events)
	if err != nil {
		t.Fatalf("prepareEventStream() error = %v", err)
	}
	headers := prepared.Headers()
	if headers.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("Content-Type = %q", headers.Get("Content-Type"))
	}
	headers.Set("Content-Type", "changed")
	if prepared.Headers().Get("Content-Type") != "text/event-stream" {
		t.Fatal("Headers() did not return a defensive copy")
	}

	var output strings.Builder
	if err := prepared.Pump(func(frame []byte) error {
		_, writeErr := output.Write(frame)
		return writeErr
	}); err != nil {
		t.Fatalf("Pump() error = %v", err)
	}
	got := output.String()
	if !strings.Contains(got, "response.output_text.delta") || !strings.Contains(got, "hello") {
		t.Fatalf("stream output omitted buffered text: %s", got)
	}
	if !strings.Contains(got, "response.completed") {
		t.Fatalf("stream output omitted completion: %s", got)
	}
}

func TestPreparedStreamDoesNotEmitCompletionForLateError(t *testing.T) {
	want := &StatusError{Code: "resource_exhausted", Message: "busy", HTTPStatus: http.StatusTooManyRequests}
	events := make(chan TurnEvent, 2)
	events <- TurnEvent{Type: "text", Text: "partial"}
	events <- TurnEvent{Type: "done", DoneReason: "error", Err: want}
	close(events)

	prepared, err := prepareEventStream("openai-response", "grok-4.6", 1, events)
	if err != nil {
		t.Fatalf("prepareEventStream() error = %v", err)
	}
	var output strings.Builder
	err = prepared.Pump(func(frame []byte) error {
		_, writeErr := output.Write(frame)
		return writeErr
	})
	if !errors.Is(err, want) {
		t.Fatalf("Pump() error = %v, want %v", err, want)
	}
	if strings.Contains(output.String(), "response.completed") {
		t.Fatalf("late error emitted a successful completion: %s", output.String())
	}
}

func TestPrepareEventStreamRejectsEmptyStream(t *testing.T) {
	events := make(chan TurnEvent)
	close(events)

	prepared, err := prepareEventStream("openai-response", "grok-4.6", 1, events)
	if prepared != nil {
		t.Fatalf("prepared = %#v, want nil", prepared)
	}
	var statusErr *StatusError
	if !errors.As(err, &statusErr) || statusErr.Code != "cursor_stream_empty" || !statusErr.Retryable {
		t.Fatalf("error = %#v, want retryable cursor_stream_empty", err)
	}
}
