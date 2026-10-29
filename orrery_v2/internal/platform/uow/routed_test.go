package uow_test

import (
	"context"
	"strings"
	"testing"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
)

func TestRoutedUnitOfWorkCommitsLogicalTableToTargetDatabase(t *testing.T) {
	ctx := context.Background()
	registry := openRegistry(t)
	defer registry.Close()
	unit := uow.NewRouted(db.NewRouter(registry))

	if err := unit.Do(ctx, func(ctx context.Context, tx uow.RoutedTx) error {
		return tx.PutRecord(ctx, "audit.audit_log", "audit-1", db.Record{"kind": "audit"})
	}); err != nil {
		t.Fatalf("Do returned error: %v", err)
	}

	if record, ok := mustStore(t, registry, db.RoleAudit).Get("audit_log", "audit-1"); !ok || record["kind"] != "audit" {
		t.Fatalf("audit database record=%+v ok=%v", record, ok)
	}
	if _, ok := mustStore(t, registry, db.RoleCore).Get("audit_log", "audit-1"); ok {
		t.Fatalf("routed unit of work wrote audit into core database")
	}
}

func TestRoutedUnitOfWorkRejectsCrossDatabaseCommitWithoutPartialWrites(t *testing.T) {
	ctx := context.Background()
	registry := openRegistry(t)
	defer registry.Close()
	unit := uow.NewRouted(db.NewRouter(registry))

	err := unit.Do(ctx, func(ctx context.Context, tx uow.RoutedTx) error {
		if err := tx.PutRecord(ctx, "audit.audit_log", "audit-1", db.Record{"kind": "audit"}); err != nil {
			return err
		}
		return tx.PutRecord(ctx, "jobs.queue_state", "job-1", db.Record{"kind": "job"})
	})
	if err == nil {
		t.Fatalf("Do returned nil error for cross-database transaction")
	}
	if !strings.Contains(err.Error(), "cross-database transaction") {
		t.Fatalf("Do error = %v, want cross-database transaction", err)
	}
	if _, ok := mustStore(t, registry, db.RoleAudit).Get("audit_log", "audit-1"); ok {
		t.Fatalf("audit write committed despite cross-database rejection")
	}
	if _, ok := mustStore(t, registry, db.RoleJobs).Get("queue_state", "job-1"); ok {
		t.Fatalf("jobs write committed despite cross-database rejection")
	}
}

func TestRoutedUnitOfWorkDoesNotCommitWhenCallbackIgnoresRoutedError(t *testing.T) {
	ctx := context.Background()
	registry := openRegistry(t)
	defer registry.Close()
	unit := uow.NewRouted(db.NewRouter(registry))

	err := unit.Do(ctx, func(ctx context.Context, tx uow.RoutedTx) error {
		if err := tx.PutRecord(ctx, "audit.audit_log", "audit-1", db.Record{"kind": "audit"}); err != nil {
			return err
		}
		_ = tx.PutRecord(ctx, "jobs.queue_state", "job-1", db.Record{"kind": "job"})
		return nil
	})
	if err == nil {
		t.Fatalf("Do returned nil error after callback ignored routed transaction error")
	}
	if !strings.Contains(err.Error(), "cross-database transaction") {
		t.Fatalf("Do error = %v, want cross-database transaction", err)
	}
	if _, ok := mustStore(t, registry, db.RoleAudit).Get("audit_log", "audit-1"); ok {
		t.Fatalf("audit write committed after ignored routed transaction error")
	}
}

