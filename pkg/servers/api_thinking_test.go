package servers

import (
	"os"
	"regexp"
	"testing"
)

func TestAnthropicThinkingBlocksCarryASignature(t *testing.T) {
	source, err := os.ReadFile("api.go")
	if err != nil {
		t.Fatalf("read api.go: %v", err)
	}

	blocks := regexp.MustCompile(`map\[string\]any\{"type": "thinking"[^}]*\}`).FindAllString(string(source), -1)
	if len(blocks) == 0 {
		t.Fatal("no thinking blocks found")
	}
	for _, block := range blocks {
		if !regexp.MustCompile(`"signature"`).MatchString(block) {
			t.Fatalf("thinking block without a signature field: %s", block)
		}
	}
}
