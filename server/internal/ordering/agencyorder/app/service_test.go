package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func TestShippingDraftUsesPublicLowerCamelCaseFields(t *testing.T) {
	payload, err := json.Marshal(ShippingDraft{Address: ShippingAddress{
		RecipientName: "Test Buyer", AddressLine1: "123 Test Street", Country: "US",
	}})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Address map[string]any `json:"Address"`
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.Address["addressLine1"] != "123 Test Street" {
		t.Fatalf("shipping wire format is not lowerCamelCase: %s", payload)
	}
}

type testClock struct{ now time.Time }

func (c testClock) Now() time.Time { return c.now }

type testTransactor struct{}

func (testTransactor) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type testIDs struct{ values []string }

func (i *testIDs) NewID() string { value := i.values[0]; i.values = i.values[1:]; return value }

type testRepo struct {
	session     agencydomain.OrderSheetSession
	creationKey string
	requestHash string
	order       agencydomain.AgencyOrder
	instruction agencydomain.PaymentInstruction
	failAtomic  bool
}

func (r *testRepo) FindSessionByCreationKey(_ context.Context, _ string, key string) (agencydomain.OrderSheetSession, string, bool, error) {
	return r.session, r.requestHash, r.creationKey == key, nil
}
func (r *testRepo) CreateSession(_ context.Context, value agencydomain.OrderSheetSession, key, requestHash string) error {
	r.session, r.creationKey, r.requestHash = value, key, requestHash
	return nil
}
func (r *testRepo) RetireExpiredSessionCreationKey(_ context.Context, userID, sessionID string) error {
	if r.session.UserID != userID || r.session.ID != sessionID ||
		r.session.State != agencydomain.SessionExpired {
		return agencydomain.ErrStateInvalid
	}
	r.creationKey = "retired:" + sessionID
	return nil
}
func (r *testRepo) GetSession(_ context.Context, userID, id string) (agencydomain.OrderSheetSession, error) {
	if r.session.UserID != userID || r.session.ID != id {
		return agencydomain.OrderSheetSession{}, errors.New("not found")
	}
	return r.session, nil
}
func (r *testRepo) SaveSession(_ context.Context, value agencydomain.OrderSheetSession, expected int64) error {
	if r.session.Version != expected {
		return agencydomain.ErrVersionConflict
	}
	r.session = value
	return nil
}
func (r *testRepo) IssueAtomic(_ context.Context, session agencydomain.OrderSheetSession, expected int64, order agencydomain.AgencyOrder, instruction agencydomain.PaymentInstruction) error {
	if r.failAtomic {
		return errors.New("rollback")
	}
	if r.session.Version != expected {
		return agencydomain.ErrVersionConflict
	}
	r.session, r.order, r.instruction = session, order, instruction
	return nil
}
func (r *testRepo) FindOrderByIdempotencyKey(_ context.Context, _ string, key string) (agencydomain.AgencyOrder, bool, error) {
	return r.order, r.order.IssuanceEvidence.IdempotencyKeyHash == key, nil
}
func (r *testRepo) FindOrderBySessionID(_ context.Context, userID, sessionID string) (agencydomain.AgencyOrder, bool, error) {
	return r.order, r.order.UserID == userID && r.order.IssuanceEvidence.OrderSheetSessionID == sessionID, nil
}
func (r *testRepo) GetOrder(_ context.Context, userID, id string) (agencydomain.AgencyOrder, agencydomain.PaymentInstruction, error) {
	if r.order.UserID != userID || r.order.ID != id {
		return agencydomain.AgencyOrder{}, agencydomain.PaymentInstruction{}, errors.New("not found")
	}
	return r.order, r.instruction, nil
}

type testCart struct {
	value agencydomain.SourceCartSnapshot
}

func (r testCart) ReadCartSnapshot(context.Context, string, string) (agencydomain.SourceCartSnapshot, error) {
	return r.value, nil
}

type testLines struct{ value []agencydomain.ExactLine }

func (r testLines) ResolveExactLines(context.Context, agencydomain.SourceCartSnapshot) ([]agencydomain.ExactLine, error) {
	return r.value, nil
}

type unavailableTestLine struct{}

func (unavailableTestLine) ResolveExactLines(context.Context, agencydomain.SourceCartSnapshot) ([]agencydomain.ExactLine, error) {
	return nil, &agencydomain.OrderPreparationLineError{
		ReasonCode: "VARIANT_UNAVAILABLE",
		ItemTitle:  "Classic Rim Dinnerware Set",
		Retryable:  false,
	}
}

type testShipping struct {
	snapshot agencydomain.ShippingSnapshot
	address  ShippingAddress
}

func (s testShipping) DefaultAddress(context.Context, string) (ShippingAddress, bool, error) {
	return s.address, true, nil
}
func (s testShipping) CreateOrderSheetSnapshot(context.Context, string, ShippingAddress) (agencydomain.ShippingSnapshot, error) {
	return s.snapshot, nil
}
func (s testShipping) RevealSnapshot(context.Context, string, string) (ShippingAddress, error) {
	return s.address, nil
}

type noDefaultShipping struct {
	snapshot agencydomain.ShippingSnapshot
}

