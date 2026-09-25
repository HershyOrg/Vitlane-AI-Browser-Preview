package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	procurementapp "github.com/vitlane/vitlane/server/internal/ordering/procurement/app"
	"github.com/vitlane/vitlane/server/internal/ordering/procurement/domain"
)

// 간이 회수 원장 기입(운영정합 5차 PR-D). Return 처분·지연 취소의 자동
// entry와 수동 entry가 한 목록으로 흐르고, 수취 SUM은 payment funding 사영이
// 기존 SQL로 읽는다. 멱등은 execution_audits의 idempotency key replay를
// 재사용한다(claim·result와 같은 전례).

const recoveryColumnsSQL = `
	entry.id::text, entry.merchant_order_id::text, entry.agency_order_id::text,
	entry.cause, entry.expected_amount_minor, entry.received_amount_minor,
	entry.state, COALESCE(entry.evidence_ref,''), COALESCE(entry.note,''),
	entry.version, entry.created_at, entry.updated_at`

func scanRecoveryEntry(scanner interface{ Scan(...any) error }) (procurementapp.RecoveryEntry, error) {
	var entry procurementapp.RecoveryEntry
	var evidenceRef string
	var createdAt, updatedAt time.Time
	err := scanner.Scan(
		&entry.ID, &entry.MerchantOrderID, &entry.AgencyOrderID,
		&entry.Cause, &entry.ExpectedAmountMinor, &entry.ReceivedAmountMinor,
		&entry.State, &evidenceRef, &entry.Note,
		&entry.Version, &createdAt, &updatedAt,
	)
	entry.Manual = strings.HasPrefix(evidenceRef, domain.ManualRecoveryEvidencePrefix)
	entry.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	entry.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return entry, err
}

func (r *Repository) ListRecoveryEntries(
	ctx context.Context,
	referenceID string,
) (procurementapp.RecoverySurface, error) {
	var directOrderID, merchantParentID sql.NullString
	if err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT
		  (SELECT orders.id::text FROM agency_orders orders WHERE orders.id=$1),
		  (SELECT mo.agency_order_id::text FROM merchant_orders mo WHERE mo.id=$1)
	`, referenceID).Scan(&directOrderID, &merchantParentID); err != nil {
		return procurementapp.RecoverySurface{}, err
	}
	if directOrderID.Valid && merchantParentID.Valid && directOrderID.String != merchantParentID.String {
		return procurementapp.RecoverySurface{}, domain.ErrRecoveryReferenceConflict
	}
	agencyOrderID := directOrderID.String
	matchedBy := "AGENCY_ORDER"
	focusMerchantOrderID := ""
	if merchantParentID.Valid {
		agencyOrderID = merchantParentID.String
		matchedBy = "MERCHANT_ORDER"
		focusMerchantOrderID = referenceID
	}
	if agencyOrderID == "" {
		return procurementapp.RecoverySurface{}, domain.ErrRecoveryNotFound
	}
	surface := procurementapp.RecoverySurface{
		AgencyOrderID:        agencyOrderID,
		MatchedBy:            matchedBy,
		FocusMerchantOrderID: focusMerchantOrderID,
		Entries:              make([]procurementapp.RecoveryEntry, 0),
		MerchantOrders:       make([]procurementapp.RecoveryMerchantOrder, 0),
	}
	moRows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT mo.id::text, mo.shop_domain, mo.checkout_ordinal
		FROM merchant_orders mo WHERE mo.agency_order_id=$1
		ORDER BY mo.checkout_ordinal ASC
	`, agencyOrderID)
	if err != nil {
		return surface, fmt.Errorf("list recovery merchant orders: %w", err)
	}
	for moRows.Next() {
		var mo procurementapp.RecoveryMerchantOrder
		if err := moRows.Scan(&mo.ID, &mo.ShopDomain, &mo.CheckoutOrdinal); err != nil {
			moRows.Close()
			return surface, err
		}
		surface.MerchantOrders = append(surface.MerchantOrders, mo)
	}
	moRows.Close()
	if err := moRows.Err(); err != nil {
		return surface, err
	}
	// 주문 자체가 없으면 형식은 맞아도 대상 없음이다 — 목록·선택지 모두 0인
	// surface 대신 명시 오류로 답해 화면이 오타를 구분하게 한다.
	if len(surface.MerchantOrders) == 0 {
		var exists bool
		if err := r.database.Queryer(ctx).QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM agency_orders orders WHERE orders.id=$1)
		`, agencyOrderID).Scan(&exists); err != nil {
			return surface, err
		}
		if !exists {
			return surface, domain.ErrRecoveryNotFound
		}
	}
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT `+recoveryColumnsSQL+`
		FROM procurement_recovery_entries entry
		WHERE entry.agency_order_id=$1
		ORDER BY entry.created_at ASC, entry.id ASC
	`, agencyOrderID)
	if err != nil {
		return surface, fmt.Errorf("list recovery entries: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		entry, err := scanRecoveryEntry(rows)
		if err != nil {
			return surface, err
		}
		surface.Entries = append(surface.Entries, entry)
	}
	return surface, rows.Err()
}

