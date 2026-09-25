package domain

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/policy"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

// 이 테스트는 고객 명령 공간 계약의 골든 fixture를 소유한다(ADR-0055 §5).
// fixture는 shared/openapi/fixtures에 있고 Web 테스트가 같은 파일을 파싱해
// FE 렌더 공간과 BE 계산이 어긋나면 CI가 잡는다. 갱신은
// UPDATE_CONTRACT_FIXTURES=1 go test ./internal/ordering/agencyorder/domain/ 로 한다.

const customerActionsFixtureName = "agency-order-customer-actions.v2.json"

type customerActionsFixtureCase struct {
	Name             string           `json:"name"`
	Now              time.Time        `json:"now"`
	Projection       Projection       `json:"projection"`
	AvailableActions []CustomerAction `json:"availableActions"`
}

type customerActionsFixture struct {
	SchemaVersion string                       `json:"schemaVersion"`
	Cases         []customerActionsFixtureCase `json:"cases"`
}

func customerActionsFixtureCases() []customerActionsFixtureCase {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	money := func(cents int64) Money { return Money{AmountMinor: cents, Currency: "USD"} }
	baseOrder := AgencyOrder{
		ID: "order-fixture-1", Status: "ISSUED",
		SourceCart: SourceCartSnapshot{CartID: "cart-fixture-1", CartVersion: 3,
			SnapshotHash: "0xfixture-cart-snapshot"},
		Lines: []ExactLine{{
			LineID: "line-a", ShopDomain: "alpha.example", ProductTitle: "Fixture Lamp",
			ProductURL: "https://alpha.example/products/fixture-lamp",
			VariantID:  "variant-blue", VariantTitle: "Blue / 110V",
			SelectedOptions: []string{"Color: Blue", "Voltage: 110V"},
			Quantity:        2, UnitPrice: money(1500), LineSubtotal: money(3000),
		}},
		ShippingAddress: ShippingSnapshot{SnapshotRef: "shipping-fixture-1",
			SnapshotRevision: 2, SnapshotHash: "0xfixture-shipping-snapshot", Country: "US"},
		PassThroughTotal: money(3000), CustomerPayableTotal: money(3192),
		AgencyFee: FeeBreakdown{
			VariableAmount: money(162), FixedAmount: money(30), Total: money(192),
			PolicyVersion: policy.PayPalMOFeePolicyVersion,
		},
		IssuanceEvidence: IssuanceEvidence{OrderSheetSessionID: "sheet-fixture-1",
			DisplayedSnapshotHash: "0xfixture-displayed-snapshot",
			DisclosureVersion:     "2026-08-27", IdempotencyKeyHash: "0xfixture-idempotency"},
		IssuedAt: now.Add(-time.Hour), ExpiresAt: now.Add(19 * time.Hour),
	}
	profile, err := ExecutionProfileForRail(PaymentRailPayPalSandbox)
	if err != nil {
		panic(err)
	}
	profileHash, err := profile.Hash()
	if err != nil {
		panic(err)
	}
	baseOrder.PaymentSelection = profile.LegacyPaymentSelection()
	baseOrder.ExecutionProfile = profile
	baseOrder.ExecutionProfileHash = profileHash
	baseOrder.ProcurementAuthorization = ProcurementAuthorization{
		Kind: ProcurementAuthorizationManualOperator,
		Shops: []AuthorizedProcurementShop{{
			MerchantID: "merchant-alpha", ShopDomain: "alpha.example",
			Lines: []AuthorizedProcurementLine{{
				LineID: "line-a", ProductURL: baseOrder.Lines[0].ProductURL,
				ProductTitle: baseOrder.Lines[0].ProductTitle,
				VariantID:    baseOrder.Lines[0].VariantID, VariantTitle: baseOrder.Lines[0].VariantTitle,
				SelectedOptions: append([]string(nil), baseOrder.Lines[0].SelectedOptions...),
				Quantity:        2, UnitPrice: money(1500), LineSubtotal: money(3000),
			}},
			ApprovedAmount: money(3000),
			Conditions: ProcurementConditions{
				ShippingStatus:     ProcurementConditionNotProvided,
				CancellationStatus: ProcurementConditionNotProvided,
				ReturnStatus:       ProcurementConditionNotProvided,
				TaxTotal:           money(0), DutiesDisposition: DutiesNoSignalAtPreflight,
			},
			QuoteFingerprint: "0xfixture-quote", EvidenceHash: "0xfixture-checkout-evidence",
		}},
		ApprovedPassThrough: money(3000), ApprovedAgencyFee: money(192),
		ApprovedCustomerPayable: money(3192),
		CustomerApproval: ProcurementApproval{AgencyConsent: true, PrivacyConsent: true,
			Locale: "en-US", CopyVersion: ProcurementAuthorizationCopyV1},
		OrderSheetSessionID:    baseOrder.IssuanceEvidence.OrderSheetSessionID,
		SourceCartSnapshotHash: baseOrder.SourceCart.SnapshotHash,
		DisplayedSnapshotHash:  baseOrder.IssuanceEvidence.DisplayedSnapshotHash,
		ExecutionProfileHash:   profileHash, AcceptedAt: baseOrder.IssuedAt,
	}
	authorizationHash, err := shareddomain.CanonicalJSONHash(baseOrder.ProcurementAuthorization)
	if err != nil {
		panic(err)
	}
	baseOrder.ProcurementAuthorization.AuthorizationHash = authorizationHash
	instruction := PaymentInstruction{
		ID: "instruction-1", AgencyOrderID: baseOrder.ID, State: "PENDING",
		PaymentSelection:     baseOrder.PaymentSelection,
		ExecutionProfileHash: profileHash,
		CustomerPayableTotal: baseOrder.CustomerPayableTotal,
		ExpiresAt:            now.Add(19 * time.Hour), CreatedAt: now.Add(-time.Hour),
	}
	payCase := Projection{
		AgencyOrder: baseOrder, PaymentInstruction: instruction,
		Process:        Process{AgencyOrderID: baseOrder.ID, State: ProcessWaitingCustomerPayment, Version: 1},
		MerchantOrders: []MerchantOrderSummary{}, Shipments: []ShipmentSummary{},
	}

	cancelCase := payCase
	cancelCase.Payment = &PaymentProjection{ID: "payment-1", Rail: "PAYPAL", State: "AUTHORIZED"}
	cancelCase.Process = Process{AgencyOrderID: baseOrder.ID, State: ProcessProcurementInProgress, Version: 3}
	cancelCase.MerchantOrders = []MerchantOrderSummary{{
		ID: "shop-1", AllocationID: "allocation-1", ShopDomain: "alpha.example", MerchantID: "merchant-1",
		CheckoutOrdinal: 1, ExecutionMode: "SIMULATED_NO_EFFECT", State: "PLANNED",
		CustomerGrossAmount: money(3192), FundingState: "AVAILABLE",
		Units: []MerchantOrderUnitSummary{{ID: "unit-1", LineID: "line-a", UnitIndex: 1, Disposition: "PENDING"}},
	}}

	refundCase := cancelCase
	refundCase.Process = Process{AgencyOrderID: baseOrder.ID, State: ProcessLogisticsInProgress, Version: 6}
	refundCase.MerchantOrders = []MerchantOrderSummary{{
		ID: "shop-1", AllocationID: "allocation-1", ShopDomain: "alpha.example", MerchantID: "merchant-1",
		CheckoutOrdinal: 1, ExecutionMode: "SIMULATED_NO_EFFECT", State: "PLACED",
		CustomerGrossAmount: money(3192), FundingState: "ACTIVE",
		Units: []MerchantOrderUnitSummary{{ID: "unit-1", LineID: "line-a", UnitIndex: 1, Disposition: "PENDING"}},
	}}

	delayCase := refundCase
	delayCase.AgencyOrder.IssuedAt = now.Add(-31 * 24 * time.Hour)

	cases := []customerActionsFixtureCase{
		{Name: "지시 유효·수납 전 — PAY만 열린다", Now: now, Projection: payCase},
		{Name: "수납 확정·Shop PLANNED — 해당 MO 자유 취소만 열린다", Now: now, Projection: cancelCase},
		{Name: "PLACED 진행 중 — 환불 요청만 열린다", Now: now, Projection: refundCase},
		{Name: "발행 31일 경과 미배송 — 지연 취소와 환불 요청이 열린다", Now: now, Projection: delayCase},
	}
	for index := range cases {
		actions := AvailableCustomerActions(cases[index].Projection, cases[index].Now)
		cases[index].Projection.AvailableActions = actions
		cases[index].AvailableActions = actions
	}
	return cases
}

