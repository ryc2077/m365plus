package servers

import (
	"strings"
	"testing"

	"github.com/ryc2077/m365plus/pkg/client"
)

func TestUploadFailureMessageNamesImageUpload(t *testing.T) {
	err := &client.UpstreamError{Op: "upload", Status: 502}
	_, _, message := streamErrorFields("image upload", err)
	if !strings.Contains(strings.ToLower(message), "image upload") {
		t.Fatalf("message = %q, want image upload context", message)
	}
}
