package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
	"github.com/vitlane/vitlane/server/internal/ordering/policy"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

const (
	orderSheetTTL = 20 * time.Minute
	// 발행에 찍는 현행 disclosure version — FEE_RETAINED 게시 판정의 유일한
	// 표현은 ordering/policy다(ADR-0055 §2).
	disclosureVersion = policy.DisclosureVersionMOGross
)

var (
	// ErrPayPalIssueDisabled covers a READY PayPal OrderSheet whose stored rail
	// no longer matches the deployment's exact new-order selector.
	ErrPayPalIssueDisabled = errors.New("PAYPAL_ISSUE_DISABLED")
	// ErrLiveIssueDisabled preserves the existing reason for a READY Live sheet
	// after the Live issue gate/selector has closed.
	ErrLiveIssueDisabled          = errors.New("PAYPAL_LIVE_ISSUE_DISABLED")
	ErrLiveControlVersionConflict = errors.New("LIVE_CONTROL_VERSION_CONFLICT")
)

type LiveIssueGate interface {
	AllowLiveOrderIssue(context.Context) (bool, int64, error)
	LockLiveOrderIssueAdmission(context.Context) (bool, int64, error)
}

type CartSnapshotReader interface {
	ReadCartSnapshot(context.Context, string, string) (agencydomain.SourceCartSnapshot, error)
}

type ExactLineResolver interface {
	ResolveExactLines(context.Context, agencydomain.SourceCartSnapshot) ([]agencydomain.ExactLine, error)
}

type ShippingAddress struct {
	RecipientName string `json:"recipientName"`
	AddressLine1  string `json:"addressLine1"`
	AddressLine2  string `json:"addressLine2"`
	City          string `json:"city"`
	Region        string `json:"region"`
	PostalCode    string `json:"postalCode"`
	Country       string `json:"country"`
	Phone         string `json:"phone"`
}

type ShippingSnapshotPort interface {
	DefaultAddress(context.Context, string) (ShippingAddress, bool, error)
	CreateOrderSheetSnapshot(context.Context, string, ShippingAddress) (agencydomain.ShippingSnapshot, error)
	RevealSnapshot(context.Context, string, string) (ShippingAddress, error)
}

type MerchantRequest struct {
	OrderSheetSessionID string
	UserID              string
	ShopDomain          string
	Lines               []agencydomain.ExactLine
	ShippingAddress     ShippingAddress
	Existing            *agencydomain.MerchantCheckout
	BuyerIP             net.IP
}

type MerchantCheckoutPreflightPort interface {
	Explore(context.Context, MerchantRequest) (agencydomain.MerchantCheckout, error)
	SelectDelivery(context.Context, MerchantRequest, map[string]string) (agencydomain.MerchantCheckout, error)
	Preflight(context.Context, MerchantRequest) (agencydomain.MerchantCheckout, error)
	FinalGet(context.Context, MerchantRequest) (agencydomain.MerchantCheckout, error)
	Cancel(context.Context, MerchantRequest) error
}

type Repository interface {
	FindSessionByCreationKey(context.Context, string, string) (agencydomain.OrderSheetSession, string, bool, error)
	// RetireExpiredSessionCreationKey는 만료된 세션의 creation key 결속을
	// 회수해 같은 key로 새 세션을 만들 수 있게 한다. 세션이 EXPIRED가 아니면
	// 아무것도 바꾸지 않고 실패를 돌려준다.
	RetireExpiredSessionCreationKey(context.Context, string, string) error
	CreateSession(context.Context, agencydomain.OrderSheetSession, string, string) error
	GetSession(context.Context, string, string) (agencydomain.OrderSheetSession, error)
	SaveSession(context.Context, agencydomain.OrderSheetSession, int64) error
	IssueAtomic(context.Context, agencydomain.OrderSheetSession, int64, agencydomain.AgencyOrder, agencydomain.PaymentInstruction) error
	FindOrderByIdempotencyKey(context.Context, string, string) (agencydomain.AgencyOrder, bool, error)
	FindOrderBySessionID(context.Context, string, string) (agencydomain.AgencyOrder, bool, error)
	GetOrder(context.Context, string, string) (agencydomain.AgencyOrder, agencydomain.PaymentInstruction, error)
}

type Service struct {
	repository         Repository
	carts              CartSnapshotReader
	lines              ExactLineResolver
	shipping           ShippingSnapshotPort
	checkout           MerchantCheckoutPreflightPort
	transactor         sharedapp.Transactor
	clock              sharedapp.Clock
	ids                sharedapp.IDGenerator
	paypalSandboxIssue bool
	paypalLiveIssue    bool
	liveIssueGate      LiveIssueGate
}

