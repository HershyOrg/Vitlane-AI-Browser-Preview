package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

// 이 파일은 payment 소유 상태 변경의 process 이벤트 발행이다(ADR-0056 §2).
// 각 emit은 owner 상태 변경과 같은 transaction에서 자기 행을 재조회해 발행한다
// — dedup_key가 행 version에 결정적으로 붙으므로 재실행·replay는 no-op이고,
// 수량 관찰(open 대표·환불 집계)은 같은 transaction의 COUNT라 정확하다.

type PaymentQueryer interface {
	procmsg.Queryer
	QueryRowContext(context.Context, string, ...any) sharedpostgres.Row
}

// openPaymentRepresentative는 미결 시도 대표 1행의 관찰이다 — 종전
// ProcessFacts.OpenPaymentState LATERAL과 같은 계약(대사 필요 상태 우선).
func openPaymentRepresentative(
	ctx context.Context,
	q PaymentQueryer,
	agencyOrderID string,
) (state, reason string, err error) {
	row := q.QueryRowContext(ctx, `
		SELECT COALESCE(pay.state,''), COALESCE(pay.last_reason_code,'')
		FROM payment_customer_payments pay
		WHERE pay.agency_order_id=$1
		  AND pay.state IN ('CREATED','ACTION_REQUIRED','PROCESSING',
		                    'OUTCOME_UNKNOWN')
		ORDER BY CASE WHEN pay.state='OUTCOME_UNKNOWN' THEN 0 ELSE 1 END,
		         pay.created_at DESC
		LIMIT 1
	`, agencyOrderID)
	if scanErr := row.Scan(&state, &reason); scanErr != nil {
		if errors.Is(scanErr, sql.ErrNoRows) {
			return "", "", nil
		}
		return "", "", scanErr
	}
	return state, reason, nil
}

// EmitCustomerPaymentEvent는 CustomerPayment 전이 하나를 발행한다. Procurement
// 계획 권위는 별도 CustomerFundingReady 이벤트이며 이 이벤트는 상태 관찰만 한다.
func EmitCustomerPaymentEvent(
	ctx context.Context,
	q PaymentQueryer,
	customerPaymentID string,
	now time.Time,
) error {
	var agencyOrderID, rail, state, reason string
	var version int64
	if err := q.QueryRowContext(ctx, `
		SELECT agency_order_id::text, rail, state, COALESCE(last_reason_code,''), version
		FROM payment_customer_payments WHERE id=$1
	`, customerPaymentID).Scan(&agencyOrderID, &rail, &state, &reason, &version); err != nil {
		return fmt.Errorf("read customer payment for event: %w", err)
	}
	if err := procmsg.LockOrderEventStream(ctx, q, agencyOrderID); err != nil {
		return err
	}
	openState, openReason, err := openPaymentRepresentative(ctx, q, agencyOrderID)
	if err != nil {
		return fmt.Errorf("observe open payment: %w", err)
	}
	event := procmsg.ProcessEvent{
		AgencyOrderID: agencyOrderID,
		Source:        procmsg.SourcePayment,
		DedupKey: procmsg.EventDedupKey("customer_payment", customerPaymentID,
			fmt.Sprintf("v%d", version)),
	}
	event.Type = procmsg.EventCustomerPaymentStateChanged
	event.Payload = procmsg.CustomerPaymentStateChangedPayload{
		CustomerPaymentID: customerPaymentID, State: state, Reason: reason,
		OpenState: openState, OpenReason: openReason,
	}
	_, err = procmsg.AppendEvent(ctx, q, event, now)
	return err
}

