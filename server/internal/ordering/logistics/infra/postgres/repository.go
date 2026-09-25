package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	logisticsapp "github.com/vitlane/vitlane/server/internal/ordering/logistics/app"
	"github.com/vitlane/vitlane/server/internal/ordering/logistics/domain"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

type Repository struct {
	database *sharedpostgres.Database
}

func NewRepository(database *sharedpostgres.Database) *Repository {
	return &Repository{database: database}
}

func isUniqueViolation(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "23505"
}

func deterministicID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	encoded := hex.EncodeToString(sum[:16])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" +
		encoded[16:20] + "-" + encoded[20:32]
}

const shipmentColumnsSQL = `shipment.id, shipment.agency_order_id, shipment.merchant_order_id,
	shipment.carrier, shipment.tracking_ref, shipment.state, shipment.version,
	shipment.created_at, shipment.updated_at`

func scanShipment(row interface{ Scan(...any) error }) (domain.Shipment, error) {
	var shipment domain.Shipment
	var state string
	err := row.Scan(
		&shipment.ID, &shipment.AgencyOrderID, &shipment.MerchantOrderID,
		&shipment.Carrier, &shipment.TrackingRef, &state, &shipment.Version,
		&shipment.CreatedAt, &shipment.UpdatedAt,
	)
	shipment.State = domain.ShipmentState(state)
	return shipment, err
}

func (r *Repository) ListOrderShipments(
	ctx context.Context,
	agencyOrderID string,
) ([]logisticsapp.ShipmentView, error) {
	views := []logisticsapp.ShipmentView{}
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT `+shipmentColumnsSQL+`
		FROM logistics_shipments shipment
		WHERE shipment.agency_order_id=$1
		ORDER BY shipment.created_at
	`, agencyOrderID)
	if err != nil {
		return nil, fmt.Errorf("list shipments: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		shipment, err := scanShipment(rows)
		if err != nil {
			return nil, fmt.Errorf("scan shipment: %w", err)
		}
		views = append(views, logisticsapp.ShipmentView{Shipment: shipment})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range views {
		units, err := r.listShipmentUnits(ctx, views[i].Shipment.ID)
		if err != nil {
			return nil, err
		}
		events, err := r.listShipmentEvents(ctx, views[i].Shipment.ID)
		if err != nil {
			return nil, err
		}
		views[i].Units = units
		views[i].Events = events
	}
	return views, nil
}

func (r *Repository) listShipmentUnits(
	ctx context.Context,
	shipmentID string,
) ([]domain.ExpectedUnit, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT expected.id, expected.merchant_order_unit_id, expected.merchant_order_id,
		       expected.agency_order_id, expected.line_id, expected.unit_index,
		       expected.fulfillment, expected.registered_at,
		       expected.version, expected.updated_at
		FROM logistics_shipment_allocations allocation
		JOIN logistics_expected_units expected ON expected.id=allocation.expected_unit_id
		WHERE allocation.shipment_id=$1 AND allocation.active
		ORDER BY expected.line_id, expected.unit_index
	`, shipmentID)
	if err != nil {
		return nil, fmt.Errorf("list shipment units: %w", err)
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
			return nil, fmt.Errorf("scan shipment unit: %w", err)
		}
		unit.Fulfillment = domain.Fulfillment(fulfillment)
		units = append(units, unit)
	}
	return units, rows.Err()
}

