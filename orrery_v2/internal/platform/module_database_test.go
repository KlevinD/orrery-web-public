package platform_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/realtime"
)

func TestBusinessModuleRegistersSQLMigrationWithoutMigrationTypes(t *testing.T) {
	ctx := context.Background()
	cfg := databaseRuntimeConfig(t)
	module := platform.ModuleFunc(func(ctx context.Context, registrar *platform.ModuleRegistrar) error {
		migration := businessProfileMigration()
		return registrar.RegisterSQLMigration(platform.DatabaseRoleCore, migration.Version, migration.Description, migration.Statements...)
	})

	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}
	defer rt.Close()
	if err := rt.Migrations.Up(ctx); err != nil {
		t.Fatalf("migration up: %v", err)
	}

	store, ok := rt.Database(platform.DatabaseRoleCore)
	if !ok {
		t.Fatalf("core database is not available")
	}
	if err := store.Exec(ctx, `INSERT INTO business_profiles (id) VALUES (?)`, "profile-1"); err != nil {
		t.Fatalf("business initializer table is not available: %v", err)
	}
	var id string
	if err := store.QueryRow(ctx, `SELECT id FROM business_profiles WHERE id = ?`, "profile-1").Scan(&id); err != nil {
		t.Fatalf("business repository query failed: %v", err)
	}
	if id != "profile-1" {
		t.Fatalf("queried profile id = %q, want profile-1", id)
	}
}

func TestRuntimeInitializeRunsMigrationsBeforeBusinessInitializers(t *testing.T) {
	ctx := context.Background()
	cfg := databaseRuntimeConfig(t)
	events := make(chan string, 2)
	module := platform.ModuleFunc(func(ctx context.Context, registrar *platform.ModuleRegistrar) error {
		migration := businessProfileMigration()
		if err := registrar.RegisterSQLMigration(platform.DatabaseRoleCore, migration.Version, migration.Description, migration.Statements...); err != nil {
			return err
		}
		return registrar.RegisterInitializer("business.profile.seed", func(ctx context.Context, init platform.InitializerContext) error {
			events <- "initializer"
			var _ *platform.DBRouter = init.DBRouter()
			var _ *platform.UnitOfWork = init.UnitOfWork()
			var _ *platform.RoutedUnitOfWork = init.RoutedUOW()
			return init.RoutedUOW().Do(ctx, func(ctx context.Context, tx platform.RoutedTx) error {
				return tx.PutRecord(ctx, "core.business_profiles", "profile-1", platform.Record{"id": "profile-1"})
			})
		})
	})

	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}
	defer rt.Close()
	if err := rt.Initialize(ctx); err != nil {
		t.Fatalf("Initialize returned error: %v", err)
	}

	select {
	case got := <-events:
		if got != "initializer" {
			t.Fatalf("event = %q, want initializer", got)
		}
	default:
		t.Fatalf("business initializer did not run")
	}
	record, ok := rt.DBRouter.Get("core.business_profiles", "profile-1")
	if !ok || record["id"] != "profile-1" {
		t.Fatalf("seeded profile record=%+v ok=%v", record, ok)
	}
	ready := rt.Ready(ctx)
	if !ready.OK {
		t.Fatalf("ready report not ok after initializer: %+v", ready)
	}
	if !hasCheck(ready.Checks, "initializer.business.profile.seed", true) {
		t.Fatalf("initializer readiness check missing: %+v", ready.Checks)
	}
}

func TestRuntimeInitializeReportsBusinessInitializerFailureInReadiness(t *testing.T) {
	ctx := context.Background()
	cfg := databaseRuntimeConfig(t)
	module := platform.ModuleFunc(func(ctx context.Context, registrar *platform.ModuleRegistrar) error {
		return registrar.RegisterInitializer("business.profile.seed", func(ctx context.Context, init platform.InitializerContext) error {
			return errors.New("seed failed")
		})
	})

	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}
	defer rt.Close()
	if err := rt.Initialize(ctx); err == nil {
		t.Fatalf("Initialize returned nil error")
	}

	ready := rt.Ready(ctx)
	if ready.OK {
		t.Fatalf("ready report OK after initializer failure: %+v", ready)
	}
	if !hasCheck(ready.Checks, "initializer.business.profile.seed", false) {
		t.Fatalf("initializer failure readiness check missing: %+v", ready.Checks)
	}
}

