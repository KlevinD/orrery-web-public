package rebac

import (
	"context"
	"os"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestPostgresPlatformStorePersistsRelationTuple(t *testing.T) {
	rawURL := os.Getenv("ORRERY_TEST_POSTGRES_URL")
	if rawURL == "" {
		t.Skip("ORRERY_TEST_POSTGRES_URL is not set")
	}
	ctx := context.Background()
	sqlStore, err := db.Open(ctx, db.Config{
		Driver: db.DriverPostgres,
		URL:    rawURL,
		Name:   "rebac-postgres",
	})
	if err != nil {
		t.Fatalf("open postgres store: %v", err)
	}
	defer sqlStore.Close()
	for _, table := range []string{"rebac_relation_tuples", "rebac_authorization_revision", "schema_migrations"} {
		if _, err := sqlStore.Exec(ctx, `DROP TABLE IF EXISTS `+table+` CASCADE`); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
	if err := migration.NewRunner([]migration.Migration{relationTupleMigration(), authorizationRevisionMigration()}).Up(ctx, sqlStore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	now := time.Unix(400, 0).UTC()
	store := NewPlatformStore(sqlStore)
	if err := store.SaveRelationTuple(ctx, RelationTuple{
		ID:        "rel-pg-1",
		TenantID:  "tenant-pg",
		Subject:   Ref{Kind: "service", ID: "iris"},
		Relation:  "committer",
		Object:    Ref{Kind: "mission", ID: "mission-pg"},
		Source:    Ref{Kind: "action_type", ID: "iris.accept_recommendation"},
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("SaveRelationTuple: %v", err)
	}
	allowed, matched, err := store.CheckRelation(ctx, RelationCheck{
		TenantID: "tenant-pg",
		Subject:  Ref{Kind: "service", ID: "iris"},
		Relation: "committer",
		Object:   Ref{Kind: "mission", ID: "mission-pg"},
		At:       now,
	})
	if err != nil {
		t.Fatalf("CheckRelation: %v", err)
	}
	if !allowed || matched.Source.ID != "iris.accept_recommendation" {
		t.Fatalf("allowed=%v matched=%#v", allowed, matched)
	}
}
