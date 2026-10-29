package execution_test

import (
	"context"
	"os"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/execution"
)

func TestPostgresQueueLifecycleAndRecovery(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{
		Driver: db.DriverPostgres,
		URL:    postgresTestURL(t),
		Name:   "postgres-execution-queue",
	})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	dropExecutionTables(t, store, "platform_queue_jobs")

	q, err := execution.NewQueue(ctx, store, execution.QueueConfig{ClaimTTL: time.Minute, MaxAttempts: 2, SweepBatchSize: 10})
	if err != nil {
		t.Fatalf("NewQueue returned error: %v", err)
	}
	now := time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC)
	for _, job := range []execution.Job{
		{ID: "job-postgres-a1", Topic: "runtime", QueueKey: "lane-a", Priority: 10, Payload: []byte("a1")},
		{ID: "job-postgres-a2", Topic: "runtime", QueueKey: "lane-a", Priority: 9, Payload: []byte("a2")},
		{ID: "job-postgres-b1", Topic: "runtime", QueueKey: "lane-b", Priority: 1, Payload: []byte("b1")},
	} {
		if err := q.Enqueue(ctx, job, now); err != nil {
			t.Fatalf("Enqueue %s returned error: %v", job.ID, err)
		}
	}

	first, ok, err := q.Claim(ctx, "worker-a", now.Add(time.Second))
	if err != nil || !ok {
		t.Fatalf("first Claim ok=%v err=%v", ok, err)
	}
	if first.ID != "job-postgres-a1" || string(first.Payload) != "a1" {
		t.Fatalf("first claimed job = %+v", first)
	}
	second, ok, err := q.Claim(ctx, "worker-b", now.Add(2*time.Second))
	if err != nil || !ok {
		t.Fatalf("second Claim ok=%v err=%v", ok, err)
	}
	if second.ID != "job-postgres-b1" {
		t.Fatalf("second claimed job = %+v, want lane-b while lane-a active", second)
	}
	if err := q.Start(ctx, second.ID, "worker-b", now.Add(3*time.Second)); err != nil {
		t.Fatalf("Start second returned error: %v", err)
	}
	if err := q.Complete(ctx, second.ID, "worker-b", now.Add(4*time.Second)); err != nil {
		t.Fatalf("Complete second returned error: %v", err)
	}
	if err := q.Start(ctx, first.ID, "worker-a", now.Add(3*time.Second)); err != nil {
		t.Fatalf("Start first returned error: %v", err)
	}
	if err := q.Complete(ctx, first.ID, "worker-a", now.Add(4*time.Second)); err != nil {
		t.Fatalf("Complete first returned error: %v", err)
	}

	retry, ok, err := q.Claim(ctx, "worker-c", now.Add(5*time.Second))
	if err != nil || !ok {
		t.Fatalf("retry Claim ok=%v err=%v", ok, err)
	}
	if retry.ID != "job-postgres-a2" {
		t.Fatalf("retry claimed job = %+v, want second lane-a job after completion", retry)
	}
	if err := q.Start(ctx, retry.ID, "worker-c", now.Add(6*time.Second)); err != nil {
		t.Fatalf("Start retry returned error: %v", err)
	}
	if err := q.Fail(ctx, retry.ID, "worker-c", "temporary", now.Add(7*time.Second)); err != nil {
		t.Fatalf("Fail retry returned error: %v", err)
	}
	retryJob, ok, err := q.Get(ctx, retry.ID)
	if err != nil || !ok {
		t.Fatalf("Get retry ok=%v err=%v", ok, err)
	}
	if retryJob.Status != execution.StatusQueued || retryJob.Attempts != 1 || retryJob.WorkerID != "" {
		t.Fatalf("retry job = %+v, want queued attempt 1 without worker", retryJob)
	}

	expiring, ok, err := q.Claim(ctx, "worker-d", now.Add(8*time.Second))
	if err != nil || !ok {
		t.Fatalf("expiring Claim ok=%v err=%v", ok, err)
	}
	if err := q.Start(ctx, expiring.ID, "worker-d", now.Add(9*time.Second)); err != nil {
		t.Fatalf("Start expiring returned error: %v", err)
	}
	recovered, err := q.RecoverExpired(ctx, now.Add(2*time.Minute), 10)
	if err != nil {
		t.Fatalf("RecoverExpired returned error: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("RecoverExpired recovered = %d, want 1", recovered)
	}
	deadJob, ok, err := q.Get(ctx, expiring.ID)
	if err != nil || !ok {
		t.Fatalf("Get dead job ok=%v err=%v", ok, err)
	}
	if deadJob.Status != execution.StatusFailed || deadJob.Attempts != 2 || deadJob.FailureReason != "timeout" {
		t.Fatalf("dead job = %+v, want failed timeout attempt 2", deadJob)
	}
	counts, err := q.StatusCounts(ctx)
	if err != nil {
		t.Fatalf("StatusCounts returned error: %v", err)
	}
	if counts[string(execution.StatusCompleted)] != 2 || counts[string(execution.StatusFailed)] != 1 {
		t.Fatalf("counts = %+v, want completed=2 failed=1", counts)
	}
}

