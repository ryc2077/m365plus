package accounts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// CDPRefresher recovers an expired account token by launching a headless
// Chromium session against the account's persisted browser profile and reading
// a fresh substrate token out of the live page (localStorage / resource
// timings). It is the CDP fallback behind the fast plain-HTTP refresh_token
// exchange: when AAD rejects the RT (dead chain), the profile's signed-in
// session is still able to silently SSO and yield a new token.
type CDPRefresher struct {
	profileRoot string
	bin         string
	timeout     time.Duration
}

const (
	cdpLaunchTimeout    = 90 * time.Second
	cdpPageWaitInterval = 500 * time.Millisecond
	cdpSubstrateAud     = "substrate.office.com"
)

// cdpExtractJS mirrors Ciallo's _CDP_JS: harvest substrate bearer tokens from
// LokiAuthToken (session/local storage), the substrate WS resource timings, and
// MSAL access-token records.
const cdpExtractJS = `(() => {
    const candidates = [];
    for (const store of [sessionStorage, localStorage]) {
        for (const key of ['LokiAuthToken', ...Object.keys(store).filter(k => k.startsWith('LokiAuthToken'))]) {
            const token = store.getItem(key);
            if (token && token.startsWith('eyJ')) candidates.push(token);
        }
    }
    for (const entry of performance.getEntriesByType('resource')) {
        if (!entry.name.includes('substrate.office.com') || !entry.name.includes('access_token=')) continue;
        const m = entry.name.match(/[?&]access_token=([^&]+)/);
        if (m) { try { candidates.push(decodeURIComponent(m[1])); } catch (e) {} }
    }
    for (const store of [sessionStorage, localStorage]) {
        for (const k of Object.keys(store)) {
            if (!k.toLowerCase().includes('accesstoken')) continue;
            try {
                const v = JSON.parse(store.getItem(k));
                if (v && v.secret && v.secret.startsWith('eyJ') &&
                    ((v.target && v.target.includes('substrate')) || k.toLowerCase().includes('substrate'))) {
                    candidates.push(v.secret);
                }
            } catch (e) {}
        }
    }
    return candidates;
})()`

// cdpNudgeJS mirrors Ciallo's _CDP_NUDGE_JS: focus the composer so the SPA
// (re)opens the substrate WebSocket, which is what populates the resource
// timings with a fresh access_token.
const cdpNudgeJS = `(() => {
    const input = document.querySelector('[aria-label="Message Copilot"], textarea, [contenteditable="true"], [role="textbox"]');
    if (!input) return false;
    input.focus();
    input.click();
    return true;
})()`

// NewCDPRefresher locates a usable Chromium binary and prepares a profile root.
// Returns (nil, nil) when no binary is found; callers treat that as disabled.
func NewCDPRefresher() (*CDPRefresher, error) {
	bin := cdpChromiumPath()
	if bin == "" {
		return nil, nil
	}
	root := cdpProfileRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create cdp profile root: %w", err)
	}
	return &CDPRefresher{profileRoot: root, bin: bin, timeout: cdpLaunchTimeout}, nil
}

// Enabled reports whether a Chromium binary was found.
func (c *CDPRefresher) Enabled() bool { return c != nil && c.bin != "" }

// Binary returns the resolved Chromium binary path ("" when disabled).
func (c *CDPRefresher) Binary() string {
	if c == nil {
		return ""
	}
	return c.bin
}

func cdpProfileRoot() string {
	if dir := os.Getenv("M365_DATA_DIR"); dir != "" {
		return filepath.Join(dir, "cdp")
	}
	h, err := os.UserHomeDir()
	if err != nil || h == "" {
		return filepath.Join(".config", "m365-copilot2api", "cdp")
	}
	return filepath.Join(h, ".config", "m365-copilot2api", "cdp")
}

func cdpChromiumPath() string {
	if bin := os.Getenv("CHROME_BIN"); bin != "" {
		if p, err := exec.LookPath(bin); err == nil {
			return p
		}
	}
	for _, name := range []string{"chromium", "chromium-browser", "microsoft-edge", "microsoft-edge-stable", "google-chrome", "chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// ProfileDir returns the per-account persisted browser profile directory.
func (c *CDPRefresher) ProfileDir(id string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '_'
	}, id)
	return filepath.Join(c.profileRoot, safe)
}

