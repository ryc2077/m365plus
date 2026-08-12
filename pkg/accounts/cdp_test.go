package accounts

import (
	"strings"
	"testing"
	"time"
)

// buildTestJWT crafts a base64 JWT with the given claims for exercising the
// substrate-token selection helpers (no signature verification here).
func buildTestJWT(claims map[string]string) string {
	header := "eyJhbGciOiJub25lIn0" // {"alg":"none"}
	payload := `{`
	first := true
	for k, v := range claims {
		if !first {
			payload += ","
		}
		first = false
		payload += `"` + k + `":"` + v + `"`
	}
	payload += `}`
	payload = base64Raw(payload)
	return header + "." + payload + "."
}

func base64Raw(s string) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	bin := []byte(s)
	var out strings.Builder
	for i := 0; i < len(bin); i += 3 {
		var n uint32
		rem := len(bin) - i
		n = uint32(bin[i]) << 16
		if rem > 1 {
			n |= uint32(bin[i+1]) << 8
		}
		if rem > 2 {
			n |= uint32(bin[i+2])
		}
		out.WriteByte(alphabet[(n>>18)&63])
		out.WriteByte(alphabet[(n>>12)&63])
		if rem > 1 {
			out.WriteByte(alphabet[(n>>6)&63])
		}
		if rem > 2 {
			out.WriteByte(alphabet[n&63])
		}
	}
	return out.String()
}

func substrateToken(claims map[string]string) string {
	if claims == nil {
		claims = map[string]string{}
	}
	if claims["aud"] == "" {
		claims["aud"] = "https://substrate.office.com/sydney"
	}
	if claims["exp"] == "" {
		claims["exp"] = "9999999999"
	}
	return buildTestJWT(claims)
}

func TestSelectSubstrateToken(t *testing.T) {
	sub := substrateToken(map[string]string{"upn": "alice@example.com"})
	other := substrateToken(map[string]string{"upn": "bob@example.com"})
	nonSubstrate := buildTestJWT(map[string]string{"aud": "https://graph.microsoft.com"})

	tests := []struct {
		name        string
		candidates  []string
		expected    string
		email       string
		expectEmpty bool
	}{
		{
			name:       "prefers matching identity",
			candidates: []string{other, sub},
			expected:   sub,
			email:      "alice@example.com",
		},
		{
			name:       "falls back to first valid when no identity match",
			candidates: []string{other, sub},
			expected:   other,
			email:      "carol@example.com",
		},
		{
			name:       "falls back to first valid token (no expected email)",
			candidates: []string{other, sub},
			expected:   other,
		},
		{
			name:       "rejects non-substrate tokens",
			candidates: []string{nonSubstrate},
			expectEmpty: true,
		},
		{
			name:        "empty candidates yields empty result",
			candidates:  nil,
			expectEmpty: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := selectSubstrateToken(tc.candidates, tc.email)
			if tc.expectEmpty {
				if got != "" {
					t.Fatalf("expected empty, got %q", got)
				}
				return
			}
			if got != tc.expected {
				t.Fatalf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}

func TestIsSubstrateToken(t *testing.T) {
	if !isSubstrateToken(substrateToken(nil)) {
		t.Fatal("substrate token should be recognized")
	}
	if isSubstrateToken(buildTestJWT(map[string]string{"aud": "https://graph.microsoft.com"})) {
		t.Fatal("graph token should not be recognized")
	}
	if isSubstrateToken("garbage") {
		t.Fatal("garbage should not be recognized")
	}
}

func TestParseExp(t *testing.T) {
	now := time.Now().Unix()
	ts, err := parseExp("9999999999")
	if err != nil {
		t.Fatal(err)
	}
	if ts.Unix() != 9999999999 {
		t.Fatalf("unexpected timestamp %d", ts.Unix())
	}
	if _, err := parseExp("abc"); err == nil {
		t.Fatal("expected error for non-numeric exp")
	}
	if _, err := parseExp(""); err == nil {
		t.Fatal("expected error for empty exp")
	}
	if now > 0 {
		_ = now
	}
}
