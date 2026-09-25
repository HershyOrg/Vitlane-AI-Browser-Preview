package app

import (
	"context"
	"strings"
	"testing"
	"time"

	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
)

type receiptLifecycleRepository struct {
	lifecycleOwnerRepository
	projection   agencydomain.Projection
	transactions []agencydomain.ChainTransaction
	receipt      agencydomain.Receipt
}

func (r *receiptLifecycleRepository) GetProjectionForReceipt(
	context.Context, string,
) (agencydomain.Projection, error) {
	return r.projection, nil
}

func (r *receiptLifecycleRepository) ListFinalizedTransactions(
	context.Context, string,
) ([]agencydomain.ChainTransaction, error) {
	return r.transactions, nil
}

func (r *receiptLifecycleRepository) CreateReceipt(
	_ context.Context, receipt agencydomain.Receipt,
) error {
	r.receipt = receipt
	return nil
}

func receiptTestOrder(t *testing.T, rail agencydomain.PaymentRail) agencydomain.AgencyOrder {
	t.Helper()
	profile, err := agencydomain.ExecutionProfileForRail(rail)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := profile.Hash()
	if err != nil {
		t.Fatal(err)
	}
	return agencydomain.AgencyOrder{
		ID:                   "order-1",
		PaymentSelection:     profile.LegacyPaymentSelection(),
		ExecutionProfile:     profile,
		ExecutionProfileHash: hash,
	}
}

func TestClassifyReceiptUsesImmutableOrderProfile(t *testing.T) {
	tests := []struct {
		name      string
		rail      agencydomain.PaymentRail
		merchants []agencydomain.MerchantOrderSummary
		kind      string
		legalSale bool
	}{
		{
			name: "PayPal Sandbox remains TEST",
			rail: agencydomain.PaymentRailPayPalSandbox,
			merchants: []agencydomain.MerchantOrderSummary{{
				ExecutionMode: agencydomain.MerchantExecutionSimulatedNoEffect,
				State:         "PLACED",
			}},
			kind: agencydomain.ReceiptKindTest,
		},
		{
			name: "PayPal Live without a placed merchant order is not mislabeled TEST",
			rail: agencydomain.PaymentRailPayPalLive,
			merchants: []agencydomain.MerchantOrderSummary{{
				ExecutionMode: agencydomain.MerchantExecutionLiveEffect,
				State:         "PLACEMENT_PENDING",
			}},
			kind: agencydomain.ReceiptKindLiveOrderRecord,
		},
		{
			name: "PayPal Live placed merchant order records a merchant sale",
			rail: agencydomain.PaymentRailPayPalLive,
			merchants: []agencydomain.MerchantOrderSummary{{
				ExecutionMode: agencydomain.MerchantExecutionLiveEffect,
				State:         "PLACED",
			}},
			kind:      agencydomain.ReceiptKindLiveOrderRecord,
			legalSale: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			classification, err := classifyReceipt(receiptTestOrder(t, test.rail), test.merchants)
			if err != nil {
				t.Fatal(err)
			}
			if classification.Kind != test.kind || classification.LegalSale != test.legalSale {
				t.Fatalf("classification=%+v", classification)
			}
			if test.kind == agencydomain.ReceiptKindLiveOrderRecord &&
				(!strings.Contains(classification.Watermark, "REAL MONEY") ||
					!strings.Contains(classification.Watermark, "NOT A MERCHANT TAX RECEIPT")) {
				t.Fatalf("unsafe Live watermark=%q", classification.Watermark)
			}
		})
	}
}

func TestClassifyReceiptRejectsMerchantModeDrift(t *testing.T) {
	_, err := classifyReceipt(
		receiptTestOrder(t, agencydomain.PaymentRailPayPalLive),
		[]agencydomain.MerchantOrderSummary{{
			ExecutionMode: agencydomain.MerchantExecutionSimulatedNoEffect,
			State:         "PLACED",
		}},
	)
	if err != agencydomain.ErrInvalid {
		t.Fatalf("mixed execution mode must fail closed, got %v", err)
	}
}

func TestIssueReceiptPersistsLiveProfileAndProviderReference(t *testing.T) {
	now := time.Date(2026, 8, 27, 1, 2, 3, 0, time.UTC)
	order := receiptTestOrder(t, agencydomain.PaymentRailPayPalLive)
	repository := &receiptLifecycleRepository{projection: agencydomain.Projection{
		AgencyOrder: order,
		Process: agencydomain.Process{
			State:          agencydomain.ProcessTerminal,
			TerminalReason: agencydomain.TerminalReasonCompletedAll,
		},
		Payment: &agencydomain.PaymentProjection{
			ID: "payment-1", Rail: agencydomain.PaymentProviderPayPal,
			CaptureID: "PAYPAL-CAPTURE-1",
		},
		MerchantOrders: []agencydomain.MerchantOrderSummary{{
			ExecutionMode: agencydomain.MerchantExecutionLiveEffect,
			State:         "PLACED",
		}},
	}}
	service := NewLifecycleService(
		repository, nil, testClock{now: now}, &testIDs{values: []string{"receipt-1"}},
	)

	if err := service.IssueReceipt(context.Background(), order.ID); err != nil {
		t.Fatal(err)
	}
	receipt := repository.receipt
	if receipt.Kind != agencydomain.ReceiptKindLiveOrderRecord ||
		receipt.CustomerPaymentID != "payment-1" ||
		receipt.TerminalTxHash != "PAYPAL-CAPTURE-1" ||
		receipt.ProviderEnvironment != agencydomain.ProviderEnvironmentLive ||
		receipt.ExecutionProfileHash != order.ExecutionProfileHash ||
		!receipt.LegalSale {
		t.Fatalf("receipt=%+v", receipt)
	}
	if !strings.Contains(string(receipt.Payload), `"schemaVersion":"vitlane.agency-order-receipt.v4"`) ||
		!strings.Contains(string(receipt.Payload), `"providerEnvironment":"LIVE"`) {
		t.Fatalf("payload does not preserve Live evidence: %s", receipt.Payload)
	}
}

