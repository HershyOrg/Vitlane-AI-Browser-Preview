package app

import (
	"context"
	"encoding/json"
	c "github.com/vitlane/vitlane/server/internal/curation/app"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"reflect"
)

type criteriaPort interface {
	LockTargetCriteria(context.Context, string, string, string) (*d.TargetCriteriaSetV1, error)
	TargetCriteria(context.Context, string, string, string) (*d.TargetCriteriaSetV1, error)
	ChangeTargetCriteria(context.Context, string, string, string, c.CriteriaCommand) (d.TargetCriteriaSetV1, error)
	ContentLocale(context.Context, string) (string, error)
	PlanContentLocale(context.Context, string, string) (string, error)
}
type criteriaCheckpointRepository interface {
	ReadCriteriaCheckpoint(context.Context, string, string) (*CatalogIntelligenceCatalogQuery, error)
	SaveCriteriaCheckpoint(context.Context, string, string, CatalogIntelligenceCatalogQuery) error
}

func (s *Service) attachCriteria(ctx context.Context, user, plan string, raw json.RawMessage) (json.RawMessage, error) {
	p, ok := s.plans.(criteriaPort)
	if !ok {
		return raw, nil
	}
	var v ResearchContext
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	var err error
	v.Criteria, err = p.TargetCriteria(ctx, user, v.Target.CurationID, v.Target.ID)
	if err != nil {
		return nil, err
	}
	v.ContentLocale, err = p.ContentLocale(ctx, user)
	if err != nil {
		return nil, err
	}
	// Round 1 and background Rounds without a requesting browser use the
	// language recorded when the user asked for the work.
	if v.RoundNumber == 1 || v.ContentLocale == "" {
		v.ContentLocale, err = p.PlanContentLocale(ctx, user, v.Target.ID)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(v)
}

// The execution checkpoint is written once under the Round lock. A settings
// edit before this point conflicts; later edits affect only later requests.
func (s *Service) checkpointCriteria(ctx context.Context, user, round string, snapshot ResearchContext, query CatalogIntelligenceCatalogQuery) (CatalogIntelligenceCatalogQuery, error) {
	p, ok := s.plans.(criteriaPort)
	if !ok {
		return query, nil
	}
	repo, ok := s.repository.(criteriaCheckpointRepository)
	if !ok {
		return query, fault.New(fault.InternalFailure, "RESEARCH_CRITERIA_UNAVAILABLE", false)
	}
	var result CatalogIntelligenceCatalogQuery
	err := s.transactor.WithinTransaction(ctx, func(tx context.Context) error {
		r, err := s.repository.GetRound(tx, user, round, true)
		if err != nil {
			return err
		}
		if r.Status != "REQUESTED" {
			return fault.New(fault.Conflict, "RESEARCH_ROUND_CLOSED", false)
		}
		saved, err := repo.ReadCriteriaCheckpoint(tx, user, round)
		if err != nil {
			return err
		}
		if saved != nil {
			result = *saved
			return nil
		}
		query.Country = string(snapshot.ResearchScope.Country)
		if !validQuerySeeds(query.Country, query) {
			return fault.New(fault.ProviderRejected, "RESEARCH_QUERY_SEEDS_INVALID", false)
		}
		old, err := p.LockTargetCriteria(tx, user, snapshot.Target.CurationID, snapshot.Target.ID)
		if err != nil {
			return err
		}
		expected := int64(0)
		if snapshot.Criteria != nil {
			expected = snapshot.Criteria.Version
		}
		if (old == nil && expected != 0) || (old != nil && old.Version != expected) {
			return fault.New(fault.Conflict, "RESEARCH_CRITERIA_CHANGED", false)
		}
		if old != nil && !c.ThreadExecutionFrom(tx).AllowFeedbackCriteria {
			// Auto applies its criteria primitive before research; legacy research stays read-only.
			query.Criteria = old
		} else {
			if query.Criteria == nil {
				return fault.New(fault.ProviderRejected, "RESEARCH_CRITERIA_INVALID", false)
			}
			next := *query.Criteria
			next.SchemaVersion = d.CriteriaSchema
			next.Version = expected
			if old != nil && reflect.DeepEqual(*old, next) {
				query.Criteria = old
			} else {
				plan, err := s.plans.Get(tx, user, snapshot.PlanID)
				if err != nil {
					return err
				}
				next.Version = expected + 1
				next, err = p.ChangeTargetCriteria(tx, user, snapshot.Target.CurationID, snapshot.Target.ID, c.CriteriaCommand{SchemaVersion: "vitlane.criteria-command.v1", ExpectedCriteriaVersion: expected, ExpectedCurationVersion: plan.Curation.Version, IdempotencyKey: "research:" + round, Criteria: next})
				if err != nil {
					return err
				}
				query.Criteria = &next
			}
		}
		if owner, ok := s.plans.(interface {
			SetTargetProductVertical(context.Context, string, string, string) error
		}); ok {
			if err := owner.SetTargetProductVertical(tx, user, snapshot.Target.ID, query.ProductVertical); err != nil {
				return err
			}
		}
		result = query
		return repo.SaveCriteriaCheckpoint(tx, user, round, query)
	})
	return result, err
}

func (s *Service) attachExecutionQuery(ctx context.Context, user, round string, v *ResearchContext) error {
	if s.liveCatalog != nil {
		v.DiscoveryRequirements = s.liveCatalog.DiscoveryRequirements(string(v.ResearchScope.Country))
	}
	if repo, ok := s.repository.(criteriaCheckpointRepository); ok {
		saved, err := repo.ReadCriteriaCheckpoint(ctx, user, round)
		if err != nil {
			return err
		}
		if saved != nil {
			v.CachedQuery = saved
			v.Criteria = saved.Criteria
			v.ExecutionCheckpoint = true
			return nil
		}
	}
	if repo, ok := s.repository.(interface {
		LatestCriteriaQuery(context.Context, string, string) (*CatalogIntelligenceCatalogQuery, error)
	}); ok {
		q, err := repo.LatestCriteriaQuery(ctx, user, v.Target.ID)
		if err != nil {
			return err
		}
		if q != nil && q.Criteria != nil && v.Criteria != nil && q.Country == string(v.ResearchScope.Country) && q.Criteria.Version == v.Criteria.Version && q.ProductVertical != "" && validQuerySeeds(q.Country, *q) {
			v.CachedQuery = q
		}
	}
	return nil
}