func NewService(repository Repository, carts CartSnapshotReader, lines ExactLineResolver,
	shipping ShippingSnapshotPort, checkout MerchantCheckoutPreflightPort,
	transactor sharedapp.Transactor, clock sharedapp.Clock, ids sharedapp.IDGenerator) (*Service, error) {
	if repository == nil || carts == nil || lines == nil || shipping == nil || checkout == nil ||
		transactor == nil || clock == nil || ids == nil {
		return nil, fault.New(fault.InvalidInput, "AGENCY_ORDER_CONFIG_INVALID", false)
	}
	return &Service{repository: repository, carts: carts, lines: lines, shipping: shipping,
		checkout: checkout, transactor: transactor, clock: clock, ids: ids}, nil
}

// EnablePayPalSandboxIssue selects Sandbox for new PayPal AgencyOrders. The
// provider registry may still retain both environments for stored obligations.
func (s *Service) EnablePayPalSandboxIssue() {
	s.paypalSandboxIssue = true
}

// EnablePayPalLiveIssue selects Live for new PayPal AgencyOrders. It is called
// during startup only after configuration, credentials and the issue gate pass.
func (s *Service) EnablePayPalLiveIssue() {
	s.paypalLiveIssue = true
}

func (s *Service) EnableLiveIssueGate(gate LiveIssueGate) { s.liveIssueGate = gate }

func (s *Service) LiveIssueStatus(ctx context.Context) (bool, int64, error) {
	if !s.paypalLiveIssue || s.liveIssueGate == nil {
		return false, 0, nil
	}
	return s.liveIssueGate.AllowLiveOrderIssue(ctx)
}

type CreateOrderSheetInput struct {
	UserID              string
	CurationID          string
	ExpectedCartVersion int64
	IdempotencyKey      string
	BuyerIP             net.IP
}

func (s *Service) CreateOrderSheet(ctx context.Context, input CreateOrderSheetInput) (agencydomain.OrderSheetSession, error) {
	input.UserID = strings.TrimSpace(input.UserID)
	input.CurationID = strings.TrimSpace(input.CurationID)
	if input.UserID == "" || input.CurationID == "" || input.ExpectedCartVersion < 0 ||
		!validIdempotencyKey(input.IdempotencyKey) {
		return agencydomain.OrderSheetSession{}, fault.New(fault.InvalidInput, "ORDER_SHEET_CREATE_INVALID", false)
	}
	keyHash := secretHash(input.IdempotencyKey)
	requestHash, err := shareddomain.CanonicalJSONHash(struct {
		CurationID string `json:"curationId"`
		Version    int64  `json:"expectedCartVersion"`
	}{input.CurationID, input.ExpectedCartVersion})
	if err != nil {
		return agencydomain.OrderSheetSession{}, err
	}
	if existing, storedRequestHash, found, findErr := s.repository.FindSessionByCreationKey(ctx, input.UserID, keyHash); findErr != nil {
		return agencydomain.OrderSheetSession{}, findErr
	} else if found {
		if storedRequestHash != requestHash {
			return agencydomain.OrderSheetSession{}, fault.New(fault.Conflict, "ORDER_SHEET_IDEMPOTENCY_CONFLICT", false)
		}
		if existing.State == agencydomain.SessionExpired {
			// 만료된 주문서는 어떤 행동도 받을 수 없다. 같은 의도(같은 key)의
			// 재진입은 만료 세션의 key를 회수하고 새 주문서를 자동 발급한다.
			if retireErr := s.repository.RetireExpiredSessionCreationKey(
				ctx, input.UserID, existing.ID,
			); retireErr != nil {
				return agencydomain.OrderSheetSession{}, retireErr
			}
		} else {
			if existing.State == agencydomain.SessionDiscoveringDelivery {
				return s.resumeDeliveryDiscovery(ctx, existing, input.BuyerIP)
			}
			return s.withIssuedOrderID(ctx, input.UserID, existing)
		}
	}
	cart, err := s.carts.ReadCartSnapshot(ctx, input.UserID, input.CurationID)
	if err != nil {
		return agencydomain.OrderSheetSession{}, err
	}
	if cart.CartVersion != input.ExpectedCartVersion {
		return agencydomain.OrderSheetSession{}, fault.New(fault.Conflict, "PHASE8_CART_VERSION_CONFLICT", true)
	}
	if len(cart.Items) == 0 || len(cart.Items) > 10 {
		return agencydomain.OrderSheetSession{}, fault.New(fault.InvalidInput, "ORDER_SHEET_CART_INVALID", false)
	}
	lines, err := s.lines.ResolveExactLines(ctx, cart)
	if err != nil {
		return agencydomain.OrderSheetSession{}, err
	}
	for index := range lines {
		if strings.TrimSpace(lines[index].LineID) == "" {
			lines[index].LineID = s.ids.NewID()
		}
	}
	now := s.clock.Now()
	session := agencydomain.OrderSheetSession{
		ID: s.ids.NewID(), UserID: input.UserID, SourceCart: cart,
		Lines: lines, MerchantCheckouts: make([]agencydomain.MerchantCheckout, 0),
		PassThroughTotal:     agencydomain.Money{Currency: "USD"},
		AgencyFee:            agencydomain.Money{Currency: "USD"},
		CustomerPayableTotal: agencydomain.Money{Currency: "USD"}, Version: 1,
		State: agencydomain.SessionEditing, CreatedAt: now,
		ExpiresAt: now.Add(orderSheetTTL),
	}
	if err := s.repository.CreateSession(ctx, session, keyHash, requestHash); err != nil {
		if existing, storedRequestHash, found, findErr := s.repository.FindSessionByCreationKey(ctx, input.UserID, keyHash); findErr == nil && found {
			if storedRequestHash != requestHash {
				return agencydomain.OrderSheetSession{}, fault.New(fault.Conflict, "ORDER_SHEET_IDEMPOTENCY_CONFLICT", false)
			}
			if existing.State == agencydomain.SessionDiscoveringDelivery {
				return s.resumeDeliveryDiscovery(ctx, existing, input.BuyerIP)
			}
			return s.withIssuedOrderID(ctx, input.UserID, existing)
		}
		return agencydomain.OrderSheetSession{}, err
	}
	return session, nil
}

