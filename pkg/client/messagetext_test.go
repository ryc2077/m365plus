package client

import "testing"

func TestCarriesAnswerTextFiltersBackendMessages(t *testing.T) {
	if !carriesAnswerText(map[string]any{"text": "answer"}) {
		t.Fatal("plain assistant message was rejected")
	}
	for _, messageType := range []string{"Progress", "InternalSearchQuery", "GeneratedCode", "TriggerPlugin", "InvokeAction"} {
		if carriesAnswerText(map[string]any{"messageType": messageType, "text": "internal"}) {
			t.Fatalf("backend message %q was treated as answer text", messageType)
		}
	}
	if !carriesAnswerText(map[string]any{"messageType": "UnknownFutureType", "text": "answer"}) {
		t.Fatal("unknown message type was rejected")
	}
}