func EmitFundsReceiptEvent(
	ctx context.Context,
	q PaymentQueryer,
	fundsReceiptID string,
	now time.Time,
) error {
	var agencyOrderID, customerPaymentID string
	if err := q.QueryRowContext(ctx, `
		SELECT agency_order_id::text, customer_payment_id::text
		FROM payment_funds_receipts WHERE id=$1
	`, fundsReceiptID).Scan(&agencyOrderID, &customerPaymentID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// receipt INSERT가 provider 유일성 충돌로 no-op된 replay다 — 최초
			// 기록이 이미 발행했다.
			return nil
		}
		return fmt.Errorf("read funds receipt for event: %w", err)
	}
	_, err := procmsg.AppendEvent(ctx, q, procmsg.ProcessEvent{
		AgencyOrderID: agencyOrderID,
		Source:        procmsg.SourcePayment,
		Type:          procmsg.EventFundsReceiptRecorded,
		Payload: procmsg.FundsReceiptRecordedPayload{
			FundsReceiptID: fundsReceiptID, CustomerPaymentID: customerPaymentID,
		},
		DedupKey: procmsg.EventDedupKey("funds_receipt", fundsReceiptID, "RECORDED"),
	}, now)
	return err
}

func EmitCustomerFundingReadyEvent(
	ctx context.Context,
	q PaymentQueryer,
	customerPaymentID string,
	now time.Time,
) error {
	var agencyOrderID, rail, environment string
	var amountMinor int64
	var fundsReceiptID string
	var positionsJSON []byte
	var version int64
	if err := q.QueryRowContext(ctx, `
		SELECT agency_order_id::text,rail,provider_environment,version,amount_minor,
		COALESCE((SELECT id::text FROM payment_funds_receipts WHERE customer_payment_id=$1 AND accepted ORDER BY occurred_at,id LIMIT 1),''),
		COALESCE((SELECT jsonb_agg(jsonb_build_object('positionId',id::text,'allocationId',allocation_id::text,'amountMinor',amount_minor,'state',state) ORDER BY allocation_id) FROM payment_mo_funding_positions WHERE customer_payment_id=$1),'[]'::jsonb)
		FROM payment_customer_payments WHERE id=$1
	`, customerPaymentID).Scan(
		&agencyOrderID, &rail, &environment, &version, &amountMinor, &fundsReceiptID, &positionsJSON,
	); err != nil {
		return fmt.Errorf("read funding-ready payment: %w", err)
	}
	var positions []procmsg.FundingPositionSnapshot
	if err := json.Unmarshal(positionsJSON, &positions); err != nil {
		return err
	}
	source := "GIWA_PREPAID"
	if rail == "PAYPAL" {
		source = "PAYPAL_AUTHORIZATION"
	}
	_, err := procmsg.AppendEvent(ctx, q, procmsg.ProcessEvent{
		AgencyOrderID: agencyOrderID,
		Source:        procmsg.SourcePayment,
		Type:          procmsg.EventCustomerFundingReady,
		Payload: procmsg.CustomerFundingReadyPayload{
			AmountMinor: amountMinor, FundsReceiptID: fundsReceiptID, Positions: positions,
			CustomerPaymentID:   customerPaymentID,
			Rail:                rail,
			Source:              source,
			ProviderEnvironment: environment,
		},
		DedupKey: procmsg.EventDedupKey(
			"customer_payment", customerPaymentID, fmt.Sprintf("FUNDING_READY_v%d", version),
		),
	}, now)
	return err
}

