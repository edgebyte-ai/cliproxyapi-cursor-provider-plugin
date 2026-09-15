package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/edgebyte-ai/cliproxyapi-cursor-provider-plugin/internal/provider"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestQuotaCapability(t *testing.T) {
	if !pluginRegistration().Capabilities.QuotaProvider {
		t.Fatal("quota provider capability is not registered")
	}
	identifier, err := dispatch(methodQuotaIdentifier, nil)
	if err != nil || identifier != (identifierResponse{Identifier: "cursor"}) {
		t.Fatalf("quota identifier = %#v, %v", identifier, err)
	}
	description, err := dispatch(methodQuotaDescribe, nil)
	want := quotaDescribeResponse{SupportedProviders: []string{"cursor"}, DisplayName: "Cursor"}
	if err != nil || !reflect.DeepEqual(description, want) {
		t.Fatalf("quota description = %#v, %v", description, err)
	}
	reset, err := dispatch(methodQuotaReset, nil)
	if err != nil || reset.(quotaResetResponse).Success {
		t.Fatalf("quota reset must not claim success: %#v, %v", reset, err)
	}
	if _, err := dispatch(methodQuotaFetch, []byte(`{`)); err == nil {
		t.Fatal("quota fetch accepted malformed JSON")
	}
}

func TestQuotaPageAvoidsReservedHostRoute(t *testing.T) {
	if strings.Contains(cursorQuotaPage, "api('/v0/management/plugins/cursor-provider/quota'+") {
		t.Fatal("quota page still calls the reserved /plugins/:id/quota route")
	}
	if !strings.Contains(cursorQuotaPage, "api('/v0/management"+cursorQuotaDetailsPath+"'+") {
		t.Fatal("quota page does not call the quota-details route")
	}
	result, err := dispatch(pluginabi.MethodManagementRegister, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, route := range result.(rpcManagementRegistrationResponse).Routes {
		if route.Method == http.MethodGet && route.Path == cursorQuotaDetailsPath {
			found = true
		}
	}
	if !found {
		t.Fatal("quota-details route is not registered")
	}
	// Both old-host and new-host paths must reach the quota handler.
	for _, path := range []string{cursorQuotaDetailsPath, "/plugins/cursor-provider/quota"} {
		response, err := handleManagement(context.Background(), rpcManagementRequest{
			ManagementRequest: pluginapi.ManagementRequest{Method: http.MethodGet, Path: "/v0/management" + path, Query: url.Values{}},
		})
		if err != nil || response.StatusCode != http.StatusBadRequest || !strings.Contains(string(response.Body), "auth_index is required") {
			t.Fatalf("route %s = %#v, %v", path, response, err)
		}
	}
}

func TestQuotaFetchStorageAndHostCallback(t *testing.T) {
	const rawAuth = `{"type":"cursor","access_token":"fixture-access","refresh_token":"fixture-refresh"}`
	for _, useCallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "storage", true: "callback"}[useCallback], func(t *testing.T) {
			req := quotaFetchRequest{Provider: "cursor", AuthIndex: "index-1", HostCallbackID: "callback-1"}
			if !useCallback {
				req.StorageJSON = []byte(rawAuth)
			}
			calledHost := false
			q := quotaRPC{
				hostCall: func(method string, payload any) (json.RawMessage, error) {
					calledHost = true
					want := hostAuthGetRequest{AuthIndex: req.AuthIndex, HostCallbackID: req.HostCallbackID}
					if method != pluginabi.MethodHostAuthGet || payload != want {
						t.Fatalf("host callback = %s, %#v", method, payload)
					}
					return json.Marshal(hostAuthGetResponse{JSON: json.RawMessage(rawAuth)})
				},
				fetch: func(_ context.Context, storage provider.AuthStorage) (provider.QuotaResponse, error) {
					if storage.Type != "cursor" || storage.AccessToken != "fixture-access" {
						t.Fatal("quota fetched with incorrect credential")
					}
					remaining, exhausted := 0.75, 0.0
					return provider.QuotaResponse{
						Subscription: &provider.SubscriptionInfo{Plan: "pro"},
						Quota: []provider.QuotaRow{
							{Key: "cursor-native", RemainingFraction: &remaining, ResetAt: "2030-01-01T00:00:00Z"},
							{Key: "other-models", RemainingFraction: &exhausted},
						},
					}, nil
				},
			}
			response, err := q.fetchQuota(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if calledHost != useCallback || response.Subscription.Plan != "pro" || len(response.Groups) != 2 {
				t.Fatalf("unexpected response: %#v, host called=%v", response, calledHost)
			}
			if response.Groups[0].DisplayName != "cursor-native" || response.Groups[0].Buckets[0].RemainingFraction != 0.75 || response.Groups[0].Buckets[0].ResetTime != "2030-01-01T00:00:00Z" {
				t.Fatalf("incorrect quota mapping: %#v", response.Groups[0])
			}
			raw, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), `"remainingFraction":0}`) || strings.Contains(string(raw), "fixture-") || strings.Contains(string(raw), "access_token") {
				t.Fatalf("quota wire response lost exhaustion or leaked credentials: %s", raw)
			}
		})
	}
}

func TestQuotaFetchRejectsInvalidCredentials(t *testing.T) {
	for _, req := range []quotaFetchRequest{
		{},
		{Provider: "codex", StorageJSON: []byte(`{"type":"cursor","access_token":"fixture-access"}`)},
		{Provider: "cursor", StorageJSON: []byte(`{"type":"codex","access_token":"fixture-access"}`)},
		{Provider: "cursor", StorageJSON: []byte(`{"type":"cursor"}`)},
		{Provider: "cursor", StorageJSON: []byte(`{`)},
	} {
		q := quotaRPC{fetch: func(context.Context, provider.AuthStorage) (provider.QuotaResponse, error) {
			t.Fatal("invalid credential reached the upstream quota fetch")
			return provider.QuotaResponse{}, nil
		}}
		if _, err := q.fetchQuota(context.Background(), req); err == nil {
			t.Fatal("invalid quota request was accepted")
		}
	}
}

func TestQuotaFetchPropagatesFailureAndUnknownUsage(t *testing.T) {
	want := errors.New("quota unavailable")
	q := quotaRPC{fetch: func(context.Context, provider.AuthStorage) (provider.QuotaResponse, error) {
		return provider.QuotaResponse{}, want
	}}
	_, err := q.fetchQuota(context.Background(), quotaFetchRequest{StorageJSON: []byte(`{"type":"cursor","access_token":"fixture-access"}`)})
	if !errors.Is(err, want) {
		t.Fatalf("fetch error = %v, want %v", err, want)
	}
	q.hostCall = func(string, any) (json.RawMessage, error) { return nil, want }
	_, err = q.fetchQuota(context.Background(), quotaFetchRequest{AuthIndex: "index-1"})
	if !errors.Is(err, want) {
		t.Fatalf("host callback error = %v, want %v", err, want)
	}
	response := normalizeQuota(provider.QuotaResponse{Quota: []provider.QuotaRow{{Key: "unknown"}}})
	if len(response.Groups) != 1 || len(response.Groups[0].Buckets) != 0 {
		t.Fatalf("unknown usage was reported as an exhausted bucket: %#v", response)
	}
}
