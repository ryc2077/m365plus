// Package auth provides SSO cookie-based re-authentication as a fallback
// when the refresh token expires (AADSTS700084).
// SSO cookies (ESTSAUTH, ESTSAUTHPERSISTENT) on login.microsoftonline.com
// last weeks/months, unlike SPA refresh tokens which expire after 24 hours.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ryc2077/m365plus/pkg/crypto"
	"github.com/ryc2077/m365plus/pkg/logging"
)

const (
	// ssoCookiesFile is the encrypted SSO cookie store.
	ssoCookiesFile = "data/tokens/sso_cookies.json"
	// m365CookiesFile is the browser-exported M365 web cookie store.
	m365CookiesFile = "data/tokens/m365_cookies.json"
	// authorizeURLTemplate is the OAuth2 authorize endpoint for silent re-auth.
	authorizeURLTemplate = "https://login.microsoftonline.com/%s/oauth2/v2.0/authorize"
	// defaultRedirectURI is the redirect URI registered for the M365 Copilot SPA app.
	defaultRedirectURI = "https://m365.cloud.microsoft/spalanding"
)

// ErrM365CookiesUnavailable indicates that no cookies for the M365 web app are stored.
var ErrM365CookiesUnavailable = errors.New("M365 web app cookies unavailable")

// ssoCookiesFileFor returns the per-account SSO cookie store path. An empty
// accountID maps to the legacy global store so single-account setups keep
// working unchanged.
func ssoCookiesFileFor(accountID string) string {
	if accountID == "" {
		return ssoCookiesFile
	}
	return filepath.Join(filepath.Dir(ssoCookiesFile), "sso_cookies_"+sanitizeAccountID(accountID)+".json")
}

// m365CookiesFileFor returns the per-account M365 web cookie store path.
func m365CookiesFileFor(accountID string) string {
	if accountID == "" {
		return m365CookiesFile
	}
	return filepath.Join(filepath.Dir(m365CookiesFile), "m365_cookies_"+sanitizeAccountID(accountID)+".json")
}

// sanitizeAccountID turns an account id (oid or email) into a safe file-name
// fragment, mirroring the CDP profile directory sanitisation.
func sanitizeAccountID(id string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '_'
	}, id)
}

// SSOCookie represents a browser cookie used by M365 authentication or web APIs.
type SSOCookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Path     string `json:"path,omitempty"`
	Domain   string `json:"domain,omitempty"`
	Secure   bool   `json:"secure,omitempty"`
	HttpOnly bool   `json:"httpOnly,omitempty"`
}

// SSOCookieStore holds all SSO cookies needed for silent re-authentication.
type SSOCookieStore struct {
	Cookies    []SSOCookie `json:"cookies"`
	CapturedAt time.Time   `json:"capturedAt"`
}

// m365CookieStore holds browser cookies used by M365 web APIs.
type m365CookieStore struct {
	Domain      string      `json:"domain"`
	ExtractedAt time.Time   `json:"extracted_at"`
	Cookies     []SSOCookie `json:"cookies"`
}

var renameFile = os.Rename

