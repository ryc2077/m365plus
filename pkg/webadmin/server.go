package webadmin

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ryc2077/m365plus/pkg/accounts"
	"github.com/ryc2077/m365plus/pkg/logging"
)

// pendingPKCE tracks an in-progress PKCE authorization.
type pendingPKCE struct {
	Verifier    string
	Created     time.Time
	Status      string
	Account     any
	Error       string
	RedirectURI string
}

const maxAdminSessions = 4096

// Server is the administrative/management plane: account pool, API-key store,
// admin console, settings, usage and conversation management. The OpenAI/SSE
// data plane lives in pkg/servers and is wired to this Server by the binary.
type Server struct {
	mu                  sync.Mutex
	tokens              *accounts.Store
	accountPool         *accountHealth
	pkce                map[string]pendingPKCE
	sessions            *sessionStore
	userSessions        *userSessionStore
	sessionResolver     *sessionResolver
	conversationManager *conversationManager
	adminPassword       string
	adminSessions       map[string]time.Time
	mustChangePassword  bool
	loginAttempts       map[string]loginAttempt
	apiKeys             *apiKeyStore
	settings            *settingsStore
	usage               *usageLog
	modelTester         ModelTester
	chatFunc            ChatFunc
	dataPlane           http.Handler
	debug               *debugStore
	rotationMu          sync.Mutex
	rotationID          string
	rotationCount       int
}

// New builds the administrative server bound to the shared account store.
func New() (*Server, error) {
	store, err := accounts.OpenStore("")
	if err != nil {
		return nil, err
	}
	if refresher, cerr := accounts.NewCDPRefresher(); cerr == nil {
		if refresher != nil && refresher.Enabled() {
			store.SetCDPRefresher(refresher)
			logging.Infof("CDP refresh fallback enabled (chromium: %s)", refresher.Binary())
		}
	} else {
		logging.Warnf("CDP refresh fallback unavailable: %v", cerr)
	}
	password, mustChange := loadAdminPassword()
	sessionTTL := 30 * time.Minute
	if v := os.Getenv("M365_USER_SESSION_TTL_MINUTES"); v != "" {
		if d, err := time.ParseDuration(v + "m"); err == nil {
			sessionTTL = d
		}
	}
	s := &Server{
		tokens:              store,
		accountPool:         newAccountHealth(),
		pkce:                map[string]pendingPKCE{},
		sessions:            openSessionStore(),
		userSessions:        openUserSessionStore(sessionTTL),
		sessionResolver:     openSessionResolver(),
		conversationManager: openConversationManager(),
		adminPassword:       password,
		adminSessions:       map[string]time.Time{},
		mustChangePassword:  mustChange,
		loginAttempts:       map[string]loginAttempt{},
		apiKeys:             openAPIKeys(),
		settings:            openSettingsStore(),
		usage:               openUsageLog(),
		debug:               openDebugStore(),
	}
	s.applyTokenRefreshInterval()
	return s, nil
}

// applyTokenRefreshInterval pushes the configured refresh interval into the
// account store so EnsureValid re-mints tokens on that cadence.
func (s *Server) applyTokenRefreshInterval() {
	cfg := s.settings.get()
	s.tokens.SetRefreshInterval(time.Duration(cfg.TokenRefreshIntervalMins) * time.Minute)
}

// ResolveAccountForKey implements the servers.AccountResolver contract: maps a
// raw API key to its bound account, or round-robins over the healthy pool when
// the key has no explicit binding. Returns ok=false when no accounts exist.
func (s *Server) ResolveAccountForKey(rawKey string) (accounts.AccountToken, bool, error) {
	if rawKey == "" {
		return accounts.AccountToken{}, false, nil
	}
	bound := s.apiKeys.boundAccountID(rawKey)
	if bound != "" {
		acc, err := s.tokens.EnsureValid(bound)
		if err != nil {
			return accounts.AccountToken{}, false, err
		}
		return acc, true, nil
	}
	acc, err := s.resolveAccount("")
	if err != nil {
		return accounts.AccountToken{}, false, err
	}
	return acc, true, nil
}

// Accounts exposes the shared account pool to the data plane.
func (s *Server) Accounts() *accounts.Store { return s.tokens }

