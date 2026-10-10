// Package router provides deterministic route selection with provider health tracking
// and cost-aware scoring (specs/004-routing-test-first/contracts/router-and-policy.md).
// Pure computation — no I/O, no model calls (constitution Principle I).
package router

import "time"

// ProviderStatus tracks a provider's availability (FR-001).
type ProviderStatus string

const (
	StatusOK          ProviderStatus = "ok"
	StatusCoolingDown ProviderStatus = "cooling_down"
	StatusQuotaLow    ProviderStatus = "quota_low"
)

// ProviderHealth is the health state for one provider (FR-001).
type ProviderHealth struct {
	ID             string
	Status         ProviderStatus
	CooldownUntil  *time.Time
	BackoffSeconds int
	QuotaResetAt   *time.Time
	QuotaRemaining int
}

// RecordFailure updates provider health on a 429/plan-limit/5xx/expired-auth signal (FR-004).
// Uses the provider's reported reset time if present, otherwise exponential backoff.
// backoffSeconds doubles on consecutive failures (1m, 2m, 4m, ... capped at 1h).
func RecordFailure(h ProviderHealth, reportedReset *time.Time, now time.Time) ProviderHealth {
	if reportedReset != nil {
		until := reportedReset.UTC()
		h.CooldownUntil = &until
	} else {
		if h.BackoffSeconds == 0 {
			h.BackoffSeconds = 60
		} else {
			h.BackoffSeconds *= 2
			if h.BackoffSeconds > 3600 {
				h.BackoffSeconds = 3600
			}
		}
		until := now.UTC().Add(time.Duration(h.BackoffSeconds) * time.Second)
		h.CooldownUntil = &until
	}
	h.Status = StatusCoolingDown
	return h
}

// IsEligible reports whether the provider is currently available for routing (FR-004).
// A cooling-down provider becomes eligible again automatically once its cooldown elapses.
func IsEligible(h ProviderHealth, now time.Time) bool {
	switch h.Status {
	case StatusOK, StatusQuotaLow:
		return true
	case StatusCoolingDown:
		if h.CooldownUntil == nil {
			return true
		}
		return now.After(*h.CooldownUntil) || now.Equal(*h.CooldownUntil)
	default:
		return false
	}
}
