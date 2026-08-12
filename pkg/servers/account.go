package servers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/ryc2077/m365plus/pkg/accounts"
	"github.com/ryc2077/m365plus/pkg/client"
	"github.com/ryc2077/m365plus/pkg/models"
	"github.com/ryc2077/m365plus/pkg/payload"
	"github.com/ryc2077/m365plus/pkg/toolcalling"
)

// AccountResolver resolves the account bound to an API key. It is injected by
// the binary when multi-account mode is enabled (webadmin management plane).
type AccountResolver interface {
	// ResolveAccountForKey returns the account bound to the API key. When the
	// key has no explicit binding and round-robin is desired, return an empty
	// (zero) AccountToken with ok=false; the data plane then selects the next
	// healthy account itself.
	ResolveAccountForKey(apiKey string) (accounts.AccountToken, bool, error)

	// IncrementalContextEnabled reports whether the incremental-session
	// context (persisted conversation continuation) is currently allowed.
	// When the resolver is nil (single-account mode) it defaults to enabled.
	IncrementalContextEnabled() bool
}

// reqAccount carries the per-request account binding through the call chain.
// A nil reqAccount means single-account mode (config + tokenManager).
type reqAccount struct {
	client *client.M365Client
	token  accounts.AccountToken
}

type reqAccountKey struct{}

// withReqAccount attaches a per-request account to the request context.
func withReqAccount(r *http.Request, ra *reqAccount) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), reqAccountKey{}, ra))
}

// reqAccountFromContext reads the per-request account from a context.
// It is used by response handlers that thread ctx rather than r.
func reqAccountFromContext(ctx context.Context) *reqAccount {
	if ctx == nil {
		return nil
	}
	ra, _ := ctx.Value(reqAccountKey{}).(*reqAccount)
	return ra
}

func reqAccountFrom(r *http.Request) *reqAccount {
	if r == nil {
		return nil
	}
	return reqAccountFromContext(r.Context())
}

// accountClientFrom returns the per-request client when a binding exists,
// otherwise the shared single-account client.
func (api *APIServer) accountClientFrom(ra *reqAccount) *client.M365Client {
	if ra != nil && ra.client != nil {
		return ra.client
	}
	return api.m365Client
}

// accountOIDTIDFrom returns the per-request OID/TID when a binding exists,
// otherwise the single-account config values.
func (api *APIServer) accountOIDTIDFrom(ra *reqAccount) (oid, tid string) {
	if ra != nil {
		if ra.token.OID != "" || ra.token.TID != "" {
			return ra.token.OID, ra.token.TID
		}
	}
	return api.config.UserOID, api.config.TenantID
}

// accountClient returns the per-request client when a binding exists,
// otherwise the shared single-account client.
func (api *APIServer) accountClient(r *http.Request) *client.M365Client {
	if ra := reqAccountFrom(r); ra != nil && ra.client != nil {
		return ra.client
	}
	return api.m365Client
}

// accountOIDTID returns the per-request OID/TID when a binding exists,
// otherwise the single-account config values.
func (api *APIServer) accountOIDTID(r *http.Request) (oid, tid string) {
	if ra := reqAccountFrom(r); ra != nil {
		if ra.token.OID != "" || ra.token.TID != "" {
			return ra.token.OID, ra.token.TID
		}
	}
	return api.config.UserOID, api.config.TenantID
}

// resolveRequestAccount binds the API key to an account via the injected
// resolver. In multi-account mode with a valid binding it returns a client
// bound to that account's access token. Returns a nil *reqAccount when no
// resolver is configured (single-account mode) or no binding exists.
func (api *APIServer) resolveRequestAccount(r *http.Request) (*reqAccount, error) {
	api.mu.RLock()
	resolver := api.accountResolver
	api.mu.RUnlock()
	if resolver == nil {
		return nil, nil
	}
	acc, ok, err := resolver.ResolveAccountForKey(api.extractAPIKey(r))
	if err != nil {
		return nil, err
	}
	if !ok || acc.AccessToken == "" {
		return nil, nil
	}
	if acc.OID == "" || acc.TID == "" {
		acc.OID, acc.TID = extractAccountOIDTID(acc.AccessToken)
	}
	return &reqAccount{
		client: api.m365Client.WithAccessToken(acc.AccessToken),
		token:  acc,
	}, nil
}