type ShippingDraft struct {
	Address ShippingAddress
	Source  string
}

func (s *Service) ShippingDraft(
	ctx context.Context,
	userID string,
	session agencydomain.OrderSheetSession,
) (ShippingDraft, error) {
	if session.ShippingAddress.SnapshotRef != "" {
		address, err := s.shipping.RevealSnapshot(ctx, userID, session.ShippingAddress.SnapshotRef)
		if err != nil {
			return ShippingDraft{}, err
		}
		return ShippingDraft{Address: address, Source: "ORDER_SHEET"}, nil
	}
	address, found, err := s.shipping.DefaultAddress(ctx, userID)
	if err != nil {
		return ShippingDraft{}, err
	}
	if found && strings.EqualFold(strings.TrimSpace(address.Country), "US") {
		return ShippingDraft{Address: address, Source: "ACCOUNT_DEFAULT"}, nil
	}
	return ShippingDraft{Address: ShippingAddress{Country: "US"}, Source: "EMPTY"}, nil
}

type SetShippingAddressInput struct {
	UserID          string
	SessionID       string
	ExpectedVersion int64
	Address         ShippingAddress
	BuyerIP         net.IP
}

func (s *Service) SetShippingAddress(
	ctx context.Context,
	input SetShippingAddressInput,
) (agencydomain.OrderSheetSession, error) {
	session, err := s.repository.GetSession(
		ctx, strings.TrimSpace(input.UserID), strings.TrimSpace(input.SessionID),
	)
	if err != nil {
		return agencydomain.OrderSheetSession{}, err
	}
	if session.State == agencydomain.SessionConsumed {
		return agencydomain.OrderSheetSession{}, s.alreadyConsumedError(ctx, input.UserID, session.ID)
	}
	if session.State == agencydomain.SessionExpired {
		return agencydomain.OrderSheetSession{}, errOrderSheetExpired()
	}
	if session.Version != input.ExpectedVersion {
		return agencydomain.OrderSheetSession{}, fault.New(fault.Conflict, "ORDER_SHEET_VERSION_CONFLICT", true)
	}
	addressCorrection := session.State == agencydomain.SessionBlocked &&
		session.BlockReason == agencydomain.BlockCheckoutCustomerCorrection
	if session.State != agencydomain.SessionEditing && session.State != agencydomain.SessionDeliverySelectionRequired &&
		!addressCorrection {
		return agencydomain.OrderSheetSession{}, agencydomain.ErrStateInvalid
	}
	normalizedAddress, err := NormalizeUSShippingAddress(input.Address)
	if err != nil {
		return agencydomain.OrderSheetSession{}, err
	}
	input.Address = normalizedAddress
	if addressCorrection {
		// A correction starts a fresh quote graph. Cancel every existing UCP
		// checkout idempotently before the local snapshot is replaced; a partial
		// provider failure leaves the same blocked session available for retry.
		for index := range session.MerchantCheckouts {
			existing := session.MerchantCheckouts[index]
			if err := s.checkout.Cancel(ctx, MerchantRequest{
				OrderSheetSessionID: session.ID, UserID: input.UserID,
				ShopDomain: existing.ShopDomain, Existing: &existing, BuyerIP: input.BuyerIP,
			}); err != nil {
				return agencydomain.OrderSheetSession{}, err
			}
		}
	} else {
		for _, checkout := range session.MerchantCheckouts {
			if checkout.CheckoutSessionSafeRef != "" {
				return agencydomain.OrderSheetSession{}, agencydomain.ErrStateInvalid
			}
		}
	}
	previousVersion := session.Version
	err = s.transactor.WithinTransaction(ctx, func(txContext context.Context) error {
		snapshot, snapshotErr := s.shipping.CreateOrderSheetSnapshot(txContext, input.UserID, input.Address)
		if snapshotErr != nil {
			return snapshotErr
		}
		if snapshot.SnapshotRef == "" || snapshot.SnapshotHash == "" || snapshot.Country != "US" {
			return agencydomain.ErrShippingInvalid
		}
		session.ShippingAddress = snapshot
		session.MerchantCheckouts = make([]agencydomain.MerchantCheckout, 0)
		session.PaymentSelection = ""
		session.DisplayedSnapshotHash = ""
		session.PassThroughTotal = agencydomain.Money{Currency: "USD"}
		session.AgencyFee = agencydomain.Money{Currency: "USD"}
		session.CustomerPayableTotal = agencydomain.Money{Currency: "USD"}
		session.BlockReason = ""
		session.RetryAfter = nil
		session.State = agencydomain.SessionDiscoveringDelivery
		session.Version++
		return s.repository.SaveSession(txContext, session, previousVersion)
	})
	if err != nil {
		return agencydomain.OrderSheetSession{}, err
	}
	return s.resumeDeliveryDiscovery(ctx, session, input.BuyerIP)
}

