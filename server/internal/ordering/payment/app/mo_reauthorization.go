package app

import (
	"context"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
)

type MOReauthorizationPlan struct {
	Required                        bool
	Expired                         bool
	AuthorizationID                 string
	TargetPositionID                string
	ProviderEnvironment             string
	PayPalOrderID                   string
	PayeeMerchantID                 string
	PreviousProviderAuthorizationID string
	AuthorizedAmountMinor           int64
	RemainingCapturableMinor        int64
	Currency                        string
	OriginalAuthorizedAt            time.Time
	HonorRefreshedAt                time.Time
	ReauthorizationCount            int
	OperationID                     string
	OperationState                  domain.OperationState
	OperationIdempotencyKey         string
	OperationResourceID             string
}

type moReauthorizationRepository interface {
	PrepareMOReauthorization(
		context.Context, string, string, time.Time,
	) (MOReauthorizationPlan, error)
	MarkMOReauthorizationSent(
		context.Context, MOReauthorizationPlan, time.Time, time.Time,
	) error
	RecordMOReauthorizationOutcome(
		context.Context, MOReauthorizationPlan, domain.OperationState,
		string, int64, string, time.Time, string, time.Time,
	) error
}
