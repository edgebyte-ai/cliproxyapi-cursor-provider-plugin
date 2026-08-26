package provider

import (
	"context"
	"errors"
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
			return "", &StatusError{Code: "cursor_upstream_error", Message: "Bad Gateway", HTTPStatus: http.StatusBadGateway}
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
	want := &StatusError{Code: "cursor_upstream_error", Message: "unavailable", HTTPStatus: http.StatusServiceUnavailable}
	_, err := retryTransient(context.Background(), cfg, func() (string, error) {
		attempts++
		return "", want
	})
	if !errors.Is(err, want) || attempts != 3 {
		t.Fatalf("retryTransient() error = %v after %d attempts", err, attempts)
	}
}
