package provider

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/http2"
)

const maxJSONResponseBytes = 4 << 20

func newH2Client(timeout time.Duration) *http.Client {
	transport := &http2.Transport{
		DialTLSContext: func(ctx context.Context, network, address string, cfg *tls.Config) (net.Conn, error) {
			dialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}, Config: cfg}
			return dialer.DialContext(ctx, network, address)
		},
	}
	return &http.Client{Transport: transport, Timeout: timeout}
}

func doJSON(ctx context.Context, method, endpoint string, headers http.Header, body []byte, timeout time.Duration, target any) (int, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("build Cursor request: %w", err)
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := newH2Client(timeout).Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("send Cursor request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxJSONResponseBytes+1))
	if err != nil {
		return resp.StatusCode, resp.Header, fmt.Errorf("read Cursor response: %w", err)
	}
	if len(raw) > maxJSONResponseBytes {
		return resp.StatusCode, resp.Header, fmt.Errorf("Cursor response exceeds %d bytes", maxJSONResponseBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, resp.Header, &UpstreamError{Status: resp.StatusCode, Body: raw, Headers: resp.Header}
	}
	if target != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, target); err != nil {
			return resp.StatusCode, resp.Header, fmt.Errorf("decode Cursor response: %w", err)
		}
	}
	return resp.StatusCode, resp.Header, nil
}

type refreshResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
}

func exchangeRefreshToken(ctx context.Context, refresh string, cfg Config) (refreshResponse, error) {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+refresh)
	headers.Set("Content-Type", "application/json")
	headers.Set("Accept", "application/json")
	var response refreshResponse
	_, _, err := doJSON(ctx, http.MethodPost, strings.TrimRight(cfg.CursorBaseURL, "/")+"/auth/exchange_user_api_key", headers, []byte("{}"), 30*time.Second, &response)
	if err != nil {
		return refreshResponse{}, fmt.Errorf("refresh Cursor access token: %w", err)
	}
	if strings.TrimSpace(response.AccessToken) == "" {
		return refreshResponse{}, fmt.Errorf("Cursor refresh response contained no access token")
	}
	return response, nil
}

func randomBase64URL(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func randomUUID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := make([]byte, 32)
	hex.Encode(encoded, raw)
	return fmt.Sprintf("%s-%s-%s-%s-%s", encoded[:8], encoded[8:12], encoded[12:16], encoded[16:20], encoded[20:]), nil
}

func loginURL(uuid, verifier string) string {
	challenge := sha256.Sum256([]byte(verifier))
	values := url.Values{}
	values.Set("challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	values.Set("uuid", uuid)
	values.Set("mode", "login")
	values.Set("redirectTarget", "cli")
	return "https://www.cursor.com/loginDeepControl?" + values.Encode()
}

type UpstreamError struct {
	Status  int
	Body    []byte
	Headers http.Header
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("Cursor upstream returned HTTP %d", e.Status)
}
