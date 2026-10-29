package migration_test

import (
	"context"
	"os"
	"testing"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/eventstore"
	"orrery_v2/internal/platform/execution"
	"orrery_v2/internal/platform/lock"
	"orrery_v2/internal/platform/migration"
	"orrery_v2/internal/platform/outbox"
)

func TestRunnerAppliesMigrationsAndTracksLatestVersion(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "stage0"})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	runner := migration.NewRunner([]migration.Migration{
		{Version: "001", Description: "create sample"},
		{Version: "002", Description: "add sample name"},
	})

	if err := runner.Up(ctx, store); err != nil {
		t.Fatalf("Up returned error: %v", err)
	}
	ready, err := runner.LatestApplied(ctx, store)
	if err != nil {
		t.Fatalf("LatestApplied returned error: %v", err)
	}
	if !ready {
		t.Fatalf("LatestApplied = false, want true")
	}
}

func TestRunnerExecutesSQLiteStatementsAndTracksSchemaMigrations(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "migration-sql"})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	runner := migration.NewRunner([]migration.Migration{
		{
			Version:     "001",
			Description: "create widgets",
			Statements: []string{
				`CREATE TABLE widgets (id TEXT PRIMARY KEY, name TEXT NOT NULL)`,
				`INSERT INTO widgets (id, name) VALUES ('w1', 'first')`,
			},
		},
	})

	if err := runner.Up(ctx, store); err != nil {
		t.Fatalf("Up returned error: %v", err)
	}

	var name string
	if err := store.QueryRow(ctx, `SELECT name FROM widgets WHERE id = 'w1'`).Scan(&name); err != nil {
		t.Fatalf("query migrated table: %v", err)
	}
	if name != "first" {
		t.Fatalf("name = %q, want first", name)
	}
	var description string
	if err := store.QueryRow(ctx, `SELECT description FROM schema_migrations WHERE version = '001'`).Scan(&description); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if description != "create widgets" {
		t.Fatalf("description = %q, want create widgets", description)
	}
	if _, ok := store.Get("schema_migrations", "001"); ok {
		t.Fatalf("schema migration marker leaked into compatibility platform_records table")
	}
}

func TestRunnerRecordsAppliedAtFromInjectedClock(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "migration-clock"})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	now := time.Date(2026, 5, 31, 18, 0, 0, 0, time.UTC)
	runner := migration.NewRunnerWithOptions([]migration.Migration{
		{Version: "001", Description: "clocked migration"},
	}, migration.RunnerOptions{Clock: clock.NewFake(now)})

	if err := runner.Up(ctx, store); err != nil {
		t.Fatalf("Up returned error: %v", err)
	}

	var appliedAt int64
	if err := store.QueryRow(ctx, `SELECT applied_at FROM schema_migrations WHERE version = '001'`).Scan(&appliedAt); err != nil {
		t.Fatalf("query applied_at: %v", err)
	}
	if appliedAt != now.UnixNano() {
		t.Fatalf("applied_at = %d, want %d", appliedAt, now.UnixNano())
	}
}

func TestRunnerRegistersBusinessInitializersWithRoleAndOrder(t *testing.T) {
	ctx := context.Background()
	core, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "migration-init-core"})
	if err != nil {
		t.Fatalf("open core: %v", err)
	}
	defer core.Close()
	events, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "migration-init-events"})
	if err != nil {
		t.Fatalf("open events: %v", err)
	}
	defer events.Close()
	registry := migration.NewRegistry()
	registry.Register("events", migration.Migration{
		Version:     "002_events_projection",
		Description: "create events projection",
		Statements:  []string{`CREATE TABLE events_projection (id TEXT PRIMARY KEY)`},
	})
	registry.Register("core", migration.Migration{
		Version:     "001_core_profile",
		Description: "create core profile",
		Statements:  []string{`CREATE TABLE core_profile (id TEXT PRIMARY KEY)`},
	})

	if err := registry.Up(ctx, map[string]*db.Store{"core": core, "events": events}); err != nil {
		t.Fatalf("registry Up returned error: %v", err)
	}

	assertTableExists(t, core, "core_profile")
	assertTableExists(t, events, "events_projection")
	if assertLatest, err := registry.LatestApplied(ctx, map[string]*db.Store{"core": core, "events": events}); err != nil || !assertLatest {
		t.Fatalf("LatestApplied = %v err=%v, want true", assertLatest, err)
	}
}

