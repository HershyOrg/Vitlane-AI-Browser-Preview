package app

import (
	"context"

	planningdomain "github.com/vitlane/vitlane/server/internal/curation/planning/domain"
)

// Repository owns persistence of the immutable ShoppingPlan and its
// idempotent creation reservation. Callers coordinate any downstream product
// records in the same ambient transaction before completing the reservation.
type Repository interface {
	CreateIdempotent(
		context.Context,
		planningdomain.ShoppingPlan,
		string,
		[]byte,
	) (planningdomain.ShoppingPlan, bool, error)
	CompleteCreation(context.Context, planningdomain.ShoppingPlan, string) error
	Get(context.Context, string, string, bool) (planningdomain.ShoppingPlan, error)
}
