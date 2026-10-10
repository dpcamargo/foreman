package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/dpcamargo/foreman/internal/core/router"
)

func TestSaveLoadProviderHealth(t *testing.T) {
	store := openTest(t)
	ctx := context.Background()

	h := router.ProviderHealth{ID: "openai", Status: router.StatusCoolingDown, BackoffSeconds: 120}
	reset := time.Date(2026, 10, 9, 13, 0, 0, 0, time.UTC)
	h.CooldownUntil = &reset

	if err := store.SaveProviderHealth(ctx, h); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.LoadProviderHealth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d providers, want 1", len(loaded))
	}
	got := loaded[0]
	if got.ID != "openai" || got.Status != router.StatusCoolingDown || got.BackoffSeconds != 120 {
		t.Errorf("got %+v", got)
	}
	if got.CooldownUntil == nil || !got.CooldownUntil.Equal(reset) {
		t.Errorf("cooldownUntil = %v, want %v", got.CooldownUntil, reset)
	}
}

// insertRouteForTest inserts a routes row to satisfy FK constraints.
func insertRouteForTest(t *testing.T, s *Store) {
	t.Helper()
	_, err := s.db.Exec(`INSERT OR IGNORE INTO providers (id, status, backoff_seconds, quota_remaining, updated_at) VALUES ('openai', 'ok', 0, 0, '2026-10-09T00:00:00.000000Z')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`INSERT OR IGNORE INTO routes (id, role, provider, model_family, harness, cost_per_1k_tokens, quota_pressure, p_min, enabled) VALUES ('codex/gpt-5', 'implementer', 'openai', 'openai', 'codex', 0.01, 0, 0, 1)`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSaveLoadRoutingStats(t *testing.T) {
	store := openTest(t)
	insertRouteForTest(t, store)
	ctx := context.Background()

	st := router.RoutingStats{RouteID: "codex/gpt-5", TaskClass: "feature", Alpha: 6, Beta: 3, Samples: 8}
	if err := store.SaveRoutingStats(ctx, st); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.LoadRoutingStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	key := router.RouteClassKey{RouteID: "codex/gpt-5", TaskClass: "feature"}
	got, ok := loaded[key]
	if !ok {
		t.Fatal("routing stats not loaded")
	}
	if got.Alpha != 6 || got.Beta != 3 || got.Samples != 8 {
		t.Errorf("got %+v", got)
	}
}

func TestRecordRouteOutcomeCreatesNew(t *testing.T) {
	store := openTest(t)
	insertRouteForTest(t, store)
	ctx := context.Background()

	if err := store.RecordRouteOutcome(ctx, "codex/gpt-5", "feature", true); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.LoadRoutingStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	key := router.RouteClassKey{RouteID: "codex/gpt-5", TaskClass: "feature"}
	got := loaded[key]
	if got.Alpha != 2 || got.Beta != 1 || got.Samples != 1 {
		t.Errorf("got %+v, want alpha=2 beta=1 samples=1", got)
	}
}

func TestRecordRouteOutcomeUpdatesExisting(t *testing.T) {
	store := openTest(t)
	insertRouteForTest(t, store)
	ctx := context.Background()

	st := router.RoutingStats{RouteID: "codex/gpt-5", TaskClass: "feature", Alpha: 6, Beta: 3, Samples: 8}
	if err := store.SaveRoutingStats(ctx, st); err != nil {
		t.Fatal(err)
	}

	if err := store.RecordRouteOutcome(ctx, "codex/gpt-5", "feature", false); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.LoadRoutingStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	key := router.RouteClassKey{RouteID: "codex/gpt-5", TaskClass: "feature"}
	got := loaded[key]
	if got.Alpha != 6 || got.Beta != 4 || got.Samples != 9 {
		t.Errorf("got %+v, want alpha=6 beta=4 samples=9", got)
	}
}

func TestSaveTestAuthorResult(t *testing.T) {
	store := openTest(t)
	ctx := context.Background()

	taskID := newTestTaskForControl(t, ctx, store)
	nodes, err := store.NodesForTask(ctx, taskID)
	if err != nil || len(nodes) == 0 {
		t.Fatalf("NodesForTask: %v, %d nodes", err, len(nodes))
	}

	r := TestAuthorRecord{
		ID:           "tar_test123",
		TaskID:       taskID,
		NodeID:       nodes[0].ID,
		VisiblePatch: []byte("visible patch"),
		HiddenPatch:  []byte("hidden patch"),
		FailsOnBase:  true,
		ModelFamily:  "anthropic",
	}
	if err := store.SaveTestAuthorResult(ctx, r); err != nil {
		t.Fatal(err)
	}
}
