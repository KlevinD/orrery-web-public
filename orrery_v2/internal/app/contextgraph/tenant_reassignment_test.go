package contextgraph

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestPlatformStoreReassignTenantMergesAndRemovesLegacyGraph(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer database.Close()
	if err := migration.NewRunner(contextGraphCompleteStoreTestMigrations()).Up(ctx, database); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	store := NewPlatformStore(database)
	legacyTenant := "org-system-runtime-support"
	canonicalTenant := "minvia"
	observedAt := time.Unix(10, 0).UTC()
	legacyAccess := AccessRef{Kind: "organization", ID: legacyTenant, TenantID: legacyTenant}

	if err := store.SaveObject(ctx, Object{
		ID: legacyTenant, TenantID: legacyTenant, ObjectType: "organization",
		CanonicalKey: "organization:" + legacyTenant, Label: "Legacy", OwnerAccessRef: legacyAccess, UpdatedAt: observedAt,
	}); err != nil {
		t.Fatalf("save legacy organization: %v", err)
	}
	if err := store.SaveObject(ctx, Object{
		ID: "task-1", TenantID: legacyTenant, ObjectType: "task", CanonicalKey: "task:1",
		Label: "legacy task", OwnerAccessRef: legacyAccess, UpdatedAt: observedAt,
	}); err != nil {
		t.Fatalf("save legacy task: %v", err)
	}
	if err := store.SaveObject(ctx, Object{
		ID: "task-1", TenantID: canonicalTenant, ObjectType: "task", CanonicalKey: "task:1",
		Label: "canonical task", OwnerAccessRef: AccessRef{Kind: "organization", ID: canonicalTenant, TenantID: canonicalTenant}, UpdatedAt: observedAt,
	}); err != nil {
		t.Fatalf("save canonical task: %v", err)
	}
	if err := store.SaveLink(ctx, Link{
		ID: "link-1", TenantID: legacyTenant, LinkType: "owns", FromObjectID: legacyTenant, ToObjectID: "task-1",
		SourceAccessRef: legacyAccess, UpdatedAt: observedAt,
	}); err != nil {
		t.Fatalf("save legacy link: %v", err)
	}
	if err := store.SaveFact(ctx, Fact{
		ID: "fact-1", TenantID: legacyTenant, Subject: Ref{Kind: "organization", ID: legacyTenant},
		Predicate: "organization_id", Value: legacyTenant, SourceStreamID: legacyTenant,
		SourceAccessRef: legacyAccess, UpdatedAt: observedAt,
	}); err != nil {
		t.Fatalf("save legacy fact: %v", err)
	}
	if err := store.SaveEvidence(ctx, Evidence{
		ID: "evidence-1", TenantID: legacyTenant, SourceRef: Ref{Kind: "organization", ID: legacyTenant},
		Summary: "legacy evidence", SourceAccessRef: legacyAccess, ObservedAt: observedAt,
	}); err != nil {
		t.Fatalf("save legacy evidence: %v", err)
	}

	changed, err := store.ReassignTenant(ctx, legacyTenant, canonicalTenant)
	if err != nil {
		t.Fatalf("ReassignTenant: %v", err)
	}
	if changed < 5 {
		t.Fatalf("changed = %d, want at least 5", changed)
	}
	if changed, err = store.ReassignTenant(ctx, legacyTenant, canonicalTenant); err != nil || changed != 0 {
		t.Fatalf("idempotent ReassignTenant = %d, %v", changed, err)
	}

	legacyObjects, legacyLinks, legacyFacts, legacyEvidence, err := store.Snapshot(ctx, legacyTenant)
	if err != nil {
		t.Fatalf("legacy Snapshot: %v", err)
	}
	if len(legacyObjects)+len(legacyLinks)+len(legacyFacts)+len(legacyEvidence) != 0 {
		t.Fatalf("legacy graph remains: objects=%d links=%d facts=%d evidence=%d", len(legacyObjects), len(legacyLinks), len(legacyFacts), len(legacyEvidence))
	}
	objects, links, facts, evidence, err := store.Snapshot(ctx, canonicalTenant)
	if err != nil {
		t.Fatalf("canonical Snapshot: %v", err)
	}
	if len(objects) != 2 || len(links) != 1 || len(facts) != 1 || len(evidence) != 1 {
		t.Fatalf("canonical graph objects=%d links=%d facts=%d evidence=%d", len(objects), len(links), len(facts), len(evidence))
	}
	if links[0].FromObjectID != canonicalTenant || links[0].SourceAccessRef.TenantID != canonicalTenant {
		t.Fatalf("link was not normalized: %+v", links[0])
	}
	if facts[0].Subject.ID != canonicalTenant || facts[0].Value != canonicalTenant || facts[0].SourceAccessRef.ID != canonicalTenant {
		t.Fatalf("fact was not normalized: %+v", facts[0])
	}
	if evidence[0].SourceRef.ID != canonicalTenant || evidence[0].SourceAccessRef.TenantID != canonicalTenant {
		t.Fatalf("evidence was not normalized: %+v", evidence[0])
	}
	for _, object := range objects {
		if object.ID == "task-1" && object.Label != "canonical task" {
			t.Fatalf("canonical conflict was overwritten: %+v", object)
		}
	}
}

func TestPlatformStoreDeleteTenantRemovesRetiredGraph(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer database.Close()
	if err := migration.NewRunner(contextGraphCompleteStoreTestMigrations()).Up(ctx, database); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	store := NewPlatformStore(database)
	tenantID := "human-superadmin"
	access := AccessRef{Kind: "human", ID: tenantID, TenantID: tenantID}
	observedAt := time.Unix(20, 0).UTC()
	if err := store.SaveObject(ctx, Object{ID: "kb-legacy", TenantID: tenantID, ObjectType: "knowledge_base", OwnerAccessRef: access, UpdatedAt: observedAt}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveObject(ctx, Object{ID: "doc-legacy", TenantID: tenantID, ObjectType: "knowledge_document", OwnerAccessRef: access, UpdatedAt: observedAt}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLink(ctx, Link{ID: "link-legacy", TenantID: tenantID, LinkType: "knowledge_base_contains_document", FromObjectID: "kb-legacy", ToObjectID: "doc-legacy", SourceAccessRef: access, UpdatedAt: observedAt}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveFact(ctx, Fact{ID: "fact-legacy", TenantID: tenantID, Subject: Ref{Kind: "knowledge_base", ID: "kb-legacy"}, Predicate: "name", Value: "legacy", SourceStreamID: "retired-principal-test", SourceAccessRef: access, UpdatedAt: observedAt}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveEvidence(ctx, Evidence{ID: "evidence-legacy", TenantID: tenantID, SourceRef: Ref{Kind: "knowledge_base", ID: "kb-legacy"}, SourceAccessRef: access, ObservedAt: observedAt}); err != nil {
		t.Fatal(err)
	}

	deleted, err := store.DeleteTenant(ctx, tenantID)
	if err != nil || deleted < 5 {
		t.Fatalf("DeleteTenant = %d, %v", deleted, err)
	}
	objects, links, facts, evidence, err := store.Snapshot(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if len(objects)+len(links)+len(facts)+len(evidence) != 0 {
		t.Fatalf("retired graph survived: objects=%d links=%d facts=%d evidence=%d", len(objects), len(links), len(facts), len(evidence))
	}
	if repeated, err := store.DeleteTenant(ctx, tenantID); err != nil || repeated != 0 {
		t.Fatalf("repeated DeleteTenant = %d, %v", repeated, err)
	}
}