func (r *Repository) listShipmentEvents(
	ctx context.Context,
	shipmentID string,
) ([]domain.ShipmentEvent, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT id, shipment_id, status, COALESCE(note,''), occurred_at, created_at
		FROM logistics_shipment_events
		WHERE shipment_id=$1
		ORDER BY occurred_at, created_at
	`, shipmentID)
	if err != nil {
		return nil, fmt.Errorf("list shipment events: %w", err)
	}
	defer rows.Close()
	events := []domain.ShipmentEvent{}
	for rows.Next() {
		var event domain.ShipmentEvent
		if err := rows.Scan(
			&event.ID, &event.ShipmentID, &event.Status, &event.Note,
			&event.OccurredAt, &event.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan shipment event: %w", err)
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

// CreateShipment는 PLACED merchant order의 미배정 기대 unit(전체 또는 지정
// 부분집합)에 새 패키지를 배정한다(ADR-0053 — 모드 무관; Sandbox 패키지는
// "처리했다 치고" evidence다). (carrier, tracking_ref)와 결정적 ID로 중복
// 등록은 충돌한다.
func (r *Repository) CreateShipment(
	ctx context.Context,
	merchantOrderID, carrier, trackingRef, operatorUserID string,
	expectedUnitIDs []string,
	now time.Time,
) (domain.Shipment, error) {
	var created domain.Shipment
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		if err := procmsg.RequireExecution(tx, merchantOrderID, procmsg.EffectApplyOwnerAction); err != nil {
			return err
		}
		scope, _ := procmsg.ExecutionFrom(tx)
		agencyOrderID := scope.AgencyOrderID

		allocableFilter := ""
		arguments := []any{merchantOrderID}
		if len(expectedUnitIDs) > 0 {
			allocableFilter = " AND expected.id = ANY($2::uuid[])"
			arguments = append(arguments, expectedUnitIDs)
		}
		rows, err := r.database.Queryer(tx).QueryContext(tx, `
			SELECT expected.id
			FROM logistics_expected_units expected
			WHERE expected.merchant_order_id=$1
			  AND expected.fulfillment='AWAITING_EFFECT'
			  AND NOT EXISTS (
			      SELECT 1 FROM logistics_shipment_allocations allocation
			      WHERE allocation.expected_unit_id=expected.id AND allocation.active
			  )`+allocableFilter+`
			ORDER BY expected.line_id, expected.unit_index
			FOR UPDATE OF expected
		`, arguments...)
		if err != nil {
			return fmt.Errorf("select allocable units: %w", err)
		}
		targetUnitIDs := []string{}
		for rows.Next() {
			var unitID string
			if err := rows.Scan(&unitID); err != nil {
				rows.Close()
				return fmt.Errorf("scan allocable unit: %w", err)
			}
			targetUnitIDs = append(targetUnitIDs, unitID)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(targetUnitIDs) == 0 ||
			(len(expectedUnitIDs) > 0 && len(targetUnitIDs) != len(expectedUnitIDs)) {
			return domain.ErrUnitsNotAllocable
		}

		shipmentSeed := strings.ToUpper(carrier) + ":" + trackingRef
		row := r.database.Queryer(tx).QueryRowContext(tx, `
			INSERT INTO logistics_shipments(
				id, agency_order_id, merchant_order_id, carrier, tracking_ref,
				state, created_by_user_id, version, created_at, updated_at
			)
			VALUES (md5('shipment:'||$1)::uuid, $2, $3, $4, $5, 'CREATED', $6, 1, $7, $7)
			RETURNING `+strings.ReplaceAll(shipmentColumnsSQL, "shipment.", "")+`
		`, shipmentSeed, agencyOrderID, merchantOrderID, carrier, trackingRef,
			operatorUserID, now)
		created, err = scanShipment(row)
		if isUniqueViolation(err) {
			return domain.ErrEvidenceDuplicate
		}
		if err != nil {
			return fmt.Errorf("insert shipment: %w", err)
		}

		if _, err := r.database.Queryer(tx).ExecContext(tx, `
			INSERT INTO logistics_shipment_allocations(id, shipment_id, expected_unit_id, active, created_at)
			SELECT md5($1||':'||unit_id::text)::uuid, $1::uuid, unit_id, TRUE, $2
			FROM unnest($3::uuid[]) AS unit_id
		`, created.ID, now, targetUnitIDs); err != nil {
			return fmt.Errorf("insert allocations: %w", err)
		}
		if _, err := r.database.Queryer(tx).ExecContext(tx, `
			UPDATE logistics_expected_units
			SET fulfillment='IN_TRANSIT_EXPECTED', version=version+1, updated_at=$2
			WHERE id = ANY($1::uuid[])
		`, targetUnitIDs, now); err != nil {
			return fmt.Errorf("mark units in transit: %w", err)
		}
		for _, unitID := range targetUnitIDs {
			if err := emitUnitEvent(tx, r.database.Queryer(tx), unitID, now); err != nil {
				return err
			}
		}
		if err := emitShipmentEvent(tx, r.database.Queryer(tx), created.ID, now); err != nil {
			return err
		}
		return r.appendEvent(tx, created.ID, "CREATED",
			"shipment registered", operatorUserID, now, now)
	})
	if err != nil {
		return domain.Shipment{}, err
	}
	return created, nil
}

// appendEvent는 append-only evidence 저장이다. dedupe_hash 충돌(같은 사실의
// 재입력)은 멱등 무시된다.
func (r *Repository) appendEvent(
	tx context.Context,
	shipmentID, status, note, operatorUserID string,
	occurredAt, now time.Time,
) error {
	hash := sha256.Sum256([]byte(status + "|" + occurredAt.UTC().Format(time.RFC3339) + "|" + note))
	_, err := r.database.Queryer(tx).ExecContext(tx, `
		INSERT INTO logistics_shipment_events(
			id, shipment_id, status, note, occurred_at, recorded_by_user_id, dedupe_hash, created_at
		)
		VALUES (md5($1||':'||$6)::uuid, $1::uuid, $2, NULLIF($3,''), $4, NULLIF($5,'')::uuid, $6, $7)
		ON CONFLICT (shipment_id, dedupe_hash) DO NOTHING
	`, shipmentID, status, note, occurredAt, operatorUserID, hex.EncodeToString(hash[:]), now)
	if err != nil {
		return fmt.Errorf("append shipment event: %w", err)
	}
	return nil
}

func (r *Repository) lockShipment(tx context.Context, shipmentID string) (domain.Shipment, error) {
	row := r.database.Queryer(tx).QueryRowContext(tx, `
		SELECT `+shipmentColumnsSQL+`
		FROM logistics_shipments shipment WHERE shipment.id=$1 FOR UPDATE
	`, shipmentID)
	shipment, err := scanShipment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Shipment{}, domain.ErrShipmentNotFound
	}
	if err != nil {
		return domain.Shipment{}, fmt.Errorf("lock shipment: %w", err)
	}
	return shipment, nil
}

// RecordEvent는 tracking evidence를 무조건 append하고, status가 §9.2 상태이며
// 허용 전이일 때만 projection을 옮긴다(carrier 이력은 단조롭지 않을 수 있음).
func (r *Repository) RecordEvent(
	ctx context.Context,
	shipmentID, status, note, operatorUserID string,
	occurredAt, now time.Time,
) (domain.Shipment, error) {
	var result domain.Shipment
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		shipment, err := r.lockShipment(tx, shipmentID)
		if err != nil {
			return err
		}
		if err := r.appendEvent(tx, shipmentID, status, note, operatorUserID, occurredAt, now); err != nil {
			return err
		}
		result = shipment
		target := domain.ShipmentState(status)
		if !isShipmentState(target) || target == shipment.State ||
			!domain.AllowedTransition(shipment.State, target) {
			return nil
		}
		result, err = r.projectShipmentState(tx, shipment, target, now)
		return err
	})
	if err != nil {
		return domain.Shipment{}, err
	}
	return result, nil
}

func isShipmentState(state domain.ShipmentState) bool {
	switch state {
	case domain.ShipmentCreated, domain.ShipmentLabelCreated, domain.ShipmentInTransit,
		domain.ShipmentOutForDelivery, domain.ShipmentDelivered, domain.ShipmentException,
		domain.ShipmentLost, domain.ShipmentReturnToSender, domain.ShipmentReturned,
		domain.ShipmentCancelledNoOp, domain.ShipmentExceptionRecon:
		return true
	}
	return false
}

func (r *Repository) projectShipmentState(
	tx context.Context,
	shipment domain.Shipment,
	target domain.ShipmentState,
	now time.Time,
) (domain.Shipment, error) {
	if _, err := r.database.Queryer(tx).ExecContext(tx, `
		UPDATE logistics_shipments SET state=$2, version=version+1, updated_at=$3
		WHERE id=$1
	`, shipment.ID, string(target), now); err != nil {
		return domain.Shipment{}, fmt.Errorf("project shipment state: %w", err)
	}
	if err := emitShipmentEvent(tx, r.database.Queryer(tx), shipment.ID, now); err != nil {
		return domain.Shipment{}, err
	}
	// 패키지 결말이 unit 기대를 확정하는 경우만 파생한다. delivered는 명시적
	// 일괄 확인(ConfirmDelivered)이 담당한다.
	unitFulfillment := ""
	switch target {
	case domain.ShipmentLost:
		unitFulfillment = string(domain.FulfillmentLost)
	case domain.ShipmentReturned:
		unitFulfillment = string(domain.FulfillmentReturned)
	}
	if unitFulfillment != "" {
		q := r.database.Queryer(tx)
		rows, err := q.QueryContext(tx, `
			UPDATE logistics_expected_units expected
			SET fulfillment=$2, version=expected.version+1, updated_at=$3
			FROM logistics_shipment_allocations allocation
			WHERE allocation.expected_unit_id=expected.id
			  AND allocation.shipment_id=$1 AND allocation.active
			  AND expected.fulfillment IN ('AWAITING_EFFECT','IN_TRANSIT_EXPECTED','SUPERSEDED_BY_CANCELLATION')
			RETURNING expected.id::text
		`, shipment.ID, unitFulfillment, now)
		if err != nil {
			return domain.Shipment{}, fmt.Errorf("derive unit fulfillment: %w", err)
		}
		unitIDs := make([]string, 0)
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return domain.Shipment{}, err
			}
			unitIDs = append(unitIDs, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return domain.Shipment{}, err
		}
		for _, id := range unitIDs {
			if err := emitUnitEvent(tx, q, id, now); err != nil {
				return domain.Shipment{}, err
			}
		}
	}
	shipment.State = target
	shipment.Version++
	shipment.UpdatedAt = now
	return shipment, nil
}

// ConfirmDelivered는 수령의 명시적 확인이다: 패키지 DELIVERED + 배정 unit 일괄
// DELIVERED_EXPECTED, exceptions unit만 MISSING/WRONG_ACTUAL로 분기한다(§9.1 —
// resolution 처리는 Step 5B).
func (r *Repository) ConfirmDelivered(
	ctx context.Context,
	shipmentID, operatorUserID string,
	exceptions map[string]string,
	now time.Time,
) (domain.Shipment, error) {
	var result domain.Shipment
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		shipment, err := r.lockShipment(tx, shipmentID)
		if err != nil {
			return err
		}
		if !domain.AllowedTransition(shipment.State, domain.ShipmentDelivered) {
			return domain.ErrTransitionInvalid
		}
		exceptionIDs := make([]string, 0, len(exceptions))
		for unitID := range exceptions {
			exceptionIDs = append(exceptionIDs, unitID)
		}
		if len(exceptionIDs) > 0 {
			var allocatedCount int
			if err := r.database.Queryer(tx).QueryRowContext(tx, `
				SELECT count(*) FROM logistics_shipment_allocations
				WHERE shipment_id=$1 AND active AND expected_unit_id = ANY($2::uuid[])
			`, shipmentID, exceptionIDs).Scan(&allocatedCount); err != nil {
				return fmt.Errorf("count exception units: %w", err)
			}
			if allocatedCount != len(exceptionIDs) {
				return domain.ErrUnitsNotAllocable
			}
			for unitID, kind := range exceptions {
				if _, err := r.database.Queryer(tx).ExecContext(tx, `
					UPDATE logistics_expected_units
					SET fulfillment=$2, version=version+1, updated_at=$3
					WHERE id=$1 AND fulfillment IN ('AWAITING_EFFECT','IN_TRANSIT_EXPECTED','SUPERSEDED_BY_CANCELLATION')
				`, unitID, kind, now); err != nil {
					return fmt.Errorf("mark exception unit: %w", err)
				}
				if err := emitUnitEvent(tx, r.database.Queryer(tx), unitID, now); err != nil {
					return err
				}
			}
		}
		deliveredRows, err := r.database.Queryer(tx).QueryContext(tx, `
			UPDATE logistics_expected_units expected
			SET fulfillment='DELIVERED_EXPECTED', version=expected.version+1, updated_at=$2
			FROM logistics_shipment_allocations allocation
			WHERE allocation.expected_unit_id=expected.id
			  AND allocation.shipment_id=$1 AND allocation.active
			  AND expected.fulfillment IN ('AWAITING_EFFECT','IN_TRANSIT_EXPECTED','SUPERSEDED_BY_CANCELLATION')
			  AND NOT (expected.id = ANY($3::uuid[]))
			RETURNING expected.id::text
		`, shipmentID, now, exceptionIDs)
		if err != nil {
			return fmt.Errorf("confirm delivered units: %w", err)
		}
		deliveredIDs := make([]string, 0)
		for deliveredRows.Next() {
			var id string
			if err := deliveredRows.Scan(&id); err != nil {
				deliveredRows.Close()
				return err
			}
			deliveredIDs = append(deliveredIDs, id)
		}
		deliveredRows.Close()
		if err := deliveredRows.Err(); err != nil {
			return err
		}
		for _, id := range deliveredIDs {
			if err := emitUnitEvent(tx, r.database.Queryer(tx), id, now); err != nil {
				return err
			}
		}
		if err := r.appendEvent(tx, shipmentID, "DELIVERED",
			"operator delivered confirmation", operatorUserID, now, now); err != nil {
			return err
		}
		if shipment.State != domain.ShipmentDelivered {
			shipment, err = r.projectShipmentState(tx, shipment, domain.ShipmentDelivered, now)
			if err != nil {
				return err
			}
		}
		result = shipment
		return nil
	})
	if err != nil {
		return domain.Shipment{}, err
	}
	return result, nil
}
