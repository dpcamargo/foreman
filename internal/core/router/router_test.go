package router

import (
	"testing"
	"time"
)

func now() time.Time {
	return time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
}

func testRoutes() []Route {
	return []Route{
		{ID: "codex/gpt-5", Role: RoleImplementer, Provider: "openai", ModelFamily: "openai", Harness: "codex", CostPer1k: 0.01, Enabled: true},
		{ID: "hermes/claude-sonnet-5", Role: RoleImplementer, Provider: "anthropic", ModelFamily: "anthropic", Harness: "hermes", CostPer1k: 0.03, Enabled: true},
		{ID: "agy/gemini-2.5-pro", Role: RoleImplementer, Provider: "google", ModelFamily: "google", Harness: "agy", CostPer1k: 0.005, Enabled: true},
		{ID: "codex/gpt-5-review", Role: RoleReviewer, Provider: "openai", ModelFamily: "openai", Harness: "codex", CostPer1k: 0.01, Enabled: true},
	}
}

func TestSelectOrdersByCostOverPSuccess(t *testing.T) {
	routes := testRoutes()
	stats := map[RouteClassKey]RoutingStats{
		{RouteID: "codex/gpt-5", TaskClass: "feature"}:        {Alpha: 6, Beta: 3},
		{RouteID: "agy/gemini-2.5-pro", TaskClass: "feature"}: {Alpha: 2, Beta: 7},
	}
	cands, err := Select(Request{Role: RoleImplementer, TaskClass: "feature"}, nil, routes, stats, now())
	if err != nil {
		t.Fatal(err)
	}
	// codex/gpt-5: cost=0.01, p=6/9=0.667, score=0.015
	// hermes: cost=0.03, p=0.5 (prior), score=0.06
	// agy: cost=0.005, p=2/9=0.222, score=0.0225
	if cands[0].RouteID != "codex/gpt-5" {
		t.Errorf("first candidate = %s, want codex/gpt-5", cands[0].RouteID)
	}
}

func TestSelectExcludesFamily(t *testing.T) {
	routes := testRoutes()
	cands, err := Select(Request{
		Role: RoleImplementer, TaskClass: "feature",
		ExcludedFamilies: []string{"openai", "anthropic"},
	}, nil, routes, nil, now())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cands {
		if c.ModelFamily == "openai" || c.ModelFamily == "anthropic" {
			t.Errorf("excluded family %s appeared in candidates", c.ModelFamily)
		}
	}
	if len(cands) != 1 || cands[0].ModelFamily != "google" {
		t.Errorf("candidates = %+v, want only google", cands)
	}
}

func TestSelectExcludesCoolingProvider(t *testing.T) {
	routes := testRoutes()
	cooldown := now().Add(1 * time.Hour)
	providers := []ProviderHealth{
		{ID: "openai", Status: StatusCoolingDown, CooldownUntil: &cooldown},
	}
	cands, err := Select(Request{Role: RoleImplementer, TaskClass: "feature"}, providers, routes, nil, now())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cands {
		if c.Provider == "openai" {
			t.Error("cooling-down provider should be excluded")
		}
	}
}

func TestSelectNoEligibleRoute(t *testing.T) {
	routes := testRoutes()
	cooldown := now().Add(1 * time.Hour)
	providers := []ProviderHealth{
		{ID: "openai", Status: StatusCoolingDown, CooldownUntil: &cooldown},
		{ID: "anthropic", Status: StatusCoolingDown, CooldownUntil: &cooldown},
		{ID: "google", Status: StatusCoolingDown, CooldownUntil: &cooldown},
	}
	_, err := Select(Request{Role: RoleImplementer, TaskClass: "feature"}, providers, routes, nil, now())
	if err != ErrNoEligibleRoute {
		t.Errorf("err = %v, want ErrNoEligibleRoute", err)
	}
}

func TestSelectSkipsDisabledRoutes(t *testing.T) {
	routes := testRoutes()
	routes[0].Enabled = false
	cands, err := Select(Request{Role: RoleImplementer, TaskClass: "feature"}, nil, routes, nil, now())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cands {
		if c.RouteID == "codex/gpt-5" {
			t.Error("disabled route should be excluded")
		}
	}
}

func TestSelectWrongRoleExcluded(t *testing.T) {
	routes := testRoutes()
	cands, err := Select(Request{Role: RoleReviewer, TaskClass: "feature"}, nil, routes, nil, now())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cands {
		if c.RouteID != "codex/gpt-5-review" {
			t.Errorf("unexpected route %s for reviewer role", c.RouteID)
		}
	}
}
