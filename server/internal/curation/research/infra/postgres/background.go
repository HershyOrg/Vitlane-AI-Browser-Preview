package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	c "github.com/vitlane/vitlane/server/internal/curation/domain"
	a "github.com/vitlane/vitlane/server/internal/curation/research/app"
	d "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"regexp"
	"strings"
	"time"
)

func bgError(code string) error { return fault.New(fault.Conflict, code, false) }
func (r *Repository) AcceptSubscription(ctx context.Context, user, cid, pid string, action c.FollowUpAction) error {
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		var owner string
		if e := q.QueryRowContext(tx, `SELECT user_id FROM curations WHERE id=$1 AND user_id=$2 AND archived_at IS NULL FOR UPDATE`, cid, user).Scan(&owner); e != nil {
			return e
		}
		var storedJSON []byte
		if e := q.QueryRowContext(tx, `SELECT payload FROM curation_follow_ups WHERE id=$1 AND user_id=$2 AND curation_id=$3 AND status='ACCEPTED' AND payload->>'kind'='SUBSCRIBE_DEALS'`, pid, user, cid).Scan(&storedJSON); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return bgError("RESEARCH_SUBSCRIPTION_PROPOSAL_REQUIRED")
			}
			return e
		}
		var stored c.FollowUpAction
		if json.Unmarshal(storedJSON, &stored) != nil || stored.Subscription == nil || action.Subscription == nil || stored.TargetID != action.TargetID ||
			d.TermsHash(*stored.Subscription) != d.TermsHash(*action.Subscription) || !stored.Subscription.ExpiresAt.Equal(action.Subscription.ExpiresAt) {
			return bgError("RESEARCH_SUBSCRIPTION_PROPOSAL_REQUIRED")
		}
		if _, e := q.ExecContext(tx, `UPDATE research_subscriptions s SET status=CASE WHEN t.removed_at IS NOT NULL THEN 'TARGET_REMOVED' ELSE 'EXPIRED' END FROM plan_targets t WHERE s.curation_id=$1 AND t.id=s.target_id AND s.status='ACTIVE' AND (s.expires_at<=now() OR t.removed_at IS NOT NULL)`, cid); e != nil {
			return e
		}
		var count int
		if e := q.QueryRowContext(tx, `SELECT count(*) FROM research_subscriptions WHERE curation_id=$1 AND status='ACTIVE'`, cid).Scan(&count); e != nil {
			return e
		}
		if count >= 3 {
			return bgError("RESEARCH_SUBSCRIPTION_LIMIT")
		}
		var active bool
		if e := q.QueryRowContext(tx, `SELECT EXISTS(SELECT 1 FROM plan_targets WHERE id=$1 AND curation_id=$2 AND user_id=$3 AND removed_at IS NULL)`, action.TargetID, cid, user).Scan(&active); e != nil {
			return e
		}
		if !active {
			return bgError("RESEARCH_SUBSCRIPTION_TARGET_REMOVED")
		}
		b, _ := json.Marshal(action.Subscription)
		_, e := q.ExecContext(tx, `INSERT INTO research_subscriptions(user_id,curation_id,target_id,proposal_id,terms,terms_hash,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, user, cid, action.TargetID, pid, b, d.TermsHash(*action.Subscription), action.Subscription.ExpiresAt)
		return e
	})
}
func (r *Repository) SubscriptionChoices(ctx context.Context, user, cid, locale string, countries []string) ([]c.FollowUp, error) {
	out := []c.FollowUp{}
	rows, e := r.database.Queryer(ctx).QueryContext(ctx, `SELECT t.id,tc.criteria,COALESCE(c.research_country,p.country),b.currency,
 CASE WHEN b.enabled THEN (SELECT floor((x->>'amount')::numeric / greatest(1,(x->>'quantity')::int) * CASE WHEN b.currency='USD' THEN 100 ELSE 1 END)::bigint FROM jsonb_array_elements(b.allocations)x WHERE x->>'targetId'=t.id::text) END,
 curation_follow_up_context(c.id,t.id)
 FROM curations c JOIN shopping_plans p ON p.id=c.shopping_plan_id JOIN plan_targets t ON t.curation_id=c.id
 JOIN curation_target_criteria tc ON tc.target_id=t.id JOIN curation_budgets b ON b.curation_id=c.id
 WHERE c.id=$1 AND c.user_id=$2 AND c.archived_at IS NULL AND t.removed_at IS NULL
 AND COALESCE(c.research_country,p.country)=ANY($3::text[])
 AND (SELECT count(*) FROM research_subscriptions WHERE curation_id=c.id AND status='ACTIVE' AND expires_at>now())<3
 AND NOT EXISTS(SELECT 1 FROM research_subscriptions WHERE target_id=t.id AND status='ACTIVE' AND expires_at>now())
 AND NOT EXISTS(SELECT 1 FROM curation_follow_ups f WHERE f.curation_id=c.id AND f.status='DISMISSED' AND f.payload->>'kind'='SUBSCRIBE_DEALS' AND f.payload->>'targetId'=t.id::text AND f.fingerprint=curation_follow_up_context(c.id,t.id))
 ORDER BY t.order_index LIMIT 3`, cid, user, countries)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var target, country, currency, fp string
		var criteria []byte
		var max sql.NullInt64
		if e = rows.Scan(&target, &criteria, &country, &currency, &max, &fp); e != nil {
			return nil, e
		}
		var criteriaSet c.TargetCriteriaSetV1
		if e = json.Unmarshal(criteria, &criteriaSet); e != nil {
			return nil, e
		}
		terms := c.SubscriptionTerms{SchemaVersion: "vitlane.research-subscription-terms.v1", Criteria: criteriaSet, Country: country, Currency: currency, Keywords: []string{criteriaSet.Subject.ProductType}, ExpiresAt: time.Now().UTC().Add(7 * 24 * time.Hour).Truncate(time.Minute)}
		if max.Valid {
			v := max.Int64
			terms.MaximumMinor = &v
		}
		body := fmt.Sprintf("%s까지 ‘%s’ 조건에 맞는 새 핫딜을 받아볼까요? 현재 조건으로 구독하고, 발견한 상품은 따로 보여드릴게요.", terms.ExpiresAt.Format("2006-01-02"), criteriaSet.Subject.Label)
		if locale == "en-US" {
			body = fmt.Sprintf("Watch for new deals matching %s until %s? I'll keep these conditions and show discoveries separately.", criteriaSet.Subject.Label, terms.ExpiresAt.Format("2006-01-02"))
		}
		out = append(out, c.FollowUp{Kind: "PROPOSAL", Content: c.FollowUpContent{Code: "SUBSCRIBE_DEALS", Body: body, Locale: locale, TargetTitle: criteriaSet.Subject.Label}, Payload: &c.FollowUpAction{Kind: "SUBSCRIBE_DEALS", TargetID: target, CriteriaVersion: criteriaSet.Version, Subscription: &terms}, Fingerprint: fp})
	}
	return out, rows.Err()
}
func (r *Repository) ExpireSubscriptions(ctx context.Context) error {
	_, e := r.database.Queryer(ctx).ExecContext(ctx, `UPDATE research_subscriptions s SET status=CASE WHEN c.archived_at IS NOT NULL THEN 'ARCHIVED' WHEN t.removed_at IS NOT NULL THEN 'TARGET_REMOVED' ELSE 'EXPIRED' END
 FROM curations c,plan_targets t WHERE c.id=s.curation_id AND t.id=s.target_id AND s.status='ACTIVE'
 AND (s.expires_at<=now() OR c.archived_at IS NOT NULL OR t.removed_at IS NOT NULL)`)
	if e != nil {
		return e
	}
	_, e = r.database.Queryer(ctx).ExecContext(ctx, `DELETE FROM research_feed_products WHERE id IN (SELECT id FROM research_feed_products WHERE expires_at<now()-interval '7 days' LIMIT 500)`)
	if e != nil {
		return e
	}
	_, e = r.database.Queryer(ctx).ExecContext(ctx, `DELETE FROM research_feed_links WHERE id IN (SELECT id FROM research_feed_links WHERE expires_at<now()-interval '7 days' AND (lease_until IS NULL OR lease_until<now()) LIMIT 500)`)
	if e != nil {
		return e
	}
	_, e = r.database.Queryer(ctx).ExecContext(ctx, `DELETE FROM curation_visible_leases WHERE visible_until<now()-interval '1 day'`)
	if e != nil {
		return e
	}
	_, e = r.database.Queryer(ctx).ExecContext(ctx, `UPDATE research_match_jobs SET status='FAILED',reason='ATTEMPTS_EXHAUSTED' WHERE status='RUNNING' AND lease_until<now() AND attempts>=3`)
	if e != nil {
		return e
	}
	_, e = r.database.Queryer(ctx).ExecContext(ctx, `DELETE FROM research_match_recipients mr USING research_subscriptions s WHERE s.id=mr.subscription_id AND s.status<>'ACTIVE'`)
	return e
}
func (r *Repository) BackgroundView(ctx context.Context, user, cid string) (a.BackgroundView, error) {
	out := a.BackgroundView{SchemaVersion: "vitlane.background-research.v1", Subscriptions: []d.ResearchSubscription{}, Findings: []d.ResearchFinding{}}
	var owner string
	if e := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT user_id FROM curations WHERE id=$1 AND user_id=$2`, cid, user).Scan(&owner); e != nil {
		return out, e
	}
	rows, e := r.database.Queryer(ctx).QueryContext(ctx, `SELECT id,curation_id,target_id,CASE WHEN status='ACTIVE' AND expires_at<=now() THEN 'EXPIRED' ELSE status END,terms,created_at FROM research_subscriptions WHERE user_id=$1 AND curation_id=$2 ORDER BY created_at DESC LIMIT 50`, user, cid)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var s d.ResearchSubscription
		var b []byte
		if e = rows.Scan(&s.ID, &s.CurationID, &s.TargetID, &s.Status, &b, &s.CreatedAt); e != nil {
			rows.Close()
			return out, e
		}
		if e = json.Unmarshal(b, &s.Terms); e != nil {
			rows.Close()
			return out, e
		}
		out.Subscriptions = append(out.Subscriptions, s)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	rows, e = r.database.Queryer(ctx).QueryContext(ctx, `SELECT id,subscription_id,target_id,status,product,reason,created_at,candidate_id FROM research_findings WHERE user_id=$1 AND curation_id=$2 AND status<>'HIDDEN' ORDER BY created_at DESC,id DESC LIMIT 100`, user, cid)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var f d.ResearchFinding
		var b []byte
		if e = rows.Scan(&f.ID, &f.SubscriptionID, &f.TargetID, &f.Status, &b, &f.Reason, &f.CreatedAt, &f.CandidateID); e != nil {
			return out, e
		}
		if e = json.Unmarshal(b, &f.Product); e != nil {
			return out, e
		}
		out.Findings = append(out.Findings, f)
	}
	return out, rows.Err()
}
func (r *Repository) CancelSubscription(ctx context.Context, user, cid, id string) error {
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		var owner string
		if e := q.QueryRowContext(tx, `SELECT user_id FROM curations WHERE id=$1 AND user_id=$2 FOR UPDATE`, cid, user).Scan(&owner); e != nil {
			return e
		}
		res, e := q.ExecContext(tx, `UPDATE research_subscriptions SET status=CASE WHEN status='ACTIVE' THEN 'CANCELLED' ELSE status END WHERE id=$1 AND curation_id=$2 AND user_id=$3`, id, cid, user)
		if e != nil {
			return e
		}
		n, e := res.RowsAffected()
		if n != 1 {
			return bgError("RESEARCH_SUBSCRIPTION_NOT_FOUND")
		}
		return e
	})
}
func (r *Repository) HideFinding(ctx context.Context, user, cid, id string) error {
	res, e := r.database.Queryer(ctx).ExecContext(ctx, `UPDATE research_findings SET status=CASE WHEN status='NEW' THEN 'HIDDEN' ELSE status END WHERE id=$1 AND curation_id=$2 AND user_id=$3`, id, cid, user)
	if e != nil {
		return e
	}
	n, e := res.RowsAffected()
	if n != 1 {
		return bgError("RESEARCH_FINDING_NOT_FOUND")
	}
	return e
}
func (r *Repository) NoticeSync(ctx context.Context, user, client, cid string, visible bool) ([]string, error) {
	ids := []string{}
	e := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		if visible && cid != "" {
			var owner string
			if e := q.QueryRowContext(tx, `SELECT user_id FROM curations WHERE id=$1 AND user_id=$2 AND archived_at IS NULL FOR UPDATE`, cid, user).Scan(&owner); e != nil {
				return e
			}
			if _, e := q.ExecContext(tx, `INSERT INTO curation_visible_leases(user_id,client_id,curation_id,visible_until) VALUES($1,$2,$3,now()+interval '90 seconds') ON CONFLICT(user_id,client_id) DO UPDATE SET curation_id=$3,visible_until=EXCLUDED.visible_until`, user, client, cid); e != nil {
				return e
			}
			if _, e := q.ExecContext(tx, `UPDATE curation_product_notices SET new_products=false WHERE curation_id=$1`, cid); e != nil {
				return e
			}
		} else {
			if _, e := q.ExecContext(tx, `DELETE FROM curation_visible_leases WHERE user_id=$1 AND client_id=$2`, user, client); e != nil {
				return e
			}
		}
		rows, e := q.QueryContext(tx, `SELECT n.curation_id FROM curation_product_notices n JOIN curations c ON c.id=n.curation_id WHERE c.user_id=$1 AND c.archived_at IS NULL AND n.new_products AND NOT EXISTS(SELECT 1 FROM curation_visible_leases v WHERE v.curation_id=c.id AND v.visible_until>now()) ORDER BY c.created_at DESC LIMIT 1000`, user)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				return e
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	return ids, e
}
func (r *Repository) ClaimBackgroundTask(ctx context.Context, name string, duration time.Duration) (bool, error) {
	res, e := r.database.Queryer(ctx).ExecContext(ctx, `UPDATE research_feed_leases SET next_at=now()+make_interval(secs=>$2) WHERE name=$1 AND next_at<=now()`, name, int(duration.Seconds()))
	if e != nil {
		return false, e
	}
	n, e := res.RowsAffected()
	return n == 1, e
}
func (r *Repository) IngestDeals(ctx context.Context, products []d.DealProduct) error {
	return r.database.WithinTransaction(ctx, func(tx context.Context) error { return r.ingestDeals(tx, products) })
}
func (r *Repository) ingestDeals(ctx context.Context, products []d.DealProduct) error {
	if len(products) > 100 {
		return bgError("RESEARCH_FEED_BATCH_TOO_LARGE")
	}
	for _, p := range products {
		if !p.Valid(time.Now()) {
			continue
		}
		b, _ := json.Marshal(p)
		// Repeat feed delivery/price refresh does not reissue a discovery or a dot.
		if _, e := r.database.Queryer(ctx).ExecContext(ctx, `INSERT INTO research_feed_products(provider,external_id,identity,product,country,expires_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(provider,external_id) DO UPDATE SET identity=EXCLUDED.identity,product=EXCLUDED.product
 WHERE research_feed_products.product->'productRef' IS NULL AND EXCLUDED.product->'productRef' IS NOT NULL`, p.Provider, p.ExternalID, p.Identity, b, p.Country, p.ExpiresAt); e != nil {
			return e
		}
		if p.ImageURL != "" {
			// A preview can become available after first delivery. Enrich only the
			// image: saved prices, delivery time, matching and user choices stay put.
			for _, query := range []string{
				`UPDATE research_feed_products SET product=jsonb_set(product,'{imageUrl}',to_jsonb($3::text)) WHERE provider=$1 AND external_id=$2 AND product->>'imageUrl' IS DISTINCT FROM $3`,
				`UPDATE research_findings SET product=jsonb_set(product,'{imageUrl}',to_jsonb($3::text)) WHERE product->>'provider'=$1 AND product->>'externalId'=$2 AND product->>'imageUrl' IS DISTINCT FROM $3`,
			} {
				if _, e := r.database.Queryer(ctx).ExecContext(ctx, query, p.Provider, p.ExternalID, p.ImageURL); e != nil {
					return e
				}
			}
		}
		if p.ProductRef != nil {
			_, e := r.database.Queryer(ctx).ExecContext(ctx, `UPDATE research_findings f SET product=$3,identity=$4
            WHERE f.product->>'provider'=$1 AND f.product->>'externalId'=$2 AND f.product->'productRef' IS NULL
            AND NOT EXISTS(SELECT 1 FROM research_findings other WHERE other.curation_id=f.curation_id AND other.identity=$4 AND other.id<>f.id)`, p.Provider, p.ExternalID, b, p.Identity)
			if e != nil {
				return e
			}
		}
	}
	return nil
}
func (r *Repository) ClassificationBatch(ctx context.Context) (a.TaxonomySnapshot, error) {
	var s a.TaxonomySnapshot
	var b []byte
	e := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT version,categories FROM research_taxonomy WHERE singleton`).Scan(&s.Version, &b)
	if e != nil {
		return s, e
	}
	if e = json.Unmarshal(b, &s.Categories); e != nil {
		return s, e
	}
	rows, e := r.database.Queryer(ctx).QueryContext(ctx, `SELECT id::text,'SUBSCRIPTION',terms->'criteria'->'subject'->>'label'||' '||(terms->'criteria')::text FROM research_subscriptions WHERE status='ACTIVE' AND expires_at>now() AND taxonomy_version<>$1
 UNION ALL SELECT id::text,'PRODUCT',product->>'title'||' '||COALESCE(product->>'description','') FROM research_feed_products WHERE expires_at>now() AND taxonomy_version<>$1 LIMIT 32`, s.Version)
	if e != nil {
		return s, e
	}
	defer rows.Close()
	for rows.Next() {
		var it a.ClassificationItem
		if e = rows.Scan(&it.ID, &it.Kind, &it.Text); e != nil {
			return s, e
		}
		s.Items = append(s.Items, it)
	}
	return s, rows.Err()
}

var categoryID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func validCategories(cats []d.ResearchCategory) bool {
	if len(cats) == 0 || len(cats) > 64 {
		return false
	}
	seen := map[string]bool{}
	for _, c := range cats {
		if !categoryID.MatchString(c.ID) || seen[c.ID] || strings.TrimSpace(c.Label) == "" || len(c.Label) > 120 || strings.TrimSpace(c.Definition) == "" || len(c.Definition) > 600 {
			return false
		}
		seen[c.ID] = true
	}
	return seen["general"]
}
func (r *Repository) SaveClassifications(ctx context.Context, s a.TaxonomySnapshot, added []d.ResearchCategory, items []a.Classification) error {
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		var version int64
		var b []byte
		if e := q.QueryRowContext(tx, `SELECT version,categories FROM research_taxonomy WHERE singleton FOR UPDATE`).Scan(&version, &b); e != nil {
			return e
		}
		if version != s.Version {
			return nil
		}
		var cats []d.ResearchCategory
		if e := json.Unmarshal(b, &cats); e != nil {
			return e
		}
		allowed := map[string]bool{}
		labels := map[string]string{}
		remap := map[string]string{}
		for _, c := range cats {
			allowed[c.ID] = true
			labels[strings.ToLower(c.Label)] = c.ID
		}
		for _, cat := range added {
			if allowed[cat.ID] {
				continue
			}
			if id, ok := labels[strings.ToLower(cat.Label)]; ok {
				remap[cat.ID] = id
				continue
			}
			cats = append(cats, cat)
			allowed[cat.ID] = true
			labels[strings.ToLower(cat.Label)] = cat.ID
		}
		if !validCategories(cats) {
			return bgError("RESEARCH_TAXONOMY_INVALID")
		}
		// A new leaf changes routing for existing broad subscriptions too.
		if len(cats) != len(s.Categories) {
			version++
			if _, e := q.ExecContext(tx, `UPDATE research_feed_products SET route_after=NULL WHERE expires_at>now()`); e != nil {
				return e
			}
		}
		byID := map[string][]string{}
		for _, it := range items {
			if _, dup := byID[it.ID]; dup {
				return bgError("RESEARCH_CLASSIFICATION_INVALID")
			}
			ids := []string{}
			seen := map[string]bool{}
			for _, id := range it.Categories {
				if mapped, ok := remap[id]; ok {
					id = mapped
				}
				if !allowed[id] {
					return bgError("RESEARCH_CLASSIFICATION_INVALID")
				}
				if !seen[id] {
					ids = append(ids, id)
					seen[id] = true
				}
			}
			byID[it.ID] = ids
		}
		if len(byID) != len(s.Items) {
			return bgError("RESEARCH_CLASSIFICATION_INVALID")
		}
		for _, it := range s.Items {
			ids, ok := byID[it.ID]
			if !ok || len(ids) == 0 || len(ids) > 12 || (it.Kind == "PRODUCT" && len(ids) != 1) {
				return bgError("RESEARCH_CLASSIFICATION_INVALID")
			}
			query := `UPDATE research_feed_products SET categories=$2,taxonomy_version=$3 WHERE id=$1 AND taxonomy_version<>$3`
			if it.Kind == "SUBSCRIPTION" {
				if version != s.Version {
					continue
				}
				query = `UPDATE research_subscriptions SET categories=$2,taxonomy_version=$3 WHERE id=$1 AND taxonomy_version<>$3`
			}
			if _, e := q.ExecContext(tx, query, it.ID, ids, version); e != nil {
				return e
			}
		}
		b, _ = json.Marshal(cats)
		_, e := q.ExecContext(tx, `UPDATE research_taxonomy SET categories=$1,version=$2 WHERE singleton`, b, version)
		return e
	})
}
func (r *Repository) RouteDeals(ctx context.Context) error {
	// Category/country SQL routing is paged per deal. Equivalent subscriptions
	// converge to one semantic job before any AI call.
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		var version int64
		if e := q.QueryRowContext(tx, `SELECT version FROM research_taxonomy WHERE singleton FOR SHARE`).Scan(&version); e != nil {
			return e
		}
		// Refactoring pauses routing until both sides have the same new taxonomy.
		var waiting bool
		if e := q.QueryRowContext(tx, `SELECT EXISTS(SELECT 1 FROM research_subscriptions WHERE status='ACTIVE' AND expires_at>now() AND taxonomy_version<>$1)`, version).Scan(&waiting); e != nil || waiting {
			return e
		}
		var id string
		var after sql.NullString
		e := q.QueryRowContext(tx, `SELECT id,route_after::text FROM research_feed_products WHERE expires_at>now() AND taxonomy_version=$1 AND routed_version<>$1 ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1`, version).Scan(&id, &after)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		rows, e := q.QueryContext(tx, `SELECT s.id,s.terms_hash,s.terms FROM research_subscriptions s JOIN research_feed_products p ON p.id=$1
   WHERE s.status='ACTIVE' AND s.expires_at>now() AND s.created_at<=p.created_at AND (p.provider<>'TELEGRAM_JIRUM' OR s.created_at<=(p.product->>'observedAt')::timestamptz) AND s.taxonomy_version=$2
   AND (s.categories && p.categories OR 'general'=ANY(s.categories) OR 'general'=ANY(p.categories))
   AND s.terms->>'country'=p.country AND ($3::uuid IS NULL OR s.id>$3::uuid)
   ORDER BY s.id LIMIT 256`, id, version, nullableString(after.String))
		if e != nil {
			return e
		}
		type recipient struct {
			id, hash string
			terms    []byte
		}
		batch := []recipient{}
		for rows.Next() {
			var x recipient
			if e = rows.Scan(&x.id, &x.hash, &x.terms); e != nil {
				rows.Close()
				return e
			}
			batch = append(batch, x)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, x := range batch {
			var job string
			if e = q.QueryRowContext(tx, `INSERT INTO research_match_jobs(deal_id,terms_hash,terms) VALUES($1,$2,$3) ON CONFLICT(deal_id,terms_hash) DO UPDATE SET terms_hash=EXCLUDED.terms_hash RETURNING id`, id, x.hash, x.terms).Scan(&job); e != nil {
				return e
			}
			if _, e = q.ExecContext(tx, `INSERT INTO research_match_recipients(job_id,subscription_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, job, x.id); e != nil {
				return e
			}
		}
		if len(batch) == 256 {
			_, e = q.ExecContext(tx, `UPDATE research_feed_products SET route_after=$2 WHERE id=$1`, id, batch[len(batch)-1].id)
		} else {
			_, e = q.ExecContext(tx, `UPDATE research_feed_products SET routed_version=$2,route_after=NULL WHERE id=$1`, id, version)
		}
		return e
	})
}
func (r *Repository) ClaimMatches(ctx context.Context) ([]a.MatchJob, error) {
	out := []a.MatchJob{}
	rows, e := r.database.Queryer(ctx).QueryContext(ctx, `WITH picked AS (SELECT j.id FROM research_match_jobs j JOIN research_feed_products p ON p.id=j.deal_id
 WHERE p.expires_at>now() AND (j.status='PENDING' OR (j.status='RUNNING' AND j.lease_until<now())) AND j.attempts<3
 ORDER BY j.created_at,j.id FOR UPDATE OF j SKIP LOCKED LIMIT 24),
 claimed AS (UPDATE research_match_jobs j SET status='RUNNING',lease_until=now()+interval '90 seconds',token=gen_random_uuid(),attempts=attempts+1 FROM picked WHERE j.id=picked.id RETURNING j.*)
 SELECT j.id,j.token,p.product,j.terms FROM claimed j JOIN research_feed_products p ON p.id=j.deal_id`)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var j a.MatchJob
		var p, t []byte
		if e = rows.Scan(&j.ID, &j.Token, &p, &t); e != nil {
			return out, e
		}
		if e = json.Unmarshal(p, &j.Product); e != nil {
			return out, e
		}
		if e = json.Unmarshal(t, &j.Terms); e != nil {
			return out, e
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
func (r *Repository) FinishMatches(ctx context.Context, jobs []a.MatchJob, decisions []a.MatchDecision) error {
	byID := map[string]a.MatchDecision{}
	for _, v := range decisions {
		if _, ok := byID[v.ID]; ok || len(v.Reason) > 1600 {
			return bgError("RESEARCH_MATCH_INVALID")
		}
		byID[v.ID] = v
	}
	if len(byID) != len(jobs) {
		return bgError("RESEARCH_MATCH_INVALID")
	}
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		for _, j := range jobs {
			v, ok := byID[j.ID]
			if !ok {
				return bgError("RESEARCH_MATCH_INVALID")
			}
			status := "NO_MATCH"
			if v.Match && d.DealFilter(j.Product, j.Terms, time.Now()) {
				status = "MATCH"
			}
			if _, e := r.database.Queryer(tx).ExecContext(tx, `UPDATE research_match_jobs SET status=$3,reason=$4 WHERE id=$1 AND token=$2 AND status='RUNNING' AND lease_until>now()`, j.ID, j.Token, status, v.Reason); e != nil {
				return e
			}
		}
		return nil
	})
}
func (r *Repository) PublishFindings(ctx context.Context) error {
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		// Lock curations before subscriptions, the same order as acceptance/cancel/import.
		rows, e := q.QueryContext(tx, `SELECT DISTINCT s.curation_id FROM research_match_jobs j JOIN research_match_recipients mr ON mr.job_id=j.id JOIN research_subscriptions s ON s.id=mr.subscription_id WHERE j.status='MATCH' AND s.status='ACTIVE' ORDER BY s.curation_id LIMIT 100`)
		if e != nil {
			return e
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, cid := range ids {
			if _, e = q.ExecContext(tx, `SELECT 1 FROM curations WHERE id=$1 FOR UPDATE`, cid); e != nil {
				return e
			}
			_, e = q.ExecContext(tx, `INSERT INTO research_findings(user_id,curation_id,target_id,subscription_id,identity,product,reason)
   SELECT s.user_id,s.curation_id,s.target_id,s.id,p.identity,p.product,j.reason
   FROM research_match_jobs j JOIN research_match_recipients mr ON mr.job_id=j.id JOIN research_subscriptions s ON s.id=mr.subscription_id
   JOIN research_feed_products p ON p.id=j.deal_id JOIN curations c ON c.id=s.curation_id JOIN plan_targets t ON t.id=s.target_id
   WHERE s.curation_id=$1 AND j.status='MATCH' AND s.status='ACTIVE' AND s.expires_at>now() AND p.expires_at>now()
   AND c.archived_at IS NULL AND t.removed_at IS NULL
   AND NOT EXISTS(SELECT 1 FROM phase8_research_candidates pc WHERE pc.curation_id=s.curation_id AND pc.identity_key=p.identity)
   ORDER BY s.created_at,s.id ON CONFLICT(curation_id,identity) DO NOTHING`, cid)
			if e != nil {
				return e
			}
		}
		// Consumed deliveries are removed, while the job caches the shared decision.
		_, e = q.ExecContext(tx, `DELETE FROM research_match_recipients mr USING research_match_jobs j,research_subscriptions s WHERE mr.job_id=j.id AND mr.subscription_id=s.id AND (j.status='NO_MATCH' OR (j.status='MATCH' AND s.curation_id=ANY($1::uuid[])))`, ids)
		return e
	})
}
func (r *Repository) ReviewTaxonomy(ctx context.Context) (a.TaxonomySnapshot, error) {
	s, e := r.ClassificationBatch(ctx)
	if e != nil {
		return s, e
	}
	s.Items = nil
	rows, e := r.database.Queryer(ctx).QueryContext(ctx, `SELECT id::text,'PRODUCT',product->>'title' FROM research_feed_products WHERE expires_at>now() ORDER BY created_at DESC LIMIT 80`)
	if e != nil {
		return s, e
	}
	defer rows.Close()
	for rows.Next() {
		var it a.ClassificationItem
		if e = rows.Scan(&it.ID, &it.Kind, &it.Text); e != nil {
			return s, e
		}
		s.Items = append(s.Items, it)
	}
	return s, rows.Err()
}
func (r *Repository) PublishTaxonomy(ctx context.Context, s a.TaxonomySnapshot, cats []d.ResearchCategory) error {
	if !validCategories(cats) {
		return bgError("RESEARCH_TAXONOMY_INVALID")
	}
	old, _ := json.Marshal(s.Categories)
	b, _ := json.Marshal(cats)
	if string(old) == string(b) {
		return nil
	}
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		var version int64
		if e := q.QueryRowContext(tx, `SELECT version FROM research_taxonomy WHERE singleton FOR UPDATE`).Scan(&version); e != nil {
			return e
		}
		if version != s.Version {
			return nil
		}
		_, e := q.ExecContext(tx, `UPDATE research_taxonomy SET version=version+1,categories=$1,revised_at=now() WHERE singleton`, b)
		if e != nil {
			return e
		}
		_, e = q.ExecContext(tx, `UPDATE research_feed_products SET route_after=NULL WHERE expires_at>now()`)
		return e
	})
}

var _ a.BackgroundRepository = (*Repository)(nil)
var _ = fmt.Sprintf
