package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"slices"
	"time"

	procurementapp "github.com/vitlane/vitlane/server/internal/ordering/procurement/app"
	"github.com/vitlane/vitlane/server/internal/ordering/procurement/domain"
)

func scanDecision(scanner interface{ Scan(...any) error }) (domain.DecisionRecord, error) {
	var record domain.DecisionRecord
	err := scanner.Scan(
		&record.ID, &record.MerchantOrderID, &record.AgencyOrderID, &record.TaskID,
		&record.Decision, &record.PublicRationale, &record.InternalNote,
		&record.ObservedCondition, &record.EvidenceSource, &record.EvidenceHash,
		&record.ObservedAt, &record.DecidedByUserID, &record.TaskVersion,
		&record.AuthorizationHash, &record.ExecutionProfileHash, &record.CreatedAt,
	)
	return record, err
}

const decisionColumns = `
	id::text, merchant_order_id::text, agency_order_id::text, task_id::text,
	decision, public_rationale, COALESCE(internal_note,''), observed_condition,
	evidence_source, evidence_hash, observed_at, decided_by_user_id::text,
	task_version, authorization_hash, execution_profile_hash, created_at`

// lockMerchantEffectState serializes every transition that can invalidate a
// prepared funding activation with the exact MerchantOrder effect lock. A
// missing row means no provider-side funding call has been authorized yet.
func (r *Repository) lockMerchantEffectState(
	tx context.Context, merchantOrderID string,
) (string, bool, error) {
	var state string
	err := r.database.Queryer(tx).QueryRowContext(tx, `
		SELECT state FROM procurement_effect_locks
		WHERE merchant_order_id=$1 FOR UPDATE
	`, merchantOrderID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return state, err == nil, err
}

func unresolvedFundingEffect(state string) bool {
	return state == "FUNDING_PENDING" || state == "FUNDING_UNKNOWN"
}

func (r *Repository) RecordManualDecision(
	ctx context.Context,
	taskID, operatorUserID, idempotencyKey string,
	decision domain.ManualDecision,
	publicRationale, internalNote, observedCondition string,
	evidenceSource domain.EvidenceSource,
	evidenceHash string,
	observedAt, now time.Time,
) (domain.DecisionRecord, bool, error) {
	var result domain.DecisionRecord
	var replay bool
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		existing, err := scanDecision(r.database.Queryer(tx).QueryRowContext(tx, `
			SELECT `+decisionColumns+` FROM procurement_decision_records
			WHERE idempotency_key=$1
		`, idempotencyKey))
		if err == nil {
			if !sameManualDecisionAttempt(
				existing, taskID, operatorUserID, decision, publicRationale,
				internalNote, observedCondition, evidenceSource, evidenceHash, observedAt,
			) {
				return domain.ErrAssignmentConflict
			}
			result, replay = existing, true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		task, order, err := r.lockTask(tx, taskID)
		if err != nil {
			return err
		}
		if err := requireActiveAssignment(task, operatorUserID, now); err != nil {
			return err
		}
		if effectState, found, err := r.lockMerchantEffectState(tx, order.ID); err != nil {
			return err
		} else if found && unresolvedFundingEffect(effectState) {
			// The decision recorded in the effect lock is the immutable authority
			// for the in-flight provider call. A second decision cannot invalidate
			// it while Capture is pending or outcome-unknown.
			return domain.ErrFundingNotReady
		}
		// After a merchant effect lock starts, the operator may still discover
		// that no order can be completed.  That path accepts only an explicit
		// UNABLE_TO_PURCHASE record so RecordFailure has a mandatory public
		// rationale; no new positive authorization decision is possible here.
		if order.State != domain.OrderPlanned &&
			(order.State != domain.OrderPlacementPending ||
				decision != domain.DecisionUnableToPurchase) {
			return domain.ErrTaskStateInvalid
		}
		// A customer's terminal refusal is a purchase-stop fact, not another
		// merchant observation that an operator may supersede.  Keep the
		// independent BeginMerchantEffect check below as the final fail-closed
		// barrier, but reject a misleading positive decision at ingress too.
		if decisionAllowsMerchantEffect(decision) {
			var stopped int
			if err := r.database.Queryer(tx).QueryRowContext(tx, `
				SELECT count(*) FROM procurement_customer_requests
				WHERE merchant_order_id=$1
				  AND state IN ('DECLINED','FAILED_NO_RESPONSE','CANCELLED')
			`, order.ID).Scan(&stopped); err != nil {
				return err
			}
			if stopped > 0 {
				return domain.ErrManualDecisionRequired
			}
		}
		var authorizationKind, authorizationHash, authorizationProfileHash,
			orderProfileHash string
		if err := r.database.Queryer(tx).QueryRowContext(tx, `
			SELECT authz.authorization_kind,
			       authz.authorization_hash,
			       authz.execution_profile_hash,
			       orders.execution_profile_hash
			FROM agency_order_procurement_authorizations authz
			JOIN agency_orders orders ON orders.id=authz.agency_order_id
			WHERE authz.agency_order_id=$1
		`, task.AgencyOrderID).Scan(
			&authorizationKind, &authorizationHash, &authorizationProfileHash,
			&orderProfileHash,
		); errors.Is(err, sql.ErrNoRows) {
			return domain.ErrAuthorizationMissing
		} else if err != nil {
			return err
		}
		if !authorizationAllowsManualProcurement(
			authorizationKind, authorizationProfileHash, orderProfileHash,
		) {
			return domain.ErrAuthorizationMissing
		}
		result, err = scanDecision(r.database.Queryer(tx).QueryRowContext(tx, `
			INSERT INTO procurement_decision_records(
				id, merchant_order_id, agency_order_id, task_id, decision,
				public_rationale, internal_note, observed_condition, evidence_source,
				evidence_hash, observed_at, decided_by_user_id, task_version,
				authorization_hash, execution_profile_hash, idempotency_key, created_at
			) VALUES(
				gen_random_uuid(),$1,$2,$3,$4,$5,NULLIF($6,''),$7,$8,$9,$10,$11,
				$12,$13,$14,$15,$16
			) RETURNING `+decisionColumns,
			order.ID, task.AgencyOrderID, task.ID, decision, publicRationale,
			internalNote, observedCondition, evidenceSource, evidenceHash,
			observedAt, operatorUserID, task.Version, authorizationHash,
			orderProfileHash, idempotencyKey, now,
		))
		if err != nil {
			return err
		}
		if err := emitDecisionRecordedEvent(
			tx, r.database.Queryer(tx), result.ID, now,
		); err != nil {
			return err
		}
		_, err = r.database.Queryer(tx).ExecContext(tx, `
			INSERT INTO agency_order_execution_audits(
				id, agency_order_id, merchant_order_id, actor_user_id, action,
				idempotency_key, details, created_at
			) VALUES(gen_random_uuid(),$1,$2,$3,'PROCUREMENT_DECISION_RECORDED',$4,$5,$6)
		`, task.AgencyOrderID, order.ID, operatorUserID, idempotencyKey,
			mustJSON(map[string]any{
				"decisionRecordId": result.ID, "decision": decision,
				"authorizationKind":    authorizationKind,
				"authorizationHash":    authorizationHash,
				"executionProfileHash": orderProfileHash,
			}), now)
		return err
	})
	return result, replay, err
}