// APIKeys exposes the shared API-key store to the data plane.
func (s *Server) APIKeys() *apiKeyStore { return s.apiKeys }

// Settings exposes the runtime settings to the data plane.
func (s *Server) Settings() *settingsStore { return s.settings }

// Usage returns the usage logger so the data plane can record every request.
func (s *Server) Usage() *usageLog { return s.usage }

// SetModelTester registers the data-plane probe hook used by adminModelTest.
func (s *Server) SetModelTester(t ModelTester) {
	s.mu.Lock()
	s.modelTester = t
	s.mu.Unlock()
}

// SetChatFunc registers the data-plane streaming chat hook used by the admin
// console chat page.
func (s *Server) SetChatFunc(f ChatFunc) {
	s.mu.Lock()
	s.chatFunc = f
	s.mu.Unlock()
}

// SetDataPlane mounts the M365Bridge data plane (pkg/servers) under /v1/.
// The admin middleware authenticates API keys before forwarding.
func (s *Server) SetDataPlane(h http.Handler) {
	s.mu.Lock()
	s.dataPlane = h
	s.mu.Unlock()
}

func (s *Server) dataPlaneHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	h := s.dataPlane
	s.mu.Unlock()
	if h == nil {
		writeOpenAIError(w, http.StatusServiceUnavailable, "data_plane_unavailable", "data plane not mounted")
		return
	}
	h.ServeHTTP(w, r)
}

// SessionResolver exposes session bindings to the data plane.
func (s *Server) SessionResolver() *sessionResolver { return s.sessionResolver }

// ConversationManager exposes cloud-conversation tracking to the data plane.
func (s *Server) ConversationManager() *conversationManager { return s.conversationManager }

// AccountPool exposes per-account health to the data plane.
func (s *Server) AccountPool() *accountHealth { return s.accountPool }

func (s *Server) InitM365CloudClient() {
	accountsList := s.tokens.List()
	if len(accountsList) == 0 {
		return
	}
	acc := accountsList[0]
	clientID := os.Getenv("M365_CLIENT_ID")
	if clientID == "" {
		clientID = acc.ClientID
	}
	if clientID == "" {
		clientID = accounts.ClientID()
	}
	InitM365CloudClient(clientID, acc.TID, acc.RefreshToken)
	log.Printf("[m365-cloud] client initialized for account %s", acc.Email)
}

// Routes returns the administrative HTTP routes. Data-plane routes
// (/v1/*) are mounted by the binary alongside these.
func (s *Server) Routes() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/api/admin/login", s.adminLogin)
	m.HandleFunc("/api/admin/logout", s.adminLogout)
	m.HandleFunc("/api/admin/session", s.adminSession)
	m.HandleFunc("/api/admin/change-password", s.adminChangePassword)
	m.HandleFunc("/api/admin/keys", s.adminKeys)
	m.HandleFunc("/api/admin/models", s.adminModels)
	m.HandleFunc("/api/admin/models/test", s.adminModelTest)
	m.HandleFunc("/api/admin/chat", s.adminChat)
	m.HandleFunc("/api/admin/settings", s.adminSettings)
	m.HandleFunc("/api/admin/proxy-pool", s.proxyPool)
	m.HandleFunc("/api/admin/deployments", s.deployments)
	m.HandleFunc("/api/admin/deployment", s.deploymentAction)
	m.HandleFunc("/api/admin/deployment/check", s.deploymentCheck)
	m.HandleFunc("/api/health", s.health)
	m.HandleFunc("/api/version", s.version)
	m.HandleFunc("/api/accounts", s.accounts)
	m.HandleFunc("/api/accounts/refresh", s.refreshAccount)
	m.HandleFunc("/api/accounts/delete", s.deleteAccount)
	m.HandleFunc("/api/auth/start", s.startPKCE)
	m.HandleFunc("/api/auth/status", s.pkceStatus)
	m.HandleFunc("/api/auth/callback", s.callbackPKCE)
	m.HandleFunc("/api/conversations", s.conversations)
	m.HandleFunc("/api/conversations/delete", s.deleteConversation)
	m.HandleFunc("/api/conversations/cleanup", s.conversationCleanup)
	m.HandleFunc("/api/conversations/whitelist", s.conversationWhitelist)
	m.HandleFunc("/v1/sessions", s.handleSessions)
	m.HandleFunc("/v1/sessions/", s.handleSessionDelete)
	m.HandleFunc("/api/m365/conversations", s.handleM365Conversations)
	m.HandleFunc("/api/m365/conversations/delete", s.handleM365Delete)
	m.HandleFunc("/api/m365/conversations/cleanup", s.handleM365Cleanup)
	m.HandleFunc("/api/stats", s.handleCacheStats)
	m.HandleFunc("/api/stats/reset", s.handleCacheStatsReset)
	m.HandleFunc("/api/usage", s.adminUsage)
	m.HandleFunc("/api/usage/logs", s.adminUsageLogs)
	m.HandleFunc("/api/admin/debug/logs", s.debugList)
	m.Handle("/v1/", http.HandlerFunc(s.dataPlaneHandler))
	m.HandleFunc("/", s.rootPage)
	return recoverPanics(requestID(httpTrace(securityHeaders(s.debugMiddleware(s.adminMiddleware(m))))))
}