func (noDefaultShipping) DefaultAddress(context.Context, string) (ShippingAddress, bool, error) {
	return ShippingAddress{}, false, nil
}
func (s noDefaultShipping) CreateOrderSheetSnapshot(context.Context, string, ShippingAddress) (agencydomain.ShippingSnapshot, error) {
	return s.snapshot, nil
}
func (noDefaultShipping) RevealSnapshot(context.Context, string, string) (ShippingAddress, error) {
	return ShippingAddress{}, nil
}

type testCheckout struct {
	finalChanged      bool
	finalQuoteChanged bool
	status            string
	buyerInput        bool
	requiresSelection bool
	beforeExplore     func()
	cancelCalls       int
}

func (c *testCheckout) Explore(_ context.Context, input MerchantRequest) (agencydomain.MerchantCheckout, error) {
	if c.beforeExplore != nil {
		c.beforeExplore()
	}
	selected := "standard"
	if c.requiresSelection {
		selected = ""
	}
	return agencydomain.MerchantCheckout{MerchantID: input.ShopDomain, ShopDomain: input.ShopDomain,
		StorefrontCartSafeRef: "cart-safe", LineRefs: []string{input.Lines[0].LineID},
		DeliveryGroups: []agencydomain.DeliveryGroup{{ID: "group-1", LineRefs: []string{input.Lines[0].LineID},
			Options: []agencydomain.DeliveryOption{{ID: "standard", Title: "Standard", AmountMinor: 100, Currency: "USD"}}, SelectedOptionRef: selected}},
		DeliveryOptions:           []agencydomain.DeliveryOption{{ID: "standard", Title: "Standard", AmountMinor: 100, Currency: "USD"}},
		SelectedDeliveryOptionRef: "standard"}, nil
}

