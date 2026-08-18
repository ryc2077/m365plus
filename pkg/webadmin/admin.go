package webadmin

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ryc2077/m365plus/pkg/accounts"
	"github.com/ryc2077/m365plus/pkg/auth"
)

// ModelTester is the hook the data plane registers so the admin console can
// probe a model without depending on the chat implementation.
type ModelTester func(ctx context.Context, acc accounts.AccountToken, model string) (string, error)

// ChatMessage is a single turn in the admin console chat page.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatChunk is one streaming unit emitted by the admin console chat hook.
type ChatChunk struct {
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`
	Done     bool   `json:"done,omitempty"`
	Error    string `json:"error,omitempty"`
}

// ChatFunc is the hook the data plane registers so the admin console can run a
// streaming multi-turn conversation without depending on the chat internals.
type ChatFunc func(ctx context.Context, acc accounts.AccountToken, model string, messages []ChatMessage) (<-chan ChatChunk, error)

func (s *Server) adminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	ip, now := clientIP(r), time.Now()
	if ok, wait := s.loginAllowed(ip, now); !ok {
		seconds := int(wait.Seconds()) + 1
		w.Header().Set("Retry-After", fmt.Sprint(seconds))
		writeOpenAIError(w, http.StatusTooManyRequests, "rate_limit_error", "too many failed login attempts; try again later")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	decodeErr := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body)
	s.mu.Lock()
	password := s.adminPassword
	mustChange := s.mustChangePassword
	s.mu.Unlock()
	if decodeErr != nil || body.Password == "" || subtle.ConstantTimeCompare([]byte(body.Password), []byte(password)) != 1 {
		s.recordLoginFailure(ip, now)
		writeOpenAIError(w, http.StatusUnauthorized, "auth_error", "invalid administrator password")
		return
	}
	s.clearLoginFailures(ip)
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		writeOpenAIError(w, 500, "internal_error", "session failure")
		return
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	s.mu.Lock()
	pruneAdminSessions(s.adminSessions, now)
	if len(s.adminSessions) >= maxAdminSessions {
		var oldest string
		var oldestExp time.Time
		for k, exp := range s.adminSessions {
			if oldest == "" || exp.Before(oldestExp) {
				oldest, oldestExp = k, exp
			}
		}
		delete(s.adminSessions, oldest)
	}
	s.adminSessions[token] = now.Add(24 * time.Hour)
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "m365_admin_session", Value: token, Path: "/", HttpOnly: true, Secure: secureAdminCookie(r), SameSite: http.SameSiteLaxMode, MaxAge: 86400})
	jsonOut(w, map[string]any{"status": "authenticated", "must_change_password": mustChange})
}

func (s *Server) adminLogout(w http.ResponseWriter, r *http.Request) {
	if c, e := r.Cookie("m365_admin_session"); e == nil {
		s.mu.Lock()
		delete(s.adminSessions, c.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "m365_admin_session", Path: "/", HttpOnly: true, Secure: secureAdminCookie(r), SameSite: http.SameSiteLaxMode, MaxAge: -1})
	jsonOut(w, map[string]string{"status": "logged_out"})
}

func (s *Server) adminSession(w http.ResponseWriter, r *http.Request) {
	authenticated := s.validAdminSession(r)
	s.mu.Lock()
	mustChange := s.mustChangePassword
	s.mu.Unlock()
	jsonOut(w, map[string]bool{"authenticated": authenticated, "must_change_password": authenticated && mustChange})
}

func (s *Server) adminKeys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonOut(w, map[string]any{"keys": s.apiKeys.list()})
	case http.MethodPost:
		var b struct {
			Name string `json:"name"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		if strings.TrimSpace(b.Name) == "" {
			b.Name = "API key"
		}
		rec, raw, e := s.apiKeys.create(b.Name)
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		jsonOut(w, map[string]any{"key": raw, "record": rec})
	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		deleted, e := s.apiKeys.delete(id)
		if e != nil {
			http.Error(w, e.Error(), http.StatusInternalServerError)
			return
		}
		if !deleted {
			http.Error(w, "key not found", 404)
			return
		}
		jsonOut(w, map[string]string{"status": "deleted"})
	case http.MethodPut:
		var b struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Revoked *bool  `json:"revoked"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil || b.ID == "" {
			http.Error(w, "bad json", 400)
			return
		}
		updated, e := s.apiKeys.update(b.ID, b.Name, b.Revoked)
		if e != nil {
			http.Error(w, e.Error(), http.StatusInternalServerError)
			return
		}
		if !updated {
			http.Error(w, "key not found", 404)
			return
		}
		jsonOut(w, map[string]string{"status": "updated"})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (s *Server) accounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	list := s.tokens.List()
	autoRotate := s.settings != nil && s.settings.get().AutoRotateAccounts
	s.rotationMu.Lock()
	activeID := s.rotationID
	s.rotationMu.Unlock()
	type view struct {
		ID          string    `json:"id"`
		Email       string    `json:"email"`
		DisplayName string    `json:"displayName,omitempty"`
		Status      string    `json:"status"`
		OID         string    `json:"oid,omitempty"`
		TID         string    `json:"tid,omitempty"`
		ExpiresAt   time.Time `json:"expiresAt"`
		UpdatedAt   time.Time `json:"updatedAt"`
		Active      bool      `json:"active"`
		SSO         ssoView   `json:"sso"`
	}
	out := make([]view, 0, len(list))
	for _, a := range list {
		out = append(out, view{
			ID: a.ID, Email: a.Email, DisplayName: a.DisplayName,
			Status: a.Status, OID: a.OID, TID: a.TID,
			ExpiresAt: a.ExpiresAt, UpdatedAt: a.UpdatedAt,
			Active: autoRotate && a.ID == activeID,
			SSO:    ssoStatusFor(a.ID),
		})
	}
	jsonOut(w, map[string]any{"accounts": out})
}

// ssoView is the per-account SSO cookie state shown in the account list. Cookie
// values are never exposed, only counts and capture timestamps.
type ssoView struct {
	LoginAvailable bool      `json:"loginAvailable"`
	LoginCookies   int       `json:"loginCookies"`
	LoginCaptured  time.Time `json:"loginCapturedAt,omitempty"`
	M365Available  bool      `json:"m365Available"`
	M365Cookies    int       `json:"m365Cookies"`
	M365Captured   time.Time `json:"m365CapturedAt,omitempty"`
}

func ssoStatusFor(accountID string) ssoView {
	st := auth.SSOStatusFor(accountID)
	return ssoView{
		LoginAvailable: st.LoginAvailable,
		LoginCookies:   st.LoginCookies,
		LoginCaptured:  st.LoginCaptured,
		M365Available:  st.M365Available,
		M365Cookies:    st.M365Cookies,
		M365Captured:   st.M365Captured,
	}
}

// switchToAccount pins the active rotation account to the requested id and
// resets the rotation request counter so the manually selected account serves
// the next requests until rotation (if enabled) counts up to the limit again.
func (s *Server) switchToAccount(id string) error {
	if id == "" {
		return fmt.Errorf("account id is empty")
	}
	acc, ok := s.tokens.Get(id)
	if !ok {
		return fmt.Errorf("account not found")
	}
	if !s.accountPool.Available(acc.ID) {
		return fmt.Errorf("account is cooling down or failing auth")
	}
	s.rotationMu.Lock()
	s.rotationID = acc.ID
	s.rotationCount = 0
	s.rotationMu.Unlock()
	return nil
}

// switchAccount handles POST /api/accounts/switch.
func (s *Server) switchAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.ID) == "" {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	acc, ok := s.tokens.Get(strings.TrimSpace(body.ID))
	if !ok {
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}
	if !s.accountPool.Available(acc.ID) {
		http.Error(w, "account is cooling down or failing auth", http.StatusConflict)
		return
	}
	if err := s.switchToAccount(acc.ID); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	jsonOut(w, map[string]any{"status": "switched", "active": acc.ID})
}

func (s *Server) refreshAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.ID) == "" {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	acc, err := s.tokens.EnsureValid(strings.TrimSpace(body.ID))
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "token_refresh_error", err.Error())
		return
	}
	jsonOut(w, map[string]any{"status": "refreshed", "account": map[string]any{
		"id": acc.ID, "email": acc.Email, "displayName": acc.DisplayName,
		"status": acc.Status, "expiresAt": acc.ExpiresAt, "updatedAt": acc.UpdatedAt,
	}})
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := s.tokens.Delete(body.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOut(w, map[string]string{"status": "deleted"})
}

func (s *Server) startPKCE(w http.ResponseWriter, _ *http.Request) {
	v, err := accounts.Verifier()
	if err != nil {
		http.Error(w, "pkce failure", http.StatusInternalServerError)
		return
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		http.Error(w, "state failure", http.StatusInternalServerError)
		return
	}
	state := hex.EncodeToString(b)
	redirectURI := accounts.RedirectURI()
	s.mu.Lock()
	s.pkce[state] = pendingPKCE{Verifier: v, Created: time.Now(), Status: "pending", RedirectURI: redirectURI}
	s.mu.Unlock()
	jsonOut(w, map[string]string{
		"status": "pkce_ready",
		"state":  state,
		"url": accounts.AuthorizationURL(
			accounts.AuthorizeEndpoint(),
			accounts.ClientID(),
			redirectURI,
			state,
			accounts.Challenge(v),
			accounts.Scope(),
		),
		"redirectUri": redirectURI,
		"note":        "If redirect is nativeclient, paste the final URL/code into /api/auth/callback after login.",
	})
}

func (s *Server) pkceStatus(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if state == "" {
		http.Error(w, "missing state", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	p, ok := s.pkce[state]
	if ok && time.Since(p.Created) > 10*time.Minute {
		delete(s.pkce, state)
		ok = false
	}
	s.mu.Unlock()
	if !ok {
		jsonOut(w, map[string]any{"status": "expired"})
		return
	}
	out := map[string]any{"status": p.Status}
	if p.Account != nil {
		out["account"] = p.Account
	}
	if p.Error != "" {
		out["error"] = p.Error
	}
	jsonOut(w, out)
}

func (s *Server) callbackPKCE(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	oauthError := r.URL.Query().Get("error")
	if code == "" && oauthError == "" {
		if u := r.URL.Query().Get("url"); u != "" {
			if parsed, err := http.NewRequest(http.MethodGet, u, nil); err == nil {
				code = parsed.URL.Query().Get("code")
				oauthError = parsed.URL.Query().Get("error")
				if state == "" {
					state = parsed.URL.Query().Get("state")
				}
			}
		}
	}
	if state == "" || (code == "" && oauthError == "") {
		http.Error(w, "missing state or authorization result", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	p, ok := s.pkce[state]
	if !ok || time.Since(p.Created) > 10*time.Minute {
		if ok {
			delete(s.pkce, state)
		}
		s.mu.Unlock()
		http.Error(w, "invalid or expired state", http.StatusBadRequest)
		return
	}
	if p.Status != "pending" {
		s.mu.Unlock()
		http.Error(w, "authorization result already consumed", http.StatusConflict)
		return
	}
	p.Status = "processing"
	s.pkce[state] = p
	s.mu.Unlock()
	if oauthError != "" {
		log.Printf("oauth_error stage=callback error=%q", oauthError)
		s.mu.Lock()
		p.Status = "error"
		p.Error = oauthError
		s.pkce[state] = p
		s.mu.Unlock()
		http.Error(w, "Microsoft authorization failed: "+oauthError, http.StatusBadRequest)
		return
	}
	tok, err := accounts.ExchangeCode(code, p.Verifier, p.RedirectURI)
	if err != nil {
		logOAuthError("code_exchange", err)
		s.mu.Lock()
		p.Status = "error"
		p.Error = err.Error()
		s.pkce[state] = p
		s.mu.Unlock()
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	acc, err := s.tokens.Upsert(tok)
	if err != nil {
		s.mu.Lock()
		p.Status = "error"
		p.Error = err.Error()
		s.pkce[state] = p
		s.mu.Unlock()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	p.Status = "authenticated"
	p.Account = map[string]any{"id": acc.ID, "email": acc.Email, "displayName": acc.DisplayName, "status": acc.Status, "oid": acc.OID, "tid": acc.TID}
	s.pkce[state] = p
	s.mu.Unlock()
	if strings.HasPrefix(p.RedirectURI, "http://127.0.0.1:") || strings.HasPrefix(p.RedirectURI, "http://localhost:") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><meta charset="utf-8"><title>M365Bridge Plus 授权完成</title><style>body{font:16px system-ui;text-align:center;padding:15vh 20px;color:#242424}main{max-width:520px;margin:auto}h1{font-size:26px}</style><main><h1>授权完成</h1><p>账号已经自动加入账号池，可以关闭此页面。</p><script>if(window.opener){window.opener.postMessage({type:"m365-auth-complete"},window.location.origin);setTimeout(()=>window.close(),300)}</script></main>`)
		return
	}
	jsonOut(w, map[string]any{
		"status":  "authenticated",
		"account": map[string]any{"id": acc.ID, "email": acc.Email, "displayName": acc.DisplayName, "status": acc.Status, "oid": acc.OID, "tid": acc.TID},
	})
}

