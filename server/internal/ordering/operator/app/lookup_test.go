package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
)

type fakeLookupRepository struct {
	input       OrderLookupInput
	actor       string
	match       OrderLookupMatch
	err         error
	identifiers []OrderIdentifier
	detailAudit string
}

func (f *fakeLookupRepository) ResolveAndAudit(
	_ context.Context, input OrderLookupInput, operatorUserID string, _ time.Time,
) (OrderLookupMatch, error) {
	f.input = input
	f.actor = operatorUserID
	return f.match, f.err
}

func (f *fakeLookupRepository) ListIdentifiers(
	_ context.Context, _ string,
) ([]OrderIdentifier, error) {
	return f.identifiers, f.err
}

func (f *fakeLookupRepository) AuditDetailView(
	_ context.Context, agencyOrderID, _ string, _ time.Time,
) error {
	f.detailAudit = agencyOrderID
	return f.err
}

type fakeOrderEvidenceReader struct {
	projection agencydomain.Projection
	err        error
}

func (f fakeOrderEvidenceReader) GetOperatorProjection(
	_ context.Context, _ string,
) (agencydomain.Projection, error) {
	return f.projection, f.err
}

func TestLookupOrderNormalizesExactInput(t *testing.T) {
	repository := &fakeLookupRepository{match: OrderLookupMatch{
		AgencyOrderID: "order-1", MatchedBy: LookupPayPalCaptureID,
		Environment: "SANDBOX", PaymentRail: "PAYPAL",
	}}
	service := NewService(&fakePorts{}, &fakePorts{}, &fakePorts{}, fakeClock{now: at(12)})
	service.EnableOrderLookup(repository, fakeOrderEvidenceReader{})
	match, err := service.LookupOrder(context.Background(), OrderLookupInput{
		IdentifierType: " paypal_capture_id ", Value: " CAPTURE-7 ",
		Environment: " sandbox ", ShopDomain: " SHOP.EXAMPLE ",
	}, " operator-1 ")
	if err != nil {
		t.Fatal(err)
	}
	if match.AgencyOrderID != "order-1" || repository.actor != "operator-1" {
		t.Fatalf("match=%+v actor=%q", match, repository.actor)
	}
	if repository.input.IdentifierType != LookupPayPalCaptureID ||
		repository.input.Value != "CAPTURE-7" ||
		repository.input.Environment != LookupEnvironmentSandbox ||
		repository.input.ShopDomain != "shop.example" {
		t.Fatalf("normalized input=%+v", repository.input)
	}
}

func TestLookupOrderRejectsInvalidScopeBeforeRepository(t *testing.T) {
	repository := &fakeLookupRepository{}
	service := NewService(&fakePorts{}, &fakePorts{}, &fakePorts{}, fakeClock{now: at(12)})
	service.EnableOrderLookup(repository, fakeOrderEvidenceReader{})
	_, err := service.LookupOrder(context.Background(), OrderLookupInput{
		IdentifierType: LookupAuto, Value: "capture-1", Environment: "PRODUCTION",
	}, "operator-1")
	if !errors.Is(err, ErrOrderLookupInvalid) || repository.actor != "" {
		t.Fatalf("err=%v repository=%+v", err, repository)
	}
}

func TestInvestigateOrderReturnsSafeEvidenceOnly(t *testing.T) {
	updatedAt := at(11)
	projection := agencydomain.Projection{
		AgencyOrder: agencydomain.AgencyOrder{
			ID: "order-1", UserID: "buyer-secret", Status: "ISSUED",
			IssuedAt: at(8), ExpiresAt: at(13), SnapshotHash: "snapshot-hash",
			ExecutionProfile: agencydomain.OrderExecutionProfile{
				PaymentRail: "PAYPAL", ProviderEnvironment: "SANDBOX", Asset: "USD",
				EconomicEffect: "NO_REAL_VALUE", MerchantExecutionMode: "SIMULATED_NO_EFFECT",
			},
			ExecutionProfileHash: "profile-hash",
			ShippingAddress: agencydomain.ShippingSnapshot{
				SnapshotRef: "secret-shipping-ref", MaskedSummary: "S***, US", Country: "US",
			},
			CustomerPayableTotal: agencydomain.Money{AmountMinor: 1250, Currency: "USD"},
			PassThroughTotal:     agencydomain.Money{AmountMinor: 1000, Currency: "USD"},
			AgencyFee:            agencydomain.FeeBreakdown{Total: agencydomain.Money{AmountMinor: 250, Currency: "USD"}},
			Lines: []agencydomain.ExactLine{{
				LineID: "line-1", ProductTitle: "Travel bag", ProductURL: "https://secret.example/item",
				Quantity: 1, ShopDomain: "shop.example", ObservedAt: at(7), EvidenceHash: "line-hash",
			}},
		},
		Process: agencydomain.Process{
			AgencyOrderID: "order-1", State: agencydomain.ProcessProcurementInProgress,
			UpdatedAt: updatedAt,
		},
		PaymentInstruction: agencydomain.PaymentInstruction{
			ID: "instruction-1", State: "CONSUMED", CreatedAt: at(8), ExpiresAt: at(13),
		},
		Receipt: &agencydomain.Receipt{
			ID: "receipt-1", TerminalState: "COMPLETED_ALL", ReceiptHash: "receipt-hash",
			Payload: json.RawMessage(`{"providerSecret":"must-not-leak"}`), CreatedAt: at(12),
		},
	}
	repository := &fakeLookupRepository{identifiers: []OrderIdentifier{{
		Kind: LookupAgencyOrderID, Value: "order-1",
	}}}
	service := NewService(&fakePorts{}, &fakePorts{}, &fakePorts{}, fakeClock{now: at(12)})
	service.EnableOrderLookup(repository, fakeOrderEvidenceReader{projection: projection})
	investigation, err := service.InvestigateOrder(context.Background(), " order-1 ", "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	if repository.detailAudit != "order-1" || investigation.Order.ShippingMasked != "S***, US" {
		t.Fatalf("investigation=%+v audit=%q", investigation, repository.detailAudit)
	}
	payload, err := json.Marshal(investigation)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"buyer-secret", "secret-shipping-ref", "secret.example/item", "must-not-leak"} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("safe investigation leaked %q: %s", forbidden, payload)
		}
	}
}
