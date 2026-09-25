package app

import (
	"context"
	"regexp"
	"strings"

	"github.com/vitlane/vitlane/server/internal/ordering/procurement/domain"
)

// 회수 기입(운영정합 5차 PR-D) — 운영자가 주문 번호로 그 주문의 회수 목록
// (자동 entry 포함)을 보고 MO를 짚어 수취를 기입하는 CRUD다. 상태는 금액에서
// 파생하고(운영자 상태 입력 없음), 자동 entry는 삭제 대신 포기로 닫는다.

type RecoveryEntry struct {
	ID                  string `json:"id"`
	MerchantOrderID     string `json:"merchantOrderId"`
	AgencyOrderID       string `json:"agencyOrderId"`
	Cause               string `json:"cause"`
	ExpectedAmountMinor int64  `json:"expectedAmountMinor"`
	ReceivedAmountMinor int64  `json:"receivedAmountMinor"`
	State               string `json:"state"`
	// Manual은 수동 생성 여부다(evidence_ref 표식 파생) — 삭제 가능 조건.
	Manual    bool   `json:"manual"`
	Note      string `json:"note,omitempty"`
	Version   int64  `json:"version"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// RecoveryMerchantOrder는 수동 생성 폼의 MO 선택지다.
type RecoveryMerchantOrder struct {
	ID              string `json:"id"`
	ShopDomain      string `json:"shopDomain"`
	CheckoutOrdinal int    `json:"checkoutOrdinal"`
}

type RecoverySurface struct {
	AgencyOrderID        string                  `json:"agencyOrderId"`
	MatchedBy            string                  `json:"matchedBy"`
	FocusMerchantOrderID string                  `json:"focusMerchantOrderId,omitempty"`
	Entries              []RecoveryEntry         `json:"entries"`
	MerchantOrders       []RecoveryMerchantOrder `json:"merchantOrders"`
}

type RecoveryCreateInput struct {
	MerchantOrderID     string
	Cause               string
	ExpectedAmountMinor int64
	ReceivedAmountMinor int64
	Note                string
}

var uuidPattern = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`,
)

// validRecoveryID는 사용자 입력(주문 번호·entry ID)의 UUID 형식 가드다 —
// 형식 오류를 DB cast 오류(500)가 아닌 422로 돌려준다.
func validRecoveryID(value string) bool { return uuidPattern.MatchString(strings.TrimSpace(value)) }

func (s *Service) ListRecovery(ctx context.Context, referenceID string) (RecoverySurface, error) {
	if !validRecoveryID(referenceID) {
		return RecoverySurface{}, domain.ErrRecoveryInvalid
	}
	return s.repository.ListRecoveryEntries(ctx, strings.TrimSpace(referenceID))
}

func (s *Service) CreateRecovery(
	ctx context.Context,
	operatorUserID, idempotencyKey string,
	input RecoveryCreateInput,
) (RecoveryEntry, bool, error) {
	if !validIdempotencyKey(idempotencyKey) || strings.TrimSpace(operatorUserID) == "" ||
		!validRecoveryID(input.MerchantOrderID) ||
		!domain.ValidManualRecoveryCause(input.Cause) {
		return RecoveryEntry{}, false, domain.ErrRecoveryInvalid
	}
	if err := domain.ValidateRecoveryAmounts(
		input.ExpectedAmountMinor, input.ReceivedAmountMinor,
	); err != nil {
		return RecoveryEntry{}, false, err
	}
	if err := domain.ValidateRecoveryNote(input.Note); err != nil {
		return RecoveryEntry{}, false, err
	}
	return s.repository.CreateRecoveryEntry(
		ctx, strings.TrimSpace(input.MerchantOrderID), strings.TrimSpace(operatorUserID),
		strings.TrimSpace(idempotencyKey), strings.TrimSpace(input.Cause),
		input.ExpectedAmountMinor, input.ReceivedAmountMinor,
		strings.TrimSpace(input.Note), s.clock.Now(),
	)
}

func (s *Service) RecordRecovery(
	ctx context.Context,
	entryID, operatorUserID, idempotencyKey string,
	receivedAmountMinor int64,
	note string,
	expectedVersion int64,
) (RecoveryEntry, bool, error) {
	if !validIdempotencyKey(idempotencyKey) || strings.TrimSpace(operatorUserID) == "" ||
		!validRecoveryID(entryID) || expectedVersion <= 0 {
		return RecoveryEntry{}, false, domain.ErrRecoveryInvalid
	}
	if err := domain.ValidateRecoveryAmounts(0, receivedAmountMinor); err != nil {
		return RecoveryEntry{}, false, err
	}
	if err := domain.ValidateRecoveryNote(note); err != nil {
		return RecoveryEntry{}, false, err
	}
	return s.repository.RecordRecoveryEntry(
		ctx, strings.TrimSpace(entryID), strings.TrimSpace(operatorUserID),
		strings.TrimSpace(idempotencyKey), receivedAmountMinor,
		strings.TrimSpace(note), expectedVersion, s.clock.Now(),
	)
}

func (s *Service) WaiveRecovery(
	ctx context.Context,
	entryID, operatorUserID, idempotencyKey, note string,
	expectedVersion int64,
) (RecoveryEntry, bool, error) {
	if !validIdempotencyKey(idempotencyKey) || strings.TrimSpace(operatorUserID) == "" ||
		!validRecoveryID(entryID) || expectedVersion <= 0 {
		return RecoveryEntry{}, false, domain.ErrRecoveryInvalid
	}
	if err := domain.ValidateRecoveryNote(note); err != nil {
		return RecoveryEntry{}, false, err
	}
	return s.repository.WaiveRecoveryEntry(
		ctx, strings.TrimSpace(entryID), strings.TrimSpace(operatorUserID),
		strings.TrimSpace(idempotencyKey), strings.TrimSpace(note),
		expectedVersion, s.clock.Now(),
	)
}

func (s *Service) DeleteRecovery(
	ctx context.Context,
	entryID, operatorUserID string,
	expectedVersion int64,
) error {
	if strings.TrimSpace(operatorUserID) == "" || !validRecoveryID(entryID) ||
		expectedVersion <= 0 {
		return domain.ErrRecoveryInvalid
	}
	return s.repository.DeleteRecoveryEntry(
		ctx, strings.TrimSpace(entryID), strings.TrimSpace(operatorUserID),
		expectedVersion, s.clock.Now(),
	)
}