func (r *Repository) lockRecoveryEntry(
	tx context.Context,
	entryID string,
) (procurementapp.RecoveryEntry, error) {
	row := r.database.Queryer(tx).QueryRowContext(tx, `
		SELECT `+recoveryColumnsSQL+`
		FROM procurement_recovery_entries entry WHERE entry.id=$1 FOR UPDATE
	`, entryID)
	entry, err := scanRecoveryEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return entry, domain.ErrRecoveryNotFound
	}
	return entry, err
}

func (r *Repository) insertRecoveryAudit(
	tx context.Context,
	agencyOrderID, merchantOrderID, operatorUserID, action, idempotencyKey string,
	details string,
	now time.Time,
) error {
	_, err := r.database.Queryer(tx).ExecContext(tx, `
		INSERT INTO agency_order_execution_audits(
			id, agency_order_id, merchant_order_id, actor_user_id, action,
			idempotency_key, details, created_at
		) VALUES(gen_random_uuid(),$1,$2,$3,$4,NULLIF($5,''),$6::jsonb,$7)
	`, agencyOrderID, merchantOrderID, operatorUserID, action,
		idempotencyKey, details, now)
	return err
}

func (r *Repository) CreateRecoveryEntry(
	ctx context.Context,
	merchantOrderID, operatorUserID, idempotencyKey, cause string,
	expectedMinor, receivedMinor int64,
	note string,
	now time.Time,
) (procurementapp.RecoveryEntry, bool, error) {
	var created procurementapp.RecoveryEntry
	var replayed bool
	entryID := deterministicRecoveryID(idempotencyKey)
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		replay, err := r.replayAudit(tx, idempotencyKey, "RECOVERY_CREATED",
			merchantOrderID, operatorUserID)
		if err != nil {
			return err
		}
		if replay {
			replayed = true
			created, err = r.lockRecoveryEntry(tx, entryID)
			return err
		}
		var agencyOrderID string
		err = r.database.Queryer(tx).QueryRowContext(tx, `
			SELECT mo.agency_order_id::text FROM merchant_orders mo WHERE mo.id=$1
		`, merchantOrderID).Scan(&agencyOrderID)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrRecoveryInvalid
		}
		if err != nil {
			return err
		}
		row := r.database.Queryer(tx).QueryRowContext(tx, `
			INSERT INTO procurement_recovery_entries(
				id, merchant_order_id, agency_order_id, cause,
				expected_amount_minor, received_amount_minor, state,
				evidence_ref, note, version, created_at, updated_at
			) VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),1,$10,$10)
			RETURNING `+strings.ReplaceAll(recoveryColumnsSQL, "entry.", "procurement_recovery_entries.")+`
		`, entryID, merchantOrderID, agencyOrderID, cause,
			expectedMinor, receivedMinor,
			domain.DeriveRecoveryState(expectedMinor, receivedMinor),
			domain.ManualRecoveryEvidencePrefix+operatorUserID, note, now)
		if created, err = scanRecoveryEntry(row); err != nil {
			return fmt.Errorf("create recovery entry: %w", err)
		}
		return r.insertRecoveryAudit(tx, agencyOrderID, merchantOrderID,
			operatorUserID, "RECOVERY_CREATED", idempotencyKey,
			fmt.Sprintf(`{"cause":%q,"expectedMinor":%d,"receivedMinor":%d}`,
				cause, expectedMinor, receivedMinor), now)
	})
	return created, replayed, err
}

