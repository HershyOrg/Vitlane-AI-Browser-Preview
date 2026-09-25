package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 7-3c: the dashboard's host section comes from a file the host watch
// writes; the reader must treat a missing, corrupt or stale file as a
// display condition, never as an error that could taint the health report.
func TestReadHostFacts(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	directory := t.TempDir()
	path := filepath.Join(directory, "facts.json")

	if facts := readHostFacts("", now); facts != nil {
		t.Fatalf("empty path must omit the section, got %#v", facts)
	}
	if facts := readHostFacts(path, now); facts != nil {
		t.Fatalf("missing file must omit the section, got %#v", facts)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if facts := readHostFacts(path, now); facts != nil {
		t.Fatalf("corrupt file must omit the section, got %#v", facts)
	}
	if err := os.WriteFile(path, []byte(`{"diskUsedPct": 40}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if facts := readHostFacts(path, now); facts != nil {
		t.Fatalf("facts without generatedAt must be dropped, got %#v", facts)
	}

	fresh := `{
		"generatedAt": "2026-08-08T11:56:00Z",
		"diskUsedPct": 42, "memoryUsedPct": 61, "tlsDaysLeft": 55,
		"services": {
			"postgres": {"state": "running", "restartCount": 0},
			"vitlane": {"state": "running", "restartCount": 1}
		}
	}`
	if err := os.WriteFile(path, []byte(fresh), 0o644); err != nil {
		t.Fatal(err)
	}
	facts := readHostFacts(path, now)
	if facts == nil {
		t.Fatal("fresh facts must be returned")
	}
	if facts.Stale || facts.AgeSeconds != 240 {
		t.Fatalf("fresh facts marked stale=%v age=%d", facts.Stale, facts.AgeSeconds)
	}
	if facts.DiskUsedPct != 42 || facts.MemoryUsedPct != 61 {
		t.Fatalf("facts values lost: %#v", facts)
	}
	if facts.TLSDaysLeft == nil || *facts.TLSDaysLeft != 55 {
		t.Fatalf("tls days lost: %#v", facts.TLSDaysLeft)
	}
	if facts.Services["vitlane"].RestartCount != 1 {
		t.Fatalf("service facts lost: %#v", facts.Services)
	}

	stale := `{"generatedAt": "2026-08-08T11:30:00Z", "diskUsedPct": 42,
		"memoryUsedPct": 61, "tlsDaysLeft": null}`
	if err := os.WriteFile(path, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	facts = readHostFacts(path, now)
	if facts == nil || !facts.Stale {
		t.Fatalf("30-minute-old facts must be flagged stale, got %#v", facts)
	}
	if facts.TLSDaysLeft != nil {
		t.Fatalf("null tls days must stay null, got %#v", facts.TLSDaysLeft)
	}
	if facts.Services == nil {
		t.Fatal("services must never be a null map")
	}
}
