package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	procurementapp "github.com/vitlane/vitlane/server/internal/ordering/procurement/app"
	"github.com/vitlane/vitlane/server/internal/ordering/procurement/domain"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

type Repository struct {
	database *sharedpostgres.Database
}

func NewRepository(database *sharedpostgres.Database) *Repository {
	return &Repository{database: database}
}

const queueItemColumns = `
	task.id, task.merchant_order_id, task.agency_order_id, task.state,
	COALESCE(task.assigned_operator_user_id::text,''), task.assigned_at,
	task.lease_until, task.handled_at, task.version, task.created_at, task.updated_at,
	mo.id, mo.agency_order_id, mo.manifest_id, mo.allocation_id, mo.merchant_id, mo.shop_domain,
	mo.checkout_ordinal, mo.checkout_snapshot, mo.execution_mode, mo.state,
	COALESCE(mo.failure_code,''), COALESCE(mo.external_order_ref,''),
	COALESCE(mo.placement_evidence_kind,''),
	COALESCE(mo.placement_receipt_safe_ref,''),
	COALESCE(mo.placement_actual_amount_minor,0),
	COALESCE(mo.placement_evidence_source,''),
	COALESCE(mo.placement_evidence_hash,''), mo.placement_observed_at,
	COALESCE(mo.placement_recorded_by_user_id::text,''), mo.placement_recorded_at,
	COALESCE(mo.result_hash,''), mo.version, mo.created_at, mo.updated_at,
	orders.snapshot, process.state, COALESCE(process.terminal_reason,''),
		COALESCE((SELECT count(*) FROM logistics_expected_units expected
			WHERE expected.merchant_order_id=mo.id),0),
		COALESCE((SELECT count(*) FROM logistics_expected_units expected
			WHERE expected.merchant_order_id=mo.id
			  AND expected.fulfillment='AWAITING_EFFECT'),0),
		COALESCE((SELECT count(*) FROM logistics_expected_units expected
			WHERE expected.merchant_order_id=mo.id
			  AND expected.fulfillment='IN_TRANSIT_EXPECTED'),0),
		COALESCE((SELECT count(*) FROM logistics_expected_units expected
		WHERE expected.merchant_order_id=mo.id
		  AND expected.fulfillment='DELIVERED_EXPECTED'),0),
	COALESCE((SELECT count(*) FROM logistics_expected_units expected
		WHERE expected.merchant_order_id=mo.id
			  AND expected.fulfillment IN ('MISSING','WRONG_ACTUAL','LOST',
			      'DELIVERY_RESOLUTION_PENDING','NONCONFORMING_RESOLUTION_PENDING')),0),
		COALESCE((SELECT count(*) FROM logistics_returns return_record
			JOIN logistics_expected_units expected ON expected.id=return_record.expected_unit_id
			WHERE expected.merchant_order_id=mo.id
			  AND return_record.state IN ('REQUESTED','RETURN_IN_TRANSIT','RECEIVED','MERCHANT_RETURNED')),0),
		COALESCE((SELECT request.state FROM agency_order_refund_requests request
			WHERE request.merchant_order_id=mo.id ORDER BY request.created_at DESC LIMIT 1),''),
		COALESCE((SELECT cancellation.kind FROM agency_order_cancellations cancellation
			WHERE cancellation.merchant_order_id=mo.id ORDER BY cancellation.created_at DESC LIMIT 1),''),
		COALESCE((SELECT resolution.cause FROM logistics_delivery_resolutions resolution
			JOIN logistics_expected_units expected ON expected.id=resolution.expected_unit_id
			WHERE expected.merchant_order_id=mo.id ORDER BY resolution.created_at DESC LIMIT 1),''),
		COALESCE((SELECT resolution.decision FROM logistics_delivery_resolutions resolution
			JOIN logistics_expected_units expected ON expected.id=resolution.expected_unit_id
			WHERE expected.merchant_order_id=mo.id ORDER BY resolution.created_at DESC LIMIT 1),''),
		COALESCE((SELECT return_record.state FROM logistics_returns return_record
			JOIN logistics_expected_units expected ON expected.id=return_record.expected_unit_id
			WHERE expected.merchant_order_id=mo.id ORDER BY return_record.updated_at DESC LIMIT 1),''),
		COALESCE((SELECT compensation.action FROM payment_mo_compensations compensation
			WHERE compensation.allocation_id=mo.allocation_id),''),
		COALESCE((SELECT compensation.state FROM payment_mo_compensations compensation
			WHERE compensation.allocation_id=mo.allocation_id),''),
		COALESCE((SELECT dispute_case.state FROM payment_paypal_dispute_cases dispute_case
			JOIN payment_mo_cash_receipts receipt ON receipt.id=dispute_case.mo_cash_receipt_id
			WHERE receipt.allocation_id=mo.allocation_id
			ORDER BY dispute_case.updated_at DESC LIMIT 1),''),
		COALESCE((SELECT dispute_case.outcome FROM payment_paypal_dispute_cases dispute_case
			JOIN payment_mo_cash_receipts receipt ON receipt.id=dispute_case.mo_cash_receipt_id
			WHERE receipt.allocation_id=mo.allocation_id
			ORDER BY dispute_case.updated_at DESC LIMIT 1),''),
		position.id::text, position.state, position.amount_minor, position.rail`

