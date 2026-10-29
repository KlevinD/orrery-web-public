package testkit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/eventstore"
	"orrery_v2/internal/platform/execution"
	"orrery_v2/internal/platform/filestore"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/lock"
	"orrery_v2/internal/platform/logging"
	"orrery_v2/internal/platform/metrics"
	"orrery_v2/internal/platform/outbox"
	"orrery_v2/internal/platform/tracing"
	"orrery_v2/internal/platform/uow"
)

type Env struct {
	Logger          logging.Logger
	LogBuffer       *bytes.Buffer
	ConsoleLog      *bytes.Buffer
	FileLog         *bytes.Buffer
	Config          config.Config
	DB              *db.Store
	Databases       *db.Registry
	DBRouter        *db.Router
	UnitOfWork      *uow.UnitOfWork
	RoutedUOW       *uow.RoutedUnitOfWork
	EventStore      *eventstore.Store
	EventRecorder   *platform.EventRecorder
	Outbox          *outbox.Store
	OutboxPublisher *platform.OutboxPublisher
	FileStore       *filestore.Local
	Artifacts       *platform.ArtifactStore
	Queue           *execution.Queue
	ExecutionQueue  *execution.QueueService
	ExecutionClaims *execution.ClaimsService
	ExecutionLive   *execution.LivenessService
	Execution       *execution.Infra
	Lock            *lock.Store
	Locks           *platform.LockManager
	Claim           *execution.ClaimStore
	Liveness        *execution.LivenessStore
	Metrics         *metrics.BoundedSink
	MetricRecorder  *platform.MetricRecorder
	MetricsRegistry *metrics.Registry
	Traces          *SpanRecorder
	Tracer          *platform.Tracer
	Clock           *clock.Fake
	IDGenerator     *idgen.Deterministic
}

func New(t testing.TB) *Env {
	t.Helper()
	cfg := NewTestConfig(t)
	databases := NewTestDatabases(t)
	store := mustStore(t, databases, db.RoleCore)
	router := db.NewRouter(databases)
	consoleLog, fileLog := NewTestLogSinks()
	fakeClock := NewFakeClock()
	ids := NewDeterministicIDGenerator("test")
	execInfra := NewTestExecutionWithClock(t, databases, fakeClock)
	lockStore := NewTestLock(t, mustStore(t, databases, db.RoleRuntime))
	fileStore := filestore.NewLocal(filestore.LocalConfig{Root: cfg.FileStoreRoot})
	eventStore := NewTestEventStoreWithClock(t, mustStore(t, databases, db.RoleEvents), fakeClock)
	outboxStore := NewTestOutboxWithClock(t, mustStore(t, databases, db.RoleJobs), fakeClock)
	metricsSink := NewTestMetricsSink()
	traces := tracing.NewBoundedRecorder(cfg.Tracing.BufferSize)
	return &Env{
		Logger:          logging.NewConsoleAndFileLogger(consoleLog, fileLog, logging.LevelDebug),
		LogBuffer:       fileLog,
		ConsoleLog:      consoleLog,
		FileLog:         fileLog,
		Config:          cfg,
		DB:              store,
		Databases:       databases,
		DBRouter:        router,
		UnitOfWork:      NewTestUnitOfWorkWithClockAndIDGenerator(t, store, fakeClock, ids),
		RoutedUOW:       NewTestRoutedUnitOfWork(t, router),
		EventStore:      eventStore,
		EventRecorder:   platform.NewEventRecorder(eventStore, idgen.NewDeterministic("test-event")),
		Outbox:          outboxStore,
		OutboxPublisher: platform.NewOutboxPublisher(outboxStore, fakeClock, idgen.NewDeterministic("test-outbox")),
		FileStore:       fileStore,
		Artifacts:       platform.NewArtifactStore(fileStore),
		Queue:           execInfra.Queue,
		ExecutionQueue:  execution.NewQueueServiceWithIDGenerator(execInfra.Queue, fakeClock, idgen.NewDeterministic("test-exec")),
		ExecutionClaims: execution.NewClaimsService(execInfra.Claims, fakeClock, cfg.Queue.ClaimTTL),
		ExecutionLive:   execution.NewLivenessService(execInfra.Liveness, fakeClock, cfg.Liveness.HeartbeatTTL),
		Execution:       execInfra,
		Lock:            lockStore,
		Locks:           platform.NewLockManager(lockStore, fakeClock, cfg.Lock.DefaultTTL),
		Claim:           execInfra.Claims,
		Liveness:        execInfra.Liveness,
		Metrics:         metricsSink,
		MetricRecorder:  platform.NewMetricRecorder(metricsSink, logging.NewConsoleAndFileLogger(consoleLog, fileLog, logging.LevelDebug)),
		MetricsRegistry: NewTestMetricsRegistryWithDatabases(t, databases),
		Traces:          &SpanRecorder{recorder: traces},
		Tracer:          platform.NewTracer(traces, tracing.SpanOptions{Clock: fakeClock, IDGenerator: idgen.NewDeterministic("test-trace")}),
		Clock:           fakeClock,
		IDGenerator:     ids,
	}
}

