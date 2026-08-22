package webadmin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ryc2077/m365plus/pkg/accounts"
	"github.com/ryc2077/m365plus/pkg/auth"
)

const accountBackupVersion = 1

type accountCookieBackup struct {
	AccountID string           `json:"accountId"`
	Login     []auth.SSOCookie `json:"loginCookies,omitempty"`
	M365      []auth.SSOCookie `json:"m365Cookies,omitempty"`
	LoginAt   time.Time        `json:"loginCapturedAt,omitempty"`
	M365At    time.Time        `json:"m365CapturedAt,omitempty"`
}

type accountBackup struct {
	Version    int                     `json:"version"`
	ExportedAt time.Time               `json:"exportedAt"`
	Accounts   []accounts.AccountToken `json:"accounts"`
	Cookies    []accountCookieBackup   `json:"cookies,omitempty"`
}

func (s *Server) accountBackupExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	backup := accountBackup{Version: accountBackupVersion, ExportedAt: time.Now(), Accounts: s.tokens.List()}
	for _, account := range backup.Accounts {
		cookies := accountCookieBackup{AccountID: account.ID}
		if login, err := auth.LoadSSOCookiesFor(account.ID); err == nil {
			cookies.Login = append([]auth.SSOCookie(nil), login.Cookies...)
			cookies.LoginAt = login.CapturedAt
		}
		if m365, capturedAt, err := auth.LoadM365CookiesFor(account.ID); err == nil {
			cookies.M365 = m365
			cookies.M365At = capturedAt
		}
		if len(cookies.Login) > 0 || len(cookies.M365) > 0 {
			backup.Cookies = append(backup.Cookies, cookies)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="m365plus-accounts-%s.json"`, time.Now().Format("20060102-150405")))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(backup)
}

func (s *Server) accountBackupImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var backup accountBackup
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&backup); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_backup", "invalid account backup: "+err.Error())
		return
	}
	if backup.Version != accountBackupVersion {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_backup", "unsupported account backup version")
		return
	}
	if len(backup.Accounts) == 0 {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_backup", "backup contains no accounts")
		return
	}
	for i := range backup.Accounts {
		account := &backup.Accounts[i]
		account.ID = strings.TrimSpace(account.ID)
		account.OID = strings.TrimSpace(account.OID)
		account.Email = strings.TrimSpace(account.Email)
		if account.ID == "" {
			account.ID = account.OID
		}
		if account.OID == "" {
			account.OID = account.ID
		}
		if account.ID == "" || account.Email == "" || (account.RefreshToken == "" && account.AccessToken == "") {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_backup", fmt.Sprintf("account %d is missing identity or token data", i+1))
			return
		}
	}
	added, updated, err := s.tokens.Import(backup.Accounts)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "account_import_error", err.Error())
		return
	}
	cookieAccounts := 0
	for _, cookieBackup := range backup.Cookies {
		accountID := strings.TrimSpace(cookieBackup.AccountID)
		if accountID == "" {
			continue
		}
		written := false
		if len(cookieBackup.Login) > 0 {
			if err := auth.SaveSSOCookiesFor(accountID, cookieBackup.Login); err != nil {
				writeOpenAIError(w, http.StatusInternalServerError, "account_import_error", err.Error())
				return
			}
			written = true
		}
		if len(cookieBackup.M365) > 0 {
			if err := auth.SaveM365CookiesFor(accountID, cookieBackup.M365); err != nil {
				writeOpenAIError(w, http.StatusInternalServerError, "account_import_error", err.Error())
				return
			}
			written = true
		}
		if written {
			cookieAccounts++
		}
	}
	jsonOut(w, map[string]any{"status": "imported", "added": added, "updated": updated, "cookieAccounts": cookieAccounts})
}