const queueItemJoin = `
	FROM merchant_order_execution_tasks task
	JOIN merchant_orders mo ON mo.id=task.merchant_order_id
	JOIN agency_orders orders ON orders.id=task.agency_order_id
	JOIN agency_order_processes process ON process.agency_order_id=task.agency_order_id
	JOIN payment_mo_funding_positions position ON position.allocation_id=mo.allocation_id`

func scanQueueItem(scanner interface{ Scan(...any) error }) (procurementapp.QueueItem, error) {
	var item procurementapp.QueueItem
	var assignedAt, leaseUntil, handledAt, evidenceObservedAt, evidenceRecordedAt sql.NullTime
	var evidence domain.PlacementEvidence
	err := scanner.Scan(
		&item.Task.ID, &item.Task.MerchantOrderID, &item.Task.AgencyOrderID,
		&item.Task.State, &item.Task.AssignedOperatorUserID,
		&assignedAt, &leaseUntil, &handledAt,
		&item.Task.Version, &item.Task.CreatedAt, &item.Task.UpdatedAt,
		&item.MerchantOrder.ID, &item.MerchantOrder.AgencyOrderID,
		&item.MerchantOrder.ManifestID, &item.MerchantOrder.AllocationID,
		&item.MerchantOrder.MerchantID,
		&item.MerchantOrder.ShopDomain, &item.MerchantOrder.CheckoutOrdinal,
		&item.MerchantOrder.CheckoutSnapshot, &item.MerchantOrder.ExecutionMode,
		&item.MerchantOrder.State, &item.MerchantOrder.FailureCode,
		&item.MerchantOrder.ExternalOrderRef, &evidence.Kind,
		&evidence.ReceiptSafeRef, &evidence.ActualAmountMinor,
		&evidence.EvidenceSource, &evidence.EvidenceHash, &evidenceObservedAt,
		&evidence.RecordedByUserID, &evidenceRecordedAt,
		&item.MerchantOrder.ResultHash,
		&item.MerchantOrder.Version, &item.MerchantOrder.CreatedAt,
		&item.MerchantOrder.UpdatedAt,
		&item.OrderSnapshot, &item.ProcessState, &item.TerminalReason,
		&item.LogisticsSummary.ExpectedUnits, &item.LogisticsSummary.AwaitingUnits,
		&item.LogisticsSummary.InTransitUnits, &item.LogisticsSummary.DeliveredUnits,
		&item.LogisticsSummary.ExceptionUnits, &item.LogisticsSummary.ReturnInProgressUnits,
		&item.RefundRequestState, &item.CancellationState,
		&item.ResolutionCause, &item.ResolutionDecision, &item.ReturnState,
		&item.CompensationAction, &item.CompensationState,
		&item.DisputeState, &item.DisputeOutcome,
		&item.Funding.PositionID, &item.Funding.State, &item.Funding.AmountMinor,
		&item.Funding.Rail,
	)
	if assignedAt.Valid {
		item.Task.AssignedAt = &assignedAt.Time
	}
	if leaseUntil.Valid {
		item.Task.LeaseUntil = &leaseUntil.Time
	}
	if handledAt.Valid {
		item.Task.HandledAt = &handledAt.Time
	}
	if evidence.Kind != "" {
		evidence.ExternalOrderRef = item.MerchantOrder.ExternalOrderRef
		evidence.Currency = "USD"
		evidence.ClaimsExternalLive = evidence.Kind == domain.PlacementEvidenceLiveEffect
		if evidenceObservedAt.Valid {
			evidence.ObservedAt = evidenceObservedAt.Time
		}
		if evidenceRecordedAt.Valid {
			evidence.RecordedAt = evidenceRecordedAt.Time
		}
		item.MerchantOrder.PlacementEvidence = &evidence
	}
	return item, err
}