func (e *Env) Close() {
	_ = e.Databases.Close()
}

func NewTestRuntime(t testing.TB) *platform.Runtime {
	t.Helper()
	cfg := NewTestConfig(t)
	rt, err := platform.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open test platform runtime: %v", err)
	}
	t.Cleanup(func() {
		_ = rt.Close()
	})
	return rt
}

func NewInitializedTestRuntime(t testing.TB, modules ...platform.Module) *platform.Runtime {
	t.Helper()
	cfg := NewTestConfig(t)
	rt, err := platform.OpenWithModules(context.Background(), cfg, modules...)
	if err != nil {
		t.Fatalf("open initialized test platform runtime: %v", err)
	}
	t.Cleanup(func() {
		_ = rt.Close()
	})
	if err := rt.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize test platform runtime: %v", err)
	}
	return rt
}

func NewTestLogBuffer() *bytes.Buffer {
	return &bytes.Buffer{}
}

func NewTestLogSinks() (*bytes.Buffer, *bytes.Buffer) {
	return NewTestLogBuffer(), NewTestLogBuffer()
}

func NewTestLogger(t testing.TB) logging.Logger {
	t.Helper()
	return NewTestLoggerWithBuffer(NewTestLogBuffer())
}

func NewTestLoggerWithBuffer(buffer *bytes.Buffer) logging.Logger {
	return logging.NewJSONLogger(buffer, logging.LevelDebug)
}

func NewTestConsoleAndFileLogger() (logging.Logger, *bytes.Buffer, *bytes.Buffer) {
	consoleLog, fileLog := NewTestLogSinks()
	return logging.NewConsoleAndFileLogger(consoleLog, fileLog, logging.LevelDebug), consoleLog, fileLog
}

func NewTestConfiguredLogger(t testing.TB, path string, level string) (logging.Logger, *bytes.Buffer, func() error) {
	t.Helper()
	logger, console, _, closeLog := NewTestConfiguredLoggerWithConfig(t, logging.Config{Level: level, Console: true, File: path})
	return logger, console, closeLog
}

func NewTestConfiguredLoggerWithConfig(t testing.TB, cfg logging.Config) (logging.Logger, *bytes.Buffer, *bytes.Buffer, func() error) {
	t.Helper()
	consoleLog, fileLog := NewTestLogSinks()
	logger, closeLog, err := logging.NewConfiguredLogger(cfg, consoleLog)
	if err != nil {
		t.Fatalf("create configured test logger: %v", err)
	}
	return logger, consoleLog, fileLog, closeLog
}

func NewTestConfig(t testing.TB) config.Config {
	t.Helper()
	databaseURL := "file://" + filepath.Join(t.TempDir(), "stage0-test.db")
	cfg, err := config.LoadYAML(writeTestConfig(t, roleDatabaseURLs(databaseURL), t.TempDir()))
	if err != nil {
		t.Fatalf("load test config: %v", err)
	}
	return cfg
}

func NewTestConfigFromYAML(t testing.TB, path string) config.Config {
	t.Helper()
	cfg, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("load yaml test config: %v", err)
	}
	return cfg
}

func NewTestConfigManager(t testing.TB, cfg config.Config) *config.Manager {
	t.Helper()
	return config.NewManager(cfg)
}