// resumeDeliveryDiscovery persists the OrderSheet identity before the first
// provider write. The Shopify capability vault therefore never references a
// session that does not exist, and an idempotent replay can continue after a
// process restart without creating a second local OrderSheet.
func (s *Service) resumeDeliveryDiscovery(
	ctx context.Context,
	session agencydomain.OrderSheetSession,
	buyerIP net.IP,
) (agencydomain.OrderSheetSession, error) {
	groups, groupErr := groupLines(session.Lines)
	if groupErr != nil {
		previousVersion := session.Version
		session.Block(agencydomain.BlockUnsupportedShop)
		if err := s.repository.SaveSession(ctx, session, previousVersion); err != nil {
			return agencydomain.OrderSheetSession{}, err
		}
		return session, nil
	}
	address, err := s.shipping.RevealSnapshot(
		ctx, session.UserID, session.ShippingAddress.SnapshotRef,
	)
	if err != nil {
		return agencydomain.OrderSheetSession{}, err
	}
	existingByShop := make(map[string]agencydomain.MerchantCheckout, len(session.MerchantCheckouts))
	for _, checkout := range session.MerchantCheckouts {
		existingByShop[checkout.ShopDomain] = checkout
	}
	shops := make([]string, 0, len(groups))
	for shop := range groups {
		shops = append(shops, shop)
	}
	sort.Strings(shops)
	for _, shop := range shops {
		if _, found := existingByShop[shop]; found {
			continue
		}
		checkout, checkoutErr := s.checkout.Explore(ctx, MerchantRequest{
			OrderSheetSessionID: session.ID, UserID: session.UserID, ShopDomain: shop,
			Lines: groups[shop], ShippingAddress: address, BuyerIP: buyerIP,
		})
		if checkoutErr != nil {
			if fault.CodeOf(checkoutErr) == fault.ExternalEffectUnknown {
				previousVersion := session.Version
				session.Block(agencydomain.BlockUnknownExternalEffect)
				if saveErr := s.repository.SaveSession(ctx, session, previousVersion); saveErr != nil {
					return agencydomain.OrderSheetSession{}, saveErr
				}
				return session, nil
			}
			return agencydomain.OrderSheetSession{}, checkoutErr
		}
		previousVersion := session.Version
		session.MerchantCheckouts = append(session.MerchantCheckouts, checkout)
		session.Version++
		if err := s.repository.SaveSession(ctx, session, previousVersion); err != nil {
			return agencydomain.OrderSheetSession{}, err
		}
		existingByShop[shop] = checkout
	}
	previousVersion := session.Version
	session.State = agencydomain.SessionEditing
	for _, checkout := range session.MerchantCheckouts {
		if !checkout.DeliverySelectionComplete() {
			session.State = agencydomain.SessionDeliverySelectionRequired
			break
		}
	}
	session.Version++
	if err := s.repository.SaveSession(ctx, session, previousVersion); err != nil {
		return agencydomain.OrderSheetSession{}, err
	}
	return session, nil
}

type MerchantDeliverySelection struct {
	ShopDomain string
	GroupID    string
	OptionID   string
}

type SelectDeliveryInput struct {
	UserID          string
	SessionID       string
	ExpectedVersion int64
	Selections      []MerchantDeliverySelection
	BuyerIP         net.IP
}

