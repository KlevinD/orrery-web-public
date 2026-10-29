package contextgraph

import (
	"context"
	"os"
	"testing"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestPostgresPlatformStorePersistsProjectionResult(t *testing.T) {
	rawURL := os.Getenv("ORRERY_TEST_POSTGRES_URL")
	if rawURL == "" {
		t.Skip("ORRERY_TEST_POSTGRES_URL is not set")
	}
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{
		Driver: db.DriverPostgres,
		URL:    rawURL,
		Name:   "contextgraph-postgres",
	})
	if err != nil {
		t.Fatalf("open postgres store: %v", err)
	}
	defer store.Close()
	for _, table := range []string{
		"context_graph_links",
		"context_graph_facts",
		"context_graph_objects",
		"context_graph_evidence",
		"schema_migrations",
	} {
		if _, err := store.Exec(ctx, `DROP TABLE IF EXISTS `+table+` CASCADE`); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
	if err := migration.NewRunner(contextGraphStoreTestMigrations()).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	graphStore := NewPlatformStore(store)
	if err := ApplyProjectionResult(ctx, graphStore, platformStoreProjectionResult("tenant-pg")); err != nil {
		t.Fatalf("ApplyProjectionResult: %v", err)
	}
	objects, links, facts, evidence, err := graphStore.Snapshot(ctx, "tenant-pg")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(objects) != 2 || len(links) != 1 || len(facts) != 1 || len(evidence) != 1 {
		t.Fatalf("postgres snapshot objects=%d links=%d facts=%d evidence=%d", len(objects), len(links), len(facts), len(evidence))
	}
}