// resolveAccount selects a healthy account for a request. When the key is not
// bound to a specific account, the selection honours the rotation policy:
// with automatic rotation enabled the current account serves up to
// MaxRequestsPerAccount requests before the next healthy account takes over
// (cycling); with rotation disabled a stable account is pinned so incremental
// sessions can continue across turns. Cooling-down or auth-failing accounts
// are always skipped.
func (s *Server) resolveAccount(accountID string) (accounts.AccountToken, error) {
	if accountID == "" {
		return s.nextRotationAccount()
	}
	return s.tokens.EnsureValid(accountID)
}

// nextRotationAccount applies the account-rotation policy. With
// MaxRequestsPerAccount <= 0 it keeps the legacy per-request round-robin.
func (s *Server) nextRotationAccount() (accounts.AccountToken, error) {
	var cfg runtimeSettings
	if s.settings != nil {
		cfg = s.settings.get()
	}
	autoRotate := cfg.AutoRotateAccounts
	maxReq := cfg.MaxRequestsPerAccount

	s.rotationMu.Lock()
	defer s.rotationMu.Unlock()

	if autoRotate {
		if maxReq > 0 {
			if s.rotationID != "" && s.rotationCount < maxReq {
				if acc, ok := s.tokens.Get(s.rotationID); ok && s.accountPool.Available(acc.ID) {
					s.rotationCount++
					return s.tokens.EnsureValid(acc.ID)
				}
			}
			for range maxAccountProbe {
				acc, ok := s.tokens.Next()
				if !ok {
					return accounts.AccountToken{}, fmt.Errorf("no accounts; login first")
				}
				if !s.accountPool.Available(acc.ID) {
					continue
				}
				previousID := s.rotationID
				s.rotationID = acc.ID
				s.rotationCount = 1
				if previousID != acc.ID {
					log.Printf("[account-rotation] switched account from=%s to=%s email=%s reason=request_limit max_requests=%d", previousID, acc.ID, acc.Email, maxReq)
				}
				return s.tokens.EnsureValid(acc.ID)
			}
			return accounts.AccountToken{}, fmt.Errorf("all accounts are cooling down or failing auth; try again later")
		}
		acc, ok := s.tokens.Next()
		if !ok {
			return accounts.AccountToken{}, fmt.Errorf("no accounts; login first")
		}
		for i := 0; !s.accountPool.Available(acc.ID) && i < maxAccountProbe; i++ {
			acc, ok = s.tokens.Next()
			if !ok {
				break
			}
		}
		if !s.accountPool.Available(acc.ID) {
			return accounts.AccountToken{}, fmt.Errorf("all accounts are cooling down or failing auth; try again later")
		}
		previousID := s.rotationID
		s.rotationID = acc.ID
		s.rotationCount = 1
		if previousID != acc.ID {
			log.Printf("[account-rotation] switched account from=%s to=%s email=%s reason=round_robin max_requests=0", previousID, acc.ID, acc.Email)
		}
		return s.tokens.EnsureValid(acc.ID)
	}

	// Rotation disabled: pin a stable healthy account so incremental sessions
	// stay on the same M365 account across turns. Switch only when the pinned
	// account becomes unavailable.
	if s.rotationID != "" {
		if acc, ok := s.tokens.Get(s.rotationID); ok && s.accountPool.Available(acc.ID) {
			return s.tokens.EnsureValid(acc.ID)
		}
		s.rotationID = ""
	}
	for range maxAccountProbe {
		acc, ok := s.tokens.Next()
		if !ok {
			return accounts.AccountToken{}, fmt.Errorf("no accounts; login first")
		}
		if !s.accountPool.Available(acc.ID) {
			continue
		}
		s.rotationID = acc.ID
		s.rotationCount = 1
		return s.tokens.EnsureValid(acc.ID)
	}
	return accounts.AccountToken{}, fmt.Errorf("all accounts are cooling down or failing auth; try again later")
}

