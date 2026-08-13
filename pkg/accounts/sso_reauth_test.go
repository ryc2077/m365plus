package accounts

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnsureValidUsesSSOReauthHookWhenRefreshFails(t *testing.T) {
	var ssoHookCalls int
	fakeToken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"AADSTS700084: refresh token expired"}`))
	}))
	defer fakeToken.Close()
	os.Setenv("M365_TOKEN_ENDPOINT", fakeToken.URL)
	defer os.Unsetenv("M365_TOKEN_ENDPOINT")

	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")
	now := time.Now()
	acc := AccountToken{
		ID:           "oid-hook",
		OID:          "oid-hook",
		Email:        "hook@example.com",
		AccessToken:  "stale",
		RefreshToken: "dead-refresh",
		ExpiresAt:    now.Add(-1 * time.Minute),
		UpdatedAt:    now.Add(-10 * time.Minute),
		TID:          "tenant-id",
	}
	data, _ := json.Marshal(Cache{Accounts: []AccountToken{acc}})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}

	s.SetSSOReauth(func(got AccountToken) (TokenSet, error) {
		ssoHookCalls++
		if got.ID != "oid-hook" {
			t.Fatalf("hook got wrong account: %+v", got)
		}
		return TokenSet{
			AccessToken:  "fresh-access",
			RefreshToken: "fresh-refresh",
			ExpiresAt:    now.Add(2 * time.Hour),
			Email:        got.Email,
			DisplayName:  got.DisplayName,
			HomeOID:      got.OID,
			TenantID:     got.TID,
		}, nil
	})

	got, err := s.EnsureValid("oid-hook")
	if err != nil {
		t.Fatalf("EnsureValid: %v", err)
	}
	if ssoHookCalls != 1 {
		t.Fatalf("expected 1 SSO reauth hook call, got %d", ssoHookCalls)
	}
	if got.AccessToken != "fresh-access" {
		t.Fatalf("expected fresh access token, got %q", got.AccessToken)
	}
	if got.Status != "online" {
		t.Fatalf("expected online status, got %q", got.Status)
	}
	persisted, ok := s.Get("oid-hook")
	if !ok {
		t.Fatal("persisted account not found")
	}
	if persisted.RefreshToken != "fresh-refresh" {
		t.Fatalf("expected rotated refresh token, got %q", persisted.RefreshToken)
	}
}

func TestEnsureValidFallsThroughToErrorWhenSSOReauthFails(t *testing.T) {
	var ssoHookCalls int
	fakeToken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"AADSTS700084: refresh token expired"}`))
	}))
	defer fakeToken.Close()
	os.Setenv("M365_TOKEN_ENDPOINT", fakeToken.URL)
	defer os.Unsetenv("M365_TOKEN_ENDPOINT")

	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")
	now := time.Now()
	acc := AccountToken{
		ID:           "oid-hook-fail",
		OID:          "oid-hook-fail",
		Email:        "fail@example.com",
		AccessToken:  "stale",
		RefreshToken: "dead-refresh",
		ExpiresAt:    now.Add(-1 * time.Minute),
		UpdatedAt:    now.Add(-10 * time.Minute),
	}
	data, _ := json.Marshal(Cache{Accounts: []AccountToken{acc}})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}

	s.SetSSOReauth(func(got AccountToken) (TokenSet, error) {
		ssoHookCalls++
		return TokenSet{}, os.ErrNotExist
	})

	_, err = s.EnsureValid("oid-hook-fail")
	if err == nil {
		t.Fatal("expected EnsureValid to fail when SSO reauth fails")
	}
	if ssoHookCalls != 1 {
		t.Fatalf("expected 1 SSO reauth hook call, got %d", ssoHookCalls)
	}
	persisted, ok := s.Get("oid-hook-fail")
	if !ok || persisted.Status != "expired" {
		t.Fatalf("expected account marked expired, got %+v", persisted)
	}
}
