package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"sort"
	"strings"
)

const researchQueryExecutionVersion = "research-discovery.v4"

type CatalogLanguageRequirement struct{ Policy, Language string }
type CatalogQueryProjection struct {
	Policy, Language, Query         string
	Seeds, MustInclude, MustExclude []string
}

func queryCountry(c ResearchContext) string {
	if c.CatalogLanguage == "ko" {
		return "KR"
	}
	if c.CatalogLanguage == "en" {
		return "US"
	}
	return c.Country
}
func researchQueryHash(c ResearchContext) string {
	raw, _ := json.Marshal(struct {
		Intent, Title, Country, Feedback, FeedbackHash, Vertical string
		Criteria                                                 *ResearchCriteria
	}{c.TargetIntent, c.TargetTitle, c.Country, c.FeedbackSummary, c.FeedbackHash, c.ProductVertical, c.Criteria})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
func (s *Service) generateResearchQuery(ctx context.Context, claimed ClaimedJob, c ResearchContext) (CatalogQueryPayload, error) {
	requirements := append([]CatalogLanguageRequirement{}, c.CatalogLanguages...)
	legacy := len(requirements) == 0
	if legacy {
		language, policy := "en", "english-latin.v1"
		if c.Country == "KR" {
			language, policy = "ko", "korean-mixed.v1"
		}
		requirements = append(requirements, CatalogLanguageRequirement{policy, language})
	}
	sort.Slice(requirements, func(i, j int) bool { return requirements[i].Policy < requirements[j].Policy })
	seen := map[string]string{}
	projections := []CatalogQueryProjection{}
	inputHash := researchQueryHash(c)
	var result CatalogQueryPayload
	for _, requirement := range requirements {
		if previous, ok := seen[requirement.Policy]; ok {
			if previous != requirement.Language {
				return result, fault.New(fault.InternalFailure, "RESEARCH_LANGUAGE_POLICY_INVALID", false)
			}
			continue
		}
		seen[requirement.Policy] = requirement.Language
		if requirement.Policy == "" || requirement.Language != "en" && requirement.Language != "ko" {
			return result, fault.New(fault.InternalFailure, "RESEARCH_LANGUAGE_POLICY_INVALID", false)
		}
		current := c
		current.CatalogLanguage = requirement.Language
		var query CatalogQueryPayload
		cached := false
		if saved := c.CachedQuery; saved != nil {
			if saved.ExecutionVersion == researchQueryExecutionVersion && (c.ExecutionCheckpoint || saved.QueryInputHash == inputHash) {
				for _, p := range saved.Projections {
					if p.Policy == requirement.Policy && p.Language == requirement.Language && validQueryProjection(p) {
						query = *saved
						query.Query = p.Query
						query.QuerySeeds = p.Seeds
						query.MustInclude = p.MustInclude
						query.MustExclude = p.MustExclude
						cached = true
						break
					}
				}
			} else if (legacy || c.ExecutionCheckpoint) && len(saved.Projections) == 0 && validProviderCatalogPhrase(queryCountry(current), saved.Query) && (c.ExecutionCheckpoint || strings.TrimSpace(c.FeedbackSummary) == "" && saved.ProductVertical != "") {
				query = *saved
				cached = true
			}
		}
		if !cached {
			key := ":query:" + requirement.Policy
			if legacy {
				key = ""
			}
			if err := s.completeKeyed(ctx, claimed, key, researchSystemPrompt(providerCatalogQueryPrompt(queryCountry(current)), current), catalogQueryPrompt(current), SchemaCatalogQuery, CatalogQuerySchema(), &query, nil); err != nil {
				return result, err
			}
		}
		projection := CatalogQueryProjection{requirement.Policy, requirement.Language, strings.TrimSpace(query.Query), query.QuerySeeds, query.MustInclude, query.MustExclude}
		if !validQueryProjection(projection) {
			return result, fault.New(fault.ProviderRejected, "PROVIDER_RESPONSE_INVALID", true)
		}
		projections = append(projections, projection)
		if len(projections) == 1 {
			result = query
		}
	}
	result.ExecutionVersion = researchQueryExecutionVersion
	result.QueryInputHash = inputHash
	result.Projections = projections
	return result, nil
}
func validQueryProjection(p CatalogQueryProjection) bool {
	country := "US"
	if p.Language == "ko" {
		country = "KR"
	}
	if !validProviderCatalogPhrase(country, p.Query) {
		return false
	}
	for _, v := range append(append(append([]string{}, p.Seeds...), p.MustInclude...), p.MustExclude...) {
		if !validProviderCatalogPhrase(country, v) {
			return false
		}
	}
	return true
}