// generatePKCE creates a PKCE code verifier and code challenge (S256).
func generatePKCE() (verifier, challenge string, err error) {
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return "", "", fmt.Errorf("failed to generate code verifier: %w", err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(verifierBytes)

	hash := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(hash[:])

	return verifier, challenge, nil
}

// SaveSSOCookies encrypts and stores SSO cookies to disk.
func SaveSSOCookies(cookies []SSOCookie) error {
	store := SSOCookieStore{
		Cookies:    cookies,
		CapturedAt: time.Now(),
	}

	data, err := json.Marshal(store)
	if err != nil {
		return fmt.Errorf("failed to marshal SSO cookies: %w", err)
	}

	encrypted, err := crypto.Encrypt(string(data))
	if err != nil {
		return fmt.Errorf("failed to encrypt SSO cookies: %w", err)
	}

	return atomicWriteFile(ssoCookiesFile, []byte(encrypted), 0600)
}

// SaveSSOCookiesFor stores SSO cookies for a specific account, isolated from
// other accounts by a per-account file. An empty accountID writes the legacy
// global store.
func SaveSSOCookiesFor(accountID string, cookies []SSOCookie) error {
	store := SSOCookieStore{
		Cookies:    cookies,
		CapturedAt: time.Now(),
	}

	data, err := json.Marshal(store)
	if err != nil {
		return fmt.Errorf("failed to marshal SSO cookies: %w", err)
	}

	encrypted, err := crypto.Encrypt(string(data))
	if err != nil {
		return fmt.Errorf("failed to encrypt SSO cookies: %w", err)
	}

	return atomicWriteFile(ssoCookiesFileFor(accountID), []byte(encrypted), 0600)
}

// SaveM365Cookies encrypts and stores browser cookies used by M365 web APIs.
func SaveM365Cookies(cookies []SSOCookie) error {
	store := m365CookieStore{
		Domain:      "m365.cloud.microsoft",
		ExtractedAt: time.Now(),
		Cookies:     cookies,
	}
	return saveM365CookieStore(store)
}

// SaveM365CookiesFor stores M365 web cookies for a specific account.
func SaveM365CookiesFor(accountID string, cookies []SSOCookie) error {
	store := m365CookieStore{
		Domain:      "m365.cloud.microsoft",
		ExtractedAt: time.Now(),
		Cookies:     cookies,
	}
	return saveM365CookieStoreFor(accountID, store)
}

func saveM365CookieStore(store m365CookieStore) error {
	return saveM365CookieStoreFor("", store)
}

func saveM365CookieStoreFor(accountID string, store m365CookieStore) error {
	data, err := json.Marshal(store)
	if err != nil {
		return fmt.Errorf("failed to marshal M365 cookies: %w", err)
	}
	encrypted, err := crypto.Encrypt(string(data))
	if err != nil {
		return fmt.Errorf("failed to encrypt M365 cookies: %w", err)
	}
	if err := atomicWriteFile(m365CookiesFileFor(accountID), []byte(encrypted), 0600); err != nil {
		return fmt.Errorf("failed to save M365 cookies: %w", err)
	}
	return nil
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) (returnErr error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	temporary, err := os.CreateTemp(dir, ".m365-cookies-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		if returnErr != nil {
			temporary.Close()
			os.Remove(temporaryPath)
		}
	}()

	if err := temporary.Chmod(mode); err != nil {
		return fmt.Errorf("failed to set temporary file permissions: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("failed to write temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("failed to sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("failed to close temporary file: %w", err)
	}
	if err := renameFile(temporaryPath, path); err != nil {
		return fmt.Errorf("failed to replace file: %w", err)
	}
	return nil
}

// SSOCookieStatus describes the currently stored SSO cookie state. Cookie
// values are never exposed, only counts and capture timestamps.
type SSOCookieStatus struct {
	LoginAvailable bool      `json:"loginAvailable"`
	LoginCookies   int       `json:"loginCookies"`
	LoginCaptured  time.Time `json:"loginCapturedAt,omitempty"`
	M365Available  bool      `json:"m365Available"`
	M365Cookies    int       `json:"m365Cookies"`
	M365Captured   time.Time `json:"m365CapturedAt,omitempty"`
}

// SSOStatus reports whether SSO and M365 web cookies are stored and how many
// cookies each store holds, without exposing their values.
func SSOStatus() SSOCookieStatus {
	return SSOStatusFor("")
}

// SSOStatusFor reports the SSO cookie state for a specific account, without
// exposing cookie values.
func SSOStatusFor(accountID string) SSOCookieStatus {
	status := SSOCookieStatus{}
	if store, err := loadSSOCookieStoreFor(accountID); err == nil {
		status.LoginAvailable = true
		status.LoginCookies = len(store.Cookies)
		status.LoginCaptured = store.CapturedAt
	}
	if store, err := loadM365CookieStoreFor(accountID); err == nil {
		status.M365Available = true
		status.M365Cookies = len(store.Cookies)
		status.M365Captured = store.ExtractedAt
	}
	return status
}

// SaveSSOCookieBatch splits cookies by domain and stores login cookies in the
// SSO store and M365 web-app cookies in the M365 store. It returns the count
// of cookies saved to each store.
func SaveSSOCookieBatch(cookies []SSOCookie) (loginCount, m365Count int, err error) {
	return SaveSSOCookieBatchFor("", cookies)
}

// SaveSSOCookieBatchFor splits cookies by domain and stores them for the given
// account, isolated from other accounts. An empty accountID writes the legacy
// global stores.
func SaveSSOCookieBatchFor(accountID string, cookies []SSOCookie) (loginCount, m365Count int, err error) {
	var loginCookies, m365Cookies []SSOCookie
	for _, c := range cookies {
		domain := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(c.Domain)), ".")
		switch domain {
		case "login.microsoftonline.com":
			loginCookies = append(loginCookies, c)
		case "m365.cloud.microsoft", "microsoft.com":
			m365Cookies = append(m365Cookies, c)
		}
	}
	if len(loginCookies) > 0 {
		if err := SaveSSOCookiesFor(accountID, loginCookies); err != nil {
			return 0, 0, err
		}
	}
	if len(m365Cookies) > 0 {
		if err := SaveM365CookiesFor(accountID, m365Cookies); err != nil {
			return 0, 0, err
		}
	}
	return len(loginCookies), len(m365Cookies), nil
}

// loadSSOCookieStore reads and decrypts the SSO cookie store from disk.
func loadSSOCookieStore() (*SSOCookieStore, error) {
	return loadSSOCookieStoreFor("")
}

// loadSSOCookieStoreFor reads and decrypts the SSO cookie store for a specific
// account. An empty accountID reads the legacy global store.
func loadSSOCookieStoreFor(accountID string) (*SSOCookieStore, error) {
	path := ssoCookiesFileFor(accountID)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("SSO cookies file not found: %w", err)
	}

	decrypted, err := crypto.Decrypt(string(data))
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt SSO cookies: %w", err)
	}

	var store SSOCookieStore
	if err := json.Unmarshal([]byte(decrypted), &store); err != nil {
		return nil, fmt.Errorf("failed to parse SSO cookies: %w", err)
	}

	return &store, nil
}

