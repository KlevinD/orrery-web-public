package execution_test

import (
	"bytes"
	"context"
	"errors"
	"orrery_v2/internal/platform/execution"
	"orrery_v2/internal/platform/testkit"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/logging"
)

func TestEphemeralQueueClaimsJobOnce(t *testing.T) {
	ctx := context.Background()
	q := testkit.NewEphemeralQueue()
	if err := q.Enqueue(ctx, execution.Job{ID: "job-1", Topic: "runtime"}); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}
	job, ok, err := q.Claim(ctx, "worker-1")
	if err != nil || !ok {
		t.Fatalf("first Claim ok=%v err=%v", ok, err)
	}
	if job.ID != "job-1" || job.WorkerID != "worker-1" {
		t.Fatalf("job = %+v", job)
	}
	_, ok, err = q.Claim(ctx, "worker-2")
	if err != nil {
		t.Fatalf("second Claim err=%v", err)
	}
	if ok {
		t.Fatalf("second Claim got same job")
	}
}

func TestJobRunnerProcessesClaimedJob(t *testing.T) {
	ctx := context.Background()
	q := testkit.NewEphemeralQueue()
	if err := q.Enqueue(ctx, execution.Job{ID: "job-1", Topic: "runtime"}); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}
	runner := testkit.NewEphemeralQueueRunner(q, "worker-1", func(ctx context.Context, job execution.Job) error {
		if job.ID != "job-1" {
			t.Fatalf("job id = %q, want job-1", job.ID)
		}
		return nil
	})
	processed, err := runner.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}
	if !processed {
		t.Fatalf("processed = false, want true")
	}
	completed, ok := q.Completed("job-1")
	if !ok || completed.WorkerID != "worker-1" {
		t.Fatalf("completed ok=%v job=%+v", ok, completed)
	}
}

func TestJobRunnerReturnsFailedJobToQueue(t *testing.T) {
	ctx := context.Background()
	q := testkit.NewEphemeralQueue()
	if err := q.Enqueue(ctx, execution.Job{ID: "job-1", Topic: "runtime"}); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}
	runner := testkit.NewEphemeralQueueRunner(q, "worker-1", func(ctx context.Context, job execution.Job) error {
		return execution.ErrRetryable
	})
	processed, err := runner.RunOnce(ctx)
	if err == nil || !processed {
		t.Fatalf("RunOnce processed=%v err=%v, want retryable error", processed, err)
	}
	job, ok, err := q.Claim(ctx, "worker-2")
	if err != nil || !ok {
		t.Fatalf("Claim after retry ok=%v err=%v", ok, err)
	}
	if job.ID != "job-1" || job.WorkerID != "worker-2" {
		t.Fatalf("retried job = %+v", job)
	}
}

func TestSQLiteQueuePersistsAndClaimsByPriority(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "jobs.db")
	store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	q, err := execution.NewQueue(ctx, store, execution.QueueConfig{ClaimTTL: time.Minute, MaxAttempts: 3})
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	if err := q.Enqueue(ctx, execution.Job{ID: "low", Topic: "runtime", Payload: []byte("low"), Priority: 1}, now); err != nil {
		t.Fatalf("enqueue low: %v", err)
	}
	if err := q.Enqueue(ctx, execution.Job{ID: "high", Topic: "runtime", Payload: []byte("high"), Priority: 10}, now.Add(time.Second)); err != nil {
		t.Fatalf("enqueue high: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer reopened.Close()
	reopenedQ, err := execution.NewQueue(ctx, reopened, execution.QueueConfig{ClaimTTL: time.Minute, MaxAttempts: 3})
	if err != nil {
		t.Fatalf("NewSQLite reopened: %v", err)
	}
	job, ok, err := reopenedQ.Claim(ctx, "worker-a", now.Add(2*time.Second))
	if err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	if job.ID != "high" || job.WorkerID != "worker-a" || string(job.Payload) != "high" {
		t.Fatalf("claimed job = %+v", job)
	}
}

func TestSQLiteQueueRunnerHonorsRetryAfter(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-queue-retry-after"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	q, err := execution.NewQueue(ctx, store, execution.QueueConfig{ClaimTTL: time.Minute, MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	if err := q.Enqueue(ctx, execution.Job{ID: "retry-after", Topic: "runtime"}, now); err != nil {
		t.Fatal(err)
	}
	runner := execution.NewQueueRunner(q, "worker-1", func(context.Context, execution.Job) error {
		return execution.RetryAfter(errors.New("temporary"), time.Minute)
	}, func() time.Time { return now })
	if processed, err := runner.RunOnce(ctx); !processed || err == nil {
		t.Fatalf("run once processed=%v err=%v", processed, err)
	}
	job, ok, err := q.Get(ctx, "retry-after")
	if err != nil || !ok {
		t.Fatalf("get retry job ok=%v err=%v", ok, err)
	}
	if job.Status != execution.StatusQueued || !job.NextAttemptAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("retry job = %+v", job)
	}
	if err := q.Enqueue(ctx, execution.Job{ID: "later-same-key", Topic: "runtime"}, now.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := q.Claim(ctx, "worker-2", now.Add(30*time.Second)); err != nil || ok {
		t.Fatalf("early claim ok=%v err=%v", ok, err)
	}
	claimed, ok, err := q.Claim(ctx, "worker-2", now.Add(time.Minute))
	if err != nil || !ok {
		t.Fatalf("due claim ok=%v err=%v", ok, err)
	}
	if claimed.ID != "retry-after" {
		t.Fatalf("claimed job = %s, want older retry-after", claimed.ID)
	}
}

