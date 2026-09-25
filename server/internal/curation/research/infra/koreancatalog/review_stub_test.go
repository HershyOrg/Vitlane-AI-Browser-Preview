package koreancatalog

import (
	"context"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"strings"
	"testing"
	"time"
)

type reviewControl struct {
	memoryControl
	off map[string]bool
}

func (c *reviewControl) ReadProviderUsage(_ context.Context, id string) (researchapp.CatalogProviderUsage, error) {
	d, _ := researchapp.CatalogAPIDefinitionFor(id)
	return researchapp.CatalogProviderUsage{CatalogAPIDefinition: d, Control: researchapp.AmazonSourceControl{Enabled: !c.off[id]}, Quota: &researchapp.CatalogProviderQuota{Remaining: 100, ResetAt: time.Now().Add(time.Hour), ObservedAt: time.Now()}}, nil
}
func (c *reviewControl) ReserveProviderCall(_ context.Context, id, _ string, _ time.Time) (string, error) {
	if c.off[id] {
		return "", safeFailure("CATALOG_API_DISABLED")
	}
	return "call", nil
}
func (c *reviewControl) SaveProviderQuota(context.Context, string, researchapp.CatalogAPIQuota, time.Time) error {
	return nil
}
func TestReviewStubBoundariesAndFallbacks(t *testing.T) {
	for _, env := range []string{"production", "", "staging"} {
		if _, err := NewReviewStub(env, &reviewControl{}); err == nil {
			t.Fatal("unsafe environment accepted")
		}
	}
	for _, tc := range []struct {
		name, query, provider, price string
		off                          map[string]bool
		count                        int
	}{
		{"naver", "라미 사파리", "NAVER API HUB", "OBSERVED", nil, 2},
		{"coupang", "갤럭시 버즈", "OpenWebNinja", "OBSERVED", nil, 1},
		{"web fallback", "lamy pen", "OpenWebNinja", "OBSERVED", map[string]bool{"NAVER_WEBKR": true, "SERP_GOOGLE": true}, 2},
		{"serp fallback", "lamy pen", "SerpApi", "UNKNOWN", map[string]bool{"NAVER_WEBKR": true, "OWN_WEB": true}, 2},
		{"detail disabled", "라미 사파리", "NAVER API HUB", "UNKNOWN", map[string]bool{"ELEVENST_HTML": true}, 2},
		{"unknown query", "unrelated fixture", "", "", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			control := &reviewControl{off: tc.off}
			g, err := NewReviewStub("test", control)
			if err != nil {
				t.Fatal(err)
			}
			got, err := g.SearchExternalMalls(context.Background(), researchapp.KoreanSearchRequest{Query: tc.query, Country: "KR"})
			if err != nil || len(got.Observations) != tc.count {
				t.Fatalf("observations %d err %v", len(got.Observations), err)
			}
			for _, o := range got.Observations {
				if o.Validate() != nil || o.Provenance.APIProvider != tc.provider || o.Seller.Kind != "UNKNOWN" || o.ObservedAt.Year() != 2026 {
					t.Fatalf("invalid observation %+v", o)
				}
			}
			if tc.count > 0 && got.Observations[tc.count-1].Price.Kind != tc.price {
				t.Fatal("price meaning changed")
			}
			if _, err = g.SearchExternalMalls(context.Background(), researchapp.KoreanSearchRequest{Query: tc.query, Country: "US"}); err == nil {
				t.Fatal("US accepted")
			}
			usage, err := g.Usage(context.Background(), "OWN_PRODUCT", true)
			if err != nil || !usage.Configured {
				t.Fatal(err)
			}
		})
	}
}

// Local review must show the direct mall paths too, with the audited sample
// products and no invented price: milk asks Kurly, pants ask Zigzag, a cable
// asks Daiso Mall. A query outside the fixture lexicon asks 11st and gets
// nothing from the mall paths.
func TestReviewStubServesTheDirectMallPaths(t *testing.T) {
	for _, test := range []struct {
		name, query, vertical string
		source                researchdomain.Source
		price                 string
		amountMinor           int64
	}{
		{"kurly", "fresh milk 900ml", "FOOD", researchdomain.SourceKurly, "UNKNOWN", 0},
		{"zigzag", "velour banding pants", "FASHION", researchdomain.SourceZigzag, "OBSERVED", 51750},
		{"daiso", "usb c to lightning cable", "LIVING", researchdomain.SourceDaisomall, "OBSERVED", 3000},
	} {
		t.Run(test.name, func(t *testing.T) {
			g, err := NewReviewStub("test", &reviewControl{})
			if err != nil {
				t.Fatal(err)
			}
			result, err := g.SearchExternalMalls(context.Background(), researchapp.KoreanSearchRequest{
				Query: test.query, Country: "KR", Vertical: test.vertical, Seeds: []string{test.query},
			})
			if err != nil {
				t.Fatal(err)
			}
			var found *researchdomain.ExternalProductObservation
			for i := range result.Observations {
				if result.Observations[i].ProductRef.Source == test.source {
					found = &result.Observations[i]
				}
			}
			if found == nil {
				t.Fatalf("%s missing from %+v", test.source, result.Observations)
			}
			if found.Validate() != nil || found.Price.Kind != test.price || found.ObservedAt.Year() != 2026 ||
				!strings.Contains(found.Provenance.APIProduct, "STUB") {
				t.Fatalf("observation=%+v", *found)
			}
			if test.amountMinor != 0 && *found.Price.AmountMinor != test.amountMinor {
				t.Fatalf("price=%+v", found.Price)
			}
			if found.ProductURL == "" || found.Title == "" {
				t.Fatalf("observation=%+v", *found)
			}
		})
	}

	g, err := NewReviewStub("test", &reviewControl{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := g.SearchExternalMalls(context.Background(), researchapp.KoreanSearchRequest{
		Query: "unrelated fixture", Country: "KR", Vertical: "FOOD", Seeds: []string{"unrelated fixture"},
	})
	if err != nil || len(result.Observations) != 0 {
		t.Fatalf("outside the lexicon the malls answer empty: err=%v observations=%d", err, len(result.Observations))
	}
}
