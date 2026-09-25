package http

import (
	"net/http/httptest"
	"strings"
	"testing"

	httpapi "github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

func TestResultRequestRejectsClientOwnedHashAndObservedTime(t *testing.T) {
	body := `{
		"done":true,
		"evidenceKind":"SANDBOX_TEST_EVIDENCE",
		"externalOrderRef":"TEST-ORDER-1",
		"receiptSafeRef":"receipt:test:1",
		"amountMode":"CHANGED",
		"actualAmountMinor":3030,
		"evidenceSource":"RECEIPT",
		"evidenceHash":"caller-controlled",
		"observedAt":"2026-08-28T00:00:00Z"
	}`
	request := httptest.NewRequest("POST", "/admin/procurement/tasks/task-1/result", strings.NewReader(body))
	response := httptest.NewRecorder()
	var decoded resultRequest
	if httpapi.DecodeJSON(response, request, &decoded) {
		t.Fatal("client-controlled hash/time fields were accepted")
	}
	if response.Code != 400 {
		t.Fatalf("unexpected status for client integrity fields: %d", response.Code)
	}
}

func TestResultRequestAcceptsOnlyOperatorAuthoredPlacementFields(t *testing.T) {
	body := `{
		"done":true,
		"evidenceKind":"SANDBOX_TEST_EVIDENCE",
		"externalOrderRef":"TEST-ORDER-1",
		"receiptSafeRef":"receipt:test:1",
		"amountMode":"CHANGED",
		"actualAmountMinor":3030,
		"evidenceSource":"RECEIPT"
	}`
	request := httptest.NewRequest("POST", "/admin/procurement/tasks/task-1/result", strings.NewReader(body))
	response := httptest.NewRecorder()
	var decoded resultRequest
	if !httpapi.DecodeJSON(response, request, &decoded) {
		t.Fatalf("operator-authored placement fields were rejected: status=%d body=%s", response.Code, response.Body.String())
	}
	if decoded.AmountMode != "CHANGED" || decoded.ActualAmountMinor != 3030 || decoded.ExternalOrderRef != "TEST-ORDER-1" {
		t.Fatalf("unexpected decoded request: %+v", decoded)
	}
}