func TestCoordinatorOwnsDatabaseRegistryAndBusinessOnlyRegistersInitializers(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{
		Databases: map[string]db.Config{
			db.RoleCore:          {Driver: db.DriverMemory, Name: "coordinator-core"},
			db.RoleEvents:        {Driver: db.DriverMemory, Name: "coordinator-events"},
			db.RoleJobs:          {Driver: db.DriverMemory, Name: "coordinator-jobs"},
			db.RoleRuntime:       {Driver: db.DriverMemory, Name: "coordinator-runtime"},
			db.RoleObservability: {Driver: db.DriverMemory, Name: "coordinator-observability"},
		},
	})
	if err != nil {
		t.Fatalf("OpenRegistry returned error: %v", err)
	}
	defer databases.Close()
	coordinator := migration.NewCoordinator(databases)

	if err := coordinator.RegisterInitializer(db.RoleCore, migration.Migration{
		Version:     "010_business_profiles",
		Description: "create business profiles",
		Statements:  []string{`CREATE TABLE business_profiles (id TEXT PRIMARY KEY)`},
	}); err != nil {
		t.Fatalf("RegisterInitializer returned error: %v", err)
	}
	if err := coordinator.Up(ctx); err != nil {
		t.Fatalf("coordinator Up returned error: %v", err)
	}

	assertTableExists(t, mustStore(t, databases, db.RoleCore), "business_profiles")
	assertTableExists(t, mustStore(t, databases, db.RoleEvents), "platform_events")
	assertTableExists(t, mustStore(t, databases, db.RoleJobs), "platform_queue_jobs")
	assertTableExists(t, mustStore(t, databases, db.RoleRuntime), "platform_liveness")
	if ready, err := coordinator.LatestApplied(ctx); err != nil || !ready {
		t.Fatalf("LatestApplied = %v err=%v, want true", ready, err)
	}
}

func TestOpenCoordinatorFromDatabaseURLsOwnsRegistryLifecycle(t *testing.T) {
	ctx := context.Background()
	registry, err := migration.OpenCoordinatorFromDatabaseURLs(ctx, map[string]string{
		db.RoleCore:          "memory://coordinator-from-config-core",
		db.RoleEvents:        "memory://coordinator-from-config-events",
		db.RoleJobs:          "memory://coordinator-from-config-jobs",
		db.RoleRuntime:       "memory://coordinator-from-config-runtime",
		db.RoleObservability: "memory://coordinator-from-config-observability",
	})
	if err != nil {
		t.Fatalf("OpenCoordinatorFromDatabaseURLs returned error: %v", err)
	}
	defer registry.Close()

	if err := registry.RegisterInitializer(db.RoleCore, migration.Migration{
		Version:     "010_business_profiles",
		Description: "create business profiles",
		Statements:  []string{`CREATE TABLE business_profiles_from_config (id TEXT PRIMARY KEY)`},
	}); err != nil {
		t.Fatalf("RegisterInitializer returned error: %v", err)
	}
	if err := registry.Up(ctx); err != nil {
		t.Fatalf("coordinator Up returned error: %v", err)
	}

	assertTableExists(t, mustStore(t, registry.Databases(), db.RoleCore), "business_profiles_from_config")
	assertTableExists(t, mustStore(t, registry.Databases(), db.RoleEvents), "platform_events")
	if err := registry.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if err := mustStore(t, registry.Databases(), db.RoleCore).Ping(ctx); err == nil {
		t.Fatalf("core database ping after coordinator Close returned nil error")
	}
}

