package health_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/health"
	"orrery_v2/internal/platform/migration"
	"orrery_v2/internal/platform/testkit"
)

func TestLiveAndReadyAreSeparated(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "stage0"})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	runner := migration.NewRunner([]migration.Migration{{Version: "001", Description: "create sample"}})
	checker := health.NewChecker(store, runner, time.Second)

	if live := checker.Live(ctx); !live.OK {
		t.Fatalf("Live OK = false, checks = %+v", live.Checks)
	}
	ready := checker.Ready(ctx)
	if ready.OK {
		t.Fatalf("Ready OK = true before migrations, want false")
	}

	if err := runner.Up(ctx, store); err != nil {
		t.Fatalf("Up returned error: %v", err)
	}
	ready = checker.Ready(ctx)
	if !ready.OK {
		t.Fatalf("Ready OK = false after migrations, checks = %+v", ready.Checks)
	}
}

func TestReportsUseInjectedClock(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 31, 17, 0, 0, 0, time.UTC)
	checker := health.NewChecker(nil, nil, 0).WithClock(clock.NewFake(now))

	live := checker.Live(ctx)
	if !live.CheckedAt.Equal(now) {
		t.Fatalf("live CheckedAt = %s, want injected clock %s", live.CheckedAt, now)
	}
	ready := checker.Ready(ctx)
	if !ready.CheckedAt.Equal(now) {
		t.Fatalf("ready CheckedAt = %s, want injected clock %s", ready.CheckedAt, now)
	}
}

func TestReadyUsesShortTTLCacheForSuccessfulResult(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "stage0"})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	runner := migration.NewRunner([]migration.Migration{{Version: "001", Description: "create sample"}})
	if err := runner.Up(ctx, store); err != nil {
		t.Fatalf("Up returned error: %v", err)
	}
	checker := health.NewChecker(store, runner, time.Hour)

	first := checker.Ready(ctx)
	if !first.OK {
		t.Fatalf("first ready = %+v", first)
	}
	_ = store.Close()
	second := checker.Ready(ctx)
	if !second.OK || !second.CheckedAt.Equal(first.CheckedAt) {
		t.Fatalf("second ready did not use cache: first=%+v second=%+v", first, second)
	}
}

func TestReadyCoalescesConcurrentSuccessfulChecks(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "stage0-health-concurrent-cache"})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	runner := migration.NewRunner([]migration.Migration{{Version: "001", Description: "create sample"}})
	if err := runner.Up(ctx, store); err != nil {
		t.Fatalf("Up returned error: %v", err)
	}

	var calls atomic.Int32
	checkStarted := make(chan struct{})
	releaseCheck := make(chan struct{})
	checker := health.NewChecker(store, runner, time.Hour).WithReadyCheck(func(context.Context) health.Check {
		if calls.Add(1) == 1 {
			close(checkStarted)
		}
		<-releaseCheck
		return health.Check{Name: "coalesced", OK: true}
	})

	const parallel = 32
	start := make(chan struct{})
	reports := make(chan health.Report, parallel)
	var ready sync.WaitGroup
	ready.Add(parallel)
	for range parallel {
		go func() {
			ready.Done()
			<-start
			reports <- checker.Ready(ctx)
		}()
	}
	ready.Wait()
	close(start)
	<-checkStarted
	time.Sleep(20 * time.Millisecond)
	close(releaseCheck)

	for range parallel {
		report := <-reports
		if !report.OK {
			t.Fatalf("Ready = %+v, want successful shared report", report)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("concurrent ready check calls = %d, want 1", got)
	}
}

