package postgres

import (
	"strings"
	"testing"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
)

func TestCandidateReaderRejectsMissingVariantDiscovery(t *testing.T) {
	candidate := researchdomain.Candidate{}
	err := decodeCandidateVariantDiscovery(&candidate, []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "variant discovery is incomplete") {
		t.Fatalf("expected strict variant discovery rejection, got %v", err)
	}
}

func TestCandidateReaderRejectsMissingOrderability(t *testing.T) {
	candidate := researchdomain.Candidate{}
	err := decodeCandidateOrderability(&candidate, []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "orderability is incomplete") {
		t.Fatalf("expected strict orderability rejection, got %v", err)
	}
}

func TestCandidateReaderAcceptsCompleteCurrentSchema(t *testing.T) {
	candidate := researchdomain.Candidate{}
	err := decodeCandidateVariantDiscovery(&candidate, []byte(
		`{"schemaVersion":"vitlane.variant-discovery.v1","status":"UNKNOWN","fields":[],"providerVariantRefs":[],"evidence":{"summary":"not observed","sourceUrls":[]}}`,
	))
	if err != nil {
		t.Fatalf("decode current variant discovery: %v", err)
	}
	err = decodeCandidateOrderability(&candidate, []byte(
		`{"schemaVersion":"vitlane.orderability.v1","providerKind":"GENERIC_WEB","executionMode":"MANUAL_MERCHANT_ORDER","externalEffect":"SIMULATED","liveOrderability":"UNVERIFIED","settlementStatus":"SUPPORTED","status":"TEST_ORDER_FLOW_AVAILABLE","reasonCodes":[]}`,
	))
	if err != nil {
		t.Fatalf("decode current orderability: %v", err)
	}
}
