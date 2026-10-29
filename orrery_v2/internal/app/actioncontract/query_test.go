package actioncontract

import (
	"context"
	"testing"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestActionTypeQueryUsesBoundedTenantAwareKeysetPages(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{actionContractMigration(), actionTypeSemanticsMigration(), actionRunExecutionModeMigration()}).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	repository := NewPlatformRepository(store)
	for _, item := range []ActionType{
		{ID: "a-global", Name: "Global", Category: CategorySystem, ProviderKind: ProviderInternalService, Enabled: true},
		{ID: "b-one", TenantID: "tenant-1", Name: "One", Category: CategorySystem, ProviderKind: ProviderInternalService, Enabled: true},
		{ID: "c-two", TenantID: "tenant-2", Name: "Two", Category: CategorySystem, ProviderKind: ProviderInternalService, Enabled: true},
		{ID: "d-one", TenantID: "tenant-1", Name: "One D", Category: CategorySystem, ProviderKind: ProviderInternalService, Enabled: true},
	} {
		if err := repository.SaveActionType(ctx, item); err != nil {
			t.Fatalf("SaveActionType(%s): %v", item.ID, err)
		}
	}
	first, err := repository.ListActionTypePage(ctx, "tenant-1", "", 2)
	if err != nil || !first.HasMore || len(first.Items) != 2 || first.Items[1].ID != "b-one" {
		t.Fatalf("first page = %+v, err=%v", first, err)
	}
	second, err := repository.ListActionTypePage(ctx, "tenant-1", "b-one", 2)
	if err != nil || second.HasMore || len(second.Items) != 1 || second.Items[0].ID != "d-one" {
		t.Fatalf("second page = %+v, err=%v", second, err)
	}
}