func (r *Repository) getQueueItem(ctx context.Context, taskID string) (procurementapp.QueueItem, error) {
	row := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT `+queueItemColumns+queueItemJoin+`
		WHERE task.id=$1
	`, taskID)
	item, err := scanQueueItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		return procurementapp.QueueItem{}, domain.ErrTaskNotFound
	}
	if err != nil {
		return procurementapp.QueueItem{}, fmt.Errorf("get procurement task: %w", err)
	}
	item.Units, err = r.listUnits(ctx, item.MerchantOrder.ID)
	return item, err
}

func (r *Repository) listUnits(ctx context.Context, merchantOrderID string) ([]domain.MerchantOrderUnit, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT id, merchant_order_id, agency_order_id, line_id, unit_index,
		       disposition, version, updated_at
		FROM merchant_order_units
		WHERE merchant_order_id=$1
		ORDER BY line_id, unit_index
	`, merchantOrderID)
	if err != nil {
		return nil, fmt.Errorf("list merchant order units: %w", err)
	}
	defer rows.Close()
	units := make([]domain.MerchantOrderUnit, 0)
	for rows.Next() {
		var unit domain.MerchantOrderUnit
		if err := rows.Scan(
			&unit.ID, &unit.MerchantOrderID, &unit.AgencyOrderID, &unit.LineID,
			&unit.UnitIndex, &unit.Disposition, &unit.Version,
			&unit.UpdatedAt,
		); err != nil {
			return nil, err
		}
		units = append(units, unit)
	}
	return units, rows.Err()
}

func (r *Repository) ListQueue(ctx context.Context, limit int) ([]procurementapp.QueueItem, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT `+queueItemColumns+queueItemJoin+`
		ORDER BY CASE task.state
		         WHEN 'QUEUED' THEN 0 WHEN 'CLAIMED' THEN 1 WHEN 'IN_PROGRESS' THEN 1
		         ELSE 2 END,
		         task.updated_at, task.id
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list procurement queue: %w", err)
	}
	defer rows.Close()
	items := make([]procurementapp.QueueItem, 0)
	for rows.Next() {
		item, err := scanQueueItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range items {
		units, err := r.listUnits(ctx, items[index].MerchantOrder.ID)
		if err != nil {
			return nil, err
		}
		items[index].Units = units
	}
	return items, nil
}

func (r *Repository) replayAudit(
	tx context.Context,
	idempotencyKey, expectedAction, merchantOrderID, actorID string,
) (bool, error) {
	var action, subject, actor string
	err := r.database.Queryer(tx).QueryRowContext(tx, `
		SELECT action, COALESCE(merchant_order_id::text,''), COALESCE(actor_user_id::text,'')
		FROM agency_order_execution_audits WHERE idempotency_key=$1
	`, idempotencyKey).Scan(&action, &subject, &actor)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if action != expectedAction || subject != merchantOrderID || actor != actorID {
		return false, domain.ErrAssignmentConflict
	}
	return true, nil
}

