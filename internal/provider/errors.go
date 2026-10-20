package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type StatusError struct {
	Code       string
	Message    string
	HTTPStatus int
	Retryable  bool
	RetryAfter time.Duration
}

func (e *StatusError) Error() string {
	return strings.TrimSpace(e.Message)
}

type cursorErrorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details []struct {
			Debug struct {
				Error   string `json:"error"`
				Details struct {
					Title          string `json:"title"`
					Detail         string `json:"detail"`
					IsRetryable    *bool  `json:"isRetryable"`
					AdditionalInfo struct {
						ChatMessage        string          `json:"chatMessage"`
						ProviderStatusCode json.RawMessage `json:"providerStatusCode"`
					} `json:"additionalInfo"`
				} `json:"details"`
			} `json:"debug"`
		} `json:"details"`
	} `json:"error"`
}

var resetDatePattern = regexp.MustCompile(`\b(\d{1,2})/(\d{1,2})/(\d{4})\b`)

func cursorStatusError(raw []byte, fallbackStatus int) *StatusError {
	var envelope cursorErrorEnvelope
	_ = json.Unmarshal(raw, &envelope)
	code := strings.TrimSpace(envelope.Error.Code)
	message := strings.TrimSpace(envelope.Error.Message)
	retryable := false
	retryabilitySet := false
	upstreamError := ""
	providerStatus := 0
	resetAt := time.Time{}
	for _, detail := range envelope.Error.Details {
		candidate := firstNonEmpty(detail.Debug.Details.Detail, detail.Debug.Details.AdditionalInfo.ChatMessage, detail.Debug.Details.Title, detail.Debug.Error)
		if candidate != "" {
			message = candidate
		}
		if hint := detail.Debug.Details.IsRetryable; hint != nil {
			// An explicit rejection of retries takes precedence over inferred retryability.
			if !retryabilitySet || !*hint {
				retryable = *hint
			}
			retryabilitySet = true
		}
		switch detail.Debug.Error {
		case "ERROR_PROVIDER_ERROR", "ERROR_NETWORK_ERROR":
			upstreamError = detail.Debug.Error
			if status := parseProviderStatus(detail.Debug.Details.AdditionalInfo.ProviderStatusCode); status != 0 {
				providerStatus = status
			}
		}
		if match := resetDatePattern.FindStringSubmatch(candidate); len(match) == 4 {
			var month, day, year int
			_, _ = fmt.Sscanf(match[0], "%d/%d/%d", &month, &day, &year)
			parsed := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
			if parsed.Year() == year && int(parsed.Month()) == month && parsed.Day() == day {
				resetAt = parsed
			}
		}
	}
	status := fallbackStatus
	if strings.EqualFold(code, "resource_exhausted") {
		status = http.StatusTooManyRequests
	}
	if upstreamError != "" {
		// Cursor also wraps provider and network failures in resource_exhausted.
		// Only an actual provider 429 should trigger the host's quota cooldown.
		status = http.StatusBadGateway
		code = "cursor_" + strings.ToLower(strings.TrimPrefix(upstreamError, "ERROR_"))
		if providerStatus == http.StatusTooManyRequests || providerStatus >= 500 {
			status = providerStatus
		}
	}
	if status == 0 {
		status = http.StatusBadGateway
	}
	if message == "" {
		message = http.StatusText(status)
	}
	if providerStatus != 0 {
		message = fmt.Sprintf("%s (provider HTTP %d)", message, providerStatus)
	}
	if !retryabilitySet && isTransientHTTPStatus(status) {
		retryable = true
	}
	retryAfter := time.Duration(0)
	if status == http.StatusTooManyRequests && !resetAt.IsZero() && resetAt.After(time.Now()) {
		retryAfter = time.Until(resetAt)
	}
	return &StatusError{Code: firstNonEmpty(code, "cursor_upstream_error"), Message: message, HTTPStatus: status, Retryable: retryable, RetryAfter: retryAfter}
}

func parseProviderStatus(raw json.RawMessage) int {
	value := strings.TrimSpace(string(raw))
	if strings.HasPrefix(value, `"`) {
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0
		}
	}
	status, err := strconv.Atoi(value)
	if err != nil || status < 400 || status > 599 {
		return 0
	}
	return status
}

func isTransientHTTPStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}