func (s *Service) SelectDelivery(ctx context.Context, input SelectDeliveryInput) (agencydomain.OrderSheetSession, error) {
	session, err := s.repository.GetSession(ctx, strings.TrimSpace(input.UserID), strings.TrimSpace(input.SessionID))
	if err != nil {
		return agencydomain.OrderSheetSession{}, err
	}
	if session.State == agencydomain.SessionConsumed {
		return agencydomain.OrderSheetSession{}, s.alreadyConsumedError(ctx, input.UserID, session.ID)
	}
	if session.State == agencydomain.SessionExpired {
		return agencydomain.OrderSheetSession{}, errOrderSheetExpired()
	}
	if session.Version != input.ExpectedVersion {
		return agencydomain.OrderSheetSession{}, fault.New(fault.Conflict, "ORDER_SHEET_VERSION_CONFLICT", true)
	}
	if session.State != agencydomain.SessionDeliverySelectionRequired && session.State != agencydomain.SessionEditing {
		return agencydomain.OrderSheetSession{}, agencydomain.ErrStateInvalid
	}
	byShop := make(map[string]map[string]string)
	for _, selection := range input.Selections {
		shop := strings.ToLower(strings.TrimSpace(selection.ShopDomain))
		groupID, optionID := strings.TrimSpace(selection.GroupID), strings.TrimSpace(selection.OptionID)
		if shop == "" || groupID == "" || optionID == "" {
			return agencydomain.OrderSheetSession{}, agencydomain.ErrInvalid
		}
		if byShop[shop] == nil {
			byShop[shop] = make(map[string]string)
		}
		if _, duplicate := byShop[shop][groupID]; duplicate {
			return agencydomain.OrderSheetSession{}, agencydomain.ErrInvalid
		}
		byShop[shop][groupID] = optionID
	}
	address, err := s.shipping.RevealSnapshot(ctx, input.UserID, session.ShippingAddress.SnapshotRef)
	if err != nil {
		return agencydomain.OrderSheetSession{}, err
	}
	groups, err := groupLines(session.Lines)
	if err != nil {
		return agencydomain.OrderSheetSession{}, err
	}
	previousVersion := session.Version
	updated := make([]agencydomain.MerchantCheckout, 0, len(session.MerchantCheckouts))
	for index := range session.MerchantCheckouts {
		existing := session.MerchantCheckouts[index]
		selections, ok := byShop[existing.ShopDomain]
		if !ok {
			return agencydomain.OrderSheetSession{}, agencydomain.ErrInvalid
		}
		checkout, selectErr := s.checkout.SelectDelivery(ctx, MerchantRequest{
			OrderSheetSessionID: session.ID, UserID: input.UserID,
			ShopDomain: existing.ShopDomain, Lines: groups[existing.ShopDomain],
			ShippingAddress: address, Existing: &existing, BuyerIP: input.BuyerIP,
		}, selections)
		if selectErr != nil {
			return agencydomain.OrderSheetSession{}, selectErr
		}
		if !checkout.DeliverySelectionComplete() {
			return agencydomain.OrderSheetSession{}, agencydomain.ErrInvalid
		}
		updated = append(updated, checkout)
	}
	if len(byShop) != len(updated) {
		return agencydomain.OrderSheetSession{}, agencydomain.ErrInvalid
	}
	session.MerchantCheckouts = updated
	session.State = agencydomain.SessionEditing
	session.Version++
	if err := s.repository.SaveSession(ctx, session, previousVersion); err != nil {
		return agencydomain.OrderSheetSession{}, err
	}
	return session, nil
}

type PreflightInput struct {
	UserID          string
	SessionID       string
	ExpectedVersion int64
	// PaymentRail은 결제수단 선택이다. 빈 값은 TVITUSD로 해석한다.
	PaymentRail agencydomain.PaymentRail
	BuyerIP     net.IP
}

// Preflight는 결제수단을 고정하고 Shop별 checkout preflight를 수행한다.
// 결제수단은 수수료 정책만 바꾸며 Shopify preflight 절차는 rail 무관 동일하다.
func (s *Service) Preflight(ctx context.Context, input PreflightInput) (agencydomain.OrderSheetSession, error) {
	session, err := s.repository.GetSession(ctx, strings.TrimSpace(input.UserID), strings.TrimSpace(input.SessionID))
	if err != nil {
		return agencydomain.OrderSheetSession{}, err
	}
	if session.State == agencydomain.SessionConsumed {
		return agencydomain.OrderSheetSession{}, s.alreadyConsumedError(ctx, input.UserID, session.ID)
	}
	if session.State == agencydomain.SessionExpired {
		return agencydomain.OrderSheetSession{}, errOrderSheetExpired()
	}
	if session.Version != input.ExpectedVersion {
		return agencydomain.OrderSheetSession{}, fault.New(fault.Conflict, "ORDER_SHEET_VERSION_CONFLICT", true)
	}
	for _, checkout := range session.MerchantCheckouts {
		if !checkout.DeliverySelectionComplete() {
			return agencydomain.OrderSheetSession{}, fault.New(fault.InvalidInput, "DELIVERY_SELECTION_REQUIRED", false)
		}
	}
	previousVersion := session.Version
	rail := input.PaymentRail
	if rail == "" {
		rail = agencydomain.PaymentRailTVITUSD
	}
	if err := session.SelectRail(rail); err != nil {
		return agencydomain.OrderSheetSession{}, err
	}
	address, err := s.shipping.RevealSnapshot(ctx, input.UserID, session.ShippingAddress.SnapshotRef)
	if err != nil {
		return agencydomain.OrderSheetSession{}, err
	}
	groups, err := groupLines(session.Lines)
	if err != nil {
		return agencydomain.OrderSheetSession{}, err
	}
	// Shop당 create/update/get/get 왕복이 수 초씩 걸리므로 직렬 실행은 Shop 수에
	// 비례해 interactive 요청 예산을 넘긴다. Shop별로 독립 checkout이라 병렬로
	// 수행하고, 세션 저장 순서는 기존 checkout 순서를 유지한다.
	checkouts := make([]agencydomain.MerchantCheckout, len(session.MerchantCheckouts))
	checkoutErrs := make([]error, len(session.MerchantCheckouts))
	var preflightGroup sync.WaitGroup
	for index := range session.MerchantCheckouts {
		preflightGroup.Add(1)
		go func(index int) {
			defer preflightGroup.Done()
			existing := session.MerchantCheckouts[index]
			checkout, checkoutErr := s.checkout.Preflight(ctx, MerchantRequest{
				OrderSheetSessionID: session.ID, UserID: input.UserID,
				ShopDomain: existing.ShopDomain, Lines: groups[existing.ShopDomain],
				ShippingAddress: address, Existing: &existing, BuyerIP: input.BuyerIP,
			})
			if checkoutErr != nil {
				checkoutErrs[index] = checkoutErr
				return
			}
			checkouts[index] = checkout
		}(index)
	}
	preflightGroup.Wait()
	for _, checkoutErr := range checkoutErrs {
		if checkoutErr != nil {
			return agencydomain.OrderSheetSession{}, checkoutErr
		}
	}
	if err := session.ApplyPreflight(checkouts, s.clock.Now()); err != nil {
		return agencydomain.OrderSheetSession{}, err
	}
	if err := s.repository.SaveSession(ctx, session, previousVersion); err != nil {
		return agencydomain.OrderSheetSession{}, err
	}
	return session, nil
}

