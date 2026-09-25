package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAddressFreeOrderSheetJSONUsesEmptyCollectionsAndOmitsUnconfirmedSnapshot(t *testing.T) {
	payload, err := json.Marshal(OrderSheetSession{
		MerchantCheckouts: []MerchantCheckout{},
		PassThroughTotal:  Money{Currency: "USD"}, AgencyFee: Money{Currency: "USD"},
		CustomerPayableTotal: Money{Currency: "USD"},
	})
	if err != nil {
		t.Fatal(err)
	}
	value := string(payload)
	if strings.Contains(value, `"shippingAddress"`) || !strings.Contains(value, `"merchantCheckouts":[]`) {
		t.Fatalf("unexpected address-free OrderSheet JSON: %s", value)
	}
}

func readySession(t *testing.T) OrderSheetSession {
	t.Helper()
	now := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	checkout := MerchantCheckout{
		MerchantID: "shop.example", ShopDomain: "shop.example",
		StorefrontCartSafeRef: "cart-safe", CheckoutSessionSafeRef: "checkout-safe",
		LineRefs: []string{"line-1"}, SelectedDeliveryOptionRef: "standard",
		DeliveryGroups: []DeliveryGroup{{ID: "group-1", LineRefs: []string{"line-1"},
			Options: []DeliveryOption{{ID: "standard", Title: "Standard", Currency: "USD"}}, SelectedOptionRef: "standard"}},
		ProviderStatus: "ready_for_complete", AuthoritativeTotal: Money{AmountMinor: 1050, Currency: "USD"},
		TaxTotal: Money{AmountMinor: 50, Currency: "USD"}, DutiesDisposition: DutiesNoSignalAtPreflight,
		AuthEvidence:    AuthEvidence{Tier: "TOKEN", ScopesHash: "scopes", AgentProfileURL: "https://vitlane.example/.well-known/ucp-agent.json", AgentProfileVersion: "2026-04-08", AgentProfileHash: "profile", CompletionPermission: CompletionNotRequested},
		CompletionRoute: CompletionManualHandoff, QuoteReadiness: PricingConfirmed,
		ProcurementHandling: ProcurementHandlingNormal,
		ContinueURLSafeRef:  "continue-safe", ContinueURLHash: "continue-hash", QuoteFingerprint: "quote",
		ObservedAt: now, ExpiresAt: now.Add(20 * time.Minute), EvidenceHash: "evidence",
	}
	session := OrderSheetSession{
		ID: "sheet-1", UserID: "user-1", PaymentSelection: PaymentRailTVITUSD,
		State: SessionPreflighting, Version: 2,
		SourceCart: SourceCartSnapshot{CartID: "cart-1", CartVersion: 4, SnapshotHash: "cart-hash", Items: []CartItemSnapshot{{CartItemID: "item-1"}}},
		Lines: []ExactLine{{
			LineID: "line-1", SourceCartItemID: "item-1", ShopDomain: "shop.example",
			ProductURL: "https://shop.example/products/test", ProductTitle: "Test product",
			VariantID: "gid://shopify/ProductVariant/1", VariantTitle: "Blue / M",
			SelectedOptions: []string{"Color: Blue", "Size: M"}, Quantity: 1,
			UnitPrice:    Money{AmountMinor: 1000, Currency: "USD"},
			LineSubtotal: Money{AmountMinor: 1000, Currency: "USD"},
		}},
		ShippingAddress: ShippingSnapshot{SnapshotRef: "address-1", SnapshotRevision: 1, SnapshotHash: "address-hash", Country: "US"},
		CreatedAt:       now, ExpiresAt: now.Add(20 * time.Minute),
	}
	if err := session.ApplyPreflight([]MerchantCheckout{checkout}, now); err != nil {
		t.Fatal(err)
	}
	return session
}

