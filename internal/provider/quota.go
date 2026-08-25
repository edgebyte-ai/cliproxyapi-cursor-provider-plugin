package provider

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type QuotaResponse struct {
	ID           string            `json:"id"`
	Quota        []QuotaRow        `json:"quota"`
	Subscription *SubscriptionInfo `json:"subscription,omitempty"`
	Source       string            `json:"source"`
}

type SubscriptionInfo struct {
	Provider string `json:"provider"`
	Plan     string `json:"plan"`
}

type QuotaRow struct {
	Key               string       `json:"key"`
	Label             string       `json:"label"`
	Scope             string       `json:"scope"`
	Metric            string       `json:"metric"`
	GroupKey          string       `json:"groupKey"`
	GroupLabel        string       `json:"groupLabel"`
	UsedPercent       *float64     `json:"usedPercent,omitempty"`
	RemainingFraction *float64     `json:"remainingFraction,omitempty"`
	Allowed           *bool        `json:"allowed,omitempty"`
	LimitReached      *bool        `json:"limitReached,omitempty"`
	Window            *QuotaWindow `json:"window,omitempty"`
	ResetAt           string       `json:"resetAt,omitempty"`
	ResetAfterSeconds *int64       `json:"resetAfterSeconds,omitempty"`
}

type QuotaWindow struct {
	Seconds int64 `json:"seconds"`
}

var usedPercentPattern = regexp.MustCompile(`(?i)\bused\s+([0-9]+(?:\.[0-9]+)?)%`)

func (s *Service) Quota(ctx context.Context, storage AuthStorage) (QuotaResponse, error) {
	cfg := s.Config()
	dashboard := map[string]any{}
	summary := map[string]any{}
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+storage.AccessToken)
	headers.Set("Content-Type", "application/json")
	headers.Set("Accept", "application/json")
	_, _, dashboardErr := doJSON(ctx, http.MethodPost, strings.TrimRight(cfg.CursorBaseURL, "/")+"/aiserver.v1.DashboardService/GetCurrentPeriodUsage", headers, []byte("{}"), 30*time.Second, &dashboard)

	subject := jwtStringClaim(storage.AccessToken, "sub")
	if separator := strings.LastIndex(subject, "|"); separator >= 0 {
		subject = subject[separator+1:]
	}
	var summaryErr error
	if subject != "" {
		summaryHeaders := make(http.Header)
		summaryHeaders.Set("Accept", "application/json")
		summaryHeaders.Set("Cookie", "WorkosCursorSessionToken="+url.QueryEscape(subject+"::"+storage.AccessToken))
		_, _, summaryErr = doJSON(ctx, http.MethodGet, "https://cursor.com/api/usage-summary", summaryHeaders, nil, 30*time.Second, &summary)
	}
	if dashboardErr != nil && (summaryErr != nil || len(summary) == 0) {
		return QuotaResponse{}, fmt.Errorf("Cursor quota endpoints are unavailable")
	}
	planUsage := nestedMap(dashboard, "planUsage")
	webPlan := nestedMap(nestedMap(summary, "individualUsage"), "plan")
	if len(webPlan) == 0 {
		webPlan = nestedMap(nestedMap(summary, "teamUsage"), "plan")
	}
	cursorNative := firstNumber(planUsage["autoPercentUsed"], webPlan["autoPercentUsed"], percentFromMessage(summary["autoModelSelectedDisplayMessage"]))
	otherModels := firstNumber(planUsage["apiPercentUsed"], webPlan["apiPercentUsed"], percentFromMessage(summary["namedModelSelectedDisplayMessage"]))
	start := firstTime(summary["billingCycleStart"], dashboard["billingCycleStart"])
	end := firstTime(summary["billingCycleEnd"], dashboard["billingCycleEnd"])
	plan := firstNonEmpty(stringValue(summary["membershipType"]), "unknown")
	source := "dashboard+usage-summary"
	if dashboardErr != nil {
		source = "usage-summary"
	} else if summaryErr != nil || len(summary) == 0 {
		source = "dashboard"
	}
	return QuotaResponse{
		ID: storage.ID,
		Quota: []QuotaRow{
			quotaRow("cursor-native", cursorNative, start, end),
			quotaRow("other-models", otherModels, start, end),
		},
		Subscription: &SubscriptionInfo{Provider: "cursor", Plan: plan},
		Source:       source,
	}, nil
}

func quotaRow(key string, used *float64, start, end time.Time) QuotaRow {
	row := QuotaRow{Key: key, Label: key, Scope: "quota_group", Metric: key, GroupKey: key, GroupLabel: key}
	if used != nil {
		clamped := math.Max(0, math.Min(100, *used))
		remaining := (100 - clamped) / 100
		allowed := clamped < 100
		limitReached := !allowed
		row.UsedPercent = &clamped
		row.RemainingFraction = &remaining
		row.Allowed = &allowed
		row.LimitReached = &limitReached
	}
	if !start.IsZero() && !end.IsZero() && end.After(start) {
		row.Window = &QuotaWindow{Seconds: int64(end.Sub(start).Seconds())}
	}
	if !end.IsZero() {
		row.ResetAt = end.UTC().Format(time.RFC3339)
		seconds := int64(math.Ceil(time.Until(end).Seconds()))
		if seconds < 0 {
			seconds = 0
		}
		row.ResetAfterSeconds = &seconds
	}
	return row
}

func nestedMap(source map[string]any, key string) map[string]any {
	result, _ := source[key].(map[string]any)
	return result
}

func firstNumber(values ...any) *float64 {
	for _, value := range values {
		switch number := value.(type) {
		case float64:
			copy := number
			return &copy
		case int:
			copy := float64(number)
			return &copy
		case string:
			var parsed float64
			if _, err := fmt.Sscanf(strings.TrimSpace(number), "%f", &parsed); err == nil {
				return &parsed
			}
		}
	}
	return nil
}

func percentFromMessage(value any) any {
	message, _ := value.(string)
	match := usedPercentPattern.FindStringSubmatch(message)
	if len(match) != 2 {
		return nil
	}
	return match[1]
}

func firstTime(values ...any) time.Time {
	for _, value := range values {
		switch raw := value.(type) {
		case string:
			if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
				return parsed
			}
		case float64:
			milliseconds := int64(raw)
			if milliseconds < 10_000_000_000 {
				return time.Unix(milliseconds, 0)
			}
			return time.UnixMilli(milliseconds)
		}
	}
	return time.Time{}
}
