package outbox_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/logging"
	"orrery_v2/internal/platform/metrics"
	"orrery_v2/internal/platform/outbox"
)

type delayedRetryError struct {
	delay time.Duration
}

func (e delayedRetryError) Error() string             { return "delayed retry" }
func (e delayedRetryError) RetryDelay() time.Duration { return e.delay }

func TestJobLifecycleClaimCompleteRetryAndDeadLetter(t *testing.T) {
	ctx := context.Background()
	store, _ := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	ob := outbox.New(store)
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	if err := ob.Enqueue(ctx, outbox.Job{ID: "job-1", Topic: "projection", Payload: []byte("x"), MaxAttempts: 2, NextAttemptAt: now}); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}
	claimed, ok, err := ob.Claim(ctx, "worker-1", now)
	if err != nil || !ok {
		t.Fatalf("Claim ok=%v err=%v", ok, err)
	}
	if claimed.Status != outbox.StatusRunning || claimed.WorkerID != "worker-1" {
		t.Fatalf("claimed = %+v", claimed)
	}
	if err := ob.Fail(ctx, "job-1", "worker-1", "temporary", now.Add(time.Second)); err != nil {
		t.Fatalf("Fail returned error: %v", err)
	}
	job, _ := ob.Get(ctx, "job-1")
	if job.Status != outbox.StatusPending || job.Attempts != 1 {
		t.Fatalf("after first fail = %+v", job)
	}
	_, _, _ = ob.Claim(ctx, "worker-1", now.Add(2*time.Second))
	if err := ob.Fail(ctx, "job-1", "worker-1", "permanent", now.Add(3*time.Second)); err != nil {
		t.Fatalf("second Fail returned error: %v", err)
	}
	job, _ = ob.Get(ctx, "job-1")
	if job.Status != outbox.StatusDeadLetter {
		t.Fatalf("status = %s, want dead_letter", job.Status)
	}
}

func TestCompleteMarksJobDone(t *testing.T) {
	ctx := context.Background()
	store, _ := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	ob := outbox.New(store)
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	if err := ob.Enqueue(ctx, outbox.Job{ID: "job-1", Topic: "projection", MaxAttempts: 1, NextAttemptAt: now}); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}
	if _, ok, err := ob.Claim(ctx, "worker-1", now); err != nil || !ok {
		t.Fatalf("Claim ok=%v err=%v", ok, err)
	}
	if err := ob.Complete(ctx, "job-1", "worker-1"); err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	job, _ := ob.Get(ctx, "job-1")
	if job.Status != outbox.StatusDone {
		t.Fatalf("status = %s, want done", job.Status)
	}
}

func TestClaimSetsLeaseAndPreventsClaimUntilRecovered(t *testing.T) {
	ctx := context.Background()
	store, _ := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	ob := outbox.New(store).WithLeaseTTL(30 * time.Second)
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	if err := ob.Enqueue(ctx, outbox.Job{ID: "job-1", Topic: "projection", MaxAttempts: 3, NextAttemptAt: now}); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}
	claimed, ok, err := ob.Claim(ctx, "worker-1", now)
	if err != nil || !ok {
		t.Fatalf("Claim ok=%v err=%v", ok, err)
	}
	if !claimed.LockedUntil.Equal(now.Add(30 * time.Second)) {
		t.Fatalf("LockedUntil = %s, want %s", claimed.LockedUntil, now.Add(30*time.Second))
	}
	if _, ok, err := ob.Claim(ctx, "worker-2", now.Add(29*time.Second)); err != nil || ok {
		t.Fatalf("second claim before recovery ok=%v err=%v, want no claim", ok, err)
	}
	recovered, err := ob.RecoverStale(ctx, now.Add(31*time.Second), 10)
	if err != nil {
		t.Fatalf("RecoverStale returned error: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("RecoverStale recovered = %d, want 1", recovered)
	}
	claimed, ok, err = ob.Claim(ctx, "worker-2", now.Add(31*time.Second))
	if err != nil || !ok {
		t.Fatalf("claim after recovery ok=%v err=%v", ok, err)
	}
	if claimed.WorkerID != "worker-2" {
		t.Fatalf("reclaimed worker = %q, want worker-2", claimed.WorkerID)
	}
}

