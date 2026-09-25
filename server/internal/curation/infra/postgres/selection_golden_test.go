package postgres

import (
	"encoding/json"
	"os"
	"testing"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
)

type selectionHashGoldenFixture struct {
	Schema       string `json:"schema"`
	SnapshotHash string `json:"snapshotHash"`
	Value        struct {
		Schema   string `json:"schema"`
		Snapshot struct {
			CandidateConfigurationHash string `json:"candidateConfigurationHash"`
			CandidateConfigurationID   string `json:"candidateConfigurationId"`
			CandidateID                string `json:"candidateId"`
			CurationID                 string `json:"curationId"`
			Quantity                   int64  `json:"quantity"`
			SelectionID                string `json:"selectionId"`
			ShoppingSessionID          string `json:"shoppingSessionId"`
			TargetID                   string `json:"targetId"`
			Version                    int64  `json:"version"`
		} `json:"snapshot"`
	} `json:"value"`
}

func TestSelectionSnapshotHashMatchesSharedGoldenFixture(t *testing.T) {
	t.Parallel()

	payload, err := os.ReadFile(
		"../../../../../shared/openapi/fixtures/" +
			"curation-selection-snapshot-hash.v1.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	var fixture selectionHashGoldenFixture
	if err := json.Unmarshal(payload, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Schema != "CurationSelectionSnapshotHashFixture.v1" ||
		fixture.Value.Schema != "CurationSelectionSnapshotHash.v1" {
		t.Fatalf(
			"unexpected schemas %q %q",
			fixture.Schema,
			fixture.Value.Schema,
		)
	}
	snapshot := fixture.Value.Snapshot
	got, err := selectionSnapshotHash(curationdomain.CurationSelection{
		ID:                         snapshot.SelectionID,
		CurationID:                 snapshot.CurationID,
		PlanTargetID:               snapshot.TargetID,
		ShoppingSessionID:          snapshot.ShoppingSessionID,
		CandidateID:                snapshot.CandidateID,
		CandidateConfigurationID:   snapshot.CandidateConfigurationID,
		CandidateConfigurationHash: snapshot.CandidateConfigurationHash,
		Quantity:                   snapshot.Quantity,
		Version:                    snapshot.Version,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != fixture.SnapshotHash {
		t.Fatalf("hash=%q want=%q", got, fixture.SnapshotHash)
	}
}
