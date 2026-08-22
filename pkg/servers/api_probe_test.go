package servers

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ryc2077/m365plus/pkg/payload"
)

func TestResponsesInputIsEmptyRecognizesProbe(t *testing.T) {
	if !responsesInputIsEmpty(responsesInputToMessages([]any{})) {
		t.Fatal("empty input was not recognized as a probe")
	}
	if !responsesInputIsEmpty([]payload.Message{{Role: "user", Content: " \n\t "}}) {
		t.Fatal("whitespace input was not recognized as a probe")
	}
	if responsesInputIsEmpty([]payload.Message{{Role: "user", Images: []payload.ImageData{{MediaType: "image/png", Base64: "AAAA"}}}}) {
		t.Fatal("image input was mistaken for a probe")
	}
}

func TestRespondResponsesProbeWithoutUpstream(t *testing.T) {
	api := &APIServer{}
	recorder := httptest.NewRecorder()
	api.respondResponsesProbe(recorder, "gpt-5.5-reasoning", false)
	if recorder.Code != 200 {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode probe response: %v", err)
	}
	if body["object"] != "response" || body["status"] != "completed" {
		t.Fatalf("unexpected probe response: %#v", body)
	}
}

func TestRespondResponsesProbeStreamsLifecycle(t *testing.T) {
	api := &APIServer{}
	recorder := httptest.NewRecorder()
	api.respondResponsesProbe(recorder, "gpt-5.5-reasoning", true)
	body := recorder.Body.String()
	for _, event := range []string{"response.created", "response.in_progress", "response.completed"} {
		if !strings.Contains(body, event) {
			t.Fatalf("probe stream omits %s: %s", event, body)
		}
	}
	if !strings.Contains(body, `"sequence_number":0`) || !strings.Contains(body, `"sequence_number":2`) {
		t.Fatalf("probe stream lost sequence numbers: %s", body)
	}
}
