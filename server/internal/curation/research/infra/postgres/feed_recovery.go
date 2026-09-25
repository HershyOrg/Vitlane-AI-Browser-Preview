package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	d "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"time"
)

func (r *Repository) FeedCheckpoint(ctx context.Context, provider string) (d.FeedCheckpoint, error) {
	var s d.FeedCheckpoint
	err := r.database.Queryer(ctx).QueryRowContext(ctx, "SELECT version,last_id,head_id,before_id FROM research_feed_checkpoints WHERE provider=$1", provider).Scan(&s.Version, &s.LastID, &s.HeadID, &s.BeforeID)
	return s, err
}
func (r *Repository) CommitFeedBatch(ctx context.Context, provider string, b d.FeedBatch) error {
	if provider != "TELEGRAM_JIRUM" || len(b.Products) > 100 || len(b.Links) > 100 || b.Checkpoint.LastID < 0 || b.Checkpoint.HeadID < 0 || b.Checkpoint.BeforeID < 0 {
		return bgError("RESEARCH_FEED_BATCH_INVALID")
	}
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		if _, err := q.ExecContext(tx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "research-feed:TELEGRAM_JIRUM"); err != nil {
			return err
		}
		var version, last int64
		if err := q.QueryRowContext(tx, "SELECT version,last_id FROM research_feed_checkpoints WHERE provider=$1 FOR UPDATE", provider).Scan(&version, &last); err != nil {
			return err
		}
		if version != b.Checkpoint.Version || b.Checkpoint.LastID < last {
			return bgError("RESEARCH_FEED_CHECKPOINT_STALE")
		}
		for _, p := range b.Products {
			if p.Provider != provider || !p.Valid(time.Now()) {
				return bgError("RESEARCH_FEED_BATCH_INVALID")
			}
			if raw := b.Links[p.ExternalID]; raw != "" && !d.FeedLinkURLAllowed(provider, raw) {
				return bgError("RESEARCH_FEED_LINK_INVALID")
			}
		}
		if err := r.IngestDeals(tx, b.Products); err != nil {
			return err
		}
		for _, p := range b.Products {
			raw := b.Links[p.ExternalID]
			if raw == "" {
				continue
			}
			if _, err := q.ExecContext(tx, "UPDATE research_feed_products SET link_url=$3 WHERE provider=$1 AND external_id=$2", provider, p.ExternalID, raw); err != nil {
				return err
			}
			if _, err := q.ExecContext(tx, `INSERT INTO research_feed_links(provider,url,expires_at) VALUES($1,$2,$3)
 ON CONFLICT(provider,url) DO UPDATE SET expires_at=GREATEST(research_feed_links.expires_at,EXCLUDED.expires_at)`, provider, raw, p.ExpiresAt); err != nil {
				return err
			}
			var refJSON []byte
			err := q.QueryRowContext(tx, "SELECT product_ref FROM research_feed_links WHERE provider=$1 AND url=$2 AND status='RESOLVED'", provider, raw).Scan(&refJSON)
			if err == sql.ErrNoRows {
				continue
			}
			if err != nil {
				return err
			}
			var ref d.SourceProductRef
			if err = json.Unmarshal(refJSON, &ref); err != nil {
				return err
			}
			if err = r.applyFeedLink(tx, provider, raw, ref); err != nil {
				return err
			}
		}
		_, err := q.ExecContext(tx, "UPDATE research_feed_checkpoints SET version=version+1,last_id=$2,head_id=$3,before_id=$4 WHERE provider=$1", provider, b.Checkpoint.LastID, b.Checkpoint.HeadID, b.Checkpoint.BeforeID)
		return err
	})
}
func (r *Repository) ClaimFeedLinks(ctx context.Context, provider string) ([]d.FeedLinkJob, error) {
	q := r.database.Queryer(ctx)
	if _, err := q.ExecContext(ctx, `UPDATE research_feed_links SET status=CASE WHEN expires_at<=now() THEN 'EXPIRED' ELSE 'FAILED' END
 WHERE provider=$1 AND status IN ('PENDING','RUNNING') AND (expires_at<=now() OR (attempts>=4 AND lease_until<now()))`, provider); err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `WITH picked AS (
 SELECT id FROM research_feed_links WHERE provider=$1 AND expires_at>now() AND attempts<4
 AND ((status='PENDING' AND next_at<=now()) OR (status='RUNNING' AND lease_until<now()))
 ORDER BY next_at,id FOR UPDATE SKIP LOCKED LIMIT 4)
 UPDATE research_feed_links j SET status='RUNNING',attempts=attempts+1,token=gen_random_uuid(),lease_until=now()+interval '90 seconds'
 FROM picked WHERE j.id=picked.id RETURNING j.id,j.token,j.provider,j.url,j.attempts,j.expires_at`, provider)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []d.FeedLinkJob{}
	for rows.Next() {
		var j d.FeedLinkJob
		if err = rows.Scan(&j.ID, &j.Token, &j.Provider, &j.URL, &j.Attempts, &j.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
func (r *Repository) FinishFeedLink(ctx context.Context, job d.FeedLinkJob, result d.FeedLinkResult) error {
	if result.ProductRef != nil && (result.ProductRef.Validate() != nil || result.ProductRef.Source != d.SourceCoupang) {
		return bgError("RESEARCH_FEED_LINK_INVALID")
	}
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		if _, err := q.ExecContext(tx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "research-feed:TELEGRAM_JIRUM"); err != nil {
			return err
		}
		var attempts int
		var expires time.Time
		var provider, raw string
		err := q.QueryRowContext(tx, "SELECT attempts,expires_at,provider,url FROM research_feed_links WHERE id=$1 AND token=$2 AND status='RUNNING' AND lease_until>now() FOR UPDATE", job.ID, job.Token).Scan(&attempts, &expires, &provider, &raw)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		if !result.Attempted {
			attempts = max(0, attempts-1)
		}
		status, next := d.FeedLinkNextAttempt(attempts, time.Now(), result)
		if !expires.After(time.Now()) {
			status = "EXPIRED"
		}
		var refJSON any
		if result.ProductRef != nil {
			b, _ := json.Marshal(result.ProductRef)
			refJSON = b
		}
		if _, err = q.ExecContext(tx, "UPDATE research_feed_links SET status=$3,attempts=$4,next_at=$5,product_ref=$6,reason=$7,lease_until=NULL WHERE id=$1 AND token=$2", job.ID, job.Token, status, attempts, next, refJSON, result.Reason); err != nil {
			return err
		}
		if status == "RESOLVED" {
			return r.applyFeedLink(tx, provider, raw, *result.ProductRef)
		}
		return nil
	})
}
func (r *Repository) applyFeedLink(ctx context.Context, provider, raw string, ref d.SourceProductRef) error {
	q := r.database.Queryer(ctx)
	refJSON, _ := json.Marshal(ref)
	address, err := ref.ExternalProductURL()
	if err != nil {
		return err
	}
	identity := ref.IdentityKey()
	// Update only identity/link fields; previously observed prices and timestamps remain intact.
	if _, err = q.ExecContext(ctx, `UPDATE research_feed_products SET identity=$3,
 product=jsonb_set(jsonb_set(jsonb_set(product,'{productRef}',$4::jsonb),'{identity}',to_jsonb($3::text)),'{url}',to_jsonb($5::text))
 WHERE provider=$1 AND link_url=$2`, provider, raw, identity, refJSON, address); err != nil {
		return err
	}
	rows, err := q.QueryContext(ctx, `SELECT id FROM curations WHERE id IN (
 SELECT f.curation_id FROM research_findings f JOIN research_feed_products p
 ON p.provider=f.product->>'provider' AND p.external_id=f.product->>'externalId'
 WHERE p.provider=$1 AND p.link_url=$2) ORDER BY id FOR UPDATE`, provider, raw)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// An unresolved finding cannot have been imported. Preserve its history but hide
	// the alias when the same canonical product was already delivered in this curation.
	if _, err = q.ExecContext(ctx, `UPDATE research_findings f SET status='HIDDEN'
 FROM research_feed_products p WHERE p.provider=$1 AND p.link_url=$2
 AND f.product->>'provider'=p.provider AND f.product->>'externalId'=p.external_id
 AND f.product->'productRef' IS NULL AND f.status='NEW'
 AND EXISTS(SELECT 1 FROM research_findings other WHERE other.curation_id=f.curation_id AND other.identity=$3 AND other.id<>f.id)`, provider, raw, identity); err != nil {
		return err
	}
	// Serialize each alias update to preserve the unique canonical identity even if
	// several unresolved posts in this batch refer to the same product.
	rows, err = q.QueryContext(ctx, `SELECT f.id FROM research_findings f JOIN research_feed_products p
 ON p.provider=f.product->>'provider' AND p.external_id=f.product->>'externalId'
 WHERE p.provider=$1 AND p.link_url=$2 AND f.product->'productRef' IS NULL ORDER BY f.created_at,f.id`, provider, raw)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = q.ExecContext(ctx, `UPDATE research_findings f SET
 status=CASE WHEN EXISTS(SELECT 1 FROM research_findings other WHERE other.curation_id=f.curation_id AND other.identity=$2 AND other.id<>f.id) THEN 'HIDDEN' ELSE f.status END
 WHERE f.id=$1 AND f.status='NEW'`, id, identity); err != nil {
			return err
		}
		if _, err = q.ExecContext(ctx, `UPDATE research_findings f SET identity=$2,
 product=jsonb_set(jsonb_set(jsonb_set(product,'{productRef}',$3::jsonb),'{identity}',to_jsonb($2::text)),'{url}',to_jsonb($4::text))
 WHERE f.id=$1 AND NOT EXISTS(SELECT 1 FROM research_findings other WHERE other.curation_id=f.curation_id AND other.identity=$2 AND other.id<>f.id)`, id, identity, refJSON, address); err != nil {
			return err
		}
	}
	return nil
}
