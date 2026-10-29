package rebac

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

type authorizationRevisionObserverFunc func(int64)

func (f authorizationRevisionObserverFunc) AuthorizationRevisionAdvanced(revision int64) {
	f(revision)
}

func TestPlatformStorePersistsRelationTuplesAndChecksByTenant(t *testing.T) {
	ctx := context.Background()
	sqlStore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "rebac-platform-store"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer sqlStore.Close()
	if err := migration.NewRunner([]migration.Migration{relationTupleMigration(), authorizationRevisionMigration()}).Up(ctx, sqlStore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	now := time.Unix(300, 0).UTC()
	validTo := now.Add(time.Hour)
	observedRevision := int64(-1)
	store := NewPlatformStore(sqlStore, WithAuthorizationRevisionObserver(authorizationRevisionObserverFunc(func(revision int64) {
		observedRevision = revision
	})))
	if revision, err := store.AuthorizationRevision(ctx); err != nil || revision != 0 {
		t.Fatalf("initial authorization revision = %d, err=%v", revision, err)
	}
	if err := store.SaveRelationTuple(ctx, RelationTuple{
		ID:        "rel-sql-1",
		TenantID:  "tenant-1",
		Subject:   Ref{Kind: "human", ID: "alice"},
		Relation:  "viewer",
		Object:    Ref{Kind: "knowledge_base", ID: "kb-1"},
		Source:    Ref{Kind: "seed", ID: "test"},
		ValidTo:   &validTo,
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("SaveRelationTuple: %v", err)
	}
	if revision, err := store.AuthorizationRevision(ctx); err != nil || revision != 1 {
		t.Fatalf("authorization revision after save = %d, err=%v", revision, err)
	}
	if observedRevision != 1 {
		t.Fatalf("observed authorization revision = %d", observedRevision)
	}
	repeated := RelationTuple{
		ID: "rel-sql-1", TenantID: "tenant-1", Subject: Ref{Kind: "human", ID: "alice"},
		Relation: "viewer", Object: Ref{Kind: "knowledge_base", ID: "kb-1"},
		Source: Ref{Kind: "seed", ID: "test"}, ValidTo: &validTo, CreatedAt: now.Add(time.Hour),
	}
	if err := store.SaveRelationTuple(ctx, repeated); err != nil {
		t.Fatalf("repeat SaveRelationTuple: %v", err)
	}
	if revision, err := store.AuthorizationRevision(ctx); err != nil || revision != 1 {
		t.Fatalf("authorization revision after unchanged save = %d, err=%v", revision, err)
	}
	if observedRevision != 1 {
		t.Fatalf("observer advanced after unchanged save: %d", observedRevision)
	}
	missing := repeated
	missing.ID = "missing"
	if err := store.DeleteRelationTuple(ctx, missing); err != nil {
		t.Fatalf("DeleteRelationTuple missing: %v", err)
	}
	if revision, err := store.AuthorizationRevision(ctx); err != nil || revision != 1 {
		t.Fatalf("authorization revision after missing delete = %d, err=%v", revision, err)
	}

	reopened := NewPlatformStore(sqlStore)
	allowed, matched, err := reopened.CheckRelation(ctx, RelationCheck{
		TenantID: "tenant-1",
		Subject:  Ref{Kind: "human", ID: "alice"},
		Relation: "viewer",
		Object:   Ref{Kind: "knowledge_base", ID: "kb-1"},
		At:       now,
	})
	if err != nil {
		t.Fatalf("CheckRelation: %v", err)
	}
	if !allowed || matched.ID != "rel-sql-1" || matched.Source.Kind != "seed" {
		t.Fatalf("allowed=%v matched=%#v", allowed, matched)
	}

	allowed, _, err = reopened.CheckRelation(ctx, RelationCheck{
		TenantID: "tenant-2",
		Subject:  Ref{Kind: "human", ID: "alice"},
		Relation: "viewer",
		Object:   Ref{Kind: "knowledge_base", ID: "kb-1"},
		At:       now,
	})
	if err != nil {
		t.Fatalf("CheckRelation tenant-2: %v", err)
	}
	if allowed {
		t.Fatal("relation leaked across tenants")
	}
	if err := store.SaveRelationTuple(ctx, RelationTuple{
		ID:        "rel-sql-2",
		TenantID:  "tenant-2",
		Subject:   Ref{Kind: "human", ID: "alice"},
		Relation:  "viewer",
		Object:    Ref{Kind: "knowledge_base", ID: "kb-2"},
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("SaveRelationTuple tenant-2: %v", err)
	}
	tuples, err := store.ListRelationTuplesByTenant(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("ListRelationTuplesByTenant: %v", err)
	}
	if len(tuples) != 1 || tuples[0].ID != "rel-sql-1" {
		t.Fatalf("tenant-1 tuples = %+v", tuples)
	}
	batched, err := store.ListRelationTuplesByTenants(ctx, []string{"tenant-2", "tenant-1", "tenant-2"})
	if err != nil {
		t.Fatalf("ListRelationTuplesByTenants: %v", err)
	}
	if len(batched) != 2 || batched[0].TenantID != "tenant-1" || batched[0].ID != "rel-sql-1" || batched[1].TenantID != "tenant-2" || batched[1].ID != "rel-sql-2" {
		t.Fatalf("batched tuples = %+v", batched)
	}
}

func TestPlatformStoreRollsBackRelationWhenRevisionCannotAdvance(t *testing.T) {
	ctx := context.Background()
	sqlStore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "rebac-revision-rollback"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer sqlStore.Close()
	if err := migration.NewRunner([]migration.Migration{relationTupleMigration()}).Up(ctx, sqlStore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	err = NewPlatformStore(sqlStore).SaveRelationTuple(ctx, RelationTuple{
		ID: "rel-1", TenantID: "tenant-1", Subject: Ref{Kind: "user", ID: "user-1"}, Relation: "viewer", Object: Ref{Kind: "mission", ID: "mission-1"}, CreatedAt: time.Now(),
	})
	if err == nil {
		t.Fatal("save without revision state error = nil")
	}
	var count int
	if err := sqlStore.QueryRow(ctx, `SELECT COUNT(*) FROM rebac_relation_tuples`).Scan(&count); err != nil {
		t.Fatalf("count relations: %v", err)
	}
	if count != 0 {
		t.Fatalf("relation committed without revision: count=%d", count)
	}
}
