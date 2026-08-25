package provider

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type AuthStorage struct {
	Type          string   `json:"type"`
	ID            string   `json:"id,omitempty"`
	Label         string   `json:"label"`
	AccessToken   string   `json:"access_token"`
	RefreshToken  string   `json:"refresh_token,omitempty"`
	ExpiresAt     string   `json:"expires_at,omitempty"`
	Prefix        string   `json:"prefix,omitempty"`
	Priority      int      `json:"priority,omitempty"`
	AllowedModels []string `json:"allowed_models,omitempty"`
	DeniedModels  []string `json:"denied_models,omitempty"`
	Disabled      bool     `json:"disabled,omitempty"`
}

func (s *Service) ParseAuth(_ context.Context, req pluginapi.AuthParseRequest) (pluginapi.AuthParseResponse, error) {
	if req.Provider != "" && !strings.EqualFold(strings.TrimSpace(req.Provider), ProviderID) {
		return pluginapi.AuthParseResponse{Handled: false}, nil
	}
	var storage AuthStorage
	if err := json.Unmarshal(req.RawJSON, &storage); err != nil {
		if req.Provider == "" {
			return pluginapi.AuthParseResponse{Handled: false}, nil
		}
		return pluginapi.AuthParseResponse{}, fmt.Errorf("decode Cursor auth: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(storage.Type), ProviderID) {
		return pluginapi.AuthParseResponse{Handled: false}, nil
	}
	auth, err := s.authData(storage, req.FileName)
	if err != nil {
		return pluginapi.AuthParseResponse{}, err
	}
	return pluginapi.AuthParseResponse{Handled: true, Auth: auth, Auths: []pluginapi.AuthData{auth}}, nil
}

func (s *Service) authData(storage AuthStorage, fileName string) (pluginapi.AuthData, error) {
	storage.Type = ProviderID
	storage.Label = strings.TrimSpace(storage.Label)
	storage.AccessToken = strings.TrimSpace(storage.AccessToken)
	storage.RefreshToken = strings.TrimSpace(storage.RefreshToken)
	storage.Prefix = strings.TrimSpace(storage.Prefix)
	if storage.AccessToken == "" {
		return pluginapi.AuthData{}, fmt.Errorf("Cursor access token is required")
	}
	if storage.Label == "" {
		storage.Label = emailFromJWT(storage.AccessToken)
	}
	if storage.Label == "" {
		storage.Label = "Cursor account"
	}
	if storage.ID == "" {
		storage.ID = stableAuthID(storage.Label, storage.AccessToken)
	}
	if storage.ExpiresAt == "" {
		if expiry := jwtExpiry(storage.AccessToken); !expiry.IsZero() {
			storage.ExpiresAt = expiry.UTC().Format(time.RFC3339)
		}
	}
	raw, err := json.Marshal(storage)
	if err != nil {
		return pluginapi.AuthData{}, fmt.Errorf("encode Cursor auth: %w", err)
	}
	metadata := map[string]any{
		"type":       ProviderID,
		"auth_kind":  "oauth",
		"priority":   storage.Priority,
		"plan":       "unknown",
		"expires_at": storage.ExpiresAt,
	}
	if email := emailFromJWT(storage.AccessToken); email != "" {
		metadata["email"] = email
		metadata["account"] = email
	}
	attributes := map[string]string{
		"auth_kind": "oauth",
		"priority":  strconv.Itoa(storage.Priority),
		"boundary":  "cursor-connect-rpc",
	}
	return pluginapi.AuthData{
		Provider:         ProviderID,
		ID:               storage.ID,
		FileName:         strings.TrimSpace(fileName),
		Label:            storage.Label,
		Prefix:           storage.Prefix,
		Disabled:         storage.Disabled,
		StorageJSON:      raw,
		Metadata:         metadata,
		Attributes:       attributes,
		NextRefreshAfter: refreshAfter(storage),
	}, nil
}

func (s *Service) RefreshAuth(ctx context.Context, req pluginapi.AuthRefreshRequest) (pluginapi.AuthRefreshResponse, error) {
	storage, err := decodeAuth(req.StorageJSON)
	if err != nil {
		return pluginapi.AuthRefreshResponse{}, err
	}
	if shouldRefresh(storage) && storage.RefreshToken != "" {
		refreshed, refreshErr := exchangeRefreshToken(ctx, storage.RefreshToken, s.Config())
		if refreshErr != nil {
			return pluginapi.AuthRefreshResponse{}, refreshErr
		}
		storage.AccessToken = refreshed.AccessToken
		if refreshed.RefreshToken != "" {
			storage.RefreshToken = refreshed.RefreshToken
		}
		if expiry := jwtExpiry(storage.AccessToken); !expiry.IsZero() {
			storage.ExpiresAt = expiry.UTC().Format(time.RFC3339)
		}
	}
	auth, err := s.authData(storage, "")
	if err != nil {
		return pluginapi.AuthRefreshResponse{}, err
	}
	auth.ID = req.AuthID
	auth.FileName = ""
	return pluginapi.AuthRefreshResponse{Auth: auth, NextRefreshAfter: auth.NextRefreshAfter}, nil
}

func decodeAuth(raw []byte) (AuthStorage, error) {
	var storage AuthStorage
	if err := json.Unmarshal(raw, &storage); err != nil {
		return AuthStorage{}, fmt.Errorf("decode Cursor auth storage: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(storage.Type), ProviderID) || strings.TrimSpace(storage.AccessToken) == "" {
		return AuthStorage{}, fmt.Errorf("invalid Cursor auth storage")
	}
	return storage, nil
}

func stableAuthID(label, token string) string {
	subject := jwtStringClaim(token, "sub")
	if subject == "" {
		subject = strings.ToLower(strings.TrimSpace(label))
	}
	sum := sha256.Sum256([]byte(subject))
	return fmt.Sprintf("cursor-%x", sum[:8])
}

func refreshAfter(storage AuthStorage) time.Time {
	expiry := jwtExpiry(storage.AccessToken)
	if expiry.IsZero() {
		return time.Now().UTC().Add(30 * time.Minute)
	}
	refreshAt := expiry.Add(-30 * time.Minute)
	if refreshAt.Before(time.Now().UTC().Add(time.Minute)) {
		return time.Now().UTC().Add(time.Minute)
	}
	return refreshAt
}

func shouldRefresh(storage AuthStorage) bool {
	expiry := jwtExpiry(storage.AccessToken)
	return !expiry.IsZero() && time.Until(expiry) <= 30*time.Minute
}

func jwtExpiry(token string) time.Time {
	part := jwtPayload(token)
	if part == nil {
		return time.Time{}
	}
	switch value := part["exp"].(type) {
	case float64:
		return time.Unix(int64(value), 0)
	case json.Number:
		seconds, _ := value.Int64()
		return time.Unix(seconds, 0)
	}
	return time.Time{}
}

func emailFromJWT(token string) string {
	for _, key := range []string{"email", "user_email"} {
		if value := jwtStringClaim(token, key); strings.Contains(value, "@") {
			return value
		}
	}
	return ""
}

func jwtStringClaim(token, key string) string {
	payload := jwtPayload(token)
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}

func jwtPayload(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var payload map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil
	}
	return payload
}
