package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

// A Round's collected source result is kept in process memory so an automatic
// retry of the same Round evaluates what an earlier attempt already paid for,
// instead of calling every catalog source again. The typical case is an
// evaluation call that failed or ran out of the attempt deadline after the
// sources had answered.
//
// Nothing here reaches the database. Raw provider titles, prices, media and
// messages stay response-scoped (GAP-037): an entry serves only the Round that
// collected it, only for the identical search input, at most once, and only
// inside a short retry window. A retry that lands on another process, arrives
// after the window, or searches with a different input collects again.
const (
	observationCheckpointTTL     = 15 * time.Minute
	observationCheckpointMaximum = 32
)

type observationCheckpoint struct {
	inputHash string
	// payload is the JSON snapshot taken right after collection. Decoding it
	// yields a copy the later steps may mutate freely.
	payload []byte
	savedAt time.Time
}

type observationCheckpoints struct {
	mu      sync.Mutex
	entries map[string]observationCheckpoint
}

func newObservationCheckpoints() *observationCheckpoints {
	return &observationCheckpoints{entries: map[string]observationCheckpoint{}}
}

// observationInputHash names everything that decides what the sources would
// return: the whole search input and the Target profile. Pool version and
// idempotency key are cleared on purpose, because every attempt has its own
// and duplicates are removed against the current pool after the checkpoint is
// read. New input fields join the hash without edits here.
func observationInputHash(input CatalogWorkspaceSearchInputV2, profile CatalogTargetSearchProfileV2) string {
	input.ExpectedPoolVersion = 0
	input.IdempotencyKey = ""
	raw, err := json.Marshal(struct {
		Input   CatalogWorkspaceSearchInputV2
		Profile CatalogTargetSearchProfileV2
	}{input, profile})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (c *observationCheckpoints) put(roundID, inputHash string, result LiveCatalogReviewResultV2, now time.Time) {
	if c == nil || roundID == "" || inputHash == "" {
		return
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, entry := range c.entries {
		if now.Sub(entry.savedAt) >= observationCheckpointTTL {
			delete(c.entries, id)
		}
	}
	if _, replacing := c.entries[roundID]; !replacing && len(c.entries) >= observationCheckpointMaximum {
		oldest := ""
		for id, entry := range c.entries {
			if oldest == "" || entry.savedAt.Before(c.entries[oldest].savedAt) {
				oldest = id
			}
		}
		delete(c.entries, oldest)
	}
	c.entries[roundID] = observationCheckpoint{inputHash: inputHash, payload: payload, savedAt: now}
}

// take returns the Round's checkpoint and removes it. A second retry of the
// same Round therefore collects fresh observations instead of evaluating the
// same set a third time.
func (c *observationCheckpoints) take(roundID, inputHash string, now time.Time) (LiveCatalogReviewResultV2, bool) {
	if c == nil || roundID == "" || inputHash == "" {
		return LiveCatalogReviewResultV2{}, false
	}
	c.mu.Lock()
	entry, ok := c.entries[roundID]
	delete(c.entries, roundID)
	c.mu.Unlock()
	if !ok || entry.inputHash != inputHash || now.Sub(entry.savedAt) >= observationCheckpointTTL {
		return LiveCatalogReviewResultV2{}, false
	}
	var result LiveCatalogReviewResultV2
	if err := json.Unmarshal(entry.payload, &result); err != nil {
		return LiveCatalogReviewResultV2{}, false
	}
	return result, true
}

func (c *observationCheckpoints) drop(roundID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(c.entries, roundID)
	c.mu.Unlock()
}