func TestRoutedUnitOfWorkBeginCommitsLogicalTableToTargetDatabase(t *testing.T) {
	ctx := context.Background()
	registry := openRegistry(t)
	defer registry.Close()
	unit := uow.NewRouted(db.NewRouter(registry))

	tx, err := unit.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin returned error: %v", err)
	}
	if err := tx.PutRecord(ctx, "audit.audit_log", "audit-1", db.Record{"kind": "audit"}); err != nil {
		t.Fatalf("PutRecord returned error: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit returned error: %v", err)
	}

	if record, ok := mustStore(t, registry, db.RoleAudit).Get("audit_log", "audit-1"); !ok || record["kind"] != "audit" {
		t.Fatalf("audit database record=%+v ok=%v", record, ok)
	}
	if _, ok := mustStore(t, registry, db.RoleCore).Get("audit_log", "audit-1"); ok {
		t.Fatalf("routed explicit transaction wrote audit into core database")
	}
}

func TestRoutedUnitOfWorkBeginRollbackDiscardsLogicalWrites(t *testing.T) {
	ctx := context.Background()
	registry := openRegistry(t)
	defer registry.Close()
	unit := uow.NewRouted(db.NewRouter(registry))

	tx, err := unit.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin returned error: %v", err)
	}
	if err := tx.PutRecord(ctx, "jobs.queue_state", "job-1", db.Record{"kind": "job"}); err != nil {
		t.Fatalf("PutRecord returned error: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("Rollback returned error: %v", err)
	}

	if _, ok := mustStore(t, registry, db.RoleJobs).Get("queue_state", "job-1"); ok {
		t.Fatalf("rolled back routed explicit transaction write was committed")
	}
}

func TestRoutedUnitOfWorkBeginRejectsCrossDatabaseCommitWithoutPartialWrites(t *testing.T) {
	ctx := context.Background()
	registry := openRegistry(t)
	defer registry.Close()
	unit := uow.NewRouted(db.NewRouter(registry))

	tx, err := unit.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin returned error: %v", err)
	}
	if err := tx.PutRecord(ctx, "audit.audit_log", "audit-1", db.Record{"kind": "audit"}); err != nil {
		t.Fatalf("PutRecord audit returned error: %v", err)
	}
	err = tx.PutRecord(ctx, "jobs.queue_state", "job-1", db.Record{"kind": "job"})
	if err == nil {
		t.Fatalf("PutRecord returned nil error for cross-database explicit transaction")
	}
	if !strings.Contains(err.Error(), "cross-database transaction") {
		t.Fatalf("PutRecord error = %v, want cross-database transaction", err)
	}
	err = tx.Commit(ctx)
	if err == nil {
		t.Fatalf("Commit returned nil error after cross-database explicit transaction")
	}
	if !strings.Contains(err.Error(), "cross-database transaction") {
		t.Fatalf("Commit error = %v, want cross-database transaction", err)
	}
	if _, ok := mustStore(t, registry, db.RoleAudit).Get("audit_log", "audit-1"); ok {
		t.Fatalf("audit write committed despite cross-database explicit transaction rejection")
	}
	if _, ok := mustStore(t, registry, db.RoleJobs).Get("queue_state", "job-1"); ok {
		t.Fatalf("jobs write committed despite cross-database explicit transaction rejection")
	}
}

func openRegistry(t *testing.T) *db.Registry {
	t.Helper()
	registry, err := db.OpenRegistry(context.Background(), db.RegistryConfig{
		Databases: map[string]db.Config{
			db.RoleCore:   {Driver: db.DriverMemory, Name: t.Name() + "-core"},
			db.RoleAudit:  {Driver: db.DriverMemory, Name: t.Name() + "-audit"},
			db.RoleEvents: {Driver: db.DriverMemory, Name: t.Name() + "-events"},
			db.RoleJobs:   {Driver: db.DriverMemory, Name: t.Name() + "-jobs"},
		},
	})
	if err != nil {
		t.Fatalf("OpenRegistry returned error: %v", err)
	}
	return registry
}

func mustStore(t *testing.T, registry *db.Registry, role string) *db.Store {
	t.Helper()
	store, err := registry.Store(role)
	if err != nil {
		t.Fatalf("lookup %s database: %v", role, err)
	}
	return store
}
