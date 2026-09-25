package postgres

import (
	"encoding/json"
	"strings"
	"testing"

	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
)

func TestDecodeSessionRestoresOwnerOutsideImmutableSnapshot(t *testing.T) {
	payload, err := json.Marshal(agencydomain.OrderSheetSession{
		ID: "sheet-1", UserID: "must-not-be-in-snapshot", Version: 1,
		State: agencydomain.SessionEditing,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) == "" || json.Valid(payload) == false {
		t.Fatal("invalid fixture payload")
	}
	if strings.Contains(string(payload), "must-not-be-in-snapshot") {
		t.Fatal("user ID must not be duplicated into the immutable JSON snapshot")
	}
	session, err := decodeSession(payload, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if session.UserID != "user-1" {
		t.Fatalf("restored owner=%q", session.UserID)
	}
}