type IssueOrderInput struct {
	UserID                     string
	SessionID                  string
	ExpectedVersion            int64
	ExpectedCapabilityRevision int64
	DisplayedSnapshotHash      string
	IdempotencyKey             string
	ProcurementApproval        agencydomain.ProcurementApproval
	BuyerIP                    net.IP
}

func (s *Service) Issue(ctx context.Context, input IssueOrderInput) (agencydomain.AgencyOrder, agencydomain.PaymentInstruction, error) {
	if !validIdempotencyKey(input.IdempotencyKey) {
		return agencydomain.AgencyOrder{}, agencydomain.PaymentInstruction{}, fault.New(fault.InvalidInput, "AGENCY_ORDER_ISSUE_INVALID", false)
	}
	keyHash := secretHash(input.IdempotencyKey)
	if existing, found, err := s.repository.FindOrderByIdempotencyKey(ctx, input.UserID, keyHash); err != nil {
		return agencydomain.AgencyOrder{}, agencydomain.PaymentInstruction{}, err
	} else if found {
		if existing.IssuanceEvidence.OrderSheetSessionID != input.SessionID || existing.IssuanceEvidence.DisplayedSnapshotHash != input.DisplayedSnapshotHash {
			return agencydomain.AgencyOrder{}, agencydomain.PaymentInstruction{}, fault.New(fault.Conflict, "AGENCY_ORDER_IDEMPOTENCY_CONFLICT", false)
		}
		if existing.ProcurementAuthorization.AuthorizationHash != "" &&
			!existing.ProcurementAuthorization.CustomerApproval.SameAs(input.ProcurementApproval) {
			return agencydomain.AgencyOrder{}, agencydomain.PaymentInstruction{}, fault.New(fault.Conflict, "AGENCY_ORDER_IDEMPOTENCY_CONFLICT", false)
		}
		order, instruction, getErr := s.repository.GetOrder(ctx, input.UserID, existing.ID)
		return order, instruction, getErr
	}
	if existing, found, err := s.repository.FindOrderBySessionID(ctx, input.UserID, strings.TrimSpace(input.SessionID)); err != nil {
		return agencydomain.AgencyOrder{}, agencydomain.PaymentInstruction{}, err
	} else if found {
		return s.repository.GetOrder(ctx, input.UserID, existing.ID)
	}
	if err := input.ProcurementApproval.Validate(); err != nil {
		return agencydomain.AgencyOrder{}, agencydomain.PaymentInstruction{}, fault.New(
			fault.InvalidInput, "PROCUREMENT_AUTHORIZATION_INVALID", false,
		)
	}
	session, err := s.repository.GetSession(ctx, input.UserID, input.SessionID)
	if err != nil {
		return agencydomain.AgencyOrder{}, agencydomain.PaymentInstruction{}, err
	}
	if session.State == agencydomain.SessionExpired {
		return agencydomain.AgencyOrder{}, agencydomain.PaymentInstruction{}, errOrderSheetExpired()
	}
	if session.Version != input.ExpectedVersion {
		return agencydomain.AgencyOrder{}, agencydomain.PaymentInstruction{}, fault.New(fault.Conflict, "ORDER_SHEET_VERSION_CONFLICT", true)
	}
	if session.DisplayedSnapshotHash != input.DisplayedSnapshotHash {
		return agencydomain.AgencyOrder{}, agencydomain.PaymentInstruction{}, fault.New(fault.Conflict, "DISPLAYED_SNAPSHOT_CHANGED", true)
	}
	switch session.PaymentSelection {
	case agencydomain.PaymentRailPayPalSandbox:
		if !s.paypalSandboxIssue {
			return agencydomain.AgencyOrder{}, agencydomain.PaymentInstruction{}, ErrPayPalIssueDisabled
		}
	case agencydomain.PaymentRailPayPalLive:
		allowed, revision, gateErr := s.LiveIssueStatus(ctx)
		if gateErr != nil || !allowed {
			return agencydomain.AgencyOrder{}, agencydomain.PaymentInstruction{}, ErrLiveIssueDisabled
		}
		if input.ExpectedCapabilityRevision != revision {
			return agencydomain.AgencyOrder{}, agencydomain.PaymentInstruction{}, ErrLiveControlVersionConflict
		}
	}
	address, err := s.shipping.RevealSnapshot(ctx, input.UserID, session.ShippingAddress.SnapshotRef)
	if err != nil {
		return agencydomain.AgencyOrder{}, agencydomain.PaymentInstruction{}, err
	}
	groups, err := groupLines(session.Lines)
	if err != nil {
		return agencydomain.AgencyOrder{}, agencydomain.PaymentInstruction{}, err
	}
	finalCheckouts := make([]agencydomain.MerchantCheckout, 0, len(session.MerchantCheckouts))
	actionSnapshotChanged := false
	for index := range session.MerchantCheckouts {
		existing := session.MerchantCheckouts[index]
		fresh, getErr := s.checkout.FinalGet(ctx, MerchantRequest{
			OrderSheetSessionID: session.ID, UserID: input.UserID, ShopDomain: existing.ShopDomain,
			Lines: groups[existing.ShopDomain], ShippingAddress: address, Existing: &existing, BuyerIP: input.BuyerIP,
		})
		if getErr != nil {
			return agencydomain.AgencyOrder{}, agencydomain.PaymentInstruction{}, getErr
		}
		if finalCheckoutAffectsAction(existing, fresh) {
			actionSnapshotChanged = true
		}
		finalCheckouts = append(finalCheckouts, fresh)
	}
	if actionSnapshotChanged {
		// Refresh the whole displayed snapshot only for economics or a hard
		// handling change. Customer-correction/impossible observations get their
		// exact blocked state; changed economics stay READY with a new display hash
		// for one explicit customer review.
		if err := session.ApplyPreflight(finalCheckouts, s.clock.Now()); err != nil {
			return agencydomain.AgencyOrder{}, agencydomain.PaymentInstruction{}, err
		}
		if saveErr := s.repository.SaveSession(ctx, session, input.ExpectedVersion); saveErr != nil {
			return agencydomain.AgencyOrder{}, agencydomain.PaymentInstruction{}, saveErr
		}
		return agencydomain.AgencyOrder{}, agencydomain.PaymentInstruction{}, fault.New(fault.Conflict, "FINAL_CHECKOUT_CHANGED", true)
	}
	order, instruction, err := agencydomain.Issue(session, agencydomain.IssueInput{
		OrderID: s.ids.NewID(), InstructionID: s.ids.NewID(), IdempotencyKeyHash: keyHash,
		DisclosureVersion: disclosureVersion, ProcurementApproval: input.ProcurementApproval,
		Now: s.clock.Now(),
	})
	if err != nil {
		if errors.Is(err, agencydomain.ErrProcurementAuthorizationInvalid) {
			return agencydomain.AgencyOrder{}, agencydomain.PaymentInstruction{}, fault.New(
				fault.InvalidInput, "PROCUREMENT_AUTHORIZATION_INVALID", false,
			)
		}
		return agencydomain.AgencyOrder{}, agencydomain.PaymentInstruction{}, err
	}
	consumed := session
	consumed.State = agencydomain.SessionConsumed
	consumed.Version++
	issueErr := s.transactor.WithinTransaction(ctx, func(txContext context.Context) error {
		if session.PaymentSelection == agencydomain.PaymentRailPayPalLive {
			allowed, revision, gateErr := s.liveIssueGate.LockLiveOrderIssueAdmission(txContext)
			if gateErr != nil || !allowed {
				return ErrLiveIssueDisabled
			}
			if input.ExpectedCapabilityRevision != revision {
				return ErrLiveControlVersionConflict
			}
		}
		return s.repository.IssueAtomic(txContext, consumed, input.ExpectedVersion, order, instruction)
	})
	if issueErr != nil {
		if existing, found, findErr := s.repository.FindOrderByIdempotencyKey(ctx, input.UserID, keyHash); findErr == nil && found {
			return s.repository.GetOrder(ctx, input.UserID, existing.ID)
		}
		if existing, found, findErr := s.repository.FindOrderBySessionID(ctx, input.UserID, input.SessionID); findErr == nil && found {
			return s.repository.GetOrder(ctx, input.UserID, existing.ID)
		}
		return agencydomain.AgencyOrder{}, agencydomain.PaymentInstruction{}, issueErr
	}
	sharedapp.RecordAnalytics(ctx, sharedapp.AnalyticsEvent{Name: "agency_order_issued", UserID: input.UserID, Key: order.ID, Action: string(session.PaymentSelection)})
	return order, instruction, nil
}