func (r *Repository) RecordRecoveryEntry(
	ctx context.Context,
	entryID, operatorUserID, idempotencyKey string,
	receivedMinor int64,
	note string,
	expectedVersion int64,
	now time.Time,
) (procurementapp.RecoveryEntry, bool, error) {
	var updated procurementapp.RecoveryEntry
	var replayed bool
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		current, err := r.lockRecoveryEntry(tx, entryID)
		if err != nil {
			return err
		}
		replay, err := r.replayAudit(tx, idempotencyKey, "RECOVERY_RECORDED",
			current.MerchantOrderID, operatorUserID)
		if err != nil {
			return err
		}
		if replay {
			replayed = true
			updated = current
			return nil
		}
		if current.Version != expectedVersion {
			return domain.ErrRecoveryVersionConflict
		}
		// 수취 기입은 상태를 금액에서 다시 파생한다 — WAIVED였어도 실제
		// 입금이 확인되면 포기가 사실과 어긋나므로 다시 연다.
		row := r.database.Queryer(tx).QueryRowContext(tx, `
			UPDATE procurement_recovery_entries entry
			SET received_amount_minor=$2, state=$3,
			    note=COALESCE(NULLIF($4,''), entry.note),
			    version=entry.version+1, updated_at=$5
			WHERE entry.id=$1
			RETURNING `+recoveryColumnsSQL+`
		`, entryID, receivedMinor,
			domain.DeriveRecoveryState(current.ExpectedAmountMinor, receivedMinor),
			note, now)
		if updated, err = scanRecoveryEntry(row); err != nil {
			return fmt.Errorf("record recovery entry: %w", err)
		}
		return r.insertRecoveryAudit(tx, current.AgencyOrderID,
			current.MerchantOrderID, operatorUserID, "RECOVERY_RECORDED",
			idempotencyKey,
			fmt.Sprintf(`{"receivedMinor":%d,"state":%q}`, receivedMinor, updated.State),
			now)
	})
	return updated, replayed, err
}

func (r *Repository) WaiveRecoveryEntry(
	ctx context.Context,
	entryID, operatorUserID, idempotencyKey, note string,
	expectedVersion int64,
	now time.Time,
) (procurementapp.RecoveryEntry, bool, error) {
	var updated procurementapp.RecoveryEntry
	var replayed bool
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		current, err := r.lockRecoveryEntry(tx, entryID)
		if err != nil {
			return err
		}
		replay, err := r.replayAudit(tx, idempotencyKey, "RECOVERY_WAIVED",
			current.MerchantOrderID, operatorUserID)
		if err != nil {
			return err
		}
		if replay {
			replayed = true
			updated = current
			return nil
		}
		if current.Version != expectedVersion {
			return domain.ErrRecoveryVersionConflict
		}
		row := r.database.Queryer(tx).QueryRowContext(tx, `
			UPDATE procurement_recovery_entries entry
			SET state='WAIVED', note=COALESCE(NULLIF($2,''), entry.note),
			    version=entry.version+1, updated_at=$3
			WHERE entry.id=$1
			RETURNING `+recoveryColumnsSQL+`
		`, entryID, note, now)
		if updated, err = scanRecoveryEntry(row); err != nil {
			return fmt.Errorf("waive recovery entry: %w", err)
		}
		return r.insertRecoveryAudit(tx, current.AgencyOrderID,
			current.MerchantOrderID, operatorUserID, "RECOVERY_WAIVED",
			idempotencyKey, `{"state":"WAIVED"}`, now)
	})
	return updated, replayed, err
}

func (r *Repository) DeleteRecoveryEntry(
	ctx context.Context,
	entryID, operatorUserID string,
	expectedVersion int64,
	now time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		current, err := r.lockRecoveryEntry(tx, entryID)
		if err != nil {
			return err
		}
		if !current.Manual {
			return domain.ErrRecoveryManualOnly
		}
		if current.Version != expectedVersion {
			return domain.ErrRecoveryVersionConflict
		}
		if _, err := r.database.Queryer(tx).ExecContext(tx, `
			DELETE FROM procurement_recovery_entries WHERE id=$1
		`, entryID); err != nil {
			return fmt.Errorf("delete recovery entry: %w", err)
		}
		return r.insertRecoveryAudit(tx, current.AgencyOrderID,
			current.MerchantOrderID, operatorUserID, "RECOVERY_DELETED", "",
			fmt.Sprintf(`{"cause":%q,"receivedMinor":%d}`,
				current.Cause, current.ReceivedAmountMinor), now)
	})
}

func deterministicRecoveryID(key string) string {
	return deterministicAuditID("manual-recovery:" + key)
}