func TestSQLiteQueueRunnerMarksPermanentFailureWithoutRetry(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-queue-permanent"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	q, err := execution.NewQueue(ctx, store, execution.QueueConfig{ClaimTTL: time.Minute, MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	if err := q.Enqueue(ctx, execution.Job{ID: "permanent", Topic: "runtime"}, now); err != nil {
		t.Fatal(err)
	}
	runner := execution.NewQueueRunner(q, "worker-1", func(context.Context, execution.Job) error {
		return execution.Permanent(errors.New("terminal"))
	}, func() time.Time { return now })
	if processed, err := runner.RunOnce(ctx); !processed || err == nil {
		t.Fatalf("run once processed=%v err=%v", processed, err)
	}
	job, ok, err := q.Get(ctx, "permanent")
	if err != nil || !ok {
		t.Fatalf("get permanent job ok=%v err=%v", ok, err)
	}
	if job.Status != execution.StatusFailed || job.Attempts != 1 || job.FailureReason != "permanent_failure" {
		t.Fatalf("permanent job = %+v", job)
	}
}

func TestSQLiteQueueClaimSkipsQueuedJobWhenSameQueueKeyIsActive(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-queue-key-claim"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	q, err := execution.NewQueue(ctx, store, execution.QueueConfig{ClaimTTL: time.Minute, MaxAttempts: 3})
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	for _, job := range []execution.Job{
		{ID: "discussion-a-1", Topic: "runtime", QueueKey: "discussion:a", Priority: 10},
		{ID: "discussion-a-2", Topic: "runtime", QueueKey: "discussion:a", Priority: 9},
		{ID: "discussion-b-1", Topic: "runtime", QueueKey: "discussion:b", Priority: 1},
	} {
		if err := q.Enqueue(ctx, job, now); err != nil {
			t.Fatalf("enqueue %s: %v", job.ID, err)
		}
	}

	first, ok, err := q.Claim(ctx, "worker-a", now.Add(time.Second))
	if err != nil || !ok {
		t.Fatalf("first claim ok=%v err=%v", ok, err)
	}
	if first.ID != "discussion-a-1" || first.QueueKey != "discussion:a" {
		t.Fatalf("first claimed job = %+v", first)
	}
	second, ok, err := q.Claim(ctx, "worker-b", now.Add(2*time.Second))
	if err != nil || !ok {
		t.Fatalf("second claim ok=%v err=%v", ok, err)
	}
	if second.ID != "discussion-b-1" || second.QueueKey != "discussion:b" {
		t.Fatalf("same queue should wait behind active lane, second claimed = %+v", second)
	}
	if err := q.Start(ctx, first.ID, "worker-a", now.Add(3*time.Second)); err != nil {
		t.Fatalf("start first: %v", err)
	}
	if err := q.Complete(ctx, first.ID, "worker-a", now.Add(4*time.Second)); err != nil {
		t.Fatalf("complete first: %v", err)
	}
	third, ok, err := q.Claim(ctx, "worker-c", now.Add(5*time.Second))
	if err != nil || !ok {
		t.Fatalf("third claim ok=%v err=%v", ok, err)
	}
	if third.ID != "discussion-a-2" {
		t.Fatalf("third claimed job = %+v", third)
	}
}

func TestSQLiteQueueClaimCompleteAndOwnerGuards(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-queue-owner"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	q, err := execution.NewQueue(ctx, store, execution.QueueConfig{ClaimTTL: time.Minute, MaxAttempts: 3})
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	if err := q.Enqueue(ctx, execution.Job{ID: "job-1", Topic: "runtime"}, now); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if _, ok, err := q.Claim(ctx, "worker-a", now); err != nil || !ok {
		t.Fatalf("claim worker-a ok=%v err=%v", ok, err)
	}
	if _, ok, err := q.Claim(ctx, "worker-b", now.Add(10*time.Second)); err != nil || ok {
		t.Fatalf("duplicate claim ok=%v err=%v", ok, err)
	}
	if err := q.Complete(ctx, "job-1", "worker-b", now.Add(20*time.Second)); err == nil {
		t.Fatalf("wrong worker complete returned nil error")
	}
	if err := q.Start(ctx, "job-1", "worker-a", now.Add(15*time.Second)); err != nil {
		t.Fatalf("owner start: %v", err)
	}
	if err := q.Complete(ctx, "job-1", "worker-a", now.Add(20*time.Second)); err != nil {
		t.Fatalf("owner complete: %v", err)
	}
	completed, ok, err := q.Get(ctx, "job-1")
	if err != nil || !ok {
		t.Fatalf("get completed ok=%v err=%v", ok, err)
	}
	if completed.Status != execution.StatusCompleted {
		t.Fatalf("status = %s, want completed", completed.Status)
	}
}

func TestSQLiteQueueClaimStartCompleteLifecycle(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-queue-lifecycle"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	q, err := execution.NewQueue(ctx, store, execution.QueueConfig{ClaimTTL: time.Minute, MaxAttempts: 3})
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	if err := q.Enqueue(ctx, execution.Job{ID: "job-1", Topic: "runtime"}, now); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	claimed, ok, err := q.Claim(ctx, "worker-a", now.Add(time.Second))
	if err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	if claimed.Status != execution.StatusDispatched || claimed.WorkerID != "worker-a" || claimed.DispatchedAt.IsZero() {
		t.Fatalf("claimed job = %+v", claimed)
	}
	if err := q.Complete(ctx, "job-1", "worker-a", now.Add(2*time.Second)); err == nil {
		t.Fatalf("complete before start returned nil error")
	}
	if err := q.Start(ctx, "job-1", "worker-b", now.Add(2*time.Second)); err == nil {
		t.Fatalf("wrong worker start returned nil error")
	}
	if err := q.Start(ctx, "job-1", "worker-a", now.Add(3*time.Second)); err != nil {
		t.Fatalf("owner start: %v", err)
	}
	running, ok, err := q.Get(ctx, "job-1")
	if err != nil || !ok {
		t.Fatalf("get running ok=%v err=%v", ok, err)
	}
	if running.Status != execution.StatusRunning || running.StartedAt.IsZero() {
		t.Fatalf("running job = %+v", running)
	}
	if err := q.Complete(ctx, "job-1", "worker-a", now.Add(4*time.Second)); err != nil {
		t.Fatalf("complete running: %v", err)
	}
	completed, ok, err := q.Get(ctx, "job-1")
	if err != nil || !ok {
		t.Fatalf("get completed ok=%v err=%v", ok, err)
	}
	if completed.Status != execution.StatusCompleted || completed.CompletedAt.IsZero() {
		t.Fatalf("completed job = %+v", completed)
	}
}