func TestIssueReceiptRejectsPaymentRailDrift(t *testing.T) {
	order := receiptTestOrder(t, agencydomain.PaymentRailPayPalLive)
	repository := &receiptLifecycleRepository{projection: agencydomain.Projection{
		AgencyOrder: order,
		Process: agencydomain.Process{
			State:          agencydomain.ProcessTerminal,
			TerminalReason: agencydomain.TerminalReasonCompletedAll,
		},
		Payment: &agencydomain.PaymentProjection{
			ID: "payment-1", Rail: agencydomain.PaymentProviderGIWA,
			CompleteTxHash: "0xcomplete",
		},
		MerchantOrders: []agencydomain.MerchantOrderSummary{{
			ExecutionMode: agencydomain.MerchantExecutionLiveEffect,
			State:         "PLACED",
		}},
	}}
	service := NewLifecycleService(
		repository, nil, testClock{now: time.Now()}, &testIDs{values: []string{"receipt-1"}},
	)

	err := service.IssueReceipt(context.Background(), order.ID)
	if err == nil || !strings.Contains(err.Error(), "payment rail mismatch") {
		t.Fatalf("payment/order profile drift must fail closed, got %v", err)
	}
	if repository.receipt.ID != "" {
		t.Fatalf("invalid receipt persisted: %+v", repository.receipt)
	}
}

// whole-MO 보상으로 환불된 GIWA 주문의 영수증은 compensation refund tx를 terminal
// 참조로 쓴다(Settlement 전체 refund tx는 없다) — projection이 채운 RefundTxHash.
func TestIssueReceiptUsesCompensationRefundReferenceForGIWA(t *testing.T) {
	now := time.Date(2026, 9, 4, 7, 0, 0, 0, time.UTC)
	order := receiptTestOrder(t, agencydomain.PaymentRailTVITUSD)
	repository := &receiptLifecycleRepository{transactions: []agencydomain.ChainTransaction{{Purpose: "REFUND_PARTIAL", TxHash: "0xrefund-partial", State: "FINALIZED"}}, projection: agencydomain.Projection{
		AgencyOrder: order,
		Process: agencydomain.Process{
			State:          agencydomain.ProcessTerminal,
			TerminalReason: agencydomain.TerminalReasonRefundedAll,
		},
		Payment: &agencydomain.PaymentProjection{
			ID: "payment-1", Rail: agencydomain.PaymentProviderGIWA,
			PayTxHash: "0xpay", RefundTxHash: "0xrefund-partial",
		},
		MerchantOrders: []agencydomain.MerchantOrderSummary{{
			ExecutionMode: agencydomain.MerchantExecutionSimulatedNoEffect,
			State:         "CANCELLED",
		}},
	}}
	service := NewLifecycleService(
		repository, nil, testClock{now: now}, &testIDs{values: []string{"receipt-1"}},
	)
	if err := service.IssueReceipt(context.Background(), order.ID); err != nil {
		t.Fatal(err)
	}
	if repository.receipt.TerminalTxHash != "0xrefund-partial" ||
		repository.receipt.TerminalState != string(agencydomain.TerminalReasonRefundedAll) {
		t.Fatalf("receipt=%+v", repository.receipt)
	}
}

func TestGIWAReceiptRequiresFinalizedTerminalProof(t *testing.T) {
	for _, tc := range []struct {
		name, payment, purpose, txState, txHash string
		allowed                                 bool
	}{
		{"broadcast complete", "COMPLETION_SUBMITTED", "PAY", "FINALIZED", "0xpay", false},
		{"safe complete", "COMPLETION_SUBMITTED", "COMPLETE", "SAFE", "0xcomplete", false},
		{"payment not converged", "COMPLETION_SUBMITTED", "COMPLETE", "FINALIZED", "0xcomplete", false},
		{"wrong terminal hash", "COMPLETED", "COMPLETE", "FINALIZED", "0xother", false},
		{"wrong purpose", "COMPLETED", "PAY", "FINALIZED", "0xcomplete", false},
		{"finalized complete", "COMPLETED", "COMPLETE", "FINALIZED", "0xCOMPLETE", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			order := receiptTestOrder(t, agencydomain.PaymentRailTVITUSD)
			repository := &receiptLifecycleRepository{
				projection: agencydomain.Projection{
					AgencyOrder: order,
					Process:     agencydomain.Process{State: agencydomain.ProcessTerminal, TerminalReason: agencydomain.TerminalReasonCompletedAll},
					Payment:     &agencydomain.PaymentProjection{ID: "payment-1", Rail: "GIWA", State: tc.payment, CompleteTxHash: "0xcomplete"},
				},
				transactions: []agencydomain.ChainTransaction{{Purpose: tc.purpose, State: tc.txState, TxHash: tc.txHash}},
			}
			service := NewLifecycleService(repository, nil, testClock{now: time.Now()}, &testIDs{values: []string{"receipt-1"}})
			err := service.IssueReceipt(context.Background(), order.ID)
			if tc.allowed {
				if err != nil || !repository.receipt.HasFinalizedTerminalTransaction() {
					t.Fatalf("finalized receipt rejected: %v", err)
				}
			} else if err == nil || repository.receipt.ID != "" {
				t.Fatalf("premature receipt persisted: err=%v receipt=%+v", err, repository.receipt)
			}
		})
	}
}
