# Contracts: Routing, Provider Health, and Test-First

## Router interface

```go
// internal/core/router/router.go
package router

type Role string
const (
    RoleImplementer Role = "implementer"
    RoleReviewer    Role = "reviewer"
    RoleTestAuthor  Role = "test_author"
    RoleSpecifier   Role = "specifier"
)

type Request struct {
    Role             Role
    TaskClass        string
    Risk             string   // "low", "medium", "high"
    ExcludedFamilies []string // model families already used on this task
}

type Candidate struct {
    RouteID      string
    Provider     string
    ModelFamily  string
    Harness      string
    CostPer1k    float64
    PSuccess     float64  // Beta-prior estimate
    Score        float64  // cost / P(verified success)
}

// Select returns candidates ordered by FR-003's argmin cost/P(verified success),
// filtered by policy, provider health, and family exclusion rules (FR-002).
// Returns ErrNoEligibleRoute if no candidate qualifies.
func Select(req Request, providers []ProviderHealth, routes []Route, stats map[RouteClassKey]RoutingStats) ([]Candidate, error)
```

## Provider health interface

```go
// internal/core/router/health.go
package router

type ProviderStatus string
const (
    StatusOK         ProviderStatus = "ok"
    StatusCoolingDown ProviderStatus = "cooling_down"
    StatusQuotaLow   ProviderStatus = "quota_low"
)

type ProviderHealth struct {
    ID             string
    Status         ProviderStatus
    CooldownUntil  *time.Time
    BackoffSeconds int
    QuotaResetAt   *time.Time
    QuotaRemaining int
}

// RecordFailure updates provider health on a 429/plan-limit/5xx/expired-auth signal.
// Uses the provider's reported reset time if present, otherwise exponential backoff (FR-004).
func RecordFailure(h ProviderHealth, reportedReset *time.Time, now time.Time) ProviderHealth

// IsEligible reports whether the provider is currently available for routing.
func IsEligible(h ProviderHealth, now time.Time) bool
```

## Policy interface

```go
// internal/core/policy/policy.go
package policy

type Lane struct {
    Name       string  // "F", "S", "D"
    MaxUSD     float64
    MaxMinutes int
    Review     string  // "light", "full"
    TestFirst  bool
    Deliberation bool
}

type Policy struct {
    Lanes                          map[string]Lane
    APISpendWithoutApprovalPerDay  float64
    ClaudeSubscriptionUnattended   bool
}

// LoadPolicy reads and validates policy.yaml. Returns error if any referenced
// lane is missing or malformed (FR-009).
func LoadPolicy(path string) (*Policy, error)

// CheckBudget returns an error if adding estimatedCost to accumulatedUSD would
// exceed the lane's max_usd (FR-010).
func CheckBudget(lane Lane, accumulatedUSD, estimatedCost float64) error
```

## Test-author output

```go
// internal/core/engine/ports.go (extend)
type TestAuthorInput struct {
    TaskID      string
    Spec        TaskSpec
    Workspace   string   // sandbox workspace for running tests
    ModelFamily string   // family of the implementer (test author must differ)
}

type TestAuthorResult struct {
    VisiblePatch []byte   // test patch for implementer's workspace
    HiddenPatch  []byte   // test patch for verifier only (stored outside workspaces)
    FailsOnBase  bool     // verifier confirmed every new test fails on unmodified base
    ModelFamily  string   // family of the test-author model used
}

type TestAuthor interface {
    AuthorTests(ctx context.Context, in TestAuthorInput) (TestAuthorResult, error)
}
```

## Classifier interface (triage)

```go
// internal/adapters/classifier/classifier.go
package classifier

type TriageResult struct {
    TaskClass  string  // "feature", "refactor", "bugfix", "docs"
    Risk       string  // "low", "medium", "high"
    Confidence float64 // 0.0-1.0
    Source     string  // "local", "api"
}

// Classify assigns task class and risk from request text. Uses the local model
// as primary; falls back to API when confidence < threshold or model unavailable (FR-014).
func (c *Classifier) Classify(ctx context.Context, requestText string) (TriageResult, error)
```

## policy.yaml schema

```yaml
# $FOREMAN_HOME/policy.yaml
lanes:
  F:
    max_usd: 0.50
    max_minutes: 10
    review: light          # "light" or "full"
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
```