// loadSSOCookies reads and decrypts SSO cookies from disk.
func (tm *TokenManager) loadSSOCookies() (*SSOCookieStore, error) {
	return loadSSOCookieStore()
}

// LoadSSOCookiesFor returns the decrypted SSO cookie store for a specific
// account. Used by the account-pool SSO reauth hook.
func LoadSSOCookiesFor(accountID string) (*SSOCookieStore, error) {
	return loadSSOCookieStoreFor(accountID)
}

// hasSSOCookies checks if SSO cookies are available on disk.
func hasSSOCookies() bool {
	return hasSSOCookiesFor("")
}

// hasSSOCookiesFor checks if SSO cookies exist for the given account.
func hasSSOCookiesFor(accountID string) bool {
	_, err := os.Stat(ssoCookiesFileFor(accountID))
	return err == nil
}

func loadM365CookieStore() (*m365CookieStore, error) {
	return loadM365CookieStoreFor("")
}

func loadM365CookieStoreFor(accountID string) (*m365CookieStore, error) {
	data, err := os.ReadFile(m365CookiesFileFor(accountID))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrM365CookiesUnavailable, err)
	}

	decrypted, decryptErr := crypto.Decrypt(string(data))
	if decryptErr == nil {
		var store m365CookieStore
		if err := json.Unmarshal([]byte(decrypted), &store); err != nil {
			return nil, fmt.Errorf("%w: failed to parse decrypted M365 cookies: %v", ErrM365CookiesUnavailable, err)
		}
		return &store, nil
	}

	var legacyData struct {
		Domain  string      `json:"domain"`
		Cookies []SSOCookie `json:"cookies"`
	}
	if err := json.Unmarshal(data, &legacyData); err != nil {
		return nil, fmt.Errorf("%w: failed to decrypt M365 cookies: %v", ErrM365CookiesUnavailable, decryptErr)
	}
	if legacyData.Domain == "" || legacyData.Cookies == nil {
		return nil, fmt.Errorf("%w: invalid legacy M365 cookie store", ErrM365CookiesUnavailable)
	}
	legacyStore := m365CookieStore{
		Domain:      legacyData.Domain,
		ExtractedAt: time.Now(),
		Cookies:     legacyData.Cookies,
	}
	if err := saveM365CookieStoreFor(accountID, legacyStore); err != nil {
		return nil, fmt.Errorf("%w: failed to migrate M365 cookies: %v", ErrM365CookiesUnavailable, err)
	}
	return &legacyStore, nil
}

// M365CookieHeader returns cookies scoped to the M365 web application.
func (tm *TokenManager) M365CookieHeader() (string, error) {
	store, err := loadM365CookieStore()
	if err != nil {
		return "", err
	}

	var cookieParts []string
	for _, cookie := range store.Cookies {
		domain := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(cookie.Domain)), ".")
		if domain != "m365.cloud.microsoft" && domain != "microsoft.com" {
			continue
		}
		if cookie.Name == "" || cookie.Value == "" {
			continue
		}
		cookieParts = append(cookieParts, cookie.Name+"="+cookie.Value)
	}
	if len(cookieParts) == 0 {
		return "", ErrM365CookiesUnavailable
	}

	return strings.Join(cookieParts, "; "), nil
}

