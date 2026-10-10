package router

import "testing"
import "time"

func TestRecordFailureCooldownWithReportedReset(t *testing.T) {
	reset := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	h := RecordFailure(ProviderHealth{ID: "openai"}, &reset, time.Now())
	if h.Status != StatusCoolingDown {
		t.Errorf("status = %q, want cooling_down", h.Status)
	}
	if h.CooldownUntil == nil || !h.CooldownUntil.Equal(reset) {
		t.Errorf("cooldownUntil = %v, want %v", h.CooldownUntil, reset)
	}
}

func TestRecordFailureExponentialBackoff(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	h := ProviderHealth{ID: "openai"}

	h = RecordFailure(h, nil, now)
	if h.BackoffSeconds != 60 {
		t.Errorf("backoff = %d, want 60", h.BackoffSeconds)
	}
	h = RecordFailure(h, nil, now)
	if h.BackoffSeconds != 120 {
		t.Errorf("backoff = %d, want 120", h.BackoffSeconds)
	}
	h = RecordFailure(h, nil, now)
	if h.BackoffSeconds != 240 {
		t.Errorf("backoff = %d, want 240", h.BackoffSeconds)
	}
}

func TestRecordFailureBackoffCapped(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	h := ProviderHealth{ID: "openai", BackoffSeconds: 3600}
	h = RecordFailure(h, nil, now)
	if h.BackoffSeconds != 3600 {
		t.Errorf("backoff = %d, want 3600 (capped)", h.BackoffSeconds)
	}
}

func TestIsEligibleCooldownExpires(t *testing.T) {
	past := time.Date(2026, 10, 9, 11, 0, 0, 0, time.UTC)
	future := time.Date(2026, 10, 9, 13, 0, 0, 0, time.UTC)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

	h := ProviderHealth{ID: "openai", Status: StatusCoolingDown, CooldownUntil: &past}
	if !IsEligible(h, now) {
		t.Error("cooldown elapsed, should be eligible")
	}

	h.CooldownUntil = &future
	if IsEligible(h, now) {
		t.Error("cooldown not elapsed, should not be eligible")
	}
}

func TestIsEligibleOKAndQuotaLow(t *testing.T) {
	now := time.Now()
	for _, status := range []ProviderStatus{StatusOK, StatusQuotaLow} {
		h := ProviderHealth{ID: "openai", Status: status}
		if !IsEligible(h, now) {
			t.Errorf("status %q should be eligible", status)
		}
	}
}
