package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	logisticsapp "github.com/vitlane/vitlane/server/internal/ordering/logistics/app"
	"github.com/vitlane/vitlane/server/internal/ordering/logistics/domain"
)

// ListExceptionUnits는 판정 대기 배송 예외 큐다(§9.1 — MISSING/WRONG_ACTUAL/
// LOST, write-once 판정 없음).
func (r *Repository) ListExceptionUnits(
	ctx context.Context,
	limit int,
) ([]domain.ExpectedUnit, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT expected.id, expected.merchant_order_unit_id, expected.merchant_order_id,
		       expected.agency_order_id, expected.line_id, expected.unit_index,
		       expected.fulfillment, expected.registered_at,
		       expected.version, expected.updated_at
		FROM logistics_expected_units expected
		WHERE expected.fulfillment IN ('MISSING','WRONG_ACTUAL','LOST')
		  AND NOT EXISTS (
		      SELECT 1 FROM logistics_delivery_resolutions resolution
		      WHERE resolution.expected_unit_id=expected.id
		  )
		ORDER BY expected.updated_at
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list exception units: %w", err)
	}
	defer rows.Close()
	units := []domain.ExpectedUnit{}
	for rows.Next() {
		var unit domain.ExpectedUnit
		var fulfillment string
		if err := rows.Scan(
			&unit.ID, &unit.MerchantOrderUnitID, &unit.MerchantOrderID,
			&unit.AgencyOrderID, &unit.LineID, &unit.UnitIndex,
			&fulfillment, &unit.RegisteredAt,
			&unit.Version, &unit.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan exception unit: %w", err)
		}
		unit.Fulfillment = domain.Fulfillment(fulfillment)
		units = append(units, unit)
	}
	return units, rows.Err()
}

// ListResolvedExceptionUnits는 판정이 종결된 예외 unit의 최근 목록이다
// (ADR-0057 — 운영자 "처리 완료" 열람, 행동 없음).
func (r *Repository) ListResolvedExceptionUnits(
	ctx context.Context,
	limit int,
) ([]domain.ResolvedExceptionUnit, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT expected.id, expected.merchant_order_unit_id, expected.merchant_order_id,
		       expected.agency_order_id, expected.line_id, expected.unit_index,
		       expected.fulfillment, expected.registered_at,
		       expected.version, expected.updated_at,
		       resolution.id, merchant_order.allocation_id, resolution.cause,
		       resolution.decision, COALESCE(resolution.note,''), resolution.created_at,
		       COALESCE(return_record.state,''), COALESCE(compensation.action,''),
		       COALESCE(compensation.state,'')
		FROM logistics_expected_units expected
		JOIN logistics_delivery_resolutions resolution
		  ON resolution.expected_unit_id=expected.id
		JOIN merchant_orders merchant_order
		  ON merchant_order.id=expected.merchant_order_id
		LEFT JOIN logistics_returns return_record
		  ON return_record.expected_unit_id=expected.id
		LEFT JOIN payment_mo_compensations compensation
		  ON compensation.allocation_id=merchant_order.allocation_id
		WHERE expected.fulfillment IN ('RESOLVED','NONCONFORMING_RESOLVED')
		ORDER BY expected.updated_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list resolved exception units: %w", err)
	}
	defer rows.Close()
	units := []domain.ResolvedExceptionUnit{}
	for rows.Next() {
		var item domain.ResolvedExceptionUnit
		var fulfillment, cause, decision string
		if err := rows.Scan(
			&item.ID, &item.MerchantOrderUnitID, &item.MerchantOrderID,
			&item.AgencyOrderID, &item.LineID, &item.UnitIndex,
			&fulfillment, &item.RegisteredAt, &item.Version, &item.UpdatedAt,
			&item.Resolution.ID, &item.Resolution.AllocationID, &cause,
			&decision, &item.Resolution.Note, &item.Resolution.CreatedAt,
			&item.ReturnState, &item.CompensationAction, &item.CompensationState,
		); err != nil {
			return nil, fmt.Errorf("scan resolved exception unit: %w", err)
		}
		item.Fulfillment = domain.Fulfillment(fulfillment)
		item.Resolution.ExpectedUnitID = item.ID
		item.Resolution.MerchantOrderID = item.MerchantOrderID
		item.Resolution.AgencyOrderID = item.AgencyOrderID
		item.Resolution.Cause = domain.Fulfillment(cause)
		item.Resolution.Decision = domain.ResolutionDecision(decision)
		units = append(units, item)
	}
	return units, rows.Err()
}

