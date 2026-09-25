package domain

import (
	"errors"
	"strings"
)

// 간이 회수 원장(procurement_recovery_entries, ADR-0052 §5.2)의 기입 계약이다
// (운영정합 5차 PR-D). Return 처분·조달 지연 취소가 만드는 자동 entry에
// 운영자의 수취 기입·수동 생성·포기·삭제를 얹는다. 금액의 집은 이 원장
// 하나이고, funding 사영(payment)은 received 합계를 읽기만 한다.

var (
	ErrRecoveryNotFound          = errors.New("recovery entry not found")
	ErrRecoveryInvalid           = errors.New("recovery entry invalid")
	ErrRecoveryVersionConflict   = errors.New("recovery entry version conflict")
	ErrRecoveryReferenceConflict = errors.New("recovery reference resolves to conflicting orders")
	// ErrRecoveryManualOnly — 자동 생성 entry(Return 처분·지연 취소)는 삭제
	// 대신 포기(WAIVED)로 닫는다. 돈 기록의 하드 삭제는 수동 생성 행만
	// 허용한다(감사 행 동반).
	ErrRecoveryManualOnly = errors.New("recovery entry is not manually created")
)

// ManualRecoveryEvidencePrefix는 수동 생성 entry의 evidence_ref 표식이다 —
// 자동 entry(return:{id}·delay-recovery)와 구분해 삭제 가드를 판정한다.
const ManualRecoveryEvidencePrefix = "operator:"

// ValidManualRecoveryCause는 수동 생성이 허용하는 cause 어휘다. RETURN은
// 자동 entry가 놓친 실물 회수의 수동 보완을 허용하고, CHARGE_WITHOUT_ORDER는
// LIVE 대사(cancel_live)의 자동 전용으로 남긴다.
func ValidManualRecoveryCause(cause string) bool {
	switch strings.TrimSpace(cause) {
	case "MERCHANT_CANCEL", "COST_ADJUSTMENT", "RETURN", "OTHER":
		return true
	default:
		return false
	}
}

// DeriveRecoveryState는 금액에서 상태를 파생한다 — 운영자에게 상태 입력을
// 요구하지 않는다(소유자 단순화안). 포기는 명시 행동으로만 진입하고, 수취
// 기입은 포기를 다시 연다(돈이 실제로 들어오면 포기가 사실과 어긋난다).
func DeriveRecoveryState(expectedMinor, receivedMinor int64) string {
	if receivedMinor <= 0 {
		return "EXPECTED"
	}
	if expectedMinor > 0 && receivedMinor > expectedMinor {
		return "OVER_RECOVERED"
	}
	return "RECEIVED"
}

// ValidateRecoveryAmounts는 기입 금액의 공통 검증이다. expected는 자동 entry
// 상한 근사와 같은 의미의 참고값이라 0을 허용한다.
func ValidateRecoveryAmounts(expectedMinor, receivedMinor int64) error {
	if expectedMinor < 0 || receivedMinor < 0 {
		return ErrRecoveryInvalid
	}
	return nil
}

// ValidateRecoveryNote는 기입 메모의 길이 상한이다(간이 원장 — 서술은 짧게).
func ValidateRecoveryNote(note string) error {
	if len([]rune(strings.TrimSpace(note))) > 500 {
		return ErrRecoveryInvalid
	}
	return nil
}