// EmitMOCompensationEvent publishes one whole-MO compensation transition.
// The order-level aggregate (active/succeeded counts) is derived by the
// reducer from its identity fold (ADR-0070 §4.1); the owner transaction only
// carries its own row and takes the order stream lock at append time.
func EmitMOCompensationEvent(
	ctx context.Context,
	q PaymentQueryer,
	moCompensationID string,
	now time.Time,
) error {
	var agencyOrderID, merchantOrderID, allocationID, state, action, cause string
	var version int64
	if err := q.QueryRowContext(ctx, `
		SELECT compensation.agency_order_id::text,
		       position.merchant_order_id::text,
		       compensation.allocation_id::text,
		       compensation.state,
		       compensation.action,
		       compensation.cause,
		       compensation.version
		FROM payment_mo_compensations compensation
		JOIN payment_mo_funding_positions position ON position.id=compensation.funding_position_id
		WHERE compensation.id=$1
	`, moCompensationID).Scan(
		&agencyOrderID, &merchantOrderID, &allocationID, &state, &action, &cause, &version,
	); err != nil {
		return fmt.Errorf("read MO compensation for event: %w", err)
	}
	var flowID, effectID string
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(process_flow_id::text,''),COALESCE(process_effect_id::text,'') FROM payment_mo_compensations WHERE id=$1`, moCompensationID).Scan(&flowID, &effectID); err != nil {
		return err
	}
	_, err := procmsg.AppendEvent(ctx, q, procmsg.ProcessEvent{
		FlowID: flowID, CausationEffectID: effectID, SourceEntityVersion: version,
		AgencyOrderID: agencyOrderID,
		Source:        procmsg.SourcePayment,
		Type:          procmsg.EventMOCompensationStateChanged,
		Payload: procmsg.MOCompensationStateChangedPayload{
			CompensationID:  moCompensationID,
			MerchantOrderID: merchantOrderID,
			AllocationID:    allocationID,
			State:           state,
			Action:          action,
			Cause:           cause,
		},
		DedupKey: procmsg.EventDedupKey(
			"payment_mo_compensation", moCompensationID, fmt.Sprintf("v%d", version),
		),
	}, now)
	return err
}

// PaymentRowsQueryer는 다행 관찰이 필요한 emit의 queryer다(shared queryer가
// 충족한다).
type PaymentRowsQueryer interface {
	PaymentQueryer
	QueryContext(context.Context, string, ...any) (sharedpostgres.Rows, error)
}

// EmitMOFundingEvent publishes one MO funding position transition (ADR-0070
// §4.2 — 카탈로그 v2). The dedup key binds to the position row version, so a
// replayed or no-op UPDATE never produces a second event. MerchantOrderID is
// empty before planning (positions are created at AUTHORIZE/prepaid intake).
func EmitMOFundingEvent(
	ctx context.Context,
	q PaymentQueryer,
	positionID string,
	now time.Time,
) error {
	var agencyOrderID, allocationID, merchantOrderID, rail, state string
	var version int64
	if err := q.QueryRowContext(ctx, `
		SELECT fp.agency_order_id::text, fp.allocation_id::text,
		       COALESCE(fp.merchant_order_id::text,''), fp.rail, fp.state, fp.version
		FROM payment_mo_funding_positions fp
		WHERE fp.id=$1
	`, positionID).Scan(
		&agencyOrderID, &allocationID, &merchantOrderID, &rail, &state, &version,
	); err != nil {
		return fmt.Errorf("read MO funding position for event: %w", err)
	}
	var flowID, effectID string
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(process_flow_id::text,''),COALESCE(process_effect_id::text,'') FROM payment_mo_funding_positions WHERE id=$1`, positionID).Scan(&flowID, &effectID); err != nil {
		return err
	}
	_, err := procmsg.AppendEvent(ctx, q, procmsg.ProcessEvent{
		FlowID: flowID, CausationEffectID: effectID, SourceEntityVersion: version,
		AgencyOrderID: agencyOrderID,
		Source:        procmsg.SourcePayment,
		Type:          procmsg.EventMOFundingStateChanged,
		Payload: procmsg.MOFundingStateChangedPayload{
			PositionID: positionID, MerchantOrderID: merchantOrderID,
			AllocationID: allocationID, Rail: rail, State: state,
		},
		DedupKey: procmsg.EventDedupKey(
			"payment_mo_funding_position", positionID, fmt.Sprintf("v%d", version),
		),
	}, now)
	return err
}

