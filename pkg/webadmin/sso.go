package webadmin

import (
	"encoding/json"
	"net/http"

	"github.com/ryc2077/m365plus/pkg/auth"
)

// ssoCookies handles GET /api/sso (status) and POST /api/sso (save).
// Saving splits cookies by domain: login.microsoftonline.com cookies go to the
// SSO store, m365.cloud.microsoft / microsoft.com cookies to the M365 store.
// Cookie values are encrypted at rest and never returned by the status view.
func (s *Server) ssoCookies(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonOut(w, map[string]any{"sso": auth.SSOStatus()})
	case http.MethodPost:
		s.saveSSOCookies(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// saveSSOCookies parses {cookies:[...]} and stores them by domain.
func (s *Server) saveSSOCookies(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Cookies []auth.SSOCookie `json:"cookies"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if len(body.Cookies) == 0 {
		http.Error(w, "no cookies provided", http.StatusBadRequest)
		return
	}
	loginCount, m365Count, err := auth.SaveSSOCookieBatch(body.Cookies)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "cookie_save_error", err.Error())
		return
	}
	jsonOut(w, map[string]any{
		"status":       "saved",
		"loginCookies": loginCount,
		"m365Cookies":  m365Count,
		"sso":          auth.SSOStatus(),
	})
}
