package testkit_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/eventstore"
	"orrery_v2/internal/platform/execution"
	"orrery_v2/internal/platform/outbox"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/tracing"
	"orrery_v2/internal/platform/uow"
)

func TestFixturesCreateReusablePlatformDependencies(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()

	if env.Logger == nil || env.Config.DatabaseURL == "" || env.DB == nil || env.Databases == nil || env.DBRouter == nil || env.UnitOfWork == nil || env.RoutedUOW == nil || env.EventStore == nil || env.EventRecorder == nil || env.Outbox == nil || env.OutboxPublisher == nil || env.FileStore == nil || env.Artifacts == nil || env.Queue == nil || env.ExecutionQueue == nil || env.ExecutionClaims == nil || env.ExecutionLive == nil || env.Lock == nil || env.Locks == nil || env.Claim == nil || env.Liveness == nil || env.Execution == nil || env.Metrics == nil || env.MetricRecorder == nil || env.MetricsRegistry == nil || env.Traces == nil || env.Tracer == nil {
		t.Fatalf("missing fixtures: %+v", env)
	}
	if env.Execution.Queue == nil || env.Execution.Claims == nil || env.Execution.Liveness == nil || env.Execution.Sweepers == nil {
		t.Fatalf("missing execution fixtures: %+v", env.Execution)
	}
	if env.ConsoleLog == nil || env.FileLog == nil || env.LogBuffer != env.FileLog {
		t.Fatalf("missing dual log fixtures: console=%v file=%v logBuffer=%v", env.ConsoleLog, env.FileLog, env.LogBuffer)
	}
	if env.Clock.Now().IsZero() {
		t.Fatalf("fake clock returned zero")
	}
	if got := env.IDGenerator.NewID(); got != "test-000001" {
		t.Fatalf("first deterministic id = %q", got)
	}
}

func TestEnvRoutesEventStoreToEventsDatabase(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()

	if err := env.EventStore.Append(context.Background(), eventstore.Envelope{
		EventID:       "evt-1",
		StreamID:      "stream-1",
		StreamVersion: 1,
		EventType:     "sample.created",
		EventVersion:  1,
	}); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}

	if events, err := env.EventStore.Load(context.Background(), "stream-1"); err != nil || len(events) != 1 {
		t.Fatalf("events db load events=%+v err=%v", events, err)
	}
	if coreEvents, err := eventstore.New(mustStore(t, env.Databases, "core")).Load(context.Background(), "stream-1"); err != nil || len(coreEvents) != 0 {
		t.Fatalf("event leaked into core db events=%+v err=%v", coreEvents, err)
	}
}

func TestEnvStoresUseSharedFakeClock(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	now := env.Clock.Now().UTC()

	if err := env.EventStore.Append(ctx, eventstore.Envelope{
		EventID:       "evt-clock",
		StreamID:      "stream-clock",
		StreamVersion: 1,
		EventType:     "clock.checked",
		EventVersion:  1,
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}
	events, err := env.EventStore.Load(ctx, "stream-clock")
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	if len(events) != 1 || !events[0].CreatedAt.Equal(now) {
		t.Fatalf("event created_at = %+v, want %s", events, now)
	}

	if err := env.Outbox.Enqueue(ctx, outbox.Job{ID: "job-clock", Topic: "clock.checked"}); err != nil {
		t.Fatalf("enqueue outbox: %v", err)
	}
	job, ok := env.Outbox.Get(ctx, "job-clock")
	if !ok || !job.NextAttemptAt.Equal(now) {
		t.Fatalf("outbox job=%+v ok=%v, want next_attempt_at %s", job, ok, now)
	}
}

func TestEnvArtifactStoreUsesSharedFileStore(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()

	ref, err := env.Artifacts.Put(ctx, "report.txt", "text/plain", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("put artifact: %v", err)
	}
	if ref.LocalPath != "" {
		t.Fatalf("artifact ref leaked local path: %+v", ref)
	}
	artifact, err := env.FileStore.Get(ctx, ref.ID)
	if err != nil {
		t.Fatalf("get artifact from shared file store: %v", err)
	}
	if string(artifact.Content) != "hello" {
		t.Fatalf("artifact content = %q", string(artifact.Content))
	}
}

func TestEnvEventRecorderUsesSharedEventStoreAndFakeClock(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	now := env.Clock.Now().UTC()

	id, err := env.EventRecorder.Record(ctx, platform.EventMessage{
		StreamID:       "stream-recorder-fixture",
		StreamVersion:  1,
		EventType:      "fixture.recorded",
		EventVersion:   1,
		Payload:        []byte(`{"fixture":true}`),
		IdempotencyKey: "fixture-event-1",
	})
	if err != nil {
		t.Fatalf("Record returned error: %v", err)
	}
	events, err := env.EventStore.Load(ctx, "stream-recorder-fixture")
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %+v, want one recorded event", events)
	}
	event := events[0]
	if event.EventID != id || event.EventType != "fixture.recorded" || string(event.Payload) != `{"fixture":true}` {
		t.Fatalf("event = %+v", event)
	}
	if !event.CreatedAt.Equal(now) {
		t.Fatalf("CreatedAt = %s, want fake clock %s", event.CreatedAt, now)
	}
}