func (r *Repository) lockTask(
	tx context.Context,
	taskID string,
) (domain.ExecutionTask, domain.MerchantOrder, error) {
	var task domain.ExecutionTask
	var order domain.MerchantOrder
	var assignedAt, leaseUntil, handledAt sql.NullTime
	err := r.database.Queryer(tx).QueryRowContext(tx, `
		SELECT task.id, task.merchant_order_id, task.agency_order_id, task.state,
		       COALESCE(task.assigned_operator_user_id::text,''), task.assigned_at,
		       task.lease_until, task.handled_at, task.version,
		       task.created_at, task.updated_at,
		       mo.id, mo.agency_order_id, mo.manifest_id, mo.allocation_id, mo.merchant_id,
		       mo.shop_domain, mo.checkout_ordinal, mo.checkout_snapshot,
		       mo.execution_mode, mo.state, COALESCE(mo.failure_code,''),
		       COALESCE(mo.external_order_ref,''), COALESCE(mo.result_hash,''),
		       mo.version, mo.created_at, mo.updated_at
		FROM merchant_order_execution_tasks task
		JOIN merchant_orders mo ON mo.id=task.merchant_order_id
		WHERE task.id=$1
		FOR UPDATE OF task, mo
	`, taskID).Scan(
		&task.ID, &task.MerchantOrderID, &task.AgencyOrderID, &task.State,
		&task.AssignedOperatorUserID, &assignedAt, &leaseUntil, &handledAt,
		&task.Version, &task.CreatedAt, &task.UpdatedAt,
		&order.ID, &order.AgencyOrderID, &order.ManifestID, &order.AllocationID,
		&order.MerchantID,
		&order.ShopDomain, &order.CheckoutOrdinal, &order.CheckoutSnapshot,
		&order.ExecutionMode, &order.State, &order.FailureCode,
		&order.ExternalOrderRef, &order.ResultHash,
		&order.Version, &order.CreatedAt, &order.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return task, order, domain.ErrTaskNotFound
	}
	if err != nil {
		return task, order, err
	}
	if assignedAt.Valid {
		task.AssignedAt = &assignedAt.Time
	}
	if leaseUntil.Valid {
		task.LeaseUntil = &leaseUntil.Time
	}
	if handledAt.Valid {
		task.HandledAt = &handledAt.Time
	}
	return task, order, nil
}

