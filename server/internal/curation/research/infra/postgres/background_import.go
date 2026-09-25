package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	a "github.com/vitlane/vitlane/server/internal/curation/research/app"
	"strings"
	"time"
)

func (r *Repository) LoadFindingImport(ctx context.Context, user, cid, id string) (a.FindingImportSnapshot, error) {
	var s a.FindingImportSnapshot
	var product, criteria []byte
	e := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT f.id,f.subscription_id,f.target_id,f.status,f.product,f.reason,f.created_at,f.candidate_id,tc.criteria,COALESCE(p.version,0),COALESCE(u.ui_locale,'ko-KR')
 FROM research_findings f JOIN curations c ON c.id=f.curation_id AND c.archived_at IS NULL
 JOIN plan_targets t ON t.id=f.target_id AND t.removed_at IS NULL JOIN curation_target_criteria tc ON tc.target_id=t.id
 LEFT JOIN phase8_research_pools p ON p.curation_id=c.id AND p.plan_target_id=t.id
 LEFT JOIN account_user_preferences u ON u.user_id=f.user_id WHERE f.user_id=$1 AND f.curation_id=$2 AND f.id=$3`, user, cid, id).Scan(&s.Finding.ID, &s.Finding.SubscriptionID, &s.Finding.TargetID, &s.Finding.Status, &product, &s.Finding.Reason, &s.Finding.CreatedAt, &s.Finding.CandidateID, &criteria, &s.PoolVersion, &s.Locale)
	if e != nil {
		return s, e
	}
	if e = json.Unmarshal(product, &s.Finding.Product); e != nil {
		return s, e
	}
	e = json.Unmarshal(criteria, &s.Criteria)
	return s, e
}
func (r *Repository) CompleteFindingImport(ctx context.Context, command a.CatalogCandidatePoolCommandV2, s a.FindingImportSnapshot, refs []a.CatalogCandidateReferenceV2) (string, error) {
	var candidate string
	e := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		var owner string
		if e := q.QueryRowContext(tx, `SELECT user_id FROM curations WHERE id=$1 AND user_id=$2 AND archived_at IS NULL FOR UPDATE`, command.CurationID, command.UserID).Scan(&owner); e != nil {
			return e
		}
		var version int64
		if e := q.QueryRowContext(tx, `SELECT version FROM curation_target_criteria WHERE target_id=$1 FOR SHARE`, command.TargetID).Scan(&version); e != nil {
			return e
		}
		if version != s.Criteria.Version {
			return bgError("FOLLOW_UP_CONTEXT_CHANGED")
		}
		var status string
		if e := q.QueryRowContext(tx, `SELECT status,candidate_id FROM research_findings WHERE id=$1 AND user_id=$2 FOR UPDATE`, s.Finding.ID, command.UserID).Scan(&status, &candidate); e != nil {
			return e
		}
		if status == "ADDED" {
			return nil
		}
		if status != "NEW" {
			return bgError("RESEARCH_FINDING_NOT_AVAILABLE")
		}
		// Existing identity (including hidden) is preserved verbatim.
		e := q.QueryRowContext(tx, `SELECT candidate_id FROM phase8_research_candidates WHERE curation_id=$1 AND plan_target_id=$2 AND identity_key=$3`, command.CurationID, command.TargetID, s.Finding.Product.Identity).Scan(&candidate)
		if e != nil && e != sql.ErrNoRows {
			return e
		}
		if e == sql.ErrNoRows {
			_, e = r.CompleteCatalogSearchV2(tx, command, refs, a.LiveCatalogReviewMetricsV2{PolicyVersion: "background-finding.v1", StartedAt: time.Now(), CompletedAt: time.Now(), AICallCount: 1, ExternalEffect: "READ_ONLY"})
			if e != nil {
				return e
			}
			candidate = refs[0].CandidateID
			if refs[0].AmazonObservation != nil {
				if e = r.SaveAmazonObservation(tx, command.UserID, command.CurationID, candidate, *refs[0].AmazonObservation); e != nil {
					return e
				}
			}
		}
		_, e = q.ExecContext(tx, `UPDATE research_findings SET status='ADDED',candidate_id=$2 WHERE id=$1`, s.Finding.ID, candidate)
		return e
	})
	return candidate, e
}

func (r *Repository) PreflightFindingImport(ctx context.Context, command a.CatalogCandidatePoolCommandV2) (a.CatalogCandidatePoolPreflightV2, error) {
	var out a.CatalogCandidatePoolPreflightV2
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		var owner string
		if e := r.database.Queryer(tx).QueryRowContext(tx, `SELECT user_id FROM curations WHERE id=$1 AND user_id=$2 AND archived_at IS NULL FOR UPDATE`, command.CurationID, command.UserID).Scan(&owner); e != nil {
			return e
		}
		var e error
		out, e = r.PreflightCatalogSearchV2(tx, command)
		return e
	})
	if err != nil {
		for _, code := range []string{"CURATION_ACTION_IN_PROGRESS", "TOO_MANY_ACTIVE_ACTIONS"} {
			if strings.Contains(err.Error(), code) {
				return out, bgError(code)
			}
		}
	}
	return out, err
}