// nextHealthyAccount returns the next round-robin account that is still
// healthy, skipping the given id first, and validates its token.
func (s *Server) nextHealthyAccount(avoidID string) (accounts.AccountToken, error) {
	for range maxAccountProbe {
		acc, ok := s.tokens.Next()
		if !ok {
			return accounts.AccountToken{}, fmt.Errorf("no accounts; login first")
		}
		if avoidID != "" && acc.ID == avoidID {
			continue
		}
		if !s.accountPool.Available(acc.ID) {
			continue
		}
		return s.tokens.EnsureValid(acc.ID)
	}
	return accounts.AccountToken{}, fmt.Errorf("no healthy account available for failover")
}

func (s *Server) adminModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	jsonOut(w, map[string]any{"object": "list", "data": modelCatalog()})
}

// adminModelTest probes a model through the registered data-plane hook. The
// console authenticates via the admin session rather than a plaintext key.
func (s *Server) adminModelTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var b struct {
		Model string `json:"model"`
	}
	if json.NewDecoder(r.Body).Decode(&b) != nil || strings.TrimSpace(b.Model) == "" {
		http.Error(w, "bad json: model required", http.StatusBadRequest)
		return
	}
	acc, err := s.resolveAccount("")
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "account_error", err.Error())
		return
	}
	if acc.OID == "" || acc.TID == "" {
		if o, t := extractOIDTID(acc.AccessToken); o != "" {
			acc.OID, acc.TID = o, t
		}
	}
	if acc.OID == "" || acc.TID == "" {
		writeOpenAIError(w, http.StatusBadRequest, "account_error", "account missing oid/tid")
		return
	}
	s.mu.Lock()
	tester := s.modelTester
	s.mu.Unlock()
	if tester == nil {
		writeOpenAIError(w, http.StatusServiceUnavailable, "model_test_unavailable", "model tester not registered")
		return
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(s.settings.get().ChatTimeoutSeconds)*time.Second)
	defer cancel()
	reply, err := tester(ctx, acc, b.Model)
	ms := time.Since(start).Milliseconds()
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "m365_error", err.Error())
		return
	}
	jsonOut(w, map[string]any{"ok": true, "model": b.Model, "reply": reply, "latency_ms": ms})
}

