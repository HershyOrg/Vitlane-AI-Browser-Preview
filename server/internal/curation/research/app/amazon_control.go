package app

import (
	"context"
	"time"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

// AmazonSourceControl is durable operator intent, independent of credentials and quota.
type AmazonSourceControl struct {
	Enabled   bool      `json:"enabled"`
	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updatedAt"`
}
type AmazonControlRepository interface {
	ReadAmazonControl(context.Context) (AmazonSourceControl, error)
	UpdateAmazonControl(context.Context, string, bool, int64, time.Time) (AmazonSourceControl, error)
}

func SetAmazonControl(ctx context.Context, repo AmazonControlRepository, configured bool, operator string, enabled bool, version int64) (AmazonSourceControl, error) {
	if operator == "" || version < 1 {
		return AmazonSourceControl{}, fault.New(fault.InvalidInput, "AMAZON_CONTROL_INVALID", false)
	}
	if enabled && !configured {
		return AmazonSourceControl{}, fault.New(fault.Conflict, "AMAZON_NOT_CONFIGURED", false)
	}
	return repo.UpdateAmazonControl(ctx, operator, enabled, version, time.Now().UTC())
}

// The same runtime switch gates search, detail and quota reads, including local stubs.
// Calls admitted before Off may finish; subsequent calls observe the durable switch.
type ControlledAmazonGateway struct {
	Gateway AmazonCatalogGateway
	Control AmazonControlRepository
}

func (g *ControlledAmazonGateway) check(ctx context.Context) error {
	control, err := g.Control.ReadAmazonControl(ctx)
	if err != nil {
		return err
	}
	if !control.Enabled {
		return fault.New(fault.ProviderUnavailable, "AMAZON_SOURCE_DISABLED", false)
	}
	return nil
}
func (g *ControlledAmazonGateway) SearchAmazon(ctx context.Context, in AmazonSearchRequest) (AmazonSearchResult, error) {
	if err := g.check(ctx); err != nil {
		return AmazonSearchResult{}, err
	}
	return g.Gateway.SearchAmazon(ctx, in)
}
func (g *ControlledAmazonGateway) LookupAmazon(ctx context.Context, ref researchdomain.SourceVariantRef) (AmazonProductDetail, error) {
	if err := g.check(ctx); err != nil {
		return AmazonProductDetail{}, err
	}
	return g.Gateway.LookupAmazon(ctx, ref)
}
func (g *ControlledAmazonGateway) RefreshAmazonUsage(ctx context.Context) (CatalogAPIUsage, error) {
	if err := g.check(ctx); err != nil {
		return CatalogAPIUsage{}, err
	}
	return g.Gateway.RefreshAmazonUsage(ctx)
}
func AmazonAdmissionUnavailable(reason string) bool {
	switch reason {
	case "AMAZON_SOURCE_DISABLED", "AMAZON_QUOTA_EXHAUSTED", "AMAZON_QUOTA_UNCONFIRMED", "AMAZON_RATE_LIMITED":
		return true
	default:
		return false
	}
}

func (g *ControlledAmazonGateway) DescribeDiscovery() DiscoveryDescriptor {
	return discoveryDescriptor(g.Gateway, EnglishDiscoveryDescriptor("AMAZON"))
}