func TestRenewLeaseRequiresCurrentOwnerAndUnexpiredRunningJob(t *testing.T) {
	ctx := context.Background()
	store, _ := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	ob := outbox.New(store).WithLeaseTTL(30 * time.Second)
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	if err := ob.Enqueue(ctx, outbox.Job{ID: "job-renew", Topic: "projection", NextAttemptAt: now}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, ok, err := ob.Claim(ctx, "worker-1", now); err != nil || !ok {
		t.Fatalf("Claim ok=%v err=%v", ok, err)
	}
	if renewed, err := ob.RenewLease(ctx, "job-renew", "worker-2", now.Add(10*time.Second)); err != nil || renewed {
		t.Fatalf("wrong-owner RenewLease renewed=%v err=%v", renewed, err)
	}
	if renewed, err := ob.RenewLease(ctx, "job-renew", "worker-1", now.Add(10*time.Second)); err != nil || !renewed {
		t.Fatalf("owner RenewLease renewed=%v err=%v", renewed, err)
	}
	job, _ := ob.Get(ctx, "job-renew")
	if !job.LockedUntil.Equal(now.Add(40 * time.Second)) {
		t.Fatalf("renewed LockedUntil = %s, want %s", job.LockedUntil, now.Add(40*time.Second))
	}
	if renewed, err := ob.RenewLease(ctx, "job-renew", "worker-1", now.Add(41*time.Second)); err != nil || renewed {
		t.Fatalf("expired RenewLease renewed=%v err=%v", renewed, err)
	}
}

func TestFailRejectsWorkerThatDoesNotOwnLease(t *testing.T) {
	ctx := context.Background()
	store, _ := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	ob := outbox.New(store)
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	if err := ob.Enqueue(ctx, outbox.Job{ID: "job-1", Topic: "projection", MaxAttempts: 3, NextAttemptAt: now}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, ok, err := ob.Claim(ctx, "worker-1", now); err != nil || !ok {
		t.Fatalf("Claim ok=%v err=%v", ok, err)
	}
	if err := ob.Fail(ctx, "job-1", "worker-2", "stale worker", now.Add(time.Second)); err == nil {
		t.Fatal("wrong-worker Fail returned nil error")
	}
	job, ok := ob.Get(ctx, "job-1")
	if !ok || job.Status != outbox.StatusRunning || job.WorkerID != "worker-1" || job.Attempts != 0 {
		t.Fatalf("job after wrong-worker Fail = %+v, ok=%v", job, ok)
	}
}

func TestRecoverStaleConsumesRetryBudgetBeforeDeadLetter(t *testing.T) {
	ctx := context.Background()
	store, _ := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	ob := outbox.New(store).WithLeaseTTL(30 * time.Second)
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	if err := ob.Enqueue(ctx, outbox.Job{ID: "job-1", Topic: "projection", MaxAttempts: 2, NextAttemptAt: now}); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}
	if _, ok, err := ob.Claim(ctx, "worker-1", now); err != nil || !ok {
		t.Fatalf("first Claim ok=%v err=%v", ok, err)
	}
	recovered, err := ob.RecoverStale(ctx, now.Add(time.Minute), 10)
	if err != nil {
		t.Fatalf("first RecoverStale returned error: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("first RecoverStale recovered = %d, want 1", recovered)
	}
	job, _ := ob.Get(ctx, "job-1")
	if job.Status != outbox.StatusPending || job.Attempts != 1 || job.LastError != "outbox lease expired" {
		t.Fatalf("job after first stale recovery = %+v", job)
	}
	if job.WorkerID != "" || !job.LockedUntil.IsZero() || !job.NextAttemptAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("job after first stale recovery retained bad fields = %+v", job)
	}

	if _, ok, err := ob.Claim(ctx, "worker-2", now.Add(time.Minute)); err != nil || !ok {
		t.Fatalf("second Claim ok=%v err=%v", ok, err)
	}
	recovered, err = ob.RecoverStale(ctx, now.Add(2*time.Minute), 10)
	if err != nil {
		t.Fatalf("second RecoverStale returned error: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("second RecoverStale recovered = %d, want 1", recovered)
	}
	job, _ = ob.Get(ctx, "job-1")
	if job.Status != outbox.StatusDeadLetter || job.Attempts != 2 || job.LastError != "outbox lease expired" {
		t.Fatalf("job after attempts exhausted = %+v", job)
	}
	if !job.NextAttemptAt.IsZero() {
		t.Fatalf("dead letter job NextAttemptAt = %s, want zero", job.NextAttemptAt)
	}
}

func TestEnqueueUsesIdempotencyKeyToDeduplicateJobs(t *testing.T) {
	ctx := context.Background()
	store, _ := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	ob := outbox.New(store)
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	if err := ob.Enqueue(ctx, outbox.Job{ID: "job-1", Topic: "projection", IdempotencyKey: "event-1", Payload: []byte("first"), NextAttemptAt: now}); err != nil {
		t.Fatalf("first Enqueue returned error: %v", err)
	}
	if err := ob.Enqueue(ctx, outbox.Job{ID: "job-2", Topic: "projection", IdempotencyKey: "event-1", Payload: []byte("second"), NextAttemptAt: now}); err != nil {
		t.Fatalf("duplicate Enqueue returned error: %v", err)
	}

	claimed, ok, err := ob.Claim(ctx, "worker-1", now)
	if err != nil || !ok {
		t.Fatalf("first Claim ok=%v err=%v", ok, err)
	}
	if claimed.ID != "job-1" || string(claimed.Payload) != "first" {
		t.Fatalf("claimed duplicate job = %+v", claimed)
	}
	if err := ob.Complete(ctx, claimed.ID, "worker-1"); err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if _, ok, err := ob.Claim(ctx, "worker-1", now); err != nil || ok {
		t.Fatalf("second Claim ok=%v err=%v, want no duplicate job", ok, err)
	}
}

func TestEnqueueUsesInjectedClockWhenNextAttemptAtMissing(t *testing.T) {
	ctx := context.Background()
	store, _ := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	now := time.Date(2026, 5, 31, 14, 0, 0, 0, time.UTC)
	ob := outbox.NewWithOptions(store, outbox.StoreOptions{
		Clock: clock.NewFake(now),
	})

	if err := ob.Enqueue(ctx, outbox.Job{ID: "job-1", Topic: "projection"}); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}

	job, ok := ob.Get(ctx, "job-1")
	if !ok {
		t.Fatalf("Get returned no job")
	}
	if !job.NextAttemptAt.Equal(now) {
		t.Fatalf("NextAttemptAt = %s, want injected clock %s", job.NextAttemptAt, now)
	}
}

func TestRelayProcessesJobsAndRetriesFailures(t *testing.T) {
	ctx := context.Background()
	store, _ := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	ob := outbox.New(store).WithLeaseTTL(time.Minute)
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	clock := outbox.StaticClock(now)

	if err := ob.Enqueue(ctx, outbox.Job{ID: "job-1", Topic: "projection", MaxAttempts: 2, NextAttemptAt: now}); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}
	attempts := 0
	relay := outbox.NewRelay(ob, outbox.RelayConfig{
		WorkerID: "relay-1",
		Clock:    clock,
		Backoff:  outbox.FixedBackoff(time.Minute),
		Handlers: map[string]outbox.Handler{
			"projection": func(ctx context.Context, job outbox.Job) error {
				attempts++
				if attempts == 1 {
					return fmt.Errorf("temporary")
				}
				return nil
			},
		},
	})

	processed, err := relay.ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("first ProcessOne processed=%v err=%v", processed, err)
	}
	job, _ := ob.Get(ctx, "job-1")
	if job.Status != outbox.StatusPending || job.Attempts != 1 || !job.NextAttemptAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("after failed relay job=%+v", job)
	}

	clock.Set(now.Add(time.Minute))
	processed, err = relay.ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("second ProcessOne processed=%v err=%v", processed, err)
	}
	job, _ = ob.Get(ctx, "job-1")
	if job.Status != outbox.StatusDone || attempts != 2 {
		t.Fatalf("after successful relay job=%+v attempts=%d", job, attempts)
	}
}

