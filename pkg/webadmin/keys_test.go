package webadmin

import (
	"path/filepath"
	"testing"
)

func TestOpenAPIKeysUsesDataDirectory(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("M365_API_KEYS", "")
	t.Setenv("M365_DATA_DIR", dataDir)

	store := openAPIKeys()
	if want := filepath.Join(dataDir, "api-keys.json"); store.Path != want {
		t.Fatalf("API key path = %q, want %q", store.Path, want)
	}
	_, raw, err := store.create("restart-test")
	if err != nil {
		t.Fatal(err)
	}
	reopened := openAPIKeys()
	if !reopened.valid(raw) {
		t.Fatal("persisted API key was not loaded")
	}
}
