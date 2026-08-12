package accounts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnsureValidRefreshInterval(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")
	now := time.Now()
	acc := AccountToken{
		ID:           "oid-int",
		OID:          "oid-int",
		Email:        "user@example.com",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    now.Add(2 * time.Hour),
		UpdatedAt:    now.Add(-5 * time.Minute),
	}
	data, _ := json.Marshal(Cache{Accounts: []AccountToken{acc}})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}

	// Without an interval, a still-valid token is returned as-is.
	got, err := s.EnsureValid("oid-int")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.AccessToken != "access-1" {
		t.Fatalf("expected cached token, got %q", got.AccessToken)
	}

	// A short interval forces a refresh even though the token is valid; the
	// fake refresh token fails at the network layer, which must surface.
	s.SetRefreshInterval(time.Minute)
	if _, err := s.EnsureValid("oid-int"); err == nil {
		t.Fatal("expected refresh attempt to fail with invalid refresh token")
	}
}
