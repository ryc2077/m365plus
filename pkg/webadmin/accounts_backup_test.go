package webadmin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryc2077/m365plus/pkg/accounts"
	"github.com/ryc2077/m365plus/pkg/auth"
)

func newBackupTestServer(t *testing.T) *Server {
	t.Helper()
	dataDir := t.TempDir()
	t.Setenv("M365_DATA_DIR", dataDir)
	store, err := accounts.OpenStore(filepath.Join(dataDir, "accounts.json"))
	if err != nil {
		t.Fatal(err)
	}
	return &Server{tokens: store, accountPool: newAccountHealth()}
}

func TestAccountBackupExportAndImport(t *testing.T) {
	source := newBackupTestServer(t)
	account := accounts.AccountToken{ID: "oid-1", OID: "oid-1", TID: "tenant-1", Email: "user@example.com", Status: "online", AccessToken: "access-secret", RefreshToken: "refresh-secret", ExpiresAt: time.Now().Add(time.Hour), UpdatedAt: time.Now()}
	if added, _, err := source.tokens.Import([]accounts.AccountToken{account}); err != nil || added != 1 {
		t.Fatalf("seed account: added=%d err=%v", added, err)
	}
	if err := auth.SaveSSOCookiesFor(account.ID, []auth.SSOCookie{{Name: "ESTSAUTH", Value: "login-secret", Domain: "login.microsoftonline.com"}}); err != nil {
		t.Fatal(err)
	}
	if err := auth.SaveM365CookiesFor(account.ID, []auth.SSOCookie{{Name: "M365Session", Value: "m365-secret", Domain: "m365.cloud.microsoft"}}); err != nil {
		t.Fatal(err)
	}

	exportRecorder := httptest.NewRecorder()
	source.accountBackupExport(exportRecorder, httptest.NewRequest(http.MethodGet, "/api/accounts/backup/export", nil))
	if exportRecorder.Code != http.StatusOK {
		t.Fatalf("export status=%d body=%s", exportRecorder.Code, exportRecorder.Body.String())
	}
	if !strings.Contains(exportRecorder.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("missing attachment header: %s", exportRecorder.Header().Get("Content-Disposition"))
	}

	target := newBackupTestServer(t)
	importRecorder := httptest.NewRecorder()
	target.accountBackupImport(importRecorder, httptest.NewRequest(http.MethodPost, "/api/accounts/backup/import", bytes.NewReader(exportRecorder.Body.Bytes())))
	if importRecorder.Code != http.StatusOK {
		t.Fatalf("import status=%d body=%s", importRecorder.Code, importRecorder.Body.String())
	}
	restored, ok := target.tokens.Get(account.ID)
	if !ok || restored.RefreshToken != account.RefreshToken || restored.AccessToken != account.AccessToken {
		t.Fatalf("restored account mismatch: %+v", restored)
	}
	login, err := auth.LoadSSOCookiesFor(account.ID)
	if err != nil || len(login.Cookies) != 1 || login.Cookies[0].Value != "login-secret" {
		t.Fatalf("restored login cookies: store=%+v err=%v", login, err)
	}
	m365, _, err := auth.LoadM365CookiesFor(account.ID)
	if err != nil || len(m365) != 1 || m365[0].Value != "m365-secret" {
		t.Fatalf("restored M365 cookies: cookies=%+v err=%v", m365, err)
	}
}

func TestAccountBackupImportRejectsInvalidVersion(t *testing.T) {
	server := newBackupTestServer(t)
	body, _ := json.Marshal(accountBackup{Version: 99, Accounts: []accounts.AccountToken{{ID: "id", Email: "user@example.com", RefreshToken: "token"}}})
	recorder := httptest.NewRecorder()
	server.accountBackupImport(recorder, httptest.NewRequest(http.MethodPost, "/api/accounts/backup/import", bytes.NewReader(body)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
