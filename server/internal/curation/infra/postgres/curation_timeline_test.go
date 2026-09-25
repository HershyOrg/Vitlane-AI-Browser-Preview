package postgres

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCurationTimelineProjectionUsesAllowlistedOwnerFields(
	t *testing.T,
) {
	required := []string{
		"FROM shopping_plans AS plan",
		"FROM curation_runs AS run",
		"target.created_by_curation_run_id=run.id",
		"job.curation_action_id=action.id",
		"FROM research_feedback AS feedback",
	}
	for _, fragment := range required {
		if !strings.Contains(
			curationTimelineProjectionQuery,
			fragment,
		) {
			t.Fatalf("projection query omitted %q", fragment)
		}
	}
	forbidden := []string{
		"input_snapshot",
		"candidate_configuration_id",
		"configuration_hash",
		"request_hash",
		"activation_ref_ciphertext",
		"shipping_snapshot",
		"'TARGET_REMOVE'",
		"'CANDIDATE_INTERACTION'",
		"'SELECTION_MUTATION'",
		"candidate_interaction_events",
		"curation_selection_commands",
		"purchase_origin_requests",
		"'PURCHASE_DIRECT'",
		"'PURCHASE_CART'",
	}
	for _, fragment := range forbidden {
		if strings.Contains(
			curationTimelineProjectionQuery,
			fragment,
		) {
			t.Fatalf(
				"projection query selected forbidden owner field %q",
				fragment,
			)
		}
	}
}

func TestScanCurationTimelineItemProducesRedactedTypedResult(
	t *testing.T,
) {
	now := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	row := timelineTestScanner{values: []any{
		"11111111-1111-4111-8111-111111111111",
		"22222222-2222-4222-8222-222222222222",
		"TARGET_RESEARCH_AGAIN",
		"CURATING",
		sql.NullString{},
		"TARGET",
		sql.NullString{
			String: "33333333-3333-4333-8333-333333333333",
			Valid:  true,
		},
		"INTELLIGENCE",
		int64(3),
		now,
		sql.NullString{String: "더 가벼운 후보", Valid: true},
		sql.NullString{String: "TARGET_RESEARCHED", Valid: true},
		sql.NullString{
			String: "의자 Target 재조사에서 후보 2개를 반영했습니다.",
			Valid:  true,
		},
		sql.NullTime{Time: now.Add(time.Minute), Valid: true},
		`["후보 A","후보 B"]`,
		`["의자"]`,
		`[]`,
	}}
	item, err := scanCurationTimelineItem(row)
	if err != nil {
		t.Fatal(err)
	}
	if item.DisplayBody != "더 가벼운 후보" ||
		item.Result == nil ||
		len(item.Result.Diff.Added) != 2 ||
		len(item.Result.Diff.Changed) != 1 {
		t.Fatalf("item=%#v", item)
	}
	payload, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"actorUserId",
		"sourceRef",
		"configuration",
		"requestHash",
	} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf(
				"timeline JSON leaked %q: %s",
				forbidden,
				payload,
			)
		}
	}
}

type timelineTestScanner struct {
	values []any
}

func (scanner timelineTestScanner) Scan(destinations ...any) error {
	for index, value := range scanner.values {
		destination := reflect.ValueOf(destinations[index]).Elem()
		source := reflect.ValueOf(value)
		if source.Type().AssignableTo(destination.Type()) {
			destination.Set(source)
			continue
		}
		destination.Set(source.Convert(destination.Type()))
	}
	return nil
}
