package postgres

import (
	"strings"
	"testing"
)

func TestPayPalResourceAdoptionProjectionKeepsExpiredResourceLessGuard(t *testing.T) {
	for _, predicate := range []string{
		"operation.purpose='PAYPAL_REAUTHORIZE'",
		"operation.state IN ('SENT','UNKNOWN')",
		"operation.provider_resource_id IS NULL",
		"operation.idempotency_deadline <= $1",
		"merchant_order.state='PLANNED'",
		"position.state='AVAILABLE'",
		"paypal_authorization.state IN ('AUTHORIZED','PARTIALLY_CAPTURED')",
		"sibling.state IN (",
		"compensation.action='REFUND'",
		"compensation.state IN ('EXECUTION_PENDING','OUTCOME_UNKNOWN')",
		"compensation.provider_resource_id IS NULL",
		"candidate.purpose='PAYPAL_MO_REFUND'",
		"ORDER BY candidate.created_at DESC,candidate.id DESC",
	} {
		if !strings.Contains(paypalResourceAdoptionCandidates, predicate) {
			t.Fatalf("manual adoption projection lost guard %q", predicate)
		}
	}
	for _, forbidden := range []string{"INSERT ", "UPDATE ", "DELETE ", "PAYPAL_MO_CAPTURE"} {
		if strings.Contains(paypalResourceAdoptionCandidates, forbidden) {
			t.Fatalf("operator adoption projection must remain read-only: %q", forbidden)
		}
	}
	if strings.Contains(paypalResourceAdoptionCandidates, "authorizations authorization") {
		t.Fatal("PostgreSQL reserved keyword authorization must not be used as a table alias")
	}
}