func TestRelayHeartbeatPreventsSecondRelayFromReclaimingBlockingHandler(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	store, _ := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	fakeClock := outbox.StaticClock(now)
	leaseTTL := 300 * time.Millisecond
	ob := outbox.NewWithOptions(store, outbox.StoreOptions{Clock: fakeClock}).WithLeaseTTL(leaseTTL)
	if err := ob.Enqueue(ctx, outbox.Job{ID: "job-heartbeat", Topic: "delivery", NextAttemptAt: now}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseHandler := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseHandler()
	firstHandler := func(ctx context.Context, _ outbox.Job) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	secondCalls := 0
	first := outbox.NewRelay(ob, outbox.RelayConfig{
		WorkerID: "relay-heartbeat-1", Clock: fakeClock,
		Handlers: map[string]outbox.Handler{"delivery": firstHandler},
	})
	second := outbox.NewRelay(ob, outbox.RelayConfig{
		WorkerID: "relay-heartbeat-2", Clock: fakeClock,
		Handlers: map[string]outbox.Handler{"delivery": func(context.Context, outbox.Job) error {
			secondCalls++
			return nil
		}},
	})
	firstResult := make(chan error, 1)
	go func() {
		processed, err := first.ProcessOne(ctx)
		if !processed && err == nil {
			err = errors.New("first relay did not process the job")
		}
		firstResult <- err
	}()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first relay handler did not start")
	}
	claimed, ok := ob.Get(ctx, "job-heartbeat")
	if !ok || claimed.Status != outbox.StatusRunning {
		t.Fatalf("claimed job = %+v, found=%v", claimed, ok)
	}
	initialLease := claimed.LockedUntil
	fakeClock.Set(now.Add(leaseTTL / 2))
	deadline := time.Now().Add(2 * time.Second)
	for {
		renewed, found := ob.Get(ctx, "job-heartbeat")
		if found && renewed.LockedUntil.After(initialLease) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("relay did not renew the blocking handler lease")
		}
		time.Sleep(10 * time.Millisecond)
	}
	probe := initialLease.Add(time.Nanosecond)
	fakeClock.Set(probe)
	if recovered, err := ob.RecoverStale(ctx, probe, 10); err != nil || recovered != 0 {
		t.Fatalf("RecoverStale recovered=%d err=%v while heartbeat was active", recovered, err)
	}
	if processed, err := second.ProcessOne(ctx); err != nil || processed {
		t.Fatalf("second relay ProcessOne processed=%v err=%v", processed, err)
	}
	if secondCalls != 0 {
		t.Fatalf("second relay handler calls = %d, want 0", secondCalls)
	}

	releaseHandler()
	select {
	case err := <-firstResult:
		if err != nil {
			t.Fatalf("first relay: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first relay did not finish")
	}
	completed, _ := ob.Get(ctx, "job-heartbeat")
	if completed.Status != outbox.StatusDone {
		t.Fatalf("completed job = %+v", completed)
	}
}

