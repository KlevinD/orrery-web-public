package contextgraph

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestPlatformStorePersistsAndQueriesTemporalHistory(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner(contextGraphStoreTestMigrations()).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	graphStore := NewPlatformStore(store)
	firstStart := temporalTestTime(2026, time.January, 1)
	firstRecorded := temporalTestTime(2026, time.January, 2)
	secondStart := temporalTestTime(2026, time.April, 1)
	secondRecorded := temporalTestTime(2026, time.March, 1)
	for _, version := range []Object{
		temporalVersionTestObject("Original", 1, firstStart, nil, firstRecorded),
		temporalVersionTestObject("Future structure", 2, secondStart, nil, secondRecorded),
	} {
		if err := graphStore.SaveObject(ctx, version); err != nil {
			t.Fatalf("SaveObject(v%d): %v", version.SourceVersion, err)
		}
	}

	reopened := NewPlatformStore(store)
	current, _, _, _, err := reopened.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(current) != 1 || current[0].Label != "Future structure" ||
		!current[0].Temporal.BusinessValidFrom.Equal(secondStart) {
		t.Fatalf("current temporal object = %+v", current)
	}
	assertTemporalObjectLabel(t, reopened, temporalTestTime(2026, time.February, 1), temporalTestTime(2026, time.May, 1), "Original")
	assertTemporalObjectLabel(t, reopened, temporalTestTime(2026, time.May, 1), temporalTestTime(2026, time.May, 1), "Future structure")

	var versions int
	if err := store.QueryRow(ctx, "SELECT COUNT(*) FROM context_graph_resource_versions WHERE tenant_id = ? AND resource_kind = ? AND resource_id = ?",
		"tenant-1", temporalResourceObject, "org-1",
	).Scan(&versions); err != nil {
		t.Fatalf("count temporal versions: %v", err)
	}
	if versions != 2 {
		t.Fatalf("temporal history rows = %d, want 2", versions)
	}
}

func TestPlatformStoreKeepsStaticResourcesOutOfTemporalHistory(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner(contextGraphStoreTestMigrations()).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	graphStore := NewPlatformStore(store)
	updatedAt := temporalTestTime(2026, time.January, 2)
	for _, object := range []Object{
		{ID: "object-1", TenantID: "tenant-1", ObjectType: "task", CanonicalKey: "task:object-1", UpdatedAt: updatedAt},
		{ID: "object-2", TenantID: "tenant-1", ObjectType: "task", CanonicalKey: "task:object-2", UpdatedAt: updatedAt},
	} {
		if err := graphStore.SaveObject(ctx, object); err != nil {
			t.Fatalf("SaveObject(%s): %v", object.ID, err)
		}
	}
	if err := graphStore.SaveLink(ctx, Link{
		ID: "link-1", TenantID: "tenant-1", LinkType: "depends_on", FromObjectID: "object-1", ToObjectID: "object-2", UpdatedAt: updatedAt,
	}); err != nil {
		t.Fatalf("SaveLink: %v", err)
	}
	if err := graphStore.SaveFact(ctx, Fact{
		ID: "fact-1", TenantID: "tenant-1", Subject: Ref{Kind: "task", ID: "object-1"}, Predicate: "status", Value: "active",
		Provenance: Ref{Kind: "event", ID: "event-1"}, SourceStreamID: "task:object-1", SourceVersion: 1, UpdatedAt: updatedAt,
	}); err != nil {
		t.Fatalf("SaveFact: %v", err)
	}
	var versions int
	if err := store.QueryRow(ctx, "SELECT COUNT(*) FROM context_graph_resource_versions").Scan(&versions); err != nil {
		t.Fatalf("count temporal versions: %v", err)
	}
	if versions != 0 {
		t.Fatalf("static temporal history rows = %d, want 0", versions)
	}
	objects, links, facts, _, err := graphStore.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(objects) != 2 || len(links) != 1 || len(facts) != 1 {
		t.Fatalf("current snapshot objects=%d links=%d facts=%d", len(objects), len(links), len(facts))
	}
}