func (r *Repository) ListManualDecisionsForTask(
	ctx context.Context, taskID, operatorUserID string,
) ([]domain.DecisionRecord, error) {
	if taskID == "" || operatorUserID == "" {
		return nil, domain.ErrTaskNotFound
	}
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT `+decisionColumns+` FROM procurement_decision_records decision
		WHERE decision.task_id=$1
		ORDER BY decision.created_at DESC, decision.id DESC LIMIT 100
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.DecisionRecord, 0)
	for rows.Next() {
		record, scanErr := scanDecision(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, record)
	}
	return result, rows.Err()
}

func scanCustomerRequest(scanner interface{ Scan(...any) error }) (domain.CustomerRequest, error) {
	var request domain.CustomerRequest
	var options []byte
	var response []byte
	var resolvedAt sql.NullTime
	err := scanner.Scan(
		&request.ID, &request.MerchantOrderID, &request.AgencyOrderID,
		&request.UserID, &request.Kind, &request.Prompt, &request.ResponseType,
		&options, &request.PublicContext, &request.State, &response,
		&request.RequestedByUserID, &request.RequestedAt, &request.DueAt,
		&resolvedAt, &request.ResolvedByUserID, &request.ResolutionReason,
		&request.SourceDecisionID, &request.Version,
	)
	if err != nil {
		return domain.CustomerRequest{}, err
	}
	if len(options) > 0 {
		_ = json.Unmarshal(options, &request.ResponseOptions)
	}
	if len(response) > 0 {
		request.Response = append(json.RawMessage(nil), response...)
	}
	if resolvedAt.Valid {
		request.ResolvedAt = &resolvedAt.Time
	}
	return request, nil
}

const customerRequestColumns = `
	id::text, merchant_order_id::text, agency_order_id::text, user_id::text,
	kind, prompt, response_type, response_options, public_context, state,
	response, requested_by_user_id::text, requested_at, due_at, resolved_at,
	COALESCE(resolved_by_user_id::text,''), COALESCE(resolution_reason,''),
	source_decision_id::text, version`

