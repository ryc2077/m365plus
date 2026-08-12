package servers

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryc2077/m365plus/pkg/auth"
	"github.com/ryc2077/m365plus/pkg/client"
	"github.com/ryc2077/m365plus/pkg/models"
	"github.com/ryc2077/m365plus/pkg/payload"
)

func TestChatAndAnthropicConversationErrorsClearStickySession(t *testing.T) {
	tests := []struct {
		name string
		run  func(*APIServer, http.ResponseWriter, string, *http.Request)
	}{
		{
			name: "chat stream",
			run: func(api *APIServer, w http.ResponseWriter, sid string, r *http.Request) {
				api.streamChatCompletions(
					r,
					w,
					[]payload.Message{{Role: "user", Content: "hello"}},
					models.ModelConfig{OpenAIID: "gpt-test"},
					sid,
					"conv-poisoned",
					0,
					false,
					nil,
				)
			},
		},
		{
			name: "chat non-stream",
			run: func(api *APIServer, w http.ResponseWriter, sid string, r *http.Request) {
				api.nonStreamChatCompletions(
					r,
					w,
					[]payload.Message{{Role: "user", Content: "hello"}},
					models.ModelConfig{OpenAIID: "gpt-test"},
					sid,
					"conv-poisoned",
					0,
					false,
					nil,
				)
			},
		},
		{
			name: "anthropic stream",
			run: func(api *APIServer, w http.ResponseWriter, sid string, r *http.Request) {
				api.streamAnthropicMessages(
					r,
					w,
					[]payload.Message{{Role: "user", Content: "hello"}},
					models.ModelConfig{OpenAIID: "gpt-test"},
					"claude-test",
					0,
					sid,
					"conv-poisoned",
					false,
					nil,
				)
			},
		},
		{
			name: "anthropic non-stream",
			run: func(api *APIServer, w http.ResponseWriter, sid string, r *http.Request) {
				api.nonStreamAnthropicMessages(
					r,
					w,
					[]payload.Message{{Role: "user", Content: "hello"}},
					models.ModelConfig{OpenAIID: "gpt-test"},
					"claude-test",
					0,
					sid,
					"conv-poisoned",
					false,
					nil,
				)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newFailingConversationServer(t)
			sid := "parity-session"
			cacheKey := "session:" + sid
			api.ctxCache.Set(cacheKey, "conv-poisoned")

			tt.run(api, httptest.NewRecorder(), sid, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))

			if got := api.ctxCache.Get(cacheKey); got != "" {
				t.Fatalf("failed conversation kept sticky session %q", got)
			}
		})
	}
}

func TestUpdateChatStreamSessionDropsOnlyEmptyFinalTurn(t *testing.T) {
	tests := []struct {
		name       string
		fullText   string
		thinking   string
		toolCalls  []client.ToolCall
		wantConvID string
	}{
		{name: "empty turn"},
		{name: "assistant content", fullText: "done", wantConvID: "conv-new"},
		{name: "reasoning only", thinking: "working", wantConvID: "conv-new"},
		{
			name: "tool call",
			toolCalls: []client.ToolCall{{
				ID:   "call_test",
				Type: "function",
				Function: client.ToolCallFunction{
					Name:      "read_nonce",
					Arguments: `{}`,
				},
			}},
			wantConvID: "conv-new",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := NewContextCache(t.TempDir())
			api := &APIServer{ctxCache: cache}
			sid := "stream-session"
			cacheKey := "session:" + sid
			cache.Set(cacheKey, "conv-old")

			api.updateChatStreamSession(
				sid,
				"conv-new",
				tt.fullText,
				tt.thinking,
				tt.toolCalls,
			)

			if got := cache.Get(cacheKey); got != tt.wantConvID {
				t.Fatalf("cached conversation = %q, want %q", got, tt.wantConvID)
			}
		})
	}
}

func TestChatAnthropicThinkingForOutputRemovesSimulationProtocolNoise(t *testing.T) {
	raw := "Checked the requested file.\n" +
		"Generating JSON response for chat.completion.\n" +
		"```json\n" +
		`{"id":"chatcmpl-test","object":"chat.completion","choices":[]}` + "\n" +
		"```\n" +
		"finish_reason will be tool_calls."

	if got := chatAnthropicThinkingForOutput(raw, true); got != "Checked the requested file." {
		t.Fatalf("simulated thinking leaked protocol narration: %q", got)
	}
	if got := chatAnthropicThinkingForOutput(raw, false); got != raw {
		t.Fatalf("non-simulated thinking changed: %q", got)
	}
}

func TestChatAndAnthropicSimulationPromptsPreserveCurrentUserMessage(t *testing.T) {
	const (
		currentUser = "Read nonce.txt and return the exact value."
		requestJSON = `{"messages":[{"role":"user","content":"Read nonce.txt and return the exact value."}]}`
		marker      = "CURRENT USER MESSAGE"
	)

	tests := []struct {
		name   string
		inject func(*[]payload.Message)
	}{
		{
			name: "chat completions",
			inject: func(messages *[]payload.Message) {
				injectSimulatedPrompt(messages, requestJSON, "required")
			},
		},
		{
			name: "anthropic messages",
			inject: func(messages *[]payload.Message) {
				injectSimulatedPromptAnthropic(messages, requestJSON, "any")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages := []payload.Message{
				{Role: "assistant", Content: "Earlier response"},
				{Role: "user", Content: currentUser},
			}

			tt.inject(&messages)

			got := messages[len(messages)-1].Content
			if !strings.Contains(got, marker) {
				t.Fatalf("simulation prompt omitted %q section:\n%s", marker, got)
			}
			if !strings.Contains(got, marker+"\n"+currentUser) {
				t.Fatalf("current user message is not explicit outside transport JSON:\n%s", got)
			}
			if strings.LastIndex(got, currentUser) <= strings.LastIndex(got, "```") {
				t.Fatalf("current user message remained buried inside transport JSON:\n%s", got)
			}
		})
	}
}

func newFailingConversationServer(t *testing.T) *APIServer {
	t.Helper()

	stateDir := t.TempDir()
	tokenManager := auth.NewTokenManager(
		"test-tenant",
		"test-client",
		"test-scope",
		filepath.Join(stateDir, "missing-refresh-token"),
		filepath.Join(stateDir, "missing-token-cache"),
	)

	return &APIServer{
		config:     &models.Config{},
		m365Client: client.NewM365Client(tokenManager),
		ctxCache:   NewContextCache(filepath.Join(stateDir, "conversation-cache")),
	}
}
