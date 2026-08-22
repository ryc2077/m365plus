package servers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ryc2077/m365plus/pkg/models"
	"github.com/ryc2077/m365plus/pkg/payload"
)

func decodeToolRoundMessages(t *testing.T, raw string) []payload.Message {
	t.Helper()
	var messages []payload.Message
	if err := json.Unmarshal([]byte(raw), &messages); err != nil {
		t.Fatalf("decode messages: %v", err)
	}
	return messages
}

func openAIToolRounds(rounds int) string {
	var builder strings.Builder
	builder.WriteString(`[{"role":"user","content":"fix the build"}`)
	for index := range rounds {
		id := fmt.Sprintf("call_%d", index)
		builder.WriteString(`,{"role":"assistant","content":null,"tool_calls":[{"id":"` + id + `","type":"function","function":{"name":"run_tests","arguments":"{}"}}]}`)
		builder.WriteString(`,{"role":"tool","tool_call_id":"` + id + `","content":"ok"}`)
	}
	builder.WriteString("]")
	return builder.String()
}

func TestBuildToolLedgerCountsCurrentTurn(t *testing.T) {
	messages := decodeToolRoundMessages(t, openAIToolRounds(2))
	ledger := buildToolLedger(messages)
	if ledger.Rounds != 2 || len(ledger.Completed) != 2 {
		t.Fatalf("ledger = %#v, want two completed rounds", ledger)
	}
}

func TestChatCompletionsRejectsRunawayToolLoop(t *testing.T) {
	api := NewAPIServer(&models.Config{MaxToolRounds: 3}, nil)
	body := `{"model":"gpt5.5","messages":` + openAIToolRounds(5) + `}`
	recorder := httptest.NewRecorder()
	api.handleChatCompletions(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusConflict)
	}
	if !strings.Contains(recorder.Body.String(), toolRoundLimitCode) {
		t.Fatalf("response does not include %q: %s", toolRoundLimitCode, recorder.Body.String())
	}
}

func TestMaxToolRoundsCeiling(t *testing.T) {
	t.Setenv("M365_MAX_TOOL_ROUNDS", "100000")
	if got := models.LoadConfig().MaxToolRounds; got != models.MaxToolRoundsCeiling {
		t.Fatalf("MaxToolRounds = %d, want %d", got, models.MaxToolRoundsCeiling)
	}
}