func (r *Repository) CreateCustomerRequest(
	ctx context.Context,
	taskID, operatorUserID, idempotencyKey string,
	observedCondition, internalNote string,
	evidenceSource domain.EvidenceSource,
	evidenceHash string,
	observedAt time.Time,
	kind domain.CustomerRequestKind,
	prompt string,
	responseType domain.CustomerResponseType,
	responseOptions []string,
	publicContext string,
	dueAt, now time.Time,
) (domain.CustomerRequest, bool, error) {
	var result domain.CustomerRequest
	var replay bool
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		existing, err := scanCustomerRequest(r.database.Queryer(tx).QueryRowContext(tx, `
			SELECT `+customerRequestColumns+` FROM procurement_customer_requests
			WHERE idempotency_key=$1
		`, idempotencyKey))
		if err == nil {
			if !sameCustomerRequestAttempt(
				existing, operatorUserID, kind, prompt, responseType,
				responseOptions, publicContext,
			) {
				return domain.ErrAssignmentConflict
			}
			existingDecision, decisionErr := scanDecision(
				r.database.Queryer(tx).QueryRowContext(tx, `
					SELECT `+decisionColumns+` FROM procurement_decision_records
					WHERE id=$1
				`, existing.SourceDecisionID),
			)
			if decisionErr != nil || !sameManualDecisionAttempt(
				existingDecision, taskID, operatorUserID,
				domain.DecisionMaterialCondition, publicContext, internalNote,
				observedCondition, evidenceSource, evidenceHash, observedAt,
			) {
				return domain.ErrAssignmentConflict
			}
			result, replay = existing, true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		task, order, err := r.lockTask(tx, taskID)
		if err != nil {
			return err
		}
		if err := requireActiveAssignment(task, operatorUserID, now); err != nil {
			return err
		}
		if _, found, err := r.lockMerchantEffectState(tx, order.ID); err != nil {
			return err
		} else if found {
			// Customer questions belong strictly before funding activation. Once
			// Prepare has committed, the exact decision/request snapshot is frozen.
			return domain.ErrFundingNotReady
		}
		if order.State != domain.OrderPlanned {
			return domain.ErrTaskStateInvalid
		}
		var ownerUserID, authorizationKind, authorizationHash,
			authorizationProfileHash, orderProfileHash string
		if err := r.database.Queryer(tx).QueryRowContext(tx, `
			SELECT orders.user_id::text, authz.authorization_kind,
			       authz.authorization_hash, authz.execution_profile_hash,
			       orders.execution_profile_hash
			FROM agency_orders orders
			JOIN agency_order_procurement_authorizations authz
			  ON authz.agency_order_id=orders.id
			WHERE orders.id=$1
		`, task.AgencyOrderID).Scan(
			&ownerUserID, &authorizationKind, &authorizationHash,
			&authorizationProfileHash, &orderProfileHash,
		); errors.Is(err, sql.ErrNoRows) {
			return domain.ErrAuthorizationMissing
		} else if err != nil {
			return err
		}
		if !authorizationAllowsManualProcurement(
			authorizationKind, authorizationProfileHash, orderProfileHash,
		) {
			return domain.ErrAuthorizationMissing
		}
		decisionRecord, err := scanDecision(r.database.Queryer(tx).QueryRowContext(tx, `
			INSERT INTO procurement_decision_records(
				id, merchant_order_id, agency_order_id, task_id, decision,
				public_rationale, internal_note, observed_condition, evidence_source,
				evidence_hash, observed_at, decided_by_user_id, task_version,
				authorization_hash, execution_profile_hash, idempotency_key,
				created_at
			) VALUES(
				gen_random_uuid(),$1,$2,$3,'MATERIAL_NEW_CONDITION',$4,NULLIF($5,''),
				$6,$7,$8,$9,$10,$11,$12,$13,$14,$15
			) RETURNING `+decisionColumns,
			order.ID, task.AgencyOrderID, task.ID, publicContext, internalNote,
			observedCondition, evidenceSource, evidenceHash, observedAt,
			operatorUserID, task.Version, authorizationHash, orderProfileHash,
			idempotencyKey+":decision", now,
		))
		if err != nil {
			return err
		}
		if err := emitDecisionRecordedEvent(
			tx, r.database.Queryer(tx), decisionRecord.ID, now,
		); err != nil {
			return err
		}
		if _, err = r.database.Queryer(tx).ExecContext(tx, `
			INSERT INTO agency_order_execution_audits(
				id, agency_order_id, merchant_order_id, actor_user_id, action,
				idempotency_key, details, created_at
			) VALUES(gen_random_uuid(),$1,$2,$3,'PROCUREMENT_DECISION_RECORDED',$4,$5,$6)
		`, task.AgencyOrderID, order.ID, operatorUserID,
			idempotencyKey+":decision-audit", mustJSON(map[string]any{
				"decisionRecordId":     decisionRecord.ID,
				"decision":             domain.DecisionMaterialCondition,
				"authorizationKind":    authorizationKind,
				"authorizationHash":    authorizationHash,
				"executionProfileHash": orderProfileHash,
			}), now); err != nil {
			return err
		}
		if responseOptions == nil {
			responseOptions = []string{}
		}
		optionsJSON, _ := json.Marshal(responseOptions)
		result, err = scanCustomerRequest(r.database.Queryer(tx).QueryRowContext(tx, `
			INSERT INTO procurement_customer_requests(
				id, merchant_order_id, agency_order_id, user_id, kind, prompt,
				response_type, response_options, public_context, state,
				requested_by_user_id, requested_at, due_at, source_decision_id,
				idempotency_key, version, created_at, updated_at
			) VALUES(
				gen_random_uuid(),$1,$2,$3,$4,$5,$6,$7,$8,'PENDING',$9,$10,$11,
				$12,$13,1,$10,$10
			) RETURNING `+customerRequestColumns,
			order.ID, task.AgencyOrderID, ownerUserID, kind, prompt, responseType,
			optionsJSON, publicContext, operatorUserID, now, dueAt, decisionRecord.ID,
			idempotencyKey,
		))
		if err != nil {
			if isUniqueViolation(err) {
				return domain.ErrOpenCustomerRequest
			}
			return err
		}
		if err := emitCustomerRequestEvent(
			tx, r.database.Queryer(tx), result.ID, now,
		); err != nil {
			return err
		}
		_, err = r.database.Queryer(tx).ExecContext(tx, `
			INSERT INTO agency_order_execution_audits(
				id, agency_order_id, merchant_order_id, actor_user_id, action,
				idempotency_key, details, created_at
			) VALUES(gen_random_uuid(),$1,$2,$3,'PROCUREMENT_REQUEST_CREATED',$4,$5,$6)
		`, task.AgencyOrderID, order.ID, operatorUserID,
			idempotencyKey+":request-audit",
			mustJSON(map[string]any{
				"requestId": result.ID, "kind": kind, "dueAt": dueAt,
			}), now)
		return err
	})
	return result, replay, err
}

// Idempotency keys bind every operator-supplied audit fact, not only the
// customer-facing subset. Reusing a key with a changed private note, evidence
// source, observation time, option set, or public context must fail closed.
func sameManualDecisionAttempt(
	existing domain.DecisionRecord,
	taskID, operatorUserID string,
	decision domain.ManualDecision,
	publicRationale, internalNote, observedCondition string,
	evidenceSource domain.EvidenceSource,
	evidenceHash string,
	observedAt time.Time,
) bool {
	return existing.TaskID == taskID &&
		existing.DecidedByUserID == operatorUserID &&
		existing.Decision == decision &&
		existing.PublicRationale == publicRationale &&
		existing.InternalNote == internalNote &&
		existing.ObservedCondition == observedCondition &&
		existing.EvidenceSource == evidenceSource &&
		existing.EvidenceHash == evidenceHash &&
		existing.ObservedAt.Equal(observedAt.Round(time.Microsecond))
}

func sameCustomerRequestAttempt(
	existing domain.CustomerRequest,
	operatorUserID string,
	kind domain.CustomerRequestKind,
	prompt string,
	responseType domain.CustomerResponseType,
	responseOptions []string,
	publicContext string,
) bool {
	return existing.RequestedByUserID == operatorUserID &&
		existing.Prompt == prompt &&
		existing.Kind == kind &&
		existing.ResponseType == responseType &&
		slices.Equal(existing.ResponseOptions, responseOptions) &&
		existing.PublicContext == publicContext
}

func (r *Repository) ListCustomerRequestsForTask(
	ctx context.Context, taskID, operatorUserID string,
) ([]domain.CustomerRequest, error) {
	if taskID == "" || operatorUserID == "" {
		return nil, domain.ErrTaskNotFound
	}
	return r.listCustomerRequests(ctx, `request.merchant_order_id=(
		SELECT merchant_order_id FROM merchant_order_execution_tasks WHERE id=$1
	)`, taskID)
}

func (r *Repository) ListCustomerRequestsForOrder(
	ctx context.Context, agencyOrderID, userID string,
) ([]domain.CustomerRequest, error) {
	if agencyOrderID == "" || userID == "" {
		return nil, domain.ErrOrderNotFound
	}
	return r.listCustomerRequests(ctx,
		`request.agency_order_id=$1::uuid AND request.user_id=$2::uuid`,
		agencyOrderID, userID)
}