func TestCustomerActionsGoldenFixture(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve fixture path")
	}
	fixturePath := filepath.Clean(filepath.Join(filepath.Dir(filename),
		"../../../../../shared/openapi/fixtures", customerActionsFixtureName))
	fixture := customerActionsFixture{
		SchemaVersion: "vitlane.agency-order-customer-actions.v2",
		Cases:         customerActionsFixtureCases(),
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(fixture); err != nil {
		t.Fatal(err)
	}
	want := buffer.Bytes()
	if os.Getenv("UPDATE_CONTRACT_FIXTURES") == "1" {
		if err := os.WriteFile(fixturePath, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	stored, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture (run with UPDATE_CONTRACT_FIXTURES=1 to create): %v", err)
	}
	if !bytes.Equal(stored, want) {
		t.Fatal("customer actions fixture is stale — regenerate with UPDATE_CONTRACT_FIXTURES=1 " +
			"and review the FE contract impact")
	}
	// fixture 자체의 계약 불변 조건: action target은 projection의 MO이고,
	// 단순변심 코드는 존재하지 않는다.
	for _, c := range fixture.Cases {
		for _, action := range c.AvailableActions {
			if action.Kind != ActionRequestRefund {
				continue
			}
			merchantOrderIDs := map[string]bool{}
			for _, merchantOrder := range c.Projection.MerchantOrders {
				merchantOrderIDs[merchantOrder.ID] = true
			}
			for _, id := range action.EligibleMerchantOrderIDs {
				if !merchantOrderIDs[id] {
					t.Fatalf("%s: eligible MerchantOrder %s is not projected", c.Name, id)
				}
			}
			for _, code := range action.ReasonCodes {
				if code == "CHANGE_OF_MIND" {
					t.Fatalf("%s: change-of-mind must stay structurally impossible", c.Name)
				}
			}
		}
	}
}