// ReauthWithSSOCookies performs silent re-authentication using the given SSO
// cookies. It uses the OAuth2 authorize endpoint with prompt=none and PKCE.
// If the SSO session is still valid, it returns new access and refresh tokens.
// It is the reusable core behind reauthWithSSO and the per-account account-pool
// fallback; it does not touch any TokenManager files.
func ReauthWithSSOCookies(tenant, clientID, scope string, cookies []SSOCookie) (accessToken, refreshToken string, expiresIn int, err error) {
	logging.Infof("reauthWithSSO: starting SSO cookie re-authentication for tenant %s", tenant)
	if len(cookies) == 0 {
		return "", "", 0, fmt.Errorf("%w: no SSO cookies available", ErrRefreshFailed)
	}

	logging.Debugf("reauthWithSSO: using %d SSO cookies", len(cookies))

	// Build Cookie header string from SSO cookies
	var cookieParts []string
	for _, c := range cookies {
		cookieParts = append(cookieParts, c.Name+"="+c.Value)
	}
	cookieHeader := strings.Join(cookieParts, "; ")

	client := &http.Client{
		// Don't follow redirects automatically; we need to capture the auth code
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 15 * time.Second,
	}

	// Generate PKCE
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return "", "", 0, fmt.Errorf("%w: %v", ErrRefreshFailed, err)
	}

	// Build authorize URL for silent auth using SSO cookies
	// sso_reload=True tells the server to use SSO cookies and skip the BssoInterrupt page.
	// prompt=none breaks SSO cookie recognition, so we omit it.
	authorizeURL := fmt.Sprintf(authorizeURLTemplate, tenant)
	params := url.Values{
		"client_id":             {clientID},
		"response_type":         {"code"},
		"redirect_uri":          {defaultRedirectURI},
		"scope":                 {scope + " offline_access"},
		"response_mode":         {"fragment"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {"m365bridge-sso"},
		"sso_reload":            {"True"},
	}

	authReq, err := http.NewRequest("GET", authorizeURL+"?"+params.Encode(), nil)
	if err != nil {
		return "", "", 0, fmt.Errorf("%w: failed to create authorize request: %v", ErrRefreshFailed, err)
	}
	authReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36")
	authReq.Header.Set("Referer", "https://m365.cloud.microsoft/")
	authReq.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	authReq.Header.Set("Cookie", cookieHeader)

	authResp, err := client.Do(authReq)
	if err != nil {
		return "", "", 0, fmt.Errorf("%w: authorize request failed: %v", ErrRefreshFailed, err)
	}
	defer authResp.Body.Close()

	// Follow redirects manually until we get the auth code or reach redirect_uri
	currentResp := authResp
	for {
		location := currentResp.Header.Get("Location")
		if location == "" {
			body, _ := io.ReadAll(currentResp.Body)
			bodyStr := string(body)
			// Check for meta refresh redirect in HTML
			if metaURL := extractMetaRefreshURL(bodyStr); metaURL != "" {
				location = metaURL
			} else {
				if len(bodyStr) > 2000 {
					bodyStr = bodyStr[:2000]
				}
				return "", "", 0, fmt.Errorf("%w: no redirect from authorize (status %d): %s", ErrRefreshFailed, currentResp.StatusCode, bodyStr)
			}
		}

		// Check if this is the redirect_uri with auth code
		if strings.Contains(location, "m365.cloud.microsoft") {
			// Parse auth code from redirect URL
			locURL, err := url.Parse(location)
			if err != nil {
				return "", "", 0, fmt.Errorf("%w: failed to parse redirect URL: %v", ErrRefreshFailed, err)
			}

			authCode := locURL.Query().Get("code")
			if authCode == "" {
				// Check for code in fragment (response_mode=fragment)
				fragment := locURL.Fragment
				if fragment != "" {
					fragParams, _ := url.ParseQuery(fragment)
					authCode = fragParams.Get("code")
					if authCode == "" {
						errCode := fragParams.Get("error")
						errDesc := fragParams.Get("error_description")
						return "", "", 0, fmt.Errorf("%w: authorize returned error: %s: %s", ErrRefreshFailed, errCode, errDesc)
					}
				}
			}
			if authCode != "" {
				// Exchange auth code for tokens
				logging.Info("reauthWithSSO: obtained auth code, exchanging for tokens")
				return exchangeAuthCodeTokens(clientID, scope, tenant, authCode, verifier)
			}
		}

		// Follow the redirect
		redirectReq, err := http.NewRequest("GET", location, nil)
		if err != nil {
			return "", "", 0, fmt.Errorf("%w: failed to create redirect request: %v", ErrRefreshFailed, err)
		}
		redirectReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36")
		redirectReq.Header.Set("Referer", "https://m365.cloud.microsoft/")
		redirectReq.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		redirectReq.Header.Set("Cookie", cookieHeader)

		currentResp.Body.Close()
		currentResp, err = client.Do(redirectReq)
		if err != nil {
			return "", "", 0, fmt.Errorf("%w: redirect request failed: %v", ErrRefreshFailed, err)
		}
		defer currentResp.Body.Close()
	}
}

// extractMetaRefreshURL parses an HTML body and extracts the URL from a
// <meta http-equiv="refresh" content="0; url=..."> tag. Returns empty string if not found.
func extractMetaRefreshURL(html string) string {
	// Find meta refresh tag
	idx := strings.Index(strings.ToLower(html), "http-equiv=\"refresh\"")
	if idx == -1 {
		idx = strings.Index(strings.ToLower(html), "http-equiv='refresh'")
	}
	if idx == -1 {
		return ""
	}

	// Find the content attribute after this position
	rest := html[idx:]
	contentIdx := strings.Index(strings.ToLower(rest), "content=\"")
	if contentIdx == -1 {
		contentIdx = strings.Index(strings.ToLower(rest), "content='")
	}
	if contentIdx == -1 {
		return ""
	}

	// Extract the content value
	rest = rest[contentIdx+9:]
	endIdx := strings.Index(rest, "\"")
	if endIdx == -1 {
		endIdx = strings.Index(rest, "'")
	}
	if endIdx == -1 {
		return ""
	}

	content := rest[:endIdx]
	// Parse "0; url=..." format
	urlIdx := strings.Index(strings.ToLower(content), "url=")
	if urlIdx == -1 {
		return ""
	}

	return strings.TrimSpace(content[urlIdx+4:])
}

func summarizeBrokerAuthorizeResponse(body string) string {
	const aadSTSMarker = "AADSTS"
	if start := strings.Index(body, aadSTSMarker); start >= 0 {
		details := body[start:]
		if end := strings.IndexAny(details, "<\r\n"); end >= 0 {
			details = details[:end]
		}
		if details = strings.TrimSpace(html.UnescapeString(details)); details != "" {
			return details
		}
	}

	lowerBody := strings.ToLower(body)
	if titleStart := strings.Index(lowerBody, "<title>"); titleStart >= 0 {
		contentStart := titleStart + len("<title>")
		if titleEnd := strings.Index(lowerBody[contentStart:], "</title>"); titleEnd >= 0 {
			title := strings.TrimSpace(html.UnescapeString(body[contentStart : contentStart+titleEnd]))
			if title != "" {
				return "page title: " + title
			}
		}
	}

	compactBody := strings.Join(strings.Fields(body), " ")
	if len(compactBody) > 300 {
		compactBody = compactBody[:300]
	}
	return compactBody
}

