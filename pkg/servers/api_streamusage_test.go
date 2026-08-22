package servers

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIncludeStreamUsageDefaultsToSending(t *testing.T) {
	if !includeStreamUsage(nil) || !includeStreamUsage(&streamOptions{}) {
		t.Fatal("absent include_usage must preserve usage output")
	}
	enabled := true
	if !includeStreamUsage(&streamOptions{IncludeUsage: &enabled}) {
		t.Fatal("include_usage true withheld usage")
	}
	disabled := false
	if includeStreamUsage(&streamOptions{IncludeUsage: &disabled}) {
		t.Fatal("include_usage false still enabled usage")
	}
}

func TestStreamOptionsDecodesIncludeUsage(t *testing.T) {
	var body struct {
		StreamOptions *streamOptions `json:"stream_options"`
	}
	if err := json.Unmarshal([]byte(`{"stream_options":{"include_usage":false}}`), &body); err != nil {
		t.Fatal(err)
	}
	if includeStreamUsage(body.StreamOptions) {
		t.Fatal("decoded include_usage false was ignored")
	}
}

func TestSSEDoneOmitsNilUsage(t *testing.T) {
	recorder := httptest.NewRecorder()
	(&APIServer{}).sendSSEDone(recorder, "chatcmpl-1", "gpt-test", "stop", nil)
	body := recorder.Body.String()
	if strings.Contains(body, `"usage"`) {
		t.Fatalf("nil usage reached the wire: %s", body)
	}
	if !strings.Contains(body, `"finish_reason":"stop"`) {
		t.Fatalf("finish reason missing: %s", body)
	}
}