func TestRelayHandlerTimeoutStopsHeartbeatAndLeavesJobRecoverable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store, _ := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	leaseTTL := 300 * time.Millisecond
	handlerTimeout := 550 * time.Millisecond
	ob := outbox.New(store).WithLeaseTTL(leaseTTL)
	now := time.Now().UTC()
	if err := ob.Enqueue(ctx, outbox.Job{ID: "job-handler-timeout", Topic: "delivery", NextAttemptAt: now}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	entered := make(chan struct{})
	handlerCanceled := make(chan struct{})
	handlerExited := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseHandler := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseHandler()
	first := outbox.NewRelay(ob, outbox.RelayConfig{
		WorkerID:       "relay-timeout-1",
		HandlerTimeout: handlerTimeout,
		Handlers: map[string]outbox.Handler{"delivery": func(ctx context.Context, _ outbox.Job) error {
			close(entered)
			go func() {
				<-ctx.Done()
				close(handlerCanceled)
			}()
			// Deliberately ignore ctx cancellation to model a stuck provider call.
			<-release
			close(handlerExited)
			return nil
		}},
	})
	type processResult struct {
		processed bool
		err       error
	}
	result := make(chan processResult, 1)
	started := time.Now()
	go func() {
		processed, err := first.ProcessOne(ctx)
		result <- processResult{processed: processed, err: err}
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not start")
	}
	select {
	case got := <-result:
		if !got.processed || !errors.Is(got.err, outbox.ErrHandlerTimeout) || !errors.Is(got.err, context.DeadlineExceeded) {
			t.Fatalf("ProcessOne processed=%v err=%v, want handler timeout", got.processed, got.err)
		}
		if elapsed := time.Since(started); elapsed < handlerTimeout {
			t.Fatalf("ProcessOne returned before handler timeout: %s < %s", elapsed, handlerTimeout)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ProcessOne remained blocked after handler timeout")
	}
	select {
	case <-handlerCanceled:
	case <-time.After(time.Second):
		t.Fatal("handler context was not canceled at timeout")
	}
	select {
	case <-handlerExited:
		t.Fatal("handler unexpectedly returned after ignoring cancellation")
	default:
	}

	job, ok := ob.Get(ctx, "job-handler-timeout")
	if !ok || job.Status != outbox.StatusRunning || job.WorkerID != "relay-timeout-1" || job.Attempts != 0 {
		t.Fatalf("timed-out job was finalized: found=%v job=%+v", ok, job)
	}
	// Allow any renewal already in flight when the deadline fired to settle, then
	// prove that no later heartbeat extends the lease.
	time.Sleep(leaseTTL / 2)
	frozen, _ := ob.Get(ctx, "job-handler-timeout")
	time.Sleep(leaseTTL / 2)
	after, _ := ob.Get(ctx, "job-handler-timeout")
	if !after.LockedUntil.Equal(frozen.LockedUntil) {
		t.Fatalf("lease kept renewing after handler timeout: %s -> %s", frozen.LockedUntil, after.LockedUntil)
	}
	if wait := time.Until(after.LockedUntil.Add(20 * time.Millisecond)); wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			t.Fatal("context ended before timed-out lease expired")
		}
	}
	if recovered, err := ob.RecoverStale(ctx, time.Now().UTC(), 10); err != nil || recovered != 1 {
		t.Fatalf("RecoverStale recovered=%d err=%v", recovered, err)
	}

	secondCalls := 0
	second := outbox.NewRelay(ob, outbox.RelayConfig{
		WorkerID: "relay-timeout-2",
		Handlers: map[string]outbox.Handler{"delivery": func(context.Context, outbox.Job) error {
			secondCalls++
			return nil
		}},
	})
	if processed, err := second.ProcessOne(ctx); err != nil || !processed {
		t.Fatalf("second ProcessOne processed=%v err=%v", processed, err)
	}
	recoveredJob, _ := ob.Get(ctx, "job-handler-timeout")
	if recoveredJob.Status != outbox.StatusDone || recoveredJob.Attempts != 1 || secondCalls != 1 {
		t.Fatalf("recovered job=%+v secondCalls=%d", recoveredJob, secondCalls)
	}

	releaseHandler()
	select {
	case <-handlerExited:
	case <-time.After(time.Second):
		t.Fatal("ignored-context handler did not exit after release")
	}
}