func (r *Repository) lockExpectedUnit(
	tx context.Context,
	expectedUnitID string,
) (domain.ExpectedUnit, error) {
	var unit domain.ExpectedUnit
	var fulfillment string
	err := r.database.Queryer(tx).QueryRowContext(tx, `
		SELECT id, merchant_order_unit_id, merchant_order_id, agency_order_id,
		       line_id, unit_index, fulfillment, registered_at,
		       version, updated_at
		FROM logistics_expected_units WHERE id=$1 FOR UPDATE
	`, expectedUnitID).Scan(
		&unit.ID, &unit.MerchantOrderUnitID, &unit.MerchantOrderID,
		&unit.AgencyOrderID, &unit.LineID, &unit.UnitIndex,
		&fulfillment, &unit.RegisteredAt,
		&unit.Version, &unit.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ExpectedUnit{}, domain.ErrUnitNotFound
	}
	if err != nil {
		return domain.ExpectedUnit{}, fmt.Errorf("lock expected unit: %w", err)
	}
	unit.Fulfillment = domain.Fulfillment(fulfillment)
	return unit, nil
}

// ResolveDeliveryException은 write-once 판정이다. REFUND는 fulfillment를
// RESOLVED로 닫고 Process가 동일 MerchantOrder의 whole-MO compensation을
// 발행한다. DELIVERED_OK는 오탐 정정이다.
func (r *Repository) ResolveDeliveryException(
	ctx context.Context,
	expectedUnitID string,
	decision domain.ResolutionDecision,
	note, operatorUserID string,
	now time.Time,
) (domain.DeliveryResolution, bool, error) {
	var resolution domain.DeliveryResolution
	var replayed bool
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		unit, err := r.lockExpectedUnit(tx, expectedUnitID)
		if err != nil {
			return err
		}
		var allocationID string
		if err := r.database.Queryer(tx).QueryRowContext(tx, `
			SELECT allocation_id::text FROM merchant_orders WHERE id=$1
		`, unit.MerchantOrderID).Scan(&allocationID); err != nil {
			return fmt.Errorf("read delivery resolution allocation: %w", err)
		}
		var existingCause, existingDecision, existingNote string
		var existingAt time.Time
		err = r.database.Queryer(tx).QueryRowContext(tx, `
			SELECT id, cause, decision, COALESCE(note,''), created_at
			FROM logistics_delivery_resolutions WHERE expected_unit_id=$1
		`, expectedUnitID).Scan(
			&resolution.ID, &existingCause, &existingDecision, &existingNote, &existingAt,
		)
		if err == nil {
			resolution.ExpectedUnitID = expectedUnitID
			resolution.MerchantOrderID = unit.MerchantOrderID
			resolution.AllocationID = allocationID
			resolution.AgencyOrderID = unit.AgencyOrderID
			resolution.Cause = domain.Fulfillment(existingCause)
			resolution.Decision = domain.ResolutionDecision(existingDecision)
			resolution.Note = existingNote
			resolution.CreatedAt = existingAt
			replayed = true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if !domain.ResolvableFulfillment(unit.Fulfillment) {
			return domain.ErrResolutionInvalid
		}
		resolution = domain.DeliveryResolution{
			ID:              deterministicID(expectedUnitID + ":resolution"),
			ExpectedUnitID:  expectedUnitID,
			MerchantOrderID: unit.MerchantOrderID,
			AllocationID:    allocationID,
			AgencyOrderID:   unit.AgencyOrderID,
			Cause:           unit.Fulfillment,
			Decision:        decision,
			Note:            note,
			CreatedAt:       now,
		}
		if _, err := r.database.Queryer(tx).ExecContext(tx, `
			INSERT INTO logistics_delivery_resolutions(
				id, expected_unit_id, agency_order_id, cause, decision, note,
				decided_by_user_id, created_at
			) VALUES($1, $2::uuid, $3::uuid, $4, $5, NULLIF($6,''), NULLIF($7,'')::uuid, $8)
		`, resolution.ID, expectedUnitID, unit.AgencyOrderID, string(unit.Fulfillment),
			string(decision), note, operatorUserID, now); err != nil {
			return err
		}
		nextFulfillment := string(domain.FulfillmentResolved)
		if decision == domain.ResolutionDeliveredOK {
			nextFulfillment = string(domain.FulfillmentDeliveredExpected)
		}
		if _, err := r.database.Queryer(tx).ExecContext(tx, `
			UPDATE logistics_expected_units
			SET fulfillment=$2, version=version+1, updated_at=$3
			WHERE id=$1
		`, expectedUnitID, nextFulfillment, now); err != nil {
			return err
		}
		q := r.database.Queryer(tx)
		if err := emitDeliveryFaultJudged(tx, q, expectedUnitID,
			string(decision), now); err != nil {
			return err
		}
		return emitUnitEvent(tx, q, expectedUnitID, now)
	})
	if err != nil {
		return domain.DeliveryResolution{}, false, err
	}
	return resolution, replayed, nil
}

