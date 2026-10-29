package ontologyscope

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestBindingBackfillIsReportOnlyBoundedAndCheckpointed(t *testing.T) {
	ctx := context.Background()
	database := bindingBackfillTestStore(t)
	defer database.Close()
	if _, err := database.Exec(ctx, `CREATE TABLE context_graph_objects (tenant_id TEXT NOT NULL)`); err != nil {
		t.Fatalf("create candidates table: %v", err)
	}
	for _, tenantID := range []string{"mission:1", "mission:2", "unknown"} {
		if _, err := database.Exec(ctx, `INSERT INTO context_graph_objects (tenant_id) VALUES (?)`, tenantID); err != nil {
			t.Fatalf("insert candidate: %v", err)
		}
	}
	resolver := bindingBackfillResolver(map[string]ScopeBinding{
		"mission:1": testBinding("mission:1", KindMission, "mission", "1", "global"),
		"mission:2": testBinding("mission:2", KindMission, "mission", "2", "global"),
	})
	store := NewPlatformBindingStore(database)
	first, err := BackfillBindings(ctx, database, resolver, store, BindingBackfillOptions{Limit: 1})
	if err != nil {
		t.Fatalf("first report: %v", err)
	}
	if !first.HasMore || first.Scanned != 1 || first.Resolved != 1 || first.Applied != 0 || first.Next.Source != "context_graph_objects" {
		t.Fatalf("first report = %+v", first)
	}
	if _, err := store.GetBinding(ctx, first.Next.TenantID); !errors.Is(err, ErrBindingNotFound) {
		t.Fatalf("report-only binding error = %v", err)
	}
	second, err := BackfillBindings(ctx, database, resolver, store, BindingBackfillOptions{Limit: 2, After: first.Next})
	if err != nil {
		t.Fatalf("second report: %v", err)
	}
	if second.Scanned != 2 || second.Resolved != 1 || second.Unresolved != 1 || len(second.UnresolvedTenantIDs) != 1 || second.UnresolvedTenantIDs[0] != "unknown" {
		t.Fatalf("second report = %+v", second)
	}
	if _, err := BackfillBindings(ctx, database, resolver, store, BindingBackfillOptions{Limit: MaxBindingBackfillBatch + 1}); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("oversized batch error = %v", err)
	}
	if _, err := BackfillBindings(ctx, database, resolver, store, BindingBackfillOptions{After: BindingBackfillCursor{TenantID: "mission:1"}}); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("incomplete cursor error = %v", err)
	}
}

func TestBindingBackfillApplyPersistsOnlyExactlyResolvedBindings(t *testing.T) {
	ctx := context.Background()
	database := bindingBackfillTestStore(t)
	defer database.Close()
	if _, err := database.Exec(ctx, `CREATE TABLE context_graph_objects (tenant_id TEXT NOT NULL)`); err != nil {
		t.Fatalf("create candidates table: %v", err)
	}
	for _, tenantID := range []string{"mission:1", "mission:guessed"} {
		if _, err := database.Exec(ctx, `INSERT INTO context_graph_objects (tenant_id) VALUES (?)`, tenantID); err != nil {
			t.Fatalf("insert candidate: %v", err)
		}
	}
	resolver := bindingBackfillResolver(map[string]ScopeBinding{
		"mission:1": testBinding("mission:1", KindMission, "mission", "1", "global"),
	})
	store := NewPlatformBindingStore(database)
	if _, err := BackfillBindings(ctx, database, resolver, store, BindingBackfillOptions{Apply: true, Limit: 10}); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("missing operator error = %v", err)
	}
	report, err := BackfillBindings(ctx, database, resolver, store, BindingBackfillOptions{Apply: true, Limit: 10, ChangedBy: "operator-1"})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if report.Applied != 1 || report.Unresolved != 1 {
		t.Fatalf("apply report = %+v", report)
	}
	if _, err := store.GetBinding(ctx, "mission:1"); err != nil {
		t.Fatalf("persisted exact binding: %v", err)
	}
	if _, err := store.GetBinding(ctx, "mission:guessed"); !errors.Is(err, ErrBindingNotFound) {
		t.Fatalf("guessed binding error = %v", err)
	}
}

func TestBindingBackfillDoesNotClaimUnprovenNextPage(t *testing.T) {
	ctx := context.Background()
	database := bindingBackfillTestStore(t)
	defer database.Close()
	if _, err := database.Exec(ctx, `CREATE TABLE context_graph_objects (tenant_id TEXT NOT NULL)`); err != nil {
		t.Fatalf("create candidates table: %v", err)
	}
	if _, err := database.Exec(ctx, `INSERT INTO context_graph_objects (tenant_id) VALUES (?)`, "mission:1"); err != nil {
		t.Fatalf("insert candidate: %v", err)
	}
	resolver := bindingBackfillResolver{"mission:1": testBinding("mission:1", KindMission, "mission", "1", "global")}
	report, err := BackfillBindings(ctx, database, resolver, NewPlatformBindingStore(database), BindingBackfillOptions{Limit: 1})
	if err != nil {
		t.Fatalf("backfill report: %v", err)
	}
	if report.HasMore || report.Next != (BindingBackfillCursor{}) {
		t.Fatalf("terminal report = %+v", report)
	}
	if _, err := BackfillBindings(ctx, database, resolver, NewPlatformBindingStore(database), BindingBackfillOptions{After: BindingBackfillCursor{Source: "unknown", TenantID: "mission:1"}}); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("unknown source error = %v", err)
	}
}

type bindingBackfillResolver map[string]ScopeBinding

func (r bindingBackfillResolver) Resolve(_ context.Context, tenantID string) (ScopeBinding, error) {
	binding, ok := r[tenantID]
	if !ok {
		return ScopeBinding{}, ErrBindingNotFound
	}
	return binding, nil
}

func bindingBackfillTestStore(t *testing.T) *db.Store {
	t.Helper()
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := migration.NewRunner([]migration.Migration{scopeBindingMigration()}).Up(ctx, database); err != nil {
		database.Close()
		t.Fatalf("migrate database: %v", err)
	}
	return database
}