func TestReadyReturnsStaleSuccessWhileRefreshRuns(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "stage0-health-stale-refresh"})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	runner := migration.NewRunner([]migration.Migration{{Version: "001", Description: "create sample"}})
	if err := runner.Up(ctx, store); err != nil {
		t.Fatalf("Up returned error: %v", err)
	}

	now := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	fakeClock := clock.NewFake(now)
	var calls atomic.Int32
	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})
	checker := health.NewChecker(store, runner, time.Second).
		WithClock(fakeClock).
		WithReadyCheck(func(context.Context) health.Check {
			if calls.Add(1) == 2 {
				close(refreshStarted)
				<-releaseRefresh
			}
			return health.Check{Name: "stale-refresh", OK: true}
		})

	first := checker.Ready(ctx)
	if !first.OK {
		t.Fatalf("first ready = %+v", first)
	}
	fakeClock.Advance(time.Second + time.Millisecond)

	startedAt := time.Now()
	stale := checker.Ready(ctx)
	if elapsed := time.Since(startedAt); elapsed > 100*time.Millisecond {
		t.Fatalf("stale ready blocked for %s", elapsed)
	}
	if !stale.OK || !stale.CheckedAt.Equal(first.CheckedAt) {
		t.Fatalf("stale ready = %+v, want cached success %+v", stale, first)
	}
	select {
	case <-refreshStarted:
	case <-time.After(time.Second):
		t.Fatal("background readiness refresh did not start")
	}

	duringRefresh := checker.Ready(ctx)
	if !duringRefresh.OK || !duringRefresh.CheckedAt.Equal(first.CheckedAt) {
		t.Fatalf("ready during refresh = %+v, want cached success %+v", duringRefresh, first)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("readiness check calls during refresh = %d, want 2", got)
	}
	close(releaseRefresh)

	deadline := time.Now().Add(time.Second)
	for {
		refreshed := checker.Ready(ctx)
		if refreshed.CheckedAt.Equal(fakeClock.Now()) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background refresh did not publish a new report: %+v", refreshed)
		}
		time.Sleep(time.Millisecond)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("readiness check calls after refresh = %d, want 2", got)
	}
}

func TestReadyStopsServingStaleSuccessAfterGracePeriod(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "stage0-health-stale-expiry"})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	runner := migration.NewRunner([]migration.Migration{{Version: "001", Description: "create sample"}})
	if err := runner.Up(ctx, store); err != nil {
		t.Fatalf("Up returned error: %v", err)
	}

	fakeClock := clock.NewFake(time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC))
	var calls atomic.Int32
	var healthy atomic.Bool
	healthy.Store(true)
	refreshFinished := make(chan struct{})
	checker := health.NewChecker(store, runner, time.Second).
		WithClock(fakeClock).
		WithReadyCheck(func(context.Context) health.Check {
			call := calls.Add(1)
			ok := healthy.Load()
			if call == 2 {
				close(refreshFinished)
			}
			return health.Check{Name: "dependency", OK: ok}
		})

	if first := checker.Ready(ctx); !first.OK {
		t.Fatalf("first ready = %+v", first)
	}
	healthy.Store(false)
	fakeClock.Advance(time.Second + time.Millisecond)
	if stale := checker.Ready(ctx); !stale.OK {
		t.Fatalf("ready did not serve stale success during grace period: %+v", stale)
	}
	select {
	case <-refreshFinished:
	case <-time.After(time.Second):
		t.Fatal("failed background readiness refresh did not finish")
	}

	fakeClock.Advance(30 * time.Second)
	afterGrace := checker.Ready(ctx)
	if afterGrace.OK {
		t.Fatalf("ready served stale success after grace period: %+v", afterGrace)
	}
	assertCheckNotOK(t, afterGrace, "dependency")
}

func TestReadyChecksNamedDatabasesFilestoreAndConfigReload(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	registry, err := db.OpenRegistry(ctx, db.RegistryConfig{
		Databases: map[string]db.Config{
			db.RoleCore:    {Driver: db.DriverFile, URL: "file://" + filepath.Join(root, "core.db")},
			db.RoleEvents:  {Driver: db.DriverFile, URL: "file://" + filepath.Join(root, "events.db")},
			db.RoleJobs:    {Driver: db.DriverFile, URL: "file://" + filepath.Join(root, "jobs.db")},
			db.RoleRuntime: {Driver: db.DriverFile, URL: "file://" + filepath.Join(root, "runtime.db")},
		},
	})
	if err != nil {
		t.Fatalf("OpenRegistry: %v", err)
	}
	defer registry.Close()
	runner := migration.NewRunner([]migration.Migration{{Version: "001", Description: "ready"}})
	if err := runner.Up(ctx, mustStore(t, registry, db.RoleCore)); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	cfg := testkit.NewTestConfig(t)
	cfg.FileStoreRoot = filepath.Join(root, "artifacts")
	manager := config.NewManager(cfg)
	checker := health.NewChecker(mustStore(t, registry, db.RoleCore), runner, 0).
		WithDatabases(registry).
		WithFileStoreRoot(cfg.FileStoreRoot).
		WithConfigManager(manager)

	ready := checker.Ready(ctx)
	if !ready.OK {
		t.Fatalf("ready = %+v", ready)
	}
	assertCheckOK(t, ready, "db.core")
	assertCheckOK(t, ready, "db.events")
	assertCheckOK(t, ready, "db.jobs")
	assertCheckOK(t, ready, "db.runtime")
	assertCheckOK(t, ready, "filestore")
	assertCheckOK(t, ready, "config")

	badPath := filepath.Join(root, "bad.yaml")
	if err := os.WriteFile(badPath, []byte("health:\n  readiness_cache_ttl: not-a-duration\n"), 0o600); err != nil {
		t.Fatalf("write bad yaml: %v", err)
	}
	if err := manager.ReloadFromFile(ctx, badPath); err == nil {
		t.Fatalf("invalid reload returned nil error")
	}
	ready = checker.Ready(ctx)
	if ready.OK {
		t.Fatalf("ready OK despite config reload error: %+v", ready)
	}
	assertCheckNotOK(t, ready, "config")
}