func TestRelayCancelsHandlerWithoutFinalizingAfterLeaseLoss(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	store, _ := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	ob := outbox.New(store).WithLeaseTTL(150 * time.Millisecond)
	now := time.Now().UTC()
	if err := ob.Enqueue(ctx, outbox.Job{ID: "job-lease-loss", Topic: "delivery", NextAttemptAt: now}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	entered := make(chan struct{})
	handlerCanceled := make(chan struct{})
	relay := outbox.NewRelay(ob, outbox.RelayConfig{
		WorkerID: "relay-lease-loss",
		Handlers: map[string]outbox.Handler{"delivery": func(ctx context.Context, _ outbox.Job) error {
			close(entered)
			<-ctx.Done()
			close(handlerCanceled)
			return ctx.Err()
		}},
	})
	result := make(chan error, 1)
	go func() {
		_, err := relay.ProcessOne(ctx)
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not start")
	}
	if err := ob.Fail(ctx, "job-lease-loss", "relay-lease-loss", "forced lease loss", time.Now().UTC()); err != nil {
		t.Fatalf("force lease loss: %v", err)
	}
	select {
	case <-handlerCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("handler was not canceled after lease loss")
	}
	select {
	case err := <-result:
		if !errors.Is(err, outbox.ErrLeaseLost) {
			t.Fatalf("ProcessOne error = %v, want ErrLeaseLost", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not return after lease loss")
	}
	job, _ := ob.Get(ctx, "job-lease-loss")
	if job.Status != outbox.StatusPending || job.Attempts != 1 || job.LastError != "forced lease loss" {
		t.Fatalf("job was finalized after lease loss: %+v", job)
	}
}

func TestRelayHonorsHandlerRetryDelay(t *testing.T) {
	ctx := context.Background()
	store, _ := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	ob := outbox.New(store)
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	clock := outbox.StaticClock(now)
	if err := ob.Enqueue(ctx, outbox.Job{ID: "job-delayed", Topic: "delivery", MaxAttempts: 2, NextAttemptAt: now}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	relay := outbox.NewRelay(ob, outbox.RelayConfig{
		WorkerID: "relay-1", Clock: clock, Backoff: outbox.FixedBackoff(time.Second),
		Handlers: map[string]outbox.Handler{"delivery": func(context.Context, outbox.Job) error {
			return delayedRetryError{delay: 7 * time.Minute}
		}},
	})
	if processed, err := relay.ProcessOne(ctx); err != nil || !processed {
		t.Fatalf("ProcessOne processed=%v err=%v", processed, err)
	}
	job, _ := ob.Get(ctx, "job-delayed")
	if !job.NextAttemptAt.Equal(now.Add(7 * time.Minute)) {
		t.Fatalf("NextAttemptAt = %s", job.NextAttemptAt)
	}
}

func TestRelayEmitsLogsAndMetricsForProcessedJobs(t *testing.T) {
	ctx := context.Background()
	store, _ := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	ob := outbox.New(store)
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	clock := outbox.StaticClock(now)
	var logs bytes.Buffer
	sink := metrics.NewBoundedSink(10)

	if err := ob.Enqueue(ctx, outbox.Job{ID: "job-1", Topic: "projection", MaxAttempts: 1, NextAttemptAt: now}); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}
	relay := outbox.NewRelay(ob, outbox.RelayConfig{
		WorkerID: "relay-1",
		Clock:    clock,
		Logger:   logging.NewJSONLogger(&logs, logging.LevelDebug),
		Metrics:  sink,
		Handlers: map[string]outbox.Handler{
			"projection": func(ctx context.Context, job outbox.Job) error {
				return nil
			},
		},
	})
	if processed, err := relay.ProcessOne(ctx); err != nil || !processed {
		t.Fatalf("ProcessOne processed=%v err=%v", processed, err)
	}

	if !strings.Contains(logs.String(), `"operation":"outbox.relay.process"`) || !strings.Contains(logs.String(), `"status":"done"`) {
		t.Fatalf("relay log missing operation/status: %s", logs.String())
	}
	events := sink.Drain(10)
	if len(events) != 1 || events[0].Name != "outbox.job.processed" {
		t.Fatalf("metrics events = %+v", events)
	}
	raw, err := json.Marshal(events[0].Fields)
	if err != nil {
		t.Fatalf("marshal fields: %v", err)
	}
	if !strings.Contains(string(raw), `"topic":"projection"`) || !strings.Contains(string(raw), `"status":"done"`) {
		t.Fatalf("metrics fields = %s", raw)
	}
}

func TestRelayRunnerPollsUntilContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, _ := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	ob := outbox.New(store)
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	clock := outbox.StaticClock(now)
	if err := ob.Enqueue(ctx, outbox.Job{ID: "job-1", Topic: "projection", NextAttemptAt: now}); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}

	processed := make(chan struct{})
	relay := outbox.NewRelay(ob, outbox.RelayConfig{
		WorkerID: "runner-1",
		Clock:    clock,
		Handlers: map[string]outbox.Handler{
			"projection": func(ctx context.Context, job outbox.Job) error {
				close(processed)
				return nil
			},
		},
	})
	errCh := make(chan error, 1)
	go func() {
		errCh <- relay.Run(ctx, outbox.RunnerConfig{PollInterval: time.Millisecond, RecoverBatchSize: 10})
	}()

	select {
	case <-processed:
	case <-time.After(time.Second):
		t.Fatalf("relay runner did not process queued job")
	}
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error after cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("relay runner did not stop after context cancellation")
	}
}

