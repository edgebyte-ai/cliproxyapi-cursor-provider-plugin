package provider

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestCursorResourceExhaustedMapsTo429AndReset(t *testing.T) {
	resetDate := time.Now().UTC().AddDate(0, 0, 2).Format("1/2/2006")
	raw := []byte(fmt.Sprintf(`{"error":{"code":"resource_exhausted","message":"limit","details":[{"debug":{"error":"ERROR_USER_REQUESTS","details":{"detail":"Usage resets on %s","isRetryable":true}}}]}}`, resetDate))
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
