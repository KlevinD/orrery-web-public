package peopledirectory

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
	"orrery_v2/internal/platform/testkit"
)

func TestPruneSnapshotsPreservesProtectedHistoryAndDeletesSupersededRows(t *testing.T) {
	ctx := context.Background()
	database := testkit.NewTestDB(t)
	if err := migration.NewRunner([]migration.Migration{directoryMigration()}).Up(ctx, database); err != nil {
		t.Fatalf("apply directory migration: %v", err)
	}
	store := NewStore(database)
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	seedDirectorySnapshot(t, ctx, store, database, "old-pending", SnapshotStatusComplete, ApplyStatusPending, now.Add(-72*time.Hour))
	seedDirectorySnapshot(t, ctx, store, database, "old-failed", SnapshotStatusFailed, "", now.Add(-72*time.Hour))
	seedDirectorySnapshot(t, ctx, store, database, "old-scanning", SnapshotStatusScanning, "", now.Add(-72*time.Hour))
	seedDirectorySnapshot(t, ctx, store, database, "old-review", SnapshotStatusComplete, ApplyStatusPendingReview, now.Add(-70*time.Hour))
	seedDirectorySnapshot(t, ctx, store, database, "old-applied", SnapshotStatusComplete, ApplyStatusApplied, now.Add(-69*time.Hour))
	seedDirectorySnapshot(t, ctx, store, database, "recent", SnapshotStatusComplete, ApplyStatusPending, now.Add(-time.Hour))
	seedDirectorySnapshot(t, ctx, store, database, "newest-2", SnapshotStatusComplete, ApplyStatusPending, now.Add(-30*time.Minute))
	seedDirectorySnapshot(t, ctx, store, database, "newest-1", SnapshotStatusComplete, ApplyStatusPending, now.Add(-10*time.Minute))

	latest, found, err := store.LatestSnapshotStartedAt(ctx, SourceFeishu, "tenant-1")
	if err != nil || !found || !latest.Equal(now.Add(-10*time.Minute)) {
		t.Fatalf("latest snapshot = %s found=%t err=%v", latest, found, err)
	}
	deleted, err := store.PruneSnapshots(ctx, now, SnapshotRetentionPolicy{KeepLatest: 2, MinimumAge: 24 * time.Hour, Limit: 100})
	if err != nil || deleted != 3 {
		t.Fatalf("PruneSnapshots deleted=%d err=%v, want 3", deleted, err)
	}
	for _, id := range []string{"old-pending", "old-failed", "old-scanning"} {
		if _, err := store.Snapshot(ctx, id); !errors.Is(err, ErrSnapshotNotFound) {
			t.Fatalf("Snapshot(%s) error=%v, want ErrSnapshotNotFound", id, err)
		}
		assertDirectorySnapshotChildren(t, ctx, database, id, 0, 0)
	}
	for _, id := range []string{"old-review", "old-applied", "recent", "newest-2", "newest-1"} {
		if _, err := store.Snapshot(ctx, id); err != nil {
			t.Fatalf("protected Snapshot(%s): %v", id, err)
		}
	}
}

func TestPruneSnapshotsStopsAtLimit(t *testing.T) {
	ctx := context.Background()
	database := testkit.NewTestDB(t)
	if err := migration.NewRunner([]migration.Migration{directoryMigration()}).Up(ctx, database); err != nil {
		t.Fatalf("apply directory migration: %v", err)
	}
	store := NewStore(database)
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	for index := 0; index < 103; index++ {
		seedDirectorySnapshot(t, ctx, store, database, fmt.Sprintf("snapshot-%03d", index), SnapshotStatusComplete, ApplyStatusPending, now.Add(time.Duration(index-200)*time.Hour))
	}

	deleted, err := store.PruneSnapshots(ctx, now, SnapshotRetentionPolicy{KeepLatest: 2, MinimumAge: 24 * time.Hour, Limit: 100})
	if err != nil || deleted != 100 {
		t.Fatalf("PruneSnapshots deleted=%d err=%v, want 100", deleted, err)
	}
	var remaining int
	if err := database.QueryRow(ctx, `SELECT COUNT(*) FROM people_directory_snapshots`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 3 {
		t.Fatalf("remaining snapshots=%d, want 3", remaining)
	}
}

func seedDirectorySnapshot(t *testing.T, ctx context.Context, store *Store, database *db.Store, id, status, applyStatus string, createdAt time.Time) {
	t.Helper()
	if err := store.BeginSnapshot(ctx, Snapshot{ID: id, Source: SourceFeishu, TenantID: "tenant-1"}); err != nil {
		t.Fatalf("BeginSnapshot(%s): %v", id, err)
	}
	if status == SnapshotStatusComplete {
		if err := store.StagePage(ctx, id, []DirectoryUser{{SourceRecordID: "user-" + id, Active: true}}, false); err != nil {
			t.Fatalf("StagePage(%s): %v", id, err)
		}
		if err := store.SaveMatchResults(ctx, id, []MatchResult{{SourceRecordID: "user-" + id, Action: MatchActionUpdate, MatchKind: MatchByEmployeeNumber, ApplyStatus: applyStatus}}); err != nil {
			t.Fatalf("SaveMatchResults(%s): %v", id, err)
		}
	} else if status == SnapshotStatusFailed {
		if err := store.MarkSnapshotFailed(ctx, id, errors.New("scan failed")); err != nil {
			t.Fatalf("MarkSnapshotFailed(%s): %v", id, err)
		}
	}
	if _, err := database.Exec(ctx, `UPDATE people_directory_snapshots SET created_at = ?, updated_at = ? WHERE id = ?`, createdAt.UnixMilli(), createdAt.UnixMilli(), id); err != nil {
		t.Fatalf("set snapshot time(%s): %v", id, err)
	}
}

func assertDirectorySnapshotChildren(t *testing.T, ctx context.Context, database *db.Store, snapshotID string, wantUsers, wantMatches int) {
	t.Helper()
	var users, matches int
	if err := database.QueryRow(ctx, `SELECT COUNT(*) FROM people_directory_staged_users WHERE snapshot_id = ?`, snapshotID).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(ctx, `SELECT COUNT(*) FROM people_directory_matches WHERE snapshot_id = ?`, snapshotID).Scan(&matches); err != nil {
		t.Fatal(err)
	}
	if users != wantUsers || matches != wantMatches {
		t.Fatalf("snapshot %s children users=%d matches=%d, want %d/%d", snapshotID, users, matches, wantUsers, wantMatches)
	}
}
