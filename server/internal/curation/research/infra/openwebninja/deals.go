package openwebninja

import (
	"context"
	d "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"net/url"
	"strings"
	"time"
)

func (g *Gateway) Name() string              { return "AMAZON" }
func (*Gateway) PollInterval() time.Duration { return 24 * time.Hour }
func (g *Gateway) Poll(ctx context.Context) ([]d.DealProduct, error) {
	// A confirmed exhausted window stays asleep until reset; do not refresh it every tick.
	saved, err := g.usage.ReadAmazonUsage(ctx)
	if err != nil {
		return nil, err
	}
	if saved.Quota != nil && saved.EstimatedRemaining != nil && *saved.EstimatedRemaining <= 0 && saved.Quota.ResetAt.After(time.Now()) {
		return nil, failure("AMAZON_QUOTA_EXHAUSTED")
	}
	if _, e := g.RefreshAmazonUsage(ctx); e != nil {
		return nil, e
	}
	call, e := g.usage.ReserveAmazonCall(ctx, "FEED", time.Now())
	if e != nil {
		return nil, e
	}
	var data struct {
		Deals []struct {
			ID    string    `json:"deal_id"`
			Title string    `json:"deal_title"`
			ASIN  string    `json:"product_asin"`
			State string    `json:"deal_state"`
			End   time.Time `json:"deal_ends_at"`
		} `json:"deals"`
	}
	e = g.get(ctx, "/realtime-amazon-data/deals-v2", url.Values{"country": {"US"}, "offset": {"0"}}, &data)
	outcome := "SUCCESS"
	if e != nil {
		outcome = safeReason(e)
	}
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := g.usage.CompleteAmazonCall(finish, call, outcome, time.Now()); err != nil {
		return nil, err
	}
	if e != nil {
		return nil, e
	}
	out := []d.DealProduct{}
	now := time.Now().UTC()
	for _, v := range data.Deals {
		if v.State != "AVAILABLE" {
			continue
		}
		ref := d.SourceProductRef{Source: d.SourceAmazon, Marketplace: "US", AnchorASIN: strings.ToUpper(v.ASIN)}
		if ref.Validate() != nil {
			continue
		}
		end := v.End
		if end.IsZero() || end.After(now.Add(48*time.Hour)) {
			end = now.Add(48 * time.Hour)
		}
		p := d.DealProduct{SchemaVersion: "vitlane.deal-product.v1", Provider: "AMAZON", ExternalID: v.ID, Identity: ref.IdentityKey(), ProductRef: &ref, Title: v.Title, URL: "https://www.amazon.com/dp/" + ref.AnchorASIN, Country: "US", Currency: "USD", ObservedAt: now, ExpiresAt: end}
		// The deals endpoint doesn't promise a price. Keep it unknown rather than
		// issuing one detail call for every subscriber or inventing a discount.
		if p.Valid(now) && len(out) < 100 {
			out = append(out, p)
		}
	}
	return out, nil
}