func TestPlatformStoreKeepsAllNonStaticResourceModesInHistory(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner(contextGraphStoreTestMigrations()).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	graphStore := NewPlatformStore(store)
	updatedAt := temporalTestTime(2026, time.January, 2)
	for _, object := range []Object{
		{ID: "object-1", TenantID: "tenant-1", ObjectType: "task", CanonicalKey: "task:object-1", UpdatedAt: updatedAt},
		{ID: "object-2", TenantID: "tenant-1", ObjectType: "task", CanonicalKey: "task:object-2", UpdatedAt: updatedAt},
	} {
		if err := graphStore.SaveObject(ctx, object); err != nil {
			t.Fatalf("SaveObject reference %s: %v", object.ID, err)
		}
	}
	if err := graphStore.SaveObject(ctx, Object{
		ID: "temporal-object", TenantID: "tenant-1", ObjectType: "task", CanonicalKey: "task:temporal-object", SourceStreamID: "object-stream", SourceVersion: 1,
		Temporal: TemporalMetadata{Mode: "state_interval", BusinessValidFrom: updatedAt, SourceRecordedAt: updatedAt}, UpdatedAt: updatedAt,
	}); err != nil {
		t.Fatalf("SaveObject temporal: %v", err)
	}
	if err := graphStore.SaveLink(ctx, Link{
		ID: "temporal-link", TenantID: "tenant-1", LinkType: "depends_on", FromObjectID: "object-1", ToObjectID: "object-2",
		SourceStreamID: "link-stream", SourceVersion: 1, Temporal: TemporalMetadata{Mode: "event", SourceRecordedAt: updatedAt}, UpdatedAt: updatedAt,
	}); err != nil {
		t.Fatalf("SaveLink temporal: %v", err)
	}
	if err := graphStore.SaveFact(ctx, Fact{
		ID: "temporal-fact", TenantID: "tenant-1", Subject: Ref{Kind: "task", ID: "object-1"}, Predicate: "progress", Value: 0.5,
		Provenance: Ref{Kind: "event", ID: "event-1"}, SourceStreamID: "fact-stream", SourceVersion: 1,
		Temporal: TemporalMetadata{Mode: "measurement", SourceRecordedAt: updatedAt}, UpdatedAt: updatedAt,
	}); err != nil {
		t.Fatalf("SaveFact temporal: %v", err)
	}
	var versions int
	if err := store.QueryRow(ctx, "SELECT COUNT(*) FROM context_graph_resource_versions").Scan(&versions); err != nil {
		t.Fatalf("count temporal versions: %v", err)
	}
	if versions != 3 {
		t.Fatalf("non-static temporal history rows = %d, want 3", versions)
	}
}

func TestPlatformStoreDeduplicatesStableTemporalReplay(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner(contextGraphStoreTestMigrations()).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	graphStore := NewPlatformStore(store)
	resource := temporalVersionTestObject(
		"Original", 1, temporalTestTime(2026, time.January, 1), nil, temporalTestTime(2026, time.January, 2),
	)
	resource.Temporal.OrreryIngestedAt = temporalTestTime(2026, time.January, 3)
	if err := graphStore.SaveObject(ctx, resource); err != nil {
		t.Fatalf("SaveObject first: %v", err)
	}
	resource.Temporal.OrreryIngestedAt = temporalTestTime(2026, time.January, 4)
	if err := graphStore.SaveObject(ctx, resource); err != nil {
		t.Fatalf("SaveObject replay: %v", err)
	}
	var versions int
	if err := store.QueryRow(ctx, "SELECT COUNT(*) FROM context_graph_resource_versions").Scan(&versions); err != nil {
		t.Fatalf("count replayed versions: %v", err)
	}
	if versions != 1 {
		t.Fatalf("replayed temporal history rows = %d, want 1", versions)
	}
	resource.Label = "Corrected"
	if err := graphStore.SaveObject(ctx, resource); err != nil {
		t.Fatalf("SaveObject semantic change: %v", err)
	}
	if err := store.QueryRow(ctx, "SELECT COUNT(*) FROM context_graph_resource_versions").Scan(&versions); err != nil {
		t.Fatalf("count semantic versions: %v", err)
	}
	if versions != 2 {
		t.Fatalf("semantic temporal history rows = %d, want 2", versions)
	}
}

func TestPlatformStoreKeepsFullIdentityForUnversionedTemporalResources(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner(contextGraphStoreTestMigrations()).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	graphStore := NewPlatformStore(store)
	resource := temporalVersionTestObject(
		"Unversioned", 0, temporalTestTime(2026, time.January, 1), nil, temporalTestTime(2026, time.January, 2),
	)
	resource.Temporal.OrreryIngestedAt = temporalTestTime(2026, time.January, 3)
	if err := graphStore.SaveObject(ctx, resource); err != nil {
		t.Fatalf("SaveObject first: %v", err)
	}
	resource.Temporal.OrreryIngestedAt = temporalTestTime(2026, time.January, 4)
	if err := graphStore.SaveObject(ctx, resource); err != nil {
		t.Fatalf("SaveObject replay: %v", err)
	}
	var versions int
	if err := store.QueryRow(ctx, "SELECT COUNT(*) FROM context_graph_resource_versions").Scan(&versions); err != nil {
		t.Fatalf("count unversioned versions: %v", err)
	}
	if versions != 2 {
		t.Fatalf("unversioned temporal history rows = %d, want 2", versions)
	}
}
