package platform_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/eventstore"
	"orrery_v2/internal/platform/execution"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/metrics"
	"orrery_v2/internal/platform/outbox"
	"orrery_v2/internal/platform/tracing"
)

func TestOpenFromYAMLAssemblesProductionInfrastructure(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	if err := os.WriteFile(configPath, []byte(`
environment: test
paths:
  base_dir: .
database:
  urls:
    core: file://./data/core.db
    audit: file://./data/audit.db
    events: file://./data/events.db
    jobs: file://./data/jobs.db
    runtime: file://./data/runtime.db
    observability: file://./data/observability.db
filestore:
  backend: local
  root: ./artifacts
logging:
  console: true
  file: ./logs/platform.jsonl
  level: debug
health:
  readiness_cache_ttl: 1ms
`), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}
	defer rt.Close()

	if rt.Config.Environment != "test" || rt.Logger == nil || rt.ConfigManager == nil || rt.Databases == nil || rt.DBRouter == nil || rt.UnitOfWork == nil || rt.RoutedUOW == nil || rt.EventStore == nil || rt.EventRecorder == nil || rt.FileStore == nil || rt.Artifacts == nil || rt.Execution == nil || rt.ExecutionQueue == nil || rt.ExecutionClaims == nil || rt.ExecutionLive == nil || rt.Lock == nil || rt.Locks == nil || rt.Secrets == nil || rt.OutboxPublisher == nil || rt.Health == nil || rt.Migrations == nil || rt.Metrics == nil || rt.MetricRecorder == nil || rt.MetricsRegistry == nil || rt.Tracer == nil {
		t.Fatalf("runtime missing dependencies: %+v", rt)
	}
	if rt.Execution.Queue == nil || rt.Execution.Claims == nil || rt.Execution.Liveness == nil || rt.Execution.Sweepers == nil || rt.Sweeper != rt.Execution.Sweepers {
		t.Fatalf("runtime execution infra missing or not wired to sweeper: %+v", rt.Execution)
	}
	if err := rt.Migrations.Up(ctx); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	if err := rt.DBRouter.PutRecord(ctx, "audit.audit_log", "event-1", db.Record{"kind": "audit"}); err != nil {
		t.Fatalf("routed put: %v", err)
	}
	if err := rt.EventStore.Append(ctx, eventstore.Envelope{EventID: "evt-1", StreamID: "runtime-stream-1", StreamVersion: 1, EventType: "runtime.created", EventVersion: 1}); err != nil {
		t.Fatalf("eventstore append: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	if err := rt.Outbox.Enqueue(ctx, outbox.Job{ID: "outbox-1", Topic: "projection", NextAttemptAt: now}); err != nil {
		t.Fatalf("outbox enqueue: %v", err)
	}
	if err := rt.Execution.Queue.Enqueue(ctx, execution.Job{ID: "queue-1", Topic: "runtime"}, now); err != nil {
		t.Fatalf("execution enqueue: %v", err)
	}
	if record, ok := mustStore(t, rt.Databases, db.RoleAudit).Get("audit_log", "event-1"); !ok || record["kind"] != "audit" {
		t.Fatalf("audit database record=%+v ok=%v", record, ok)
	}
	if _, ok := mustStore(t, rt.Databases, db.RoleEvents).Get("audit_log", "event-1"); ok {
		t.Fatalf("audit record leaked into events database")
	}
	if _, ok := mustStore(t, rt.Databases, db.RoleCore).Get("audit_log", "event-1"); ok {
		t.Fatalf("routed record leaked into core database")
	}
	events, err := rt.EventStore.Load(ctx, "runtime-stream-1")
	if err != nil {
		t.Fatalf("eventstore load: %v", err)
	}
	if len(events) != 1 || events[0].EventID != "evt-1" {
		t.Fatalf("eventstore events = %+v, want evt-1", events)
	}
	ready := rt.Health.Ready(ctx)
	if !ready.OK {
		t.Fatalf("ready report not ok: %+v", ready)
	}
	metricsRec := httptest.NewRecorder()
	metrics.NewHandler(rt.MetricsRegistry.Gatherer).ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	metricsBody := metricsRec.Body.String()
	for _, want := range []string{
		`orrery_db_open_connections{role="core"}`,
		`orrery_db_open_connections{role="audit"}`,
		`orrery_outbox_jobs{role="jobs",status="pending"} 1`,
		`orrery_execution_jobs{role="jobs",status="queued"} 1`,
	} {
		if !strings.Contains(metricsBody, want) {
			t.Fatalf("runtime metrics missing %q: %s", want, metricsBody)
		}
	}
	for _, path := range []string{
		filepath.Join(root, "data", "core.db"),
		filepath.Join(root, "data", "audit.db"),
		filepath.Join(root, "data", "events.db"),
		filepath.Join(root, "logs", "platform.jsonl"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected runtime path %s: %v", path, err)
		}
	}
}

func TestRuntimeOutboxUsesPlatformClock(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := platformConfigFromYAML(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	now := time.Date(2026, 5, 31, 15, 0, 0, 0, time.UTC)
	rt, err := platform.OpenWithOptions(ctx, cfg, platform.RuntimeOptions{
		Clock: clock.NewFake(now),
	})
	if err != nil {
		t.Fatalf("OpenWithOptions returned error: %v", err)
	}
	defer rt.Close()

	id, err := rt.OutboxPublisher.Publish(ctx, platform.OutboxMessage{Topic: "projection"})
	if err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}

	job, ok := rt.OutboxPublisher.GetStatus(ctx, id)
	if !ok {
		t.Fatalf("GetStatus returned no job")
	}
	if !job.NextAttemptAt.Equal(now) {
		t.Fatalf("NextAttemptAt = %s, want runtime clock %s", job.NextAttemptAt, now)
	}
}

func TestRuntimeOutboxRelayUsesPlatformClockForRetries(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := platformConfigFromYAML(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	now := time.Date(2026, 5, 31, 16, 0, 0, 0, time.UTC)
	rt, err := platform.OpenWithOptions(ctx, cfg, platform.RuntimeOptions{
		Clock: clock.NewFake(now),
	})
	if err != nil {
		t.Fatalf("OpenWithOptions returned error: %v", err)
	}
	defer rt.Close()

	if err := rt.RegisterOutboxDeliveryHandler("projection.retry", func(ctx context.Context, delivery platform.OutboxDelivery) error {
		return errors.New("try again")
	}); err != nil {
		t.Fatalf("RegisterOutboxDeliveryHandler returned error: %v", err)
	}
	id, err := rt.OutboxPublisher.Publish(ctx, platform.OutboxMessage{Topic: "projection.retry", MaxAttempts: 3})
	if err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}
	if err := rt.StartOutboxRelay(ctx, platform.OutboxRelayRuntimeConfig{PollInterval: time.Hour}); err != nil {
		t.Fatalf("StartOutboxRelay returned error: %v", err)
	}
	waitFor(t, time.Second, func() bool {
		job, ok := rt.OutboxPublisher.GetStatus(ctx, id)
		return ok && job.Attempts == 1 && job.Status == platform.OutboxStatusPending
	})

	job, ok := rt.OutboxPublisher.GetStatus(ctx, id)
	if !ok {
		t.Fatalf("GetStatus returned no retry job")
	}
	want := now.Add(time.Second)
	if !job.NextAttemptAt.Equal(want) {
		t.Fatalf("NextAttemptAt = %s, want runtime clock retry %s", job.NextAttemptAt, want)
	}
}

func TestRuntimeHealthUsesPlatformClock(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := platformConfigFromYAML(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	now := time.Date(2026, 5, 31, 17, 30, 0, 0, time.UTC)
	rt, err := platform.OpenWithOptions(ctx, cfg, platform.RuntimeOptions{
		Clock: clock.NewFake(now),
	})
	if err != nil {
		t.Fatalf("OpenWithOptions returned error: %v", err)
	}
	defer rt.Close()

	live := rt.Health.Live(ctx)
	if !live.CheckedAt.Equal(now) {
		t.Fatalf("live CheckedAt = %s, want runtime clock %s", live.CheckedAt, now)
	}
	ready := rt.Health.Ready(ctx)
	if !ready.CheckedAt.Equal(now) {
		t.Fatalf("ready CheckedAt = %s, want runtime clock %s", ready.CheckedAt, now)
	}
}

func TestRuntimeMigrationsUsePlatformClock(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := platformConfigFromYAML(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	now := time.Date(2026, 5, 31, 18, 30, 0, 0, time.UTC)
	rt, err := platform.OpenWithOptions(ctx, cfg, platform.RuntimeOptions{
		Clock: clock.NewFake(now),
	})
	if err != nil {
		t.Fatalf("OpenWithOptions returned error: %v", err)
	}
	defer rt.Close()

	if err := rt.Migrations.Up(ctx); err != nil {
		t.Fatalf("migration up: %v", err)
	}

	var appliedAt int64
	if err := mustStore(t, rt.Databases, db.RoleEvents).QueryRow(ctx, `SELECT applied_at FROM schema_migrations WHERE version = '0001_platform_events'`).Scan(&appliedAt); err != nil {
		t.Fatalf("query migration applied_at: %v", err)
	}
	if appliedAt != now.UnixNano() {
		t.Fatalf("applied_at = %d, want runtime clock %d", appliedAt, now.UnixNano())
	}
}

func TestRuntimeStartsRegisteredOutboxRelayAndStopsOnClose(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	if err := os.WriteFile(configPath, []byte(`
paths:
  base_dir: .
database:
  urls:
    core: file://./data/core.db
    audit: file://./data/audit.db
    events: file://./data/events.db
    jobs: file://./data/jobs.db
    runtime: file://./data/runtime.db
    observability: file://./data/observability.db
filestore:
  backend: local
  root: ./artifacts
logging:
  console: false
  file: ./logs/platform.jsonl
`), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}

	processed := make(chan string, 1)
	if err := rt.RegisterOutboxDeliveryHandler("projection", func(ctx context.Context, delivery platform.OutboxDelivery) error {
		processed <- delivery.ID
		return nil
	}); err != nil {
		_ = rt.Close()
		t.Fatalf("RegisterOutboxDeliveryHandler returned error: %v", err)
	}
	id, err := rt.OutboxPublisher.Publish(ctx, platform.OutboxMessage{Topic: "projection"})
	if err != nil {
		_ = rt.Close()
		t.Fatalf("Publish returned error: %v", err)
	}
	if err := rt.StartOutboxRelay(ctx, platform.OutboxRelayRuntimeConfig{PollInterval: time.Millisecond}); err != nil {
		_ = rt.Close()
		t.Fatalf("StartOutboxRelay returned error: %v", err)
	}

	select {
	case got := <-processed:
		if got != id {
			t.Fatalf("processed job = %q, want %q", got, id)
		}
	case <-time.After(time.Second):
		_ = rt.Close()
		t.Fatalf("outbox relay did not process job")
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
}

func TestRuntimeReadyReportsManagedOutboxRelayUnexpectedExit(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	if err := os.WriteFile(configPath, []byte(`
paths:
  base_dir: .
database:
  urls:
    core: file://./data/core.db
    audit: file://./data/audit.db
    events: file://./data/events.db
    jobs: file://./data/jobs.db
    runtime: file://./data/runtime.db
    observability: file://./data/observability.db
filestore:
  backend: local
  root: ./artifacts
logging:
  console: false
  file: ./logs/platform.jsonl
`), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}
	defer rt.Close()

	runCtx, cancel := context.WithCancel(ctx)
	if err := rt.StartOutboxRelay(runCtx, platform.OutboxRelayRuntimeConfig{PollInterval: time.Millisecond}); err != nil {
		t.Fatalf("StartOutboxRelay returned error: %v", err)
	}
	cancel()
	waitFor(t, time.Second, func() bool {
		_, ok := findReadyCheck(rt.Health.Ready(ctx), "runtime.worker.outbox")
		return ok
	})
	ready := rt.Health.Ready(ctx)
	if check, ok := findReadyCheck(ready, "runtime.worker.outbox"); !ok || check.OK {
		t.Fatalf("runtime.worker.outbox check = %+v ok=%v, want not ready", check, ok)
	}
}

func TestOpenFromYAMLClosesAlreadyOpenedResourcesWhenLaterAssemblyFails(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	badLogPath := filepath.Join(root, "logs-as-file")
	if err := os.WriteFile(badLogPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write bad log path: %v", err)
	}
	configPath := filepath.Join(root, "orrery.yaml")
	if err := os.WriteFile(configPath, []byte(`
paths:
  base_dir: .
database:
  urls:
    core: file://./data/core.db
    audit: file://./data/audit.db
    events: file://./data/events.db
    jobs: file://./data/jobs.db
    runtime: file://./data/runtime.db
    observability: file://./data/observability.db
filestore:
  backend: local
  root: ./artifacts
logging:
  console: false
  file: ./logs-as-file/platform.jsonl
`), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err == nil {
		defer rt.Close()
		t.Fatalf("OpenFromYAML returned nil error")
	}
	if rt != nil {
		t.Fatalf("OpenFromYAML returned runtime on failure: %+v", rt)
	}
	if !strings.Contains(err.Error(), "open configured logger") {
		t.Fatalf("OpenFromYAML error = %v, want open configured logger", err)
	}

	store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + filepath.Join(root, "data", "core.db")})
	if err != nil {
		t.Fatalf("reopen core database after failed assembly: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close reopened database: %v", err)
	}
}

func TestRuntimeReloadComponentsRebuildsTracingRecorderFromCurrentConfig(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts-a", "./logs/a.jsonl", "1ms")
	appendRuntimeYAML(t, configPath, "\ntracing:\n  buffer_size: 1\n")

	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}
	defer rt.Close()

	rt.Traces.RecordSpan(tracing.Span{Name: "first"})
	rt.Traces.RecordSpan(tracing.Span{Name: "dropped"})
	if dropped := rt.Traces.Dropped(); dropped != 1 {
		t.Fatalf("initial tracing dropped = %d, want 1", dropped)
	}

	writeRuntimeYAML(t, configPath, ".", "./artifacts-b", "./logs/b.jsonl", "1ms")
	appendRuntimeYAML(t, configPath, "\ntracing:\n  buffer_size: 2\n")
	if err := rt.ReloadFromFile(ctx, configPath); err != nil {
		t.Fatalf("ReloadFromFile returned error: %v", err)
	}
	if err := rt.ReloadComponents(ctx); err != nil {
		t.Fatalf("ReloadComponents returned error: %v", err)
	}

	rt.Traces.RecordSpan(tracing.Span{Name: "one"})
	rt.Traces.RecordSpan(tracing.Span{Name: "two"})
	if dropped := rt.Traces.Dropped(); dropped != 0 {
		t.Fatalf("reloaded tracing dropped = %d, want 0", dropped)
	}
	spans := rt.Traces.Drain()
	if len(spans) != 2 {
		t.Fatalf("reloaded tracing drained %d spans, want 2", len(spans))
	}
}

func TestRuntimeTracingOptionsUsePlatformClockAndIDGenerator(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := platformConfigFromYAML(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	now := time.Date(2026, 5, 31, 21, 0, 0, 0, time.UTC)
	fakeClock := clock.NewFake(now)
	ids := idgen.NewDeterministic("trace")
	rt, err := platform.OpenWithOptions(ctx, cfg, platform.RuntimeOptions{
		Clock:       fakeClock,
		IDGenerator: ids,
	})
	if err != nil {
		t.Fatalf("OpenWithOptions returned error: %v", err)
	}
	defer rt.Close()

	spanCtx, finish := tracing.StartSpanWithOptions(ctx, "runtime.trace", rt.Traces, rt.TracingOptions)
	if tracing.TraceID(spanCtx) != "trace-000001" || tracing.SpanID(spanCtx) != "trace-000002" {
		t.Fatalf("trace/span IDs = %q/%q, want runtime ID generator", tracing.TraceID(spanCtx), tracing.SpanID(spanCtx))
	}
	fakeClock.Advance(15 * time.Millisecond)
	finish(nil)

	spans := rt.Traces.Drain()
	if len(spans) != 1 || !spans[0].StartedAt.Equal(now) || spans[0].Duration != 15*time.Millisecond {
		t.Fatalf("spans = %+v, want runtime clock timing", spans)
	}
}

func TestRuntimeHTTPMetricsUsePlatformClock(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := platformConfigFromYAML(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	now := time.Date(2026, 5, 31, 22, 0, 0, 0, time.UTC)
	fakeClock := clock.NewFake(now)
	rt, err := platform.OpenWithOptions(ctx, cfg, platform.RuntimeOptions{
		Clock: fakeClock,
	})
	if err != nil {
		t.Fatalf("OpenWithOptions returned error: %v", err)
	}
	defer rt.Close()

	handler := rt.MetricsRegistry.HTTP.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fakeClock.Advance(125 * time.Millisecond)
		w.WriteHeader(http.StatusCreated)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/runtime-metrics", nil)
	req = metrics.WithRoutePattern(req, "/api/runtime-metrics")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	rec := httptest.NewRecorder()
	metrics.NewHandler(rt.MetricsRegistry.Gatherer).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `orrery_http_request_duration_seconds_sum{method="POST",route="/api/runtime-metrics",status="201"} 0.125`) {
		t.Fatalf("metrics body missing runtime clock duration:\n%s", body)
	}
}
