package testkit_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/logging"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/tracing"
	"orrery_v2/internal/platform/uow"
)

func TestDBConfigFromURLSupportsFileAndMemory(t *testing.T) {
	memory := testkit.DBConfigFromURL(t, "memory://case", "case")
	if memory.Driver.String() != "memory" {
		t.Fatalf("memory driver = %s", memory.Driver)
	}
	file := testkit.DBConfigFromURL(t, "file://./case.json", "case")
	if file.Driver.String() != "file" {
		t.Fatalf("file driver = %s", file.Driver)
	}
}

func TestNewTestDatabasesCreatesIsolatedRoleStores(t *testing.T) {
	registry := testkit.NewTestDatabases(t)
	defer registry.Close()

	events := mustStore(t, registry, "events")
	audit := mustStore(t, registry, "audit")
	jobs := mustStore(t, registry, "jobs")
	events.Put("records", "event-1", map[string]any{"kind": "event"})
	audit.Put("records", "audit-1", map[string]any{"kind": "audit"})
	jobs.Put("records", "job-1", map[string]any{"kind": "job"})

	if _, ok := events.Get("records", "audit-1"); ok {
		t.Fatalf("audit record leaked into events database")
	}
	if _, ok := jobs.Get("records", "event-1"); ok {
		t.Fatalf("event record leaked into jobs database")
	}
	if _, ok := events.Get("records", "job-1"); ok {
		t.Fatalf("job record leaked into events database")
	}
}

func TestNewTestDBRouterHidesSplitDatabasesBehindLogicalTableNames(t *testing.T) {
	router := testkit.NewTestDBRouter(t)
	ctx := context.Background()

	if err := router.PutRecord(ctx, "audit.audit_log", "audit-1", db.Record{"kind": "audit"}); err != nil {
		t.Fatalf("PutRecord returned error: %v", err)
	}
	record, ok, err := router.GetRecord(ctx, "audit.audit_log", "audit-1")
	if err != nil || !ok || record["kind"] != "audit" {
		t.Fatalf("GetRecord record=%+v ok=%v err=%v", record, ok, err)
	}
}

func TestEnvRoutedUnitOfWorkUsesLogicalTableNames(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()

	if err := env.RoutedUOW.Do(ctx, func(ctx context.Context, tx uow.RoutedTx) error {
		return tx.PutRecord(ctx, "audit.audit_log", "audit-1", db.Record{"kind": "audit"})
	}); err != nil {
		t.Fatalf("RoutedUOW.Do returned error: %v", err)
	}
	if record, ok := mustStore(t, env.Databases, "audit").Get("audit_log", "audit-1"); !ok || record["kind"] != "audit" {
		t.Fatalf("audit database record=%+v ok=%v", record, ok)
	}
	if _, ok := mustStore(t, env.Databases, "events").Get("audit_log", "audit-1"); ok {
		t.Fatalf("audit record leaked into events database")
	}
}

func TestNewTestRuntimeUsesProductionAssemblyPath(t *testing.T) {
	rt := testkit.NewTestRuntime(t)
	ctx := context.Background()

	if rt.Logger == nil || rt.ConfigManager == nil || rt.Clock == nil || rt.IDGenerator == nil || rt.Databases == nil || rt.DBRouter == nil || rt.UnitOfWork == nil || rt.RoutedUOW == nil || rt.Migrations == nil || rt.FileStore == nil || rt.Artifacts == nil || rt.ExecutionQueue == nil || rt.ExecutionClaims == nil || rt.ExecutionLive == nil || rt.Lock == nil || rt.Locks == nil || rt.Secrets == nil || rt.Health == nil || rt.Metrics == nil || rt.MetricRecorder == nil || rt.Traces == nil || rt.Tracer == nil || rt.Sweeper == nil {
		t.Fatalf("runtime missing dependencies: %+v", rt)
	}
	if err := rt.Migrations.Up(ctx); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	if report := rt.Health.Ready(ctx); !report.OK {
		t.Fatalf("ready report not ok: %+v", report)
	}
}