func TestRuntimeInitializeDoesNotRerunSuccessfulBusinessInitializer(t *testing.T) {
	ctx := context.Background()
	cfg := databaseRuntimeConfig(t)
	calls := 0
	module := platform.ModuleFunc(func(ctx context.Context, registrar *platform.ModuleRegistrar) error {
		return registrar.RegisterInitializer("business.profile.seed", func(ctx context.Context, init platform.InitializerContext) error {
			calls++
			return nil
		})
	})

	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}
	defer rt.Close()
	if err := rt.Initialize(ctx); err != nil {
		t.Fatalf("first Initialize returned error: %v", err)
	}
	if err := rt.Initialize(ctx); err != nil {
		t.Fatalf("second Initialize returned error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("initializer calls = %d, want 1", calls)
	}
}

func TestRuntimeInitializeRetriesFailedBusinessInitializer(t *testing.T) {
	ctx := context.Background()
	cfg := databaseRuntimeConfig(t)
	calls := 0
	module := platform.ModuleFunc(func(ctx context.Context, registrar *platform.ModuleRegistrar) error {
		return registrar.RegisterInitializer("business.profile.seed", func(ctx context.Context, init platform.InitializerContext) error {
			calls++
			if calls == 1 {
				return errors.New("seed failed")
			}
			return nil
		})
	})

	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}
	defer rt.Close()
	if err := rt.Initialize(ctx); err == nil {
		t.Fatalf("first Initialize returned nil error")
	}
	if err := rt.Initialize(ctx); err != nil {
		t.Fatalf("second Initialize returned error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("initializer calls = %d, want 2", calls)
	}
}

func TestModuleRegistrarRejectsDuplicateBusinessInitializers(t *testing.T) {
	ctx := context.Background()
	cfg := databaseRuntimeConfig(t)
	module := platform.ModuleFunc(func(ctx context.Context, registrar *platform.ModuleRegistrar) error {
		if err := registrar.RegisterInitializer("business.profile.seed", func(context.Context, platform.InitializerContext) error {
			return nil
		}); err != nil {
			return err
		}
		return registrar.RegisterInitializer("business.profile.seed", func(context.Context, platform.InitializerContext) error {
			return nil
		})
	})

	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if rt != nil {
		_ = rt.Close()
	}
	if err == nil {
		t.Fatalf("OpenWithModules returned nil error")
	}
}

func TestBusinessModuleRegistersRealtimeOutboxMigrationInJobsDatabase(t *testing.T) {
	ctx := context.Background()
	cfg := databaseRuntimeConfig(t)
	module := platform.ModuleFunc(func(ctx context.Context, registrar *platform.ModuleRegistrar) error {
		migration := realtime.OutboxMigration()
		return registrar.RegisterSQLMigration(platform.DatabaseRoleJobs, migration.Version, migration.Description, migration.Statements...)
	})

	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}
	defer rt.Close()
	if err := rt.Initialize(ctx); err != nil {
		t.Fatalf("Initialize returned error: %v", err)
	}

	jobs, ok := rt.Database(platform.DatabaseRoleJobs)
	if !ok {
		t.Fatalf("jobs database is not available")
	}
	exists, err := jobs.TableExists(ctx, "realtime_outbox")
	if err != nil {
		t.Fatalf("TableExists returned error: %v", err)
	}
	if !exists {
		t.Fatalf("realtime_outbox table was not created in jobs database")
	}
}

type businessMigration struct {
	Version     string
	Description string
	Statements  []string
}

func businessProfileMigration() businessMigration {
	return businessMigration{
		Version:     "010_business_profiles",
		Description: "create business profile table",
		Statements:  []string{`CREATE TABLE business_profiles (id TEXT PRIMARY KEY)`},
	}
}

func databaseRuntimeConfig(t *testing.T) config.Config {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "orrery.yaml")
	raw := `
environment: test
paths:
  base_dir: .
database:
  urls:
    core: memory://database-initializer-core
    audit: memory://database-initializer-audit
    events: memory://database-initializer-events
    jobs: memory://database-initializer-jobs
    runtime: memory://database-initializer-runtime
    observability: memory://database-initializer-observability
filestore:
  backend: local
  root: ./artifacts
logging:
  console: true
health:
  readiness_cache_ttl: 1ms
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	cfg, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}
	return cfg
}

func hasCheck(checks []platform.ReadyCheck, name string, ok bool) bool {
	for _, check := range checks {
		if check.Name == name && check.OK == ok {
			return true
		}
	}
	return false
}
