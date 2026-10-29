package ontology

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestCanonicalStateProjectionUsesSemanticPayloadOnly(t *testing.T) {
	first, err := CanonicalStateProjection(StateProjectionInput{
		TenantID: " tenant-1 ", EventType: " scheduler.task.updated ", EntityID: " task-1 ",
		Payload: map[string]any{"enabled": true, "id": "task-1"}, SourceVersion: 7,
		SourceUpdatedAt: time.Date(2026, 8, 21, 1, 0, 0, 0, time.FixedZone("CST", 8*60*60)),
		SnapshotAt:      time.Date(2026, 8, 21, 2, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CanonicalStateProjection(first): %v", err)
	}
	second, err := CanonicalStateProjection(StateProjectionInput{
		TenantID: "tenant-1", EventType: "scheduler.task.updated", EntityID: "task-1",
		Payload: map[string]any{"id": "task-1", "enabled": true}, SourceVersion: 99,
		SourceUpdatedAt: time.Date(2026, 8, 22, 3, 0, 0, 0, time.UTC),
		SnapshotAt:      time.Date(2026, 8, 21, 23, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CanonicalStateProjection(second): %v", err)
	}
	if first.SemanticHash != second.SemanticHash || len(first.SemanticHash) != 64 {
		t.Fatalf("semantic hashes = %q and %q", first.SemanticHash, second.SemanticHash)
	}
	if first.SnapshotDate != "2026-08-21" || first.SourceUpdatedAt.Location() != time.UTC || first.TenantID != "tenant-1" {
		t.Fatalf("canonical projection = %+v", first)
	}
}

func TestCanonicalStateProjectionRejectsInvalidInput(t *testing.T) {
	base := StateProjectionInput{
		TenantID: "tenant-1", EventType: "scheduler.task.updated", EntityID: "task-1",
		Payload: map[string]any{"id": "task-1"}, SnapshotAt: time.Now().UTC(),
	}
	tests := []struct {
		name   string
		mutate func(*StateProjectionInput)
	}{
		{name: "tenant", mutate: func(input *StateProjectionInput) { input.TenantID = "" }},
		{name: "event type", mutate: func(input *StateProjectionInput) { input.EventType = "" }},
		{name: "entity", mutate: func(input *StateProjectionInput) { input.EntityID = "" }},
		{name: "payload", mutate: func(input *StateProjectionInput) { input.Payload = nil }},
		{name: "snapshot time", mutate: func(input *StateProjectionInput) { input.SnapshotAt = time.Time{} }},
		{name: "unencodable payload", mutate: func(input *StateProjectionInput) { input.Payload = map[string]any{"bad": math.Inf(1)} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := base
			test.mutate(&input)
			if _, err := CanonicalStateProjection(input); err == nil {
				t.Fatalf("CanonicalStateProjection(%s) error = nil", test.name)
			}
		})
	}
}

func TestPlatformStateProjectionStoreBoundsSemanticState(t *testing.T) {
	ctx := context.Background()
	dbstore := openStateProjectionTestStore(t, ctx)
	store := NewPlatformStateProjectionStore(dbstore)
	input := testStateProjectionInput(7, time.Date(2026, 8, 21, 2, 0, 0, 0, time.UTC), map[string]any{
		"id": "task-1", "enabled": true,
	})
	var graphCalls atomic.Int32
	applyGraph := func(context.Context, db.Tx) error {
		graphCalls.Add(1)
		return nil
	}

	first, err := store.Apply(ctx, input, applyGraph)
	if err != nil || first.Outcome != StateProjectionChanged {
		t.Fatalf("first Apply = %+v, err=%v", first, err)
	}
	for range 100 {
		repeated, err := store.Apply(ctx, input, applyGraph)
		if err != nil || repeated.Outcome != StateProjectionNoop {
			t.Fatalf("repeated Apply = %+v, err=%v", repeated, err)
		}
	}
	if graphCalls.Load() != 1 {
		t.Fatalf("graph calls = %d, want 1", graphCalls.Load())
	}
	assertStateProjectionRowCounts(t, ctx, dbstore, 1, 1)

	nextDay := input
	nextDay.SnapshotAt = time.Date(2026, 8, 22, 1, 0, 0, 0, time.UTC)
	snapshotOnly, err := store.Apply(ctx, nextDay, applyGraph)
	if err != nil || snapshotOnly.Outcome != StateProjectionSnapshotOnly {
		t.Fatalf("next-day Apply = %+v, err=%v", snapshotOnly, err)
	}
	assertStateProjectionRowCounts(t, ctx, dbstore, 1, 2)

	changed := nextDay
	changed.SourceVersion = 8
	changed.SourceUpdatedAt = changed.SourceUpdatedAt.Add(time.Minute)
	changed.Payload = map[string]any{"id": "task-1", "enabled": false}
	changedReport, err := store.Apply(ctx, changed, applyGraph)
	if err != nil || changedReport.Outcome != StateProjectionChanged {
		t.Fatalf("changed Apply = %+v, err=%v", changedReport, err)
	}
	if graphCalls.Load() != 2 {
		t.Fatalf("graph calls after change = %d, want 2", graphCalls.Load())
	}
	assertStateProjectionRowCounts(t, ctx, dbstore, 1, 2)
	var enabled bool
	if err := dbstore.QueryRow(ctx, `SELECT json_extract(payload_json, '$.enabled') FROM ontology_daily_snapshots WHERE snapshot_date = ?`, "2026-08-22").Scan(&enabled); err != nil || enabled {
		t.Fatalf("latest daily enabled = %v, err=%v", enabled, err)
	}
}

func TestPlatformStateProjectionStoreRejectsStaleAndConflictingWrites(t *testing.T) {
	ctx := context.Background()
	dbstore := openStateProjectionTestStore(t, ctx)
	store := NewPlatformStateProjectionStore(dbstore)
	base := testStateProjectionInput(10, time.Date(2026, 8, 21, 2, 0, 0, 0, time.UTC), map[string]any{"id": "task-1", "value": "current"})
	if _, err := store.Apply(ctx, base, func(context.Context, db.Tx) error { return nil }); err != nil {
		t.Fatalf("Apply(base): %v", err)
	}

	stale := base
	stale.SourceVersion = 9
	stale.Payload = map[string]any{"id": "task-1", "value": "stale"}
	staleReport, err := store.Apply(ctx, stale, func(context.Context, db.Tx) error { return errors.New("must not run") })
	if err != nil || staleReport.Outcome != StateProjectionStale {
		t.Fatalf("stale Apply = %+v, err=%v", staleReport, err)
	}

	conflicting := base
	conflicting.Payload = map[string]any{"id": "task-1", "value": "conflict"}
	if _, err := store.Apply(ctx, conflicting, func(context.Context, db.Tx) error { return nil }); !errors.Is(err, ErrStateProjectionConflict) {
		t.Fatalf("equal-version conflict error = %v", err)
	}

	repair := conflicting
	repair.AllowEqualOrderRepair = true
	graphApplied := false
	repaired, err := store.Apply(ctx, repair, func(context.Context, db.Tx) error {
		graphApplied = true
		return nil
	})
	if err != nil || repaired.Outcome != StateProjectionChanged || !graphApplied {
		t.Fatalf("authoritative equal-version repair = %+v, graphApplied=%v, err=%v", repaired, graphApplied, err)
	}
	var repairedValue string
	if err := dbstore.QueryRow(ctx, `SELECT json_extract(payload_json, '$.value') FROM ontology_daily_snapshots WHERE entity_id = ? AND snapshot_date = ?`, repair.EntityID, repaired.SnapshotDate).Scan(&repairedValue); err != nil || repairedValue != "conflict" {
		t.Fatalf("repaired value = %q, err=%v", repairedValue, err)
	}

	newerEqual := base
	newerEqual.SourceVersion = 11
	newerEqual.SourceUpdatedAt = newerEqual.SourceUpdatedAt.Add(time.Minute)
	newerEqual.Payload = repair.Payload
	watermark, err := store.Apply(ctx, newerEqual, func(context.Context, db.Tx) error { return errors.New("must not run") })
	if err != nil || watermark.Outcome != StateProjectionNoop {
		t.Fatalf("watermark Apply = %+v, err=%v", watermark, err)
	}
	var applied, observed int64
	if err := dbstore.QueryRow(ctx, `SELECT applied_version, observed_version FROM ontology_projection_state`).Scan(&applied, &observed); err != nil || applied != 10 || observed != 11 {
		t.Fatalf("versions = applied:%d observed:%d err=%v", applied, observed, err)
	}
}

func TestPlatformStateProjectionStoreUsesTimestampOrderingWithoutVersion(t *testing.T) {
	ctx := context.Background()
	dbstore := openStateProjectionTestStore(t, ctx)
	store := NewPlatformStateProjectionStore(dbstore)
	base := testStateProjectionInput(0, time.Date(2026, 8, 21, 2, 0, 0, 0, time.UTC), map[string]any{"id": "task-1", "value": "current"})
	if _, err := store.Apply(ctx, base, func(context.Context, db.Tx) error { return nil }); err != nil {
		t.Fatalf("Apply(base): %v", err)
	}

	stale := base
	stale.SourceUpdatedAt = stale.SourceUpdatedAt.Add(-time.Second)
	stale.Payload = map[string]any{"id": "task-1", "value": "stale"}
	report, err := store.Apply(ctx, stale, func(context.Context, db.Tx) error { return errors.New("must not run") })
	if err != nil || report.Outcome != StateProjectionStale {
		t.Fatalf("stale timestamp Apply = %+v, err=%v", report, err)
	}

	conflict := base
	conflict.Payload = map[string]any{"id": "task-1", "value": "conflict"}
	if _, err := store.Apply(ctx, conflict, func(context.Context, db.Tx) error { return nil }); !errors.Is(err, ErrStateProjectionConflict) {
		t.Fatalf("equal-timestamp conflict error = %v", err)
	}
}

func TestPlatformStateProjectionStoreRollsBackGraphFailure(t *testing.T) {
	ctx := context.Background()
	dbstore := openStateProjectionTestStore(t, ctx)
	store := NewPlatformStateProjectionStore(dbstore)
	wantErr := errors.New("graph failed")
	if _, err := store.Apply(ctx, testStateProjectionInput(1, time.Now().UTC(), map[string]any{"id": "task-1"}), func(context.Context, db.Tx) error {
		return wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("Apply error = %v, want %v", err, wantErr)
	}
	assertStateProjectionRowCounts(t, ctx, dbstore, 0, 0)
}

func TestPlatformStateProjectionStoreConcurrentEqualWritesConverge(t *testing.T) {
	ctx := context.Background()
	dbstore := openStateProjectionTestStore(t, ctx)
	store := NewPlatformStateProjectionStore(dbstore)
	input := testStateProjectionInput(1, time.Now().UTC(), map[string]any{"id": "task-1", "enabled": true})
	var graphCalls atomic.Int32
	var group sync.WaitGroup
	errorsFound := make(chan error, 20)
	for range 20 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := store.Apply(ctx, input, func(context.Context, db.Tx) error {
				graphCalls.Add(1)
				return nil
			})
			if err != nil {
				errorsFound <- err
			}
		}()
	}
	group.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("concurrent Apply: %v", err)
	}
	if graphCalls.Load() != 1 {
		t.Fatalf("graph calls = %d, want 1", graphCalls.Load())
	}
	assertStateProjectionRowCounts(t, ctx, dbstore, 1, 1)
}

func TestPlatformStateProjectionStoreDailySnapshotRetentionIsStrictAndBatched(t *testing.T) {
	ctx := context.Background()
	dbstore := openStateProjectionTestStore(t, ctx)
	store := NewPlatformStateProjectionStore(dbstore)
	currentDate := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	cutoffDate := currentDate.AddDate(0, 0, -180).Format("2006-01-02")
	oldDate := currentDate.AddDate(0, 0, -181).Format("2006-01-02")
	for index := range 501 {
		_, err := dbstore.Exec(ctx, `
INSERT INTO ontology_daily_snapshots (
	tenant_id, event_type, entity_id, snapshot_date, semantic_hash, payload_json,
	source_version, source_updated_at, projected_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"tenant-retention", "scheduler.task.updated", fmt.Sprintf("old-%03d", index), oldDate,
			"hash", `{}`, 1, currentDate.UnixNano(), currentDate.UnixNano())
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, date := range []string{cutoffDate, currentDate.Format("2006-01-02")} {
		if _, err := dbstore.Exec(ctx, `
INSERT INTO ontology_daily_snapshots (
	tenant_id, event_type, entity_id, snapshot_date, semantic_hash, payload_json,
	source_version, source_updated_at, projected_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"tenant-retention", "scheduler.task.updated", "boundary-"+date, date,
			"hash", `{}`, 1, currentDate.UnixNano(), currentDate.UnixNano()); err != nil {
			t.Fatal(err)
		}
	}

	deleted, err := store.DeleteDailySnapshotsBefore(ctx, cutoffDate, 500)
	if err != nil || deleted != 500 {
		t.Fatalf("first retention delete = %d, err=%v", deleted, err)
	}
	deleted, err = store.DeleteDailySnapshotsBefore(ctx, cutoffDate, 500)
	if err != nil || deleted != 1 {
		t.Fatalf("second retention delete = %d, err=%v", deleted, err)
	}
	deleted, err = store.DeleteDailySnapshotsBefore(ctx, cutoffDate, 500)
	if err != nil || deleted != 0 {
		t.Fatalf("converged retention delete = %d, err=%v", deleted, err)
	}
	var remaining, boundary int
	if err := dbstore.QueryRow(ctx, `SELECT COUNT(*) FROM ontology_daily_snapshots`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if err := dbstore.QueryRow(ctx, `SELECT COUNT(*) FROM ontology_daily_snapshots WHERE snapshot_date >= ?`, cutoffDate).Scan(&boundary); err != nil {
		t.Fatal(err)
	}
	if remaining != 2 || boundary != 2 {
		t.Fatalf("remaining snapshots = %d boundary/current = %d", remaining, boundary)
	}
}

func openStateProjectionTestStore(t *testing.T, ctx context.Context) *db.Store {
	t.Helper()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := migration.NewRunner([]migration.Migration{ontologyStateProjectionMigration()}).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	return store
}

func testStateProjectionInput(version int64, snapshotAt time.Time, payload map[string]any) StateProjectionInput {
	return StateProjectionInput{
		TenantID: "tenant-1", EventType: "scheduler.task.updated", EntityID: "task-1",
		Payload: payload, SourceVersion: version,
		SourceUpdatedAt: time.Date(2026, 8, 21, 1, 0, 0, 0, time.UTC),
		SnapshotAt:      snapshotAt,
	}
}

func assertStateProjectionRowCounts(t *testing.T, ctx context.Context, store *db.Store, wantState, wantSnapshots int) {
	t.Helper()
	var stateCount, snapshotCount int
	if err := store.QueryRow(ctx, `SELECT COUNT(*) FROM ontology_projection_state`).Scan(&stateCount); err != nil {
		t.Fatalf("count projection state: %v", err)
	}
	if err := store.QueryRow(ctx, `SELECT COUNT(*) FROM ontology_daily_snapshots`).Scan(&snapshotCount); err != nil {
		t.Fatalf("count daily snapshots: %v", err)
	}
	if stateCount != wantState || snapshotCount != wantSnapshots {
		t.Fatalf("row counts = state:%d snapshots:%d, want state:%d snapshots:%d", stateCount, snapshotCount, wantState, wantSnapshots)
	}
}

func TestOntologyStateProjectionMigrationCreatesBoundedStateTables(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if err := migration.NewRunner([]migration.Migration{ontologyStateProjectionMigration()}).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	for _, table := range []string{
		"ontology_projection_state",
		"ontology_daily_snapshots",
		"ontology_refresh_manifests",
	} {
		if exists, err := store.TableExists(ctx, table); err != nil || !exists {
			t.Fatalf("TableExists(%s) = %v, err=%v", table, exists, err)
		}
	}
	for _, index := range []string{
		"idx_ontology_daily_snapshots_retention",
		"idx_ontology_refresh_manifests_status",
	} {
		var found string
		if err := store.QueryRow(ctx, `SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&found); err != nil {
			t.Fatalf("missing index %s: %v", index, err)
		}
	}
}
