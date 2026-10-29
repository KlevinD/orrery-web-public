package datapolicy

import (
	"context"
	"os"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestPostgresPlatformStorePersistsDataPolicies(t *testing.T) {
	rawURL := os.Getenv("ORRERY_TEST_POSTGRES_URL")
	if rawURL == "" {
		t.Skip("ORRERY_TEST_POSTGRES_URL is not set")
	}
	ctx := context.Background()
	sqlStore, err := db.Open(ctx, db.Config{Driver: db.DriverPostgres, URL: rawURL, Name: "datapolicy-postgres"})
	if err != nil {
		t.Fatalf("open postgres store: %v", err)
	}
	defer sqlStore.Close()
	for _, table := range []string{"field_policies", "purpose_policies", "data_policy_revision", "schema_migrations"} {
		if _, err := sqlStore.Exec(ctx, `DROP TABLE IF EXISTS `+table+` CASCADE`); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
	if err := migration.NewRunner([]migration.Migration{dataPolicyMigration(), dataPolicyRevisionMigration()}).Up(ctx, sqlStore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	now := time.Unix(700, 0).UTC()
	store := NewPlatformStore(sqlStore)
	if err := store.SaveFieldPolicy(ctx, FieldPolicy{
		ID:         "field-mask-pg",
		TenantID:   "tenant-pg",
		Consumer:   "iris",
		Purpose:    "mission_assignment",
		ObjectType: "mission",
		FieldPath:  "secret_notes",
		Effect:     EffectMask,
		Enabled:    true,
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("SaveFieldPolicy: %v", err)
	}
	decision, err := store.DecideContext(ctx, contextgraph.BuildContextBundleRequest{
		TenantID: "tenant-pg",
		Consumer: "iris",
		Purpose:  "mission_assignment",
	}, []contextgraph.Object{{ID: "mission-pg", ObjectType: "mission"}})
	if err != nil {
		t.Fatalf("DecideContext: %v", err)
	}
	if got := decision.MaskedFields["mission-pg"]; len(got) != 1 || got[0] != "secret_notes" {
		t.Fatalf("masked fields = %+v", decision.MaskedFields)
	}
}