func TestReadyCacheDoesNotHideConfigReloadError(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "stage0-health-config-cache"})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	runner := migration.NewRunner([]migration.Migration{{Version: "001", Description: "ready"}})
	if err := runner.Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	cfg := testkit.NewTestConfig(t)
	manager := config.NewManager(cfg)
	checker := health.NewChecker(store, runner, time.Hour).WithConfigManager(manager)

	first := checker.Ready(ctx)
	if !first.OK {
		t.Fatalf("first ready = %+v", first)
	}
	badPath := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(badPath, []byte("queue:\n  max_attempts: 0\n"), 0o600); err != nil {
		t.Fatalf("write bad yaml: %v", err)
	}
	if err := manager.ReloadFromFile(ctx, badPath); err == nil {
		t.Fatalf("invalid reload returned nil error")
	}

	ready := checker.Ready(ctx)
	if ready.OK {
		t.Fatalf("ready OK used stale success cache despite config reload error: first=%+v ready=%+v", first, ready)
	}
	assertCheckNotOK(t, ready, "config")
}

func TestReadyReportsRestartRequiredConfigErrorSeparately(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "stage0-health-restart-required"})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	runner := migration.NewRunner([]migration.Migration{{Version: "001", Description: "ready"}})
	if err := runner.Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	cfg := testkit.NewTestConfig(t)
	manager := config.NewManager(cfg)
	manager.RecordError(errors.New("restart required: static runtime config changed: database.urls.events"))
	checker := health.NewChecker(store, runner, 0).WithConfigManager(manager)

	ready := checker.Ready(ctx)
	if ready.OK {
		t.Fatalf("ready OK despite restart required: %+v", ready)
	}
	assertCheckNotOK(t, ready, "config")
	assertCheckMessage(t, ready, "runtime.restart_required", "restart required: static runtime config changed: database.urls.events")
}

func TestReadyReportsMissingDatabaseAndMigrationRunnerWithoutPanicking(t *testing.T) {
	ctx := context.Background()
	checker := health.NewChecker(nil, nil, 0)

	ready := checker.Ready(ctx)
	if ready.OK {
		t.Fatalf("ready OK despite missing database and migration runner: %+v", ready)
	}
	assertCheckMessage(t, ready, "db", "database store is not configured")
	assertCheckMessage(t, ready, "migration", "migration runner is not configured")
}

func TestReadyChecksRegisteredMigrationsAcrossDatabaseRoles(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	registry, err := db.OpenRegistry(ctx, db.RegistryConfig{
		Databases: map[string]db.Config{
			db.RoleCore:   {Driver: db.DriverFile, URL: "file://" + filepath.Join(root, "core.db")},
			db.RoleEvents: {Driver: db.DriverFile, URL: "file://" + filepath.Join(root, "events.db")},
		},
	})
	if err != nil {
		t.Fatalf("OpenRegistry: %v", err)
	}
	defer registry.Close()
	migrations := migration.NewRegistry()
	migrations.Register(db.RoleCore, migration.Migration{
		Version:     "001_core",
		Description: "core init",
		Statements:  []string{`CREATE TABLE core_ready (id TEXT PRIMARY KEY)`},
	})
	migrations.Register(db.RoleEvents, migration.Migration{
		Version:     "001_events",
		Description: "events init",
		Statements:  []string{`CREATE TABLE events_ready (id TEXT PRIMARY KEY)`},
	})
	if err := migration.NewRunner([]migration.Migration{{Version: "001_core", Description: "core init", Statements: []string{`CREATE TABLE core_ready (id TEXT PRIMARY KEY)`}}}).Up(ctx, mustStore(t, registry, db.RoleCore)); err != nil {
		t.Fatalf("core migration up: %v", err)
	}
	checker := health.NewChecker(mustStore(t, registry, db.RoleCore), migration.NewRunner(nil), 0).
		WithDatabases(registry).
		WithMigrationRegistry(migrations)

	ready := checker.Ready(ctx)
	if ready.OK {
		t.Fatalf("ready OK despite missing events migration: %+v", ready)
	}
	assertCheckNotOK(t, ready, "migration.events")

	if err := migrations.Up(ctx, map[string]*db.Store{
		db.RoleCore:   mustStore(t, registry, db.RoleCore),
		db.RoleEvents: mustStore(t, registry, db.RoleEvents),
	}); err != nil {
		t.Fatalf("registered migrations up: %v", err)
	}
	ready = checker.Ready(ctx)
	if !ready.OK {
		t.Fatalf("ready false after all registered migrations applied: %+v", ready)
	}
	assertCheckOK(t, ready, "migration.core")
	assertCheckOK(t, ready, "migration.events")
}

