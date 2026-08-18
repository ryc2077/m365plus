package webadmin

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryc2077/m365plus/pkg/accounts"
)

func seedStoreN(t *testing.T, dir string, n int) *accounts.Store {
	t.Helper()
	path := filepath.Join(dir, "accounts.json")
	os.MkdirAll(dir, 0o700)
	var list []accounts.AccountToken
	for i := range n {
		id := "oid-" + string(rune('a'+i))
		list = append(list, accounts.AccountToken{
			ID:           id,
			OID:          id,
			Email:        "user" + string(rune('a'+i)) + "@example.com",
			AccessToken:  "access-" + string(rune('a'+i)),
			RefreshToken: "refresh-" + string(rune('a'+i)),
			ExpiresAt:    time.Now().Add(30 * time.Minute),
			UpdatedAt:    time.Now(),
		})
	}
	data, _ := json.Marshal(accounts.Cache{Accounts: list})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := accounts.OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// TestNextRotationAccountCycles verifies the request-count rotation: with a
// limit of 2 requests per account, the same account serves two requests, then
// the next account takes over, and the cycle wraps around.
func TestNextRotationAccountCycles(t *testing.T) {
	dir := t.TempDir()
	s := &Server{tokens: seedStoreN(t, dir, 2), accountPool: newAccountHealth()}
	s.settings = openSettingsStore()
	cfg := s.settings.get()
	cfg.MaxRequestsPerAccount = 2
	cfg.AutoRotateAccounts = true
	if err := s.settings.save(cfg); err != nil {
		t.Fatal(err)
	}

	seen := []string{}
	for i := range 6 {
		acc, err := s.nextRotationAccount()
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		seen = append(seen, acc.ID)
	}
	// Expect A,A,B,B,A,A with two accounts and limit 2.
	want := []string{"oid-a", "oid-a", "oid-b", "oid-b", "oid-a", "oid-a"}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("iter %d: got %s want %s (full %v)", i, seen[i], want[i], seen)
		}
	}
}

func TestNextRotationAccountLogsSwitch(t *testing.T) {
	dir := t.TempDir()
	s := &Server{tokens: seedStoreN(t, dir, 2), accountPool: newAccountHealth()}
	s.settings = openSettingsStore()
	cfg := s.settings.get()
	cfg.MaxRequestsPerAccount = 1
	cfg.AutoRotateAccounts = true
	if err := s.settings.save(cfg); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previousWriter) })

	if _, err := s.nextRotationAccount(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.nextRotationAccount(); err != nil {
		t.Fatal(err)
	}

	logs := output.String()
	for _, expected := range []string{
		"[account-rotation] switched account",
		"from=oid-a",
		"to=oid-b",
		"email=userb@example.com",
		"reason=request_limit",
		"max_requests=1",
	} {
		if !strings.Contains(logs, expected) {
			t.Fatalf("rotation log %q does not contain %q", logs, expected)
		}
	}
}

// TestNextRotationAccountSkipsUnavailable verifies a cooling-down account is
// skipped even when it would otherwise serve the next request in the cycle.
func TestNextRotationAccountSkipsUnavailable(t *testing.T) {
	dir := t.TempDir()
	s := &Server{tokens: seedStoreN(t, dir, 2), accountPool: newAccountHealth()}
	s.settings = openSettingsStore()
	cfg := s.settings.get()
	cfg.MaxRequestsPerAccount = 2
	cfg.AutoRotateAccounts = true
	if err := s.settings.save(cfg); err != nil {
		t.Fatal(err)
	}

	// Account a serves one request, then gets rate-limited.
	acc, err := s.nextRotationAccount()
	if err != nil {
		t.Fatal(err)
	}
	if acc.ID != "oid-a" {
		t.Fatalf("expected oid-a first, got %s", acc.ID)
	}
	s.accountPool.MarkFailure("oid-a", &UpstreamHTTPError{Status: 429}, time.Minute)

	// Next request must skip a and use b.
	acc, err = s.nextRotationAccount()
	if err != nil {
		t.Fatal(err)
	}
	if acc.ID != "oid-b" {
		t.Fatalf("expected oid-b after rate limit, got %s", acc.ID)
	}
}