// finalCheckoutAffectsAction deliberately excludes provider status, notices,
// manual-site steps and their evidence hash. Those observations are read-only
// once the customer approved an exact quote. Only a changed economic snapshot
// or a newly observed hard handling decision requires redisplay.
func finalCheckoutAffectsAction(existing, fresh agencydomain.MerchantCheckout) bool {
	return fresh.QuoteFingerprint != existing.QuoteFingerprint ||
		fresh.QuoteReadiness != agencydomain.PricingConfirmed ||
		fresh.ProcurementHandling == agencydomain.ProcurementHandlingCustomerCorrection ||
		fresh.ProcurementHandling == agencydomain.ProcurementHandlingImpossible ||
		fresh.DutiesDisposition != agencydomain.DutiesNoSignalAtPreflight ||
		fresh.CompletionRoute != agencydomain.CompletionManualHandoff ||
		fresh.AuthEvidence.Tier != "TOKEN" ||
		fresh.AuthEvidence.CompletionPermission != agencydomain.CompletionNotRequested
}

func (s *Service) GetSession(ctx context.Context, userID, sessionID string) (agencydomain.OrderSheetSession, error) {
	userID, sessionID = strings.TrimSpace(userID), strings.TrimSpace(sessionID)
	session, err := s.repository.GetSession(ctx, userID, sessionID)
	if err != nil {
		return agencydomain.OrderSheetSession{}, err
	}
	return s.withIssuedOrderID(ctx, userID, session)
}

