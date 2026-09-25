package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	agencyapp "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/app"
	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
)

func uniqueViolation(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "23505"
}

// CreateRefundRequest atomically verifies owner, post-effect eligibility and
// the one-MO economic boundary. The amount returned to callers is read from
// the immutable issuance allocation; no line/unit calculation occurs here.
func (r *Repository) CreateRefundRequest(
	ctx context.Context,
	request agencydomain.RefundRequest,
	authority agencyapp.RefundAuthority,
	now time.Time,
) (agencydomain.RefundRequest, error) {
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		if e := procmsg.RequireExecution(tx, request.MerchantOrderID, procmsg.EffectApplyOwnerAction); e != nil {
			return e
		}
		if authority.HasCompensation {
			return agencydomain.ErrRefundRequestInvalid
		}
		var owner, allocationID string
		var grossMinor int64
		merchantState, fundingState := authority.MerchantState, authority.FundingState
		err := r.database.Queryer(tx).QueryRowContext(tx, `
            SELECT o.user_id::text,a.id::text,a.customer_gross_minor FROM agency_orders o
            JOIN agency_order_mo_allocations a ON a.agency_order_id=o.id
            WHERE o.id=$1 AND a.id=$2 AND NOT EXISTS(
             SELECT 1 FROM agency_order_refund_requests prior WHERE prior.allocation_id=a.id
             AND (prior.state IN ('REQUESTED','REVIEWING') OR prior.decision='APPROVED'))
            FOR UPDATE OF o`, request.AgencyOrderID, authority.AllocationID).Scan(&owner, &allocationID, &grossMinor)
		if errors.Is(err, sql.ErrNoRows) {
			return agencydomain.ErrRefundRequestInvalid
		}
		if err != nil {
			return err
		}
		if owner != request.UserID {
			return agencydomain.ErrNotFound
		}
		// PLANNED/AVAILABLE is the no-reason cancellation lane. Once canceled,
		// the MO cannot be reopened as a refund request.
		if merchantState == "PLANNED" || merchantState == "FAILED" ||
			merchantState == "CANCELLED" || fundingState == "AVAILABLE" ||
			fundingState == "FAILED" || fundingState == "RELEASED" {
			return agencydomain.ErrRefundRequestInvalid
		}
		if _, err = r.database.Queryer(tx).ExecContext(tx, `
			INSERT INTO agency_order_refund_requests(
				id,agency_order_id,user_id,state,reason_code,public_rationale,
				allocation_id,merchant_order_id,created_at,updated_at
			) VALUES($1,$2,$3,'REQUESTED',$4,$5,$6,$7,$8,$8)
		`, request.ID, request.AgencyOrderID, request.UserID, request.ReasonCode,
			strings.TrimSpace(request.PublicRationale), allocationID,
			request.MerchantOrderID, now); err != nil {
			return err
		}
		return emitRefundRequested(tx, r.database.Queryer(tx), request.ID, now)
	})
	if err != nil {
		if uniqueViolation(err) {
			return agencydomain.RefundRequest{}, agencydomain.ErrRefundRequestInvalid
		}
		return agencydomain.RefundRequest{}, err
	}
	return r.getRefundRequest(ctx, request.ID, false)
}

