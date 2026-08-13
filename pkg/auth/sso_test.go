package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcquireDesignerTokenReacquiresExpiredBrokerToken(t *testing.T) {
	useTemporaryWorkingDirectory(t)
	tm := NewTokenManager("tenant", "client", "scope", "refresh", "cache")
	if err := tm.writeBrokerRefreshToken("expired-token"); err != nil {
		t.Fatalf("write expired broker refresh token: %v", err)
	}

	var requests []string
	tm.designerTokenRequest = func(refreshToken string) (string, int, error) {
		requests = append(requests, refreshToken)
		if refreshToken == "expired-token" {
			return "", 0, &designerOAuthError{
				Status:      http.StatusBadRequest,
				Code:        "invalid_grant",
				Description: "AADSTS700084: refresh token expired",
			}
		}
		return "designer-token", 3600, nil
	}

	acquisitions := 0
	tm.brokerTokenAcquisition = func() (string, error) {
		acquisitions++
		return "replacement-token", nil
	}

	token, expiresIn, err := tm.acquireDesignerToken()
	if err != nil {
		t.Fatalf("acquire designer token: %v", err)
	}
	if token != "designer-token" || expiresIn != 3600 {
		t.Fatalf("unexpected token result: token=%q expiresIn=%d", token, expiresIn)
	}
	if acquisitions != 1 {
		t.Fatalf("expected one SSO acquisition, got %d", acquisitions)
	}
	if len(requests) != 2 || requests[0] != "expired-token" || requests[1] != "replacement-token" {
		t.Fatalf("unexpected refresh token requests: %v", requests)
	}
}

func TestAcquireDesignerTokenDoesNotReacquireTransientFailure(t *testing.T) {
	useTemporaryWorkingDirectory(t)
	tm := NewTokenManager("tenant", "client", "scope", "refresh", "cache")
	if err := tm.writeBrokerRefreshToken("existing-token"); err != nil {
		t.Fatalf("write broker refresh token: %v", err)
	}

	transientErr := &designerOAuthError{
		Status:      http.StatusInternalServerError,
		Code:        "temporarily_unavailable",
		Description: "retry later",
	}
	tm.designerTokenRequest = func(string) (string, int, error) {
		return "", 0, transientErr
	}
	tm.brokerTokenAcquisition = func() (string, error) {
		t.Fatal("SSO acquisition must not run for transient failures")
		return "", nil
	}

	_, _, err := tm.acquireDesignerToken()
	if !errors.Is(err, transientErr) {
		t.Fatalf("expected transient error, got %v", err)
	}
}