// exchangeAuthCodeTokens exchanges an authorization code for access and
// refresh tokens. It is the reusable token-exchange core used by both the SSO
// cookie reauth path and the broker flow; it does not persist anything.
func exchangeAuthCodeTokens(clientID, scope, tenant, authCode, verifier string) (accessToken, refreshToken string, expiresIn int, err error) {
	tokenData := url.Values{
		"client_id":     {clientID},
		"grant_type":    {"authorization_code"},
		"code":          {authCode},
		"redirect_uri":  {defaultRedirectURI},
		"code_verifier": {verifier},
		"scope":         {scope + " offline_access"},
	}

	tokenURL := fmt.Sprintf(tokenURLTemplate, tenant)
	tokenReq, err := http.NewRequest("POST", tokenURL, strings.NewReader(tokenData.Encode()))
	if err != nil {
		return "", "", 0, fmt.Errorf("%w: failed to create token request: %v", ErrRefreshFailed, err)
	}
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	tokenReq.Header.Set("Origin", "https://m365.cloud.microsoft")
	tokenReq.Header.Set("Referer", "https://m365.cloud.microsoft/")
	tokenReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36")
	tokenReq.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	tokenResp, err := client.Do(tokenReq)
	if err != nil {
		return "", "", 0, fmt.Errorf("%w: token exchange failed: %v", ErrRefreshFailed, err)
	}
	defer tokenResp.Body.Close()

	body, err := io.ReadAll(tokenResp.Body)
	if err != nil {
		return "", "", 0, fmt.Errorf("%w: failed to read token response: %v", ErrRefreshFailed, err)
	}

	if tokenResp.StatusCode != http.StatusOK {
		return "", "", 0, fmt.Errorf("%w: token exchange status %d: %s", ErrRefreshFailed, tokenResp.StatusCode, string(body))
	}

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", 0, fmt.Errorf("%w: failed to parse token response: %v", ErrRefreshFailed, err)
	}

	logging.Infof("exchangeAuthCode: success, expires_in=%d", result.ExpiresIn)
	return result.AccessToken, result.RefreshToken, result.ExpiresIn, nil
}

// reauthWithSSO performs silent re-authentication using stored SSO cookies and
// persists the resulting tokens. This is the TokenManager method used by the
// legacy single-account data plane; the account pool uses the standalone
// ReauthWithSSOCookies through its own hook instead.
func (tm *TokenManager) reauthWithSSO() (string, error) {
	store, err := loadSSOCookieStore()
	if err != nil {
		return "", fmt.Errorf("%w: no SSO cookies available: %v", ErrRefreshFailed, err)
	}
	accessToken, refreshToken, expiresIn, err := ReauthWithSSOCookies(tm.tenant, tm.clientID, tm.scope, store.Cookies)
	if err != nil {
		logging.Errorf("reauthWithSSO: failed: %v", err)
		return "", err
	}
	if refreshToken != "" {
		if werr := tm.writeRefreshToken(refreshToken); werr != nil {
			logging.Errorf("reauthWithSSO: failed to save refresh token: %v", werr)
		}
	}
	if expiresIn > 0 {
		expiresAt := time.Now().Add(time.Duration(expiresIn) * time.Second)
		if werr := tm.writeCache(TokenCache{AccessToken: accessToken, ExpiresAt: expiresAt.Unix()}); werr != nil {
			logging.Errorf("reauthWithSSO: failed to write cache: %v", werr)
		}
	}
	return accessToken, nil
}

// designerTokenCacheFile stores the designerapp access token cache.
const designerTokenCacheFile = "data/tokens/designer_token_cache.json"

// designerBrokerRefreshFile stores the broker-compatible refresh token.
// This refresh token is obtained via SSO cookie broker authorize flow and
// is separate from the standard refresh token (rt_90day.txt) because the
// broker flow requires a refresh token issued for brk-multihub:// redirect URI.
const designerBrokerRefreshFile = "data/tokens/rt_broker.txt"

// designerClientID is the M365 web app client_id used for designerapp tokens.
// This is the "brokered" app that the broker (c0ab8ce9) acquires tokens on
// behalf of.
const designerClientID = "4765445b-32c6-49b0-83e6-1d93765276ca"

// designerBrokerScope is the OAuth2 scope for the broker token request.
// The .default scope for the designerappservice resource is what MSAL.js
// uses when acquiring tokens for image downloads.
const designerBrokerScope = "https://designerappservice.officeapps.live.com/.default openid profile offline_access"

// designerBrokerRedirectURI is the redirect URI used in the broker token
// request body. MSAL.js uses the brk-multihub scheme for brokered flows.
const designerBrokerRedirectURI = "brk-multihub://outlook.office.com"