func (s *Service) GetOrder(ctx context.Context, userID, orderID string) (agencydomain.AgencyOrder, agencydomain.PaymentInstruction, error) {
	return s.repository.GetOrder(ctx, strings.TrimSpace(userID), strings.TrimSpace(orderID))
}

func (s *Service) withIssuedOrderID(ctx context.Context, userID string, session agencydomain.OrderSheetSession) (agencydomain.OrderSheetSession, error) {
	if session.State != agencydomain.SessionConsumed {
		return session, nil
	}
	order, found, err := s.repository.FindOrderBySessionID(ctx, userID, session.ID)
	if err != nil {
		return agencydomain.OrderSheetSession{}, err
	}
	if !found {
		return agencydomain.OrderSheetSession{}, fault.New(fault.InternalFailure, "ORDER_SHEET_CONSUMED_ORDER_MISSING", false)
	}
	session.IssuedAgencyOrderID = order.ID
	return session, nil
}

// errOrderSheetExpired는 만료된 주문서에 대한 모든 행동을 구분된 reason으로
// 거절한다. 웹은 이 reason으로 "만료 안내 + 새 주문서 만들기"를 표시한다.
func errOrderSheetExpired() error {
	return fault.New(fault.Conflict, "ORDER_SHEET_EXPIRED", false)
}

func (s *Service) alreadyConsumedError(ctx context.Context, userID, sessionID string) error {
	order, found, err := s.repository.FindOrderBySessionID(ctx, strings.TrimSpace(userID), strings.TrimSpace(sessionID))
	if err != nil {
		return err
	}
	if !found {
		return fault.New(fault.InternalFailure, "ORDER_SHEET_CONSUMED_ORDER_MISSING", false)
	}
	return &agencydomain.OrderSheetAlreadyConsumedError{AgencyOrderID: order.ID}
}

func groupLines(lines []agencydomain.ExactLine) (map[string][]agencydomain.ExactLine, error) {
	groups := make(map[string][]agencydomain.ExactLine)
	for _, line := range lines {
		domain := canonicalShopDomain(line.ShopDomain)
		if domain == "" || line.LineID == "" || line.VariantID == "" || line.Quantity < 1 {
			return nil, agencydomain.ErrInvalid
		}
		line.ShopDomain = domain
		groups[domain] = append(groups[domain], line)
	}
	if len(groups) == 0 {
		return nil, agencydomain.ErrInvalid
	}
	return groups, nil
}

func canonicalShopDomain(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if parsed, err := url.Parse(value); err == nil && parsed.Hostname() != "" {
		value = strings.ToLower(parsed.Hostname())
	}
	if strings.ContainsAny(value, "/:@") || net.ParseIP(value) != nil || !strings.Contains(value, ".") {
		return ""
	}
	return value
}

func validIdempotencyKey(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) >= 8 && len(value) <= 200
}

func secretHash(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}

func IsRetryableVersionConflict(err error) bool {
	return errors.Is(err, agencydomain.ErrVersionConflict)
}