func TestStatusCountsReportsJobsByStatus(t *testing.T) {
	ctx := context.Background()
	store, _ := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	ob := outbox.New(store)
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	for _, id := range []string{"pending-1", "pending-2"} {
		if err := ob.Enqueue(ctx, outbox.Job{ID: id, Topic: "projection", NextAttemptAt: now}); err != nil {
			t.Fatalf("Enqueue %s returned error: %v", id, err)
		}
	}
	if err := ob.Enqueue(ctx, outbox.Job{ID: "running-1", Topic: "projection", NextAttemptAt: now}); err != nil {
		t.Fatalf("Enqueue running returned error: %v", err)
	}
	if _, ok, err := ob.Claim(ctx, "worker-1", now); err != nil || !ok {
		t.Fatalf("Claim ok=%v err=%v", ok, err)
	}

	counts, err := ob.StatusCounts(ctx)
	if err != nil {
		t.Fatalf("StatusCounts returned error: %v", err)
	}
	if counts[string(outbox.StatusPending)] != 2 || counts[string(outbox.StatusRunning)] != 1 {
		t.Fatalf("counts = %+v, want pending=2 running=1", counts)
	}
}

func TestPruneCompletedRetainsRecentAndDeadLetterJobs(t *testing.T) {
	ctx := context.Background()
	store, _ := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	fakeClock := clock.NewFake(now.Add(-40 * 24 * time.Hour))
	ob := outbox.NewWithOptions(store, outbox.StoreOptions{Clock: fakeClock})
	for _, id := range []string{"done-old", "dead-old"} {
		if err := ob.Enqueue(ctx, outbox.Job{ID: id, Topic: "projection", MaxAttempts: 1, NextAttemptAt: fakeClock.Now()}); err != nil {
			t.Fatalf("Enqueue %s: %v", id, err)
		}
		job, ok, err := ob.Claim(ctx, "worker-1", fakeClock.Now())
		if err != nil || !ok || job.ID != id {
			t.Fatalf("Claim %s = %+v ok=%v err=%v", id, job, ok, err)
		}
		if id == "done-old" {
			if err := ob.Complete(ctx, id, "worker-1"); err != nil {
				t.Fatalf("Complete: %v", err)
			}
		} else if err := ob.Fail(ctx, id, "worker-1", "permanent", fakeClock.Now()); err != nil {
			t.Fatalf("Fail: %v", err)
		}
	}
	fakeClock.Advance(40 * 24 * time.Hour)
	if err := ob.Enqueue(ctx, outbox.Job{ID: "done-recent", Topic: "projection", NextAttemptAt: now}); err != nil {
		t.Fatalf("Enqueue recent: %v", err)
	}
	if _, ok, err := ob.Claim(ctx, "worker-2", now); err != nil || !ok {
		t.Fatalf("Claim recent ok=%v err=%v", ok, err)
	}
	if err := ob.Complete(ctx, "done-recent", "worker-2"); err != nil {
		t.Fatalf("Complete recent: %v", err)
	}
	pruned, err := ob.PruneCompleted(ctx, now.Add(-30*24*time.Hour), 100)
	if err != nil || pruned != 1 {
		t.Fatalf("PruneCompleted = %d, %v", pruned, err)
	}
	if _, ok := ob.Get(ctx, "done-old"); ok {
		t.Fatal("old completed job was retained")
	}
	if job, ok := ob.Get(ctx, "dead-old"); !ok || job.Status != outbox.StatusDeadLetter {
		t.Fatalf("dead-letter job = %+v ok=%v", job, ok)
	}
	if job, ok := ob.Get(ctx, "done-recent"); !ok || job.Status != outbox.StatusDone {
		t.Fatalf("recent completed job = %+v ok=%v", job, ok)
	}
}