func (r *Repository) ClaimTask(
	ctx context.Context,
	taskID, operatorUserID, idempotencyKey string,
	leaseUntil, now time.Time,
) (procurementapp.QueueItem, bool, error) {
	var replayed bool
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		task, order, err := r.lockTask(tx, taskID)
		if err != nil {
			return err
		}
		replayed, err = r.replayAudit(tx, idempotencyKey, "OPERATOR_ASSIGNED", order.ID, operatorUserID)
		if err != nil {
			return err
		}
		if replayed {
			return nil
		}
		previousOperatorUserID := task.AssignedOperatorUserID
		takeover := previousOperatorUserID != "" &&
			previousOperatorUserID != operatorUserID
		switch task.State {
		case domain.TaskQueued:
		case domain.TaskClaimed, domain.TaskInProgress:
			// 같은 운영자는 lease 연장, 다른 운영자는 lease 만료 시에만 인수한다.
			if task.AssignedOperatorUserID != operatorUserID &&
				(task.LeaseUntil == nil || task.LeaseUntil.After(now)) {
				return domain.ErrAssignmentConflict
			}
		default:
			return domain.ErrTaskStateInvalid
		}
		transferredEffectState := ""
		if takeover {
			var effectOperatorID, effectState string
			effectErr := r.database.Queryer(tx).QueryRowContext(tx, `
				SELECT operator_user_id::text,state
				FROM procurement_effect_locks
				WHERE merchant_order_id=$1
				FOR UPDATE
			`, order.ID).Scan(&effectOperatorID, &effectState)
			if effectErr != nil && !errors.Is(effectErr, sql.ErrNoRows) {
				return effectErr
			}
			if effectErr == nil {
				if effectOperatorID != previousOperatorUserID &&
					effectOperatorID != operatorUserID {
					return domain.ErrAssignmentConflict
				}
				switch effectState {
				case "FUNDING_PENDING", "FUNDING_UNKNOWN", "STARTED":
					if effectOperatorID != operatorUserID {
						result, updateErr := r.database.Queryer(tx).ExecContext(tx, `
							UPDATE procurement_effect_locks
							SET operator_user_id=$2, version=version+1
							WHERE merchant_order_id=$1 AND operator_user_id=$3
							  AND state=$4
						`, order.ID, operatorUserID, effectOperatorID, effectState)
						if updateErr != nil {
							return updateErr
						}
						if affected, updateErr := result.RowsAffected(); updateErr != nil ||
							affected != 1 {
							if updateErr != nil {
								return updateErr
							}
							return domain.ErrAssignmentConflict
						}
						if err := emitEffectLockEvent(
							tx, r.database.Queryer(tx), order.ID, now,
						); err != nil {
							return err
						}
						transferredEffectState = effectState
					}
				default:
					return domain.ErrTaskStateInvalid
				}
			}
		}
		if _, err := r.database.Queryer(tx).ExecContext(tx, `
			UPDATE merchant_order_execution_tasks
			SET state='CLAIMED', assigned_operator_user_id=$1, assigned_at=$2,
			    lease_until=$3, version=version+1, updated_at=$2
			WHERE id=$4
		`, operatorUserID, now, leaseUntil, taskID); err != nil {
			return err
		}
		auditDetails := map[string]any{"state": "CLAIMED"}
		if takeover {
			auditDetails["previousOperatorUserId"] = previousOperatorUserID
		}
		if transferredEffectState != "" {
			auditDetails["effectLockTransferred"] = true
			auditDetails["effectLockState"] = transferredEffectState
		}
		_, err = r.database.Queryer(tx).ExecContext(tx, `
			INSERT INTO agency_order_execution_audits(
				id, agency_order_id, merchant_order_id, actor_user_id, action,
				idempotency_key, details, created_at
			) VALUES(gen_random_uuid(),$1,$2,$3,'OPERATOR_ASSIGNED',$4,$5,$6)
		`, task.AgencyOrderID, order.ID, operatorUserID, idempotencyKey,
			mustJSON(auditDetails), now)
		return err
	})
	if err != nil {
		return procurementapp.QueueItem{}, false, err
	}
	item, err := r.getQueueItem(ctx, taskID)
	return item, replayed, err
}

// requireActiveAssignment는 결과·reveal 직전의 재검사다: 현재 담당자, 유효한
// lease, 진행 중 상태를 요청 시점에 다시 확인한다(큐 진입 사실만 신뢰하지
// 않는다 — 계약 v7 §14).
func requireActiveAssignment(task domain.ExecutionTask, operatorUserID string, now time.Time) error {
	if task.State != domain.TaskClaimed && task.State != domain.TaskInProgress {
		return domain.ErrTaskStateInvalid
	}
	if task.AssignedOperatorUserID != operatorUserID {
		return domain.ErrAssignmentRequired
	}
	if task.LeaseUntil == nil || !task.LeaseUntil.After(now) {
		return domain.ErrLeaseExpired
	}
	return nil
}

func (r *Repository) requireGrantedReveal(
	tx context.Context,
	merchantOrderID, operatorUserID string,
) error {
	var granted bool
	if err := r.database.Queryer(tx).QueryRowContext(tx, `
		SELECT EXISTS(
			SELECT 1 FROM agency_order_pii_access_audits audit
			WHERE audit.merchant_order_id=$1
			  AND audit.actor_user_id=$2
			  AND audit.outcome='GRANTED'
			  AND audit.action='SHIPPING_ADDRESS_REVEAL'
			  AND audit.reason_code='PLACE_MERCHANT_ORDER'
		)
	`, merchantOrderID, operatorUserID).Scan(&granted); err != nil {
		return err
	}
	if !granted {
		return domain.ErrPIIAccessDenied
	}
	return nil
}