func (r *Repository) getRefundRequest(
	ctx context.Context,
	requestID string,
	includeReviewContext bool,
) (agencydomain.RefundRequest, error) {
	var request agencydomain.RefundRequest
	var decidedAt sql.NullTime
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT request.id::text, request.agency_order_id::text,
		       request.merchant_order_id::text, request.allocation_id::text,
		       allocation.customer_gross_minor, allocation.currency,
		       request.user_id::text, request.state, request.reason_code,
		       request.public_rationale, COALESCE(request.decision,''),
		       COALESCE(request.decision_public_rationale,''),
		       COALESCE(request.internal_note,''),
		       COALESCE(request.decided_by_user_id::text,''), request.decided_at,
		       request.created_at, request.updated_at
		FROM agency_order_refund_requests request
		JOIN agency_order_mo_allocations allocation ON allocation.id=request.allocation_id
		WHERE request.id=$1
	`, requestID).Scan(
		&request.ID, &request.AgencyOrderID, &request.MerchantOrderID,
		&request.AllocationID, &request.RequestedGrossAmount.AmountMinor,
		&request.RequestedGrossAmount.Currency, &request.UserID, &request.State,
		&request.ReasonCode, &request.PublicRationale, &request.Decision,
		&request.DecisionPublicRationale, &request.InternalNote, &request.DecidedBy,
		&decidedAt, &request.CreatedAt, &request.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return agencydomain.RefundRequest{}, agencydomain.ErrNotFound
	}
	if err != nil {
		return agencydomain.RefundRequest{}, err
	}
	if decidedAt.Valid {
		request.DecidedAt = &decidedAt.Time
	}
	if includeReviewContext {
		request.ReviewContext, err = r.loadRefundReviewContext(ctx, request)
	}
	return request, err
}

func (r *Repository) loadRefundReviewContext(
	ctx context.Context,
	request agencydomain.RefundRequest,
) (*agencydomain.RefundReviewContext, error) {
	var snapshotJSON []byte
	if err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT snapshot FROM agency_orders WHERE id=$1
	`, request.AgencyOrderID).Scan(&snapshotJSON); err != nil {
		return nil, fmt.Errorf("load refund review order snapshot: %w", err)
	}
	var order agencydomain.AgencyOrder
	if err := json.Unmarshal(snapshotJSON, &order); err != nil {
		return nil, fmt.Errorf("decode refund review order snapshot: %w", err)
	}

	result := &agencydomain.RefundReviewContext{
		OrderNumber: request.AgencyOrderID, MerchantOrderID: request.MerchantOrderID,
		AllocationID: request.AllocationID, RequestedGrossAmount: request.RequestedGrossAmount,
		Lines: []agencydomain.RefundReviewLine{}, Units: []agencydomain.RefundReviewUnitFact{},
		InternalNote: request.InternalNote,
	}
	var checkoutOrdinal int
	if err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT merchant_order.shop_domain, merchant_order.merchant_id,
		       COALESCE(merchant_order.external_order_ref,''), merchant_order.state,
		       merchant_order.checkout_ordinal
		FROM merchant_orders merchant_order
		WHERE merchant_order.id=$1 AND merchant_order.allocation_id=$2
	`, request.MerchantOrderID, request.AllocationID).Scan(
		&result.ShopDomain, &result.MerchantID, &result.ExternalOrderRef,
		&result.MerchantOrderState, &checkoutOrdinal,
	); err != nil {
		return nil, fmt.Errorf("load refund review MerchantOrder: %w", err)
	}
	if checkoutOrdinal < 1 || checkoutOrdinal > len(order.MerchantCheckouts) {
		return nil, agencydomain.ErrRefundRequestInvalid
	}
	lineIDs := make(map[string]bool)
	for _, lineID := range order.MerchantCheckouts[checkoutOrdinal-1].LineRefs {
		lineIDs[lineID] = true
	}
	for _, line := range order.Lines {
		if lineIDs[line.LineID] {
			result.Lines = append(result.Lines, agencydomain.RefundReviewLine{
				LineID: line.LineID, ProductURL: line.ProductURL,
				ProductTitle: line.ProductTitle, VariantID: line.VariantID,
				VariantTitle:    line.VariantTitle,
				SelectedOptions: append([]string{}, line.SelectedOptions...),
				Quantity:        line.Quantity,
			})
		}
	}
	if len(result.Lines) == 0 {
		return nil, agencydomain.ErrRefundRequestInvalid
	}

	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT merchant_unit.id::text, merchant_unit.line_id,
		       merchant_unit.unit_index, merchant_unit.disposition,
		       (expected.id IS NOT NULL), COALESCE(expected.fulfillment,''),
		       COALESCE(shipment.state,''), COALESCE(shipment.carrier,''),
		       COALESCE(shipment.tracking_ref,''),
		       LEFT(COALESCE(latest_event.status,''),200),
		       LEFT(COALESCE(latest_event.note,''),2000), latest_event.occurred_at,
		       COALESCE(resolution.cause,''), COALESCE(resolution.decision,''),
		       COALESCE(resolution.note,''), resolution.created_at,
		       (return_record.id IS NOT NULL), COALESCE(return_record.state,''),
		       COALESCE(return_record.merchant_disposition,''),
		       COALESCE(return_record.note,''), return_record.updated_at
		FROM merchant_order_units merchant_unit
		LEFT JOIN logistics_expected_units expected
		  ON expected.merchant_order_unit_id=merchant_unit.id
		LEFT JOIN logistics_shipment_allocations allocation
		  ON allocation.expected_unit_id=expected.id AND allocation.active
		LEFT JOIN logistics_shipments shipment ON shipment.id=allocation.shipment_id
		LEFT JOIN LATERAL (
			SELECT event.status,event.note,event.occurred_at
			FROM logistics_shipment_events event
			WHERE event.shipment_id=shipment.id
			ORDER BY event.occurred_at DESC,event.created_at DESC,event.id DESC LIMIT 1
		) latest_event ON TRUE
		LEFT JOIN logistics_delivery_resolutions resolution
		  ON resolution.expected_unit_id=expected.id
		LEFT JOIN logistics_returns return_record
		  ON return_record.expected_unit_id=expected.id
		WHERE merchant_unit.merchant_order_id=$1
		ORDER BY merchant_unit.line_id,merchant_unit.unit_index
	`, request.MerchantOrderID)
	if err != nil {
		return nil, fmt.Errorf("load refund review unit facts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var unit agencydomain.RefundReviewUnitFact
		var deliveryRecorded, returnRecorded bool
		var latestEventAt, resolutionAt, returnUpdatedAt sql.NullTime
		if err := rows.Scan(
			&unit.MerchantOrderUnitID, &unit.LineID, &unit.UnitIndex, &unit.Disposition,
			&deliveryRecorded, &unit.DeliveryFacts.ExpectedFulfillment,
			&unit.DeliveryFacts.ShipmentState, &unit.DeliveryFacts.Carrier,
			&unit.DeliveryFacts.TrackingRef, &unit.DeliveryFacts.LatestEventStatus,
			&unit.DeliveryFacts.LatestEventNote, &latestEventAt,
			&unit.DeliveryFacts.ResolutionCause, &unit.DeliveryFacts.ResolutionDecision,
			&unit.DeliveryFacts.ResolutionNote, &resolutionAt,
			&returnRecorded, &unit.ReturnFacts.State,
			&unit.ReturnFacts.MerchantDisposition, &unit.ReturnFacts.Note,
			&returnUpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan refund review unit facts: %w", err)
		}
		unit.DeliveryFacts.Recorded = deliveryRecorded
		unit.ReturnFacts.Recorded = returnRecorded
		if latestEventAt.Valid {
			unit.DeliveryFacts.LatestEventOccurredAt = &latestEventAt.Time
		}
		if resolutionAt.Valid {
			unit.DeliveryFacts.ResolutionRecordedAt = &resolutionAt.Time
		}
		if returnUpdatedAt.Valid {
			unit.ReturnFacts.UpdatedAt = &returnUpdatedAt.Time
		}
		result.Units = append(result.Units, unit)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Repository) ListRefundRequests(
	ctx context.Context,
	userID, agencyOrderID string,
	openOnly bool,
	limit int,
) ([]agencydomain.RefundRequest, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	where := []string{"TRUE"}
	arguments := []any{}
	bind := func(value any) string {
		arguments = append(arguments, value)
		return fmt.Sprintf("$%d", len(arguments))
	}
	if strings.TrimSpace(userID) != "" {
		where = append(where, "request.user_id="+bind(strings.TrimSpace(userID)))
	}
	if strings.TrimSpace(agencyOrderID) != "" {
		where = append(where, "request.agency_order_id="+bind(strings.TrimSpace(agencyOrderID)))
	}
	if openOnly {
		where = append(where, "request.state IN ('REQUESTED','REVIEWING')")
	}
	arguments = append(arguments, limit)
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT request.id::text FROM agency_order_refund_requests request
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY request.created_at LIMIT $`+fmt.Sprint(len(arguments)), arguments...)
	if err != nil {
		return nil, fmt.Errorf("list refund requests: %w", err)
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	requests := make([]agencydomain.RefundRequest, 0, len(ids))
	includeReviewContext := strings.TrimSpace(userID) == ""
	for _, id := range ids {
		request, err := r.getRefundRequest(ctx, id, includeReviewContext)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, nil
}

func (r *Repository) ListResolvedRefundRequests(
	ctx context.Context,
	limit int,
) ([]agencydomain.RefundRequest, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT id::text FROM agency_order_refund_requests
		WHERE state='RESOLVED' ORDER BY updated_at DESC LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list resolved refund requests: %w", err)
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	requests := make([]agencydomain.RefundRequest, 0, len(ids))
	for _, id := range ids {
		request, err := r.getRefundRequest(ctx, id, true)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, nil
}

func (r *Repository) DecideRefundRequest(
	ctx context.Context,
	requestID, actorUserID string,
	decision agencyapp.RefundDecision,
	now time.Time,
) (agencydomain.RefundRequest, error) {
	outcome := agencydomain.RefundDecisionRejected
	if decision.Approve {
		outcome = agencydomain.RefundDecisionApproved
	}
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		var orderID, moID string
		if e := r.database.Queryer(tx).QueryRowContext(tx, `SELECT agency_order_id::text,merchant_order_id::text FROM agency_order_refund_requests WHERE id=$1`, requestID).Scan(&orderID, &moID); e != nil {
			return e
		}
		if e := procmsg.RequireExecution(tx, moID, procmsg.EffectApplyOwnerAction); e != nil {
			return e
		}
		result, err := r.database.Queryer(tx).ExecContext(tx, `
			UPDATE agency_order_refund_requests
			SET state='RESOLVED',decision=$2,decision_public_rationale=$3,
			    internal_note=NULLIF($4,''),decided_by_user_id=$5,
			    decided_at=$6,updated_at=$6
			WHERE id=$1 AND state IN ('REQUESTED','REVIEWING')
		`, requestID, outcome, strings.TrimSpace(decision.PublicRationale),
			strings.TrimSpace(decision.InternalNote), actorUserID, now)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return agencydomain.ErrRefundRequestInvalid
		}
		return emitRefundReviewDecided(tx, r.database.Queryer(tx), requestID, now)
	})
	if err != nil {
		return agencydomain.RefundRequest{}, err
	}
	return r.getRefundRequest(ctx, requestID, true)
}

func (r *Repository) CountOpenRefundRequests(ctx context.Context) (int, error) {
	var count int
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT count(*) FROM agency_order_refund_requests
		WHERE state IN ('REQUESTED','REVIEWING')
	`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count open refund requests: %w", err)
	}
	return count, nil
}
