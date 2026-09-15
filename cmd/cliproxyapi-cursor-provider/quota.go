package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/edgebyte-ai/cliproxyapi-cursor-provider-plugin/internal/provider"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

const cursorQuotaDetailsPath = "/plugins/cursor-provider/quota-details"

// Keep the additive quota JSON ABI local so the plugin still builds against
// v7.2.141 and works on hosts predating the quota provider capability.
const (
	methodQuotaIdentifier = "quota.identifier"
	methodQuotaDescribe   = "quota.describe"
	methodQuotaFetch      = "quota.fetch"
	methodQuotaReset      = "quota.reset"
)

type quotaDescribeResponse struct {
	SupportedProviders []string `json:"supported_providers"`
	DisplayName        string   `json:"display_name"`
	SupportsReset      bool     `json:"supports_reset"`
}

type quotaFetchRequest struct {
	AuthIndex      string `json:"auth_index"`
	Provider       string `json:"provider"`
	StorageJSON    []byte `json:"storage_json,omitempty"`
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type quotaFetchResponse struct {
	Subscription *quotaSubscription `json:"subscription,omitempty"`
	Groups       []quotaGroup       `json:"groups,omitempty"`
}

type quotaSubscription struct {
	Plan string `json:"plan,omitempty"`
}

type quotaGroup struct {
	DisplayName string        `json:"displayName,omitempty"`
	Buckets     []quotaBucket `json:"buckets,omitempty"`
}

type quotaBucket struct {
	Window            string  `json:"window,omitempty"`
	RemainingFraction float64 `json:"remainingFraction"`
	ResetTime         string  `json:"resetTime,omitempty"`
	Description       string  `json:"description,omitempty"`
}

type quotaResetResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

type quotaRPC struct {
	hostCall func(string, any) (json.RawMessage, error)
	fetch    func(context.Context, provider.AuthStorage) (provider.QuotaResponse, error)
}

func (q quotaRPC) fetchQuota(ctx context.Context, req quotaFetchRequest) (quotaFetchResponse, error) {
	if req.Provider != "" && !strings.EqualFold(strings.TrimSpace(req.Provider), provider.ProviderID) {
		return quotaFetchResponse{}, fmt.Errorf("credential is not a Cursor credential")
	}
	raw := req.StorageJSON
	if len(raw) == 0 {
		if strings.TrimSpace(req.AuthIndex) == "" {
			return quotaFetchResponse{}, fmt.Errorf("auth_index or storage_json is required")
		}
		result, err := q.hostCall(pluginabi.MethodHostAuthGet, hostAuthGetRequest{
			HostCallbackID: req.HostCallbackID, AuthIndex: req.AuthIndex,
		})
		if err != nil {
			return quotaFetchResponse{}, fmt.Errorf("read Cursor quota credential: %w", err)
		}
		var auth hostAuthGetResponse
		if err := json.Unmarshal(result, &auth); err != nil {
			return quotaFetchResponse{}, fmt.Errorf("decode Cursor quota credential response: %w", err)
		}
		raw = auth.JSON
	}
	storage, ok := decodeCursorAuthStorage(raw)
	if !ok {
		return quotaFetchResponse{}, fmt.Errorf("credential is not a Cursor credential")
	}
	if strings.TrimSpace(storage.AccessToken) == "" {
		return quotaFetchResponse{}, fmt.Errorf("Cursor credential has no access token")
	}
	quota, err := q.fetch(ctx, storage)
	if err != nil {
		return quotaFetchResponse{}, err
	}
	return normalizeQuota(quota), nil
}

func normalizeQuota(quota provider.QuotaResponse) quotaFetchResponse {
	response := quotaFetchResponse{}
	if quota.Subscription != nil {
		response.Subscription = &quotaSubscription{Plan: quota.Subscription.Plan}
	}
	for _, row := range quota.Quota {
		group := quotaGroup{DisplayName: row.GroupLabel}
		if group.DisplayName == "" {
			group.DisplayName = row.Key
		}
		// An absent usage value is unknown, not an exhausted quota bucket.
		if row.RemainingFraction != nil {
			group.Buckets = []quotaBucket{{
				Window: "billing-cycle", RemainingFraction: *row.RemainingFraction,
				ResetTime: row.ResetAt, Description: row.Label,
			}}
		}
		response.Groups = append(response.Groups, group)
	}
	return response
}