func (s *Server) adminMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/admin/login" || r.URL.Path == "/api/admin/session" || r.URL.Path == "/api/admin/change-password" || r.URL.Path == "/api/admin/logout" || r.URL.Path == "/" || r.URL.Path == "/login" {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			if !s.validAPIKey(r) {
				http.Error(w, `{"error":{"message":"valid API key required","type":"auth_error"}}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if s.adminPassword == "" {
			http.Error(w, `{"error":{"message":"administrator password is not configured","type":"configuration_error"}}`, http.StatusServiceUnavailable)
			return
		}
		if !s.validAdminSession(r) {
			writeOpenAIError(w, http.StatusUnauthorized, "auth_error", "administrator login required")
			return
		}
		s.mu.Lock()
		mustChange := s.mustChangePassword
		s.mu.Unlock()
		if mustChange && r.URL.Path != "/api/admin/change-password" && r.URL.Path != "/api/admin/logout" {
			writeOpenAIError(w, http.StatusForbidden, "password_change_required", "administrator password must be changed before using the console")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func secureAdminCookie(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return net.ParseIP(host).IsLoopback() && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (s *Server) validAdminSession(r *http.Request) bool {
	c, err := r.Cookie("m365_admin_session")
	if err != nil || c.Value == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	expires, ok := s.adminSessions[c.Value]
	if !ok || time.Now().After(expires) {
		delete(s.adminSessions, c.Value)
		return false
	}
	return true
}

func (s *Server) validAPIKey(r *http.Request) bool {
	raw := strings.TrimSpace(r.Header.Get("X-API-Key"))
	if raw == "" {
		v := r.Header.Get("Authorization")
		if strings.HasPrefix(strings.ToLower(v), "bearer ") {
			raw = strings.TrimSpace(v[7:])
		}
	}
	return raw != "" && s.apiKeys.valid(raw)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	list := s.tokens.List()
	jsonOut(w, map[string]any{
		"status":       "ok",
		"auth":         []string{"pkce"},
		"chat":         "m365bridge",
		"clientId":     accounts.ClientID(),
		"scope":        accounts.Scope(),
		"tokenCache":   s.tokens.Path(),
		"accountCount": len(list),
	})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// extractOIDTID pulls oid/tid claims from a JWT access token on the fly.
func extractOIDTID(accessToken string) (oid, tid string) {
	if accessToken == "" {
		return "", ""
	}
	parts := strings.Split(accessToken, ".")
	if len(parts) < 2 {
		return "", ""
	}
	raw := strings.NewReplacer("-", "+", "_", "/").Replace(parts[1])
	switch len(raw) % 4 {
	case 2:
		raw += "=="
	case 3:
		raw += "="
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", ""
	}
	var m map[string]any
	if json.Unmarshal(decoded, &m) != nil {
		return "", ""
	}
	oid, _ = m["oid"].(string)
	tid, _ = m["tid"].(string)
	return oid, tid
}

func fmtErr(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}
