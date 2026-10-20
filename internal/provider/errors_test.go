package provider

import (
	"fmt"
	"net/http"
	"strings"
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

func TestCursorStatusErrorDistinguishesProviderFailuresFromQuota(t *testing.T) {
	tests := []struct {
		name       string
		debugError string
		statusJSON string
		retryJSON  string
		wantStatus int
		wantCode   string
		wantRetry  bool
		wantHint   string
	}{
		{
			name: "provider request rejected", debugError: "ERROR_PROVIDER_ERROR", statusJSON: `"400"`, retryJSON: `,"isRetryable":false`,
			wantStatus: http.StatusBadGateway, wantCode: "cursor_provider_error", wantHint: "provider HTTP 400",
		},
		{
			name: "provider actually rate limited", debugError: "ERROR_PROVIDER_ERROR", statusJSON: `"429"`, retryJSON: `,"isRetryable":true`,
			wantStatus: http.StatusTooManyRequests, wantCode: "cursor_provider_error", wantRetry: true, wantHint: "provider HTTP 429",
		},
		{
			name: "provider unavailable", debugError: "ERROR_PROVIDER_ERROR", statusJSON: `503`,
			wantStatus: http.StatusServiceUnavailable, wantCode: "cursor_provider_error", wantRetry: true, wantHint: "provider HTTP 503",
		},
		{
			name: "provider rejects retries", debugError: "ERROR_PROVIDER_ERROR", statusJSON: `"503"`, retryJSON: `,"isRetryable":false`,
			wantStatus: http.StatusServiceUnavailable, wantCode: "cursor_provider_error", wantHint: "provider HTTP 503",
		},
		{
			name: "network failure", debugError: "ERROR_NETWORK_ERROR", statusJSON: `null`,
			wantStatus: http.StatusBadGateway, wantCode: "cursor_network_error", wantRetry: true,
		},
		{
			name: "invalid provider status", debugError: "ERROR_PROVIDER_ERROR", statusJSON: `"secret-not-a-status"`,
			wantStatus: http.StatusBadGateway, wantCode: "cursor_provider_error", wantRetry: true,
		},
		{
			name: "unknown resource exhaustion", debugError: "ERROR_UNKNOWN", statusJSON: `null`,
			wantStatus: http.StatusTooManyRequests, wantCode: "resource_exhausted",
		},
		{
			name: "account quota", debugError: "ERROR_USER_REQUESTS", statusJSON: `null`, retryJSON: `,"isRetryable":false`,
			wantStatus: http.StatusTooManyRequests, wantCode: "resource_exhausted",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := []byte(fmt.Sprintf(`{"error":{"code":"resource_exhausted","message":"Error","details":[{"type":"aiserver.v1.ErrorDetails","debug":{"error":%q,"details":{"title":"Provider Error","detail":"Upstream rejected the request"%s,"additionalInfo":{"providerStatusCode":%s}},"isExpected":true}}]}}`, tt.debugError, tt.retryJSON, tt.statusJSON))
			err := cursorStatusError(raw, http.StatusBadGateway)
			if err.HTTPStatus != tt.wantStatus || err.Code != tt.wantCode || err.Retryable != tt.wantRetry || err.RetryAfter != 0 {
				t.Fatalf("cursorStatusError() = %#v", err)
			}
			if !strings.Contains(err.Message, "Upstream rejected the request") || (tt.wantHint != "" && !strings.Contains(err.Message, tt.wantHint)) {
				t.Fatalf("message = %q, want upstream detail and %q", err.Message, tt.wantHint)
			}
			if strings.Contains(err.Message, "secret-not-a-status") {
				t.Fatalf("message includes unvalidated provider status: %q", err.Message)
			}
		})
	}
}

func TestCursorStatusErrorHonorsExplicitRetryRejection(t *testing.T) {
	raw := []byte(`{"error":{"details":[{"debug":{"details":{"isRetryable":false}}}]}}`)
	err := cursorStatusError(raw, http.StatusBadGateway)
	if err.Retryable {
		t.Fatalf("cursorStatusError() ignored isRetryable=false: %#v", err)
	}
}