func TestEnvOutboxPublisherUsesSharedOutboxStoreAndFakeClock(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()

	id, err := env.OutboxPublisher.Publish(ctx, platform.OutboxMessage{
		Topic:          "fixture.published",
		Payload:        []byte(`{"ok":true}`),
		IdempotencyKey: "fixture-1",
		Delay:          3 * time.Minute,
		MaxAttempts:    4,
	})
	if err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}
	job, ok := env.Outbox.Get(ctx, id)
	if !ok {
		t.Fatalf("outbox job %q not found through shared store", id)
	}
	if job.Topic != "fixture.published" || string(job.Payload) != `{"ok":true}` || job.IdempotencyKey != "fixture-1" {
		t.Fatalf("job = %+v", job)
	}
	if job.MaxAttempts != 4 {
		t.Fatalf("job MaxAttempts = %d, want 4", job.MaxAttempts)
	}
	if !job.NextAttemptAt.Equal(env.Clock.Now().Add(3 * time.Minute)) {
		t.Fatalf("job NextAttemptAt = %s, want fake clock plus delay", job.NextAttemptAt)
	}
}

func TestEnvClaimStoreUsesSharedFakeClock(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	now := env.Clock.Now().UTC()

	if err := env.Claim.Bump(ctx, "runtime-clock"); err != nil {
		t.Fatalf("bump claim cache: %v", err)
	}

	var updatedAt int64
	if err := mustStore(t, env.Databases, "runtime").QueryRow(ctx, `SELECT updated_at FROM platform_negative_claim_cache WHERE cache_key = ?`, "runtime-clock").Scan(&updatedAt); err != nil {
		t.Fatalf("query claim cache: %v", err)
	}
	if updatedAt != now.UnixNano() {
		t.Fatalf("claim cache updated_at = %d, want %d", updatedAt, now.UnixNano())
	}
}

func TestEnvExecutionClaimsUseSharedFakeClockAndDefaultTTL(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()

	ok, err := env.ExecutionClaims.Acquire(ctx, "run-clock", "worker-a")
	if err != nil || !ok {
		t.Fatalf("first claim acquire ok=%v err=%v", ok, err)
	}
	ok, err = env.ExecutionClaims.Acquire(ctx, "run-clock", "worker-b")
	if err != nil || ok {
		t.Fatalf("second claim acquire before fake-clock expiry ok=%v err=%v", ok, err)
	}
	env.Clock.Advance(env.Config.Queue.ClaimTTL + time.Second)
	ok, err = env.ExecutionClaims.Acquire(ctx, "run-clock", "worker-b")
	if err != nil || !ok {
		t.Fatalf("second claim acquire after fake-clock expiry ok=%v err=%v", ok, err)
	}
}

func TestEnvExecutionLivenessUsesSharedFakeClockAndDefaultTTL(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()

	if err := env.ExecutionLive.Touch(ctx, "runtime-clock"); err != nil {
		t.Fatalf("touch liveness: %v", err)
	}
	alive, err := env.ExecutionLive.IsAlive(ctx, "runtime-clock")
	if err != nil || !alive {
		t.Fatalf("alive immediately after touch = %v err=%v", alive, err)
	}
	env.Clock.Advance(env.Config.Liveness.HeartbeatTTL + time.Second)
	alive, err = env.ExecutionLive.IsAlive(ctx, "runtime-clock")
	if err != nil || alive {
		t.Fatalf("alive after fake-clock expiry = %v err=%v", alive, err)
	}
}

func TestEnvTracerUsesSharedRecorderClockAndIDGenerator(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	now := env.Clock.Now().UTC()

	spanCtx, finish := env.Tracer.Start(ctx, "fixture.tracer")
	if tracing.TraceID(spanCtx) != "test-trace-000001" || tracing.SpanID(spanCtx) != "test-trace-000002" {
		t.Fatalf("trace/span IDs = %q/%q", tracing.TraceID(spanCtx), tracing.SpanID(spanCtx))
	}
	env.Clock.Advance(15 * time.Millisecond)
	finish(nil)

	spans := env.Traces.Spans()
	if len(spans) != 1 || spans[0].Name != "fixture.tracer" || !spans[0].StartedAt.Equal(now) || spans[0].Duration != 15*time.Millisecond {
		t.Fatalf("spans = %+v, want fixture tracer span", spans)
	}
}

