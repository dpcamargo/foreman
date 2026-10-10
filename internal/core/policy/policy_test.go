package policy

import "testing"

const validYAML = `lanes:
  F:
    max_usd: 0.50
    max_minutes: 10
    review: light
    test_first: false
  S:
    max_usd: 5.00
    max_minutes: 30
    review: full
    test_first: true
  D:
    max_usd: 20.00
    max_minutes: 120
    review: full
    test_first: true
    deliberation: true
api_spend_without_approval_usd_per_day: 10.00
claude_subscription_unattended: false
`

func TestParsePolicyValid(t *testing.T) {
	p, err := ParsePolicy(validYAML)
	if err != nil {
		t.Fatalf("ParsePolicy: %v", err)
	}
	if len(p.Lanes) != 3 {
		t.Errorf("lanes = %d, want 3", len(p.Lanes))
	}
	f := p.Lanes["F"]
	if f.MaxUSD != 0.50 || f.Review != "light" || f.TestFirst {
		t.Errorf("lane F = %+v", f)
	}
	s := p.Lanes["S"]
	if s.MaxUSD != 5.00 || s.Review != "full" || !s.TestFirst {
		t.Errorf("lane S = %+v", s)
	}
	if p.APISpendWithoutApprovalPerDay != 10.00 {
		t.Errorf("api_spend = %v, want 10.00", p.APISpendWithoutApprovalPerDay)
	}
}

func TestParsePolicyMissingLane(t *testing.T) {
	yaml := "lanes:\n  F:\n    max_usd: 1\n    max_minutes: 1\n    review: light\n"
	_, err := ParsePolicy(yaml)
	if err == nil {
		t.Error("expected error for missing lanes")
	}
}

func TestParsePolicyInvalidReview(t *testing.T) {
	yaml := "lanes:\n  F:\n    max_usd: 1\n    max_minutes: 1\n    review: medium\n  S:\n    max_usd: 1\n    max_minutes: 1\n    review: full\n  D:\n    max_usd: 1\n    max_minutes: 1\n    review: full\n"
	_, err := ParsePolicy(yaml)
	if err == nil {
		t.Error("expected error for invalid review intensity")
	}
}

func TestCheckBudgetWithinCap(t *testing.T) {
	lane := Lane{Name: "F", MaxUSD: 0.50}
	if err := CheckBudget(lane, 0.20, 0.25); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCheckBudgetExceeded(t *testing.T) {
	lane := Lane{Name: "F", MaxUSD: 0.50}
	if err := CheckBudget(lane, 0.40, 0.20); err == nil {
		t.Error("expected budget exceeded error")
	}
}