func NewTestDB(t testing.TB) *db.Store {
	t.Helper()
	cfg := NewTestConfig(t)
	store, err := db.Open(context.Background(), DBConfigFromURL(t, cfg.DatabaseURL, t.Name()))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func NewTestDatabases(t testing.TB) *db.Registry {
	t.Helper()
	root := t.TempDir()
	sum := sha256.Sum256([]byte(root))
	namespace := hex.EncodeToString(sum[:8])
	databaseURLs := map[string]string{}
	for _, role := range []string{"core", "audit", "events", "jobs", "runtime", "observability"} {
		databaseURLs[role] = "memory://" + namespace + "/" + role
	}
	cfg, err := config.LoadYAML(writeTestConfig(t, databaseURLs, t.TempDir()))
	if err != nil {
		t.Fatalf("load test database registry config: %v", err)
	}
	registryConfig, err := db.RegistryConfigFromURLs(cfg.DatabaseURLs)
	if err != nil {
		t.Fatalf("build test database registry config: %v", err)
	}
	registry, err := db.OpenRegistry(context.Background(), registryConfig)
	if err != nil {
		t.Fatalf("open test database registry: %v", err)
	}
	t.Cleanup(func() {
		_ = registry.Close()
	})
	return registry
}

func roleDatabaseURLs(databaseURL string) map[string]string {
	return map[string]string{
		"core": databaseURL, "audit": databaseURL, "events": databaseURL,
		"jobs": databaseURL, "runtime": databaseURL, "observability": databaseURL,
	}
}

func writeTestConfig(t testing.TB, databaseURLs map[string]string, filestoreRoot string) string {
	t.Helper()
	document := map[string]any{
		"paths": map[string]any{"base_dir": "."},
		"database": map[string]any{
			"default_url": databaseURLs["core"],
			"urls":        databaseURLs,
		},
		"filestore": map[string]any{
			"backend": "local",
			"root":    filestoreRoot,
		},
	}
	body, err := yaml.Marshal(document)
	if err != nil {
		t.Fatalf("marshal test config: %v", err)
	}
	path := filepath.Join(t.TempDir(), "orrery.test.yaml")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	return path
}

func NewTestDBRouter(t testing.TB) *db.Router {
	t.Helper()
	return db.NewRouter(NewTestDatabases(t))
}

func DBConfigFromURL(t testing.TB, rawURL, name string) db.Config {
	t.Helper()
	switch {
	case strings.HasPrefix(rawURL, "memory://"):
		return db.Config{Driver: db.DriverMemory, Name: name, URL: rawURL}
	case strings.HasPrefix(rawURL, "file://"), strings.HasPrefix(rawURL, "sqlite://"):
		return db.Config{Driver: db.DriverFile, Name: name, URL: rawURL}
	default:
		t.Fatalf("unsupported test database url: %s", rawURL)
		return db.Config{}
	}
}

func NewTestUnitOfWork(t testing.TB, store *db.Store) *uow.UnitOfWork {
	t.Helper()
	return uow.New(store)
}

func NewTestUnitOfWorkWithClock(t testing.TB, store *db.Store, clock clock.Clock) *uow.UnitOfWork {
	t.Helper()
	return uow.New(store).WithClock(clock)
}

func NewTestUnitOfWorkWithClockAndIDGenerator(t testing.TB, store *db.Store, clock clock.Clock, generator idgen.ErrorGenerator) *uow.UnitOfWork {
	t.Helper()
	return uow.New(store).WithClock(clock).WithIDGenerator(generator)
}

func NewTestRoutedUnitOfWork(t testing.TB, router *db.Router) *uow.RoutedUnitOfWork {
	t.Helper()
	return uow.NewRouted(router)
}

func NewTestEventStore(t testing.TB, store *db.Store) *eventstore.Store {
	t.Helper()
	return eventstore.New(store)
}

func NewTestEventStoreWithClock(t testing.TB, store *db.Store, clock clock.Clock) *eventstore.Store {
	t.Helper()
	return eventstore.NewWithOptions(store, eventstore.StoreOptions{Clock: clock})
}

func NewTestOutbox(t testing.TB, store *db.Store) *outbox.Store {
	t.Helper()
	return outbox.New(store).WithLeaseTTL(30 * time.Second)
}

func NewTestOutboxWithClock(t testing.TB, store *db.Store, clock clock.Clock) *outbox.Store {
	t.Helper()
	return outbox.NewWithOptions(store, outbox.StoreOptions{Clock: clock}).WithLeaseTTL(30 * time.Second)
}

func NewTestFileStore(t testing.TB) *filestore.Local {
	t.Helper()
	return filestore.NewLocal(filestore.LocalConfig{Root: t.TempDir()})
}

func NewTestQueue(t testing.TB, store *db.Store) *execution.Queue {
	t.Helper()
	q, err := execution.NewQueue(context.Background(), store, execution.QueueConfig{ClaimTTL: 30 * time.Second, MaxAttempts: 3, SweepBatchSize: 100})
	if err != nil {
		t.Fatalf("create sqlite test queue: %v", err)
	}
	return q
}

func NewTestExecution(t testing.TB, registry *db.Registry) *execution.Infra {
	t.Helper()
	return NewTestExecutionWithClock(t, registry, nil)
}

func NewTestExecutionWithClock(t testing.TB, registry *db.Registry, clock clock.Clock) *execution.Infra {
	t.Helper()
	infra, err := execution.Open(context.Background(), execution.Stores{
		Jobs:    mustStore(t, registry, db.RoleJobs),
		Runtime: mustStore(t, registry, db.RoleRuntime),
	}, execution.Config{
		Queue:   execution.QueueConfig{ClaimTTL: 30 * time.Second, MaxAttempts: 3, SweepBatchSize: 100},
		Sweeper: sweeperConfig(),
		Clock:   clock,
	})
	if err != nil {
		t.Fatalf("create test execution infra: %v", err)
	}
	return infra
}

func sweeperConfig() execution.SweeperConfig {
	return execution.SweeperConfig{BatchSize: 100}
}

func NewTestLock(t testing.TB, store *db.Store) *lock.Store {
	t.Helper()
	locks, err := lock.New(context.Background(), store)
	if err != nil {
		t.Fatalf("create test lock: %v", err)
	}
	return locks
}

func NewTestClaimStore(t testing.TB, store *db.Store) *execution.ClaimStore {
	t.Helper()
	claims, err := execution.NewClaimStore(context.Background(), store)
	if err != nil {
		t.Fatalf("create sqlite test claim store: %v", err)
	}
	return claims
}

func NewTestLivenessStore(t testing.TB, store *db.Store) *execution.LivenessStore {
	t.Helper()
	live, err := execution.NewLivenessStore(context.Background(), store)
	if err != nil {
		t.Fatalf("create sqlite test liveness store: %v", err)
	}
	return live
}

func NewTestMetricsSink() *metrics.BoundedSink {
	return metrics.NewBoundedSink(100)
}

func NewTestMetricsRegistry(t testing.TB) *metrics.Registry {
	t.Helper()
	return NewTestMetricsRegistryWithDatabases(t, NewTestDatabases(t))
}

func NewTestMetricsRegistryWithDatabases(t testing.TB, registry *db.Registry) *metrics.Registry {
	t.Helper()
	return metrics.NewRegistry(metrics.RegistryOptions{Databases: map[string]metrics.DBStatsProvider{
		db.RoleCore:          mustStore(t, registry, db.RoleCore),
		db.RoleAudit:         mustStore(t, registry, db.RoleAudit),
		db.RoleEvents:        mustStore(t, registry, db.RoleEvents),
		db.RoleJobs:          mustStore(t, registry, db.RoleJobs),
		db.RoleRuntime:       mustStore(t, registry, db.RoleRuntime),
		db.RoleObservability: mustStore(t, registry, db.RoleObservability),
	}})
}

type SpanRecorder struct {
	recorder *tracing.BoundedRecorder
	spans    []tracing.Span
}

func NewTestSpanRecorder() *SpanRecorder {
	return &SpanRecorder{recorder: tracing.NewBoundedRecorder(100)}
}

func (r *SpanRecorder) RecordSpan(span tracing.Span) {
	if r.recorder != nil {
		r.recorder.RecordSpan(span)
		return
	}
	r.spans = append(r.spans, span)
}

func (r *SpanRecorder) Spans() []tracing.Span {
	if r.recorder != nil {
		return r.recorder.Drain()
	}
	return append([]tracing.Span(nil), r.spans...)
}

func NewFakeClock() *clock.Fake {
	return clock.NewFake(time.Date(2026, 5, 30, 0, 0, 0, 0, time.UTC))
}

func NewDeterministicIDGenerator(prefix string) *idgen.Deterministic {
	return idgen.NewDeterministic(prefix)
}

func mustStore(t testing.TB, registry *db.Registry, role string) *db.Store {
	t.Helper()
	store, err := registry.Store(role)
	if err != nil {
		t.Fatalf("lookup %s database: %v", role, err)
	}
	return store
}
