//go:build importaccount

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type sourceAccount struct {
	Label        string `json:"label"`
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresAt    string `json:"expiresAt"`
}

type targetAccount struct {
	Type          string   `json:"type"`
	Label         string   `json:"label"`
	AccessToken   string   `json:"access_token"`
	RefreshToken  string   `json:"refresh_token,omitempty"`
	ExpiresAt     string   `json:"expires_at,omitempty"`
	Prefix        string   `json:"prefix,omitempty"`
	Priority      int      `json:"priority,omitempty"`
	AllowedModels []string `json:"allowed_models,omitempty"`
	DeniedModels  []string `json:"denied_models,omitempty"`
}

func main() {
	input := flag.String("in", "", "source Cursor account JSON")
	output := flag.String("out", "", "target CLIProxyAPI auth JSON")
	label := flag.String("label", "", "account label override")
	prefix := flag.String("prefix", "", "model prefix")
	priority := flag.Int("priority", 0, "routing priority")
	allow := flag.String("allow", "", "comma-separated model globs")
	deny := flag.String("deny", "", "comma-separated model globs")
	flag.Parse()
	if strings.TrimSpace(*input) == "" || strings.TrimSpace(*output) == "" {
		fmt.Fprintln(os.Stderr, "-in and -out are required")
		os.Exit(2)
	}
	raw, err := os.ReadFile(*input)
	if err != nil {
		panic(err)
	}
	var source sourceAccount
	if strings.HasPrefix(strings.TrimSpace(string(raw)), "[") {
		var accounts []sourceAccount
		if err := json.Unmarshal(raw, &accounts); err != nil {
			panic(err)
		}
		if len(accounts) == 0 {
			panic("source account array is empty")
		}
		source = accounts[0]
	} else if err := json.Unmarshal(raw, &source); err != nil {
		panic(err)
	}
	if strings.TrimSpace(source.AccessToken) == "" {
		panic("source account has no accessToken")
	}
	resolvedLabel := strings.TrimSpace(*label)
	if resolvedLabel == "" {
		resolvedLabel = strings.TrimSpace(source.Label)
	}
	target := targetAccount{
		Type: "cursor", Label: resolvedLabel, AccessToken: source.AccessToken,
		RefreshToken: source.RefreshToken, ExpiresAt: source.ExpiresAt,
		Prefix: strings.TrimSpace(*prefix), Priority: *priority,
		AllowedModels: splitCSV(*allow), DeniedModels: splitCSV(*deny),
	}
	encoded, err := json.MarshalIndent(target, "", "  ")
	if err != nil {
		panic(err)
	}
	encoded = append(encoded, '\n')
	if err := os.MkdirAll(filepath.Dir(*output), 0o700); err != nil {
		panic(err)
	}
	if err := os.WriteFile(*output, encoded, 0o600); err != nil {
		panic(err)
	}
	fmt.Printf("wrote %s label=%s priority=%d allow=%d deny=%d\n", *output, resolvedLabel, *priority, len(target.AllowedModels), len(target.DeniedModels))
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
