package execution_test

import (
	"context"
	"orrery_v2/internal/platform/execution"
	"path/filepath"
	"testing"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/db"
)

func TestLeaseStoreRecoversStaleLease(t *testing.T) {
	ctx := context.Background()
	store := execution.NewLeaseStore()
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	if ok := store.Acquire(ctx, "run-1", "worker-a", time.Minute, now); !ok {
		t.Fatalf("first acquire failed")
	}
	if ok := store.Acquire(ctx, "run-1", "worker-b", time.Minute, now.Add(30*time.Second)); ok {
		t.Fatalf("second acquire before stale succeeded")
	}
	recovered := store.RecoverStale(ctx, now.Add(2*time.Minute))
	if recovered != 1 {
		t.Fatalf("recovered = %d, want 1", recovered)
	}
	if ok := store.Acquire(ctx, "run-1", "worker-b", time.Minute, now.Add(2*time.Minute)); !ok {
		t.Fatalf("acquire after recovery failed")
	}
}

func TestNegativeClaimCacheVersionBumpInvalidatesEmptyVerdict(t *testing.T) {
	cache := execution.NewNegativeCache()
	observed := cache.CurrentVersion("runtime-a")
	cache.MarkEmpty("runtime-a", observed)
	if !cache.IsEmpty("runtime-a") {
		t.Fatalf("expected empty verdict")
	}
	cache.Bump("runtime-a")
	if cache.IsEmpty("runtime-a") {
		t.Fatalf("empty verdict survived version bump")
	}
}

func TestSQLiteStorePersistsLeaseAndRecoversStaleAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runtime.db")
	store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	claims, err := execution.NewClaimStore(ctx, store)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	if ok, err := claims.Acquire(ctx, "run-1", "worker-a", time.Minute, now); err != nil || !ok {
		t.Fatalf("acquire ok=%v err=%v", ok, err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer reopened.Close()
	reopenedClaims, err := execution.NewClaimStore(ctx, reopened)
	if err != nil {
		t.Fatalf("NewSQLite reopened: %v", err)
	}
	if ok, err := reopenedClaims.Acquire(ctx, "run-1", "worker-b", time.Minute, now.Add(30*time.Second)); err != nil || ok {
		t.Fatalf("acquire before expiry ok=%v err=%v", ok, err)
	}
	recovered, err := reopenedClaims.RecoverStale(ctx, now.Add(2*time.Minute), 10)
	if err != nil {
		t.Fatalf("RecoverStale: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered = %d, want 1", recovered)
	}
	if ok, err := reopenedClaims.Acquire(ctx, "run-1", "worker-b", time.Minute, now.Add(2*time.Minute)); err != nil || !ok {
		t.Fatalf("acquire after recovery ok=%v err=%v", ok, err)
	}
}

func TestSQLiteStoreRenewAndReleaseRequireOwner(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-claim-owner"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	claims, err := execution.NewClaimStore(ctx, store)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	if ok, err := claims.Acquire(ctx, "run-1", "worker-a", time.Minute, now); err != nil || !ok {
		t.Fatalf("acquire ok=%v err=%v", ok, err)
	}
	if renewed, err := claims.Renew(ctx, "run-1", "worker-b", time.Minute, now.Add(30*time.Second)); err != nil || renewed {
		t.Fatalf("wrong-owner renew renewed=%v err=%v", renewed, err)
	}
	if released, err := claims.Release(ctx, "run-1", "worker-b"); err != nil || released {
		t.Fatalf("wrong-owner release released=%v err=%v", released, err)
	}
	if renewed, err := claims.Renew(ctx, "run-1", "worker-a", 2*time.Minute, now.Add(30*time.Second)); err != nil || !renewed {
		t.Fatalf("owner renew renewed=%v err=%v", renewed, err)
	}
	if released, err := claims.Release(ctx, "run-1", "worker-a"); err != nil || !released {
		t.Fatalf("owner release released=%v err=%v", released, err)
	}
}

func TestSQLiteStoreAllowsExpiredOwnerRenewUntilTakeover(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-claim-late-renew"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	claims, err := execution.NewClaimStore(ctx, store)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Date(2026, 9, 1, 4, 0, 0, 0, time.UTC)
	if ok, err := claims.Acquire(ctx, "run-late", "worker-a", time.Minute, now); err != nil || !ok {
		t.Fatalf("acquire ok=%v err=%v", ok, err)
	}
	if renewed, err := claims.Renew(ctx, "run-late", "worker-a", time.Minute, now.Add(2*time.Minute)); err != nil || !renewed {
		t.Fatalf("late owner renew renewed=%v err=%v", renewed, err)
	}
	if acquired, err := claims.Acquire(ctx, "run-late", "worker-b", time.Minute, now.Add(2*time.Minute+30*time.Second)); err != nil || acquired {
		t.Fatalf("takeover before renewed expiry acquired=%v err=%v", acquired, err)
	}
	if acquired, err := claims.Acquire(ctx, "run-late", "worker-b", time.Minute, now.Add(3*time.Minute)); err != nil || !acquired {
		t.Fatalf("takeover after renewed expiry acquired=%v err=%v", acquired, err)
	}
	if renewed, err := claims.Renew(ctx, "run-late", "worker-a", time.Minute, now.Add(3*time.Minute+time.Second)); err != nil || renewed {
		t.Fatalf("old owner renewed after takeover renewed=%v err=%v", renewed, err)
	}
}

