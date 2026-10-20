package provider

import (
	"encoding/base64"
	"regexp"
	"strings"
)

const encodedToolCallIDPrefix = "cursor_tc_"

var (
	safeToolCallID   = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	legacyMuseCallID = regexp.MustCompile(`^call_([0-9a-f]{32})_fc_([0-9a-f]{32})$`)
)

// Cursor IDs can contain provider metadata separated by a newline. Encode the
// entire ID for callers, then restore it in history instead of losing metadata
// through character replacement or truncation.
func normalizeToolCallID(value string) string {
	if strings.TrimSpace(value) == "" {
		return "call_" + mustUUID()
	}
	if safeToolCallID.MatchString(value) && !strings.HasPrefix(value, encodedToolCallIDPrefix) && !legacyMuseCallID.MatchString(value) {
		return value
	}
	return encodedToolCallIDPrefix + base64.RawURLEncoding.EncodeToString([]byte(value))
}

func restoreToolCallID(value string) string {
	if encoded, ok := strings.CutPrefix(value, encodedToolCallIDPrefix); ok && encoded != "" {
		if decoded, err := base64.RawURLEncoding.DecodeString(encoded); err == nil {
			return string(decoded)
		}
	}
	// Versions through 0.1.12 replaced the separator in Muse's call/item ID
	// pair. Recover this specific shape so existing conversations can continue.
	if match := legacyMuseCallID.FindStringSubmatch(value); len(match) == 3 && match[1] == match[2] {
		return "call_" + match[1] + "\nfc_" + match[2]
	}
	return value
}