func (r *Repository) RecordFailure(
	ctx context.Context,
	taskID, operatorUserID, idempotencyKey, failureCode string,
	now time.Time,
) (procurementapp.QueueItem, bool, error) {
	var replayed bool
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		task, order, err := r.lockTask(tx, taskID)
		if err != nil {
			return err
		}
		replayed, err = r.replayAudit(tx, idempotencyKey, "FULFILLMENT_FAILED", order.ID, operatorUserID)
		if err != nil {
			return err
		}
		if replayed {
			return nil
		}
		if err := requireActiveAssignment(task, operatorUserID, now); err != nil {
			return err
		}
		if order.State != domain.OrderPlanned && order.State != domain.OrderPlacementPending {
			return domain.ErrTaskStateInvalid
		}
		if effectState, found, err := r.lockMerchantEffectState(tx, order.ID); err != nil {
			return err
		} else if found && unresolvedFundingEffect(effectState) {
			// A provider Capture may already be in flight. Keep the MO/task open
			// until its exact outcome is reconciled; a generic operator failure may
			// not create a contradictory terminal projection.
			return domain.ErrFundingNotReady
		}
		// A generic failure code is not customer-facing evidence.  Both a
		// pre-effect stop and a failed placement attempt require the latest
		// manual decision card to explain why purchasing is impossible.
		var latestDecision domain.ManualDecision
		if err := r.database.Queryer(tx).QueryRowContext(tx, `
			SELECT decision FROM procurement_decision_records
			WHERE merchant_order_id=$1 AND task_id=$2
			ORDER BY created_at DESC, id DESC LIMIT 1
		`, order.ID, task.ID).Scan(&latestDecision); errors.Is(err, sql.ErrNoRows) {
			return domain.ErrManualDecisionRequired
		} else if err != nil {
			return err
		}
		if latestDecision != domain.DecisionUnableToPurchase {
			return domain.ErrManualDecisionRequired
		}
		if _, err := r.database.Queryer(tx).ExecContext(tx, `
			UPDATE merchant_orders
			SET state='FAILED', failure_code=$1, version=version+1, updated_at=$2
			WHERE id=$3
		`, failureCode, now, order.ID); err != nil {
			return err
		}
		if _, err := r.database.Queryer(tx).ExecContext(tx, `
			UPDATE merchant_payments
			SET state='FAILED', version=version+1, updated_at=$1
			WHERE merchant_order_id=$2 AND state IN ('PLANNED','EXECUTION_PENDING')
		`, now, order.ID); err != nil {
			return err
		}
		// Compensation is one immutable allocation for this whole MO. Physical
		// units remain logistics identities and never receive monetary states.
		if _, err := r.database.Queryer(tx).ExecContext(tx, `
			UPDATE merchant_order_execution_tasks
			SET state='FAILED', handled_at=$1, version=version+1, updated_at=$1
			WHERE id=$2
		`, now, taskID); err != nil {
			return err
		}
		if order.State == domain.OrderPlacementPending {
			if _, err := r.database.Queryer(tx).ExecContext(tx, `
				UPDATE procurement_effect_locks
				SET state='FAILED', resolved_at=$1, version=version+1
				WHERE merchant_order_id=$2 AND state='STARTED'
			`, now, order.ID); err != nil {
				return err
			}
			if err := emitEffectLockEvent(
				tx, r.database.Queryer(tx), order.ID, now,
			); err != nil {
				return err
			}
		}
		if err := emitMerchantOrderEvent(tx, r.database.Queryer(tx), order.ID, now); err != nil {
			return err
		}
		// 실패 reason의 process 반영은 Manager가 mo FAILED 이벤트 소비로
		// 결정한다(ADR-0056 — 종전 last_reason_code 직접 쓰기 폐지).
		_, err = r.database.Queryer(tx).ExecContext(tx, `
			INSERT INTO agency_order_execution_audits(
				id, agency_order_id, merchant_order_id, actor_user_id, action,
				idempotency_key, details, created_at
			) VALUES(gen_random_uuid(),$1,$2,$3,'FULFILLMENT_FAILED',$4,$5,$6)
		`, task.AgencyOrderID, order.ID, operatorUserID, idempotencyKey,
			mustJSON(map[string]any{"outcome": "FAILED", "failureCode": failureCode}), now)
		return err
	})
	if err != nil {
		return procurementapp.QueueItem{}, false, err
	}
	item, err := r.getQueueItem(ctx, taskID)
	return item, replayed, err
}

