package main

import (
	"encoding/json"
	"os"
	"time"
)

// opsHostFacts is the L3 slice of the ops dashboard: host-level facts the
// container cannot observe itself (root filesystem usage, host memory,
// container restart counts, TLS expiry). The host watch timer writes them to
// a file on every run and the container reads that file through a read-only
// bind mount, so no new listener, credential or privilege appears
// (ADR-0040 §6). Thresholds keep living in the watch script; this section is
// display-only.
type opsHostFacts struct {
	GeneratedAt   time.Time                 `json:"generatedAt"`
	DiskUsedPct   int64                     `json:"diskUsedPct"`
	MemoryUsedPct int64                     `json:"memoryUsedPct"`
	TLSDaysLeft   *int64                    `json:"tlsDaysLeft"`
	Services      map[string]opsHostService `json:"services"`
	// AgeSeconds and Stale are computed at read time. A stale facts file
	// means the watch timer itself stopped, which alert A-04 (missing watch
	// heartbeat) already owns; the dashboard just labels the data as old.
	AgeSeconds int64 `json:"ageSeconds"`
	Stale      bool  `json:"stale"`
}

type opsHostService struct {
	State        string `json:"state"`
	RestartCount int64  `json:"restartCount"`
}

// opsHostFactsMaxAge marks facts older than three watch intervals as stale.
const opsHostFactsMaxAge = 15 * time.Minute

// readHostFacts returns nil when the deployment has no facts file (dev, E2E)
// or the file is unreadable; the host section is simply omitted then. A
// corrupt or stale file is never an error condition here — the watch layer
// owns alerting on itself.
func readHostFacts(path string, now time.Time) *opsHostFacts {
	if path == "" {
		return nil
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var facts opsHostFacts
	if err := json.Unmarshal(payload, &facts); err != nil {
		return nil
	}
	if facts.GeneratedAt.IsZero() {
		return nil
	}
	age := now.Sub(facts.GeneratedAt)
	if age < 0 {
		age = 0
	}
	facts.AgeSeconds = int64(age / time.Second)
	facts.Stale = age > opsHostFactsMaxAge
	if facts.Services == nil {
		facts.Services = map[string]opsHostService{}
	}
	return &facts
}