func testProcurementApproval() ProcurementApproval {
	return ProcurementApproval{
		OrderMessage:    "Leave the receipt in the package.",
		DeliveryMessage: "Front desk delivery is allowed.",
		AgencyConsent:   true, PrivacyConsent: true,
		Locale: "en-US", CopyVersion: ProcurementAuthorizationCopyV1,
	}
}

func TestApplyPreflightComputesCeilingOnePercentFee(t *testing.T) {
	session := readySession(t)
	if session.PassThroughTotal.AmountMinor != 1050 || session.AgencyFee.AmountMinor != 11 ||
		session.CustomerPayableTotal.AmountMinor != 1061 {
		t.Fatalf("unexpected totals: %#v %#v %#v", session.PassThroughTotal, session.AgencyFee, session.CustomerPayableTotal)
	}
}

func TestBuyerReviewEscalationWithConfirmedQuoteCanIssue(t *testing.T) {
	session := readySession(t)
	checkout := session.MerchantCheckouts[0]
	checkout.ProviderStatus = "requires_escalation"
	checkout.ProcurementHandling = ProcurementHandlingOperatorLater
	checkout.ProviderNotices = []ProviderNotice{{Source: "MESSAGE", Severity: "requires_buyer_review", Code: "extension_interaction_required", Presentation: "NOTICE", Audience: "CUSTOMER_AND_OPERATOR", Registered: true}}
	session.State = SessionPreflighting
	if err := session.ApplyPreflight([]MerchantCheckout{checkout}, session.CreatedAt); err != nil {
		t.Fatal(err)
	}
	if session.State != SessionReady {
		t.Fatalf("review-only quote was blocked: %#v", session)
	}
	if _, _, err := Issue(session, IssueInput{OrderID: "o", InstructionID: "i", IdempotencyKeyHash: "k", DisclosureVersion: "v", ProcurementApproval: testProcurementApproval(), Now: session.CreatedAt}); err != nil {
		t.Fatalf("review-only quote was not issuable: %v", err)
	}
}

func TestUnknownBuyerInputWithConfirmedQuoteIsOperatorLater(t *testing.T) {
	for _, environment := range []string{"TEST", "SANDBOX", "LIVE"} {
		for _, effect := range []string{"NO_REAL_VALUE", "REAL_MONEY"} {
			session := readySession(t)
			now := session.CreatedAt
			session.State = SessionPreflighting
			checkout := session.MerchantCheckouts[0]
			checkout.ProviderStatus = "requires_escalation"
			checkout.ProcurementHandling = ProcurementHandlingOperatorLater
			checkout.ProviderNotices = []ProviderNotice{{Source: "MESSAGE", Severity: "requires_buyer_input", Code: "unsupported_buyer_input", Presentation: "INTERNAL", Audience: "OPERATOR", Registered: false}}
			if err := session.ApplyPreflight([]MerchantCheckout{checkout}, now); err != nil {
				t.Fatalf("%s/%s: %v", environment, effect, err)
			}
			if session.State != SessionReady || session.BlockReason != "" {
				t.Fatalf("%s/%s operator-later quote was blocked: %#v", environment, effect, session)
			}
			if len(session.MerchantCheckouts) != 1 ||
				session.MerchantCheckouts[0].ShopDomain != "shop.example" ||
				len(session.MerchantCheckouts[0].ProviderNotices) != 1 ||
				session.MerchantCheckouts[0].ProviderNotices[0].Registered {
				t.Fatalf("%s/%s session lost unregistered observation: %#v", environment, effect, session.MerchantCheckouts)
			}
			if _, _, err := Issue(session, IssueInput{OrderID: "o", InstructionID: "i", IdempotencyKeyHash: "k", DisclosureVersion: "v", ProcurementApproval: testProcurementApproval(), Now: now}); err != nil {
				t.Fatalf("%s/%s issue err=%v", environment, effect, err)
			}
		}
	}
}

