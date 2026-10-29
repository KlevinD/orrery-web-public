package ontology

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestPlatformMappingProjectionRefreshStorePersistsPendingDelivery(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "mapping-projection-refresh"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{ontologyMappingProjectionRefreshMigration(), ontologyMappingProjectionRefreshRetryMigration()}).Up(ctx, store); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	refreshes := NewPlatformMappingProjectionRefreshStore(store)
	if revision, err := refreshes.MappingProjectionRevision(ctx); err != nil || revision != 0 {
		t.Fatalf("initial revision = %d, err=%v", revision, err)
	}
	now := time.Unix(100, 0).UTC()
	refreshes.now = func() time.Time { return now }
	if err := refreshes.EnqueueMappingProjectionRefresh(ctx, "receipt-1", "tenant-1"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := refreshes.EnqueueMappingProjectionRefresh(ctx, "receipt-1", "tenant-1"); err != nil {
		t.Fatalf("idempotent enqueue: %v", err)
	}
	if revision, err := refreshes.MappingProjectionRevision(ctx); err != nil || revision != 1 {
		t.Fatalf("revision after idempotent enqueue = %d, err=%v", revision, err)
	}
	pending, err := refreshes.ListPendingMappingProjectionRefreshes(ctx, now, 10)
	if err != nil || len(pending) != 1 || pending[0].ReceiptID != "receipt-1" {
		t.Fatalf("pending = %+v err=%v", pending, err)
	}
	nextAttemptAt := now.Add(time.Minute)
	if err := refreshes.RecordMappingProjectionRefreshFailure(ctx, "receipt-1", "projection_refresh_failed", 0, nextAttemptAt, 3); err != nil {
		t.Fatalf("record failure: %v", err)
	}
	failed, err := refreshes.GetMappingProjectionRefresh(ctx, "receipt-1")
	if err != nil || failed.State != MappingProjectionRefreshPending || failed.Attempts != 1 || failed.LastErrorCode != "projection_refresh_failed" || !failed.NextAttemptAt.Equal(nextAttemptAt) {
		t.Fatalf("failed = %+v err=%v", failed, err)
	}
	if err := refreshes.RecordMappingProjectionRefreshFailure(ctx, "receipt-1", "projection_refresh_failed", 0, nextAttemptAt, 3); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale failure error = %v, want ErrVersionConflict", err)
	}
	pending, err = refreshes.ListPendingMappingProjectionRefreshes(ctx, now, 10)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending before retry deadline = %+v err=%v", pending, err)
	}
	pending, err = refreshes.ListPendingMappingProjectionRefreshes(ctx, nextAttemptAt, 10)
	if err != nil || len(pending) != 1 || pending[0].ReceiptID != "receipt-1" {
		t.Fatalf("pending at retry deadline = %+v err=%v", pending, err)
	}
	if err := refreshes.CompleteMappingProjectionRefresh(ctx, "receipt-1"); err != nil {
		t.Fatalf("complete: %v", err)
	}
	completed, err := refreshes.GetMappingProjectionRefresh(ctx, "receipt-1")
	if err != nil || completed.State != MappingProjectionRefreshSucceeded || completed.Attempts != 2 || completed.LastErrorCode != "" {
		t.Fatalf("completed = %+v err=%v", completed, err)
	}
	pending, err = refreshes.ListPendingMappingProjectionRefreshes(ctx, nextAttemptAt, 10)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending after completion = %+v err=%v", pending, err)
	}
}

