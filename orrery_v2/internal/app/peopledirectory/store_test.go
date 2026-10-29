package peopledirectory

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/platform/migration"
	"orrery_v2/internal/platform/testkit"
)

func TestStorePersistsReviewableMatchesOnlyForCompleteSnapshot(t *testing.T) {
	ctx := context.Background()
	database := testkit.NewTestDB(t)
	if err := migration.NewRunner([]migration.Migration{directoryMigration()}).Up(ctx, database); err != nil {
		t.Fatalf("apply directory migration: %v", err)
	}
	store := NewStore(database)
	if err := store.BeginSnapshot(ctx, Snapshot{ID: "snapshot-1", Source: SourceFeishu, TenantID: "tenant-1"}); err != nil {
		t.Fatalf("BeginSnapshot: %v", err)
	}
	if err := store.StagePage(ctx, "snapshot-1", []DirectoryUser{{SourceRecordID: "ou_1", Active: true}}, true); err != nil {
		t.Fatalf("StagePage partial: %v", err)
	}
	result := MatchResult{SourceRecordID: "ou_1", Action: MatchActionConflict, MatchKind: MatchByConflict, ConflictReason: "employee number is ambiguous", ApplyStatus: ApplyStatusPendingReview}
	if err := store.SaveMatchResults(ctx, "snapshot-1", []MatchResult{result}); !errors.Is(err, ErrSnapshotIncomplete) {
		t.Fatalf("partial SaveMatchResults error = %v", err)
	}
	if err := store.StagePage(ctx, "snapshot-1", nil, false); err != nil {
		t.Fatalf("StagePage complete: %v", err)
	}
	if err := store.SaveMatchResults(ctx, "snapshot-1", []MatchResult{result}); err != nil {
		t.Fatalf("SaveMatchResults: %v", err)
	}
	matches, err := store.MatchResults(ctx, "snapshot-1")
	if err != nil {
		t.Fatalf("MatchResults: %v", err)
	}
	if len(matches) != 1 || matches[0].ConflictReason != result.ConflictReason || matches[0].ApplyStatus != ApplyStatusPendingReview {
		t.Fatalf("matches = %+v", matches)
	}
}

func TestStoreReservesStablePersonIDBeforeMarkingCreateApplied(t *testing.T) {
	ctx := context.Background()
	database := testkit.NewTestDB(t)
	if err := migration.NewRunner([]migration.Migration{directoryMigration()}).Up(ctx, database); err != nil {
		t.Fatalf("apply directory migration: %v", err)
	}
	store := NewStore(database)
	if err := store.BeginSnapshot(ctx, Snapshot{ID: "snapshot-create", Source: SourceFeishu, TenantID: "tenant-1"}); err != nil {
		t.Fatalf("BeginSnapshot: %v", err)
	}
	if err := store.StagePage(ctx, "snapshot-create", []DirectoryUser{{SourceRecordID: "u_new", Active: true}}, false); err != nil {
		t.Fatalf("StagePage: %v", err)
	}
	if err := store.SaveMatchResults(ctx, "snapshot-create", []MatchResult{{SourceRecordID: "u_new", Action: MatchActionCreate, MatchKind: MatchByNewPerson}}); err != nil {
		t.Fatalf("SaveMatchResults: %v", err)
	}
	if err := store.ReservePersonID(ctx, "snapshot-create", "u_new", "human-generated"); err != nil {
		t.Fatalf("ReservePersonID: %v", err)
	}
	if err := store.ReservePersonID(ctx, "snapshot-create", "u_new", "human-other"); !errors.Is(err, ErrMatchConflict) {
		t.Fatalf("conflicting ReservePersonID error = %v", err)
	}
	if err := store.MarkApplied(ctx, "snapshot-create", "u_new", "human-generated"); err != nil {
		t.Fatalf("MarkApplied: %v", err)
	}
	matches, err := store.MatchResults(ctx, "snapshot-create")
	if err != nil {
		t.Fatalf("MatchResults: %v", err)
	}
	if len(matches) != 1 || matches[0].ProposedPersonID != "human-generated" || matches[0].ApplyStatus != ApplyStatusApplied {
		t.Fatalf("matches = %+v", matches)
	}
}