func TestNewInitializedTestRuntimeRunsBusinessModuleInitializers(t *testing.T) {
	ctx := context.Background()
	module := platform.ModuleFunc(func(ctx context.Context, registrar *platform.ModuleRegistrar) error {
		if err := registrar.RegisterSQLMigration(platform.DatabaseRoleCore, "010_business_fixture", "create business fixture", `CREATE TABLE business_fixture (id TEXT PRIMARY KEY)`); err != nil {
			return err
		}
		return registrar.RegisterInitializer("business.fixture.seed", func(ctx context.Context, init platform.InitializerContext) error {
			return init.RoutedUOW().Do(ctx, func(ctx context.Context, tx platform.RoutedTx) error {
				return tx.PutRecord(ctx, "core.business_fixture", "fixture-1", db.Record{"id": "fixture-1"})
			})
		})
	})

	rt := testkit.NewInitializedTestRuntime(t, module)

	record, ok := rt.DBRouter.Get("core.business_fixture", "fixture-1")
	if !ok || record["id"] != "fixture-1" {
		t.Fatalf("initialized runtime record=%+v ok=%v, want fixture-1", record, ok)
	}
	if report := rt.Health.Ready(ctx); !report.OK {
		t.Fatalf("ready report not ok: %+v", report)
	}
}

func TestTracingHTTPMiddlewareWorksWithTestSpanRecorder(t *testing.T) {
	recorder := testkit.NewTestSpanRecorder()
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	handler := tracing.HTTPMiddleware("http.test", recorder)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tracing.TraceID(r.Context()) != traceID {
			t.Fatalf("TraceID = %q, want %q", tracing.TraceID(r.Context()), traceID)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("traceparent", "00-"+traceID+"-00f067aa0ba902b7-01")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("traceparent"); !strings.Contains(got, traceID) {
		t.Fatalf("response traceparent = %q, want trace id", got)
	}
	spans := recorder.Spans()
	if len(spans) != 1 || spans[0].TraceID != traceID || spans[0].Name != "http.test" {
		t.Fatalf("spans = %+v", spans)
	}
}

func TestNewTestConfigFromYAMLAndManager(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	if err := os.WriteFile(path, []byte(`
environment: test-yaml
database:
  default_url: memory://core
  urls:
    core: memory://core
    audit: memory://audit
    events: memory://events
    jobs: memory://jobs
    runtime: memory://runtime
    observability: memory://observability
filestore:
  root: ./yaml-artifacts
`), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	cfg := testkit.NewTestConfigFromYAML(t, path)
	if cfg.Environment != "test-yaml" || cfg.FileStoreRoot != filepath.Join(filepath.Dir(path), "yaml-artifacts") {
		t.Fatalf("cfg = %+v", cfg)
	}
	manager := testkit.NewTestConfigManager(t, cfg)
	if manager.Current().Environment != "test-yaml" {
		t.Fatalf("manager current = %+v", manager.Current())
	}
}

func TestNewTestConfiguredLoggerUsesConfigShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "platform.jsonl")
	logger, console, closeLog := testkit.NewTestConfiguredLogger(t, path, "warn")
	logger.Info(context.Background(), "hidden", logging.String("operation", "below"))
	logger.Warn(context.Background(), "visible", logging.String("operation", "configured"))
	if err := closeLog(); err != nil {
		t.Fatalf("close log: %v", err)
	}
	if strings.Contains(console.String(), "hidden") || !strings.Contains(console.String(), "visible") {
		t.Fatalf("console did not honor configured logger level: %s", console.String())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if strings.Contains(string(raw), "hidden") || !strings.Contains(string(raw), `"operation":"configured"`) {
		t.Fatalf("file did not honor configured logger: %s", string(raw))
	}
}
