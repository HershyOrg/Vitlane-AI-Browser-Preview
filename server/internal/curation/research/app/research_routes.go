package app

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
	"sort"
	"time"
)

const ResearchRoutePolicyVersion = "catalog-routes.v1"

type ResourceConstraint struct {
	ID       string     `json:"id"`
	Kind     string     `json:"kind"`
	Used     int64      `json:"used"`
	Limit    int64      `json:"limit"`
	Pressure float64    `json:"pressure"`
	ReadyAt  *time.Time `json:"readyAt,omitempty"`
	Reason   string     `json:"reason,omitempty"`
}
type ProviderResourceState struct {
	CanStart    bool                 `json:"canStart"`
	Pressure    float64              `json:"pressure"`
	CostClass   string               `json:"costClass"`
	Reason      string               `json:"reason,omitempty"`
	ReadyAt     *time.Time           `json:"readyAt,omitempty"`
	ObservedAt  time.Time            `json:"observedAt"`
	Constraints []ResourceConstraint `json:"constraints"`
}

// ComposeProviderResources joins distinct constraint IDs, never summing unlike
// units or copying account usage into every Actor. Reasons are admission gates;
// pressure only determines preference among routes that can start.
func ComposeProviderResources(cost string, now time.Time, constraints []ResourceConstraint) ProviderResourceState {
	out := ProviderResourceState{CanStart: true, CostClass: cost, ObservedAt: now, Constraints: []ResourceConstraint{}}
	seen := map[string]int{}
	permanent := false
	for _, c := range constraints {
		if c.Limit > 0 {
			c.Pressure = min(1, max(0, float64(c.Used)/float64(c.Limit)))
		}
		if index, ok := seen[c.ID]; ok {
			previous := out.Constraints[index]
			if previous.Pressure > c.Pressure {
				c.Used = previous.Used
				c.Limit = previous.Limit
				c.Pressure = previous.Pressure
			}
			if previous.Reason != "" && c.Reason == "" {
				c.Reason = previous.Reason
				c.ReadyAt = previous.ReadyAt
			}
			out.Constraints[index] = c
		} else {
			seen[c.ID] = len(out.Constraints)
			out.Constraints = append(out.Constraints, c)
		}
		if c.Reason != "" {
			out.CanStart = false
			if out.Reason == "" {
				out.Reason = c.Reason
			}
			if c.ReadyAt == nil {
				permanent = true
			} else if out.ReadyAt == nil || c.ReadyAt.After(*out.ReadyAt) {
				at := *c.ReadyAt
				out.ReadyAt = &at
			}
		}
		out.Pressure = max(out.Pressure, c.Pressure)
	}
	if permanent {
		out.ReadyAt = nil
	}
	return out
}

type ResearchRoute struct {
	ID          string                `json:"id"`
	Source      string                `json:"source"`
	APIIDs      []string              `json:"apiIds"`
	Specialized bool                  `json:"specialized"`
	Resources   ProviderResourceState `json:"resources"`
	Weight      float64               `json:"weight"`
}

// OrderResearchRoutes uses a seeded exponential race (weighted sampling without
// replacement). Stable request+route IDs make replay reproducible; no diversity
// state, previous mall counts, or candidate quantity affects selection.
func OrderResearchRoutes(seed string, routes []ResearchRoute) []ResearchRoute {
	out := append([]ResearchRoute(nil), routes...)
	scores := map[string]float64{}
	for i := range out {
		route := &out[i]
		fit := 1.0
		if route.Specialized {
			fit = 2
		}
		cost := 1.0
		switch route.Resources.CostClass {
		case "FREE":
			cost = 3
		case "INCLUDED":
			cost = 2
		}
		route.Weight = 0
		if route.Resources.CanStart {
			route.Weight = fit * cost * max(0.1, 1-route.Resources.Pressure)
		}
		scores[route.ID] = math.Inf(1)
		if route.Weight > 0 {
			h := sha256.Sum256([]byte(ResearchRoutePolicyVersion + "|" + seed + "|" + route.ID))
			u := (float64(binary.BigEndian.Uint64(h[:8])>>11) + 1) / (float64(uint64(1)<<53) + 1)
			scores[route.ID] = -math.Log(u) / route.Weight
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if scores[out[i].ID] == scores[out[j].ID] {
			return out[i].ID < out[j].ID
		}
		return scores[out[i].ID] < scores[out[j].ID]
	})
	return out
}

// CombineRouteResources presents a multi-API route as one resource. Shared
// constraints are deduplicated; the most expensive billing class wins.
func CombineRouteResources(now time.Time, states ...ProviderResourceState) ProviderResourceState {
	cost := "FREE"
	constraints := []ResourceConstraint{}
	for i, state := range states {
		if state.CostClass == "METERED" {
			cost = "METERED"
		} else if state.CostClass == "INCLUDED" && cost == "FREE" {
			cost = "INCLUDED"
		}
		constraints = append(constraints, state.Constraints...)
		if !state.CanStart {
			reason := state.Reason
			if reason == "" {
				reason = "CATALOG_RESOURCE_UNAVAILABLE"
			}
			constraints = append(constraints, ResourceConstraint{ID: string(rune('A'+i)) + ":availability", Reason: reason, ReadyAt: state.ReadyAt})
		}
	}
	return ComposeProviderResources(cost, now, constraints)
}