func TestSaveM365CookiesEncryptsStore(t *testing.T) {
	useTemporaryWorkingDirectory(t)
	cookies := []SSOCookie{{Name: "M365Session", Value: "sensitive-value", Domain: "m365.cloud.microsoft"}}

	if err := SaveM365Cookies(cookies); err != nil {
		t.Fatalf("save M365 cookies: %v", err)
	}
	data, err := os.ReadFile(m365CookiesFile)
	if err != nil {
		t.Fatalf("read encrypted M365 cookies: %v", err)
	}
	if strings.Contains(string(data), "M365Session") || strings.Contains(string(data), "sensitive-value") {
		t.Fatal("encrypted M365 cookie store contains plaintext cookie data")
	}
	info, err := os.Stat(m365CookiesFile)
	if err != nil {
		t.Fatalf("stat M365 cookie store: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("M365 cookie store mode = %o, want 600", info.Mode().Perm())
	}

	tm := NewTokenManager("tenant", "client", "scope", "refresh", "cache")
	header, err := tm.M365CookieHeader()
	if err != nil {
		t.Fatalf("load encrypted M365 cookies: %v", err)
	}
	if header != "M365Session=sensitive-value" {
		t.Fatalf("unexpected M365 cookie header: %q", header)
	}
}

func TestM365CookieHeaderMigratesLegacyPlaintextStore(t *testing.T) {
	useTemporaryWorkingDirectory(t)
	writeM365Cookies(t, []SSOCookie{{Name: "M365Session", Value: "legacy-value", Domain: "m365.cloud.microsoft"}})

	tm := NewTokenManager("tenant", "client", "scope", "refresh", "cache")
	header, err := tm.M365CookieHeader()
	if err != nil {
		t.Fatalf("migrate legacy M365 cookies: %v", err)
	}
	if header != "M365Session=legacy-value" {
		t.Fatalf("unexpected M365 cookie header: %q", header)
	}
	data, err := os.ReadFile(m365CookiesFile)
	if err != nil {
		t.Fatalf("read migrated M365 cookies: %v", err)
	}
	if strings.Contains(string(data), "legacy-value") || json.Valid(data) {
		t.Fatal("legacy M365 cookie store was not replaced with ciphertext")
	}
}

func TestM365CookieHeaderRejectsCorruptCiphertextWithoutOverwrite(t *testing.T) {
	useTemporaryWorkingDirectory(t)
	original := []byte("not-valid-ciphertext")
	if err := os.WriteFile(m365CookiesFile, original, 0600); err != nil {
		t.Fatalf("write corrupt M365 cookies: %v", err)
	}

	tm := NewTokenManager("tenant", "client", "scope", "refresh", "cache")
	_, err := tm.M365CookieHeader()
	if !errors.Is(err, ErrM365CookiesUnavailable) {
		t.Fatalf("expected M365 cookies unavailable, got %v", err)
	}
	current, readErr := os.ReadFile(m365CookiesFile)
	if readErr != nil {
		t.Fatalf("read corrupt M365 cookies: %v", readErr)
	}
	if string(current) != string(original) {
		t.Fatal("corrupt ciphertext was overwritten")
	}
}

func TestSaveM365CookiesPreservesExistingFileWhenRenameFails(t *testing.T) {
	useTemporaryWorkingDirectory(t)
	original := []byte("existing-data")
	if err := os.WriteFile(m365CookiesFile, original, 0600); err != nil {
		t.Fatalf("write existing M365 cookies: %v", err)
	}
	previousRename := renameFile
	renameFile = func(string, string) error { return errors.New("rename failed") }
	t.Cleanup(func() { renameFile = previousRename })

	err := SaveM365Cookies([]SSOCookie{{Name: "M365Session", Value: "new-value", Domain: "m365.cloud.microsoft"}})
	if err == nil {
		t.Fatal("expected save failure")
	}
	current, readErr := os.ReadFile(m365CookiesFile)
	if readErr != nil {
		t.Fatalf("read existing M365 cookies: %v", readErr)
	}
	if string(current) != string(original) {
		t.Fatal("existing M365 cookie store changed after failed rename")
	}
}

func TestM365CookieHeaderFiltersCookiesByDomain(t *testing.T) {
	useTemporaryWorkingDirectory(t)
	writeM365Cookies(t, []SSOCookie{
		{Name: "ESTSAUTH", Value: "login-secret", Domain: "login.microsoftonline.com"},
		{Name: "M365Session", Value: "m365-secret", Domain: ".m365.cloud.microsoft"},
		{Name: "Empty", Value: "", Domain: "m365.cloud.microsoft"},
	})

	tm := NewTokenManager("tenant", "client", "scope", "refresh", "cache")
	header, err := tm.M365CookieHeader()
	if err != nil {
		t.Fatalf("build M365 cookie header: %v", err)
	}
	if header != "M365Session=m365-secret" {
		t.Fatalf("unexpected M365 cookie header: %q", header)
	}
}

func TestM365CookieHeaderRequiresM365DomainCookie(t *testing.T) {
	useTemporaryWorkingDirectory(t)
	writeM365Cookies(t, []SSOCookie{{Name: "ESTSAUTH", Value: "secret", Domain: "login.microsoftonline.com"}})

	tm := NewTokenManager("tenant", "client", "scope", "refresh", "cache")
	_, err := tm.M365CookieHeader()
	if !errors.Is(err, ErrM365CookiesUnavailable) {
		t.Fatalf("expected M365 cookies unavailable, got %v", err)
	}
}

func TestSummarizeBrokerAuthorizeResponsePrefersAADSTSError(t *testing.T) {
	body := `<!DOCTYPE html><html><head><title>Something went wrong</title></head><body>
<p>AADSTS50011: The reply URL specified in the request does not match the reply URLs configured for the application.</p>
</body></html>`

	summary := summarizeBrokerAuthorizeResponse(body)
	if !strings.Contains(summary, "AADSTS50011") {
		t.Fatalf("expected AADSTS error in summary, got %q", summary)
	}
	if strings.Contains(summary, "<") {
		t.Fatalf("expected summary without HTML markup, got %q", summary)
	}
}

func TestSummarizeBrokerAuthorizeResponseUsesTitleFallback(t *testing.T) {
	summary := summarizeBrokerAuthorizeResponse("<html><head><title>Something went wrong</title></head><body></body></html>")
	if summary != "page title: Something went wrong" {
		t.Fatalf("unexpected fallback summary: %q", summary)
	}
}

func writeM365Cookies(t *testing.T, cookies []SSOCookie) {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"domain":       "m365.cloud.microsoft",
		"extracted_at": "legacy-browser-timestamp",
		"cookies":      cookies,
	})
	if err != nil {
		t.Fatalf("marshal M365 cookies: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(m365CookiesFile), 0700); err != nil {
		t.Fatalf("create M365 cookie directory: %v", err)
	}
	if err := os.WriteFile(m365CookiesFile, data, 0600); err != nil {
		t.Fatalf("write M365 cookies: %v", err)
	}
}

