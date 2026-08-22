package servers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ryc2077/m365plus/pkg/payload"
)

func decodeToolMessages(t *testing.T, raw string) []payload.Message {
	t.Helper()
	var messages []payload.Message
	if err := json.Unmarshal([]byte(raw), &messages); err != nil {
		t.Fatalf("decode messages: %v", err)
	}
	return messages
}

func decodeMessages(t *testing.T, raw string) []payload.Message {
	return decodeToolMessages(t, raw)
}

func TestValidateToolResultMessagesAcceptsMatchingPair(t *testing.T) {
	messages := decodeToolMessages(t, `[
		{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"call_1","content":"sunny"}
	]`)
	if err := validateToolResultMessages(messages); err != nil {
		t.Fatalf("matching tool result rejected: %v", err)
	}
}

func TestValidateToolResultMessagesRejectsInvalidIDs(t *testing.T) {
	cases := map[string]string{
		"missing":          `[{"role":"tool","content":"sunny"}]`,
		"unknown":          `[{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"weather","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_ghost","content":"sunny"}]`,
		"duplicate call":   `[{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"weather","arguments":"{}"}}]},{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"weather","arguments":"{}"}}]}]`,
		"duplicate result": `[{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"weather","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_1","content":"sunny"},{"role":"tool","tool_call_id":"call_1","content":"rainy"}]`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			err := validateToolResultMessages(decodeToolMessages(t, raw))
			if err == nil {
				t.Fatal("invalid tool history was accepted")
			}
			if name != "missing" && !strings.Contains(err.Error(), "call_") {
				t.Fatalf("error does not identify the call: %v", err)
			}
		})
	}
}

func TestValidateToolResultMessagesAllowsTrimmedHistory(t *testing.T) {
	messages := decodeToolMessages(t, `[{"role":"tool","tool_call_id":"call_old","content":"sunny"}]`)
	if err := validateToolResultMessages(messages); err != nil {
		t.Fatalf("trimmed history rejected: %v", err)
	}
}
