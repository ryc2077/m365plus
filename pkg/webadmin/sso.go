package webadmin

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/ryc2077/m365plus/pkg/accounts"
	"github.com/ryc2077/m365plus/pkg/auth"
)

// ssoCookies handles GET /api/sso (status) and POST /api/sso (save).
// Both endpoints accept an optional accountId to scope the operation to a
// single account; when omitted they operate on the legacy global store.
// Saving splits cookies by domain: login.microsoftonline.com cookies go to the
// SSO store, m365.cloud.microsoft / microsoft.com cookies to the M365 store.
// Cookie values are encrypted at rest and never returned by the status view.
func (s *Server) ssoCookies(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		accountID := r.URL.Query().Get("accountId")
		jsonOut(w, map[string]any{"sso": auth.SSOStatusFor(accountID)})
	case http.MethodPost:
		s.saveSSOCookies(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// saveSSOCookies parses {accountId?, cookies:[...]} and stores them by domain
// for the requested account (or the global store when no accountId is given).
func (s *Server) saveSSOCookies(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AccountID string           `json:"accountId"`
		Cookies   []auth.SSOCookie `json:"cookies"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if len(body.Cookies) == 0 {
		http.Error(w, "no cookies provided", http.StatusBadRequest)
		return
	}
	loginCount, m365Count, err := auth.SaveSSOCookieBatchFor(body.AccountID, body.Cookies)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "cookie_save_error", err.Error())
		return
	}
	jsonOut(w, map[string]any{
		"status":       "saved",
		"accountId":    body.AccountID,
		"loginCookies": loginCount,
		"m365Cookies":  m365Count,
		"sso":          auth.SSOStatusFor(body.AccountID),
	})
}

// ssoReauthForAccount is the account-pool SSO re-authentication hook. When the
// plain-HTTP refresh exchange for an account fails (dead RT chain), it loads
// that account's own SSO cookies and performs a silent cookie re-auth, minting
// a fresh access/refresh token pair for the account. The returned TokenSet is
// persisted back to the account store by the accounts package.
func ssoReauthForAccount(acc accounts.AccountToken) (accounts.TokenSet, error) {
	store, err := auth.LoadSSOCookiesFor(acc.ID)
	if err != nil {
		return accounts.TokenSet{}, err
	}
	clientID := acc.ClientID
	if clientID == "" {
		clientID = accounts.ClientID()
	}
	tid := acc.TID
	if tid == "" {
		return accounts.TokenSet{}, fmtErr("account %s has no tenant id, cannot SSO re-auth", acc.Email)
	}
	accessToken, refreshToken, expiresIn, err := auth.ReauthWithSSOCookies(tid, clientID, accounts.Scope(), store.Cookies)
	if err != nil {
		return accounts.TokenSet{}, err
	}
	set := accounts.TokenSet{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second),
		Email:        acc.Email,
		DisplayName:  acc.DisplayName,
		HomeOID:      firstNonEmpty(acc.OID, acc.ID),
		TenantID:     tid,
	}
	return set, nil
}