func (r *Repository) AuthorizeShippingReveal(
	ctx context.Context,
	taskID, operatorUserID, reasonCode, reasonDetail, correlationID, idempotencyKey string,
	now time.Time,
) (string, bool, error) {
	var snapshotID string
	var replayed bool
	var denial error
	// 거절이어도 트랜잭션은 커밋한다 — DENIED 감사를 보존하고(운영정합 5차
	// B3) 커밋 뒤에 거절을 에러로 변환한다.
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		var err error
		snapshotID, replayed, denial, err = r.authorizeReveal(
			tx, taskID, operatorUserID, "SHIPPING_ADDRESS_REVEAL",
			reasonCode, reasonDetail, correlationID, idempotencyKey, now,
		)
		return err
	})
	if err == nil && denial != nil {
		err = denial
	}
	return snapshotID, replayed, err
}

func (r *Repository) AuthorizeContinueURLReveal(
	ctx context.Context,
	taskID, operatorUserID, reasonCode, reasonDetail, correlationID, idempotencyKey string,
	now time.Time,
) (procurementapp.ContinueURLRef, bool, error) {
	var ref procurementapp.ContinueURLRef
	var replayed bool
	var denial error
	// 거절이어도 커밋해 DENIED 감사를 보존한다(운영정합 5차 B3) — ref 조회는
	// 인가된 경우에만 수행하고, 거절은 커밋 뒤 에러로 변환한다.
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		_, granted, deniedErr, err := r.authorizeReveal(
			tx, taskID, operatorUserID, "CONTINUE_URL_REVEAL",
			reasonCode, reasonDetail, correlationID, idempotencyKey, now,
		)
		if err != nil {
			return err
		}
		replayed = granted
		if denial = deniedErr; denial != nil {
			return nil
		}
		err = r.database.Queryer(tx).QueryRowContext(tx, `
			SELECT orders.user_id::text,
			       COALESCE(mo.checkout_snapshot->>'continueUrlSafeRef',''),
			       COALESCE(mo.checkout_snapshot->>'continueUrlHash',''),
			       mo.shop_domain,
			       COALESCE((mo.checkout_snapshot->>'expiresAt')::timestamptz, $2)
			FROM merchant_order_execution_tasks task
			JOIN merchant_orders mo ON mo.id=task.merchant_order_id
			JOIN agency_orders orders ON orders.id=task.agency_order_id
			WHERE task.id=$1
		`, taskID, now).Scan(&ref.OwnerUserID, &ref.SafeRef, &ref.Hash, &ref.ShopDomain, &ref.ExpiresAt)
		return err
	})
	if err == nil && denial != nil {
		err = denial
	}
	return ref, replayed, err
}