// Refresh launches a headless Chromium against the account profile, navigates to
// the M365 chat surface (biased to the account identity via login_hint), and
// captures a fresh substrate token. It returns an updated AccountToken on
// success and an error otherwise. This method must be serialised by the caller
// (Store holds a global mutex): only one Chromium may be alive at a time.
func (c *CDPRefresher) Refresh(acc AccountToken) (AccountToken, error) {
	if acc.ID == "" {
		return acc, fmt.Errorf("cdp refresh: empty account id")
	}
	profileDir := c.ProfileDir(acc.ID)
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return acc, fmt.Errorf("cdp refresh: mkdir profile: %w", err)
	}
	cleanupProfileLocks(profileDir)

	port, err := freePort()
	if err != nil {
		return acc, fmt.Errorf("cdp refresh: find port: %w", err)
	}
	url := m365ChatURL(acc.Email)

	args := []string{
		fmt.Sprintf("--remote-debugging-port=%d", port),
		fmt.Sprintf("--user-data-dir=%s", profileDir),
		"--no-first-run",
		"--no-default-browser-check",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--disable-gpu",
		"--disable-background-networking",
		"--disable-sync",
		"--disable-breakpad",
		"--log-level=3",
		"--disable-software-rasterizer",
		"--headless=new",
		url,
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return acc, fmt.Errorf("cdp refresh: launch chromium: %w", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	tab, err := waitForM365Page(ctx, port)
	if err != nil {
		return acc, err
	}

	token, err := cdpCaptureToken(ctx, tab, acc.Email, c.timeout)
	if err != nil {
		return acc, err
	}

	claims, err := decodeJWTClaims(token)
	if err != nil {
		return acc, fmt.Errorf("cdp refresh: captured token not a JWT: %w", err)
	}
	exp, _ := parseExp(claims["exp"])
	if exp.IsZero() || exp.Before(time.Now()) {
		return acc, fmt.Errorf("cdp refresh: captured token has no valid exp")
	}
	acc.AccessToken = token
	acc.ExpiresAt = exp
	acc.Status = "online"
	acc.UpdatedAt = time.Now()
	if tid := claims["tid"]; tid != "" {
		acc.TID = tid
	}
	if oid := claims["oid"]; oid != "" {
		acc.OID = oid
	}
	return acc, nil
}

type cdpTab struct {
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func waitForM365Page(ctx context.Context, port int) (cdpTab, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(cdpLaunchTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return cdpTab{}, ctx.Err()
		default:
		}
		tabs, err := fetchTabs(client, port)
		if err == nil {
			for _, t := range tabs {
				if t.Type == "page" && isM365PageURL(t.URL) {
					return t, nil
				}
			}
		}
		time.Sleep(cdpPageWaitInterval)
	}
	return cdpTab{}, fmt.Errorf("cdp refresh: M365 page not ready on port %d", port)
}

func fetchTabs(client *http.Client, port int) ([]cdpTab, error) {
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/json", port))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var tabs []cdpTab
	if err := json.NewDecoder(resp.Body).Decode(&tabs); err != nil {
		return nil, err
	}
	return tabs, nil
}

func isM365PageURL(u string) bool {
	for _, p := range []string{
		"https://m365.cloud.microsoft/",
		"https://www.microsoft365.com/",
		"https://office.com/",
		"https://www.office.com/",
		"https://login.microsoftonline.com/",
		"https://login.live.com/",
	} {
		if strings.HasPrefix(u, p) {
			return true
		}
	}
	return false
}

func m365ChatURL(loginHint string) string {
	if strings.TrimSpace(loginHint) == "" {
		return "https://m365.cloud.microsoft/chat"
	}
	return "https://m365.cloud.microsoft/chat?login_hint=" + urlQueryEscape(loginHint)
}

func urlQueryEscape(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~', c == '@':
			b.WriteRune(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// cdpCaptureToken reads substrate token candidates from the live page. When a
// quick harvest finds none it nudges the composer (focus + synthetic keys) to
// force the substrate WebSocket open, then re-harvests for a short window.
func cdpCaptureToken(ctx context.Context, tab cdpTab, expectedEmail string, budget time.Duration) (string, error) {
	if tab.WebSocketDebuggerURL == "" {
		return "", fmt.Errorf("cdp refresh: tab has no websocket url")
	}
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, _, err := dialer.DialContext(ctx, tab.WebSocketDebuggerURL, nil)
	if err != nil {
		return "", fmt.Errorf("cdp refresh: dial page websocket: %w", err)
	}
	defer conn.Close()

	if token := cdpEvaluateToken(conn, expectedEmail); token != "" {
		return token, nil
	}
	if !cdpNudge(conn) {
		return "", fmt.Errorf("cdp refresh: composer not found; no substrate token captured")
	}
	// After the nudge the SPA opens the substrate WS, which populates the
	// resource timings; poll briefly for the token to appear.
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		if token := cdpEvaluateToken(conn, expectedEmail); token != "" {
			return token, nil
		}
		time.Sleep(2 * time.Second)
	}
	return "", fmt.Errorf("cdp refresh: no substrate token captured after nudge")
}

func cdpEvaluateToken(conn *websocket.Conn, expectedEmail string) string {
	msg := map[string]any{
		"id":     1,
		"method": "Runtime.evaluate",
		"params": map[string]any{"expression": cdpExtractJS, "returnByValue": true},
	}
	if err := conn.WriteJSON(msg); err != nil {
		return ""
	}
	var resp struct {
		Result struct {
			Result struct {
				Value []string `json:"value"`
			} `json:"result"`
		} `json:"result"`
	}
	if err := conn.ReadJSON(&resp); err != nil {
		return ""
	}
	return selectSubstrateToken(resp.Result.Result.Value, expectedEmail)
}

func cdpNudge(conn *websocket.Conn) bool {
	msg := map[string]any{
		"id":     10,
		"method": "Runtime.evaluate",
		"params": map[string]any{"expression": cdpNudgeJS, "returnByValue": true},
	}
	if err := conn.WriteJSON(msg); err != nil {
		return false
	}
	var resp struct {
		Result struct {
			Result struct {
				Value bool `json:"value"`
			} `json:"result"`
		} `json:"result"`
	}
	if err := conn.ReadJSON(&resp); err != nil {
		return false
	}
	return resp.Result.Result.Value
}

func selectSubstrateToken(candidates []string, expectedEmail string) string {
	want := strings.ToLower(strings.TrimSpace(expectedEmail))
	firstValid := ""
	for _, token := range candidates {
		if !isSubstrateToken(token) {
			continue
		}
		if firstValid == "" {
			firstValid = token
		}
		if want != "" && tokenIdentityEmail(token) == want {
			return token
		}
	}
	return firstValid
}

func isSubstrateToken(token string) bool {
	if !strings.HasPrefix(token, "eyJ") {
		return false
	}
	claims, err := decodeJWTClaims(token)
	if err != nil {
		return false
	}
	return strings.Contains(claims["aud"], cdpSubstrateAud)
}

func tokenIdentityEmail(token string) string {
	claims, err := decodeJWTClaims(token)
	if err != nil {
		return ""
	}
	for _, k := range []string{"email", "upn", "unique_name", "preferred_username"} {
		if v := strings.TrimSpace(claims[k]); v != "" {
			return strings.ToLower(v)
		}
	}
	return ""
}

func parseExp(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, fmt.Errorf("empty exp")
	}
	var secs int64
	for _, c := range raw {
		if c < '0' || c > '9' {
			return time.Time{}, fmt.Errorf("non-numeric exp")
		}
		secs = secs*10 + int64(c-'0')
	}
	return time.Unix(secs, 0), nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// cleanupProfileLocks stops stale Chromium processes bound to this profile and
// removes the Singleton* locks so a fresh launch can take ownership.
func cleanupProfileLocks(profileDir string) {
	profile := filepath.Clean(profileDir)
	cmdline, _ := os.ReadFile("/proc/self/cmdline")
	self := string(bytes.ReplaceAll(cmdline, []byte{0}, []byte(" ")))
	procRoot := "/proc"
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return
	}
	killStale := func(sig os.Signal) {
		for _, e := range entries {
			if !allDigits(e.Name()) {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "cmdline"))
			if err != nil {
				continue
			}
			line := string(bytes.ReplaceAll(raw, []byte{0}, []byte(" ")))
			if strings.Contains(self, "--user-data-dir="+profile) {
				continue
			}
			if strings.Contains(line, "--user-data-dir="+profile) || strings.Contains(line, "--user-data-dir="+profileDir) {
				_ = exec.Command("kill", "-"+signalString(sig), e.Name()).Run()
			}
		}
	}
	killStale(os.Interrupt)
	time.Sleep(300 * time.Millisecond)
	killStale(os.Kill)
	for _, name := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket"} {
		_ = os.Remove(filepath.Join(profileDir, name))
	}
}

func signalString(s os.Signal) string {
	if s == os.Kill {
		return "KILL"
	}
	return "TERM"
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
