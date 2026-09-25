package app

import (
	"reflect"
	"testing"
	"time"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

func checkpointFixture() LiveCatalogReviewResultV2 {
	observed := time.Date(2026, 9, 16, 1, 2, 3, 0, time.UTC)
	ref := researchdomain.SourceProductRef{Source: researchdomain.SourceKurly, ProductID: "5051234"}
	return LiveCatalogReviewResultV2{
		NextProgress: SourceProgressSet{"KURLY_JSON": {Query: "우유", Page: 2}},
		Search: CatalogProductSearchResult{
			Provider: "KOREAN", Outcome: CatalogOutcome("SUCCEEDED"),
			Products: []CatalogProductObservation{{
				ExternalObservation: &researchdomain.ExternalProductObservation{
					SchemaVersion: "vitlane.external-product-observation.v1", ProductRef: ref,
					ProductURL: "https://www.kurly.com/goods/5051234", Title: "우유 900ml",
					ImageURL: "https://img.example/milk.jpg", ObservedAt: observed,
				},
				SourceProductRef:  &ref,
				ProviderProductID: "KURLY:5051234",
				Title:             "우유 900ml",
				Media:             []CatalogMedia{{URL: "https://img.example/milk.jpg"}},
				Categories:        []CatalogCategory{{Value: "Dairy"}},
				ProviderOrder:     1,
			}},
			Messages: []CatalogProviderMessage{{Type: "info", Code: "PARTIAL"}},
		},
		Metrics: LiveCatalogReviewMetricsV2{
			SourceCoverage: []SourceCoverage{{Source: "KURLY", Status: "SUCCEEDED", CandidateCount: 1}},
			StartedAt:      observed, CompletedAt: observed.Add(4 * time.Second), Duration: 4 * time.Second,
		},
		DiscardedNoLocatorCount: 1,
	}
}

func TestObservationCheckpointServesOneRetryOfTheSameRoundAndInput(t *testing.T) {
	checkpoints := newObservationCheckpoints()
	now := time.Date(2026, 9, 16, 1, 0, 0, 0, time.UTC)
	collected := checkpointFixture()
	checkpoints.put("round-1", "hash-a", collected, now)

	if _, ok := checkpoints.take("round-2", "hash-a", now); ok {
		t.Fatal("another Round read this Round's observations")
	}
	if _, ok := checkpoints.take("round-1", "hash-b", now.Add(time.Minute)); ok {
		t.Fatal("a different search input reused the observations")
	}
	// The mismatched read consumed the entry: a changed input always collects.
	checkpoints.put("round-1", "hash-a", collected, now)
	got, ok := checkpoints.take("round-1", "hash-a", now.Add(time.Minute))
	if !ok || !reflect.DeepEqual(got, collected) {
		t.Fatalf("retry read ok=%v\n got=%+v\nwant=%+v", ok, got, collected)
	}
	if _, ok := checkpoints.take("round-1", "hash-a", now.Add(2*time.Minute)); ok {
		t.Fatal("a second retry evaluated the same observations again")
	}
}

func TestObservationCheckpointExpiresEvictsAndDrops(t *testing.T) {
	checkpoints := newObservationCheckpoints()
	now := time.Date(2026, 9, 16, 1, 0, 0, 0, time.UTC)
	checkpoints.put("stale", "h", checkpointFixture(), now)
	if _, ok := checkpoints.take("stale", "h", now.Add(observationCheckpointTTL)); ok {
		t.Fatal("expired observations were reused")
	}

	for i := 0; i < observationCheckpointMaximum+1; i++ {
		checkpoints.put("round-"+string(rune('A'+i)), "h", checkpointFixture(), now.Add(time.Duration(i)*time.Second))
	}
	if len(checkpoints.entries) != observationCheckpointMaximum {
		t.Fatalf("entries=%d, want bounded to %d", len(checkpoints.entries), observationCheckpointMaximum)
	}
	if _, ok := checkpoints.entries["round-A"]; ok {
		t.Fatal("the oldest checkpoint survived eviction")
	}
	// Saving later purges whatever outlived the window.
	checkpoints.put("late", "h", checkpointFixture(), now.Add(time.Hour))
	if len(checkpoints.entries) != 1 {
		t.Fatalf("expired entries kept after a later save: %d", len(checkpoints.entries))
	}
	checkpoints.drop("late")
	if len(checkpoints.entries) != 0 {
		t.Fatal("finalized Round kept its checkpoint")
	}

	var absent *observationCheckpoints
	absent.put("round", "h", checkpointFixture(), now)
	if _, ok := absent.take("round", "h", now); ok {
		t.Fatal("a Service without checkpoints reused observations")
	}
	absent.drop("round")
}

func TestObservationCheckpointCopyIsIndependentOfLaterSteps(t *testing.T) {
	checkpoints := newObservationCheckpoints()
	now := time.Date(2026, 9, 16, 1, 0, 0, 0, time.UTC)
	collected := checkpointFixture()
	checkpoints.put("round", "h", collected, now)
	// Evaluation and finalize mutate the live result in place.
	collected.Search.Products[0].Title = "changed"
	collected.Search.Products[0].ExternalObservation.Title = "changed"
	collected.NextProgress["KURLY_JSON"] = SourceSearchProgress{Query: "우유", Page: 9}
	collected.Metrics.SourceCoverage[0].CandidateCount = 0

	got, ok := checkpoints.take("round", "h", now)
	if !ok || got.Search.Products[0].Title != "우유 900ml" ||
		got.Search.Products[0].ExternalObservation.Title != "우유 900ml" ||
		got.NextProgress["KURLY_JSON"].Page != 2 || got.Metrics.SourceCoverage[0].CandidateCount != 1 {
		t.Fatalf("checkpoint shared memory with the live result: %+v", got)
	}
}

func TestObservationInputHashIgnoresPerAttemptFieldsOnly(t *testing.T) {
	minimum := shareddomain.Money{Amount: "10000", Currency: "KRW"}
	input := CatalogWorkspaceSearchInputV2{
		UserID: "u", CurationID: "c", TargetID: "t", Mode: CatalogResearchAppendV2,
		ProductVertical: "FOOD", QuerySeeds: []string{"우유"}, ExpectedPoolVersion: 3, IdempotencyKey: "attempt-1",
		Search: LiveCatalogReviewSearchInputV2{Query: "우유", Limit: 16, Country: "KR", Currency: "KRW"},
	}
	profile := CatalogTargetSearchProfileV2{TargetID: "t", NormalizedIntent: "우유", MinimumPrice: &minimum}
	base := observationInputHash(input, profile)
	if base == "" {
		t.Fatal("empty hash")
	}
	retry := input
	retry.ExpectedPoolVersion, retry.IdempotencyKey = 4, "attempt-2"
	if observationInputHash(retry, profile) != base {
		t.Fatal("a new attempt of the same Round changed the input hash")
	}
	for name, change := range map[string]func(*CatalogWorkspaceSearchInputV2, *CatalogTargetSearchProfileV2){
		"query":    func(i *CatalogWorkspaceSearchInputV2, _ *CatalogTargetSearchProfileV2) { i.Search.Query = "두유" },
		"vertical": func(i *CatalogWorkspaceSearchInputV2, _ *CatalogTargetSearchProfileV2) { i.ProductVertical = "GENERAL" },
		"progress": func(i *CatalogWorkspaceSearchInputV2, _ *CatalogTargetSearchProfileV2) {
			i.Progress = SourceProgressSet{"KURLY_JSON": {Query: "우유", Page: 2}}
		},
		"price": func(_ *CatalogWorkspaceSearchInputV2, p *CatalogTargetSearchProfileV2) {
			p.MinimumPrice = &shareddomain.Money{Amount: "20000", Currency: "KRW"}
		},
	} {
		nextInput, nextProfile := input, profile
		change(&nextInput, &nextProfile)
		if observationInputHash(nextInput, nextProfile) == base {
			t.Fatalf("changing %s kept the same hash", name)
		}
	}
}

// Every type the checkpoint snapshots must survive a JSON round trip. An
// unexported or interface field would be dropped silently on a retry.
func TestObservationCheckpointTypesAreFullySerializable(t *testing.T) {
	seen := map[reflect.Type]bool{}
	var walk func(reflect.Type, string)
	walk = func(typ reflect.Type, path string) {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array || typ.Kind() == reflect.Map {
			if typ.Kind() == reflect.Map && typ.Key().Kind() != reflect.String {
				t.Fatalf("%s has a non-string map key", path)
			}
			typ = typ.Elem()
		}
		if seen[typ] || typ == reflect.TypeOf(time.Time{}) {
			return
		}
		seen[typ] = true
		switch typ.Kind() {
		case reflect.Interface, reflect.Func, reflect.Chan, reflect.UnsafePointer:
			t.Fatalf("%s is %s and cannot be snapshotted", path, typ.Kind())
		case reflect.Struct:
			for i := 0; i < typ.NumField(); i++ {
				field := typ.Field(i)
				if !field.IsExported() {
					t.Fatalf("%s.%s is unexported and would be lost", path, field.Name)
				}
				if field.Tag.Get("json") == "-" {
					t.Fatalf("%s.%s is excluded from JSON and would be lost", path, field.Name)
				}
				walk(field.Type, path+"."+field.Name)
			}
		}
	}
	walk(reflect.TypeOf(LiveCatalogReviewResultV2{}), "LiveCatalogReviewResultV2")
}
