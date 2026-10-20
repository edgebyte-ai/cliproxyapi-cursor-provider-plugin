package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestRetryTransientRetriesGatewayFailureOnce(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TransientRetryDelayMS = 0
	attempts := 0
	result, err := retryTransient(context.Background(), cfg, func() (string, error) {
		attempts++
		if attempts == 1 {
			return "", cursorStatusError([]byte("Bad Gateway"), http.StatusBadGateway)
		}
		return "ok", nil
	})
	if err != nil || result != "ok" || attempts != 2 {
		t.Fatalf("retryTransient() = %q, %v after %d attempts", result, err, attempts)
	}
}

func TestRetryTransientDoesNotRetryQuotaOrAuthFailure(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests} {
		cfg := DefaultConfig()
		cfg.TransientRetryDelayMS = 0
		attempts := 0
		want := &StatusError{Code: "upstream_rejected", Message: "rejected", HTTPStatus: status, Retryable: true}
		_, err := retryTransient(context.Background(), cfg, func() (string, error) {
			attempts++
			return "", want
		})
		if !errors.Is(err, want) || attempts != 1 {
			t.Fatalf("status %d: error = %v after %d attempts", status, err, attempts)
		}
	}
}

func TestRetryTransientStopsAfterConfiguredRetries(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TransientRetryCount = 2
	cfg.TransientRetryDelayMS = 0
	attempts := 0
	want := &StatusError{Code: "cursor_upstream_error", Message: "unavailable", HTTPStatus: http.StatusServiceUnavailable, Retryable: true}
	_, err := retryTransient(context.Background(), cfg, func() (string, error) {
		attempts++
		return "", want
	})
	if !errors.Is(err, want) || attempts != 3 {
		t.Fatalf("retryTransient() error = %v after %d attempts", err, attempts)
	}
}

func TestRetryTransientDoesNotRetryProviderRejection(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TransientRetryDelayMS = 0
	raw := []byte(`{"error":{"code":"resource_exhausted","details":[{"debug":{"error":"ERROR_PROVIDER_ERROR","details":{"detail":"Provider rejected the request","isRetryable":false,"additionalInfo":{"providerStatusCode":"400"}}}}]}}`)
	want := cursorStatusError(raw, http.StatusBadGateway)
	attempts := 0
	_, err := retryTransient(context.Background(), cfg, func() (string, error) {
		attempts++
		return "", fmt.Errorf("run turn: %w", want)
	})
	if !errors.Is(err, want) || attempts != 1 {
		t.Fatalf("retryTransient() error = %v after %d attempts", err, attempts)
	}
}
