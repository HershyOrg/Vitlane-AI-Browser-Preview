package postgres

import (
	"context"
	"database/sql"
	"errors"
	"github.com/vitlane/vitlane/server/internal/ordering/logistics/domain"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
)

func (r *Repository) ActionScope(ctx context.Context, p procmsg.ActionRequest) (procmsg.ActionRequest, error) {
	var query string
	var id string
	switch p.Kind {
	case procmsg.RequestCreateShipment:
		query = `SELECT agency_order_id::text,merchant_order_id::text FROM logistics_expected_units WHERE merchant_order_id=$1 LIMIT 1`
		id = p.MerchantOrderID
	case procmsg.RequestShipmentEvent, procmsg.RequestConfirmDelivery:
		query = `SELECT agency_order_id::text,merchant_order_id::text FROM logistics_shipments WHERE id=$1`
		id = p.ReferenceID
	case procmsg.RequestResolveDelivery, procmsg.RequestCreateReturn:
		query = `SELECT agency_order_id::text,merchant_order_id::text FROM logistics_expected_units WHERE id=$1`
		id = p.ReferenceID
	case procmsg.RequestUpdateReturn:
		query = `SELECT u.agency_order_id::text,u.merchant_order_id::text FROM logistics_returns r JOIN logistics_expected_units u ON u.id=r.expected_unit_id WHERE r.id=$1`
		id = p.ReferenceID
	default:
		return p, procmsg.ErrRequestInvalid
	}
	err := r.database.Queryer(ctx).QueryRowContext(ctx, query, id).Scan(&p.AgencyOrderID, &p.MerchantOrderID)
	if errors.Is(err, sql.ErrNoRows) {
		err = domain.ErrUnitNotFound
	}
	return p, err
}