func TestReadyChecksMigrationCoordinatorWithoutApplicationStoreWiring(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{
		Databases: map[string]db.Config{
			db.RoleCore:    {Driver: db.DriverMemory, Name: "ready-coordinator-core"},
			db.RoleEvents:  {Driver: db.DriverMemory, Name: "ready-coordinator-events"},
			db.RoleJobs:    {Driver: db.DriverMemory, Name: "ready-coordinator-jobs"},
			db.RoleRuntime: {Driver: db.DriverMemory, Name: "ready-coordinator-runtime"},
		},
	})
	if err != nil {
		t.Fatalf("OpenRegistry returned error: %v", err)
	}
	defer databases.Close()
	coordinator := migration.NewCoordinator(databases)
	if err := coordinator.RegisterInitializer(db.RoleCore, migration.Migration{
		Version:     "010_business_profiles",
		Description: "create business profile table",
		Statements:  []string{`CREATE TABLE business_profiles (id TEXT PRIMARY KEY)`},
	}); err != nil {
		t.Fatalf("RegisterInitializer returned error: %v", err)
	}
	checker := health.NewChecker(mustStore(t, databases, db.RoleCore), migration.NewRunner(nil), 0).
		WithMigrationCoordinator(coordinator)

	ready := checker.Ready(ctx)
	if ready.OK {
		t.Fatalf("ready OK before coordinator Up: %+v", ready)
	}
	assertCheckNotOK(t, ready, "migration.core")
	if err := coordinator.Up(ctx); err != nil {
		t.Fatalf("coordinator Up returned error: %v", err)
	}
	ready = checker.Ready(ctx)
	if !ready.OK {
		t.Fatalf("ready false after coordinator Up: %+v", ready)
	}
	assertCheckOK(t, ready, "migration.core")
	assertCheckOK(t, ready, "migration.events")
	assertCheckOK(t, ready, "migration.jobs")
	assertCheckOK(t, ready, "migration.runtime")
}

func TestReadyUsesMigrationCoordinatorForDatabaseAndMigrationChecks(t *testing.T) {
	ctx := context.Background()
	coordinator, err := migration.OpenCoordinatorFromDatabaseURLs(ctx, map[string]string{
		db.RoleCore:          "memory://ready-coordinator-owned-core",
		db.RoleEvents:        "memory://ready-coordinator-owned-events",
		db.RoleJobs:          "memory://ready-coordinator-owned-jobs",
		db.RoleRuntime:       "memory://ready-coordinator-owned-runtime",
		db.RoleObservability: "memory://ready-coordinator-owned-observability",
	})
	if err != nil {
		t.Fatalf("OpenCoordinatorFromDatabaseURLs returned error: %v", err)
	}
	defer coordinator.Close()
	if err := coordinator.RegisterInitializer(db.RoleCore, migration.Migration{
		Version:     "010_business_profiles",
		Description: "create business profile table",
		Statements:  []string{`CREATE TABLE business_profiles_ready (id TEXT PRIMARY KEY)`},
	}); err != nil {
		t.Fatalf("RegisterInitializer returned error: %v", err)
	}
	checker := health.NewChecker(nil, nil, 0).WithMigrationCoordinator(coordinator)

	ready := checker.Ready(ctx)
	if ready.OK {
		t.Fatalf("ready OK before coordinator Up: %+v", ready)
	}
	assertCheckOK(t, ready, "db.core")
	assertCheckNotOK(t, ready, "migration.core")
	if err := coordinator.Up(ctx); err != nil {
		t.Fatalf("coordinator Up returned error: %v", err)
	}
	ready = checker.Ready(ctx)
	if !ready.OK {
		t.Fatalf("ready false after coordinator Up: %+v", ready)
	}
	assertCheckOK(t, ready, "db.events")
	assertCheckOK(t, ready, "migration.core")
}

