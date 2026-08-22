package client

import "testing"

func TestSnapshotDeltaEmitsFirstSnapshotAndExtensions(t *testing.T) {
	if chunk, advanced := snapshotDelta("", "A **"); !advanced || chunk != "A **" {
		t.Fatalf("first snapshot: chunk=%q advanced=%v", chunk, advanced)
	}
	if chunk, advanced := snapshotDelta("As of today, ", "As of today, Go is current."); !advanced || chunk != "Go is current." {
		t.Fatalf("extension: chunk=%q advanced=%v", chunk, advanced)
	}
}

func TestSnapshotDeltaDropsRepeatedAndReencodedSnapshots(t *testing.T) {
	if chunk, advanced := snapshotDelta("same", "same"); advanced || chunk != "" {
		t.Fatalf("repeated snapshot: chunk=%q advanced=%v", chunk, advanced)
	}
	emitted := "Go [1](https://go.dev) is current."
	snapshot := "Go citation-marker is current."
	if chunk, advanced := snapshotDelta(emitted, snapshot); advanced || chunk != "" {
		t.Fatalf("reencoded snapshot: chunk=%q advanced=%v", chunk, advanced)
	}
}