func scanReturn(row interface{ Scan(...any) error }) (domain.Return, error) {
	var item domain.Return
	var state, disposition, note string
	err := row.Scan(
		&item.ID, &item.ExpectedUnitID, &item.AgencyOrderID, &state,
		&disposition, &note, &item.Version, &item.CreatedAt, &item.UpdatedAt,
	)
	item.State = domain.ReturnState(state)
	item.MerchantDisposition = disposition
	item.Note = note
	return item, err
}

const returnColumnsSQL = `id, expected_unit_id, agency_order_id, state,
	COALESCE(merchant_disposition,''), COALESCE(note,''), version, created_at, updated_at`

// CreateReturn은 §9.3 수동 회수 lane 시작이다. 오배송(WRONG_ACTUAL)·수령 후
// 하자(DELIVERED_EXPECTED)·판정 종결(RESOLVED) unit만 대상이며 unit당 하나다.
func (r *Repository) CreateReturn(
	ctx context.Context,
	expectedUnitID, note, operatorUserID string,
	now time.Time,
) (domain.Return, error) {
	var created domain.Return
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		unit, err := r.lockExpectedUnit(tx, expectedUnitID)
		if err != nil {
			return err
		}
		switch unit.Fulfillment {
		case domain.FulfillmentWrongActual, domain.FulfillmentDeliveredExpected,
			domain.FulfillmentResolved:
		default:
			return domain.ErrReturnInvalid
		}
		row := r.database.Queryer(tx).QueryRowContext(tx, `
			INSERT INTO logistics_returns(
				id, expected_unit_id, agency_order_id, state, note,
				created_by_user_id, version, created_at, updated_at
			) VALUES($1, $2::uuid, $3::uuid, 'REQUESTED', NULLIF($4,''),
			        NULLIF($5,'')::uuid, 1, $6, $6)
			ON CONFLICT (expected_unit_id) DO NOTHING
			RETURNING `+returnColumnsSQL+`
		`, deterministicID(expectedUnitID+":return"), expectedUnitID,
			unit.AgencyOrderID, note, operatorUserID, now)
		created, err = scanReturn(row)
		if errors.Is(err, sql.ErrNoRows) {
			// 이미 존재 — 멱등 반환.
			existing := r.database.Queryer(tx).QueryRowContext(tx, `
				SELECT `+returnColumnsSQL+` FROM logistics_returns WHERE expected_unit_id=$1
			`, expectedUnitID)
			created, err = scanReturn(existing)
		}
		if err != nil {
			return fmt.Errorf("create return: %w", err)
		}
		return emitReturnEvent(tx, r.database.Queryer(tx), created.ID, now)
	})
	if err != nil {
		return domain.Return{}, err
	}
	return created, nil
}