// brokerClientID is the broker app client_id used in broker token requests.
// This is always c0ab8ce9 (the M365 Copilot broker app), regardless of the
// configured M365_CLIENT_ID, because the broker flow requires the broker
// app's client_id to acquire tokens on behalf of the brokered app (4765445b).
const brokerClientID = "c0ab8ce9-e9a0-42e7-b064-33d422df41f1"

// designerTokenCache is the on-disk cache for the designerapp access token.
type designerTokenCache struct {
	AccessToken string `json:"access_token"`
	ExpiresAt   int64  `json:"expires_at"`
}

// designerOAuthError represents an OAuth error returned by the broker token endpoint.
type designerOAuthError struct {
	Status      int
	Code        string
	Description string
}

// Error returns the broker OAuth error details.
func (e *designerOAuthError) Error() string {
	return fmt.Sprintf("designer broker token status %d: %s: %s", e.Status, e.Code, e.Description)
}

// isExpiredRefreshToken reports whether the broker refresh token must be replaced.
func (e *designerOAuthError) isExpiredRefreshToken() bool {
	return e.Code == "invalid_grant" && strings.Contains(e.Description, "AADSTS700084")
}

// GetDesignerToken returns a valid designerapp access token, acquiring a new
// one via the broker refresh token flow if the cached token is expired or
// missing. Falls back to SSO cookie broker authorize flow if no broker
// refresh token is available.
func (tm *TokenManager) GetDesignerToken() (string, error) {
	// Check cache first
	data, err := os.ReadFile(designerTokenCacheFile)
	if err == nil {
		var cache designerTokenCache
		if json.Unmarshal(data, &cache) == nil {
			if time.Now().Unix() < cache.ExpiresAt-60 {
				logging.Debug("GetDesignerToken: cache hit")
				return cache.AccessToken, nil
			}
		}
	}

	logging.Info("GetDesignerToken: cache miss, acquiring new token")
	// Acquire new token via broker refresh token flow
	token, expiresIn, err := tm.acquireDesignerToken()
	if err != nil {
		logging.Errorf("GetDesignerToken: failed to acquire token: %v", err)
		return "", err
	}

	// Cache it
	cache := designerTokenCache{
		AccessToken: token,
		ExpiresAt:   time.Now().Add(time.Duration(expiresIn) * time.Second).Unix(),
	}
	cacheData, _ := json.Marshal(cache)
	os.WriteFile(designerTokenCacheFile, cacheData, 0600)

	logging.Infof("GetDesignerToken: success, expires_in=%d", expiresIn)
	return token, nil
}

// acquireDesignerToken performs a broker refresh token request to obtain a
// JWE access token for designerapp.officeapps.live.com image downloads.
// Uses a broker-compatible refresh token (stored in rt_broker.txt). If none
// exists, falls back to SSO cookie broker authorize flow to obtain one.
func (tm *TokenManager) acquireDesignerToken() (string, int, error) {
	requestToken := tm.requestDesignerToken
	if tm.designerTokenRequest != nil {
		requestToken = tm.designerTokenRequest
	}
	acquireBrokerToken := tm.acquireBrokerRefreshTokenViaSSO
	if tm.brokerTokenAcquisition != nil {
		acquireBrokerToken = tm.brokerTokenAcquisition
	}

	refreshToken, err := tm.readBrokerRefreshToken()
	if err != nil {
		logging.Info("acquireDesignerToken: no broker refresh token, acquiring via SSO cookies")
		refreshToken, err = acquireBrokerToken()
		if err != nil {
			logging.Errorf("acquireDesignerToken: failed to acquire broker refresh token: %v", err)
			return "", 0, fmt.Errorf("failed to acquire broker refresh token: %w", err)
		}
		return requestToken(refreshToken)
	}

	logging.Debug("acquireDesignerToken: using existing broker refresh token")
	token, expiresIn, err := requestToken(refreshToken)
	var oauthErr *designerOAuthError
	if !errors.As(err, &oauthErr) || !oauthErr.isExpiredRefreshToken() {
		return token, expiresIn, err
	}

	logging.Warn("acquireDesignerToken: broker refresh token expired, acquiring a new token via SSO cookies")
	refreshToken, err = acquireBrokerToken()
	if err != nil {
		return "", 0, fmt.Errorf("failed to reacquire expired broker refresh token: %w", err)
	}

	return requestToken(refreshToken)
}

