package postgres

import (
	"context"
	"github.com/vitlane/vitlane/server/internal/ordering/testfixture"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
	"testing"
	"time"
)

func openIsolated(t *testing.T, ctx context.Context) *sharedpostgres.Database {
	return testfixture.Open(t, ctx)
}

type fixedClock struct{ now time.Time }

func (c *fixedClock) Now() time.Time { return c.now }
