package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
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
					IsRetryable    bool   `json:"isRetryable"`
					AdditionalInfo struct {
						ChatMessage string `json:"chatMessage"`
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
	resetAt := time.Time{}
	for _, detail := range envelope.Error.Details {
		candidate := firstNonEmpty(detail.Debug.Details.Detail, detail.Debug.Details.AdditionalInfo.ChatMessage, detail.Debug.Details.Title, detail.Debug.Error)
		if candidate != "" {
			message = candidate
		}
		retryable = retryable || detail.Debug.Details.IsRetryable
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
	if status == 0 {
		status = http.StatusBadGateway
	}
	if message == "" {
		message = http.StatusText(status)
	}
	if isTransientHTTPStatus(status) {
		retryable = true
	}
	retryAfter := time.Duration(0)
	if !resetAt.IsZero() && resetAt.After(time.Now()) {
		retryAfter = time.Until(resetAt)
	}
	return &StatusError{Code: firstNonEmpty(code, "cursor_upstream_error"), Message: message, HTTPStatus: status, Retryable: retryable, RetryAfter: retryAfter}
}

func isTransientHTTPStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}