func useTemporaryWorkingDirectory(t *testing.T) {
	t.Helper()
	originalDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("change working directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	if err := os.MkdirAll(filepath.Dir(designerBrokerRefreshFile), 0700); err != nil {
		t.Fatalf("create token directory: %v", err)
	}
}

func TestSaveSSOCookieBatchSplitsByDomain(t *testing.T) {
	useTemporaryWorkingDirectory(t)

	cookies := []SSOCookie{
		{Name: "ESTSAUTH", Value: "v1", Domain: "login.microsoftonline.com"},
		{Name: "ESTSAUTHPERSISTENT", Value: "v2", Domain: "login.microsoftonline.com"},
		{Name: "ccs", Value: "v3", Domain: "m365.cloud.microsoft"},
		{Name: "MC1", Value: "v4", Domain: "microsoft.com"},
		{Name: "Other", Value: "v5", Domain: "example.com"},
	}

	loginCount, m365Count, err := SaveSSOCookieBatch(cookies)
	if err != nil {
		t.Fatalf("SaveSSOCookieBatch: %v", err)
	}
	if loginCount != 2 {
		t.Errorf("expected 2 login cookies, got %d", loginCount)
	}
	if m365Count != 2 {
		t.Errorf("expected 2 m365 cookies, got %d", m365Count)
	}

	status := SSOStatus()
	if !status.LoginAvailable || status.LoginCookies != 2 {
		t.Errorf("unexpected login status: %+v", status)
	}
	if !status.M365Available || status.M365Cookies != 2 {
		t.Errorf("unexpected m365 status: %+v", status)
	}

	store, err := loadSSOCookieStore()
	if err != nil {
		t.Fatalf("load SSO cookies: %v", err)
	}
	if len(store.Cookies) != 2 {
		t.Fatalf("expected 2 stored login cookies, got %d", len(store.Cookies))
	}
	if store.Cookies[0].Name != "ESTSAUTH" || store.Cookies[0].Value != "v1" {
		t.Errorf("unexpected first login cookie: %+v", store.Cookies[0])
	}
}

