package servers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ryc2077/m365plus/pkg/models"
)

func authStatus(t *testing.T, headers map[string]string) int {
	t.Helper()
	api := &APIServer{config: &models.Config{APIKeys: []string{"good-key"}}}
	handler := api.withAuth(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	return recorder.Code
}

func TestAuthAcceptsEitherAPIKeyHeader(t *testing.T) {
	cases := map[string]map[string]string{
		"x-api-key":         {"X-API-Key": "good-key"},
		"bearer":            {"Authorization": "Bearer good-key"},
		"lowercase bearer":  {"Authorization": "bearer good-key"},
		"raw authorization": {"Authorization": "good-key"},
	}
	for name, headers := range cases {
		t.Run(name, func(t *testing.T) {
			if status := authStatus(t, headers); status != http.StatusOK {
				t.Fatalf("status = %d, want 200", status)
			}
		})
	}
}

func TestAuthAcceptsValidKeyBesideStaleKey(t *testing.T) {
	status := authStatus(t, map[string]string{
		"X-API-Key":     "stale-key",
		"Authorization": "Bearer good-key",
	})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
}

func TestAuthRejectsMissingAndInvalidKeys(t *testing.T) {
	for name, headers := range map[string]map[string]string{
		"missing":      nil,
		"invalid":      {"X-API-Key": "wrong"},
		"empty bearer": {"Authorization": "Bearer "},
	} {
		t.Run(name, func(t *testing.T) {
			if status := authStatus(t, headers); status != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", status)
			}
		})
	}
}
