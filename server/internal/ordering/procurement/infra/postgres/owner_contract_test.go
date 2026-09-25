package postgres

import (
	"context"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	procurementapp "github.com/vitlane/vitlane/server/internal/ordering/procurement/app"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
	"testing"
)

// These tests isolate the Procurement Owner contract. Full command execution
// and real Payment/Logistics ports are tested separately by Workflow scenarios.
type ownerFundingTestResult struct{ PositionID, State string }
type ownerFundingTestActivator interface {
	ActivateMerchantOrderFunding(context.Context, string, string) (ownerFundingTestResult, error)
}
type ownerPurchasePGDriver struct {
	*procurementapp.Service
	db      *sharedpostgres.Database
	t       *testing.T
	funding ownerFundingTestActivator
}

func newOwnerPurchasePGDriver(t *testing.T, db *sharedpostgres.Database, s *procurementapp.Service) *ownerPurchasePGDriver {
	return &ownerPurchasePGDriver{Service: s, db: db, t: t}
}
func (s *ownerPurchasePGDriver) EnableMerchantOrderFunding(a ownerFundingTestActivator) {
	s.funding = a
}
func ownerTestAuthority(state string) procurementapp.PurchasePreparation {
	return procurementapp.PurchasePreparation{AuthorizationKind: "MANUAL_OPERATOR_PURCHASE", AuthorizationHash: "failed-funding-authorization", ExecutionProfileHash: failedFundingProfileHash, ExecutionMode: "SIMULATED_NO_EFFECT", FundingPositionID: failedFundingPositionID, FundingState: state}
}
func ownerContractScope(t *testing.T, ctx context.Context, db *sharedpostgres.Database, mo, actor, key, action string) context.Context {
	t.Helper()
	return procmsg.WithExecutionScope(ctx, procmsg.ExecutionScope{AgencyOrderID: failedFundingOrderID, MerchantOrderID: mo, FlowID: procmsg.EffectIdentity(failedFundingOrderID, "test-flow:"+key), EffectID: procmsg.EffectIdentity(failedFundingOrderID, "test-effect:"+key), ClaimVersion: 1, Action: action})
}
func (s *ownerPurchasePGDriver) BeginMerchantEffect(ctx context.Context, task, actor, key string) (procurementapp.QueueItem, bool, error) {
	// The isolated test driver derives its fake authority and assertions from the
	// read model; production Workflow execution only reads Owner-owned subjects.
	item, e := NewRepository(s.db).GetQueueItem(ctx, task)
	if e != nil {
		return item, false, e
	}
	a := ownerTestAuthority(item.Funding.State)
	// Freeze the actual immutable authorization used by the fixture.
	if e = s.db.DB.QueryRowContext(ctx, `SELECT authorization_hash FROM agency_order_procurement_authorizations WHERE agency_order_id=$1`, failedFundingOrderID).Scan(&a.AuthorizationHash); e != nil {
		return item, false, e
	}
	preparedCtx := ownerContractScope(s.t, ctx, s.db, item.MerchantOrder.ID, actor, key, procmsg.EffectReservePurchase)
	// A fresh HTTP retry is an alias for the same unresolved process flow.
	var priorFlow string
	if err := s.db.DB.QueryRowContext(ctx, `SELECT COALESCE(process_flow_id::text,'') FROM procurement_effect_locks WHERE merchant_order_id=$1`, item.MerchantOrder.ID).Scan(&priorFlow); err == nil && priorFlow != "" {
		scope, _ := procmsg.ExecutionFrom(preparedCtx)
		scope.FlowID = priorFlow
		preparedCtx = procmsg.WithExecutionScope(preparedCtx, scope)
	}

	_, replay, e := s.PreparePurchase(preparedCtx, task, actor, key, a)
	if e != nil {
		return item, replay, e
	}
	f, fundingErr := s.funding.ActivateMerchantOrderFunding(ctx, item.MerchantOrder.ID, key)
	if fundingErr != nil && f.State != "FAILED" {
		return item, replay, fundingErr
	}
	a.FundingPositionID = f.PositionID
	a.FundingState = f.State
	scope, _ := procmsg.ExecutionFrom(preparedCtx)
	scope.Action = procmsg.EffectGrantMerchantPurchase
	item, _, e = s.AuthorizeMerchant(procmsg.WithExecutionScope(ctx, scope), task, actor, key, a)
	if e == nil {
		e = fundingErr
	}
	visible, readErr := NewRepository(s.db).GetQueueItem(ctx, task)
	if readErr == nil {
		item = visible
	} else if e == nil {
		e = readErr
	}
	return item, replay, e
}