func TestSQLiteQueueStatusCountsReportsJobsByStatus(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-queue-status-counts"})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	q, err := execution.NewQueue(ctx, store, execution.QueueConfig{ClaimTTL: time.Minute, MaxAttempts: 3})
	if err != nil {
		t.Fatalf("NewQueue returned error: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	for _, id := range []string{"queued-1", "queued-2", "dispatched-1"} {
		if err := q.Enqueue(ctx, execution.Job{ID: id, Topic: "runtime"}, now); err != nil {
			t.Fatalf("Enqueue %s returned error: %v", id, err)
		}
	}
	if _, ok, err := q.Claim(ctx, "worker-1", now); err != nil || !ok {
		t.Fatalf("Claim ok=%v err=%v", ok, err)
	}

	counts, err := q.StatusCounts(ctx)
	if err != nil {
		t.Fatalf("StatusCounts returned error: %v", err)
	}
	if counts[string(execution.StatusQueued)] != 2 || counts[string(execution.StatusDispatched)] != 1 {
		t.Fatalf("counts = %+v, want queued=2 dispatched=1", counts)
	}
}

func TestSQLiteQueueCancelCoversQueuedDispatchedAndRunning(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-queue-cancel"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	q, err := execution.NewQueue(ctx, store, execution.QueueConfig{ClaimTTL: time.Minute, MaxAttempts: 3})
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	if err := q.Cancel(ctx, "missing", "manual", now); err != nil {
		t.Fatalf("cancel missing job: %v", err)
	}
	for _, job := range []execution.Job{
		{ID: "queued", Topic: "runtime", Priority: 1},
		{ID: "dispatched", Topic: "runtime", QueueKey: "cancel:dispatched", Priority: 3},
		{ID: "running", Topic: "runtime", QueueKey: "cancel:running", Priority: 2},
	} {
		if err := q.Enqueue(ctx, job, now); err != nil {
			t.Fatalf("enqueue %s: %v", job.ID, err)
		}
	}
	if _, ok, err := q.Claim(ctx, "worker-a", now.Add(time.Second)); err != nil || !ok {
		t.Fatalf("claim dispatched ok=%v err=%v", ok, err)
	}
	if _, ok, err := q.Claim(ctx, "worker-a", now.Add(2*time.Second)); err != nil || !ok {
		t.Fatalf("claim running ok=%v err=%v", ok, err)
	}
	if err := q.Start(ctx, "running", "worker-a", now.Add(3*time.Second)); err != nil {
		t.Fatalf("start running: %v", err)
	}

	for _, id := range []string{"queued", "dispatched", "running"} {
		if err := q.Cancel(ctx, id, "manual", now.Add(4*time.Second)); err != nil {
			t.Fatalf("cancel %s: %v", id, err)
		}
		job, ok, err := q.Get(ctx, id)
		if err != nil || !ok {
			t.Fatalf("get %s ok=%v err=%v", id, ok, err)
		}
		if job.Status != execution.StatusCancelled || job.FailureReason != "manual" || job.CompletedAt.IsZero() {
			t.Fatalf("cancelled %s = %+v", id, job)
		}
	}
}

func TestSQLiteQueueCancelTreatsTerminalJobsAsStopped(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-queue-cancel-terminal"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	q, err := execution.NewQueue(ctx, store, execution.QueueConfig{ClaimTTL: time.Minute, MaxAttempts: 3})
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"completed", "failed"} {
		if err := q.Enqueue(ctx, execution.Job{ID: id, Topic: "runtime"}, now); err != nil {
			t.Fatalf("enqueue %s: %v", id, err)
		}
		job, ok, err := q.Claim(ctx, "worker-a", now.Add(time.Second))
		if err != nil || !ok || job.ID != id {
			t.Fatalf("claim %s job=%+v ok=%v err=%v", id, job, ok, err)
		}
		if err := q.Start(ctx, id, "worker-a", now.Add(2*time.Second)); err != nil {
			t.Fatalf("start %s: %v", id, err)
		}
		if id == "completed" {
			err = q.Complete(ctx, id, "worker-a", now.Add(3*time.Second))
		} else {
			err = q.FailPermanently(ctx, id, "worker-a", "failed", now.Add(3*time.Second))
		}
		if err != nil {
			t.Fatalf("finish %s: %v", id, err)
		}
		if err := q.Cancel(ctx, id, "cleanup", now.Add(4*time.Second)); err != nil {
			t.Fatalf("cancel terminal %s: %v", id, err)
		}
	}
}

