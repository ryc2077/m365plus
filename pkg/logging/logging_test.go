package logging

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitUsesConfiguredDataDirectory(t *testing.T) {
	Close()
	t.Cleanup(Close)

	dataDir := t.TempDir()
	t.Setenv("M365_DATA_DIR", dataDir)

	if err := Init(LevelInfo); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	path := filepath.Join(dataDir, logFileName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("configured log file was not created at %s: %v", path, err)
	}
}