// UpdateReturn은 상태 전이와 merchant disposition 기록이다. MERCHANT_REFUNDED
// 처분은 간이 회수 원장(procurement_recovery_entries, cause=RETURN) entry를
// 멱등 생성한다 — 실제 입금 대사는 운영자가 원장에서 갱신한다.
func (r *Repository) UpdateReturn(
	ctx context.Context,
	returnID, state, merchantDisposition, note, operatorUserID string,
	now time.Time,
) (domain.Return, error) {
	var updated domain.Return
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		row := r.database.Queryer(tx).QueryRowContext(tx, `
			SELECT `+returnColumnsSQL+` FROM logistics_returns WHERE id=$1 FOR UPDATE
		`, returnID)
		current, err := scanReturn(row)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrReturnNotFound
		}
		if err != nil {
			return fmt.Errorf("lock return: %w", err)
		}
		next := current.State
		if state != "" {
			next = domain.ReturnState(state)
			if !domain.AllowedReturnTransition(current.State, next) {
				return domain.ErrReturnInvalid
			}
		}
		disposition := current.MerchantDisposition
		if merchantDisposition != "" {
			disposition = merchantDisposition
		}
		result := r.database.Queryer(tx).QueryRowContext(tx, `
			UPDATE logistics_returns
			SET state=$2, merchant_disposition=NULLIF($3,''),
			    note=COALESCE(NULLIF($4,''), note),
			    version=version+1, updated_at=$5
			WHERE id=$1
			RETURNING `+returnColumnsSQL+`
		`, returnID, string(next), disposition, note, now)
		updated, err = scanReturn(result)
		if err != nil {
			return fmt.Errorf("update return: %w", err)
		}
		if err := emitReturnEvent(tx, r.database.Queryer(tx), updated.ID, now); err != nil {
			return err
		}
		if disposition != "MERCHANT_REFUNDED" || current.MerchantDisposition == "MERCHANT_REFUNDED" {
			return nil
		}
		// A whole-MO return opens at most one recovery expectation. The planned
		// merchant spend is the operational estimate; operators append the actual
		// recovered cash to the event ledger separately.
		_, err = r.database.Queryer(tx).ExecContext(tx, `
			INSERT INTO procurement_recovery_entries(
				id, merchant_order_id, agency_order_id, cause,
				expected_amount_minor, received_amount_minor, state,
				evidence_ref, created_at, updated_at
			)
			SELECT md5(expected.merchant_order_id::text||':return-recovery')::uuid,
			       expected.merchant_order_id, expected.agency_order_id, 'RETURN',
			       payment.amount_minor, 0, 'EXPECTED', $1, $2, $2
			FROM logistics_expected_units expected
			JOIN merchant_payments payment
			  ON payment.merchant_order_id=expected.merchant_order_id
			WHERE expected.id=$3
			ON CONFLICT (id) DO NOTHING
		`, "return:"+returnID, now, updated.ExpectedUnitID)
		if err != nil {
			return fmt.Errorf("record return recovery: %w", err)
		}
		return nil
	})
	if err != nil {
		return domain.Return{}, err
	}
	return updated, nil
}