func TestConcurrentClaimOnlyOneWorkerGetsPendingJob(t *testing.T) {
	ctx := context.Background()
	store, _ := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	ob := outbox.New(store)
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	if err := ob.Enqueue(ctx, outbox.Job{ID: "job-1", Topic: "projection", MaxAttempts: 3, NextAttemptAt: now}); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}

	const workers = 100
	var ready sync.WaitGroup
	ready.Add(workers)
	start := make(chan struct{})
	results := make(chan bool, workers)
	errs := make(chan error, workers)

	for i := 0; i < workers; i++ {
		i := i
		go func() {
			ready.Done()
			<-start
			_, ok, err := ob.Claim(ctx, fmt.Sprintf("worker-%03d", i), now)
			results <- ok
			errs <- err
		}()
	}
	ready.Wait()
	close(start)

	claimed := 0
	for i := 0; i < workers; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("Claim returned error: %v", err)
		}
		if <-results {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("claimed workers = %d, want 1", claimed)
	}
}

func TestOutboxJobCanBeClaimedAfterFileDatabaseReopen(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/outbox.json"
	store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	if err := outbox.New(store).Enqueue(ctx, outbox.Job{ID: "job-1", Topic: "projection", MaxAttempts: 3, NextAttemptAt: now}); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}
	_ = store.Close()

	reopened, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatalf("reopen returned error: %v", err)
	}
	defer reopened.Close()
	job, ok, err := outbox.New(reopened).Claim(ctx, "worker-1", now)
	if err != nil || !ok {
		t.Fatalf("Claim after reopen ok=%v err=%v", ok, err)
	}
	if job.ID != "job-1" || job.WorkerID != "worker-1" {
		t.Fatalf("claimed job = %+v", job)
	}
}

func TestOutboxUsesDedicatedSQLiteSchemaAndOwnerGuards(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	ob := outbox.New(store)
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	if err := ob.Enqueue(ctx, outbox.Job{ID: "job-1", Topic: "projection", Payload: []byte("x"), MaxAttempts: 2, NextAttemptAt: now}); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}

	var count int
	if err := store.QueryRow(ctx, `SELECT COUNT(*) FROM platform_outbox_jobs WHERE id = ?`, "job-1").Scan(&count); err != nil {
		t.Fatalf("query dedicated outbox table: %v", err)
	}
	if count != 1 {
		t.Fatalf("platform_outbox_jobs count = %d, want 1", count)
	}
	if _, ok, err := ob.Claim(ctx, "worker-1", now); err != nil || !ok {
		t.Fatalf("Claim ok=%v err=%v", ok, err)
	}
	if err := ob.Complete(ctx, "job-1", "worker-2"); err == nil {
		t.Fatalf("wrong-worker Complete returned nil error")
	}
	if err := ob.Complete(ctx, "job-1", "worker-1"); err != nil {
		t.Fatalf("owner Complete returned error: %v", err)
	}
}

