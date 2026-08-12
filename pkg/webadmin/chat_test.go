package webadmin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryc2077/m365plus/pkg/accounts"
)

// seedStore writes a cache file with one healthy account and returns a store.
func seedStore(t *testing.T, dir string) *accounts.Store {
	t.Helper()
	path := filepath.Join(dir, "accounts.json")
	os.MkdirAll(dir, 0o700)
	acc := accounts.AccountToken{
		ID:           "oid-test-1",
		OID:          "oid-test-1",
		TID:          "tid-test-1",
		Email:        "user@example.com",
		AccessToken:  "fake-access-token",
		RefreshToken: "fake-refresh-token",
		ExpiresAt:    time.Now().Add(30 * time.Minute),
	}
	data, _ := json.Marshal(accounts.Cache{Accounts: []accounts.AccountToken{acc}})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := accounts.OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestAdminChatStreams(t *testing.T) {
	dir := t.TempDir()
	s := &Server{tokens: seedStore(t, dir), accountPool: newAccountHealth()}
	s.settings = openSettingsStore()
	s.SetChatFunc(func(ctx context.Context, acc accounts.AccountToken, model string, messages []ChatMessage) (<-chan ChatChunk, error) {
		ch := make(chan ChatChunk)
		go func() {
			defer close(ch)
			ch <- ChatChunk{Thinking: "thinking…"}
			ch <- ChatChunk{Text: "Hel"}
			ch <- ChatChunk{Text: "lo"}
		}()
		return ch, nil
	})

	body := `{"model":"gpt-4-quick","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/chat", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.adminChat(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type=%s", ct)
	}
	out := w.Body.String()
	if !strings.Contains(out, `"Thinking":"thinking…"`) && !strings.Contains(out, `"thinking":"thinking…"`) {
		t.Fatalf("missing thinking chunk: %s", out)
	}
	if !strings.Contains(out, `"Text":"Hel"`) && !strings.Contains(out, `"text":"Hel"`) {
		t.Fatalf("missing text chunk: %s", out)
	}
	if !strings.Contains(out, `"Done":true`) && !strings.Contains(out, `"done":true`) {
		t.Fatalf("missing done marker: %s", out)
	}
}

func TestAdminChatRequiresModelAndMessages(t *testing.T) {
	dir := t.TempDir()
	s := &Server{tokens: seedStore(t, dir), accountPool: newAccountHealth()}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/chat", bytes.NewBufferString(`{"model":""}`))
	w := httptest.NewRecorder()
	s.adminChat(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestAdminChatHookError(t *testing.T) {
	dir := t.TempDir()
	s := &Server{tokens: seedStore(t, dir), accountPool: newAccountHealth()}
	s.settings = openSettingsStore()
	s.SetChatFunc(func(ctx context.Context, acc accounts.AccountToken, model string, messages []ChatMessage) (<-chan ChatChunk, error) {
		ch := make(chan ChatChunk)
		go func() {
			defer close(ch)
			ch <- ChatChunk{Error: "upstream exploded"}
		}()
		return ch, nil
	})
	body := `{"model":"gpt-4-quick","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/chat", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	s.adminChat(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("hook error should stream then finish, status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "upstream exploded") {
		t.Fatalf("missing error chunk: %s", w.Body.String())
	}
}