func TestReadyReportsRegisteredMigrationsInStableRoleOrder(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	registry, err := db.OpenRegistry(ctx, db.RegistryConfig{
		Databases: map[string]db.Config{
			db.RoleRuntime:       {Driver: db.DriverFile, URL: "file://" + filepath.Join(root, "runtime.db")},
			db.RoleJobs:          {Driver: db.DriverFile, URL: "file://" + filepath.Join(root, "jobs.db")},
			db.RoleCore:          {Driver: db.DriverFile, URL: "file://" + filepath.Join(root, "core.db")},
			db.RoleObservability: {Driver: db.DriverFile, URL: "file://" + filepath.Join(root, "observability.db")},
			db.RoleEvents:        {Driver: db.DriverFile, URL: "file://" + filepath.Join(root, "events.db")},
		},
	})
	if err != nil {
		t.Fatalf("OpenRegistry: %v", err)
	}
	defer registry.Close()
	migrations := migration.NewRegistry()
	migrations.Register(db.RoleRuntime, migration.Migration{Version: "001_runtime", Description: "runtime init"})
	migrations.Register(db.RoleJobs, migration.Migration{Version: "001_jobs", Description: "jobs init"})
	migrations.Register(db.RoleCore, migration.Migration{Version: "001_core", Description: "core init"})
	migrations.Register(db.RoleObservability, migration.Migration{Version: "001_observability", Description: "observability init"})
	migrations.Register(db.RoleEvents, migration.Migration{Version: "001_events", Description: "events init"})
	if err := migrations.Up(ctx, map[string]*db.Store{
		db.RoleCore:          mustStore(t, registry, db.RoleCore),
		db.RoleEvents:        mustStore(t, registry, db.RoleEvents),
		db.RoleJobs:          mustStore(t, registry, db.RoleJobs),
		db.RoleObservability: mustStore(t, registry, db.RoleObservability),
		db.RoleRuntime:       mustStore(t, registry, db.RoleRuntime),
	}); err != nil {
		t.Fatalf("registered migrations up: %v", err)
	}
	checker := health.NewChecker(mustStore(t, registry, db.RoleCore), migration.NewRunner(nil), 0).
		WithDatabases(registry).
		WithMigrationRegistry(migrations)

	want := []string{"migration.core", "migration.events", "migration.jobs", "migration.observability", "migration.runtime"}
	for attempt := 0; attempt < 50; attempt++ {
		ready := checker.Ready(ctx)
		got := migrationCheckNames(ready)
		if len(got) != len(want) {
			t.Fatalf("migration checks = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("attempt %d migration checks = %v, want %v", attempt, got, want)
			}
		}
	}
}

func TestReadyFailsWhenFilestoreRootIsAFile(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "stage0-health-filestore"})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	runner := migration.NewRunner([]migration.Migration{{Version: "001", Description: "ready"}})
	if err := runner.Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	filePath := filepath.Join(t.TempDir(), "not-dir")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	checker := health.NewChecker(store, runner, 0).WithFileStoreRoot(filePath)

	ready := checker.Ready(ctx)
	if ready.OK {
		t.Fatalf("ready OK despite bad filestore root: %+v", ready)
	}
	assertCheckNotOK(t, ready, "filestore")
}

func migrationCheckNames(report health.Report) []string {
	names := []string{}
	for _, check := range report.Checks {
		if len(check.Name) >= len("migration.") && check.Name[:len("migration.")] == "migration." {
			names = append(names, check.Name)
		}
	}
	return names
}

func assertCheckOK(t *testing.T, report health.Report, name string) {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			if !check.OK {
				t.Fatalf("check %s not OK: %+v", name, check)
			}
			return
		}
	}
	t.Fatalf("check %s missing from %+v", name, report.Checks)
}

func assertCheckNotOK(t *testing.T, report health.Report, name string) {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			if check.OK {
				t.Fatalf("check %s unexpectedly OK: %+v", name, check)
			}
			return
		}
	}
	t.Fatalf("check %s missing from %+v", name, report.Checks)
}

func assertCheckMessage(t *testing.T, report health.Report, name, message string) {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			if check.OK {
				t.Fatalf("check %s unexpectedly OK: %+v", name, check)
			}
			if check.Message != message {
				t.Fatalf("check %s message = %q, want %q", name, check.Message, message)
			}
			return
		}
	}
	t.Fatalf("check %s missing from %+v", name, report.Checks)
}

func mustStore(t *testing.T, registry *db.Registry, role string) *db.Store {
	t.Helper()
	store, err := registry.Store(role)
	if err != nil {
		t.Fatalf("lookup %s database: %v", role, err)
	}
	return store
}
