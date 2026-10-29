package execution_test

import (
	"bytes"
	"context"
	"orrery_v2/internal/platform/execution"
	"orrery_v2/internal/platform/testkit"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/logging"
)

func TestStoreTouchesAndExpiresHeartbeat(t *testing.T) {
	ctx := context.Background()
	store := testkit.NewEphemeralLivenessStore()
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	store.Touch(ctx, "runtime-a", time.Minute, now)
	alive := store.IsAliveBatch(ctx, []string{"runtime-a", "runtime-b"}, now.Add(30*time.Second))
	if !alive["runtime-a"] || alive["runtime-b"] {
		t.Fatalf("alive map before expiry = %+v", alive)
	}
	alive = store.IsAliveBatch(ctx, []string{"runtime-a"}, now.Add(2*time.Minute))
	if alive["runtime-a"] {
		t.Fatalf("runtime-a alive after expiry")
	}
}

func TestFallbackStoreUsesSecondaryWhenPrimaryFails(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	secondary := testkit.NewEphemeralLivenessStore()
	store := execution.NewFallback(testkit.FailingLivenessStore{}, secondary)

	store.Touch(ctx, "runtime-a", time.Minute, now)
	alive := store.IsAliveBatch(ctx, []string{"runtime-a"}, now.Add(30*time.Second))

	if !alive["runtime-a"] {
		t.Fatalf("fallback did not use secondary store: %+v", alive)
	}
}

func TestFallbackStoreLogsWarnWhenUsingSecondary(t *testing.T) {
	var logs bytes.Buffer
	ctx := logging.WithCorrelationID(context.Background(), "corr-live")
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	secondary := testkit.NewEphemeralLivenessStore()
	store := execution.NewFallback(testkit.FailingLivenessStore{}, secondary).WithLogger(logging.NewJSONLogger(&logs, logging.LevelDebug))

	store.Touch(ctx, "runtime-a", time.Minute, now)
	alive := store.IsAliveBatch(ctx, []string{"runtime-a"}, now.Add(30*time.Second))

	if !alive["runtime-a"] {
		t.Fatalf("fallback did not use secondary store: %+v", alive)
	}
	got := logs.String()
	for _, want := range []string{`"level":"WARN"`, `"operation":"liveness.fallback"`, `"correlation_id":"corr-live"`, `"error_code":"liveness_primary_unavailable"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("log missing %s in %s", want, got)
		}
	}
}

func TestSQLiteStorePersistsHeartbeatAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runtime.db")
	store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	live, err := execution.NewLivenessStore(ctx, store)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	live.Touch(ctx, "runtime-a", time.Minute, now)
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	reopened, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer reopened.Close()
	reopenedLive, err := execution.NewLivenessStore(ctx, reopened)
	if err != nil {
		t.Fatalf("NewSQLite reopened: %v", err)
	}

	alive := reopenedLive.IsAliveBatch(ctx, []string{"runtime-a", "runtime-b"}, now.Add(30*time.Second))
	if !alive["runtime-a"] || alive["runtime-b"] {
		t.Fatalf("alive after reopen = %+v", alive)
	}
}

func TestSQLiteStoreSnapshotsHeartbeatDetails(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-liveness-snapshot"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	live, err := execution.NewLivenessStore(ctx, store)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)

	live.Touch(ctx, "agent-run:run-1", 90*time.Second, now)
	snapshots, err := live.SnapshotBatch(ctx, []string{"agent-run:run-1", "missing-runtime"}, now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}

	snapshot, ok := snapshots["agent-run:run-1"]
	if !ok {
		t.Fatalf("snapshot missing: %+v", snapshots)
	}
	if snapshot.RuntimeID != "agent-run:run-1" || !snapshot.LastSeenAt.Equal(now) || !snapshot.ExpiresAt.Equal(now.Add(90*time.Second)) || !snapshot.Alive {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if _, ok := snapshots["missing-runtime"]; ok {
		t.Fatalf("missing runtime should not have a snapshot: %+v", snapshots)
	}
}

func TestSQLiteStoreSnapshotBatchReturnsDatabaseErrors(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-liveness-snapshot-error"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	live, err := execution.NewLivenessStore(ctx, store)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	if _, err := live.SnapshotBatch(ctx, []string{"runtime-a"}, time.Now()); err == nil {
		t.Fatalf("SnapshotBatch returned nil error after database close")
	}
}

func TestSQLiteStoreTouchHeartbeatReturnsDatabaseErrors(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-liveness-touch-error"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	live, err := execution.NewLivenessStore(ctx, store)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	err = live.TouchHeartbeat(ctx, "runtime-a", time.Minute, time.Now())
	if err == nil {
		t.Fatalf("TouchHeartbeat returned nil error after database close")
	}
}

func TestSQLiteStoreForgetHeartbeatReturnsDatabaseErrors(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-liveness-forget-error"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	live, err := execution.NewLivenessStore(ctx, store)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	err = live.ForgetHeartbeat(ctx, "runtime-a")
	if err == nil {
		t.Fatalf("ForgetHeartbeat returned nil error after database close")
	}
}

func TestSQLiteStoreForgetAndSweepStaleHeartbeats(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-liveness"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	live, err := execution.NewLivenessStore(ctx, store)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	live.Touch(ctx, "runtime-a", time.Minute, now)
	live.Touch(ctx, "runtime-b", time.Minute, now)
	live.Forget(ctx, "runtime-b")
	alive := live.IsAliveBatch(ctx, []string{"runtime-a", "runtime-b"}, now.Add(30*time.Second))
	if !alive["runtime-a"] || alive["runtime-b"] {
		t.Fatalf("alive after forget = %+v", alive)
	}

	removed, err := live.SweepStale(ctx, now.Add(2*time.Minute), 10)
	if err != nil {
		t.Fatalf("SweepStale: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	alive = live.IsAliveBatch(ctx, []string{"runtime-a"}, now.Add(2*time.Minute))
	if alive["runtime-a"] {
		t.Fatalf("runtime-a alive after stale sweep")
	}
}

func TestSQLiteStoreContextCancellationIsNonFatal(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-liveness-cancel"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	live, err := execution.NewLivenessStore(ctx, store)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()

	live.Touch(cancelled, "runtime-a", time.Minute, time.Now())
	alive := live.IsAliveBatch(cancelled, []string{"runtime-a"}, time.Now())
	if len(alive) != 0 {
		t.Fatalf("cancelled IsAliveBatch = %+v, want empty map", alive)
	}
	live.Forget(cancelled, "runtime-a")
}