func TestExtensionInteractionBuyerInputWithConfirmedQuoteCanIssue(t *testing.T) {
	// ADR-0049: 해소 가능 buyer-input(extension_interaction_required)은 가격
	// 증거가 확정이면 BUYER_REVIEW와 같은 수동 인계 발행 경로를 탄다.
	session := readySession(t)
	checkout := session.MerchantCheckouts[0]
	checkout.ProviderStatus = "requires_escalation"
	checkout.ProcurementHandling = ProcurementHandlingOperatorLater
	checkout.ProviderNotices = []ProviderNotice{{Source: "MESSAGE", Severity: "requires_buyer_input", Code: "extension_interaction_required", Presentation: "INTERNAL", Audience: "OPERATOR", Registered: true}}
	session.State = SessionPreflighting
	if err := session.ApplyPreflight([]MerchantCheckout{checkout}, session.CreatedAt); err != nil {
		t.Fatal(err)
	}
	if session.State != SessionReady {
		t.Fatalf("resolvable buyer-input quote was blocked: %#v", session)
	}
	if _, _, err := Issue(session, IssueInput{OrderID: "o", InstructionID: "i", IdempotencyKeyHash: "k", DisclosureVersion: "v", ProcurementApproval: testProcurementApproval(), Now: session.CreatedAt}); err != nil {
		t.Fatalf("resolvable buyer-input quote was not issuable: %v", err)
	}
}

func TestFourShopProviderCodesDoNotRegressConfirmedOrder(t *testing.T) {
	session := readySession(t)
	base := session.MerchantCheckouts[0]
	base.ProcurementHandling = ProcurementHandlingOperatorLater
	base.ProviderStatus = "requires_escalation"
	base.ProviderNotices = []ProviderNotice{{Source: "MESSAGE", Type: "error", Severity: "requires_buyer_input", Code: "extension_interaction_required", Presentation: "INTERNAL", Audience: "OPERATOR", Registered: true}}
	checkouts := make([]MerchantCheckout, 0, 4)
	shops := []string{"bigidesign.com", "tomsstudio.com", "www.ellingtonpens.com", "www.gouletpens.com"}
	for index, shop := range shops {
		checkout := base
		checkout.ShopDomain = shop
		checkout.MerchantID = shop
		checkout.LineRefs = []string{fmt.Sprintf("line-%d", index+1)}
		checkout.DeliveryGroups = []DeliveryGroup{{
			ID: fmt.Sprintf("group-%d", index+1), LineRefs: checkout.LineRefs,
			Options:           []DeliveryOption{{ID: "standard", Title: "Standard", Currency: "USD"}},
			SelectedOptionRef: "standard",
		}}
		checkout.QuoteFingerprint = fmt.Sprintf("quote-%d", index+1)
		checkout.EvidenceHash = fmt.Sprintf("evidence-%d", index+1)
		if shop == "tomsstudio.com" {
			checkout.ProviderNotices = append(append([]ProviderNotice(nil), checkout.ProviderNotices...), ProviderNotice{
				Source: "MESSAGE", Type: "error", Severity: "requires_buyer_input", Code: "redirect_to_checkout_required",
				Presentation: "INTERNAL", Audience: "OPERATOR", Registered: true,
			})
		}
		checkouts = append(checkouts, checkout)
		if index > 0 {
			line := session.Lines[0]
			line.LineID = fmt.Sprintf("line-%d", index+1)
			line.ShopDomain = shop
			session.Lines = append(session.Lines, line)
		} else {
			session.Lines[0].ShopDomain = shop
		}
	}
	session.State = SessionPreflighting
	if err := session.ApplyPreflight(checkouts, session.CreatedAt); err != nil {
		t.Fatal(err)
	}
	if session.State != SessionReady || len(session.MerchantCheckouts) != 4 {
		t.Fatalf("four-shop confirmed order regressed: %#v", session)
	}
}