func TestSSOStatusEmptyWhenNoCookies(t *testing.T) {
	useTemporaryWorkingDirectory(t)

	status := SSOStatus()
	if status.LoginAvailable || status.M365Available {
		t.Fatalf("expected empty status, got %+v", status)
	}
	if status.LoginCookies != 0 || status.M365Cookies != 0 {
		t.Fatalf("expected zero counts, got %+v", status)
	}
}

func TestSaveSSOCookieBatchForIsolatesAccounts(t *testing.T) {
	useTemporaryWorkingDirectory(t)

	accA := "11111111-1111-1111-1111-111111111111"
	accB := "22222222-2222-2222-2222-222222222222"
	cookies := []SSOCookie{
		{Name: "ESTSAUTH", Value: "a-login", Domain: "login.microsoftonline.com"},
		{Name: "ccs", Value: "a-m365", Domain: "m365.cloud.microsoft"},
	}
	loginCount, m365Count, err := SaveSSOCookieBatchFor(accA, cookies)
	if err != nil {
		t.Fatalf("SaveSSOCookieBatchFor: %v", err)
	}
	if loginCount != 1 || m365Count != 1 {
		t.Fatalf("unexpected counts: login=%d m365=%d", loginCount, m365Count)
	}

	if status := SSOStatusFor(accA); !status.LoginAvailable || status.LoginCookies != 1 || !status.M365Available || status.M365Cookies != 1 {
		t.Fatalf("account A status wrong: %+v", status)
	}
	if status := SSOStatusFor(accB); status.LoginAvailable || status.M365Available {
		t.Fatalf("account B must be isolated, got %+v", status)
	}
	if status := SSOStatus(); status.LoginAvailable || status.M365Available {
		t.Fatalf("global store must stay empty, got %+v", status)
	}

	if _, err := os.Stat(ssoCookiesFileFor(accA)); err != nil {
		t.Fatalf("account A login store missing: %v", err)
	}
	if _, err := os.Stat(ssoCookiesFileFor(accB)); err == nil {
		t.Fatal("account B login store must not exist")
	}
	if _, err := os.Stat(ssoCookiesFile); err == nil {
		t.Fatal("global login store must not exist")
	}

	storeA, err := loadSSOCookieStoreFor(accA)
	if err != nil {
		t.Fatalf("load account A login cookies: %v", err)
	}
	if len(storeA.Cookies) != 1 || storeA.Cookies[0].Value != "a-login" {
		t.Fatalf("unexpected account A login cookies: %+v", storeA.Cookies)
	}

	storeB, err := loadSSOCookieStoreFor(accB)
	if err == nil {
		t.Fatalf("account B load must fail, got %+v", storeB)
	}
}

func TestSSOCookiePathSanitizesAccountID(t *testing.T) {
	if got := ssoCookiesFileFor("user@example.com"); !strings.Contains(got, "user_example_com") {
		t.Fatalf("expected sanitised path, got %q", got)
	}
	if got := ssoCookiesFileFor(""); got != ssoCookiesFile {
		t.Fatalf("empty account id must map to global file, got %q", got)
	}
}

func TestLoadSSOCookiesForReturnsStore(t *testing.T) {
	useTemporaryWorkingDirectory(t)
	acc := "33333333-3333-3333-3333-333333333333"
	if err := SaveSSOCookiesFor(acc, []SSOCookie{{Name: "ESTSAUTH", Value: "v", Domain: "login.microsoftonline.com"}}); err != nil {
		t.Fatalf("save cookies: %v", err)
	}
	store, err := LoadSSOCookiesFor(acc)
	if err != nil {
		t.Fatalf("load cookies: %v", err)
	}
	if len(store.Cookies) != 1 || store.Cookies[0].Value != "v" {
		t.Fatalf("unexpected store: %+v", store.Cookies)
	}
	if _, err := LoadSSOCookiesFor("missing-account"); err == nil {
		t.Fatal("missing account must fail to load")
	}
}
