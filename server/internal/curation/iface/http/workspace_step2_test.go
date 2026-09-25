package http

import (
	"encoding/json"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"os"
	"testing"
)

func TestWorkspaceWirePreservesSharedExternalProductObservations(t *testing.T) {
	raw, err := os.ReadFile("../../../../../shared/openapi/fixtures/curation-step2.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Observation researchdomain.ExternalProductObservation   `json:"observation"`
		Unknown     researchdomain.ExternalProductObservation   `json:"unknownObservation"`
		Malls       []researchdomain.ExternalProductObservation `json:"mallObservations"`
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	// The registry malls travel the same wire: identity, canonical URL and the
	// observed or unknown price must survive for every one of them.
	if len(fixture.Malls) == 0 {
		t.Fatal("the shared fixture lost its mall observations")
	}
	products := []researchapp.CatalogProductObservation{}
	for _, o := range append([]researchdomain.ExternalProductObservation{fixture.Observation, fixture.Unknown}, fixture.Malls...) {
		p, e := researchapp.CatalogProductFromExternalObservation(o)
		if e != nil {
			t.Fatal(e)
		}
		products = append(products, p)
	}
	mapped := mapCatalogResearchWorkspace(researchapp.CatalogWorkspaceViewV2{Pools: []researchapp.CatalogWorkspacePoolV2{{Metadata: researchapp.CatalogPoolMetadataV2{TargetID: "target", Version: 1}, Products: products}}})
	encoded, err := json.Marshal(mapped)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Pools []struct {
			Products []struct {
				Source      string                                     `json:"source"`
				Observation *researchdomain.ExternalProductObservation `json:"externalObservation"`
			} `json:"products"`
		} `json:"pools"`
	}
	if err = json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	sources := map[string]bool{}
	for i, p := range decoded.Pools[0].Products {
		if p.Observation == nil || p.Observation.Validate() != nil || p.Source != string(products[i].Source()) {
			t.Fatal("workspace omitted observation or source")
		}
		if p.Observation.ProductURL != products[i].ExternalObservation.ProductURL ||
			p.Observation.ProductRef != products[i].ExternalObservation.ProductRef ||
			p.Observation.Price.Kind != products[i].ExternalObservation.Price.Kind {
			t.Fatalf("%s lost identity, URL or price meaning on the wire", p.Source)
		}
		sources[p.Source] = true
	}
	for _, mall := range fixture.Malls {
		if !sources[string(mall.ProductRef.Source)] {
			t.Fatalf("%s never reached the workspace wire", mall.ProductRef.Source)
		}
	}
	if decoded.Pools[0].Products[1].Observation.Price.AmountMinor != nil || decoded.Pools[0].Products[1].Observation.Price.Kind != "UNKNOWN" {
		t.Fatal("unknown price turned into zero")
	}
}
