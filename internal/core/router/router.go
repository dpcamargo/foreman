package router

import (
	"errors"
	"sort"
	"time"
)

// Role identifies the engine role a route serves.
type Role string

const (
	RoleImplementer Role = "implementer"
	RoleReviewer    Role = "reviewer"
	RoleTestAuthor  Role = "test_author"
	RoleSpecifier   Role = "specifier"
)

// Route is a (harness, provider, model, effort) tuple eligible for a given role.
type Route struct {
	ID            string
	Role          Role
	Provider      string
	ModelFamily   string
	Harness       string
	CostPer1k     float64
	QuotaPressure float64
	PMin          float64
	Enabled       bool
}

// Request describes a routing request (FR-002).
type Request struct {
	Role             Role
	TaskClass        string
	Risk             string
	ExcludedFamilies []string
}

// Candidate is a route eligible for selection, with its score (FR-003).
type Candidate struct {
	RouteID     string
	Provider    string
	ModelFamily string
	Harness     string
	CostPer1k   float64
	PSuccess    float64
	Score       float64
}

// ErrNoEligibleRoute is returned when no candidate qualifies (FR-005).
var ErrNoEligibleRoute = errors.New("no eligible route")

// Select returns candidates ordered by FR-003's argmin cost / P(verified success),
// filtered by policy (enabled), provider health, and family exclusion rules (FR-002).
func Select(req Request, providers []ProviderHealth, routes []Route, stats map[RouteClassKey]RoutingStats, now time.Time) ([]Candidate, error) {
	// Build provider health lookup
	healthByID := make(map[string]ProviderHealth)
	for _, p := range providers {
		healthByID[p.ID] = p
	}

	// Build excluded family set
	excluded := make(map[string]bool)
	for _, f := range req.ExcludedFamilies {
		excluded[f] = true
	}

	var candidates []Candidate
	for _, r := range routes {
		if !r.Enabled || r.Role != req.Role {
			continue
		}
		// Family exclusion (FR-002: "excluded families never appear in the candidate list")
		if excluded[r.ModelFamily] {
			continue
		}
		// Provider health filter (FR-004)
		h, ok := healthByID[r.Provider]
		if ok && !IsEligible(h, now) {
			continue
		}
		// Beta-prior success estimate (FR-003)
		key := RouteClassKey{RouteID: r.ID, TaskClass: req.TaskClass}
		s, ok := stats[key]
		pSuccess := 0.5 // prior default
		if ok {
			pSuccess = s.PSuccess()
		}
		// Risk-tier minimum (FR-003)
		if r.PMin > 0 && pSuccess < r.PMin {
			continue
		}
		// Score: argmin cost / P(verified success) (FR-003)
		// Subscription routes have CostPer1k=0; apply quota_pressure as cost surrogate.
		effectiveCost := r.CostPer1k + r.QuotaPressure
		score := effectiveCost / pSuccess

		candidates = append(candidates, Candidate{
			RouteID:     r.ID,
			Provider:    r.Provider,
			ModelFamily: r.ModelFamily,
			Harness:     r.Harness,
			CostPer1k:   r.CostPer1k,
			PSuccess:    pSuccess,
			Score:       score,
		})
	}

	if len(candidates) == 0 {
		return nil, ErrNoEligibleRoute
	}

	// Sort by score ascending (argmin)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score < candidates[j].Score
	})
	return candidates, nil
}
