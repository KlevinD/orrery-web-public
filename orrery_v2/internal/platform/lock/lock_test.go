package lock_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/lock"
	"orrery_v2/internal/platform/testkit"
)

func TestMemoryLockPreventsDuplicateAcquireUntilExpiry(t *testing.T) {
	ctx := context.Background()
	locks := testkit.NewEphemeralLock()
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	first, ok := locks.TryAcquire(ctx, "run-1", "owner-a", time.Minute, now)
	if !ok {
		t.Fatalf("first acquire failed")
	}
	defer first.Release(ctx)
	if _, ok := locks.TryAcquire(ctx, "run-1", "owner-b", time.Minute, now); ok {
		t.Fatalf("second acquire succeeded before expiry")
	}
	if _, ok := locks.TryAcquire(ctx, "run-1", "owner-b", time.Minute, now.Add(2*time.Minute)); !ok {
		t.Fatalf("acquire after expiry failed")
	}
}

func TestSQLiteLockPreventsDuplicateAcquireUntilExpiryAndPersists(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runtime.db")
	store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	locks, err := lock.NewSQLite(ctx, store)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	if _, ok, err := locks.TryAcquire(ctx, "resource", "owner-a", time.Minute, now); err != nil || !ok {
		t.Fatalf("first acquire ok=%v err=%v", ok, err)
	}
	if _, ok, err := locks.TryAcquire(ctx, "resource", "owner-b", time.Minute, now.Add(10*time.Second)); err != nil || ok {
		t.Fatalf("second acquire before expiry ok=%v err=%v", ok, err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer reopened.Close()
	reopenedLocks, err := lock.NewSQLite(ctx, reopened)
	if err != nil {
		t.Fatalf("NewSQLite reopened: %v", err)
	}
	if _, ok, err := reopenedLocks.TryAcquire(ctx, "resource", "owner-b", time.Minute, now.Add(30*time.Second)); err != nil || ok {
		t.Fatalf("reopened acquire before expiry ok=%v err=%v", ok, err)
	}
	if _, ok, err := reopenedLocks.TryAcquire(ctx, "resource", "owner-b", time.Minute, now.Add(2*time.Minute)); err != nil || !ok {
		t.Fatalf("reopened acquire after expiry ok=%v err=%v", ok, err)
	}
}

func TestSQLiteLockReleaseAndRenewRequireOwner(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-lock-owner"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	locks, err := lock.NewSQLite(ctx, store)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	if _, ok, err := locks.TryAcquire(ctx, "resource", "owner-a", time.Minute, now); err != nil || !ok {
		t.Fatalf("acquire ok=%v err=%v", ok, err)
	}
	if released, err := locks.Release(ctx, "resource", "owner-b"); err != nil || released {
		t.Fatalf("wrong-owner release released=%v err=%v", released, err)
	}
	if renewed, err := locks.Renew(ctx, "resource", "owner-b", time.Minute, now.Add(30*time.Second)); err != nil || renewed {
		t.Fatalf("wrong-owner renew renewed=%v err=%v", renewed, err)
	}
	if renewed, err := locks.Renew(ctx, "resource", "owner-a", 2*time.Minute, now.Add(30*time.Second)); err != nil || !renewed {
		t.Fatalf("owner renew renewed=%v err=%v", renewed, err)
	}
	if _, ok, err := locks.TryAcquire(ctx, "resource", "owner-b", time.Minute, now.Add(90*time.Second)); err != nil || ok {
		t.Fatalf("acquire before renewed expiry ok=%v err=%v", ok, err)
	}
	if released, err := locks.Release(ctx, "resource", "owner-a"); err != nil || !released {
		t.Fatalf("owner release released=%v err=%v", released, err)
	}
	if _, ok, err := locks.TryAcquire(ctx, "resource", "owner-b", time.Minute, now.Add(90*time.Second)); err != nil || !ok {
		t.Fatalf("acquire after release ok=%v err=%v", ok, err)
	}
}

func TestSQLiteLockSweepsExpiredInBatches(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-lock-sweep"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	locks, err := lock.NewSQLite(ctx, store)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	for _, key := range []string{"a", "b", "c"} {
		if _, ok, err := locks.TryAcquire(ctx, key, "owner", time.Minute, now); err != nil || !ok {
			t.Fatalf("acquire %s ok=%v err=%v", key, ok, err)
		}
	}

	removed, err := locks.SweepExpired(ctx, now.Add(2*time.Minute), 2)
	if err != nil {
		t.Fatalf("SweepExpired: %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	removed, err = locks.SweepExpired(ctx, now.Add(2*time.Minute), 2)
	if err != nil {
		t.Fatalf("SweepExpired second: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed second = %d, want 1", removed)
	}
}

func TestLockStoreGenericConstructorWorksWithSQLite(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "generic-lock"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	locks, err := lock.New(ctx, store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	if _, ok, err := locks.TryAcquire(ctx, "resource", "owner-a", time.Minute, now); err != nil || !ok {
		t.Fatalf("generic acquire ok=%v err=%v", ok, err)
	}
}

func TestPostgresLockStoreAcquireRenewReleaseAndSweep(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{
		Driver: db.DriverPostgres,
		URL:    postgresTestURL(t),
		Name:   "postgres-lock",
	})
	if err != nil {
		t.Fatalf("open postgres db: %v", err)
	}
	defer store.Close()
	dropLockTables(t, store)
	locks, err := lock.New(ctx, store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	if _, ok, err := locks.TryAcquire(ctx, "resource", "owner-a", time.Minute, now); err != nil || !ok {
		t.Fatalf("first acquire ok=%v err=%v", ok, err)
	}
	if _, ok, err := locks.TryAcquire(ctx, "resource", "owner-b", time.Minute, now.Add(10*time.Second)); err != nil || ok {
		t.Fatalf("second acquire before expiry ok=%v err=%v", ok, err)
	}
	if renewed, err := locks.Renew(ctx, "resource", "owner-a", 2*time.Minute, now.Add(30*time.Second)); err != nil || !renewed {
		t.Fatalf("renew renewed=%v err=%v", renewed, err)
	}
	if released, err := locks.Release(ctx, "resource", "owner-a"); err != nil || !released {
		t.Fatalf("release released=%v err=%v", released, err)
	}
	for _, key := range []string{"expired-a", "expired-b"} {
		if _, ok, err := locks.TryAcquire(ctx, key, "owner", time.Minute, now); err != nil || !ok {
			t.Fatalf("acquire %s ok=%v err=%v", key, ok, err)
		}
	}
	removed, err := locks.SweepExpired(ctx, now.Add(2*time.Minute), 1)
	if err != nil || removed != 1 {
		t.Fatalf("SweepExpired removed=%d err=%v, want 1 nil", removed, err)
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

func dropLockTables(t *testing.T, store *db.Store) {
	t.Helper()
	if store.Dialect() != db.DialectPostgres {
		t.Fatalf("dropLockTables requires postgres store, got %s", store.Dialect())
	}
	if _, err := store.Exec(context.Background(), `DROP TABLE IF EXISTS platform_locks CASCADE`); err != nil {
		t.Fatalf("drop platform_locks: %v", err)
	}
}