func (r *Repository) listCustomerRequests(
	ctx context.Context, predicate string, arguments ...any,
) ([]domain.CustomerRequest, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT `+customerRequestColumns+` FROM procurement_customer_requests request
		WHERE `+predicate+`
		ORDER BY request.requested_at DESC, request.id DESC LIMIT 100
	`, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.CustomerRequest, 0)
	for rows.Next() {
		request, scanErr := scanCustomerRequest(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, request)
	}
	return result, rows.Err()
}

func resolutionRequestHash(
	state domain.CustomerRequestState, response json.RawMessage, reason string,
) string {
	payload, _ := json.Marshal(struct {
		State    domain.CustomerRequestState `json:"state"`
		Response json.RawMessage             `json:"response,omitempty"`
		Reason   string                      `json:"reason,omitempty"`
	}{state, response, reason})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func normalizeBooleanConsentResolution(
	responseType domain.CustomerResponseType,
	state domain.CustomerRequestState,
	response json.RawMessage,
	resolutionReason string,
) (domain.CustomerRequestState, json.RawMessage, string) {
	if state != domain.RequestAnswered || responseType != domain.ResponseBooleanConsent {
		return state, response, resolutionReason
	}
	var value struct {
		Accepted *bool `json:"accepted"`
	}
	// ResolveCustomerRequest validates this payload immediately before calling
	// the helper.  Keep the parse guard fail-neutral for defensive reuse.
	if json.Unmarshal(response, &value) != nil || value.Accepted == nil || *value.Accepted {
		return state, response, resolutionReason
	}
	return domain.RequestDeclined, response, "CUSTOMER_DECLINED"
}

func (r *Repository) ResolveCustomerRequest(
	ctx context.Context,
	requestID, actorUserID, idempotencyKey string,
	expectedVersion int64,
	state domain.CustomerRequestState,
	response json.RawMessage,
	resolutionReason string,
	now time.Time,
) (domain.CustomerRequest, bool, error) {
	var result domain.CustomerRequest
	var replay bool
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		// Customer terminal outcomes and operator decisions share the same
		// task/MerchantOrder lock.  This gives them one serialization order: a
		// positive decision that starts after a decline/no-response/cancel must
		// observe that stop, while a stop that follows an earlier decision still
		// blocks BeginMerchantEffect independently.
		var taskID string
		if err := r.database.Queryer(tx).QueryRowContext(tx, `
			SELECT task.id::text
			FROM procurement_customer_requests request
			JOIN merchant_order_execution_tasks task
			  ON task.merchant_order_id=request.merchant_order_id
			WHERE request.id=$1
		`, requestID).Scan(&taskID); errors.Is(err, sql.ErrNoRows) {
			return domain.ErrRequestNotFound
		} else if err != nil {
			return err
		}
		resolutionTask, resolutionOrder, err := r.lockTask(tx, taskID)
		if err != nil {
			return err
		}
		request, err := scanCustomerRequest(r.database.Queryer(tx).QueryRowContext(tx, `
			SELECT `+customerRequestColumns+` FROM procurement_customer_requests
			WHERE id=$1 FOR UPDATE
		`, requestID))
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrRequestNotFound
		}
		if err != nil {
			return err
		}
		if state == domain.RequestAnswered {
			if actorUserID != request.UserID ||
				domain.ValidateCustomerResponse(request.ResponseType,
					request.ResponseOptions, response) != nil {
				return domain.ErrRequestResponseInvalid
			}
			state, response, resolutionReason = normalizeBooleanConsentResolution(
				request.ResponseType, state, response, resolutionReason,
			)
		}
		hash := resolutionRequestHash(state, response, resolutionReason)
		var storedKey, storedHash string
		err = r.database.Queryer(tx).QueryRowContext(tx, `
			SELECT COALESCE(resolution_idempotency_key,''),
			       COALESCE(resolution_request_hash,'')
			FROM procurement_customer_requests WHERE id=$1
		`, requestID).Scan(&storedKey, &storedHash)
		if err != nil {
			return err
		}
		if storedKey != "" {
			if storedKey == idempotencyKey && storedHash == hash &&
				request.State == state && request.ResolvedByUserID == actorUserID {
				result, replay = request, true
				return nil
			}
			return domain.ErrRequestStateConflict
		}
		if _, found, err := r.lockMerchantEffectState(tx, resolutionOrder.ID); err != nil {
			return err
		} else if found {
			// Prepare cannot coexist with a pending customer request. This check
			// also closes the transaction race: a late answer may not revoke the
			// authority snapshot after a provider funding call was released.
			return domain.ErrFundingNotReady
		}
		if request.State != domain.RequestPending || request.Version != expectedVersion {
			return domain.ErrRequestStateConflict
		}
		switch state {
		case domain.RequestAnswered:
			// Typed response and ownership were validated before replay matching.
		case domain.RequestDeclined:
			if actorUserID != request.UserID {
				return domain.ErrRequestResponseInvalid
			}
		case domain.RequestFailedNoResponse, domain.RequestCancelled:
			if resolutionTask.AssignedOperatorUserID != actorUserID ||
				resolutionTask.LeaseUntil == nil || !now.Before(*resolutionTask.LeaseUntil) {
				return domain.ErrAssignmentRequired
			}
			if state == domain.RequestFailedNoResponse && now.Before(request.DueAt) {
				return domain.ErrNoResponseTooEarly
			}
		default:
			return domain.ErrRequestInvalid
		}
		result, err = scanCustomerRequest(r.database.Queryer(tx).QueryRowContext(tx, `
			UPDATE procurement_customer_requests SET
				state=$1, response=NULLIF($2::text,'')::jsonb, resolved_at=$3,
				resolved_by_user_id=$4, resolution_reason=NULLIF($5,''),
				resolution_idempotency_key=$6, resolution_request_hash=$7,
				version=version+1, updated_at=$3
			WHERE id=$8 AND state='PENDING' AND version=$9
			RETURNING `+customerRequestColumns,
			state, string(response), now, actorUserID, resolutionReason,
			idempotencyKey, hash, requestID, expectedVersion,
		))
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrRequestStateConflict
		}
		if err != nil {
			return err
		}
		if err := emitCustomerRequestEvent(
			tx, r.database.Queryer(tx), result.ID, now,
		); err != nil {
			return err
		}
		_, err = r.database.Queryer(tx).ExecContext(tx, `
			INSERT INTO agency_order_execution_audits(
				id, agency_order_id, merchant_order_id, actor_user_id, action,
				idempotency_key, details, created_at
			) VALUES(gen_random_uuid(),$1,$2,$3,'PROCUREMENT_REQUEST_RESOLVED',$4,$5,$6)
		`, request.AgencyOrderID, request.MerchantOrderID, actorUserID,
			idempotencyKey, mustJSON(map[string]any{
				"requestId": requestID, "state": state,
			}), now)
		return err
	})
	return result, replay, err
}

func (r *Repository) PrepareMerchantEffectFunding(
	ctx context.Context,
	taskID, operatorUserID, idempotencyKey string,
	liveEnabled bool,
	authority procurementapp.PurchasePreparation,
	now time.Time,
) (procurementapp.QueueItem, bool, error) {
	var replay bool
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		// The MerchantOrder/task rows are the lock root (ADR-0070 §4.7 — sibling
		// MOs of one order no longer serialize on the AgencyOrder row).
		orderProfileHash, orderExecutionMode := authority.ExecutionProfileHash, authority.ExecutionMode
		task, order, err := r.lockTask(tx, taskID)
		if err != nil {
			return err
		}
		if err := procmsg.RequireExecution(tx, order.ID, procmsg.EffectReservePurchase); err != nil {
			return err
		}
		scope, _ := procmsg.ExecutionFrom(tx)
		var existingTask, existingOperator, existingState string
		err = r.database.Queryer(tx).QueryRowContext(tx, `
			SELECT task_id::text, operator_user_id::text, state
			FROM procurement_effect_locks WHERE idempotency_key=$1
			FOR UPDATE
		`, idempotencyKey).Scan(&existingTask, &existingOperator, &existingState)
		if err == nil {
			if existingTask != taskID {
				return domain.ErrEffectAlreadyStarted
			}
			if existingState != "FUNDING_PENDING" && existingState != "FUNDING_UNKNOWN" &&
				existingState != "STARTED" && existingState != "FAILED" {
				return domain.ErrEffectAlreadyStarted
			}
			if existingState == "FUNDING_PENDING" || existingState == "FUNDING_UNKNOWN" {
				if err := requireActiveAssignment(task, operatorUserID, now); err != nil {
					return err
				}
				if existingOperator != operatorUserID {
					if err := r.reassignPendingEffectLock(
						tx, task, order, existingOperator, operatorUserID,
						idempotencyKey, now,
					); err != nil {
						return err
					}
				}
			} else if existingOperator != operatorUserID {
				// STARTED may already correspond to a human merchant purchase and
				// FAILED is terminal. Neither is transferred by a funding retry.
				return domain.ErrEffectAlreadyStarted
			}
			replay = true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		// The browser intentionally creates a new command idempotency key for each
		// operator click. A capture timeout must not strand the original technical
		// lock behind that lost client key: adopt the one lock owned by this exact
		// task/operator and let Payment reconcile its deterministic MO operation.
		// A fresh key never transfers an effect lock to another assignee.
		err = r.database.Queryer(tx).QueryRowContext(tx, `
			SELECT task_id::text, operator_user_id::text, state
			FROM procurement_effect_locks
			WHERE merchant_order_id=$1
			FOR UPDATE
		`, order.ID).Scan(&existingTask, &existingOperator, &existingState)
		if err == nil {
			if existingTask != taskID {
				return domain.ErrEffectAlreadyStarted
			}
			if existingState != "FUNDING_PENDING" && existingState != "FUNDING_UNKNOWN" &&
				existingState != "STARTED" && existingState != "FAILED" {
				return domain.ErrEffectAlreadyStarted
			}
			if err := requireActiveAssignment(task, operatorUserID, now); err != nil {
				return err
			}
			if existingOperator != operatorUserID {
				if existingState != "FUNDING_PENDING" && existingState != "FUNDING_UNKNOWN" {
					return domain.ErrEffectAlreadyStarted
				}
				if err := r.reassignPendingEffectLock(
					tx, task, order, existingOperator, operatorUserID,
					idempotencyKey, now,
				); err != nil {
					return err
				}
			}
			replay = true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := requireActiveAssignment(task, operatorUserID, now); err != nil {
			return err
		}
		if order.State != domain.OrderPlanned {
			return domain.ErrTaskStateInvalid
		}
		if string(order.ExecutionMode) != orderExecutionMode {
			return domain.ErrTaskStateInvalid
		}
		if order.ExecutionMode == domain.ModeLiveMerchantEffect && !liveEnabled {
			return domain.ErrLiveModeClosed
		}
		if err := r.requireGrantedReveal(tx, order.ID, operatorUserID); err != nil {
			return err
		}
		authorizationKind, authorizationHash, authorizationProfileHash := authority.AuthorizationKind, authority.AuthorizationHash, authority.ExecutionProfileHash
		if !authorizationAllowsManualProcurement(
			authorizationKind, authorizationProfileHash, orderProfileHash,
		) {
			return domain.ErrAuthorizationMissing
		}
		var decisionID, decisionValue, decisionAuthorizationHash,
			decisionProfileHash string
		var decisionCreatedAt time.Time
		if err := r.database.Queryer(tx).QueryRowContext(tx, `
			SELECT id::text, decision, authorization_hash, execution_profile_hash,
			       created_at
			FROM procurement_decision_records WHERE merchant_order_id=$1
			ORDER BY created_at DESC, id DESC LIMIT 1
		`, order.ID).Scan(&decisionID, &decisionValue, &decisionAuthorizationHash,
			&decisionProfileHash, &decisionCreatedAt); errors.Is(err, sql.ErrNoRows) {
			return domain.ErrManualDecisionRequired
		} else if err != nil {
			return err
		}
		if (decisionValue != string(domain.DecisionWithinAuthorization) &&
			decisionValue != string(domain.DecisionImmaterialVariance)) ||
			decisionAuthorizationHash != authorizationHash ||
			decisionProfileHash != orderProfileHash {
			return domain.ErrManualDecisionRequired
		}
		var openRequests, resolvedAfterDecision, terminalStops int
		if err := r.database.Queryer(tx).QueryRowContext(tx, `
			SELECT
				count(*) FILTER (WHERE request.state='PENDING'),
				count(*) FILTER (WHERE request.resolved_at IS NOT NULL
				                       AND request.resolved_at >= $2),
				count(*) FILTER (WHERE request.state IN (
					'DECLINED','FAILED_NO_RESPONSE','CANCELLED'
				))
			FROM procurement_customer_requests request
			WHERE request.merchant_order_id=$1
		`, order.ID, decisionCreatedAt).Scan(
			&openRequests, &resolvedAfterDecision, &terminalStops,
		); err != nil {
			return err
		}
		if terminalStops > 0 {
			return domain.ErrManualDecisionRequired
		}
		if openRequests > 0 {
			return domain.ErrOpenCustomerRequest
		}
		if resolvedAfterDecision > 0 {
			return domain.ErrManualDecisionRequired
		}
		var cancelled bool
		if err := r.database.Queryer(tx).QueryRowContext(tx, `SELECT EXISTS(SELECT 1 FROM agency_order_cancellations WHERE merchant_order_id=$1)`, order.ID).Scan(&cancelled); err != nil {
			return err
		}
		if authority.HasRefundObligation || cancelled {
			return domain.ErrOrderInException
		}
		fundingPositionID := authority.FundingPositionID
		if fundingPositionID == "" {
			return domain.ErrFundingNotReady
		}
		switch authority.FundingState {
		case "AVAILABLE", "ACTIVATION_PENDING", "ACTIVATION_UNKNOWN", "ACTIVE", "FAILED":
		default:
			return domain.ErrFundingNotReady
		}
		if _, err := r.database.Queryer(tx).ExecContext(tx, `
			INSERT INTO procurement_effect_locks(
				merchant_order_id, task_id, decision_record_id, authorization_hash,
				execution_profile_hash, operator_user_id, state, idempotency_key,
				started_at, funding_position_id, funding_state,
				funding_requested_at, process_flow_id
			) VALUES($1,$2,$3,$4,$5,$6,'FUNDING_PENDING',$7,$8,$9,
				'FUNDING_PENDING',$8,$10)
		`, order.ID, task.ID, decisionID, authorizationHash, orderProfileHash,
			operatorUserID, idempotencyKey, now, fundingPositionID, scope.FlowID); err != nil {
			if isUniqueViolation(err) {
				return domain.ErrEffectAlreadyStarted
			}
			return err
		}
		return emitEffectLockEvent(tx, r.database.Queryer(tx), order.ID, now)
	})
	if err != nil {
		return procurementapp.QueueItem{}, false, err
	}
	item, err := r.GetPurchaseSubject(ctx, taskID)
	return item, replay, err
}

// reassignPendingEffectLock transfers only a pre-seller-effect technical lock
// to the task's current active assignee. ClaimTask already records the human
// assignment change; this additional audit binds that takeover to the stranded
// funding lock without changing its canonical PayPal operation identity.
func (r *Repository) reassignPendingEffectLock(
	tx context.Context,
	task domain.ExecutionTask,
	order domain.MerchantOrder,
	previousOperatorID, operatorUserID, retryIdempotencyKey string,
	now time.Time,
) error {
	q := r.database.Queryer(tx)
	result, err := q.ExecContext(tx, `
		UPDATE procurement_effect_locks
		SET operator_user_id=$2, version=version+1
		WHERE merchant_order_id=$1 AND operator_user_id=$3
		  AND state IN ('FUNDING_PENDING','FUNDING_UNKNOWN')
	`, order.ID, operatorUserID, previousOperatorID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return err
		}
		return domain.ErrAssignmentConflict
	}
	if err := emitEffectLockEvent(tx, q, order.ID, now); err != nil {
		return err
	}
	_, err = q.ExecContext(tx, `
		INSERT INTO agency_order_execution_audits(
			id,agency_order_id,merchant_order_id,actor_user_id,action,
			idempotency_key,details,created_at
		) VALUES(gen_random_uuid(),$1,$2,$3,'MERCHANT_EFFECT_FUNDING_REASSIGNED',$4,
		         jsonb_build_object('previousOperatorUserId',$5::text),$6)
		ON CONFLICT (idempotency_key)
		WHERE idempotency_key IS NOT NULL DO NOTHING
	`, task.AgencyOrderID, order.ID, operatorUserID, retryIdempotencyKey,
		previousOperatorID, now)
	return err
}

// ResolveMerchantEffectFunding is the second half of BeginMerchantEffect.
// Only ACTIVE customer funding may move the MO/task/payment to the merchant
// effect states. FAILED is adopted into the exact terminal MO/task projection;
// pending or unknown funding remains visibly locked. None of those non-ACTIVE
// outcomes emits seller purchase permission.
func (r *Repository) ResolveMerchantEffectFunding(
	ctx context.Context,
	taskID, operatorUserID, idempotencyKey, positionID, fundingState string,
	authority procurementapp.PurchasePreparation,
	now time.Time,
) (procurementapp.QueueItem, bool, error) {
	replay := false
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		// Match Prepare/cancel lock order: task/MerchantOrder rows first, then the
		// effect lock. The final validation is therefore atomic with every
		// purchase-stop transition of this exact MO (ADR-0070 §4.7).
		task, order, err := r.lockTask(tx, taskID)
		if err != nil {
			return err
		}
		if err := procmsg.RequireExecution(tx, order.ID, procmsg.EffectGrantMerchantPurchase); err != nil {
			return err
		}
		var lockTaskID, lockOperatorID, lockPositionID, lockState,
			lockIdempotencyKey, lockDecisionID, lockFundingState, lockOperationID string
		if err := q.QueryRowContext(tx, `
			SELECT task_id::text,operator_user_id::text,funding_position_id::text,state,
			       idempotency_key,decision_record_id::text,funding_state,COALESCE(process_flow_id::text,'')
			FROM procurement_effect_locks
			WHERE merchant_order_id=$1
			FOR UPDATE
		`, order.ID).Scan(
			&lockTaskID, &lockOperatorID, &lockPositionID, &lockState,
			&lockIdempotencyKey, &lockDecisionID, &lockFundingState, &lockOperationID,
		); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.ErrFundingNotReady
			}
			return err
		}
		scope, _ := procmsg.ExecutionFrom(tx)
		if lockOperationID != scope.FlowID {
			return procmsg.ErrExecutionRequired
		}
		if lockOperatorID != operatorUserID && (lockState == "FUNDING_PENDING" || lockState == "FUNDING_UNKNOWN") && fundingState == "ACTIVE" {
			if err := requireActiveAssignment(task, operatorUserID, now); err != nil {
				return err
			}
			if err := r.reassignPendingEffectLock(tx, task, order, lockOperatorID, operatorUserID, "workflow:"+scope.FlowID+":reassign:"+operatorUserID, now); err != nil {
				return err
			}
			lockOperatorID = operatorUserID
		}
		if lockTaskID != taskID || lockOperatorID != operatorUserID ||
			lockPositionID != positionID {
			return domain.ErrAssignmentConflict
		}
		if lockState == "STARTED" {
			replay = true
			return nil
		}
		if lockState == "FAILED" &&
			((fundingState == "FAILED" && lockFundingState == "FAILED") ||
				(fundingState == "ACTIVE" && lockFundingState == "FUNDED")) {
			replay = true
			return nil
		}
		// The provider call happens between Prepare and Resolve. Recheck the lease
		// before opening seller-effect permission so a capture completed by a stale
		// operator request remains funded-but-closed until an active assignee retries.
		if fundingState == "ACTIVE" {
			if err := requireActiveAssignment(task, operatorUserID, now); err != nil {
				return err
			}
		}
		if authority.FundingPositionID != positionID || authority.FundingState != fundingState {
			return domain.ErrFundingNotReady
		}
		switch fundingState {
		case "ACTIVATION_PENDING":
			if _, err = q.ExecContext(tx, `
				UPDATE procurement_effect_locks
				SET state='FUNDING_PENDING',funding_state='FUNDING_PENDING',
				    version=version+1
				WHERE merchant_order_id=$1
			`, order.ID); err != nil {
				return err
			}
			return emitEffectLockEvent(tx, q, order.ID, now)
		case "ACTIVATION_UNKNOWN":
			if _, err = q.ExecContext(tx, `
				UPDATE procurement_effect_locks
				SET state='FUNDING_UNKNOWN',funding_state='FUNDING_UNKNOWN',
				    version=version+1
				WHERE merchant_order_id=$1
			`, order.ID); err != nil {
				return err
			}
			return emitEffectLockEvent(tx, q, order.ID, now)
		case "FAILED":
			if _, err = q.ExecContext(tx, `
				UPDATE procurement_effect_locks
				SET state='FAILED',funding_state='FAILED',funding_resolved_at=$2,
				    resolved_at=$2,version=version+1
				WHERE merchant_order_id=$1
				  AND state IN ('FUNDING_PENDING','FUNDING_UNKNOWN')
			`, order.ID, now); err != nil {
				return fmt.Errorf("project failed funding lock: %w", err)
			}
			if err = emitEffectLockEvent(tx, q, order.ID, now); err != nil {
				return err
			}
			if _, err = q.ExecContext(tx, `
				UPDATE merchant_orders
				SET state='FAILED',failure_code='FUNDING_ACTIVATION_FAILED',
				    result_hash=COALESCE(result_hash,'funding-failed:'||$2),
				    version=version+1,updated_at=$3
				WHERE id=$1 AND state='PLANNED'
			`, order.ID, positionID, now); err != nil {
				return fmt.Errorf("project failed merchant order: %w", err)
			}
			if _, err = q.ExecContext(tx, `
				UPDATE merchant_payments SET state='FAILED',version=version+1,updated_at=$2
				WHERE merchant_order_id=$1 AND state='PLANNED'
			`, order.ID, now); err != nil {
				return fmt.Errorf("project failed merchant payment: %w", err)
			}
			if _, err = q.ExecContext(tx, `
				UPDATE merchant_order_execution_tasks
				SET state='FAILED',handled_at=$2,version=version+1,updated_at=$2
				WHERE id=$1 AND state IN ('CLAIMED','IN_PROGRESS')
			`, task.ID, now); err != nil {
				return fmt.Errorf("project failed procurement task: %w", err)
			}
			if err = emitMerchantOrderEvent(tx, q, order.ID, now); err != nil {
				return fmt.Errorf("emit failed merchant order: %w", err)
			}
			_, err = q.ExecContext(tx, `
				INSERT INTO agency_order_execution_audits(
					id,agency_order_id,merchant_order_id,actor_user_id,action,
					idempotency_key,details,created_at
				) VALUES(gen_random_uuid(),$1,$2,$3,'MERCHANT_EFFECT_FUNDING_FAILED',
				         $4,jsonb_build_object('positionId',$5::text),$6)
				ON CONFLICT (idempotency_key)
				WHERE idempotency_key IS NOT NULL DO NOTHING
			`, task.AgencyOrderID, order.ID, operatorUserID, lockIdempotencyKey,
				positionID, now)
			if err != nil {
				return fmt.Errorf("audit failed merchant funding: %w", err)
			}
			return nil
		case "ACTIVE":
			// Continue below.
		default:
			return domain.ErrFundingNotReady
		}
		if order.State != domain.OrderPlanned ||
			(task.State != domain.TaskClaimed && task.State != domain.TaskInProgress) {
			return domain.ErrTaskStateInvalid
		}
		var latestDecisionID string
		if err := q.QueryRowContext(tx, `
			SELECT id::text FROM procurement_decision_records
			WHERE merchant_order_id=$1
			ORDER BY created_at DESC,id DESC LIMIT 1
		`, order.ID).Scan(&latestDecisionID); errors.Is(err, sql.ErrNoRows) {
			return domain.ErrManualDecisionRequired
		} else if err != nil {
			return err
		}
		if latestDecisionID != lockDecisionID {
			return domain.ErrManualDecisionRequired
		}
		var purchaseStops, cancellations int
		if err := q.QueryRowContext(tx, `SELECT
           (SELECT count(*) FROM procurement_customer_requests WHERE merchant_order_id=$1 AND state IN ('PENDING','DECLINED','FAILED_NO_RESPONSE','CANCELLED')),
           (SELECT count(*) FROM agency_order_cancellations WHERE merchant_order_id=$1)`, order.ID).Scan(&purchaseStops, &cancellations); err != nil {
			return err
		}
		refundObligations := 0
		if authority.HasRefundObligation {
			refundObligations = 1
		}
		if purchaseStops > 0 || refundObligations > 0 {
			// This is a defensive adoption path for a stop fact that somehow
			// crossed the ingress fence. Capture is already ACTIVE, so close seller
			// permission and emit an exact-MO FAILED event. Payment then selects a
			// REFUND compensation from the still-ACTIVE funding position.
			if _, err := q.ExecContext(tx, `
				UPDATE procurement_effect_locks
				SET state='FAILED',funding_state='FUNDED',
				    funding_resolved_at=COALESCE(funding_resolved_at,$2),resolved_at=$2,
				    version=version+1
				WHERE merchant_order_id=$1
				  AND state IN ('FUNDING_PENDING','FUNDING_UNKNOWN')
			`, order.ID, now); err != nil {
				return fmt.Errorf("close customer-stopped funding lock: %w", err)
			}
			if err := emitEffectLockEvent(tx, q, order.ID, now); err != nil {
				return err
			}
			if _, err := q.ExecContext(tx, `
				UPDATE merchant_orders
				SET state='FAILED',failure_code='CUSTOMER_PURCHASE_STOP_AFTER_FUNDING',
				    result_hash=COALESCE(result_hash,'customer-stop:'||$2),
				    version=version+1,updated_at=$3
				WHERE id=$1 AND state='PLANNED'
			`, order.ID, positionID, now); err != nil {
				return fmt.Errorf("project customer-stopped merchant order: %w", err)
			}
			if _, err := q.ExecContext(tx, `
				UPDATE merchant_payments
				SET state='FAILED',version=version+1,updated_at=$2
				WHERE merchant_order_id=$1 AND state='PLANNED'
			`, order.ID, now); err != nil {
				return fmt.Errorf("project customer-stopped merchant payment: %w", err)
			}
			if _, err := q.ExecContext(tx, `
				UPDATE merchant_order_execution_tasks
				SET state='FAILED',handled_at=$2,version=version+1,updated_at=$2
				WHERE id=$1 AND state IN ('CLAIMED','IN_PROGRESS')
			`, task.ID, now); err != nil {
				return fmt.Errorf("project customer-stopped procurement task: %w", err)
			}
			if err := emitMerchantOrderEvent(tx, q, order.ID, now); err != nil {
				return fmt.Errorf("emit customer-stopped merchant order: %w", err)
			}
			_, err := q.ExecContext(tx, `
				INSERT INTO agency_order_execution_audits(
					id,agency_order_id,merchant_order_id,actor_user_id,action,
					idempotency_key,details,created_at
				) VALUES(gen_random_uuid(),$1,$2,$3,
				         'MERCHANT_EFFECT_CUSTOMER_STOP_AFTER_FUNDING',$4,
				         jsonb_build_object('positionId',$5::text),$6)
				ON CONFLICT (idempotency_key)
				WHERE idempotency_key IS NOT NULL DO NOTHING
			`, task.AgencyOrderID, order.ID, operatorUserID,
				lockIdempotencyKey+":customer-stop", positionID, now)
			return err
		}
		if refundObligations > 0 || cancellations > 0 {
			return domain.ErrOrderInException
		}
		if _, err := q.ExecContext(tx, `
			UPDATE procurement_effect_locks
			SET state='STARTED',funding_state='FUNDED',funding_resolved_at=$2,process_effect_id=$3,
			    version=version+1
			WHERE merchant_order_id=$1
			  AND state IN ('FUNDING_PENDING','FUNDING_UNKNOWN')
		`, order.ID, now, processEffectID(tx)); err != nil {
			return err
		}
		if err := emitEffectLockEvent(tx, q, order.ID, now); err != nil {
			return err
		}
		if _, err := q.ExecContext(tx, `
			UPDATE merchant_orders SET state='PLACEMENT_PENDING',
				version=version+1,updated_at=$1 WHERE id=$2 AND state='PLANNED'
		`, now, order.ID); err != nil {
			return err
		}
		// PURCHASING 진입도 보고한다(ADR-0070 §4.2 — 종전에는 미발행이라 리듀서가
		// 구매 진행 중인 MO를 몰랐다).
		if err := emitMerchantOrderEvent(tx, q, order.ID, now); err != nil {
			return err
		}
		if _, err := q.ExecContext(tx, `
			UPDATE merchant_order_execution_tasks SET state='IN_PROGRESS',
				version=version+1,updated_at=$1 WHERE id=$2
		`, now, task.ID); err != nil {
			return err
		}
		if _, err := q.ExecContext(tx, `
			UPDATE merchant_payments SET state='EXECUTION_PENDING',
				version=version+1,updated_at=$1
			WHERE merchant_order_id=$2 AND state='PLANNED'
		`, now, order.ID); err != nil {
			return err
		}
		_, err = q.ExecContext(tx, `
			INSERT INTO agency_order_execution_audits(
				id,agency_order_id,merchant_order_id,actor_user_id,action,
				idempotency_key,details,created_at
			) VALUES(gen_random_uuid(),$1,$2,$3,'MERCHANT_EFFECT_STARTED',$4,$5,$6)
			ON CONFLICT (idempotency_key)
			WHERE idempotency_key IS NOT NULL DO NOTHING
		`, task.AgencyOrderID, order.ID, operatorUserID, lockIdempotencyKey,
			mustJSON(map[string]any{
				"fundingPositionId": positionID,
				"fundingState":      "FUNDED",
			}), now)
		return err
	})
	if err != nil {
		return procurementapp.QueueItem{}, replay, err
	}
	item, err := r.GetPurchaseSubject(ctx, taskID)
	return item, replay, err
}

func decisionAllowsMerchantEffect(decision domain.ManualDecision) bool {
	return decision == domain.DecisionWithinAuthorization ||
		decision == domain.DecisionImmaterialVariance
}

func authorizationAllowsManualProcurement(
	authorizationKind, authorizationProfileHash, orderProfileHash string,
) bool {
	return authorizationProfileHash != "" &&
		authorizationProfileHash == orderProfileHash &&
		domain.ProcurementAuthorizationKind(authorizationKind) ==
			domain.AuthorizationManualOperatorPurchase
}

func isUniqueViolation(err error) bool {
	type sqlStater interface{ SQLState() string }
	var state sqlStater
	return errors.As(err, &state) && state.SQLState() == "23505"
}

func (r *Repository) completeEffectLock(
	ctx context.Context, merchantOrderID, state string, now time.Time,
) error {
	if _, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE procurement_effect_locks
		SET state=$1, resolved_at=$2, version=version+1
		WHERE merchant_order_id=$3 AND state='STARTED'
	`, state, now, merchantOrderID); err != nil {
		return err
	}
	return emitEffectLockEvent(ctx, r.database.Queryer(ctx), merchantOrderID, now)
}

var _ procurementapp.ManualReviewRepository = (*Repository)(nil)

// fmt remains used in error wrapping while this repository is kept as a
// standalone vertical slice.
var _ = fmt.Sprintf

func processEffectID(ctx context.Context) string {
	scope, _ := procmsg.ExecutionFrom(ctx)
	return scope.EffectID
}
