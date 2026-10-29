package rebac

import (
	"context"
	"testing"
	"time"
)

func TestMemoryStoreChecksDirectTenantScopedRelations(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(100, 0).UTC()
	store := NewMemoryStore()
	tuple := RelationTuple{
		ID:        "rel-1",
		TenantID:  "tenant-1",
		Subject:   Ref{Kind: "human", ID: "alice"},
		Relation:  "owner",
		Object:    Ref{Kind: "mission", ID: "mission-1"},
		Source:    Ref{Kind: "seed", ID: "test"},
		ValidFrom: &now,
		CreatedAt: now,
	}
	if err := store.SaveRelationTuple(ctx, tuple); err != nil {
		t.Fatalf("SaveRelationTuple: %v", err)
	}
	if revision, err := store.AuthorizationRevision(ctx); err != nil || revision != 1 {
		t.Fatalf("authorization revision = %d, err=%v", revision, err)
	}

	allowed, matched, err := store.CheckRelation(ctx, RelationCheck{
		TenantID: "tenant-1",
		Subject:  Ref{Kind: "human", ID: "alice"},
		Relation: "owner",
		Object:   Ref{Kind: "mission", ID: "mission-1"},
		At:       now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("CheckRelation: %v", err)
	}
	if !allowed || matched.ID != tuple.ID {
		t.Fatalf("allowed=%v matched=%#v", allowed, matched)
	}

	allowed, _, err = store.CheckRelation(ctx, RelationCheck{
		TenantID: "tenant-2",
		Subject:  Ref{Kind: "human", ID: "alice"},
		Relation: "owner",
		Object:   Ref{Kind: "mission", ID: "mission-1"},
		At:       now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("CheckRelation tenant-2: %v", err)
	}
	if allowed {
		t.Fatal("relation leaked across tenants")
	}
}

func TestMemoryStoreAdvancesRevisionOnlyForAuthorizationChanges(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Unix(100, 0).UTC()
	tuple := RelationTuple{
		ID: "rel-1", TenantID: "tenant-1", Subject: Ref{Kind: "human", ID: "alice"},
		Relation: "viewer", Object: Ref{Kind: "mission", ID: "mission-1"},
		Source: Ref{Kind: "seed", ID: "test"}, CreatedAt: now,
	}
	if err := store.SaveRelationTuple(ctx, tuple); err != nil {
		t.Fatalf("SaveRelationTuple: %v", err)
	}
	tuple.CreatedAt = now.Add(time.Hour)
	if err := store.SaveRelationTuple(ctx, tuple); err != nil {
		t.Fatalf("repeat SaveRelationTuple: %v", err)
	}
	if revision, err := store.AuthorizationRevision(ctx); err != nil || revision != 1 {
		t.Fatalf("revision after unchanged save = %d, err=%v", revision, err)
	}
	tuple.Relation = "editor"
	if err := store.SaveRelationTuple(ctx, tuple); err != nil {
		t.Fatalf("changed SaveRelationTuple: %v", err)
	}
	if revision, err := store.AuthorizationRevision(ctx); err != nil || revision != 2 {
		t.Fatalf("revision after changed save = %d, err=%v", revision, err)
	}
	missing := tuple
	missing.ID = "missing"
	if err := store.DeleteRelationTuple(ctx, missing); err != nil {
		t.Fatalf("DeleteRelationTuple missing: %v", err)
	}
	if revision, err := store.AuthorizationRevision(ctx); err != nil || revision != 2 {
		t.Fatalf("revision after missing delete = %d, err=%v", revision, err)
	}
}

func TestMemoryStoreIgnoresExpiredRelations(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(200, 0).UTC()
	expiredAt := now.Add(-time.Minute)
	store := NewMemoryStore()
	if err := store.SaveRelationTuple(ctx, RelationTuple{
		ID:        "rel-expired",
		TenantID:  "tenant-1",
		Subject:   Ref{Kind: "agent", ID: "agent-1"},
		Relation:  "can_write",
		Object:    Ref{Kind: "task", ID: "task-1"},
		ValidTo:   &expiredAt,
		CreatedAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("SaveRelationTuple: %v", err)
	}

	allowed, _, err := store.CheckRelation(ctx, RelationCheck{
		TenantID: "tenant-1",
		Subject:  Ref{Kind: "agent", ID: "agent-1"},
		Relation: "can_write",
		Object:   Ref{Kind: "task", ID: "task-1"},
		At:       now,
	})
	if err != nil {
		t.Fatalf("CheckRelation: %v", err)
	}
	if allowed {
		t.Fatal("expired relation should not allow access")
	}
}
