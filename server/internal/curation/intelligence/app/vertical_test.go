package app

import (
	"strings"
	"testing"
)

// New model responses classify one family; old checkpoints remain decodable.
func TestCatalogQuerySchemaRequiresOneProductVertical(t *testing.T) {
	schema := CatalogQuerySchema()
	properties := schema["properties"].(map[string]any)
	vertical, ok := properties["productVertical"].(map[string]any)
	if !ok {
		t.Fatal("productVertical missing from the catalog query schema")
	}
	values := vertical["enum"].([]string)
	if strings.Join(values, ",") != "GENERAL,FASHION,BEAUTY,FOOD,LIVING,ELECTRONICS" {
		t.Fatalf("vertical enum=%v", values)
	}
	found := false
	for _, required := range schema["required"].([]string) {
		found = found || required == "productVertical"
	}
	if !found {
		t.Fatal("new model responses must include productVertical")
	}
	if !strings.Contains(providerCatalogQueryPrompt("KR"), "productVertical") || strings.Contains(providerCatalogQueryPrompt("US"), "productVertical") {
		t.Fatal("only the Korean query prompt asks for the vertical")
	}
}

func TestRankingSchemaRequiresTheActualSelectedCount(t *testing.T) {
	for _, count := range []int{1, 15, 50} {
		schema := CandidateRankingSchema(count, 3)
		ranked := schema["properties"].(map[string]any)["ranked"].(map[string]any)
		if ranked["minItems"] != count || ranked["maxItems"] != count {
			t.Fatal(ranked)
		}
	}
}