func TestSQLiteQueueCancelWithResultDistinguishesOutcomes(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-queue-cancel-result"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	q, err := execution.NewQueue(ctx, store, execution.QueueConfig{ClaimTTL: time.Minute, MaxAttempts: 3})
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	now := time.Date(2026, 9, 3, 15, 0, 0, 0, time.UTC)

	result, err := q.CancelWithResult(ctx, "missing", "manual", now)
	if err != nil || result.Disposition != execution.CancelMissing || result.Status != "" {
		t.Fatalf("cancel missing result=%+v err=%v", result, err)
	}
	if err := q.Cancel(ctx, "missing", "legacy cleanup", now.Add(time.Second)); err != nil {
		t.Fatalf("legacy Cancel missing job: %v", err)
	}

	if err := q.Enqueue(ctx, execution.Job{ID: "active", Topic: "runtime"}, now); err != nil {
		t.Fatalf("enqueue active: %v", err)
	}
	result, err = q.CancelWithResult(ctx, "active", "manual", now.Add(time.Second))
	if err != nil || result.Disposition != execution.CancelApplied || result.Status != execution.StatusCancelled {
		t.Fatalf("cancel active result=%+v err=%v", result, err)
	}
	cancelled, ok, err := q.Get(ctx, "active")
	if err != nil || !ok {
		t.Fatalf("get cancelled job ok=%v err=%v", ok, err)
	}
	result, err = q.CancelWithResult(ctx, "active", "replacement reason", now.Add(2*time.Second))
	if err != nil || result.Disposition != execution.CancelAlreadyCancelled || result.Status != execution.StatusCancelled {
		t.Fatalf("cancel already-cancelled result=%+v err=%v", result, err)
	}
	if err := q.Cancel(ctx, "active", "legacy replacement reason", now.Add(3*time.Second)); err != nil {
		t.Fatalf("legacy Cancel cancelled job: %v", err)
	}
	stillCancelled, ok, err := q.Get(ctx, "active")
	if err != nil || !ok {
		t.Fatalf("get idempotently cancelled job ok=%v err=%v", ok, err)
	}
	if stillCancelled.Status != execution.StatusCancelled || stillCancelled.FailureReason != cancelled.FailureReason || !stillCancelled.CompletedAt.Equal(cancelled.CompletedAt) || !stillCancelled.UpdatedAt.Equal(cancelled.UpdatedAt) {
		t.Fatalf("legacy Cancel mutated cancelled job: before=%+v after=%+v", cancelled, stillCancelled)
	}

	terminalStatuses := []execution.Status{execution.StatusCompleted, execution.StatusFailed, execution.StatusDeadLetter}
	for index, status := range terminalStatuses {
		id := string(status)
		if err := q.Enqueue(ctx, execution.Job{ID: id, Topic: "runtime"}, now.Add(time.Duration(index+1)*time.Minute)); err != nil {
			t.Fatalf("enqueue %s: %v", status, err)
		}
		if status == execution.StatusDeadLetter {
			terminalAt := now.Add(time.Duration(index+1)*time.Minute + 3*time.Second)
			if _, err := store.Exec(ctx, `
UPDATE platform_queue_jobs
SET status = ?, last_error = ?, failure_reason = ?, completed_at = ?, updated_at = ?
WHERE id = ?`, string(status), "attempts exhausted", "max_attempts_exhausted", terminalAt.UnixNano(), terminalAt.UnixNano(), id); err != nil {
				t.Fatalf("mark %s terminal: %v", status, err)
			}
		} else {
			job, claimed, err := q.Claim(ctx, "worker-a", now.Add(time.Duration(index+1)*time.Minute+time.Second))
			if err != nil || !claimed || job.ID != id {
				t.Fatalf("claim %s job=%+v claimed=%v err=%v", status, job, claimed, err)
			}
			if err := q.Start(ctx, id, "worker-a", now.Add(time.Duration(index+1)*time.Minute+2*time.Second)); err != nil {
				t.Fatalf("start %s: %v", status, err)
			}
			if status == execution.StatusCompleted {
				err = q.Complete(ctx, id, "worker-a", now.Add(time.Duration(index+1)*time.Minute+3*time.Second))
			} else {
				err = q.FailPermanently(ctx, id, "worker-a", "permanent failure", now.Add(time.Duration(index+1)*time.Minute+3*time.Second))
			}
			if err != nil {
				t.Fatalf("finish %s: %v", status, err)
			}
		}

		before, ok, err := q.Get(ctx, id)
		if err != nil || !ok {
			t.Fatalf("get terminal %s ok=%v err=%v", status, ok, err)
		}
		result, err := q.CancelWithResult(ctx, id, "cleanup", now.Add(10*time.Minute))
		if err != nil || result.Disposition != execution.CancelAlreadyTerminal || result.Status != status {
			t.Fatalf("cancel terminal %s result=%+v err=%v", status, result, err)
		}
		if err := q.Cancel(ctx, id, "legacy cleanup", now.Add(11*time.Minute)); err != nil {
			t.Fatalf("legacy Cancel terminal %s: %v", status, err)
		}
		after, ok, err := q.Get(ctx, id)
		if err != nil || !ok {
			t.Fatalf("get terminal %s after legacy Cancel ok=%v err=%v", status, ok, err)
		}
		if after.Status != before.Status || after.FailureReason != before.FailureReason || !after.CompletedAt.Equal(before.CompletedAt) || !after.UpdatedAt.Equal(before.UpdatedAt) {
			t.Fatalf("legacy Cancel mutated %s job: before=%+v after=%+v", status, before, after)
		}
	}
}