// requestDesignerToken exchanges a broker refresh token for a designer access token.
func (tm *TokenManager) requestDesignerToken(refreshToken string) (string, int, error) {
	// Build the broker token URL with query parameters
	tokenURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token?brk_client_id=%s&brk_redirect_uri=%s&client_id=%s&client-request-id=%s",
		tm.tenant,
		designerClientID,
		url.QueryEscape(defaultRedirectURI),
		brokerClientID,
		generateClientRequestID(),
	)

	// Build the request body matching MSAL.js broker flow
	body := url.Values{
		"client_id":                  {brokerClientID},
		"redirect_uri":               {designerBrokerRedirectURI},
		"scope":                      {designerBrokerScope},
		"grant_type":                 {"refresh_token"},
		"client_info":                {"1"},
		"x-client-SKU":               {"msal.js.browser"},
		"x-client-VER":               {"5.9.0"},
		"x-ms-lib-capability":        {"retry-after, h429"},
		"x-client-current-telemetry": {"5|61,0,,,|,"},
		"x-client-last-telemetry":    {"5|0|||0,0"},
		"refresh_token":              {refreshToken},
	}

	// X-AnchorMailbox helps AAD route the request to the correct token service
	if tm.userOID != "" {
		body.Set("X-AnchorMailbox", fmt.Sprintf("Oid:%s@%s", tm.userOID, tm.tenant))
	}

	// brk_ params go in both URL query and body (MSAL.js sends them in both)
	body.Set("brk_client_id", designerClientID)
	body.Set("brk_redirect_uri", defaultRedirectURI)

	bodyEncoded := body.Encode()

	req, err := http.NewRequest("POST", tokenURL, strings.NewReader(bodyEncoded))
	if err != nil {
		return "", 0, fmt.Errorf("failed to create designer broker token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	req.Header.Set("Origin", "https://m365.cloud.microsoft")
	req.Header.Set("Referer", "https://m365.cloud.microsoft/")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("designer broker token request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, fmt.Errorf("failed to read designer broker token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var oauthResult struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		if err := json.Unmarshal(respBody, &oauthResult); err == nil && oauthResult.Error != "" {
			return "", 0, &designerOAuthError{
				Status:      resp.StatusCode,
				Code:        oauthResult.Error,
				Description: oauthResult.ErrorDescription,
			}
		}
		return "", 0, fmt.Errorf("designer broker token status %d: %s", resp.StatusCode, string(respBody)[:min(300, len(respBody))])
	}

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", 0, fmt.Errorf("failed to parse designer broker token response: %w", err)
	}

	// Save rotated broker refresh token if returned
	if result.RefreshToken != "" {
		if err := tm.writeBrokerRefreshToken(result.RefreshToken); err != nil {
			return "", 0, fmt.Errorf("failed to save rotated broker refresh token: %w", err)
		}
	}

	return result.AccessToken, result.ExpiresIn, nil
}

// acquireBrokerRefreshTokenViaSSO performs a broker authorize flow using SSO
// cookies to obtain a broker-compatible refresh token. This is needed because
// the standard refresh token (issued for spalanding redirect URI) is not
// compatible with the broker flow (which requires brk-multihub:// redirect URI).
func (tm *TokenManager) acquireBrokerRefreshTokenViaSSO() (string, error) {
	logging.Info("acquireBrokerRefreshTokenViaSSO: starting broker authorize flow via SSO cookies")
	store, err := tm.loadSSOCookies()
	if err != nil {
		logging.Errorf("acquireBrokerRefreshTokenViaSSO: no SSO cookies: %v", err)
		return "", fmt.Errorf("no SSO cookies for broker authorize: %w", err)
	}

	var cookieParts []string
	for _, c := range store.Cookies {
		cookieParts = append(cookieParts, c.Name+"="+c.Value)
	}
	cookieHeader := strings.Join(cookieParts, "; ")

	verifier, challenge, err := generatePKCE()
	if err != nil {
		return "", fmt.Errorf("PKCE failed: %w", err)
	}

	// Broker authorize URL with PKCE and brk_ params
	params := url.Values{
		"client_id":             {brokerClientID},
		"response_type":         {"code"},
		"redirect_uri":          {designerBrokerRedirectURI},
		"scope":                 {designerBrokerScope},
		"response_mode":         {"fragment"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"brk_client_id":         {designerClientID},
		"brk_redirect_uri":      {defaultRedirectURI},
		"sso_reload":            {"True"},
	}

	authorizeURL := fmt.Sprintf(authorizeURLTemplate, tm.tenant)
	authReq, err := http.NewRequest("GET", authorizeURL+"?"+params.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("failed to create broker authorize request: %w", err)
	}
	authReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36")
	authReq.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	authReq.Header.Set("Cookie", cookieHeader)

	httpClient := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 15 * time.Second,
	}

	currentResp, err := httpClient.Do(authReq)
	if err != nil && !strings.Contains(err.Error(), "ErrUseLastResponse") {
		return "", fmt.Errorf("broker authorize request failed: %w", err)
	}
	defer currentResp.Body.Close()

	// Follow redirects manually until we get the auth code or reach brk_redirect_uri.
	// Microsoft AAD may return intermediate redirects (e.g. /jsdisabled, /kmsi)
	// before the final redirect to spalanding#code=..., especially in headless/Docker
	// environments where JS is not available.
	const maxRedirects = 10
	for i := range maxRedirects {
		location := currentResp.Header.Get("Location")
		if location == "" {
			body, _ := io.ReadAll(currentResp.Body)
			bodyStr := string(body)
			// Check for meta refresh redirect in HTML (AAD sometimes uses this)
			if metaURL := extractMetaRefreshURL(bodyStr); metaURL != "" {
				location = metaURL
			} else {
				return "", fmt.Errorf("no redirect from broker authorize (status %d, hop %d): %s", currentResp.StatusCode, i, summarizeBrokerAuthorizeResponse(bodyStr))
			}
		}

		logging.Debugf("acquireBrokerRefreshTokenViaSSO: redirect hop %d -> %s", i, location[:min(120, len(location))])

		// Check if this is the final redirect with auth code (brk_redirect_uri = spalanding)
		if strings.Contains(location, "m365.cloud.microsoft") {
			locURL, err := url.Parse(location)
			if err != nil {
				return "", fmt.Errorf("failed to parse broker redirect URL: %w", err)
			}

			authCode := locURL.Query().Get("code")
			if authCode == "" {
				// Check for code in fragment (response_mode=fragment)
				fragment := locURL.Fragment
				if fragment != "" {
					fragParams, _ := url.ParseQuery(fragment)
					authCode = fragParams.Get("code")
					if authCode == "" {
						errCode := fragParams.Get("error")
						errDesc := fragParams.Get("error_description")
						return "", fmt.Errorf("broker authorize error: %s: %s", errCode, errDesc)
					}
				}
			}
			if authCode != "" {
				logging.Info("acquireBrokerRefreshTokenViaSSO: obtained auth code, exchanging for broker tokens")
				return tm.exchangeBrokerAuthCode(authCode, verifier)
			}
		}

		// Follow the redirect, forwarding SSO cookies
		redirectReq, err := http.NewRequest("GET", location, nil)
		if err != nil {
			return "", fmt.Errorf("failed to create broker redirect request (hop %d): %w", i, err)
		}
		redirectReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36")
		redirectReq.Header.Set("Referer", "https://login.microsoftonline.com/")
		redirectReq.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		redirectReq.Header.Set("Cookie", cookieHeader)

		currentResp.Body.Close()
		currentResp, err = httpClient.Do(redirectReq)
		if err != nil && !strings.Contains(err.Error(), "ErrUseLastResponse") {
			return "", fmt.Errorf("broker redirect request failed (hop %d): %w", i, err)
		}
		defer currentResp.Body.Close()
	}

	return "", fmt.Errorf("broker authorize: max redirects (%d) reached without obtaining auth code", maxRedirects)
}