func TestOrderSheetIdentityExistsBeforeProviderCartWrite(t *testing.T) {
	checkout := &testCheckout{}
	service, repository := testService(t, checkout)
	checkout.beforeExplore = func() {
		if repository.session.ID == "" || repository.session.State != agencydomain.SessionDiscoveringDelivery {
			t.Fatalf("provider write started before durable OrderSheet: %#v", repository.session)
		}
	}
	created, err := service.CreateOrderSheet(context.Background(), CreateOrderSheetInput{
		UserID: "user-1", CurationID: "curation-1", ExpectedCartVersion: 3,
		IdempotencyKey: "create-key-durable", BuyerIP: net.ParseIP("203.0.113.2"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.State != agencydomain.SessionEditing || created.Version != 1 {
		t.Fatalf("created=%#v", created)
	}
	created = setTestShipping(t, service, created, net.ParseIP("203.0.113.2"))
	if created.State != agencydomain.SessionEditing || created.Version < 3 {
		t.Fatalf("addressed=%#v", created)
	}
}

func TestUnavailableExactLineCreatesNoOrderSheetOrPayment(t *testing.T) {
	now := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	repository := &testRepo{}
	checkout := &testCheckout{beforeExplore: func() {
		t.Fatal("unavailable line must stop before provider checkout")
	}}
	service, err := NewService(
		repository,
		testCart{agencydomain.SourceCartSnapshot{
			CartID: "cart-1", CartVersion: 3, SnapshotHash: "cart-hash",
			Items: []agencydomain.CartItemSnapshot{{
				CartItemID: "item-1", ProductTitle: "Classic Rim Dinnerware Set",
			}},
		}},
		unavailableTestLine{},
		noDefaultShipping{},
		checkout, testTransactor{}, testClock{now}, &testIDs{values: []string{"must-not-be-used"}},
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.CreateOrderSheet(context.Background(), CreateOrderSheetInput{
		UserID: "user-1", CurationID: "curation-1", ExpectedCartVersion: 3,
		IdempotencyKey: "unavailable-line-key",
	})
	var lineFailure *agencydomain.OrderPreparationLineError
	if !errors.As(err, &lineFailure) || lineFailure.ItemTitle != "Classic Rim Dinnerware Set" {
		t.Fatalf("expected product-specific line failure, got %v", err)
	}
	if repository.session.ID != "" || repository.order.ID != "" || repository.instruction.ID != "" {
		t.Fatalf("unavailable line wrote commerce state: repo=%#v", repository)
	}
}

func TestOrderSheetCreationDoesNotRequireAnAccountDefaultAddress(t *testing.T) {
	checkout := &testCheckout{beforeExplore: func() {
		t.Fatal("creating an OrderSheet must not call Shopify before its address is confirmed")
	}}
	now := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	repository := &testRepo{}
	service, err := NewService(
		repository,
		testCart{agencydomain.SourceCartSnapshot{
			CartID: "cart-1", CartVersion: 3, SnapshotHash: "cart-hash",
			Items: []agencydomain.CartItemSnapshot{{CartItemID: "item-1"}},
		}},
		testLines{[]agencydomain.ExactLine{{
			LineID: "line-1", SourceCartItemID: "item-1",
			VariantID: "gid://shopify/ProductVariant/1", ShopDomain: "shop.example", Quantity: 1,
		}}},
		noDefaultShipping{snapshot: agencydomain.ShippingSnapshot{
			SnapshotRef: "shipping-1", SnapshotRevision: 1,
			SnapshotHash: "shipping-hash", Country: "US",
		}},
		checkout, testTransactor{}, testClock{now}, &testIDs{values: []string{"sheet-1"}},
	)
	if err != nil {
		t.Fatal(err)
	}

	session, err := service.CreateOrderSheet(context.Background(), CreateOrderSheetInput{
		UserID: "user-1", CurationID: "curation-1", ExpectedCartVersion: 3,
		IdempotencyKey: "create-without-account-default",
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.State != agencydomain.SessionEditing || session.ShippingAddress.SnapshotRef != "" {
		t.Fatalf("unexpected address-free OrderSheet: %#v", session)
	}
	if session.MerchantCheckouts == nil || session.PassThroughTotal.Currency != "USD" {
		t.Fatalf("new OrderSheet collections and money must have API-safe zero values: %#v", session)
	}
	draft, err := service.ShippingDraft(context.Background(), "user-1", session)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Source != "EMPTY" || draft.Address.Country != "US" {
		t.Fatalf("unexpected empty shipping draft: %#v", draft)
	}
}

func TestOrderSheetRejectsNonUSAddressBeforeProviderWrite(t *testing.T) {
	checkout := &testCheckout{beforeExplore: func() {
		t.Fatal("an unsupported shipping address must not call Shopify")
	}}
	service, _ := testService(t, checkout)
	session, err := service.CreateOrderSheet(context.Background(), CreateOrderSheetInput{
		UserID: "user-1", CurationID: "curation-1", ExpectedCartVersion: 3,
		IdempotencyKey: "create-non-us-address",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.SetShippingAddress(context.Background(), SetShippingAddressInput{
		UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version,
		Address: ShippingAddress{
			RecipientName: "Test Buyer", AddressLine1: "1 Test Road",
			City: "Toronto", Region: "ON", PostalCode: "M5V 3A8", Country: "CA",
		},
	})
	if failure, ok := fault.As(err); !ok || failure.Reason != "SHIPPING_ADDRESS_US_ONLY" {
		t.Fatalf("expected SHIPPING_ADDRESS_US_ONLY, got %v", err)
	}
}

func TestNonUSAccountDefaultIsNotUsedAsAnOrderSheetDraft(t *testing.T) {
	service, _ := testService(t, &testCheckout{})
	service.shipping = testShipping{address: ShippingAddress{
		RecipientName: "Test Buyer", AddressLine1: "1 Test Road",
		City: "Toronto", Region: "ON", PostalCode: "M5V 3A8", Country: "CA",
	}}
	session, err := service.CreateOrderSheet(context.Background(), CreateOrderSheetInput{
		UserID: "user-1", CurationID: "curation-1", ExpectedCartVersion: 3,
		IdempotencyKey: "create-with-ca-account-default",
	})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := service.ShippingDraft(context.Background(), "user-1", session)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Source != "EMPTY" || draft.Address.Country != "US" || draft.Address.AddressLine1 != "" {
		t.Fatalf("non-US Account address leaked into US OrderSheet: %#v", draft)
	}
}
func (_ *testCheckout) SelectDelivery(_ context.Context, input MerchantRequest, selections map[string]string) (agencydomain.MerchantCheckout, error) {
	value := *input.Existing
	value.DeliveryGroups = append([]agencydomain.DeliveryGroup(nil), value.DeliveryGroups...)
	value.DeliveryGroups[0].SelectedOptionRef = selections[value.DeliveryGroups[0].ID]
	return value, nil
}
func (c *testCheckout) Preflight(_ context.Context, input MerchantRequest) (agencydomain.MerchantCheckout, error) {
	status := c.status
	if status == "" {
		status = "ready_for_complete"
	}
	checkout := *input.Existing
	checkout.CheckoutSessionSafeRef = "checkout-safe"
	checkout.ProviderStatus = status
	checkout.Totals = []agencydomain.Total{{Type: "subtotal", AmountMinor: 1000}, {Type: "fulfillment", AmountMinor: 100}, {Type: "tax", AmountMinor: 80}, {Type: "total", AmountMinor: 1180}}
	checkout.AuthoritativeTotal = agencydomain.Money{AmountMinor: 1180, Currency: "USD"}
	checkout.TaxTotal = agencydomain.Money{AmountMinor: 80, Currency: "USD"}
	checkout.DutiesDisposition = agencydomain.DutiesNoSignalAtPreflight
	checkout.AuthEvidence = agencydomain.AuthEvidence{Tier: "TOKEN", ScopesHash: "scope", AgentProfileURL: "https://vitlane.example/.well-known/ucp-agent.json", AgentProfileVersion: "2026-04-08", AgentProfileHash: "profile", CompletionPermission: agencydomain.CompletionNotRequested}
	checkout.CompletionRoute = agencydomain.CompletionManualHandoff
	checkout.QuoteReadiness = agencydomain.PricingConfirmed
	checkout.ProcurementHandling = agencydomain.ProcurementHandlingNormal
	checkout.ContinueURLSafeRef = "continue-safe"
	checkout.ContinueURLHash = "continue-hash"
	checkout.QuoteFingerprint = "stable-quote"
	if status == "requires_escalation" {
		checkout.ProcurementHandling = agencydomain.ProcurementHandlingOperatorLater
		checkout.ProviderNotices = []agencydomain.ProviderNotice{{Source: "MESSAGE", Severity: "requires_buyer_review", Presentation: "NOTICE", Audience: "CUSTOMER_AND_OPERATOR", Registered: true}}
	}
	if c.buyerInput {
		checkout.ProcurementHandling = agencydomain.ProcurementHandlingOperatorLater
		checkout.ProviderNotices = []agencydomain.ProviderNotice{{Source: "MESSAGE", Severity: "requires_buyer_input", Code: "unregistered_input", Presentation: "INTERNAL", Audience: "OPERATOR", Registered: false}}
	}
	checkout.ObservedAt = time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	checkout.ExpiresAt = checkout.ObservedAt.Add(20 * time.Minute)
	checkout.EvidenceHash = "stable-evidence"
	return checkout, nil
}
func (c *testCheckout) FinalGet(ctx context.Context, input MerchantRequest) (agencydomain.MerchantCheckout, error) {
	value, err := c.Preflight(ctx, input)
	if c.finalChanged {
		value.EvidenceHash = "changed"
		value.ProviderStatus = "requires_escalation"
		value.ProcurementHandling = agencydomain.ProcurementHandlingOperatorLater
		value.ProviderNotices = []agencydomain.ProviderNotice{{Source: "MESSAGE", Code: "new_read_only_code", Presentation: "INTERNAL", Audience: "OPERATOR", Registered: false}}
	}
	if c.finalQuoteChanged {
		value.EvidenceHash = "changed-quote"
		value.QuoteFingerprint = "changed-quote"
	}
	return value, err
}
func (c *testCheckout) Cancel(context.Context, MerchantRequest) error {
	c.cancelCalls++
	return nil
}

func setTestShipping(t *testing.T, service *Service, session agencydomain.OrderSheetSession, buyerIP net.IP) agencydomain.OrderSheetSession {
	t.Helper()
	updated, err := service.SetShippingAddress(context.Background(), SetShippingAddressInput{
		UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version,
		Address: ShippingAddress{
			RecipientName: "Test Buyer", AddressLine1: "123 Test Street",
			City: "Seattle", Region: "WA", PostalCode: "98101", Country: "US",
			Phone: "+12065550123",
		},
		BuyerIP: buyerIP,
	})
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func TestCustomerCorrectionCancelsOldCheckoutAndBuildsFreshAddressGraph(t *testing.T) {
	checkout := &testCheckout{}
	service, repository := testService(t, checkout)
	ctx := context.Background()
	session, err := service.CreateOrderSheet(ctx, CreateOrderSheetInput{
		UserID: "user-1", CurationID: "curation-1", ExpectedCartVersion: 3,
		IdempotencyKey: "customer-correction-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	session = setTestShipping(t, service, session, net.ParseIP("203.0.113.2"))
	session, err = service.Preflight(ctx, PreflightInput{
		UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version,
	})
	if err != nil {
		t.Fatal(err)
	}
	blocked := session
	blocked.MerchantCheckouts[0].ProcurementHandling = agencydomain.ProcurementHandlingCustomerCorrection
	blocked.Block(agencydomain.BlockCheckoutCustomerCorrection)
	repository.session = blocked

	updated, err := service.SetShippingAddress(ctx, SetShippingAddressInput{
		UserID: "user-1", SessionID: blocked.ID, ExpectedVersion: blocked.Version,
		Address: ShippingAddress{
			RecipientName: "Corrected Buyer", AddressLine1: "500 Corrected Avenue",
			City: "Seattle", Region: "WA", PostalCode: "98109", Country: "US",
			Phone: "+12065550199",
		},
		BuyerIP: net.ParseIP("203.0.113.2"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if checkout.cancelCalls != 1 || updated.State == agencydomain.SessionBlocked ||
		updated.PaymentSelection != "" || updated.BlockReason != "" ||
		updated.MerchantCheckouts[0].CheckoutSessionSafeRef != "" {
		t.Fatalf("cancelCalls=%d updated=%#v", checkout.cancelCalls, updated)
	}
}

func testService(t *testing.T, checkout *testCheckout) (*Service, *testRepo) {
	t.Helper()
	now := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	repo := &testRepo{}
	service, err := NewService(repo,
		testCart{agencydomain.SourceCartSnapshot{CartID: "cart-1", CartVersion: 3, SnapshotHash: "cart-hash", Items: []agencydomain.CartItemSnapshot{{CartItemID: "item-1"}}}},
		testLines{[]agencydomain.ExactLine{{
			LineID: "line-1", SourceCartItemID: "item-1",
			VariantID: "gid://shopify/ProductVariant/1", ShopDomain: "shop.example",
			ProductURL: "https://shop.example/products/test", ProductTitle: "Test product",
			VariantTitle: "Blue / M", SelectedOptions: []string{"Color: Blue", "Size: M"},
			Quantity: 1, UnitPrice: agencydomain.Money{AmountMinor: 1000, Currency: "USD"},
			LineSubtotal: agencydomain.Money{AmountMinor: 1000, Currency: "USD"},
		}}},
		testShipping{agencydomain.ShippingSnapshot{SnapshotRef: "shipping-1", SnapshotRevision: 1, SnapshotHash: "shipping-hash", Country: "US"}, ShippingAddress{Country: "US"}},
		checkout, testTransactor{}, testClock{now}, &testIDs{values: []string{
			"sheet-1", "order-1", "instruction-1",
			"order-2", "instruction-2", "order-3", "instruction-3",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	return service, repo
}

func testProcurementApproval() agencydomain.ProcurementApproval {
	return agencydomain.ProcurementApproval{
		OrderMessage:    "Include the merchant receipt.",
		DeliveryMessage: "Deliver to the front desk.",
		AgencyConsent:   true, PrivacyConsent: true,
		Locale: "en-US", CopyVersion: agencydomain.ProcurementAuthorizationCopyV1,
	}
}

func TestVerticalCreatesPreflightsAndIssuesAtomically(t *testing.T) {
	service, repo := testService(t, &testCheckout{})
	ctx := context.Background()
	session, err := service.CreateOrderSheet(ctx, CreateOrderSheetInput{UserID: "user-1", CurationID: "curation-1", ExpectedCartVersion: 3, IdempotencyKey: "create-key-123", BuyerIP: net.ParseIP("203.0.113.2")})
	if err != nil {
		t.Fatal(err)
	}
	session = setTestShipping(t, service, session, net.ParseIP("203.0.113.2"))
	session, err = service.Preflight(ctx, PreflightInput{UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version})
	if err != nil {
		t.Fatal(err)
	}
	order, instruction, err := service.Issue(ctx, IssueOrderInput{UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version, DisplayedSnapshotHash: session.DisplayedSnapshotHash, IdempotencyKey: "issue-key-123", ProcurementApproval: testProcurementApproval()})
	if err != nil {
		t.Fatal(err)
	}
	if repo.session.State != agencydomain.SessionConsumed || repo.order.ID != order.ID || repo.instruction.ID != instruction.ID || instruction.AgencyOrderSnapshotHash != order.SnapshotHash {
		t.Fatalf("atomic result mismatch: %#v %#v %#v", repo.session, order, instruction)
	}
}

type testLiveIssueGate struct {
	allowed        bool
	revision       int64
	lockedAllow    *bool
	lockedRevision *int64
}

func (g *testLiveIssueGate) AllowLiveOrderIssue(context.Context) (bool, int64, error) {
	return g.allowed, g.revision, nil
}
func (g *testLiveIssueGate) LockLiveOrderIssueAdmission(context.Context) (bool, int64, error) {
	allowed, revision := g.allowed, g.revision
	if g.lockedAllow != nil {
		allowed = *g.lockedAllow
	}
	if g.lockedRevision != nil {
		revision = *g.lockedRevision
	}
	return allowed, revision, nil
}

func TestIssueRechecksRuntimePayPalLiveGateForPreviouslyReadySession(t *testing.T) {
	service, repo := testService(t, &testCheckout{})
	gate := &testLiveIssueGate{revision: 7}
	service.EnablePayPalLiveIssue()
	service.EnableLiveIssueGate(gate)
	ctx := context.Background()
	session, err := service.CreateOrderSheet(ctx, CreateOrderSheetInput{
		UserID: "user-1", CurationID: "curation-1", ExpectedCartVersion: 3,
		IdempotencyKey: "create-live-before-kill-switch",
	})
	if err != nil {
		t.Fatal(err)
	}
	session = setTestShipping(t, service, session, net.ParseIP("203.0.113.2"))
	session, err = service.Preflight(ctx, PreflightInput{
		UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version,
		PaymentRail: agencydomain.PaymentRailPayPalLive,
	})
	if err != nil {
		t.Fatal(err)
	}
	issue := IssueOrderInput{
		UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version,
		DisplayedSnapshotHash:      session.DisplayedSnapshotHash,
		IdempotencyKey:             "issue-live-after-kill-switch",
		ProcurementApproval:        testProcurementApproval(),
		ExpectedCapabilityRevision: 7,
	}
	if _, _, err := service.Issue(ctx, issue); !errors.Is(err, ErrLiveIssueDisabled) {
		t.Fatalf("closed Live issue gate error=%v", err)
	}
	if repo.order.ID != "" || repo.session.State != agencydomain.SessionReady {
		t.Fatalf("closed gate issued order=%#v session=%#v", repo.order, repo.session)
	}

	gate.allowed = true
	lockedAllowed := false
	gate.lockedAllow = &lockedAllowed
	if _, _, err := service.Issue(ctx, issue); !errors.Is(err, ErrLiveIssueDisabled) {
		t.Fatalf("final locked Live issue gate error=%v", err)
	}
	if repo.order.ID != "" || repo.session.State != agencydomain.SessionReady {
		t.Fatalf("final locked gate issued order=%#v session=%#v", repo.order, repo.session)
	}
	lockedAllowed = true
	order, _, err := service.Issue(ctx, issue)
	if err != nil || order.ExecutionProfile.ProviderEnvironment != "LIVE" {
		t.Fatalf("opened Live issue gate order=%#v err=%v", order, err)
	}
	gate.allowed = false
	replayed, _, err := service.Issue(ctx, issue)
	if err != nil || replayed.ID != order.ID {
		t.Fatalf("closed gate must preserve issued-order replay: order=%#v err=%v", replayed, err)
	}
}

func TestIssueAllowsSandboxWhileLiveIsAlsoEnabled(t *testing.T) {
	service, _ := testService(t, &testCheckout{})
	service.EnablePayPalSandboxIssue()
	ctx := context.Background()
	session, err := service.CreateOrderSheet(ctx, CreateOrderSheetInput{
		UserID: "user-1", CurationID: "curation-1", ExpectedCartVersion: 3,
		IdempotencyKey: "create-sandbox-before-selector-switch",
	})
	if err != nil {
		t.Fatal(err)
	}
	session = setTestShipping(t, service, session, net.ParseIP("203.0.113.2"))
	session, err = service.Preflight(ctx, PreflightInput{
		UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version,
		PaymentRail: agencydomain.PaymentRailPayPalSandbox,
	})
	if err != nil {
		t.Fatal(err)
	}
	issue := IssueOrderInput{
		UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version,
		DisplayedSnapshotHash: session.DisplayedSnapshotHash,
		IdempotencyKey:        "issue-sandbox-after-selector-switch",
		ProcurementApproval:   testProcurementApproval(),
	}
	service.EnablePayPalLiveIssue()
	order, _, err := service.Issue(ctx, issue)
	if err != nil || order.ExecutionProfile.ProviderEnvironment != "SANDBOX" {
		t.Fatalf("Sandbox selector order=%#v err=%v", order, err)
	}
	replayed, _, err := service.Issue(ctx, issue)
	if err != nil || replayed.ID != order.ID {
		t.Fatalf("selector change must preserve issued-order replay: order=%#v err=%v", replayed, err)
	}
}

func TestIssueRejectsMissingProcurementConsentBeforeFinalProviderRead(t *testing.T) {
	checkout := &testCheckout{}
	service, repo := testService(t, checkout)
	ctx := context.Background()
	session, err := service.CreateOrderSheet(ctx, CreateOrderSheetInput{
		UserID: "user-1", CurationID: "curation-1", ExpectedCartVersion: 3,
		IdempotencyKey: "create-authorization-invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	session = setTestShipping(t, service, session, net.ParseIP("203.0.113.2"))
	session, err = service.Preflight(ctx, PreflightInput{
		UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = service.Issue(ctx, IssueOrderInput{
		UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version,
		DisplayedSnapshotHash: session.DisplayedSnapshotHash,
		IdempotencyKey:        "issue-authorization-invalid",
		ProcurementApproval: agencydomain.ProcurementApproval{
			AgencyConsent: true, PrivacyConsent: false,
			Locale: "en-US", CopyVersion: agencydomain.ProcurementAuthorizationCopyV1,
		},
	})
	failure, ok := fault.As(err)
	if !ok || failure.Reason != "PROCUREMENT_AUTHORIZATION_INVALID" || repo.order.ID != "" {
		t.Fatalf("order=%#v err=%v", repo.order, err)
	}
}

func TestIssueIdempotencyBindsExactProcurementApproval(t *testing.T) {
	service, _ := testService(t, &testCheckout{})
	ctx := context.Background()
	session, err := service.CreateOrderSheet(ctx, CreateOrderSheetInput{
		UserID: "user-1", CurationID: "curation-1", ExpectedCartVersion: 3,
		IdempotencyKey: "create-authorization-idempotency",
	})
	if err != nil {
		t.Fatal(err)
	}
	session = setTestShipping(t, service, session, net.ParseIP("203.0.113.2"))
	session, err = service.Preflight(ctx, PreflightInput{
		UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version,
	})
	if err != nil {
		t.Fatal(err)
	}
	approval := testProcurementApproval()
	input := IssueOrderInput{
		UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version,
		DisplayedSnapshotHash: session.DisplayedSnapshotHash,
		IdempotencyKey:        "issue-authorization-idempotency", ProcurementApproval: approval,
	}
	if _, _, err := service.Issue(ctx, input); err != nil {
		t.Fatal(err)
	}
	input.ProcurementApproval.OrderMessage = "A different instruction"
	_, _, err = service.Issue(ctx, input)
	failure, ok := fault.As(err)
	if !ok || failure.Reason != "AGENCY_ORDER_IDEMPOTENCY_CONFLICT" {
		t.Fatalf("changed approval replay err=%v", err)
	}
}

func TestConsumedOrderSheetRecoversExistingOrderAcrossIdempotencyKeys(t *testing.T) {
	service, repo := testService(t, &testCheckout{})
	ctx := context.Background()
	session, err := service.CreateOrderSheet(ctx, CreateOrderSheetInput{
		UserID: "user-1", CurationID: "curation-1", ExpectedCartVersion: 3,
		IdempotencyKey: "create-key-123",
	})
	if err != nil {
		t.Fatal(err)
	}
	session = setTestShipping(t, service, session, net.ParseIP("203.0.113.2"))
	session, err = service.Preflight(ctx, PreflightInput{
		UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version,
	})
	if err != nil {
		t.Fatal(err)
	}
	issued, _, err := service.Issue(ctx, IssueOrderInput{
		UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version,
		DisplayedSnapshotHash: session.DisplayedSnapshotHash, IdempotencyKey: "issue-key-123",
		ProcurementApproval: testProcurementApproval(),
	})
	if err != nil {
		t.Fatal(err)
	}

	replayedSession, err := service.CreateOrderSheet(ctx, CreateOrderSheetInput{
		UserID: "user-1", CurationID: "curation-1", ExpectedCartVersion: 3,
		IdempotencyKey: "create-key-123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if replayedSession.State != agencydomain.SessionConsumed || replayedSession.IssuedAgencyOrderID != issued.ID {
		t.Fatalf("consumed replay=%#v", replayedSession)
	}

	replayedOrder, replayedInstruction, err := service.Issue(ctx, IssueOrderInput{
		UserID: "user-1", SessionID: session.ID, ExpectedVersion: -1,
		DisplayedSnapshotHash: "stale", IdempotencyKey: "different-issue-key-456",
		ProcurementApproval: testProcurementApproval(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if replayedOrder.ID != issued.ID || replayedInstruction.ID != repo.instruction.ID {
		t.Fatalf("order=%#v instruction=%#v", replayedOrder, replayedInstruction)
	}

	_, err = service.Preflight(ctx, PreflightInput{
		UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version,
	})
	var consumed *agencydomain.OrderSheetAlreadyConsumedError
	if !errors.As(err, &consumed) || consumed.AgencyOrderID != issued.ID {
		t.Fatalf("consumed error=%#v err=%v", consumed, err)
	}
}

func TestDeliverySelectionMustBePersistedBeforeCheckoutPreflight(t *testing.T) {
	service, _ := testService(t, &testCheckout{requiresSelection: true})
	ctx := context.Background()
	session, err := service.CreateOrderSheet(ctx, CreateOrderSheetInput{UserID: "user-1", CurationID: "curation-1", ExpectedCartVersion: 3, IdempotencyKey: "create-key-123", BuyerIP: net.ParseIP("203.0.113.2")})
	if err != nil || session.State != agencydomain.SessionEditing {
		t.Fatalf("session=%#v err=%v", session, err)
	}
	session = setTestShipping(t, service, session, net.ParseIP("203.0.113.2"))
	if session.State != agencydomain.SessionDeliverySelectionRequired {
		t.Fatalf("session=%#v", session)
	}
	if _, err := service.Preflight(ctx, PreflightInput{UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version}); err == nil {
		t.Fatal("preflight accepted missing delivery selection")
	}
	session, err = service.SelectDelivery(ctx, SelectDeliveryInput{
		UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version,
		Selections: []MerchantDeliverySelection{{ShopDomain: "shop.example", GroupID: "group-1", OptionID: "standard"}},
	})
	if err != nil || !session.MerchantCheckouts[0].DeliverySelectionComplete() {
		t.Fatalf("selected=%#v err=%v", session, err)
	}
	if _, err := service.Preflight(ctx, PreflightInput{UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version}); err != nil {
		t.Fatal(err)
	}
}

func TestFinalGetReadOnlyNoticeChangeStillIssues(t *testing.T) {
	service, repo := testService(t, &testCheckout{finalChanged: true})
	ctx := context.Background()
	session, err := service.CreateOrderSheet(ctx, CreateOrderSheetInput{UserID: "user-1", CurationID: "curation-1", ExpectedCartVersion: 3, IdempotencyKey: "create-key-123"})
	if err != nil {
		t.Fatal(err)
	}
	session = setTestShipping(t, service, session, net.ParseIP("203.0.113.2"))
	session, err = service.Preflight(ctx, PreflightInput{UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = service.Issue(ctx, IssueOrderInput{UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version, DisplayedSnapshotHash: session.DisplayedSnapshotHash, IdempotencyKey: "issue-key-123", ProcurementApproval: testProcurementApproval()})
	if err != nil || repo.order.ID == "" || repo.instruction.ID == "" || repo.session.State != agencydomain.SessionConsumed {
		t.Fatalf("read-only final notice blocked issue: err=%v order=%#v instruction=%#v session=%#v", err, repo.order, repo.instruction, repo.session)
	}
}

func TestFinalGetQuoteChangeProducesNoOrderOrInstruction(t *testing.T) {
	service, repo := testService(t, &testCheckout{finalQuoteChanged: true})
	ctx := context.Background()
	session, err := service.CreateOrderSheet(ctx, CreateOrderSheetInput{UserID: "user-1", CurationID: "curation-1", ExpectedCartVersion: 3, IdempotencyKey: "create-key-123"})
	if err != nil {
		t.Fatal(err)
	}
	session = setTestShipping(t, service, session, net.ParseIP("203.0.113.2"))
	session, err = service.Preflight(ctx, PreflightInput{UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = service.Issue(ctx, IssueOrderInput{UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version, DisplayedSnapshotHash: session.DisplayedSnapshotHash, IdempotencyKey: "issue-key-123", ProcurementApproval: testProcurementApproval()})
	if err == nil || repo.order.ID != "" || repo.instruction.ID != "" || repo.session.State != agencydomain.SessionReady {
		t.Fatalf("changed final quote escaped: err=%v order=%#v instruction=%#v session=%#v", err, repo.order, repo.instruction, repo.session)
	}
}

func TestUnknownBuyerInputEscalationIssuesAsOperatorLater(t *testing.T) {
	service, _ := testService(t, &testCheckout{status: "requires_escalation", buyerInput: true})
	ctx := context.Background()
	session, err := service.CreateOrderSheet(ctx, CreateOrderSheetInput{UserID: "user-1", CurationID: "curation-1", ExpectedCartVersion: 3, IdempotencyKey: "create-key-123"})
	if err != nil {
		t.Fatal(err)
	}
	session = setTestShipping(t, service, session, net.ParseIP("203.0.113.2"))
	session, err = service.Preflight(ctx, PreflightInput{UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version})
	if err != nil {
		t.Fatal(err)
	}
	if session.State != agencydomain.SessionReady || session.BlockReason != "" ||
		session.MerchantCheckouts[0].ProcurementHandling != agencydomain.ProcurementHandlingOperatorLater {
		t.Fatalf("session=%#v", session)
	}
	order, _, err := service.Issue(ctx, IssueOrderInput{UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version, DisplayedSnapshotHash: session.DisplayedSnapshotHash, IdempotencyKey: "issue-key-123", ProcurementApproval: testProcurementApproval()})
	if err != nil || order.ID == "" {
		t.Fatalf("operator-later escalation did not issue: %v %#v", err, order)
	}
}

func TestBuyerReviewEscalationIssuesThroughExistingPaymentInstruction(t *testing.T) {
	service, _ := testService(t, &testCheckout{status: "requires_escalation"})
	ctx := context.Background()
	session, err := service.CreateOrderSheet(ctx, CreateOrderSheetInput{UserID: "user-1", CurationID: "curation-1", ExpectedCartVersion: 3, IdempotencyKey: "create-key-review"})
	if err != nil {
		t.Fatal(err)
	}
	session = setTestShipping(t, service, session, net.ParseIP("203.0.113.2"))
	session, err = service.Preflight(ctx, PreflightInput{UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version})
	if err != nil || session.State != agencydomain.SessionReady {
		t.Fatalf("session=%#v err=%v", session, err)
	}
	order, instruction, err := service.Issue(ctx, IssueOrderInput{UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version, DisplayedSnapshotHash: session.DisplayedSnapshotHash, IdempotencyKey: "issue-key-review", ProcurementApproval: testProcurementApproval()})
	if err != nil || order.ID == "" || instruction.AgencyOrderID != order.ID || instruction.State != "PENDING" {
		t.Fatalf("order=%#v instruction=%#v err=%v", order, instruction, err)
	}
}

func TestExpiredOrderSheetReplayMintsAFreshSession(t *testing.T) {
	// 만료된 주문서가 같은 creation key의 재진입으로 그대로 반환되면 사용자는
	// 어떤 행동도 할 수 없는 화면에 갇힌다. 만료 세션의 key를 회수하고 새
	// 주문서를 자동 발급해야 한다.
	service, repository := testService(t, &testCheckout{})
	created, err := service.CreateOrderSheet(context.Background(), CreateOrderSheetInput{
		UserID: "user-1", CurationID: "curation-1", ExpectedCartVersion: 3,
		IdempotencyKey: "expiry-key-123",
	})
	if err != nil {
		t.Fatal(err)
	}
	expired := repository.session
	expired.State = agencydomain.SessionExpired
	repository.session = expired

	renewed, err := service.CreateOrderSheet(context.Background(), CreateOrderSheetInput{
		UserID: "user-1", CurationID: "curation-1", ExpectedCartVersion: 3,
		IdempotencyKey: "expiry-key-123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if renewed.ID == created.ID || renewed.State != agencydomain.SessionEditing {
		t.Fatalf("expired replay did not mint a fresh session: %#v", renewed)
	}
}

func TestExpiredOrderSheetActionsReturnTypedExpiredReason(t *testing.T) {
	service, repository := testService(t, &testCheckout{})
	session, err := service.CreateOrderSheet(context.Background(), CreateOrderSheetInput{
		UserID: "user-1", CurationID: "curation-1", ExpectedCartVersion: 3,
		IdempotencyKey: "expiry-key-456",
	})
	if err != nil {
		t.Fatal(err)
	}
	expired := repository.session
	expired.State = agencydomain.SessionExpired
	repository.session = expired

	_, err = service.Preflight(context.Background(), PreflightInput{
		UserID: "user-1", SessionID: session.ID, ExpectedVersion: session.Version,
	})
	if failure, ok := fault.As(err); !ok || failure.Reason != "ORDER_SHEET_EXPIRED" {
		t.Fatalf("expected ORDER_SHEET_EXPIRED, got %v", err)
	}
}