func TestRegistryOrdersSameRoleMigrationsByVersion(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "migration-init-order"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	registry := migration.NewRegistry()
	registry.Register("core", migration.Migration{
		Version:     "002_insert_profile",
		Description: "insert profile",
		Statements:  []string{`INSERT INTO profile_order (id) VALUES ('p1')`},
	})
	registry.Register("core", migration.Migration{
		Version:     "001_create_profile",
		Description: "create profile",
		Statements:  []string{`CREATE TABLE profile_order (id TEXT PRIMARY KEY)`},
	})

	if err := registry.Up(ctx, map[string]*db.Store{"core": store}); err != nil {
		t.Fatalf("registry Up returned error: %v", err)
	}
	var count int
	if err := store.QueryRow(ctx, `SELECT COUNT(*) FROM profile_order WHERE id = 'p1'`).Scan(&count); err != nil {
		t.Fatalf("query profile_order: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}

func TestRunnerOrdersMigrationsByVersion(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "migration-runner-order"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	runner := migration.NewRunner([]migration.Migration{
		{
			Version:     "002_insert_profile",
			Description: "insert profile",
			Statements:  []string{`INSERT INTO runner_profile_order (id) VALUES ('p1')`},
		},
		{
			Version:     "001_create_profile",
			Description: "create profile",
			Statements:  []string{`CREATE TABLE runner_profile_order (id TEXT PRIMARY KEY)`},
		},
	})

	if err := runner.Up(ctx, store); err != nil {
		t.Fatalf("runner Up returned error: %v", err)
	}
	var count int
	if err := store.QueryRow(ctx, `SELECT COUNT(*) FROM runner_profile_order WHERE id = 'p1'`).Scan(&count); err != nil {
		t.Fatalf("query runner_profile_order: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}

func TestRegistryRejectsDuplicateMigrationVersionsBeforeApplying(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "migration-duplicate-version"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	registry := migration.NewRegistry()
	registry.Register("core", migration.Migration{
		Version:     "001_profile",
		Description: "create duplicate guard",
		Statements:  []string{`CREATE TABLE duplicate_guard (id TEXT PRIMARY KEY)`},
	})
	registry.Register("core", migration.Migration{
		Version:     "001_profile",
		Description: "duplicate version",
		Statements:  []string{`CREATE TABLE duplicate_guard_second (id TEXT PRIMARY KEY)`},
	})

	if err := registry.Up(ctx, map[string]*db.Store{"core": store}); err == nil {
		t.Fatalf("registry Up returned nil error for duplicate migration version")
	}
	assertTableMissing(t, store, "duplicate_guard")
	assertTableMissing(t, store, "duplicate_guard_second")
}

func TestRunnerRejectsDuplicateMigrationVersionsBeforeApplying(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "migration-runner-duplicate-version"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	runner := migration.NewRunner([]migration.Migration{
		{
			Version:     "001_profile",
			Description: "create duplicate guard",
			Statements:  []string{`CREATE TABLE runner_duplicate_guard (id TEXT PRIMARY KEY)`},
		},
		{
			Version:     "001_profile",
			Description: "duplicate version",
			Statements:  []string{`CREATE TABLE runner_duplicate_guard_second (id TEXT PRIMARY KEY)`},
		},
	})

	if err := runner.Up(ctx, store); err == nil {
		t.Fatalf("runner Up returned nil error for duplicate migration version")
	}
	assertTableMissing(t, store, "runner_duplicate_guard")
	assertTableMissing(t, store, "runner_duplicate_guard_second")
}

func TestPlatformMigrationRegistryRoutesInfrastructureTablesToDatabaseRoles(t *testing.T) {
	ctx := context.Background()
	stores := map[string]*db.Store{}
	for _, role := range []string{"core", "events", "jobs", "runtime", "observability"} {
		store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "platform-role-" + role})
		if err != nil {
			t.Fatalf("open %s store: %v", role, err)
		}
		defer store.Close()
		stores[role] = store
	}

	if err := migration.PlatformMigrationRegistry().Up(ctx, stores); err != nil {
		t.Fatalf("PlatformMigrationRegistry Up returned error: %v", err)
	}

	assertTableExists(t, stores["events"], "platform_events")
	assertTableMissing(t, stores["core"], "platform_events")
	assertTableExists(t, stores["jobs"], "platform_outbox_jobs")
	assertTableExists(t, stores["jobs"], "platform_queue_jobs")
	assertTableMissing(t, stores["events"], "platform_queue_jobs")
	assertTableExists(t, stores["runtime"], "platform_locks")
	assertTableExists(t, stores["runtime"], "platform_claim_leases")
	assertTableExists(t, stores["runtime"], "platform_liveness")
}

func TestRunnerRollsBackFailedMigrationAndDoesNotMarkApplied(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "migration-rollback"})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	runner := migration.NewRunner([]migration.Migration{
		{
			Version:     "001",
			Description: "broken migration",
			Statements: []string{
				`CREATE TABLE rollback_widgets (id TEXT PRIMARY KEY)`,
				`INSERT INTO missing_table (id) VALUES ('w1')`,
			},
		},
	})

	if err := runner.Up(ctx, store); err == nil {
		t.Fatalf("Up returned nil error for broken migration")
	}

	var count int
	err = store.QueryRow(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = '001'`).Scan(&count)
	if err != nil {
		t.Fatalf("query schema_migrations after failed migration: %v", err)
	}
	if count != 0 {
		t.Fatalf("schema_migrations count = %d, want 0", count)
	}
	if err := store.QueryRow(ctx, `SELECT COUNT(*) FROM rollback_widgets`).Scan(&count); err == nil {
		t.Fatalf("rollback_widgets table still exists after failed migration")
	}
}

func assertTableExists(t *testing.T, store *db.Store, table string) {
	t.Helper()
	exists, err := store.TableExists(context.Background(), table)
	if err != nil {
		t.Fatalf("query table %s: %v", table, err)
	}
	if !exists {
		t.Fatalf("table %s missing", table)
	}
}

func assertTableMissing(t *testing.T, store *db.Store, table string) {
	t.Helper()
	exists, err := store.TableExists(context.Background(), table)
	if err != nil {
		t.Fatalf("query table %s: %v", table, err)
	}
	if exists {
		t.Fatalf("table %s exists unexpectedly", table)
	}
}

func TestPlatformMigrationsCreateRequiredInfrastructureTables(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "platform-migrations"})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()

	if err := migration.NewRunner(migration.PlatformMigrations()).Up(ctx, store); err != nil {
		t.Fatalf("Up returned error: %v", err)
	}

	for _, table := range []string{
		"schema_migrations",
		"platform_events",
		"platform_event_idempotency",
		"platform_outbox_jobs",
		"platform_queue_jobs",
		"platform_locks",
		"platform_claim_leases",
		"platform_negative_claim_cache",
		"platform_liveness",
	} {
		t.Run(table, func(t *testing.T) {
			exists, err := store.TableExists(ctx, table)
			if err != nil {
				t.Fatalf("query table %s: %v", table, err)
			}
			if !exists {
				t.Fatalf("table %s missing", table)
			}
		})
	}
}

func TestPostgresRunnerAppliesPlatformMigrations(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{
		Driver: db.DriverPostgres,
		URL:    postgresTestURL(t),
		Name:   "postgres-platform-migrations",
	})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	for _, table := range []string{
		"schema_migrations",
		"platform_events",
		"platform_event_idempotency",
		"platform_outbox_jobs",
		"platform_queue_jobs",
		"platform_locks",
		"platform_claim_leases",
		"platform_negative_claim_cache",
		"platform_liveness",
	} {
		dropPostgresTable(t, store, table)
	}

	if err := migration.NewRunner(migration.PlatformMigrations()).Up(ctx, store); err != nil {
		t.Fatalf("Up returned error: %v", err)
	}

	for _, table := range []string{
		"schema_migrations",
		"platform_events",
		"platform_event_idempotency",
		"platform_outbox_jobs",
		"platform_queue_jobs",
		"platform_locks",
		"platform_claim_leases",
		"platform_negative_claim_cache",
		"platform_liveness",
	} {
		t.Run(table, func(t *testing.T) {
			assertTableExists(t, store, table)
		})
	}
	for _, column := range []struct {
		table string
		name  string
		want  string
	}{
		{table: "schema_migrations", name: "applied_at", want: "bigint"},
		{table: "platform_outbox_jobs", name: "payload", want: "bytea"},
		{table: "platform_outbox_jobs", name: "next_attempt_at", want: "bigint"},
		{table: "platform_outbox_jobs", name: "locked_until", want: "bigint"},
		{table: "platform_outbox_jobs", name: "created_at", want: "bigint"},
		{table: "platform_outbox_jobs", name: "updated_at", want: "bigint"},
		{table: "platform_queue_jobs", name: "payload", want: "bytea"},
		{table: "platform_queue_jobs", name: "locked_until", want: "bigint"},
		{table: "platform_queue_jobs", name: "created_at", want: "bigint"},
		{table: "platform_queue_jobs", name: "updated_at", want: "bigint"},
		{table: "platform_queue_jobs", name: "dispatched_at", want: "bigint"},
		{table: "platform_queue_jobs", name: "started_at", want: "bigint"},
		{table: "platform_queue_jobs", name: "completed_at", want: "bigint"},
		{table: "platform_queue_jobs", name: "next_attempt_at", want: "bigint"},
		{table: "platform_locks", name: "expires_at", want: "bigint"},
		{table: "platform_locks", name: "updated_at", want: "bigint"},
		{table: "platform_claim_leases", name: "expires_at", want: "bigint"},
		{table: "platform_claim_leases", name: "updated_at", want: "bigint"},
		{table: "platform_negative_claim_cache", name: "updated_at", want: "bigint"},
		{table: "platform_liveness", name: "last_seen_at", want: "bigint"},
		{table: "platform_liveness", name: "expires_at", want: "bigint"},
	} {
		assertPostgresColumnType(t, store, column.table, column.name, column.want)
	}
}

func TestPlatformMigrationsCreateSchemasUsableByInfrastructureModules(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "platform-migrations-usable"})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner(migration.PlatformMigrations()).Up(ctx, store); err != nil {
		t.Fatalf("Up returned error: %v", err)
	}

	events := eventstore.New(store)
	if err := events.Append(ctx, eventstore.Envelope{
		EventID:       "evt-1",
		StreamID:      "stream-1",
		StreamVersion: 1,
		EventType:     "sample.created",
		EventVersion:  1,
		Payload:       []byte(`{"ok":true}`),
		Metadata:      []byte(`{"source":"test"}`),
	}); err != nil {
		t.Fatalf("event Append after PlatformMigrations: %v", err)
	}
	loaded, err := events.Load(ctx, "stream-1")
	if err != nil || len(loaded) != 1 {
		t.Fatalf("event Load after PlatformMigrations events=%+v err=%v", loaded, err)
	}

	jobs := outbox.New(store)
	if err := jobs.Enqueue(ctx, outbox.Job{ID: "job-1", Topic: "projection", Payload: []byte("payload")}); err != nil {
		t.Fatalf("outbox Enqueue after PlatformMigrations: %v", err)
	}
	job, ok := jobs.Get(ctx, "job-1")
	if !ok || job.Topic != "projection" {
		t.Fatalf("outbox Get after PlatformMigrations ok=%v job=%+v", ok, job)
	}

	queueStore, err := execution.NewQueue(ctx, store, execution.QueueConfig{ClaimTTL: time.Minute, MaxAttempts: 2})
	if err != nil {
		t.Fatalf("queue NewSQLite after PlatformMigrations: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	if err := queueStore.Enqueue(ctx, execution.Job{ID: "queue-1", Topic: "work", Payload: []byte("payload")}, now); err != nil {
		t.Fatalf("queue Enqueue after PlatformMigrations: %v", err)
	}
	claimedJob, ok, err := queueStore.Claim(ctx, "worker-1", now)
	if err != nil || !ok || claimedJob.ID != "queue-1" {
		t.Fatalf("queue Claim after PlatformMigrations ok=%v job=%+v err=%v", ok, claimedJob, err)
	}

	lockStore, err := lock.New(ctx, store)
	if err != nil {
		t.Fatalf("lock New after PlatformMigrations: %v", err)
	}
	lease, ok, err := lockStore.TryAcquire(ctx, "resource-1", "owner-1", time.Minute, now)
	if err != nil || !ok || lease == nil {
		t.Fatalf("lock TryAcquire after PlatformMigrations ok=%v lease=%v err=%v", ok, lease, err)
	}

	claimStore, err := execution.NewClaimStore(ctx, store)
	if err != nil {
		t.Fatalf("claim NewSQLite after PlatformMigrations: %v", err)
	}
	acquired, err := claimStore.Acquire(ctx, "run-1", "worker-1", time.Minute, now)
	if err != nil || !acquired {
		t.Fatalf("claim Acquire after PlatformMigrations acquired=%v err=%v", acquired, err)
	}
	if err := claimStore.Bump(ctx, "runtime-1"); err != nil {
		t.Fatalf("claim Bump after PlatformMigrations: %v", err)
	}

	livenessStore, err := execution.NewLivenessStore(ctx, store)
	if err != nil {
		t.Fatalf("liveness NewSQLite after PlatformMigrations: %v", err)
	}
	livenessStore.Touch(ctx, "runtime-1", time.Minute, now)
	alive := livenessStore.IsAliveBatch(ctx, []string{"runtime-1"}, now)
	if !alive["runtime-1"] {
		t.Fatalf("liveness IsAliveBatch after PlatformMigrations = %+v", alive)
	}
}

func TestLatestAppliedReturnsFalseWhenLatestMigrationMissing(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "stage0"})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	runner := migration.NewRunner([]migration.Migration{
		{Version: "001", Description: "create sample"},
		{Version: "002", Description: "add sample name"},
	})

	if err := migration.NewRunner([]migration.Migration{{Version: "001", Description: "create sample"}}).Up(ctx, store); err != nil {
		t.Fatalf("Up returned error: %v", err)
	}
	ready, err := runner.LatestApplied(ctx, store)
	if err != nil {
		t.Fatalf("LatestApplied returned error: %v", err)
	}
	if ready {
		t.Fatalf("LatestApplied = true, want false")
	}
}

func TestMigrationsRemainAppliedAfterFileDatabaseReopen(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/stage0.json"
	runner := migration.NewRunner([]migration.Migration{{Version: "001", Description: "create sample"}})
	store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if err := runner.Up(ctx, store); err != nil {
		t.Fatalf("Up returned error: %v", err)
	}
	_ = store.Close()

	reopened, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatalf("reopen returned error: %v", err)
	}
	defer reopened.Close()
	ready, err := runner.LatestApplied(ctx, reopened)
	if err != nil {
		t.Fatalf("LatestApplied returned error: %v", err)
	}
	if !ready {
		t.Fatalf("LatestApplied = false after reopen")
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

func postgresTestURL(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("ORRERY_TEST_POSTGRES_URL")
	if raw == "" {
		t.Skip("ORRERY_TEST_POSTGRES_URL is not set")
	}
	return raw
}

func dropPostgresTable(t *testing.T, store *db.Store, table string) {
	t.Helper()
	if store.Dialect() != db.DialectPostgres {
		t.Fatalf("dropPostgresTable requires postgres store, got %s", store.Dialect())
	}
	if _, err := store.Exec(context.Background(), `DROP TABLE IF EXISTS `+table+` CASCADE`); err != nil {
		t.Fatalf("drop postgres table %s: %v", table, err)
	}
}

func assertPostgresColumnType(t *testing.T, store *db.Store, table, column, want string) {
	t.Helper()
	var dataType string
	if err := store.QueryRow(context.Background(), `
SELECT data_type
FROM information_schema.columns
WHERE table_schema = current_schema()
	AND table_name = ?
	AND column_name = ?`, table, column).Scan(&dataType); err != nil {
		t.Fatalf("query postgres column type %s.%s: %v", table, column, err)
	}
	if dataType != want {
		t.Fatalf("postgres column type %s.%s = %s, want %s", table, column, dataType, want)
	}
}
