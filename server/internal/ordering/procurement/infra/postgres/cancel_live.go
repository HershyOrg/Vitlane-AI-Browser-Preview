package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/policy"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	procurementapp "github.com/vitlane/vitlane/server/internal/ordering/procurement/app"
	"github.com/vitlane/vitlane/server/internal/ordering/procurement/domain"
)

// CancelMerchantOrder changes only Procurement-owned facts. The Process has
// already locked its control, and obtained authoritative Payment/Logistics facts
// under their app ports in this same transaction.
func (r *Repository) CancelMerchantOrder(ctx context.Context, orderID, moID, userID, kind string, authority procurementapp.CancellationAuthority, now time.Time) (replay bool, err error) {
	action := procmsg.EffectCancelPrePurchase
	if kind == "DELAY_RULE" {
		action = procmsg.EffectCancelByDelayRule
	} else if kind != "PRE_EFFECT" {
		return false, domain.ErrCancelNotEligible
	}
	if err := procmsg.RequireExecution(ctx, moID, action); err != nil {
		return false, err
	}
	scope, _ := procmsg.ExecutionFrom(ctx)
	if scope.AgencyOrderID != orderID || authority.UserID != userID {
		return false, domain.ErrOrderNotFound
	}
	err = r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		var state, allocationID string
		if err := q.QueryRowContext(tx, `SELECT state,allocation_id::text FROM merchant_orders WHERE id=$1 AND agency_order_id=$2 FOR UPDATE`, moID, orderID).Scan(&state, &allocationID); err != nil {
			return err
		}
		var previous string
		err := q.QueryRowContext(tx, `SELECT kind FROM agency_order_cancellations WHERE merchant_order_id=$1 AND agency_order_id=$2`, moID, orderID).Scan(&previous)
		if err == nil {
			if previous != kind {
				return domain.ErrCancelNotEligible
			}
			replay = true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if kind == "DELAY_RULE" && now.Before(authority.IssuedAt.Add(time.Duration(policy.DelayRuleWindowDays)*24*time.Hour)) {
			return domain.ErrCancelNotEligible
		}
		effect, found, err := r.lockMerchantEffectState(tx, moID)
		if err != nil {
			return err
		}
		if found && (unresolvedFundingEffect(effect) || effect == "STARTED") {
			if kind == "PRE_EFFECT" {
				return domain.ErrCancelNotEligible
			}
			return domain.ErrFundingNotReady
		}
		preEffect := state == "PLANNED" || state == "READY_TO_PLACE"
		if preEffect && authority.FundingState != "AVAILABLE" {
			return domain.ErrFundingNotReady
		}
		if kind == "PRE_EFFECT" && !preEffect {
			return domain.ErrCancelNotEligible
		}
		if kind == "DELAY_RULE" && !preEffect && (state != "PLACED" || authority.UndeliveredUnits == 0) {
			return domain.ErrCancelNotEligible
		}
		if _, err = q.ExecContext(tx, `INSERT INTO agency_order_cancellations(id,agency_order_id,allocation_id,merchant_order_id,user_id,kind,refund_basis,created_at) VALUES(md5($1||':workflow-cancel')::uuid,$2,$3,$1::uuid,$4,$5,'GROSS',$6)`, moID, orderID, allocationID, userID, kind, now); err != nil {
			return err
		}
		if preEffect {
			if _, err = q.ExecContext(tx, `UPDATE merchant_orders SET state='CANCELLED',version=version+1,updated_at=$2 WHERE id=$1`, moID, now); err != nil {
				return err
			}
			if _, err = q.ExecContext(tx, `UPDATE merchant_order_execution_tasks SET state='CANCELLED',handled_at=$2,version=version+1,updated_at=$2 WHERE merchant_order_id=$1 AND state IN ('QUEUED','CLAIMED')`, moID, now); err != nil {
				return err
			}
			if _, err = q.ExecContext(tx, `UPDATE merchant_payments SET state='FAILED',version=version+1,updated_at=$2 WHERE merchant_order_id=$1 AND state='PLANNED'`, moID, now); err != nil {
				return err
			}
			if err = emitMerchantOrderEvent(tx, q, moID, now); err != nil {
				return err
			}
		} else {
			if _, err = q.ExecContext(tx, `INSERT INTO procurement_recovery_entries(id,merchant_order_id,agency_order_id,cause,expected_amount_minor,received_amount_minor,state,evidence_ref,created_at,updated_at) SELECT md5(mo.id::text||':delay-recovery')::uuid,mo.id,mo.agency_order_id,'OTHER',p.amount_minor,0,'EXPECTED','delay-rule-cancel:'||mo.id::text,$2,$2 FROM merchant_orders mo JOIN merchant_payments p ON p.merchant_order_id=mo.id WHERE mo.id=$1 ON CONFLICT(id) DO NOTHING`, moID, now); err != nil {
				return err
			}
		}
		_, err = q.ExecContext(tx, `INSERT INTO agency_order_execution_audits(id,agency_order_id,merchant_order_id,actor_user_id,action,details,created_at) VALUES(gen_random_uuid(),$1,$2,$3,'CUSTOMER_CANCELLED',jsonb_build_object('kind',$4::text,'refundBasis','GROSS','operationId',$5::text),$6)`, orderID, moID, userID, kind, scope.FlowID, now)
		return err
	})
	return
}

