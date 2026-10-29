package ontology

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestPlatformStateRefreshManifestStoreClaimsAndCompletesOnce(t *testing.T) {
	ctx := context.Background()
	store := NewPlatformStateRefreshManifestStore(openStateProjectionTestStore(t, ctx))
	startedAt := time.Date(2026, 8, 21, 1, 0, 0, 0, time.UTC)
	manifest := StateRefreshManifest{
		RefreshKind: "daily_state", Fingerprint: "fingerprint-1", SnapshotDate: "2026-08-21",
		MappingRevision: 7, SourceCount: 12, StartedAt: startedAt,
	}

	claimed, err := store.Claim(ctx, manifest)
	if err != nil || !claimed {
		t.Fatalf("first Claim = %t, %v", claimed, err)
	}
	claimed, err = store.Claim(ctx, manifest)
	if err != nil || claimed {
		t.Fatalf("second running Claim = %t, %v", claimed, err)
	}
	running, found, err := store.Get(ctx, manifest.RefreshKind, manifest.Fingerprint)
	if err != nil || !found {
		t.Fatalf("Get running found=%t err=%v", found, err)
	}
	if running.Status != StateRefreshRunning || !running.StartedAt.Equal(startedAt) || running.SourceCount != 12 {
		t.Fatalf("running manifest = %+v", running)
	}

	completedAt := startedAt.Add(time.Minute)
	manifest.ChangedCount = 3
	manifest.SnapshotOnlyCount = 2
	manifest.NoopCount = 6
	manifest.StaleCount = 1
	manifest.CompletedAt = completedAt
	if err := store.Complete(ctx, manifest); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	completed, found, err := store.Get(ctx, manifest.RefreshKind, manifest.Fingerprint)
	if err != nil || !found {
		t.Fatalf("Get completed found=%t err=%v", found, err)
	}
	if completed.Status != StateRefreshSucceeded || !completed.CompletedAt.Equal(completedAt) ||
		completed.ChangedCount != 3 || completed.SnapshotOnlyCount != 2 || completed.NoopCount != 6 || completed.StaleCount != 1 {
		t.Fatalf("completed manifest = %+v", completed)
	}
	claimed, err = store.Claim(ctx, manifest)
	if err != nil || claimed {
		t.Fatalf("completed Claim = %t, %v", claimed, err)
	}
}

func TestPlatformStateRefreshManifestStoreRetriesFailureAndBoundsError(t *testing.T) {
	ctx := context.Background()
	store := NewPlatformStateRefreshManifestStore(openStateProjectionTestStore(t, ctx))
	startedAt := time.Date(2026, 8, 21, 1, 0, 0, 0, time.UTC)
	manifest := StateRefreshManifest{
		RefreshKind: "startup_backfill", Fingerprint: "fingerprint-failed", SnapshotDate: "2026-08-21",
		MappingRevision: 9, SourceCount: 8, StartedAt: startedAt,
	}
	claimed, err := store.Claim(ctx, manifest)
	if err != nil || !claimed {
		t.Fatalf("Claim = %t, %v", claimed, err)
	}
	manifest.CompletedAt = startedAt.Add(time.Minute)
	manifest.LastError = strings.Repeat("界", 5000)
	if err := store.Fail(ctx, manifest); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	failed, found, err := store.Get(ctx, manifest.RefreshKind, manifest.Fingerprint)
	if err != nil || !found {
		t.Fatalf("Get failed found=%t err=%v", found, err)
	}
	if failed.Status != StateRefreshFailed || utf8.RuneCountInString(failed.LastError) != 4000 {
		t.Fatalf("failed manifest status=%s error_runes=%d", failed.Status, utf8.RuneCountInString(failed.LastError))
	}

	manifest.StartedAt = startedAt.Add(2 * time.Minute)
	manifest.CompletedAt = time.Time{}
	manifest.LastError = ""
	claimed, err = store.Claim(ctx, manifest)
	if err != nil || !claimed {
		t.Fatalf("retry Claim = %t, %v", claimed, err)
	}
	retried, found, err := store.Get(ctx, manifest.RefreshKind, manifest.Fingerprint)
	if err != nil || !found {
		t.Fatalf("Get retried found=%t err=%v", found, err)
	}
	if retried.Status != StateRefreshRunning || !retried.StartedAt.Equal(manifest.StartedAt) || !retried.CompletedAt.IsZero() || retried.LastError != "" {
		t.Fatalf("retried manifest = %+v", retried)
	}
}