func TestSQLiteQueueRecoveryDifferentiatesDispatchAndRunningTimeouts(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-queue-timeout"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	q, err := execution.NewQueue(ctx, store, execution.QueueConfig{ClaimTTL: time.Minute, MaxAttempts: 3})
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	for _, job := range []execution.Job{
		{ID: "dispatch-timeout", Topic: "runtime", QueueKey: "timeout:dispatch", Priority: 2},
		{ID: "running-timeout", Topic: "runtime", QueueKey: "timeout:running", Priority: 1},
	} {
		if err := q.Enqueue(ctx, job, now); err != nil {
			t.Fatalf("enqueue %s: %v", job.ID, err)
		}
	}
	if _, ok, err := q.Claim(ctx, "worker-a", now); err != nil || !ok {
		t.Fatalf("claim dispatch timeout ok=%v err=%v", ok, err)
	}
	if _, ok, err := q.Claim(ctx, "worker-a", now.Add(time.Second)); err != nil || !ok {
		t.Fatalf("claim running timeout ok=%v err=%v", ok, err)
	}
	if err := q.Start(ctx, "running-timeout", "worker-a", now.Add(2*time.Second)); err != nil {
		t.Fatalf("start running timeout: %v", err)
	}

	recovered, err := q.RecoverExpired(ctx, now.Add(2*time.Minute), 10)
	if err != nil {
		t.Fatalf("RecoverExpired: %v", err)
	}
	if recovered != 2 {
		t.Fatalf("recovered = %d, want 2", recovered)
	}
	dispatchJob, ok, err := q.Get(ctx, "dispatch-timeout")
	if err != nil || !ok {
		t.Fatalf("get dispatch timeout ok=%v err=%v", ok, err)
	}
	if dispatchJob.Status != execution.StatusQueued || dispatchJob.WorkerID != "" {
		t.Fatalf("dispatch timeout job = %+v", dispatchJob)
	}
	if !dispatchJob.DispatchedAt.IsZero() || !dispatchJob.LeaseUntil.IsZero() {
		t.Fatalf("dispatch timeout retained lease fields = %+v", dispatchJob)
	}
	runningJob, ok, err := q.Get(ctx, "running-timeout")
	if err != nil || !ok {
		t.Fatalf("get running timeout ok=%v err=%v", ok, err)
	}
	if runningJob.Status != execution.StatusQueued || runningJob.FailureReason != "timeout" || runningJob.Attempts != 1 {
		t.Fatalf("running timeout job = %+v", runningJob)
	}
	if runningJob.WorkerID != "" || !runningJob.DispatchedAt.IsZero() || !runningJob.StartedAt.IsZero() || !runningJob.CompletedAt.IsZero() {
		t.Fatalf("running timeout retained active fields = %+v", runningJob)
	}
}