// authorizeReveal은 감사 hash chain을 유지하며 reveal 인가를 기록한다. 조건
// (담당·lease)이 미충족이면 DENIED 감사를 남기고 거절한다.
//
// 거절은 denial 반환값으로 나른다 — 에러로 반환하면 트랜잭션 wrapper가
// rollback해 DENIED 감사가 소실된다(운영정합 5차 B3). 호출부는 커밋 후
// denial을 에러로 변환한다. err는 infra 실패 전용(rollback 대상)이다.
func (r *Repository) authorizeReveal(
	tx context.Context,
	taskID, operatorUserID, action, reasonCode, reasonDetail, correlationID, idempotencyKey string,
	now time.Time,
) (snapshotID string, replayed bool, denial error, err error) {
	var existingAction, existingSubject, existingActor, existingOutcome string
	var existingSnapshot sql.NullString
	err = r.database.Queryer(tx).QueryRowContext(tx, `
		SELECT action, COALESCE(merchant_order_id::text,''),
		       actor_user_id::text, outcome, shipping_snapshot_id::text
		FROM agency_order_pii_access_audits WHERE idempotency_key=$1
	`, idempotencyKey).Scan(
		&existingAction, &existingSubject, &existingActor, &existingOutcome,
		&existingSnapshot,
	)
	if err == nil {
		task, _, lookupErr := r.lockTask(tx, taskID)
		if lookupErr != nil {
			return "", false, nil, lookupErr
		}
		if existingAction != action || existingSubject != task.MerchantOrderID ||
			existingActor != operatorUserID || existingOutcome != "GRANTED" {
			return "", false, domain.ErrPIIAccessDenied, nil
		}
		return existingSnapshot.String, true, nil, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, nil, err
	}

	task, order, err := r.lockTask(tx, taskID)
	if err != nil {
		return "", false, nil, err
	}
	if err := r.database.Queryer(tx).QueryRowContext(tx, `
		SELECT orders.shipping_snapshot_id::text FROM agency_orders orders WHERE orders.id=$1
	`, task.AgencyOrderID).Scan(&snapshotID); err != nil {
		return "", false, nil, err
	}
	outcome, denialCode := "GRANTED", ""
	// 거절 사유(담당·lease·상태)는 타입을 보존해 반환한다 — 종전처럼
	// ErrPIIAccessDenied로 뭉개면 핸들러의 LEASE_EXPIRED 409("다시 담당해
	// 주세요") 매핑이 영원히 도달하지 못한다(운영정합 5차 B2).
	assignErr := requireActiveAssignment(task, operatorUserID, now)
	if assignErr != nil {
		outcome, denialCode = "DENIED", "ASSIGNMENT_OR_LEASE_REQUIRED"
	}
	var previousHash string
	_ = r.database.Queryer(tx).QueryRowContext(tx, `
		SELECT event_hash FROM agency_order_pii_access_audits
		ORDER BY created_at DESC, id DESC LIMIT 1 FOR UPDATE
	`).Scan(&previousHash)
	auditID := deterministicAuditID(idempotencyKey)
	eventHash := revealAuditHash(previousHash, task.AgencyOrderID, order.ID,
		operatorUserID, action, reasonCode, reasonDetail, outcome, denialCode,
		correlationID, idempotencyKey, now)
	snapshotColumn := any(nil)
	if action == "SHIPPING_ADDRESS_REVEAL" {
		snapshotColumn = snapshotID
	}
	if _, err := r.database.Queryer(tx).ExecContext(tx, `
		INSERT INTO agency_order_pii_access_audits(
			id, agency_order_id, merchant_order_id, shipping_snapshot_id,
			actor_user_id, action, reason_code, reason_detail, outcome, denial_code,
			correlation_id, idempotency_key, previous_event_hash, event_hash, created_at
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),$11,$12,NULLIF($13,''),$14,$15)
	`, auditID, task.AgencyOrderID, order.ID, snapshotColumn, operatorUserID,
		action, reasonCode, reasonDetail, outcome, denialCode, correlationID,
		idempotencyKey, previousHash, eventHash, now); err != nil {
		return "", false, nil, err
	}
	if outcome != "GRANTED" {
		return "", false, assignErr, nil
	}
	return snapshotID, false, nil, nil
}

func mustJSON(value any) json.RawMessage { payload, _ := json.Marshal(value); return payload }

func deterministicAuditID(key string) string {
	sum := sha256.Sum256([]byte("procurement-reveal:" + key))
	encoded := hex.EncodeToString(sum[:16])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

func revealAuditHash(previous, agencyOrderID, merchantOrderID, actor, action, reasonCode, reasonDetail, outcome, denial, correlationID, key string, now time.Time) string {
	canonical := strings.Join([]string{previous, agencyOrderID, merchantOrderID,
		actor, action, reasonCode, reasonDetail, outcome, denial, correlationID,
		key, now.UTC().Format(time.RFC3339Nano)}, "\x00")
	sum := sha256.Sum256([]byte(canonical))
	return "0x" + hex.EncodeToString(sum[:])
}

// CountOpenTasks — nav 뱃지용 전역 카운트(ADR-0057 2차 P2). 진행 국면은
// 큐 표시 의미와 동일하게 종결(SUCCEEDED·FAILED·CANCELLED) 이전이다.
func (r *Repository) CountOpenTasks(ctx context.Context) (int, error) {
	var count int
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT count(*) FROM merchant_order_execution_tasks
		WHERE state IN ('QUEUED','CLAIMED','IN_PROGRESS','OUTCOME_UNKNOWN')
	`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count open tasks: %w", err)
	}
	return count, nil
}
