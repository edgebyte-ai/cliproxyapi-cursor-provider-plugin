package provider

import (
	"net/http"
	"testing"
)

func TestCursorResourceExhaustedMapsTo429AndReset(t *testing.T) {
	raw := []byte(`{"error":{"code":"resource_exhausted","message":"limit","details":[{"debug":{"error":"ERROR_USER_REQUESTS","details":{"detail":"Usage resets on 8/27/2026","isRetryable":true}}}]}}`)
	err := cursorStatusError(raw, http.StatusBadGateway)
	if err.HTTPStatus != http.StatusTooManyRequests || !err.Retryable || err.RetryAfter <= 0 {
		t.Fatalf("unexpected mapped error: %+v", err)
	}
}

func TestCursorStatusErrorMarksGatewayFailureRetryable(t *testing.T) {
	err := cursorStatusError([]byte("Bad Gateway"), http.StatusBadGateway)
	if err.HTTPStatus != http.StatusBadGateway || !err.Retryable || err.Message != "Bad Gateway" {
		t.Fatalf("cursorStatusError() = %#v", err)
	}
}