// TestNextRotationAccountNoLimit verifies legacy round-robin when the limit is
// disabled (0).
func TestNextRotationAccountNoLimit(t *testing.T) {
	dir := t.TempDir()
	s := &Server{tokens: seedStoreN(t, dir, 2), accountPool: newAccountHealth()}
	s.settings = openSettingsStore()
	cfg := s.settings.get()
	cfg.MaxRequestsPerAccount = 0
	cfg.AutoRotateAccounts = true
	if err := s.settings.save(cfg); err != nil {
		t.Fatal(err)
	}

	seen := []string{}
	for range 4 {
		acc, err := s.nextRotationAccount()
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, acc.ID)
	}
	want := []string{"oid-a", "oid-b", "oid-a", "oid-b"}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("iter %d: got %s want %s (full %v)", i, seen[i], want[i], seen)
		}
	}
}

// TestNextRotationAccountPinned verifies that with auto-rotation disabled the
// account stays pinned so incremental sessions can continue on the same M365
// account; the pin only moves when the account becomes unavailable.
func TestNextRotationAccountPinned(t *testing.T) {
	dir := t.TempDir()
	s := &Server{tokens: seedStoreN(t, dir, 2), accountPool: newAccountHealth()}
	s.settings = openSettingsStore()
	cfg := s.settings.get()
	cfg.AutoRotateAccounts = false
	if err := s.settings.save(cfg); err != nil {
		t.Fatal(err)
	}

	for i := range 4 {
		acc, err := s.nextRotationAccount()
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if acc.ID != "oid-a" {
			t.Fatalf("iter %d: expected pinned oid-a, got %s", i, acc.ID)
		}
	}

	s.accountPool.MarkFailure("oid-a", &UpstreamHTTPError{Status: 429}, time.Minute)
	acc, err := s.nextRotationAccount()
	if err != nil {
		t.Fatal(err)
	}
	if acc.ID != "oid-b" {
		t.Fatalf("expected oid-b after pinned account failed, got %s", acc.ID)
	}
	acc, err = s.nextRotationAccount()
	if err != nil {
		t.Fatal(err)
	}
	if acc.ID != "oid-b" {
		t.Fatalf("expected oid-b to stay pinned, got %s", acc.ID)
	}
}

// TestSwitchAccount verifies the manual switch pins a specific account and
// resets the rotation request counter.
func TestSwitchAccount(t *testing.T) {
	dir := t.TempDir()
	s := &Server{tokens: seedStoreN(t, dir, 2), accountPool: newAccountHealth()}
	s.settings = openSettingsStore()
	cfg := s.settings.get()
	cfg.MaxRequestsPerAccount = 2
	cfg.AutoRotateAccounts = true
	if err := s.settings.save(cfg); err != nil {
		t.Fatal(err)
	}

	acc, err := s.nextRotationAccount()
	if err != nil {
		t.Fatal(err)
	}
	if acc.ID != "oid-a" {
		t.Fatalf("expected oid-a first, got %s", acc.ID)
	}

	if err := s.switchToAccount("oid-b"); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		acc, err = s.nextRotationAccount()
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if acc.ID != "oid-b" {
			t.Fatalf("iter %d: expected switched oid-b, got %s", i, acc.ID)
		}
	}
}

// TestSettingsMutualExclusion verifies saving settings with auto-rotation
// enabled forces incremental context off.
func TestSettingsMutualExclusion(t *testing.T) {
	s := openSettingsStore()
	cfg := s.get()
	cfg.AutoRotateAccounts = true
	cfg.IncrementalContext = true
	if err := s.save(cfg); err != nil {
		t.Fatal(err)
	}
	got := s.get()
	if !got.AutoRotateAccounts {
		t.Fatal("expected autoRotateAccounts to stay true")
	}
	if got.IncrementalContext {
		t.Fatal("expected incrementalContext to be forced off when auto-rotation is on")
	}
}

// TestIncrementalContextEnabled verifies the resolver reports rotation and
// incremental context as mutually exclusive.
func TestIncrementalContextEnabled(t *testing.T) {
	s := &Server{settings: openSettingsStore()}

	cfg := s.settings.get()
	cfg.AutoRotateAccounts = false
	cfg.IncrementalContext = true
	if err := s.settings.save(cfg); err != nil {
		t.Fatal(err)
	}
	if !s.IncrementalContextEnabled() {
		t.Fatal("expected incremental context enabled with rotation off")
	}

	cfg = s.settings.get()
	cfg.AutoRotateAccounts = true
	if err := s.settings.save(cfg); err != nil {
		t.Fatal(err)
	}
	if s.IncrementalContextEnabled() {
		t.Fatal("expected incremental context disabled when rotation is on")
	}
}
