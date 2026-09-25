package domain_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	supportdomain "github.com/vitlane/vitlane/server/internal/support/domain"
)

func TestBusinessCardCatalogAndAuthors(t *testing.T) {
	card, err := supportdomain.NewBusinessCard(
		supportdomain.CardProcurementRequest,
		supportdomain.ReferenceProcurement,
		"procurement-request-1",
		true,
		"",
		json.RawMessage(`{ "prompt": "Confirm the color" }`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(card.PublicPayload) != `{"prompt":"Confirm the color"}` {
		t.Fatalf("payload=%s", card.PublicPayload)
	}
	operator, err := supportdomain.NewOperatorBusinessCardMessage(
		"m1", "u1", "op1", "Please confirm the color.", "", key, card, now,
	)
	if err != nil || operator.Author != supportdomain.AuthorOperator ||
		operator.ContentKind != supportdomain.ContentKindBusinessCard ||
		operator.BusinessCard == nil || !operator.BusinessCard.ActionRequired {
		t.Fatalf("operator=%+v err=%v", operator, err)
	}
	system, err := supportdomain.NewSystemBusinessCardMessage(
		"m2", "u1", "A PayPal dispute was opened.", "", key,
		mustCard(t, supportdomain.CardPayPalDispute,
			supportdomain.ReferencePayPalDispute, "dispute-1"), now,
	)
	if err != nil || system.Author != supportdomain.AuthorSystem {
		t.Fatalf("system=%+v err=%v", system, err)
	}
	if system.IdempotencyNamespace != supportdomain.IdempotencyNamespaceBusinessCard {
		t.Fatalf("system namespace=%q", system.IdempotencyNamespace)
	}
	legacyGlobal := system
	legacyGlobal.IdempotencyNamespace = supportdomain.IdempotencyNamespaceExternal
	if supportdomain.CanonicalRequestHash(legacyGlobal, nil) != system.RequestHash {
		t.Fatal("namespace migration invalidated an existing card replay hash")
	}
	customer, err := supportdomain.NewCustomerBusinessCardMessage(
		"m3", "u1", "I agree.", "", key,
		mustCard(t, supportdomain.CardProcurementResponse,
			supportdomain.ReferenceProcurement, "request-1"), now,
	)
	if err != nil || customer.Author != supportdomain.AuthorCustomer {
		t.Fatalf("customer=%+v err=%v", customer, err)
	}
	if _, err := supportdomain.NewBusinessCard(
		supportdomain.CardOrderCancellation, supportdomain.ReferenceProcurement,
		"order-1", false, "", json.RawMessage(`{"state":"CANCELLED"}`),
	); err != nil {
		t.Fatalf("order cancellation card: %v", err)
	}
}

func TestBusinessCardRejectsUnknownAndNonObjectPayload(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		cardType string
		refType  string
		refID    string
		payload  json.RawMessage
	}{
		{"unknown card", "OTHER", supportdomain.ReferenceRefund, "r1", json.RawMessage(`{}`)},
		{"unknown reference", supportdomain.CardRefundStatus, "OTHER", "r1", json.RawMessage(`{}`)},
		{"mismatched reference", supportdomain.CardRefundStatus, supportdomain.ReferenceProcurement, "r1", json.RawMessage(`{}`)},
		{"missing reference", supportdomain.CardRefundStatus, supportdomain.ReferenceRefund, "", json.RawMessage(`{}`)},
		{"array payload", supportdomain.CardRefundStatus, supportdomain.ReferenceRefund, "r1", json.RawMessage(`[]`)},
		{"oversized", supportdomain.CardRefundStatus, supportdomain.ReferenceRefund, "r1", json.RawMessage(`{"value":"` + strings.Repeat("x", supportdomain.MaxBusinessCardPayloadBytes) + `"}`)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := supportdomain.NewBusinessCard(
				testCase.cardType, testCase.refType, testCase.refID,
				false, "", testCase.payload,
			)
			if !errors.Is(err, supportdomain.ErrBusinessCardInvalid) {
				t.Fatalf("err=%v", err)
			}
		})
	}
	if _, err := supportdomain.NewBusinessCard(
		supportdomain.CardRefundDecision, supportdomain.ReferenceRefund, "r1",
		true, "request-card-1", json.RawMessage(`{}`),
	); !errors.Is(err, supportdomain.ErrBusinessCardInvalid) {
		t.Fatalf("action and resolution accepted together: %v", err)
	}
}

func TestConversationViewsAreSeparate(t *testing.T) {
	for input, expected := range map[string]supportdomain.ConversationView{
		"":                supportdomain.ConversationViewAwaitingReply,
		"AWAITING":        supportdomain.ConversationViewAwaitingReply,
		"AWAITING_REPLY":  supportdomain.ConversationViewAwaitingReply,
		"ACTION_REQUIRED": supportdomain.ConversationViewActionRequired,
		"ALL":             supportdomain.ConversationViewAll,
	} {
		view, err := supportdomain.ParseConversationView(input)
		if err != nil || view != expected {
			t.Fatalf("input=%q view=%q err=%v", input, view, err)
		}
	}
	if _, err := supportdomain.ParseConversationView("COMBINED"); !errors.Is(err, supportdomain.ErrQueryInvalid) {
		t.Fatalf("err=%v", err)
	}
}

func mustCard(
	t *testing.T,
	cardType, referenceType, referenceID string,
) supportdomain.BusinessCard {
	t.Helper()
	card, err := supportdomain.NewBusinessCard(
		cardType, referenceType, referenceID, false, "", json.RawMessage(`{}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	return card
}
