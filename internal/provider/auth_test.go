package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestParseAuthPreservesRoutingWithoutPublishingTokens(t *testing.T) {
	token := testJWT(map[string]any{"sub": "user|account-1", "email": "person@example.test", "exp": 4_102_444_800})
	raw, _ := json.Marshal(AuthStorage{Type: ProviderID, Label: "Cursor One", AccessToken: token, Priority: 10, AllowedModels: []string{"cursor-grok-*"}})
	response, err := New().ParseAuth(context.Background(), pluginapi.AuthParseRequest{RawJSON: raw, FileName: "cursor-1.json"})
	if err != nil {
		t.Fatal(err)
	}
	if !response.Handled || len(response.Auths) != 1 {
		t.Fatalf("unexpected auth response: %+v", response)
	}
	auth := response.Auths[0]
	if auth.Provider != ProviderID || auth.Attributes["priority"] != "10" || auth.Metadata["email"] != "person@example.test" {
		t.Fatalf("unexpected auth metadata: %+v", auth)
	}
	metadataJSON, _ := json.Marshal(auth.Metadata)
	if strings.Contains(string(metadataJSON), token) {
		t.Fatal("access token leaked into public metadata")
	}
	if !strings.Contains(string(auth.StorageJSON), token) {
		t.Fatal("private auth storage did not retain the token")
	}
}

func TestStartLoginReturnsCursorPKCEURL(t *testing.T) {
	response, err := New().StartLogin(context.Background(), pluginapi.AuthLoginStartRequest{Metadata: map[string]any{"label": "Cursor Two", "priority": 7}})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"https://www.cursor.com/loginDeepControl?", "challenge=", "uuid=", "redirectTarget=cli"} {
		if !strings.Contains(response.URL, expected) {
			t.Fatalf("login URL missing %q: %s", expected, response.URL)
		}
	}
	if response.Provider != ProviderID || response.State == "" || response.ExpiresAt.IsZero() {
		t.Fatalf("unexpected login response: %+v", response)
	}
}

func testJWT(payload map[string]any) string {
	header, _ := json.Marshal(map[string]any{"alg": "none"})
	body, _ := json.Marshal(payload)
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body) + "."
}