func TestClaimsServiceRetainsExpiredClaimsForLateOwnerRecovery(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-claim-cleanup-grace"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	claims, err := execution.NewClaimStore(ctx, store)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	now := time.Date(2026, 9, 1, 4, 0, 0, 0, time.UTC)
	fakeClock := clock.NewFake(now)
	service := execution.NewClaimsService(claims, fakeClock, time.Minute)
	for _, runID := range []string{"run-cleanup", "run-takeover"} {
		if ok, err := service.Acquire(ctx, runID, "worker-a"); err != nil || !ok {
			t.Fatalf("acquire %s ok=%v err=%v", runID, ok, err)
		}
	}

	fakeClock.Advance(2 * time.Minute)
	if recovered, err := service.RecoverStale(ctx, 10); err != nil || recovered != 0 {
		t.Fatalf("recover inside grace recovered=%d err=%v", recovered, err)
	}
	if acquired, err := service.Acquire(ctx, "run-takeover", "worker-b"); err != nil || !acquired {
		t.Fatalf("takeover during cleanup grace acquired=%v err=%v", acquired, err)
	}

	fakeClock.Advance(4 * time.Minute)
	if recovered, err := service.RecoverStale(ctx, 10); err != nil || recovered != 1 {
		t.Fatalf("recover after grace recovered=%d err=%v", recovered, err)
	}
}

func TestSQLiteNegativeClaimCacheVersionBumpPersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runtime.db")
	store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	claims, err := execution.NewClaimStore(ctx, store)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	version, err := claims.CurrentVersion(ctx, "runtime-a")
	if err != nil {
		t.Fatalf("CurrentVersion: %v", err)
	}
	if err := claims.MarkEmpty(ctx, "runtime-a", version); err != nil {
		t.Fatalf("MarkEmpty: %v", err)
	}
	if empty, err := claims.IsEmpty(ctx, "runtime-a"); err != nil || !empty {
		t.Fatalf("IsEmpty before bump empty=%v err=%v", empty, err)
	}
	if err := claims.Bump(ctx, "runtime-a"); err != nil {
		t.Fatalf("Bump: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer reopened.Close()
	reopenedClaims, err := execution.NewClaimStore(ctx, reopened)
	if err != nil {
		t.Fatalf("NewSQLite reopened: %v", err)
	}
	if empty, err := reopenedClaims.IsEmpty(ctx, "runtime-a"); err != nil || empty {
		t.Fatalf("IsEmpty after persisted bump empty=%v err=%v", empty, err)
	}
}

func TestSQLiteNegativeClaimCacheUsesInjectedClock(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "sqlite-claim-clock"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	now := time.Date(2026, 5, 31, 19, 0, 0, 0, time.UTC)
	claims, err := execution.NewClaimStoreWithOptions(ctx, store, execution.ClaimStoreOptions{
		Clock: clock.NewFake(now),
	})
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}

	if err := claims.Bump(ctx, "runtime-a"); err != nil {
		t.Fatalf("Bump: %v", err)
	}
	var bumpedAt int64
	if err := store.QueryRow(ctx, `SELECT updated_at FROM platform_negative_claim_cache WHERE cache_key = ?`, "runtime-a").Scan(&bumpedAt); err != nil {
		t.Fatalf("query bumped updated_at: %v", err)
	}
	if bumpedAt != now.UnixNano() {
		t.Fatalf("bump updated_at = %d, want %d", bumpedAt, now.UnixNano())
	}

	if err := claims.MarkEmpty(ctx, "runtime-a", 1); err != nil {
		t.Fatalf("MarkEmpty: %v", err)
	}
	var markedAt int64
	if err := store.QueryRow(ctx, `SELECT updated_at FROM platform_negative_claim_cache WHERE cache_key = ?`, "runtime-a").Scan(&markedAt); err != nil {
		t.Fatalf("query marked updated_at: %v", err)
	}
	if markedAt != now.UnixNano() {
		t.Fatalf("mark empty updated_at = %d, want %d", markedAt, now.UnixNano())
	}
}