func TestPostgresClaimStoreLeasesAndNegativeCache(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{
		Driver: db.DriverPostgres,
		URL:    postgresTestURL(t),
		Name:   "postgres-execution-claims",
	})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	dropExecutionTables(t, store, "platform_claim_leases", "platform_negative_claim_cache")

	claims, err := execution.NewClaimStore(ctx, store)
	if err != nil {
		t.Fatalf("NewClaimStore returned error: %v", err)
	}
	now := time.Date(2026, 6, 11, 11, 0, 0, 0, time.UTC)
	if ok, err := claims.Acquire(ctx, "run-postgres-1", "worker-a", time.Minute, now); err != nil || !ok {
		t.Fatalf("first Acquire ok=%v err=%v", ok, err)
	}
	if ok, err := claims.Acquire(ctx, "run-postgres-1", "worker-b", time.Minute, now.Add(30*time.Second)); err != nil || ok {
		t.Fatalf("second Acquire before expiry ok=%v err=%v, want false", ok, err)
	}
	if ok, err := claims.Renew(ctx, "run-postgres-1", "worker-a", 2*time.Minute, now.Add(45*time.Second)); err != nil || !ok {
		t.Fatalf("owner Renew ok=%v err=%v", ok, err)
	}
	if ok, err := claims.Release(ctx, "run-postgres-1", "worker-b"); err != nil || ok {
		t.Fatalf("wrong-owner Release ok=%v err=%v, want false", ok, err)
	}
	if ok, err := claims.Release(ctx, "run-postgres-1", "worker-a"); err != nil || !ok {
		t.Fatalf("owner Release ok=%v err=%v", ok, err)
	}

	if ok, err := claims.Acquire(ctx, "run-postgres-stale", "worker-a", time.Minute, now); err != nil || !ok {
		t.Fatalf("stale Acquire ok=%v err=%v", ok, err)
	}
	recovered, err := claims.RecoverStale(ctx, now.Add(2*time.Minute), 10)
	if err != nil {
		t.Fatalf("RecoverStale returned error: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("RecoverStale recovered = %d, want 1", recovered)
	}

	version, err := claims.CurrentVersion(ctx, "runtime-postgres")
	if err != nil {
		t.Fatalf("CurrentVersion returned error: %v", err)
	}
	if err := claims.MarkEmpty(ctx, "runtime-postgres", version); err != nil {
		t.Fatalf("MarkEmpty returned error: %v", err)
	}
	if empty, err := claims.IsEmpty(ctx, "runtime-postgres"); err != nil || !empty {
		t.Fatalf("IsEmpty before bump empty=%v err=%v", empty, err)
	}
	if err := claims.Bump(ctx, "runtime-postgres"); err != nil {
		t.Fatalf("Bump returned error: %v", err)
	}
	if empty, err := claims.IsEmpty(ctx, "runtime-postgres"); err != nil || empty {
		t.Fatalf("IsEmpty after bump empty=%v err=%v, want false", empty, err)
	}
}

func TestPostgresLivenessHeartbeatLifecycle(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{
		Driver: db.DriverPostgres,
		URL:    postgresTestURL(t),
		Name:   "postgres-execution-liveness",
	})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	dropExecutionTables(t, store, "platform_liveness")

	live, err := execution.NewLivenessStore(ctx, store)
	if err != nil {
		t.Fatalf("NewLivenessStore returned error: %v", err)
	}
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	if err := live.TouchHeartbeat(ctx, "runtime-postgres", time.Minute, now); err != nil {
		t.Fatalf("TouchHeartbeat returned error: %v", err)
	}
	alive := live.IsAliveBatch(ctx, []string{"runtime-postgres", "runtime-missing"}, now.Add(30*time.Second))
	if !alive["runtime-postgres"] || alive["runtime-missing"] {
		t.Fatalf("alive = %+v, want only runtime-postgres", alive)
	}
	snapshots, err := live.SnapshotBatch(ctx, []string{"runtime-postgres"}, now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, ok := snapshots["runtime-postgres"]
	if !ok || !snapshot.LastSeenAt.Equal(now) || !snapshot.ExpiresAt.Equal(now.Add(time.Minute)) || !snapshot.Alive {
		t.Fatalf("snapshot ok=%v value=%+v", ok, snapshot)
	}
	removed, err := live.SweepStale(ctx, now.Add(2*time.Minute), 10)
	if err != nil {
		t.Fatalf("SweepStale returned error: %v", err)
	}
	if removed != 1 {
		t.Fatalf("SweepStale removed = %d, want 1", removed)
	}
	if alive := live.IsAliveBatch(ctx, []string{"runtime-postgres"}, now.Add(2*time.Minute)); alive["runtime-postgres"] {
		t.Fatalf("runtime-postgres remained alive after sweep: %+v", alive)
	}
	if err := live.TouchHeartbeat(ctx, "runtime-postgres-forget", time.Minute, now); err != nil {
		t.Fatalf("TouchHeartbeat for forget returned error: %v", err)
	}
	if err := live.ForgetHeartbeat(ctx, "runtime-postgres-forget"); err != nil {
		t.Fatalf("ForgetHeartbeat returned error: %v", err)
	}
	if alive := live.IsAliveBatch(ctx, []string{"runtime-postgres-forget"}, now.Add(30*time.Second)); alive["runtime-postgres-forget"] {
		t.Fatalf("forgotten runtime remained alive: %+v", alive)
	}
}

func postgresTestURL(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("ORRERY_TEST_POSTGRES_URL")
	if raw == "" {
		t.Skip("ORRERY_TEST_POSTGRES_URL is not set")
	}
	return raw
}

func dropExecutionTables(t *testing.T, store *db.Store, tables ...string) {
	t.Helper()
	if store.Dialect() != db.DialectPostgres {
		t.Fatalf("dropExecutionTables requires postgres store, got %s", store.Dialect())
	}
	for _, table := range tables {
		if _, err := store.Exec(context.Background(), `DROP TABLE IF EXISTS `+table+` CASCADE`); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
}