func TestCustomerCorrectionAndImpossibleBlockWithExactReasons(t *testing.T) {
	for handling, reason := range map[string]BlockReason{
		ProcurementHandlingCustomerCorrection: BlockCheckoutCustomerCorrection,
		ProcurementHandlingImpossible:         BlockCheckoutImpossible,
	} {
		session := readySession(t)
		checkout := session.MerchantCheckouts[0]
		checkout.ProcurementHandling = handling
		session.State = SessionPreflighting
		if err := session.ApplyPreflight([]MerchantCheckout{checkout}, session.CreatedAt); err != nil {
			t.Fatal(err)
		}
		if session.State != SessionBlocked || session.BlockReason != reason {
			t.Fatalf("handling=%s session=%#v", handling, session)
		}
	}
}

func TestContinueURLIsOptionalReferenceForManualProcurement(t *testing.T) {
	session := readySession(t)
	checkout := session.MerchantCheckouts[0]
	checkout.ContinueURLSafeRef = ""
	checkout.ContinueURLHash = ""
	session.State = SessionPreflighting
	if err := session.ApplyPreflight([]MerchantCheckout{checkout}, session.CreatedAt); err != nil {
		t.Fatal(err)
	}
	if session.State != SessionReady {
		t.Fatalf("missing optional continue URL blocked manual procurement: %#v", session)
	}
}

func TestDutiesUnknownBlocks(t *testing.T) {
	now := time.Now().UTC()
	session := OrderSheetSession{PaymentSelection: PaymentRailTVITUSD, State: SessionPreflighting}
	if err := session.ApplyPreflight([]MerchantCheckout{{ProviderStatus: "ready_for_complete", DutiesDisposition: "UNKNOWN"}}, now); err != nil {
		t.Fatal(err)
	}
	if session.BlockReason != BlockCrossBorderOrDuties {
		t.Fatalf("reason=%s", session.BlockReason)
	}
}

