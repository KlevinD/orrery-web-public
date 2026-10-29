package ontologyscope

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestPlatformBindingStorePreparesAndRestoresSourceDeletionAtomically(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "scope-source-deletion"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer database.Close()
	if err := migration.NewRunner([]migration.Migration{scopeBindingMigration(), scopeBindingSourceDeletionMigration()}).Up(ctx, database); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := NewPlatformBindingStore(database)
	now := time.Unix(100, 0).UTC()
	store.now = func() time.Time { return now }
	bindings := []ScopeBinding{
		testSourceDeletionBinding("tenant-a"),
		testSourceDeletionBinding("tenant-b"),
	}
	for index := range bindings {
		bindings[index], err = store.SaveBinding(ctx, bindings[index], 0, "test:seed")
		if err != nil {
			t.Fatalf("seed binding %d: %v", index, err)
		}
	}

	_, err = store.PrepareSourceDeletion(ctx, identity.ObjectRef{Type: "mission", ID: "mission-1"}, []string{"mission:mission-1"}, []SourceDeletionBinding{
		{Binding: bindings[0], ExpectedVersion: 1},
		{Binding: bindings[1], ExpectedVersion: 99},
	}, "test:delete-conflict")
	if !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("prepare conflict error = %v", err)
	}
	for _, binding := range bindings {
		current, getErr := store.GetBinding(ctx, binding.TenantID)
		if getErr != nil || current.State != BindingStateActive || current.BindingVersion != 1 {
			t.Fatalf("binding after rolled back prepare = %+v, err=%v", current, getErr)
		}
	}

	intent, err := store.PrepareSourceDeletion(ctx, identity.ObjectRef{Type: "mission", ID: "mission-1"}, []string{"mission:mission-1"}, []SourceDeletionBinding{
		{Binding: bindings[0], ExpectedVersion: 1},
		{Binding: bindings[1], ExpectedVersion: 1},
	}, "test:delete")
	if err != nil {
		t.Fatalf("prepare source deletion: %v", err)
	}
	if len(intent.Bindings) != 2 || !intent.RecoverAfter.Equal(now.Add(sourceDeletionRecoveryDelay)) {
		t.Fatalf("intent = %+v", intent)
	}
	for _, binding := range bindings {
		current, getErr := store.GetBinding(ctx, binding.TenantID)
		if getErr != nil || current.State != BindingStateDisabled || current.BindingVersion != 2 {
			t.Fatalf("disabled binding = %+v, err=%v", current, getErr)
		}
	}
	if intents, listErr := store.ListRecoverableSourceDeletions(ctx, now, 10); listErr != nil || len(intents) != 0 {
		t.Fatalf("premature recoverable intents = %+v, err=%v", intents, listErr)
	}
	if intents, listErr := store.ListRecoverableSourceDeletions(ctx, now.Add(sourceDeletionRecoveryDelay), 10); listErr != nil || len(intents) != 1 || intents[0].IntentID != intent.IntentID {
		t.Fatalf("recoverable intents = %+v, err=%v", intents, listErr)
	}
	if err := store.RestoreSourceDeletion(ctx, intent, "test:rollback"); err != nil {
		t.Fatalf("restore source deletion: %v", err)
	}
	for _, binding := range bindings {
		current, getErr := store.GetBinding(ctx, binding.TenantID)
		if getErr != nil || current.State != BindingStateActive || current.BindingVersion != 3 {
			t.Fatalf("restored binding = %+v, err=%v", current, getErr)
		}
	}
	if intents, listErr := store.ListRecoverableSourceDeletions(ctx, now.Add(time.Hour), 10); listErr != nil || len(intents) != 0 {
		t.Fatalf("intent remained after restore = %+v, err=%v", intents, listErr)
	}
}

func testSourceDeletionBinding(tenantID string) ScopeBinding {
	return ScopeBinding{
		TenantID: tenantID, Kind: KindCustom,
		SourceObject:          identity.ObjectRef{Type: "mission", ID: "mission-1"},
		AuthorizationTenantID: "global", ReadAction: "mission.view", ManageAction: "mission.manage",
		BindingVersion: 1, State: BindingStateActive,
	}
}
