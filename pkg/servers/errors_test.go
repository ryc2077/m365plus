package servers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ryc2077/m365plus/pkg/auth"
	"github.com/ryc2077/m365plus/pkg/client"
)

func decodeOpenAIError(t *testing.T, body []byte) (string, string, string) {
	t.Helper()
	var decoded struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	return decoded.Error.Message, decoded.Error.Type, decoded.Error.Code
}

func TestOpenAIErrorShapeAndStatusMapping(t *testing.T) {
	cases := map[int]string{
		http.StatusBadRequest:      "invalid_request_error",
		http.StatusUnauthorized:    "authentication_error",
		http.StatusForbidden:       "authentication_error",
		http.StatusTooManyRequests: "rate_limit_error",
		http.StatusBadGateway:      "server_error",
	}
	for status, want := range cases {
		if got := openAIErrorType(status); got != want {
			t.Fatalf("openAIErrorType(%d) = %q, want %q", status, got, want)
		}
	}
	api := &APIServer{}
	recorder := httptest.NewRecorder()
	api.sendError(recorder, http.StatusBadRequest, "Invalid model")
	message, errorType, code := decodeOpenAIError(t, recorder.Body.Bytes())
	if message != "Invalid model" || errorType != "invalid_request_error" || code != "bad_request" {
		t.Fatalf("unexpected error envelope: message=%q type=%q code=%q", message, errorType, code)
	}
}

func TestClassifyUpstreamErrors(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"missing token", fmt.Errorf("token: %w", auth.ErrTokenNotFound), http.StatusUnauthorized, upstreamAuthFailedCode},
		{"deadline", context.DeadlineExceeded, http.StatusGatewayTimeout, upstreamTimeoutCode},
		{"forbidden", &client.UpstreamError{Op: "dial", Status: http.StatusForbidden, Err: errors.New("rejected")}, http.StatusForbidden, upstreamForbiddenCode},
		{"rate limit", &client.UpstreamError{Op: "dial", Status: http.StatusTooManyRequests, Err: errors.New("limited")}, http.StatusTooManyRequests, upstreamRateLimitCode},
		{"unavailable", &client.UpstreamError{Op: "dial", Status: http.StatusServiceUnavailable, Err: errors.New("offline")}, http.StatusServiceUnavailable, upstreamUnavailableCode},
		{"turn failed", &client.TurnFailedError{Value: "InternalError", TurnState: "Failed"}, http.StatusBadGateway, upstreamTurnFailedCode},
		{"empty turn", client.ErrEmptyTurn, http.StatusBadGateway, upstreamTurnFailedCode},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			status, code := classifyUpstreamError(test.err)
			if status != test.status || code != test.code {
				t.Fatalf("classification = (%d, %q), want (%d, %q)", status, code, test.status, test.code)
			}
		})
	}
}

func TestSendUpstreamErrorIsSanitizedAndSetsRetryAfter(t *testing.T) {
	api := &APIServer{}
	recorder := httptest.NewRecorder()
	secret := "https://example.invalid/path?token=secret-value"
	err := &client.UpstreamError{Op: "dial", Status: http.StatusTooManyRequests, Err: errors.New(secret)}
	api.sendUpstreamError(recorder, "chat", err)
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", recorder.Code)
	}
	if recorder.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After header is missing")
	}
	message, _, code := decodeOpenAIError(t, recorder.Body.Bytes())
	if code != upstreamRateLimitCode {
		t.Fatalf("code = %q, want %q", code, upstreamRateLimitCode)
	}
	if strings.Contains(message, secret) || strings.Contains(recorder.Body.String(), "secret-value") {
		t.Fatalf("upstream transport detail leaked: %s", recorder.Body.String())
	}
}

func TestStreamErrorFieldsAreSanitized(t *testing.T) {
	secret := "Bearer secret-token"
	status, code, message := streamErrorFields("responses", &client.UpstreamError{Op: "read", Status: http.StatusBadGateway, Err: errors.New(secret)})
	if status != http.StatusBadGateway || code != upstreamRejectedCode {
		t.Fatalf("classification = (%d, %q)", status, code)
	}
	if strings.Contains(message, secret) || strings.Contains(message, "secret-token") {
		t.Fatalf("stream message leaked upstream detail: %q", message)
	}
}