func TestSQLiteQueueRecoveryRetriesExpiredRunningJobUntilAttemptsExhausted(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-queue-running-retry-budget"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	q, err := execution.NewQueue(ctx, store, execution.QueueConfig{ClaimTTL: time.Minute, MaxAttempts: 2})
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	if err := q.Enqueue(ctx, execution.Job{ID: "running-timeout", Topic: "runtime"}, now); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, ok, err := q.Claim(ctx, "worker-a", now); err != nil || !ok {
		t.Fatalf("claim first ok=%v err=%v", ok, err)
	}
	if err := q.Start(ctx, "running-timeout", "worker-a", now.Add(time.Second)); err != nil {
		t.Fatalf("start first: %v", err)
	}
	recovered, err := q.RecoverExpired(ctx, now.Add(2*time.Minute), 10)
	if err != nil {
		t.Fatalf("RecoverExpired first returned error: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("first recovered = %d, want 1", recovered)
	}
	retryJob, ok, err := q.Get(ctx, "running-timeout")
	if err != nil || !ok {
		t.Fatalf("get retry job ok=%v err=%v", ok, err)
	}
	if retryJob.Status != execution.StatusQueued || retryJob.Attempts != 1 || retryJob.LastError != "job lease expired" || retryJob.FailureReason != "timeout" {
		t.Fatalf("retry job = %+v, want queued timeout attempt 1", retryJob)
	}
	if retryJob.WorkerID != "" || !retryJob.LeaseUntil.IsZero() || !retryJob.DispatchedAt.IsZero() || !retryJob.StartedAt.IsZero() || !retryJob.CompletedAt.IsZero() {
		t.Fatalf("retry job retained active fields = %+v", retryJob)
	}

	if _, ok, err := q.Claim(ctx, "worker-b", now.Add(3*time.Minute)); err != nil || !ok {
		t.Fatalf("claim retry ok=%v err=%v", ok, err)
	}
	if err := q.Start(ctx, "running-timeout", "worker-b", now.Add(3*time.Minute+time.Second)); err != nil {
		t.Fatalf("start retry: %v", err)
	}
	recovered, err = q.RecoverExpired(ctx, now.Add(5*time.Minute), 10)
	if err != nil {
		t.Fatalf("RecoverExpired second returned error: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("second recovered = %d, want 1", recovered)
	}
	deadJob, ok, err := q.Get(ctx, "running-timeout")
	if err != nil || !ok {
		t.Fatalf("get dead job ok=%v err=%v", ok, err)
	}
	if deadJob.Status != execution.StatusFailed || deadJob.Attempts != 2 || deadJob.LastError != "job lease expired" || deadJob.FailureReason != "timeout" || deadJob.CompletedAt.IsZero() {
		t.Fatalf("dead job = %+v, want failed timeout attempt 2", deadJob)
	}
}

func TestSQLiteQueueExtendLeasePreventsPrematureRunningRecovery(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-queue-extend-lease"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	q, err := execution.NewQueue(ctx, store, execution.QueueConfig{ClaimTTL: time.Minute, MaxAttempts: 3})
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	if err := q.Enqueue(ctx, execution.Job{ID: "long-job", Topic: "runtime"}, now); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, ok, err := q.Claim(ctx, "worker-a", now); err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	if err := q.Start(ctx, "long-job", "worker-a", now.Add(10*time.Second)); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := q.ExtendLease(ctx, "long-job", "worker-b", now.Add(20*time.Second)); err == nil {
		t.Fatalf("wrong worker ExtendLease returned nil error")
	}
	extendedUntil := now.Add(90 * time.Second)
	if err := q.ExtendLease(ctx, "long-job", "worker-a", extendedUntil); err != nil {
		t.Fatalf("ExtendLease returned error: %v", err)
	}

	recovered, err := q.RecoverExpired(ctx, now.Add(80*time.Second), 10)
	if err != nil {
		t.Fatalf("RecoverExpired before extended lease returned error: %v", err)
	}
	if recovered != 0 {
		t.Fatalf("recovered before extended lease = %d, want 0", recovered)
	}
	job, ok, err := q.Get(ctx, "long-job")
	if err != nil || !ok {
		t.Fatalf("get long-job ok=%v err=%v", ok, err)
	}
	if job.Status != execution.StatusRunning || !job.LeaseUntil.Equal(extendedUntil) || !job.UpdatedAt.Equal(extendedUntil) {
		t.Fatalf("job after ExtendLease = %+v, want running with extended lease", job)
	}

	recovered, err = q.RecoverExpired(ctx, now.Add(2*time.Minute), 10)
	if err != nil {
		t.Fatalf("RecoverExpired after extended lease returned error: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered after extended lease = %d, want 1", recovered)
	}
}

func TestSQLiteQueueExpiresOldQueuedJobs(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-queue-ttl"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	q, err := execution.NewQueue(ctx, store, execution.QueueConfig{ClaimTTL: time.Minute, MaxAttempts: 3})
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	if err := q.Enqueue(ctx, execution.Job{ID: "old", Topic: "runtime"}, now.Add(-3*time.Hour)); err != nil {
		t.Fatalf("enqueue old: %v", err)
	}
	if err := q.Enqueue(ctx, execution.Job{ID: "fresh", Topic: "runtime"}, now); err != nil {
		t.Fatalf("enqueue fresh: %v", err)
	}

	expired, err := q.ExpireQueued(ctx, now.Add(-2*time.Hour), 10, "queued_expired", now)
	if err != nil {
		t.Fatalf("ExpireQueued: %v", err)
	}
	if expired != 1 {
		t.Fatalf("expired = %d, want 1", expired)
	}
	oldJob, ok, err := q.Get(ctx, "old")
	if err != nil || !ok {
		t.Fatalf("get old ok=%v err=%v", ok, err)
	}
	if oldJob.Status != execution.StatusFailed || oldJob.FailureReason != "queued_expired" {
		t.Fatalf("old job = %+v", oldJob)
	}
	freshJob, ok, err := q.Get(ctx, "fresh")
	if err != nil || !ok {
		t.Fatalf("get fresh ok=%v err=%v", ok, err)
	}
	if freshJob.Status != execution.StatusQueued {
		t.Fatalf("fresh job = %+v", freshJob)
	}
}

func TestSQLiteQueueRetriesThenFailsWhenAttemptsExhausted(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-queue-retry"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	q, err := execution.NewQueue(ctx, store, execution.QueueConfig{ClaimTTL: time.Minute, MaxAttempts: 2})
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	if err := q.Enqueue(ctx, execution.Job{ID: "job-1", Topic: "runtime"}, now); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, ok, err := q.Claim(ctx, "worker-a", now); err != nil || !ok {
		t.Fatalf("claim first ok=%v err=%v", ok, err)
	}
	if err := q.Start(ctx, "job-1", "worker-a", now.Add(time.Second)); err != nil {
		t.Fatalf("start first: %v", err)
	}
	if err := q.Fail(ctx, "job-1", "worker-a", "retry me", now.Add(10*time.Second)); err != nil {
		t.Fatalf("fail first: %v", err)
	}
	retryJob, ok, err := q.Get(ctx, "job-1")
	if err != nil || !ok {
		t.Fatalf("get retry job ok=%v err=%v", ok, err)
	}
	if retryJob.Status != execution.StatusQueued || retryJob.WorkerID != "" || !retryJob.DispatchedAt.IsZero() || !retryJob.StartedAt.IsZero() || !retryJob.LeaseUntil.IsZero() {
		t.Fatalf("retry job retained active execution fields = %+v", retryJob)
	}
	if _, ok, err := q.Claim(ctx, "worker-b", now.Add(11*time.Second)); err != nil || !ok {
		t.Fatalf("claim retry ok=%v err=%v", ok, err)
	}
	if err := q.Start(ctx, "job-1", "worker-b", now.Add(12*time.Second)); err != nil {
		t.Fatalf("start second: %v", err)
	}
	if err := q.Fail(ctx, "job-1", "worker-b", "dead", now.Add(20*time.Second)); err != nil {
		t.Fatalf("fail second: %v", err)
	}
	job, ok, err := q.Get(ctx, "job-1")
	if err != nil || !ok {
		t.Fatalf("get dead ok=%v err=%v", ok, err)
	}
	if job.Status != execution.StatusFailed || job.Attempts != 2 || job.LastError != "dead" || job.FailureReason != "max_attempts_exhausted" {
		t.Fatalf("job after attempts exhausted = %+v", job)
	}
}

func TestSQLiteQueueRecoversExpiredClaims(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-queue-expired"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	q, err := execution.NewQueue(ctx, store, execution.QueueConfig{ClaimTTL: time.Minute, MaxAttempts: 3})
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	if err := q.Enqueue(ctx, execution.Job{ID: "job-1", Topic: "runtime"}, now); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, ok, err := q.Claim(ctx, "worker-a", now); err != nil || !ok {
		t.Fatalf("claim first ok=%v err=%v", ok, err)
	}

	recovered, err := q.RecoverExpired(ctx, now.Add(2*time.Minute), 10)
	if err != nil {
		t.Fatalf("RecoverExpired: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered = %d, want 1", recovered)
	}
	if job, ok, err := q.Claim(ctx, "worker-b", now.Add(2*time.Minute)); err != nil || !ok || job.WorkerID != "worker-b" {
		t.Fatalf("claim after recovery job=%+v ok=%v err=%v", job, ok, err)
	}
}

func TestSQLiteRunnerProcessesAndCompletesDurableJob(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-runner-complete"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	q, err := execution.NewQueue(ctx, store, execution.QueueConfig{ClaimTTL: time.Minute, MaxAttempts: 3})
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	if err := q.Enqueue(ctx, execution.Job{ID: "job-1", Topic: "runtime", Payload: []byte("payload")}, now); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	runner := execution.NewQueueRunner(q, "worker-1", func(ctx context.Context, job execution.Job) error {
		if job.ID != "job-1" || string(job.Payload) != "payload" {
			t.Fatalf("job = %+v", job)
		}
		return nil
	}, func() time.Time { return now.Add(time.Second) })
	processed, err := runner.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}
	if !processed {
		t.Fatalf("processed = false, want true")
	}
	job, ok, err := q.Get(ctx, "job-1")
	if err != nil || !ok {
		t.Fatalf("get job ok=%v err=%v", ok, err)
	}
	if job.Status != execution.StatusCompleted || job.WorkerID != "worker-1" {
		t.Fatalf("job after runner = %+v", job)
	}
}

func TestSQLiteRunnerProvidesLeaseExtenderToHandler(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-runner-lease-extender"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	q, err := execution.NewQueue(ctx, store, execution.QueueConfig{ClaimTTL: time.Minute, MaxAttempts: 3})
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	if err := q.Enqueue(ctx, execution.Job{ID: "job-1", Topic: "runtime"}, now); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	runAt := now.Add(10 * time.Second)
	runner := execution.NewQueueRunner(q, "worker-1", func(ctx context.Context, job execution.Job) error {
		extend := execution.LeaseExtenderFromContext(ctx)
		if extend == nil {
			t.Fatalf("LeaseExtenderFromContext returned nil")
		}
		if err := extend.ExtendLease(ctx, runAt.Add(3*time.Minute)); err != nil {
			t.Fatalf("ExtendLease returned error: %v", err)
		}
		job, ok, err := q.Get(ctx, job.ID)
		if err != nil || !ok {
			t.Fatalf("get job during handler ok=%v err=%v", ok, err)
		}
		if !job.LeaseUntil.Equal(runAt.Add(3 * time.Minute)) {
			t.Fatalf("LeaseUntil = %s, want %s", job.LeaseUntil, runAt.Add(3*time.Minute))
		}
		return nil
	}, func() time.Time { return runAt })

	processed, err := runner.RunOnce(ctx)
	if err != nil || !processed {
		t.Fatalf("RunOnce processed=%v err=%v", processed, err)
	}
}

func TestSQLiteRunnerAutomaticallyRenewsLeaseForLongRunningHandler(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-runner-lease-heartbeat"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	q, err := execution.NewQueue(ctx, store, execution.QueueConfig{ClaimTTL: 90 * time.Millisecond, MaxAttempts: 3})
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Now().UTC()
	if err := q.Enqueue(ctx, execution.Job{ID: "long-running-job", Topic: "runtime"}, now); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	started := make(chan time.Time, 1)
	release := make(chan struct{})
	runner := execution.NewQueueRunner(q, "worker-1", func(_ context.Context, job execution.Job) error {
		started <- job.LeaseUntil
		<-release
		return nil
	}, time.Now)
	renewalWindowStartedAt := time.Now().UTC()
	result := make(chan error, 1)
	go func() {
		processed, err := runner.RunOnce(ctx)
		if !processed && err == nil {
			err = errors.New("runner did not process job")
		}
		result <- err
	}()

	initialLease := <-started
	deadline := time.Now().Add(time.Second)
	var renewedJob execution.Job
	var renewalObservedAt time.Time
	for time.Now().Before(deadline) {
		job, ok, err := q.Get(ctx, "long-running-job")
		if err != nil || !ok {
			t.Fatalf("get running job ok=%v err=%v", ok, err)
		}
		if job.LeaseUntil.After(initialLease) {
			renewedJob = job
			renewalObservedAt = time.Now().UTC()
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if renewedJob.LeaseUntil.IsZero() {
		t.Fatal("runner did not renew the running job lease")
	}
	if renewedJob.UpdatedAt.Before(renewalWindowStartedAt) || renewedJob.UpdatedAt.After(renewalObservedAt) {
		t.Fatalf("renewal UpdatedAt = %s, want within [%s, %s]", renewedJob.UpdatedAt, renewalWindowStartedAt, renewalObservedAt)
	}
	if !renewedJob.UpdatedAt.Before(renewedJob.LeaseUntil) {
		t.Fatalf("renewed job timestamps = %+v, want UpdatedAt before LeaseUntil", renewedJob)
	}
	if recovered, err := q.RecoverExpired(ctx, initialLease.Add(time.Millisecond), 10); err != nil || recovered != 0 {
		t.Fatalf("RecoverExpired after heartbeat recovered=%d err=%v, want 0", recovered, err)
	}

	close(release)
	if err := <-result; err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}
	job, ok, err := q.Get(ctx, "long-running-job")
	if err != nil || !ok {
		t.Fatalf("get completed job ok=%v err=%v", ok, err)
	}
	if job.Status != execution.StatusCompleted || job.Attempts != 0 {
		t.Fatalf("job after long handler = %+v", job)
	}
}

func TestSQLiteQueueCancellationInterruptsRunningHandler(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-runner-cancel-running"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	q, err := execution.NewQueue(ctx, store, execution.QueueConfig{ClaimTTL: time.Minute, MaxAttempts: 3})
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	if err := q.Enqueue(ctx, execution.Job{ID: "cancel-running-job", Topic: "runtime"}, time.Now().UTC()); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	started := make(chan struct{})
	interrupted := make(chan error, 1)
	runner := execution.NewQueueRunner(q, "worker-1", func(handlerCtx context.Context, _ execution.Job) error {
		close(started)
		<-handlerCtx.Done()
		interrupted <- handlerCtx.Err()
		return handlerCtx.Err()
	}, time.Now)
	result := make(chan error, 1)
	go func() {
		processed, err := runner.RunOnce(ctx)
		if !processed && err == nil {
			err = errors.New("runner did not process job")
		}
		result <- err
	}()

	<-started
	if err := q.Cancel(ctx, "cancel-running-job", "Mission paused", time.Now().UTC()); err != nil {
		t.Fatalf("Cancel returned error: %v", err)
	}
	if err := <-interrupted; !errors.Is(err, context.Canceled) {
		t.Fatalf("handler error = %v, want context.Canceled", err)
	}
	if err := <-result; err != nil {
		t.Fatalf("RunOnce returned error after cancellation: %v", err)
	}
	job, ok, err := q.Get(ctx, "cancel-running-job")
	if err != nil || !ok {
		t.Fatalf("get cancelled job ok=%v err=%v", ok, err)
	}
	if job.Status != execution.StatusCancelled || job.FailureReason != "Mission paused" {
		t.Fatalf("cancelled job = %+v", job)
	}
	if err := q.Cancel(ctx, "cancel-running-job", "Mission paused", time.Now().UTC()); err != nil {
		t.Fatalf("idempotent Cancel returned error: %v", err)
	}
}

func TestSQLiteRunnerLogsClaimResultWithWorkerID(t *testing.T) {
	ctx := logging.WithCorrelationID(context.Background(), "corr-claim")
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-runner-logs"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	q, err := execution.NewQueue(ctx, store, execution.QueueConfig{ClaimTTL: time.Minute, MaxAttempts: 3})
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	if err := q.Enqueue(ctx, execution.Job{ID: "job-1", Topic: "runtime"}, now); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	var logs bytes.Buffer
	runner := execution.NewQueueRunner(q, "worker-1", func(context.Context, execution.Job) error {
		return nil
	}, func() time.Time { return now.Add(time.Second) }).WithLogger(logging.NewJSONLogger(&logs, logging.LevelDebug))

	processed, err := runner.RunOnce(ctx)
	if err != nil || !processed {
		t.Fatalf("RunOnce processed=%v err=%v", processed, err)
	}
	output := logs.String()
	for _, want := range []string{
		`"operation":"queue.claim"`,
		`"worker_id":"worker-1"`,
		`"claim_result":"claimed"`,
		`"job_id":"job-1"`,
		`"correlation_id":"corr-claim"`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("claim log missing %s in %s", want, output)
		}
	}
}

func TestSQLiteRunnerFailsJobForRetryAndTerminalFailure(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-runner-fail"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	q, err := execution.NewQueue(ctx, store, execution.QueueConfig{ClaimTTL: time.Minute, MaxAttempts: 2})
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	if err := q.Enqueue(ctx, execution.Job{ID: "job-1", Topic: "runtime"}, now); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	runAt := now
	runner := execution.NewQueueRunner(q, "worker-1", func(ctx context.Context, job execution.Job) error {
		return errors.New("handler failed")
	}, func() time.Time { return runAt })

	processed, err := runner.RunOnce(ctx)
	if err == nil || !processed {
		t.Fatalf("first RunOnce processed=%v err=%v, want handler error", processed, err)
	}
	job, ok, err := q.Get(ctx, "job-1")
	if err != nil || !ok {
		t.Fatalf("get after first failure ok=%v err=%v", ok, err)
	}
	if job.Status != execution.StatusQueued || job.Attempts != 1 || job.LastError != "handler failed" {
		t.Fatalf("job after first failure = %+v", job)
	}

	runAt = now.Add(time.Second)
	processed, err = runner.RunOnce(ctx)
	if err == nil || !processed {
		t.Fatalf("second RunOnce processed=%v err=%v, want handler error", processed, err)
	}
	job, ok, err = q.Get(ctx, "job-1")
	if err != nil || !ok {
		t.Fatalf("get after second failure ok=%v err=%v", ok, err)
	}
	if job.Status != execution.StatusFailed || job.Attempts != 2 || job.LastError != "handler failed" || job.FailureReason != "max_attempts_exhausted" {
		t.Fatalf("job after terminal failure = %+v", job)
	}
}

func TestSQLiteRunnerReturnsFailureStateErrorWhenHandlerFails(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-runner-fail-state-error"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	q, err := execution.NewQueue(ctx, store, execution.QueueConfig{ClaimTTL: time.Minute, MaxAttempts: 2})
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	if err := q.Enqueue(ctx, execution.Job{ID: "job-1", Topic: "runtime"}, now); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	runner := execution.NewQueueRunner(q, "worker-1", func(ctx context.Context, job execution.Job) error {
		if job.ID != "job-1" {
			t.Fatalf("job = %+v", job)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
		return errors.New("handler failed")
	}, func() time.Time { return now.Add(time.Second) })

	processed, err := runner.RunOnce(ctx)
	if !processed {
		t.Fatalf("processed = false, want true")
	}
	if err == nil {
		t.Fatalf("RunOnce returned nil error")
	}
	got := err.Error()
	for _, want := range []string{"handler failed", "fail sqlite job"} {
		if !strings.Contains(got, want) {
			t.Fatalf("RunOnce error missing %q in %q", want, got)
		}
	}
}