func TestPostgresOutboxLifecycleAndIdempotency(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{
		Driver: db.DriverPostgres,
		URL:    postgresTestURL(t),
		Name:   "postgres-outbox",
	})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	dropOutboxTables(t, store)

	now := time.Date(2026, 6, 10, 9, 0, 0, 0, time.UTC)
	ob := outbox.New(store).WithLeaseTTL(30 * time.Second)
	if err := ob.Enqueue(ctx, outbox.Job{
		ID:             "job-postgres-1",
		Topic:          "projection",
		Payload:        []byte(`{"source":"postgres"}`),
		IdempotencyKey: "idem-postgres-1",
		MaxAttempts:    2,
		NextAttemptAt:  now,
	}); err != nil {
		t.Fatalf("first Enqueue returned error: %v", err)
	}
	if err := ob.Enqueue(ctx, outbox.Job{
		ID:             "job-postgres-duplicate",
		Topic:          "projection",
		Payload:        []byte(`{"source":"duplicate"}`),
		IdempotencyKey: "idem-postgres-1",
		MaxAttempts:    2,
		NextAttemptAt:  now,
	}); err != nil {
		t.Fatalf("duplicate Enqueue returned error: %v", err)
	}

	claimed, ok, err := ob.Claim(ctx, "worker-postgres", now)
	if err != nil || !ok {
		t.Fatalf("Claim ok=%v err=%v", ok, err)
	}
	if claimed.ID != "job-postgres-1" || string(claimed.Payload) != `{"source":"postgres"}` {
		t.Fatalf("claimed job = %+v", claimed)
	}
	retryAt := now.Add(time.Minute)
	if err := ob.Fail(ctx, claimed.ID, "worker-postgres", "temporary", retryAt); err != nil {
		t.Fatalf("Fail returned error: %v", err)
	}
	job, ok := ob.Get(ctx, claimed.ID)
	if !ok || job.Status != outbox.StatusPending || job.Attempts != 1 || !job.NextAttemptAt.Equal(retryAt) {
		t.Fatalf("job after fail ok=%v job=%+v", ok, job)
	}

	claimed, ok, err = ob.Claim(ctx, "worker-postgres", retryAt)
	if err != nil || !ok {
		t.Fatalf("second Claim ok=%v err=%v", ok, err)
	}
	if err := ob.Complete(ctx, claimed.ID, "worker-postgres"); err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if _, ok, err := ob.Claim(ctx, "worker-postgres", retryAt); err != nil || ok {
		t.Fatalf("post-complete Claim ok=%v err=%v, want no duplicate job", ok, err)
	}
	counts, err := ob.StatusCounts(ctx)
	if err != nil {
		t.Fatalf("StatusCounts returned error: %v", err)
	}
	if counts[string(outbox.StatusDone)] != 1 {
		t.Fatalf("counts = %+v, want one done job", counts)
	}

	if err := ob.Enqueue(ctx, outbox.Job{
		ID:            "job-postgres-stale",
		Topic:         "projection",
		MaxAttempts:   2,
		NextAttemptAt: now,
	}); err != nil {
		t.Fatalf("enqueue stale job: %v", err)
	}
	if claimed, ok, err := ob.Claim(ctx, "worker-postgres-stale", now); err != nil || !ok || claimed.ID != "job-postgres-stale" {
		t.Fatalf("claim stale job = %+v ok=%v err=%v", claimed, ok, err)
	}
	recovered, err := ob.RecoverStale(ctx, now.Add(31*time.Second), 10)
	if err != nil || recovered != 1 {
		t.Fatalf("RecoverStale = %d, %v", recovered, err)
	}
	job, ok = ob.Get(ctx, "job-postgres-stale")
	if !ok || job.Status != outbox.StatusPending || job.Attempts != 1 {
		t.Fatalf("stale job after recovery ok=%v job=%+v", ok, job)
	}
}

func TestOpenReturnsInitializationError(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("Open db returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	if _, err := outbox.Open(ctx, store); err == nil {
		t.Fatalf("Open returned nil error for closed database")
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

func dropOutboxTables(t *testing.T, store *db.Store) {
	t.Helper()
	if store.Dialect() != db.DialectPostgres {
		t.Fatalf("dropOutboxTables requires postgres store, got %s", store.Dialect())
	}
	if _, err := store.Exec(context.Background(), `DROP TABLE IF EXISTS platform_outbox_jobs CASCADE`); err != nil {
		t.Fatalf("drop platform_outbox_jobs: %v", err)
	}
}
