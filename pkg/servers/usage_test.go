package servers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ryc2077/m365plus/pkg/models"
)

type recordingUsageSink struct {
	records []UsageRecord
}

func (s *recordingUsageSink) RecordUsage(record UsageRecord) {
	s.records = append(s.records, record)
}

func TestWithAuthRecordsUsage(t *testing.T) {
	api := NewAPIServer(&models.Config{}, nil)
	sink := &recordingUsageSink{}
	api.SetUsageRecorder(sink)
	handler := api.withAuth(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"output":"hello world"}`))
	})

	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-test","stream":true,"input":"hello"}`))
	request.Header.Set("Authorization", "Bearer sk-1234567890")
	response := httptest.NewRecorder()
	handler(response, request)

	if len(sink.records) != 1 {
		t.Fatalf("usage records = %d, want 1", len(sink.records))
	}
	record := sink.records[0]
	if record.Model != "gpt-test" || !record.Stream {
		t.Fatalf("unexpected request metadata: %#v", record)
	}
	if record.Endpoint != "/v1/responses" || record.Status != http.StatusCreated {
		t.Fatalf("unexpected endpoint/status: %#v", record)
	}
	if record.APIKeyPrefix != "sk-12345" {
		t.Fatalf("API key prefix = %q", record.APIKeyPrefix)
	}
	if record.InputTokens <= 0 || record.OutputTokens <= 0 {
		t.Fatalf("token counts were not recorded: %#v", record)
	}
}
