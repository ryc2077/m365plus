package payload

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestConversationPayloadUsesUploadedImageAnnotationOnly(t *testing.T) {
	messages := []Message{{
		Role:    "user",
		Content: "Analyze the attached image.",
		Images:  []ImageData{{Base64: "aW1hZ2U=", MediaType: "image/png", FileName: "upload.png"}},
		Annotations: []MessageAnnotation{{
			ID:                    "doc-image",
			MessageAnnotationType: "ImageFile",
			MessageAnnotationMetadata: map[string]string{
				"@type": "File", "annotationType": "File", "fileType": "png", "fileName": "upload.png",
			},
		}},
	}}

	raw, err := BuildConversationPayloadIncremental("0123456789abcdef0123456789abcdef", "session", messages, true, "Balanced", "", true, true, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatal(err)
	}
	arguments := envelope["arguments"].([]any)
	message := arguments[0].(map[string]any)["message"].(map[string]any)
	if _, exists := message["imageBase64"]; exists {
		t.Fatalf("imageBase64 must not be embedded after image upload: %#v", message["imageBase64"])
	}
	if _, exists := message["imageUrl"]; exists {
		t.Fatalf("imageUrl must not be embedded after image upload: %#v", message["imageUrl"])
	}
	connections, ok := message["connectedFederatedConnections"].([]any)
	if !ok || len(connections) != 1 || connections[0] != "dummyId" {
		t.Fatalf("connectedFederatedConnections = %#v", message["connectedFederatedConnections"])
	}
	annotations, ok := message["messageAnnotations"].([]any)
	if !ok || len(annotations) != 1 {
		t.Fatalf("messageAnnotations = %#v", message["messageAnnotations"])
	}
}

func TestBuildURLUsesEduStarterRoute(t *testing.T) {
	raw, _, _, err := BuildURL(
		"token",
		"0123456789abcdef0123456789abcdef",
		"",
		"user",
		"tenant",
	)
	if err != nil {
		t.Fatalf("BuildURL returned error: %v", err)
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("BuildURL returned invalid URL: %v", err)
	}
	query := parsed.Query()
	if got := query.Get("licenseType"); got != "Starter" {
		t.Fatalf("licenseType = %q, want Starter", got)
	}
	if got := query.Get("isEdu"); got != "true" {
		t.Fatalf("isEdu = %q, want true", got)
	}
	if got := query.Get("scenario"); got != "OfficeWebIncludedCopilot" {
		t.Fatalf("scenario = %q, want OfficeWebIncludedCopilot", got)
	}
}

func TestConversationTextForM365IncludesClientHistoryWhenRequested(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "Read nonce.txt."},
		{Role: "assistant", Content: "Tool call: read_nonce({})"},
		{Role: "user", Content: "Authoritative tool result: NONCE-EXACT"},
		{Role: "user", Content: "Return the exact tool result now."},
	}

	got := conversationTextForM365(messages, true)

	for _, expected := range []string{
		"CLIENT-PROVIDED CONVERSATION HISTORY",
		"Read nonce.txt.",
		"Tool call: read_nonce({})",
		"Authoritative tool result: NONCE-EXACT",
		"CURRENT USER MESSAGE",
		"Return the exact tool result now.",
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("flattened conversation lost %q:\n%s", expected, got)
		}
	}
}

func TestConversationTextForM365KeepsOnlyCurrentMessageForStickyConversation(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "Earlier user message"},
		{Role: "assistant", Content: "Earlier assistant message"},
		{Role: "user", Content: "Current request"},
	}

	got := conversationTextForM365(messages, false)

	if got != "Current request" {
		t.Fatalf("sticky conversation text = %q, want current request only", got)
	}
}