// RecordPlaced는 BeginMerchantEffect가 만든 technical lock 뒤의 결과 창구다
// (모드 무관 PLACEMENT_PENDING→PLACED). 공통 선행은 담당·reveal 감사·Logistics
// 등록 barrier, 완전한 evidence와 승인 상한 이하 실제 지출(초과 지출 차단)이다. Sandbox는
// SANDBOX_TEST_EVIDENCE로 같은 운영 절차를 연습하되 실제 effect를 주장하지 않고,
// Live만 LIVE_MERCHANT_EFFECT_EVIDENCE를 쓴다. activation은 새 Begin에만 적용되며,
// STARTED lock 뒤에는 kill switch가 내려가도 결과와 증거를 반드시 기록한다. 운영자가
// 해시·관찰 시각·기록 운영자는 서버가 최초 성공 시 생성한다. replay는 운영자가 입력한
// 참조·실결제액·출처만 원본과 비교하며 server-owned integrity field는 입력으로 받지 않는다.
func (r *Repository) RecordPlaced(
	ctx context.Context,
	taskID, operatorUserID, idempotencyKey string,
	evidence domain.PlacementEvidence,
	_ bool,
	now time.Time,
) (procurementapp.QueueItem, bool, error) {
	var replayed bool
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		task, order, err := r.lockTask(tx, taskID)
		if err != nil {
			return err
		}
		evidence.ExternalOrderRef = strings.TrimSpace(evidence.ExternalOrderRef)
		evidence.ReceiptSafeRef = strings.TrimSpace(evidence.ReceiptSafeRef)
		evidence.Currency = strings.ToUpper(strings.TrimSpace(evidence.Currency))
		// Defense in depth for non-HTTP callers: integrity fields are never
		// accepted from the caller, even if a stale client still sends them.
		evidence.EvidenceHash = ""
		evidence.ObservedAt = time.Time{}
		evidence.RecordedByUserID = ""
		evidence.RecordedAt = time.Time{}
		var authorizedMinor int64
		if err := r.database.Queryer(tx).QueryRowContext(tx, `
			SELECT COALESCE((checkout_snapshot->'authoritativeTotal'->>'amountMinor')::bigint, 0)
			FROM merchant_orders WHERE id=$1
		`, order.ID).Scan(&authorizedMinor); err != nil {
			return err
		}
		evidence, err = domain.ResolvePlacementAmount(authorizedMinor, evidence)
		if err != nil {
			return err
		}
		replayed, err = r.replayAudit(tx, idempotencyKey, "MERCHANT_ORDER_PLACED", order.ID, operatorUserID)
		if err != nil {
			return err
		}
		if replayed {
			var recorded domain.PlacementEvidence
			if err := r.database.Queryer(tx).QueryRowContext(tx, `
				SELECT placement_evidence_kind, external_order_ref,
				       placement_receipt_safe_ref, placement_actual_amount_minor,
				       placement_evidence_source, placement_evidence_hash,
				       placement_observed_at
				FROM merchant_orders WHERE id=$1
			`, order.ID).Scan(
				&recorded.Kind, &recorded.ExternalOrderRef, &recorded.ReceiptSafeRef,
				&recorded.ActualAmountMinor, &recorded.EvidenceSource,
				&recorded.EvidenceHash, &recorded.ObservedAt,
			); err != nil {
				return err
			}
			recorded.Currency = "USD"
			recorded.ClaimsExternalLive = recorded.Kind == domain.PlacementEvidenceLiveEffect
			if !placementEvidenceReplayMatches(recorded, evidence) {
				return domain.ErrAssignmentConflict
			}
			return nil
		}
		if err := requireActiveAssignment(task, operatorUserID, now); err != nil {
			return err
		}
		if order.State != domain.OrderPlacementPending {
			return domain.ErrTaskStateInvalid
		}
		var effectTaskID, effectOperatorID, effectState string
		if err := r.database.Queryer(tx).QueryRowContext(tx, `
			SELECT task_id::text, operator_user_id::text, state
			FROM procurement_effect_locks WHERE merchant_order_id=$1 FOR UPDATE
		`, order.ID).Scan(&effectTaskID, &effectOperatorID, &effectState); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.ErrManualDecisionRequired
			}
			return err
		}
		if effectTaskID != taskID || effectOperatorID != operatorUserID || effectState != "STARTED" {
			return domain.ErrTaskStateInvalid
		}
		if err := r.requireGrantedReveal(tx, order.ID, operatorUserID); err != nil {
			return err
		}
		// STARTED proves the Process completed the Logistics registration barrier.
		evidence = domain.FinalizePlacementEvidence(
			order.ID, operatorUserID, now, evidence,
		)
		if err := domain.ValidatePlacementEvidence(
			order.ExecutionMode, authorizedMinor, evidence,
		); err != nil {
			return err
		}
		paymentAmount := evidence.ActualAmountMinor
		chargeEvidence := "test-evidence:" + evidence.ExternalOrderRef
		outcome := "SANDBOX_TEST_EVIDENCE_RECORDED"
		if evidence.Kind == domain.PlacementEvidenceLiveEffect {
			chargeEvidence = "live-evidence:" + evidence.ExternalOrderRef
			outcome = "LIVE_MERCHANT_EFFECT_EVIDENCE_RECORDED"
		}
		auditDetails := map[string]any{
			"outcome": outcome, "evidenceKind": evidence.Kind,
			"externalOrderRef":         evidence.ExternalOrderRef,
			"receiptSafeRef":           evidence.ReceiptSafeRef,
			"amountMinor":              evidence.ActualAmountMinor,
			"currency":                 evidence.Currency,
			"evidenceSource":           evidence.EvidenceSource,
			"evidenceHash":             evidence.EvidenceHash,
			"observedAt":               evidence.ObservedAt,
			"claimsExternalLiveEffect": evidence.ClaimsExternalLive,
		}
		if _, err := r.database.Queryer(tx).ExecContext(tx, `
			UPDATE merchant_orders
			SET state='PLACED', external_order_ref=$1,
			    placement_evidence_kind=$2, placement_receipt_safe_ref=$3,
			    placement_actual_amount_minor=$4, placement_evidence_source=$5,
			    placement_evidence_hash=$6, placement_observed_at=$7,
			    placement_recorded_by_user_id=$8, placement_recorded_at=$9,
			    result_hash=$6, version=version+1, updated_at=$9
			WHERE id=$10
		`, evidence.ExternalOrderRef, evidence.Kind, evidence.ReceiptSafeRef,
			evidence.ActualAmountMinor, evidence.EvidenceSource,
			evidence.EvidenceHash, evidence.ObservedAt, operatorUserID, now,
			order.ID); err != nil {
			return err
		}
		// synthetic이든 실효든 같은 지출 원장을 쓴다 — 대사 워크플로 동등
		// (실효 여부는 merchant_orders.execution_mode join으로 판별한다).
		if paymentAmount > 0 {
			var merchantPaymentID string
			if err := r.database.Queryer(tx).QueryRowContext(tx, `
				INSERT INTO merchant_payments(
					id, merchant_order_id, agency_order_id, amount_minor, currency,
					state, version, created_at, updated_at
				) VALUES(md5($1||':payment')::uuid, $1::uuid, $2::uuid, $3, 'USD',
				        'SUCCEEDED', 1, $4, $4)
				ON CONFLICT (merchant_order_id) DO UPDATE
				SET amount_minor=EXCLUDED.amount_minor,
				    state='SUCCEEDED', version=merchant_payments.version+1,
				    updated_at=EXCLUDED.updated_at
				WHERE merchant_payments.state IN ('PLANNED','EXECUTION_PENDING')
				RETURNING id::text
			`, order.ID, order.AgencyOrderID, paymentAmount, now).Scan(
				&merchantPaymentID,
			); err != nil {
				return err
			}
			if _, err := r.database.Queryer(tx).ExecContext(tx, `
				INSERT INTO merchant_charges(
					id, merchant_payment_id, merchant_order_id, agency_order_id,
					shop_domain, amount_minor, currency, kind, evidence_ref,
					observed_at, created_at
				) VALUES(md5($1||':charge')::uuid, $2::uuid, $1::uuid,
				        $3::uuid, $4, $5, 'USD', 'EXPECTED', $6, $7, $7)
				ON CONFLICT (shop_domain, evidence_ref) DO NOTHING
			`, order.ID, merchantPaymentID, order.AgencyOrderID, order.ShopDomain,
				paymentAmount, chargeEvidence, evidence.ObservedAt); err != nil {
				return err
			}
		}
		if _, err := r.database.Queryer(tx).ExecContext(tx, `
			UPDATE merchant_order_execution_tasks
			SET state='SUCCEEDED', handled_at=$1, version=version+1, updated_at=$1
			WHERE id=$2
		`, now, taskID); err != nil {
			return err
		}
		if _, err := r.database.Queryer(tx).ExecContext(tx, `
			UPDATE procurement_effect_locks
			SET state='PLACED', resolved_at=$1, version=version+1
			WHERE merchant_order_id=$2 AND state='STARTED'
		`, now, order.ID); err != nil {
			return err
		}
		if err := emitEffectLockEvent(tx, r.database.Queryer(tx), order.ID, now); err != nil {
			return err
		}
		if err := emitMerchantOrderEvent(tx, r.database.Queryer(tx), order.ID, now); err != nil {
			return err
		}
		_, err = r.database.Queryer(tx).ExecContext(tx, `
			INSERT INTO agency_order_execution_audits(
				id, agency_order_id, merchant_order_id, actor_user_id, action,
				idempotency_key, details, created_at
			) VALUES(gen_random_uuid(),$1,$2,$3,'MERCHANT_ORDER_PLACED',$4,$5,$6)
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

func placementEvidenceReplayMatches(
	recorded domain.PlacementEvidence,
	submitted domain.PlacementEvidence,
) bool {
	return recorded.Kind == submitted.Kind &&
		recorded.ExternalOrderRef == submitted.ExternalOrderRef &&
		recorded.ReceiptSafeRef == submitted.ReceiptSafeRef &&
		recorded.ActualAmountMinor == submitted.ActualAmountMinor &&
		recorded.Currency == submitted.Currency &&
		recorded.EvidenceSource == submitted.EvidenceSource &&
		recorded.ClaimsExternalLive == submitted.ClaimsExternalLive
}

// GetQueueItem은 taskID 기준 단건 사영이다(결과 기록 뒤 최신 상태 반환용).
func (r *Repository) GetQueueItem(ctx context.Context, taskID string) (procurementapp.QueueItem, error) {
	return r.getQueueItem(ctx, taskID)
}