func TestIssueCreatesExactOrderAndInstruction(t *testing.T) {
	session := readySession(t)
	now := session.CreatedAt.Add(time.Minute).Add(456789 * time.Nanosecond)
	order, instruction, err := Issue(session, IssueInput{OrderID: "order-1", InstructionID: "instruction-1", IdempotencyKeyHash: "key-hash", DisclosureVersion: "2026-08-14", ProcurementApproval: testProcurementApproval(), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if order.SnapshotHash == "" || instruction.AgencyOrderSnapshotHash != order.SnapshotHash ||
		instruction.CustomerPayableTotal != order.CustomerPayableTotal || instruction.State != "PENDING" {
		t.Fatalf("order/instruction mismatch: %#v %#v", order, instruction)
	}
	if want := now.UTC().Round(time.Microsecond); !order.IssuedAt.Equal(want) ||
		!order.ProcurementAuthorization.AcceptedAt.Equal(want) || order.IssuedAt.Nanosecond()%1_000 != 0 {
		t.Fatalf("issuance timestamps are not sealed at PostgreSQL precision: order=%s authorization=%s want=%s",
			order.IssuedAt.Format(time.RFC3339Nano),
			order.ProcurementAuthorization.AcceptedAt.Format(time.RFC3339Nano),
			want.Format(time.RFC3339Nano))
	}
	authorization := order.ProcurementAuthorization
	if authorization.Kind != ProcurementAuthorizationManualOperator ||
		authorization.AuthorizationHash == "" || authorization.ExecutionProfileHash == "" ||
		authorization.DisplayedSnapshotHash != session.DisplayedSnapshotHash ||
		authorization.SourceCartSnapshotHash != session.SourceCart.SnapshotHash ||
		len(authorization.Shops) != 1 || len(authorization.Shops[0].Lines) != 1 ||
		authorization.Shops[0].Lines[0].VariantTitle != "Blue / M" ||
		authorization.ApprovedCustomerPayable != order.CustomerPayableTotal ||
		authorization.CustomerApproval.OrderMessage != "Leave the receipt in the package." {
		t.Fatalf("incomplete ProcurementAuthorization: %#v", authorization)
	}
	if err := authorization.VerifyHash(); err != nil {
		t.Fatalf("authorization hash did not verify: %v", err)
	}
	if err := authorization.ValidateForOrder(order); err != nil {
		t.Fatalf("authorization was not bound to its order: %v", err)
	}
	authorization.CustomerApproval.OrderMessage = "mutated"
	if err := authorization.VerifyHash(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mutated authorization hash err=%v", err)
	}
}

func TestProcurementAuthorizationRequiresConsentAndBoundsCustomerMessages(t *testing.T) {
	session := readySession(t)
	input := IssueInput{
		OrderID: "o", InstructionID: "i", IdempotencyKeyHash: "k",
		DisclosureVersion: "v", ProcurementApproval: testProcurementApproval(), Now: session.CreatedAt,
	}
	input.ProcurementApproval.PrivacyConsent = false
	if _, _, err := Issue(session, input); !errors.Is(err, ErrProcurementAuthorizationInvalid) {
		t.Fatalf("missing privacy consent err=%v", err)
	}
	input.ProcurementApproval = testProcurementApproval()
	input.ProcurementApproval.OrderMessage = strings.Repeat("가", 501)
	if _, _, err := Issue(session, input); !errors.Is(err, ErrProcurementAuthorizationInvalid) {
		t.Fatalf("oversized order message err=%v", err)
	}
}

func TestProcurementAuthorizationCapturesProviderNoticesAndTerms(t *testing.T) {
	session := readySession(t)
	checkout := session.MerchantCheckouts[0]
	checkout.ProviderNotices = []ProviderNotice{{
		Source: "MESSAGE", Severity: "requires_buyer_review", Code: "review_return_policy",
		Text: "Review the return policy before purchase.", Presentation: "NOTICE",
		Audience: "CUSTOMER_AND_OPERATOR", Registered: false,
	}}
	checkout.PolicyLinks = []ProviderPolicyLink{{
		Kind: "RETURN", Label: "Returns", URL: "https://shop.example/policies/returns",
	}}
	// The displayed hash must bind the provider presentation evidence too.
	session.State = SessionPreflighting
	if err := session.ApplyPreflight([]MerchantCheckout{checkout}, session.CreatedAt); err != nil {
		t.Fatal(err)
	}
	order, _, err := Issue(session, IssueInput{
		OrderID: "o", InstructionID: "i", IdempotencyKeyHash: "k",
		DisclosureVersion: "v", ProcurementApproval: testProcurementApproval(), Now: session.CreatedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	shop := order.ProcurementAuthorization.Shops[0]
	if shop.Conditions.ReturnStatus != ProcurementConditionProviderSnapshot ||
		len(shop.ProviderNotices) != 1 || shop.ProviderNotices[0].Registered {
		t.Fatalf("provider terms were not captured: %#v", shop)
	}
}

func TestPayPalSandboxFeeAndIssueSelection(t *testing.T) {
	session := readySession(t)
	// PAYPAL_SANDBOX 재선택: 수수료 정책만 바뀌고 preflight 절차는 동일하다.
	if err := session.SelectRail(PaymentRailPayPalSandbox); err != nil {
		t.Fatal(err)
	}
	checkouts := append([]MerchantCheckout(nil), session.MerchantCheckouts...)
	if err := session.ApplyPreflight(checkouts, session.CreatedAt); err != nil {
		t.Fatal(err)
	}
	// P=1050 → variable=ceil(1050*540bps)=57, fixed=30, payable=1137.
	if session.AgencyFee.AmountMinor != 87 || session.CustomerPayableTotal.AmountMinor != 1137 {
		t.Fatalf("unexpected PayPal fee: %#v %#v", session.AgencyFee, session.CustomerPayableTotal)
	}
	order, instruction, err := Issue(session, IssueInput{
		OrderID: "o", InstructionID: "i", IdempotencyKeyHash: "k",
		DisclosureVersion: "v", ProcurementApproval: testProcurementApproval(), Now: session.CreatedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	selection := order.PaymentSelection
	if selection.Rail != "PAYPAL" || selection.ProviderEnvironment != "SANDBOX" ||
		selection.Asset != "USD" || selection.EconomicEffect != "NO_REAL_VALUE" ||
		selection.MerchantExecution != "SIMULATED" {
		t.Fatalf("unexpected payment selection: %#v", selection)
	}
	fee := order.AgencyFee
	if fee.VariableAmount.AmountMinor != 57 || fee.FixedAmount.AmountMinor != 30 ||
		fee.Total.AmountMinor != 87 ||
		fee.PolicyVersion != "PAYPAL_MO_PASS_THROUGH_540BPS_PLUS_30C_V1" ||
		len(fee.MerchantOrders) != 1 || fee.MerchantOrders[0].CheckoutOrdinal != 1 ||
		fee.MerchantOrders[0].CustomerPayableTotal.AmountMinor != 1137 {
		t.Fatalf("unexpected fee breakdown: %#v", fee)
	}
	if instruction.PaymentSelection.Rail != "PAYPAL" ||
		instruction.CustomerPayableTotal.AmountMinor != 1137 {
		t.Fatalf("unexpected instruction: %#v", instruction)
	}
}

func TestRailFeeAllocationIsImmutablePerMerchantCheckout(t *testing.T) {
	secondCheckout := func(session OrderSheetSession) MerchantCheckout {
		checkout := session.MerchantCheckouts[0]
		checkout.MerchantID = "second.example"
		checkout.ShopDomain = "second.example"
		checkout.LineRefs = []string{"line-2"}
		checkout.DeliveryGroups = []DeliveryGroup{{
			ID: "group-2", LineRefs: []string{"line-2"},
			Options:           []DeliveryOption{{ID: "standard", Title: "Standard", Currency: "USD"}},
			SelectedOptionRef: "standard",
		}}
		checkout.AuthoritativeTotal = Money{AmountMinor: 100, Currency: "USD"}
		checkout.TaxTotal = Money{Currency: "USD"}
		checkout.QuoteFingerprint = "quote-2"
		checkout.EvidenceHash = "evidence-2"
		return checkout
	}
	addSecondLine := func(session *OrderSheetSession) {
		session.Lines = append(session.Lines, ExactLine{
			LineID: "line-2", SourceCartItemID: "item-2", ShopDomain: "second.example",
			ProductURL: "https://second.example/products/test", ProductTitle: "Second product",
			VariantID: "gid://shopify/ProductVariant/2", VariantTitle: "Default",
			Quantity: 1, UnitPrice: Money{AmountMinor: 100, Currency: "USD"},
			LineSubtotal: Money{AmountMinor: 100, Currency: "USD"},
		})
	}

	t.Run("paypal sums independently rounded MO fees", func(t *testing.T) {
		session := readySession(t)
		addSecondLine(&session)
		if err := session.SelectRail(PaymentRailPayPalSandbox); err != nil {
			t.Fatal(err)
		}
		checkouts := append([]MerchantCheckout(nil), session.MerchantCheckouts...)
		checkouts = append(checkouts, secondCheckout(session))
		if err := session.ApplyPreflight(checkouts, session.CreatedAt); err != nil {
			t.Fatal(err)
		}
		order, _, err := Issue(session, IssueInput{
			OrderID: "multi-paypal", InstructionID: "instruction", IdempotencyKeyHash: "key",
			DisclosureVersion: "v", ProcurementApproval: testProcurementApproval(), Now: session.CreatedAt,
		})
		if err != nil {
			t.Fatal(err)
		}
		fee := order.AgencyFee
		if order.PassThroughTotal.AmountMinor != 1150 || fee.VariableAmount.AmountMinor != 63 ||
			fee.FixedAmount.AmountMinor != 60 || fee.Total.AmountMinor != 123 ||
			order.CustomerPayableTotal.AmountMinor != 1273 || len(fee.MerchantOrders) != 2 {
			t.Fatalf("unexpected multi-MO PayPal fee: %+v order=%+v", fee, order)
		}
		if fee.MerchantOrders[0].Total.AmountMinor != 87 ||
			fee.MerchantOrders[1].Total.AmountMinor != 36 {
			t.Fatalf("PayPal MO fees were not independently rounded: %+v", fee.MerchantOrders)
		}
	})

	t.Run("tvit rounds order once and allocates by checkout ordinal", func(t *testing.T) {
		session := readySession(t)
		addSecondLine(&session)
		session.State = SessionPreflighting
		checkouts := append([]MerchantCheckout(nil), session.MerchantCheckouts...)
		checkouts = append(checkouts, secondCheckout(session))
		if err := session.ApplyPreflight(checkouts, session.CreatedAt); err != nil {
			t.Fatal(err)
		}
		order, _, err := Issue(session, IssueInput{
			OrderID: "multi-tvit", InstructionID: "instruction", IdempotencyKeyHash: "key",
			DisclosureVersion: "v", ProcurementApproval: testProcurementApproval(), Now: session.CreatedAt,
		})
		if err != nil {
			t.Fatal(err)
		}
		fee := order.AgencyFee
		if fee.Total.AmountMinor != 12 || fee.PolicyVersion != "TVITUSD_ORDER_PASS_THROUGH_100BPS_MO_ALLOC_V1" ||
			len(fee.MerchantOrders) != 2 || fee.MerchantOrders[0].Total.AmountMinor != 11 ||
			fee.MerchantOrders[1].Total.AmountMinor != 1 {
			t.Fatalf("unexpected tVIT MO fee allocation: %+v", fee)
		}
	})
}

func TestPayPalLiveIssueBuildsRealMoneyProfileWithoutExternalEffect(t *testing.T) {
	session := readySession(t)
	if err := session.SelectRail(PaymentRailPayPalLive); err != nil {
		t.Fatal(err)
	}
	if err := session.ApplyPreflight(
		append([]MerchantCheckout(nil), session.MerchantCheckouts...), session.CreatedAt,
	); err != nil {
		t.Fatal(err)
	}
	order, instruction, err := Issue(session, IssueInput{
		OrderID: "o-live", InstructionID: "i-live", IdempotencyKeyHash: "k-live",
		DisclosureVersion: "v", ProcurementApproval: testProcurementApproval(),
		Now: session.CreatedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	expectedProfile, err := ExecutionProfileForRail(PaymentRailPayPalLive)
	if err != nil {
		t.Fatal(err)
	}
	if order.ExecutionProfile != expectedProfile ||
		order.ExecutionProfileHash != "0xba51a8eb9a32c1c6ede94a0ad7b8eb75b81ab1a1028dd7c536219895c37f096a" ||
		instruction.ExecutionProfileHash != order.ExecutionProfileHash ||
		order.ProcurementAuthorization.ExecutionProfileHash != order.ExecutionProfileHash {
		t.Fatalf("Live profile handoff mismatch: order=%+v instruction=%+v",
			order.ExecutionProfile, instruction)
	}
	if order.PaymentSelection.ProviderEnvironment != "LIVE" ||
		order.PaymentSelection.EconomicEffect != "REAL_MONEY" ||
		order.PaymentSelection.MerchantExecution != "LIVE" ||
		order.AgencyFee.PolicyVersion != "PAYPAL_MO_PASS_THROUGH_540BPS_PLUS_30C_V1" {
		t.Fatalf("unexpected Live issuance facts: selection=%+v fee=%+v",
			order.PaymentSelection, order.AgencyFee)
	}
}

func TestSelectRailRejectsUnknownRail(t *testing.T) {
	session := readySession(t)
	if err := session.SelectRail(PaymentRail("PAYPAL_COMING_SOON")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown rail must be rejected, got %v", err)
	}
}