// exchangeBrokerAuthCode exchanges an authorization code for a broker
// refresh token and designerapp access token.
func (tm *TokenManager) exchangeBrokerAuthCode(authCode, verifier string) (string, error) {
	tokenURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token?brk_client_id=%s&brk_redirect_uri=%s&client_id=%s&client-request-id=%s",
		tm.tenant,
		designerClientID,
		url.QueryEscape(defaultRedirectURI),
		brokerClientID,
		generateClientRequestID(),
	)

	body := url.Values{
		"client_id":        {brokerClientID},
		"redirect_uri":     {designerBrokerRedirectURI},
		"scope":            {designerBrokerScope},
		"grant_type":       {"authorization_code"},
		"code":             {authCode},
		"code_verifier":    {verifier},
		"client_info":      {"1"},
		"brk_client_id":    {designerClientID},
		"brk_redirect_uri": {defaultRedirectURI},
	}

	req, err := http.NewRequest("POST", tokenURL, strings.NewReader(body.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create broker code exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	req.Header.Set("Origin", "https://m365.cloud.microsoft")
	req.Header.Set("Referer", "https://m365.cloud.microsoft/")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("broker code exchange failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read broker code exchange response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("broker code exchange status %d: %s", resp.StatusCode, string(respBody)[:min(300, len(respBody))])
	}

	var result struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("failed to parse broker code exchange response: %w", err)
	}

	if result.RefreshToken == "" {
		return "", fmt.Errorf("no refresh token in broker code exchange response")
	}

	// Save broker refresh token
	if err := tm.writeBrokerRefreshToken(result.RefreshToken); err != nil {
		return "", fmt.Errorf("failed to save broker refresh token: %w", err)
	}

	logging.Info("exchangeBrokerAuthCode: success, broker refresh token saved")
	return result.RefreshToken, nil
}

// readBrokerRefreshToken reads and decrypts the broker refresh token from file.
func (tm *TokenManager) readBrokerRefreshToken() (string, error) {
	data, err := os.ReadFile(designerBrokerRefreshFile)
	if err != nil {
		return "", fmt.Errorf("broker refresh token not found: %w", err)
	}

	encrypted := string(data)
	if encrypted == "" {
		return "", fmt.Errorf("broker refresh token file is empty")
	}

	decrypted, err := crypto.Decrypt(encrypted)
	if err != nil {
		return encrypted, nil
	}

	return decrypted, nil
}

// writeBrokerRefreshToken encrypts and writes the broker refresh token to file.
func (tm *TokenManager) writeBrokerRefreshToken(token string) error {
	encrypted, err := crypto.Encrypt(token)
	if err != nil {
		return fmt.Errorf("failed to encrypt broker refresh token: %w", err)
	}

	dir := filepath.Dir(designerBrokerRefreshFile)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create directory for broker refresh token: %w", err)
	}

	return os.WriteFile(designerBrokerRefreshFile, []byte(encrypted), 0600)
}

// generateClientRequestID generates a UUID for the client-request-id parameter.
func generateClientRequestID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
