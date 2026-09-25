package http

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

func TestAmazonPurchaseCheckForwardsSnapshot(t *testing.T) {
	fake := &amazonHTTPFake{}
	h := NewLiveCatalogReviewHandlerV2(fake)
	body := `{"variantRef":{"source":"AMAZON","marketplace":"US","asin":"B012345678"},"checked":true,"expectedVersion":0,"snapshot":{"productTitle":"Wireless Headset","variantTitle":"Black","merchant":"Amazon","priceMinor":12999,"priceUnknown":false,"currency":"USD"}}`
	r := catalogWorkspaceHTTPRequestV2(body, "command-1")
	r.SetPathValue("candidateId", "candidate-1")
	r.SetPathValue("amazonAction", "purchase-check")
	w := httptest.NewRecorder()
	h.AmazonCandidate(w, r)
	if w.Code != 200 || fake.calls != 1 || fake.input.Snapshot == nil || fake.input.Snapshot.ProductTitle != "Wireless Headset" || fake.input.Snapshot.VariantTitle != "Black" || fake.input.Snapshot.PriceMinor != 12999 || fake.input.Snapshot.Currency != "USD" || fake.input.Snapshot.PriceUnknown {
		t.Fatalf("snapshot forwarding: %d %#v", w.Code, fake.input.Snapshot)
	}
	// The strict decoder rejects unknown fields; a snapshot-less body forwards nil.
	r = catalogWorkspaceHTTPRequestV2(body[:strings.Index(body, `,"snapshot"`)]+"}", "command-2")
	r.SetPathValue("candidateId", "candidate-1")
	r.SetPathValue("amazonAction", "purchase-check")
	w = httptest.NewRecorder()
	h.AmazonCandidate(w, r)
	if w.Code != 200 || fake.calls != 2 || fake.input.Snapshot != nil {
		t.Fatalf("snapshot-less command must forward nil: %d %#v", w.Code, fake.input.Snapshot)
	}
}

type purchaseCheckHTTPRepository struct {
	researchapp.Repository
	rows  []researchdomain.PurchaseCheck
	user  string
	limit int
}

func (r *purchaseCheckHTTPRepository) ListPurchaseChecks(_ context.Context, user string, limit int) ([]researchdomain.PurchaseCheck, error) {
	r.user, r.limit = user, limit
	return r.rows, nil
}

func TestListPurchaseChecksHTTP(t *testing.T) {
	recorded := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	repository := &purchaseCheckHTTPRepository{rows: []researchdomain.PurchaseCheck{{
		CurationID: "c1", TargetID: "t1", TargetTitle: "헤드폰", CandidateID: "cand",
		VariantRef: &researchdomain.SourceVariantRef{Source: researchdomain.SourceAmazon, Marketplace: "US", ASIN: "B012345678"},
		ProductURL: "https://www.amazon.com/dp/B012345678", Checked: true, Version: 2, RecordedAt: recorded, Evidence: "SELF_REPORTED",
		Snapshot: &researchdomain.PurchaseCheckSnapshot{ProductTitle: "Wireless Headset", PriceMinor: 12999, Currency: "USD"}, SnapshotAt: &recorded,
	}, {
		CurationID: "c1", CandidateID: "legacy",
		VariantRef: &researchdomain.SourceVariantRef{Source: researchdomain.SourceAmazon, Marketplace: "US", ASIN: "B000000001"},
		ProductURL: "https://www.amazon.com/dp/B000000001", Checked: true, Version: 1, RecordedAt: recorded, Evidence: "SELF_REPORTED",
	}}}
	handler := NewHandler(researchapp.NewService(repository, nil, nil, nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil))))
	anonymous := httptest.NewRequest(http.MethodGet, "/api/v1/account/purchase-checks", nil)
	w := httptest.NewRecorder()
	handler.ListPurchaseChecks(w, anonymous)
	if w.Code != 401 || repository.limit != 0 {
		t.Fatalf("anonymous list: %d", w.Code)
	}
	r := anonymous.WithContext(sharedapp.WithAuthenticatedUserID(anonymous.Context(), "user-1"))
	w = httptest.NewRecorder()
	handler.ListPurchaseChecks(w, r)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || repository.user != "user-1" || repository.limit != 50 {
		t.Fatalf("owner list: %d %q user=%s limit=%d", w.Code, w.Header().Get("Cache-Control"), repository.user, repository.limit)
	}
	var body struct {
		SchemaVersion string            `json:"schemaVersion"`
		Records       []json.RawMessage `json:"records"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.SchemaVersion != "vitlane.account-purchase-checks.v1" || len(body.Records) != 2 {
		t.Fatalf("list body: %s %v", w.Body.String(), err)
	}
	first, second := string(body.Records[0]), string(body.Records[1])
	for _, fragment := range []string{`"productUrl":"https://www.amazon.com/dp/B012345678"`, `"targetTitle":"헤드폰"`, `"snapshot":{"productTitle":"Wireless Headset","priceMinor":12999,"priceUnknown":false,"currency":"USD"}`, `"evidence":"SELF_REPORTED"`, `"snapshotAt":"2026-09-14T12:00:00Z"`} {
		if !strings.Contains(first, fragment) {
			t.Fatalf("record missing %s: %s", fragment, first)
		}
	}
	if !strings.Contains(second, `"snapshot":null`) || strings.Contains(second, "snapshotAt") || strings.Contains(second, "targetTitle") {
		t.Fatalf("legacy record must expose a null snapshot only: %s", second)
	}
}
