package webadmin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryc2077/m365plus/pkg/servers"
)

func TestRecordUsagePersistsAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	usage := &usageLog{Path: path}
	usage.persist = &persistStore{flush: usage.flush}
	server := &Server{usage: usage}

	server.RecordUsage(servers.UsageRecord{
		Time:         time.Now(),
		APIKeyPrefix: "m365_ab",
		AccountEmail: "user@example.com",
		Model:        "gpt5.5",
		Endpoint:     "/v1/chat/completions",
		InputTokens:  12,
		OutputTokens: 7,
		DurationMs:   25,
		Status:       200,
	})
	if err := usage.persist.flushNowBlocking(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var persisted UsageRecord
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Model != "gpt5.5" || persisted.InputTokens != 12 || persisted.OutputTokens != 7 {
		t.Fatalf("unexpected persisted record: %#v", persisted)
	}

	reloaded := &usageLog{Path: path}
	reloaded.load()
	logs := reloaded.logs(10, 0)
	if logs["total"] != 1 {
		t.Fatalf("reloaded total = %#v, want 1", logs["total"])
	}
}