// ListReturns는 회수 lane 목록이다(최근 갱신 순). openOnly는 종결 상태를
// 제외해 운영자 진행 화면의 서버 진실이 된다.
// ListClosedReturns는 종결(CLOSED·CANCELLED) 회수의 최근 목록이다(ADR-0057 —
// 운영자 "처리 완료" 열람, 행동 없음).
func (r *Repository) ListClosedReturns(
	ctx context.Context,
	limit int,
) ([]domain.Return, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT `+returnColumnsSQL+`
		FROM logistics_returns
		WHERE state IN ('CLOSED','CANCELLED')
		ORDER BY updated_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list closed returns: %w", err)
	}
	defer rows.Close()
	items := []domain.Return{}
	for rows.Next() {
		item, err := scanReturn(rows)
		if err != nil {
			return nil, fmt.Errorf("scan closed return: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) ListReturns(
	ctx context.Context,
	openOnly bool,
	limit int,
) ([]domain.Return, error) {
	stateFilter := ""
	if openOnly {
		stateFilter = ` WHERE state NOT IN ('CLOSED','CANCELLED')`
	}
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT `+returnColumnsSQL+`
		FROM logistics_returns`+stateFilter+`
		ORDER BY updated_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list returns: %w", err)
	}
	defer rows.Close()
	items := []domain.Return{}
	for rows.Next() {
		item, err := scanReturn(rows)
		if err != nil {
			return nil, fmt.Errorf("scan return: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// CountExceptionUnits·CountOpenReturns — nav 뱃지용 전역 카운트(ADR-0057 2차
// P2). 술어는 각 목록 조회와 동일하다.
func (r *Repository) CountExceptionUnits(ctx context.Context) (int, error) {
	var count int
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT count(*)
		FROM logistics_expected_units expected
		WHERE expected.fulfillment IN ('MISSING','WRONG_ACTUAL','LOST')
		  AND NOT EXISTS (
		      SELECT 1 FROM logistics_delivery_resolutions resolution
		      WHERE resolution.expected_unit_id=expected.id
		  )
	`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count exception units: %w", err)
	}
	return count, nil
}

func (r *Repository) CountOpenReturns(ctx context.Context) (int, error) {
	var count int
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT count(*) FROM logistics_returns
		WHERE state NOT IN ('CLOSED','CANCELLED')
	`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count open returns: %w", err)
	}
	return count, nil
}

// GetDeliveryResolution은 SUPPORT executor의 판정 사실 조회다(ADR-0070 §4.5).
func (r *Repository) GetDeliveryResolution(
	ctx context.Context,
	resolutionID string,
) (logisticsapp.DeliverySupportProjection, error) {
	var item logisticsapp.DeliverySupportProjection
	var cause, decision string
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT resolution.id::text, resolution.expected_unit_id::text,
		       expected.merchant_order_id::text, orders.allocation_id::text,
		       resolution.agency_order_id::text, resolution.cause,
		       resolution.decision, COALESCE(resolution.note,''),
		       resolution.created_at,
		       COALESCE(resolution.decided_by_user_id::text,'')
		FROM logistics_delivery_resolutions resolution
		JOIN logistics_expected_units expected
		  ON expected.id=resolution.expected_unit_id
		JOIN merchant_orders orders ON orders.id=expected.merchant_order_id
		WHERE resolution.id=$1
	`, resolutionID).Scan(
		&item.Resolution.ID, &item.Resolution.ExpectedUnitID,
		&item.Resolution.MerchantOrderID, &item.Resolution.AllocationID,
		&item.Resolution.AgencyOrderID, &cause, &decision,
		&item.Resolution.Note, &item.Resolution.CreatedAt,
		&item.OperatorUserID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return logisticsapp.DeliverySupportProjection{}, domain.ErrResolutionNotFound
	}
	if err != nil {
		return logisticsapp.DeliverySupportProjection{}, fmt.Errorf("get delivery resolution: %w", err)
	}
	item.Resolution.Cause = domain.Fulfillment(cause)
	item.Resolution.Decision = domain.ResolutionDecision(decision)
	return item, nil
}