// adminChat streams a multi-turn conversation through the registered data-plane
// hook. The console authenticates via the admin session rather than a key.
func (s *Server) adminChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var b struct {
		Model    string        `json:"model"`
		Messages []ChatMessage `json:"messages"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&b) != nil ||
		strings.TrimSpace(b.Model) == "" || len(b.Messages) == 0 {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "bad json: model and messages required")
		return
	}
	acc, err := s.resolveAccount("")
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "account_error", err.Error())
		return
	}
	if acc.OID == "" || acc.TID == "" {
		if o, t := extractOIDTID(acc.AccessToken); o != "" {
			acc.OID, acc.TID = o, t
		}
	}
	if acc.OID == "" || acc.TID == "" {
		writeOpenAIError(w, http.StatusBadRequest, "account_error", "account missing oid/tid")
		return
	}
	s.mu.Lock()
	chat := s.chatFunc
	s.mu.Unlock()
	if chat == nil {
		writeOpenAIError(w, http.StatusServiceUnavailable, "chat_unavailable", "chat hook not registered")
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	chunks, err := chat(ctx, acc, b.Model, b.Messages)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "m365_error", err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	for chunk := range chunks {
		payload, _ := json.Marshal(chunk)
		if _, werr := fmt.Fprintf(w, "data: %s\n\n", payload); werr != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	done, _ := json.Marshal(ChatChunk{Done: true})
	_, _ = fmt.Fprintf(w, "data: %s\n\n", done)
	if flusher != nil {
		flusher.Flush()
	}
}
