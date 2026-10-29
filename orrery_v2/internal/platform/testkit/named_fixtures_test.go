package testkit_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"orrery_v2/internal/platform/execution"
	"orrery_v2/internal/platform/lock"
	"orrery_v2/internal/platform/logging"
	"orrery_v2/internal/platform/metrics"
	"orrery_v2/internal/platform/outbox"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/tracing"
)

func TestNamedFixturesAreAvailableIndependently(t *testing.T) {
	logger := testkit.NewTestLogger(t)
	consoleFileLogger, consoleLog, fileLog := testkit.NewTestConsoleAndFileLogger()
	cfg := testkit.NewTestConfig(t)
	store := testkit.NewTestDB(t)
	uow := testkit.NewTestUnitOfWork(t, store)
	eventStore := testkit.NewTestEventStore(t, store)
	fileStore := testkit.NewTestFileStore(t)
	queueStore := testkit.NewTestQueue(t, store)
	lockStore := testkit.NewTestLock(t, store)
	claimStore := testkit.NewTestClaimStore(t, store)
	livenessStore := testkit.NewTestLivenessStore(t, store)
	outboxStore := testkit.NewTestOutbox(t, store)
	metricsSink := testkit.NewTestMetricsSink()
	metricsRegistry := testkit.NewTestMetricsRegistry(t)
	traceRecorder := testkit.NewTestSpanRecorder()
	fakeClock := testkit.NewFakeClock()
	ids := testkit.NewDeterministicIDGenerator("case")

	if logger == nil || consoleFileLogger == nil || cfg.DatabaseURL == "" || store == nil || uow == nil || eventStore == nil || fileStore == nil || queueStore == nil || lockStore == nil || claimStore == nil || livenessStore == nil || outboxStore == nil || metricsSink == nil || metricsRegistry == nil || traceRecorder == nil {
		t.Fatalf("one or more named fixtures were nil")
	}
	if _, ok := any(outboxStore).(*outbox.Store); !ok {
		t.Fatalf("outbox fixture has unexpected type")
	}
	metricsSink.Capture(metrics.Event{Name: "fixture"})
	if got := metricsSink.Drain(1); len(got) != 1 || got[0].Name != "fixture" {
		t.Fatalf("metrics sink fixture events = %+v", got)
	}
	traceRecorder.RecordSpan(tracing.Span{Name: "fixture"})
	if got := traceRecorder.Spans(); len(got) != 1 || got[0].Name != "fixture" {
		t.Fatalf("trace recorder spans = %+v", got)
	}
	consoleFileLogger.Info(logging.WithCorrelationID(context.Background(), "test-corr"), "ready", logging.String("operation", "fixture"))
	if !strings.Contains(consoleLog.String(), "operation=fixture") {
		t.Fatalf("console fixture log missing operation: %s", consoleLog.String())
	}
	var record map[string]any
	if err := json.Unmarshal(fileLog.Bytes(), &record); err != nil {
		t.Fatalf("decode file fixture log: %v\n%s", err, fileLog.String())
	}
	if record["operation"] != "fixture" || record["correlation_id"] != "test-corr" {
		t.Fatalf("file fixture log missing fields: %+v", record)
	}
	if fakeClock.Now().IsZero() {
		t.Fatalf("fake clock returned zero")
	}
	if got := ids.NewID(); got != "case-000001" {
		t.Fatalf("id = %q, want case-000001", got)
	}
}

func TestProductionLikeRuntimeFixturesUseSQLiteBackends(t *testing.T) {
	registry := testkit.NewTestDatabases(t)
	defer registry.Close()
	ctx := context.Background()
	now := testkit.NewFakeClock().Now()

	q := testkit.NewTestQueue(t, mustStore(t, registry, "jobs"))
	if err := q.Enqueue(ctx, execution.Job{ID: "job-1", Topic: "runtime"}, now); err != nil {
		t.Fatalf("queue enqueue: %v", err)
	}
	if job, ok, err := q.Claim(ctx, "worker-1", now); err != nil || !ok || job.ID != "job-1" {
		t.Fatalf("queue claim job=%+v ok=%v err=%v", job, ok, err)
	}

	locks := testkit.NewTestLock(t, mustStore(t, registry, "runtime"))
	if _, ok, err := locks.TryAcquire(ctx, "resource", "owner", testkit.NewTestConfig(t).Lock.DefaultTTL, now); err != nil || !ok {
		t.Fatalf("lock acquire ok=%v err=%v", ok, err)
	}
	if _, ok := any(locks).(*lock.Store); !ok {
		t.Fatalf("lock fixture is not database-backed")
	}

	claims := testkit.NewTestClaimStore(t, mustStore(t, registry, "runtime"))
	if ok, err := claims.Acquire(ctx, "run-1", "worker-1", testkit.NewTestConfig(t).Queue.ClaimTTL, now); err != nil || !ok {
		t.Fatalf("claim acquire ok=%v err=%v", ok, err)
	}
	live := testkit.NewTestLivenessStore(t, mustStore(t, registry, "runtime"))
	live.Touch(ctx, "runtime-a", testkit.NewTestConfig(t).Liveness.HeartbeatTTL, now)
	if alive := live.IsAliveBatch(ctx, []string{"runtime-a"}, now); !alive["runtime-a"] {
		t.Fatalf("liveness fixture did not report runtime alive: %+v", alive)
	}
}