func TestEnvMetricRecorderUsesSharedBoundedSink(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()

	if ok := env.MetricRecorder.Capture("fixture.metric", map[string]any{"kind": "test"}); !ok {
		t.Fatalf("Capture returned false")
	}
	events := env.Metrics.Drain(1)
	if len(events) != 1 || events[0].Name != "fixture.metric" || events[0].Fields["kind"] != "test" {
		t.Fatalf("events = %+v, want fixture metric", events)
	}
}

func TestEnvLockFacadeUsesSharedFakeClock(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()

	if _, ok, err := env.Locks.TryAcquire(ctx, "clocked-lock", "owner-a"); err != nil || !ok {
		t.Fatalf("first lock acquire ok=%v err=%v", ok, err)
	}
	if _, ok, err := env.Locks.TryAcquire(ctx, "clocked-lock", "owner-b"); err != nil || ok {
		t.Fatalf("second lock acquire before fake-clock expiry ok=%v err=%v", ok, err)
	}
	env.Clock.Advance(env.Config.Lock.DefaultTTL + time.Second)
	if _, ok, err := env.Locks.TryAcquire(ctx, "clocked-lock", "owner-b"); err != nil || !ok {
		t.Fatalf("second lock acquire after fake-clock expiry ok=%v err=%v", ok, err)
	}
}

func TestEnvExecutionQueueUsesSharedFakeClock(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	now := env.Clock.Now().UTC()

	if err := env.ExecutionQueue.Enqueue(ctx, execution.Job{ID: "exec-clock", Topic: "clock.checked"}); err != nil {
		t.Fatalf("enqueue execution job: %v", err)
	}
	job, ok, err := env.ExecutionQueue.Get(ctx, "exec-clock")
	if err != nil {
		t.Fatalf("get execution job: %v", err)
	}
	if !ok || !job.CreatedAt.Equal(now) || !job.UpdatedAt.Equal(now) {
		t.Fatalf("execution job=%+v ok=%v, want runtime clock %s", job, ok, now)
	}
}

func TestEnvExecutionQueueSubmitUsesSharedQueueAndFakeClock(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	now := env.Clock.Now().UTC()

	id, err := env.ExecutionQueue.Submit(ctx, execution.Message{
		Topic:       "fixture.submitted",
		Payload:     []byte(`{"fixture":true}`),
		Priority:    3,
		MaxAttempts: 2,
	})
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	job, ok, err := env.Queue.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if !ok {
		t.Fatalf("execution job %q not found through shared queue", id)
	}
	if job.Topic != "fixture.submitted" || string(job.Payload) != `{"fixture":true}` {
		t.Fatalf("job = %+v", job)
	}
	if job.Priority != 3 || job.MaxAttempts != 2 {
		t.Fatalf("job priority/max attempts = %d/%d, want 3/2", job.Priority, job.MaxAttempts)
	}
	if !job.CreatedAt.Equal(now) || !job.UpdatedAt.Equal(now) {
		t.Fatalf("execution job=%+v, want fake clock %s", job, now)
	}
}

func TestEnvUnitOfWorkUsesSharedFakeClockForTxStores(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	now := env.Clock.Now().UTC()

	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := tx.EventStore().Append(ctx, eventstore.Envelope{
			EventID:       "evt-uow-clock",
			StreamID:      "stream-uow-clock",
			StreamVersion: 1,
			EventType:     "clock.checked",
			EventVersion:  1,
		}); err != nil {
			return err
		}
		return tx.Outbox().Enqueue(ctx, outbox.Job{ID: "job-uow-clock", Topic: "clock.checked"})
	}); err != nil {
		t.Fatalf("UnitOfWork.Do returned error: %v", err)
	}

	events, err := eventstore.New(mustStore(t, env.Databases, "core")).Load(ctx, "stream-uow-clock")
	if err != nil {
		t.Fatalf("load tx events: %v", err)
	}
	if len(events) != 1 || !events[0].CreatedAt.Equal(now) {
		t.Fatalf("uow events = %+v, want CreatedAt %s", events, now)
	}
	job, ok := outbox.New(mustStore(t, env.Databases, "core")).Get(ctx, "job-uow-clock")
	if !ok || !job.NextAttemptAt.Equal(now) {
		t.Fatalf("uow outbox job=%+v ok=%v, want NextAttemptAt %s", job, ok, now)
	}
}

func mustStore(t *testing.T, registry *db.Registry, role string) *db.Store {
	t.Helper()
	store, err := registry.Store(role)
	if err != nil {
		t.Fatalf("lookup %s database: %v", role, err)
	}
	return store
}
