package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type pollResponse struct {
	AccessToken  string `json:"accessToken"`
	APIKey       string `json:"apiKey"`
	RefreshToken string `json:"refreshToken"`
	AuthID       string `json:"authId"`
}

func (s *Service) StartLogin(_ context.Context, req pluginapi.AuthLoginStartRequest) (pluginapi.AuthLoginStartResponse, error) {
	verifier, err := randomBase64URL(32)
	if err != nil {
		return pluginapi.AuthLoginStartResponse{}, fmt.Errorf("create Cursor login verifier: %w", err)
	}
	id, err := randomUUID()
	if err != nil {
		return pluginapi.AuthLoginStartResponse{}, fmt.Errorf("create Cursor login id: %w", err)
	}
	label := metadataString(req.Metadata, "label")
	prefix := metadataString(req.Metadata, "prefix")
	priority := metadataInt(req.Metadata, "priority")
	expiresAt := s.now().UTC().Add(5 * time.Minute)
	s.loginMu.Lock()
	s.logins[id] = &loginSession{Verifier: verifier, ExpiresAt: expiresAt, Label: label, Prefix: prefix, Priority: priority}
	s.loginMu.Unlock()
	return pluginapi.AuthLoginStartResponse{
		Provider:  ProviderID,
		URL:       loginURL(id, verifier),
		State:     id,
		ExpiresAt: expiresAt,
		Metadata: map[string]any{
			"message": "Approve the Cursor browser login, then wait for CLIProxyAPI to finish polling.",
		},
	}, nil
}

func (s *Service) PollLogin(ctx context.Context, req pluginapi.AuthLoginPollRequest) (pluginapi.AuthLoginPollResponse, error) {
	state := strings.TrimSpace(req.State)
	s.loginMu.Lock()
	session := s.logins[state]
	s.loginMu.Unlock()
	if session == nil {
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError, Message: "unknown Cursor login state"}, nil
	}
	if !s.now().Before(session.ExpiresAt) {
		s.loginMu.Lock()
		delete(s.logins, state)
		s.loginMu.Unlock()
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError, Message: "Cursor login expired"}, nil
	}
	values := url.Values{}
	values.Set("uuid", state)
	values.Set("verifier", session.Verifier)
	headers := make(http.Header)
	headers.Set("Accept", "application/json")
	var polled pollResponse
	_, _, err := doJSON(ctx, http.MethodGet, strings.TrimRight(s.Config().CursorBaseURL, "/")+"/auth/poll?"+values.Encode(), headers, nil, 15*time.Second, &polled)
	if err != nil {
		if upstream, ok := err.(*UpstreamError); ok && (upstream.Status == http.StatusNotFound || upstream.Status == http.StatusAccepted) {
			return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusPending, Message: "waiting for Cursor login approval"}, nil
		}
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError, Message: "Cursor login poll failed"}, nil
	}
	accessToken := strings.TrimSpace(polled.AccessToken)
	if accessToken == "" {
		accessToken = strings.TrimSpace(polled.APIKey)
	}
	if accessToken == "" {
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusPending, Message: "waiting for Cursor login approval"}, nil
	}
	label := strings.TrimSpace(session.Label)
	if label == "" {
		label = emailFromJWT(accessToken)
	}
	if label == "" {
		label = "Cursor account"
	}
	storage := AuthStorage{
		Type:         ProviderID,
		Label:        label,
		AccessToken:  accessToken,
		RefreshToken: strings.TrimSpace(polled.RefreshToken),
		Prefix:       strings.TrimSpace(session.Prefix),
		Priority:     session.Priority,
	}
	auth, err := s.authData(storage, "")
	if err != nil {
		return pluginapi.AuthLoginPollResponse{}, err
	}
	auth.FileName = auth.ID + ".json"
	s.loginMu.Lock()
	delete(s.logins, state)
	s.loginMu.Unlock()
	return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusSuccess, Message: "Cursor login complete", Auth: auth, Auths: []pluginapi.AuthData{auth}}, nil
}

func metadataString(metadata map[string]any, key string) string {
	value, _ := metadata[key].(string)
	return strings.TrimSpace(value)
}

func metadataInt(metadata map[string]any, key string) int {
	switch value := metadata[key].(type) {
	case int:
		return value
	case float64:
		return int(value)
	case json.Number:
		parsed, _ := strconv.Atoi(value.String())
		return parsed
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(value))
		return parsed
	default:
		return 0
	}
}