func TestPlatformMappingProjectionRefreshStoreAbandonsPoisonJobsWithoutBlockingNewWork(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "mapping-projection-refresh-abandoned"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{ontologyMappingProjectionRefreshMigration(), ontologyMappingProjectionRefreshRetryMigration()}).Up(ctx, store); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	refreshes := NewPlatformMappingProjectionRefreshStore(store)
	now := time.Unix(200, 0).UTC()
	refreshes.now = func() time.Time { return now }
	if err := refreshes.EnqueueMappingProjectionRefresh(ctx, "poison", "tenant-1"); err != nil {
		t.Fatalf("enqueue poison: %v", err)
	}
	if err := refreshes.RecordMappingProjectionRefreshFailure(ctx, "poison", "projection_refresh_failed", 0, now.Add(time.Minute), 3); err != nil {
		t.Fatalf("record first poison failure: %v", err)
	}
	if err := refreshes.EnqueueMappingProjectionRefresh(ctx, "new-work", "tenant-2"); err != nil {
		t.Fatalf("enqueue new work: %v", err)
	}
	due, err := refreshes.ListPendingMappingProjectionRefreshes(ctx, now, 10)
	if err != nil || len(due) != 1 || due[0].ReceiptID != "new-work" {
		t.Fatalf("due work behind backed-off job = %+v err=%v", due, err)
	}
	for attempt := 1; attempt < 3; attempt++ {
		if err := refreshes.RecordMappingProjectionRefreshFailure(ctx, "poison", "projection_refresh_failed", attempt, now.Add(time.Duration(attempt+1)*time.Minute), 3); err != nil {
			t.Fatalf("record poison failure %d: %v", attempt+1, err)
		}
	}
	abandoned, err := refreshes.GetMappingProjectionRefresh(ctx, "poison")
	if err != nil || abandoned.State != MappingProjectionRefreshAbandoned || abandoned.Attempts != 3 {
		t.Fatalf("abandoned poison job = %+v err=%v", abandoned, err)
	}
	requeued, changed, err := refreshes.RequeueAbandonedMappingProjectionRefresh(ctx, "poison", "tenant-1")
	if err != nil || !changed || requeued.State != MappingProjectionRefreshPending || requeued.Attempts != 0 || requeued.LastErrorCode != "" {
		t.Fatalf("requeued poison job = %+v err=%v", requeued, err)
	}
	idempotent, changed, err := refreshes.RequeueAbandonedMappingProjectionRefresh(ctx, "poison", "tenant-1")
	if err != nil || changed || idempotent.State != MappingProjectionRefreshPending || idempotent.Attempts != 0 {
		t.Fatalf("idempotent requeue = %+v err=%v", idempotent, err)
	}
	if _, _, err := refreshes.RequeueAbandonedMappingProjectionRefresh(ctx, "poison", "tenant-2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant requeue error = %v, want ErrNotFound", err)
	}
	listed, err := refreshes.ListMappingProjectionRefreshes(ctx, []string{"poison", "new-work", "poison"})
	if err != nil || len(listed) != 2 || listed["poison"].TenantID != "tenant-1" || listed["new-work"].TenantID != "tenant-2" {
		t.Fatalf("listed refreshes = %+v err=%v", listed, err)
	}
}

func TestMetadataPromotionRequiresMappingProjectionRefresh(t *testing.T) {
	if MetadataPromotionRequiresMappingProjectionRefresh([]MetadataProposalPromotionChange{{
		Operation: PromotionOperationNoop, TargetKind: "mapping_rule", AppliedVersion: 2,
	}}) {
		t.Fatal("noop change requires refresh")
	}
	if !MetadataPromotionRequiresMappingProjectionRefresh([]MetadataProposalPromotionChange{{
		Operation: PromotionOperationUpdate, TargetKind: "mapping_rule", AppliedVersion: 2,
	}}) {
		t.Fatal("applied mapping rule change does not require refresh")
	}
}

func TestPlatformMappingProjectionRefreshRetryIsPersistentlyIdempotent(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "mapping-projection-refresh-command"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{
		ontologyMappingProjectionRefreshMigration(),
		ontologyMappingProjectionRefreshRetryMigration(),
		ontologyMappingProjectionRefreshCommandMigration(),
	}).Up(ctx, store); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	refreshes := NewPlatformMappingProjectionRefreshStore(store)
	now := time.Unix(300, 0).UTC()
	refreshes.now = func() time.Time { return now }
	if err := refreshes.EnqueueMappingProjectionRefresh(ctx, "receipt-1", "tenant-1"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		if err := refreshes.RecordMappingProjectionRefreshFailure(ctx, "receipt-1", "projection_refresh_failed", attempt, now.Add(time.Minute), 3); err != nil {
			t.Fatalf("record failure %d: %v", attempt+1, err)
		}
	}

	first, err := refreshes.ExecuteMappingProjectionRefreshRetry(ctx, "tenant-1", "receipt-1", "retry-key")
	if err != nil || first.Status != MappingProjectionRefreshPending || !first.Requeued || first.IdempotentReplay {
		t.Fatalf("first retry = %+v, err=%v", first, err)
	}
	if err := refreshes.CompleteMappingProjectionRefresh(ctx, "receipt-1"); err != nil {
		t.Fatalf("complete refresh: %v", err)
	}
	replayed, err := refreshes.ExecuteMappingProjectionRefreshRetry(ctx, "tenant-1", "receipt-1", "retry-key")
	if err != nil || replayed.Status != MappingProjectionRefreshPending || replayed.Requeued || !replayed.IdempotentReplay {
		t.Fatalf("replayed retry = %+v, err=%v", replayed, err)
	}
	if err := refreshes.EnqueueMappingProjectionRefresh(ctx, "receipt-2", "tenant-1"); err != nil {
		t.Fatalf("enqueue second receipt: %v", err)
	}
	if _, err := refreshes.ExecuteMappingProjectionRefreshRetry(ctx, "tenant-1", "receipt-2", "retry-key"); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("reused idempotency key error = %v, want ErrVersionConflict", err)
	}
}
