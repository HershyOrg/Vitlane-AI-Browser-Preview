package postgres

import (
	"context"
	"database/sql"
	"errors"

	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
)

// 종전 고지함 CRUD(CreateNotice·ListNotices·MarkNoticeRead)는 ADR-0059로
// Support 대화에 흡수되어 제거됐다. 이 파일에는 주문 소유자 해석만 남는다 —
// SYSTEM 지연 rule 고지는 lifecycle.go의 RecordDelayRuleNotice가 계속 쓴다.

// GetRefundRequestByID는 SUPPORT executor의 환불 요청 조회다(ADR-0070 §4.5).
func (r *Repository) GetRefundRequestByID(
	ctx context.Context,
	requestID string,
) (agencydomain.RefundRequest, error) {
	return r.getRefundRequest(ctx, requestID, true)
}

// GetDelayRuleNotice는 기록된 SYSTEM 지연 rule 고지 한 건이다.
func (r *Repository) GetDelayRuleNotice(
	ctx context.Context,
	agencyOrderID, idempotencyKey string,
) (agencydomain.CustomerNotice, error) {
	var notice agencydomain.CustomerNotice
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT id::text, agency_order_id::text, user_id::text, kind, body,
		       idempotency_key, created_at
		FROM agency_order_customer_notices
		WHERE agency_order_id=$1 AND idempotency_key=$2
	`, agencyOrderID, idempotencyKey).Scan(
		&notice.ID, &notice.AgencyOrderID, &notice.UserID, &notice.Kind, &notice.Body,
		&notice.IdempotencyKey, &notice.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return agencydomain.CustomerNotice{}, agencydomain.ErrNotFound
	}
	return notice, err
}

// ResolveOrderOwner는 주문 소유 고객을 확인한다(Support 주문 첨부 검증·
// 지연 rule 고지 대상 해석).
func (r *Repository) ResolveOrderOwner(
	ctx context.Context,
	agencyOrderID string,
) (string, error) {
	var owner string
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT user_id::text FROM agency_orders WHERE id=$1
	`, agencyOrderID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", agencydomain.ErrNotFound
	}
	return owner, err
}