// extractAccountOIDTID pulls oid/tid claims from a JWT access token on the fly.
func extractAccountOIDTID(accessToken string) (oid, tid string) {
	if accessToken == "" {
		return "", ""
	}
	parts := strings.Split(accessToken, ".")
	if len(parts) < 2 {
		return "", ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ""
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return "", ""
	}
	oid, _ = m["oid"].(string)
	tid, _ = m["tid"].(string)
	return oid, tid
}

// ProbeModel sends a minimal chat request through the account-bound client,
// used by the admin console model test.
func (api *APIServer) ProbeModel(ctx context.Context, acc accounts.AccountToken, model string) (string, error) {
	if api.m365Client == nil {
		return "", fmt.Errorf("data plane not initialized")
	}
	if acc.OID == "" || acc.TID == "" {
		acc.OID, acc.TID = extractAccountOIDTID(acc.AccessToken)
	}
	cfg := models.LookupModel(model)
	if cfg.OpenAIID == "" {
		cfg = models.LookupModel("gpt5.5")
	}
	m365 := api.m365Client.WithAccessToken(acc.AccessToken)
	text, _, _, _, _, err := m365.ChatConversation(
		[]payload.Message{{Role: "user", Content: `Say "OK" in one word.`}},
		cfg.Tone, cfg.Override, "", acc.OID, acc.TID, false,
	)
	if err != nil {
		return "", err
	}
	return text, nil
}

// ChatStream streams a multi-turn conversation for a specific account. It is
// the streaming counterpart of ProbeModel used by the admin console chat page:
// the request binds to the selected account and returns parsed content chunks.
//
// The request is routed through the simulated-response path even though the
// chat page declares no client tools. M365 Copilot otherwise runs its own
// code_interpreter sandbox for wording such as "get the system config"
// regardless of stripped option sets or system instructions. The simulation
// prompt makes M365 emit a chat-completion-shaped JSON object instead of
// executing code; the parsed content is then streamed to the caller.
func (api *APIServer) ChatStream(ctx context.Context, acc accounts.AccountToken, model string, messages []payload.Message) (<-chan client.StreamChunk, error) {
	if api.m365Client == nil {
		return nil, fmt.Errorf("data plane not initialized")
	}
	if acc.OID == "" || acc.TID == "" {
		acc.OID, acc.TID = extractAccountOIDTID(acc.AccessToken)
	}
	if acc.OID == "" || acc.TID == "" {
		return nil, fmt.Errorf("account missing oid/tid")
	}
	cfg := models.LookupModel(model)
	if cfg.OpenAIID == "" {
		return nil, fmt.Errorf("unknown model: %s", model)
	}

	// Build a chat-completions request JSON embedding the whole conversation.
	// The simulation prompt consumes this shape and forces a JSON response
	// object, so M365 never executes code in its own sandbox.
	reqJSON, err := buildWebAdminSimulationRequest(model, messages)
	if err != nil {
		return nil, err
	}
	prompt := toolcalling.BuildSimulatedPrompt(reqJSON, false, "")
	simulated := append([]payload.Message(nil), messages...)
	simulated = append(simulated, payload.Message{Role: "user", Content: prompt})

	m365 := api.m365Client.WithAccessToken(acc.AccessToken)
	upstream := m365.ChatConversationStreamGenContext(ctx, simulated, cfg.Tone, cfg.Override, "", acc.OID, acc.TID, true)

	out := make(chan client.StreamChunk)
	go func() {
		defer close(out)
		var raw strings.Builder
		for c := range upstream {
			if c.Error != nil {
				out <- client.StreamChunk{Error: c.Error}
				return
			}
			if c.Thinking != "" {
				out <- client.StreamChunk{Thinking: c.Thinking}
			}
			if c.Text != "" {
				raw.WriteString(c.Text)
			}
		}
		res := toolcalling.ParseSimulatedResponse(raw.String(), nil, nil)
		text := res.Content
		if text == "" && res.HasPayload {
			text = raw.String()
		}
		if text != "" {
			out <- client.StreamChunk{Text: text, IsFinal: true}
		}
	}()
	return out, nil
}

// buildWebAdminSimulationRequest serializes the chat conversation into an
// OpenAI chat.completions request body that the simulation prompt can embed.
func buildWebAdminSimulationRequest(model string, messages []payload.Message) (string, error) {
	req := map[string]any{
		"model":    model,
		"messages": messages,
		"stream":   false,
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