// EmitMOFundingEventForCompensation emits the funding position transition
// that a whole-MO compensation transaction just applied (GIWA outbox paths
// update the position through the compensation join).
func EmitMOFundingEventForCompensation(
	ctx context.Context,
	q PaymentQueryer,
	compensationID string,
	now time.Time,
) error {
	var positionID string
	if err := q.QueryRowContext(ctx, `
		SELECT funding_position_id::text FROM payment_mo_compensations WHERE id=$1
	`, compensationID).Scan(&positionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("read compensation funding position for event: %w", err)
	}
	return EmitMOFundingEvent(ctx, q, positionID, now)
}

// EmitMOFundingEventsForPayment emits every position of one customer payment
// — the AVAILABLE creation fan-out at PayPal AUTHORIZE / GIWA prepaid intake.
func EmitMOFundingEventsForPayment(
	ctx context.Context,
	q PaymentRowsQueryer,
	customerPaymentID string,
	now time.Time,
) error {
	rows, err := q.QueryContext(ctx, `
		SELECT id::text FROM payment_mo_funding_positions
		WHERE customer_payment_id=$1
		ORDER BY created_at, id
	`, customerPaymentID)
	if err != nil {
		return fmt.Errorf("list funding positions for event: %w", err)
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := EmitMOFundingEvent(ctx, q, id, now); err != nil {
			return err
		}
	}
	return nil
}

// FailAvailableFundingPositions closes every still-AVAILABLE position of one
// PayPal authorization (expiry / definitive reauthorization failure) and
// reports each closed position. It returns how many positions it closed.
func FailAvailableFundingPositions(
	ctx context.Context,
	q PaymentRowsQueryer,
	authorizationID string,
	now time.Time,
) (int64, error) {
	rows, err := q.QueryContext(ctx, `
		UPDATE payment_mo_funding_positions
		SET state='FAILED',version=version+1,updated_at=$2
		WHERE paypal_authorization_id=$1 AND state='AVAILABLE'
		RETURNING id::text
	`, authorizationID, now)
	if err != nil {
		return 0, err
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := EmitMOFundingEvent(ctx, q, id, now); err != nil {
			return 0, err
		}
	}
	return int64(len(ids)), nil
}

// EmitDisputeEvent publishes one PayPal dispute case transition (webhook
// observation or manual action). The dedup key binds to the case row version.
func EmitDisputeEvent(
	ctx context.Context,
	q PaymentQueryer,
	caseID, actionID string,
	now time.Time,
) error {
	var agencyOrderID, receiptID, merchantOrderID, state, providerStatus, outcome string
	var version int64
	if err := q.QueryRowContext(ctx, `
		SELECT dispute.agency_order_id::text,
		       COALESCE(dispute.mo_cash_receipt_id::text,''),
		       COALESCE(mo.id::text,''),
		       dispute.state, COALESCE(dispute.provider_status,''),
		       COALESCE(dispute.outcome,''), dispute.version
		FROM payment_paypal_dispute_cases dispute
		LEFT JOIN payment_mo_cash_receipts receipt ON receipt.id=dispute.mo_cash_receipt_id
		LEFT JOIN merchant_orders mo ON mo.allocation_id=receipt.allocation_id
		WHERE dispute.id=$1
	`, caseID).Scan(
		&agencyOrderID, &receiptID, &merchantOrderID, &state, &providerStatus,
		&outcome, &version,
	); err != nil {
		return fmt.Errorf("read dispute case for event: %w", err)
	}
	_, err := procmsg.AppendEvent(ctx, q, procmsg.ProcessEvent{
		AgencyOrderID: agencyOrderID,
		Source:        procmsg.SourcePayment,
		Type:          procmsg.EventDisputeStateChanged,
		Payload: procmsg.DisputeStateChangedPayload{
			CaseID: caseID, MerchantOrderID: merchantOrderID,
			MOCashReceiptID: receiptID, State: state, ProviderStatus: providerStatus,
			Outcome: outcome, ActionID: actionID, Version: version,
		},
		DedupKey: procmsg.EventDedupKey(
			"payment_paypal_dispute_case", caseID, fmt.Sprintf("v%d", version),
		),
	}, now)
	return err
}
