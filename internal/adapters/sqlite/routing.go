package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/dpcamargo/foreman/internal/core/router"
)

// SaveProviderHealth persists a provider's health state (FR-001).
func (s *Store) SaveProviderHealth(ctx context.Context, h router.ProviderHealth) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO providers (id, status, cooldown_until, backoff_seconds, quota_reset_at, quota_remaining, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET status = excluded.status, cooldown_until = excluded.cooldown_until,
			backoff_seconds = excluded.backoff_seconds, quota_reset_at = excluded.quota_reset_at,
			quota_remaining = excluded.quota_remaining, updated_at = excluded.updated_at`,
		h.ID, string(h.Status), nullableTime(h.CooldownUntil), h.BackoffSeconds,
		nullableTime(h.QuotaResetAt), h.QuotaRemaining, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("save provider health: %w", err)
	}
	return nil
}

// LoadProviderHealth reads all provider health rows (FR-001).
func (s *Store) LoadProviderHealth(ctx context.Context) ([]router.ProviderHealth, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, status, cooldown_until, backoff_seconds, quota_reset_at, quota_remaining FROM providers`)
	if err != nil {
		return nil, fmt.Errorf("load provider health: %w", err)
	}
	defer rows.Close()
	var out []router.ProviderHealth
	for rows.Next() {
		var h router.ProviderHealth
		var status string
		var cooldownUntil, quotaResetAt sql.NullString
		if err := rows.Scan(&h.ID, &status, &cooldownUntil, &h.BackoffSeconds, &quotaResetAt, &h.QuotaRemaining); err != nil {
			return nil, fmt.Errorf("scan provider health: %w", err)
		}
		h.Status = router.ProviderStatus(status)
		h.CooldownUntil = parseNullTime(cooldownUntil)
		h.QuotaResetAt = parseNullTime(quotaResetAt)
		out = append(out, h)
	}
	return out, rows.Err()
}

// SaveRoutingStats persists Beta-prior routing statistics (FR-003, FR-011).
func (s *Store) SaveRoutingStats(ctx context.Context, st router.RoutingStats) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO routing_stats (route_id, task_class, alpha, beta, samples, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (route_id, task_class) DO UPDATE SET alpha = excluded.alpha, beta = excluded.beta,
			samples = excluded.samples, updated_at = excluded.updated_at`,
		st.RouteID, st.TaskClass, st.Alpha, st.Beta, st.Samples, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("save routing stats: %w", err)
	}
	return nil
}

// LoadRoutingStats reads all routing statistics (FR-003).
func (s *Store) LoadRoutingStats(ctx context.Context) (map[router.RouteClassKey]router.RoutingStats, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT route_id, task_class, alpha, beta, samples FROM routing_stats`)
	if err != nil {
		return nil, fmt.Errorf("load routing stats: %w", err)
	}
	defer rows.Close()
	out := make(map[router.RouteClassKey]router.RoutingStats)
	for rows.Next() {
		var st router.RoutingStats
		if err := rows.Scan(&st.RouteID, &st.TaskClass, &st.Alpha, &st.Beta, &st.Samples); err != nil {
			return nil, fmt.Errorf("scan routing stats: %w", err)
		}
		key := router.RouteClassKey{RouteID: st.RouteID, TaskClass: st.TaskClass}
		out[key] = st
	}
	return out, rows.Err()
}

// RecordRouteOutcome updates Beta-prior stats for a (route, task_class) pair (FR-011).
func (s *Store) RecordRouteOutcome(ctx context.Context, routeID, taskClass string, verifiedSuccess bool) error {
	var st router.RoutingStats
	err := s.db.QueryRowContext(ctx, `SELECT route_id, task_class, alpha, beta, samples FROM routing_stats WHERE route_id = ? AND task_class = ?`,
		routeID, taskClass).Scan(&st.RouteID, &st.TaskClass, &st.Alpha, &st.Beta, &st.Samples)
	if err == sql.ErrNoRows {
		st = router.RoutingStats{RouteID: routeID, TaskClass: taskClass, Alpha: 1, Beta: 1}
	} else if err != nil {
		return fmt.Errorf("load routing stats for outcome: %w", err)
	}
	st = st.Update(verifiedSuccess, time.Now().UTC())
	return s.SaveRoutingStats(ctx, st)
}

// SaveTestAuthorResult persists test-author node output (FR-007).
func (s *Store) SaveTestAuthorResult(ctx context.Context, r TestAuthorRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO test_author_results (id, task_id, node_id, visible_patch, hidden_patch, fails_on_base, model_family, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.TaskID, r.NodeID, r.VisiblePatch, r.HiddenPatch, boolToInt(r.FailsOnBase), r.ModelFamily, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("save test author result: %w", err)
	}
	return nil
}

// TestAuthorRecord is the persisted form of a test-author node's output (FR-007).
type TestAuthorRecord struct {
	ID           string
	TaskID       string
	NodeID       string
	VisiblePatch []byte
	HiddenPatch  []byte
	FailsOnBase  bool
	ModelFamily  string
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(tsLayout)
}

func parseNullTime(ns sql.NullString) *time.Time {
	if !ns.Valid {
		return nil
	}
	t, err := time.Parse(tsLayout, ns.String)
	if err != nil {
		return nil
	}
	return &t
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
