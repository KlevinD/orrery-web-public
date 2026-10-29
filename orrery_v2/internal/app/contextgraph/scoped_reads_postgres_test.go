package contextgraph

import (
	"context"
	"strings"
	"testing"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
	"orrery_v2/internal/platform/testpg"
)

func TestPostgresStallGraphReadsAreTenantAndDocumentScoped(t *testing.T) {
	registry := testpg.Registry(t)
	coordinator := migration.NewCoordinator(registry)
	if err := RegisterMigrations(coordinator); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatal(err)
	}
	database, err := registry.Store(db.RoleCore)
	if err != nil {
		t.Fatal(err)
	}
	graph := NewPlatformStore(database)
	for _, tenant := range []string{"stall-tenant-a", "stall-tenant-b"} {
		access := AccessRef{Kind: "organization", ID: tenant, TenantID: tenant}
		for _, object := range []Object{{ID: "same-person", ObjectType: "person"}, {ID: "document", ObjectType: "knowledge_document"}} {
			object.TenantID, object.OwnerAccessRef = tenant, access
			if err := graph.EnsureObjectReference(ctx, object); err != nil {
				t.Fatal(err)
			}
		}
		for _, document := range []string{"doc_%", "doc-other"} {
			if err := graph.SaveLink(ctx, Link{ID: document + "-link", TenantID: tenant, FromObjectID: "document", ToObjectID: "same-person", LinkType: "mentions", SourceAccessRef: access, SourceStreamID: "knowledge_revision:" + document}); err != nil {
				t.Fatal(err)
			}
			if err := graph.SaveEvidence(ctx, Evidence{ID: document + "-evidence", TenantID: tenant, SourceRef: Ref{Kind: "knowledge_revision", ID: document + "@1"}, SourceAccessRef: access}); err != nil {
				t.Fatal(err)
			}
		}
		for _, fact := range []Fact{
			{ID: "provider-fact", Subject: Ref{Kind: "knowledge_document", ID: "document"}, Predicate: "Provider_Document_ID", Value: "doc_%"},
			{ID: "base-fact", Subject: Ref{Kind: "knowledge_document", ID: "document"}, Predicate: "knowledge_base_id", Value: "base"},
			{ID: "unrelated-fact", Subject: Ref{Kind: "person", ID: "same-person"}, Predicate: "name", Value: "Someone"},
		} {
			fact.TenantID, fact.SourceAccessRef, fact.SubjectAccessRef = tenant, access, access
			fact.SourceStreamID, fact.SourceVersion = "stall-metadata:"+fact.ID, 1
			if err := graph.SaveFact(ctx, fact); err != nil {
				t.Fatal(err)
			}
		}
	}
	objects, err := graph.ObjectsByID(ctx, "stall-tenant-a", []string{"same-person", "missing"})
	if err != nil || len(objects) != 1 || objects[0].TenantID != "stall-tenant-a" {
		t.Fatalf("objects=%+v err=%v", objects, err)
	}
	_, links, facts, evidence, err := graph.SnapshotKnowledgeDocument(ctx, "stall-tenant-a", "doc_%")
	if err != nil || len(links) != 1 || len(evidence) != 1 || links[0].ID != "doc_%-link" || evidence[0].SourceRef.ID != "doc_%@1" {
		t.Fatalf("links=%+v evidence=%+v err=%v", links, evidence, err)
	}
	if len(facts) != 2 {
		t.Fatalf("dictionary metadata facts=%+v", facts)
	}
	// A sparse metadata lookup should use the dedicated index even when most
	// tenant facts have nothing to do with knowledge documents.
	if _, err := database.Exec(ctx, `INSERT INTO context_graph_facts (id, tenant_id, subject_kind, subject_id, predicate, updated_at)
SELECT 'noise-' || n, 'stall-tenant-a', 'person', 'same-person', 'name', 0 FROM generate_series(1, 3000) n`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(ctx, `ANALYZE context_graph_facts`); err != nil {
		t.Fatal(err)
	}
	var plan string
	if err := database.QueryRow(ctx, `EXPLAIN (FORMAT JSON) SELECT id FROM context_graph_facts WHERE tenant_id = ? AND LOWER(subject_kind) = 'knowledge_document' AND LOWER(TRIM(predicate)) IN ('provider_document_id', 'knowledge_base_id') ORDER BY id`, "stall-tenant-a").Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "idx_context_graph_knowledge_metadata") {
		t.Fatalf("metadata lookup ignored its sparse index: %s", plan)
	}
	if _, err := graph.Objects(ctx, ""); err == nil {
		t.Fatal("blank tenant accepted")
	}
	// Dictionary and memory reads must remain usable without fact/evidence
	// tables; renames are rolled back and affect only this isolated schema.
	if err := database.WithTx(ctx, func(tx db.Tx) error {
		for _, table := range []string{"context_graph_facts", "context_graph_evidence"} {
			if _, err := tx.ExecContext(ctx, "ALTER TABLE "+table+" RENAME TO unused_"+table); err != nil {
				return err
			}
		}
		selected := NewPlatformStoreTx(tx)
		objects, links, err := selected.SnapshotObjectsLinks(ctx, "stall-tenant-a")
		if err != nil {
			return err
		}
		if len(objects) != 2 || len(links) != 2 {
			t.Errorf("objects=%d links=%d", len(objects), len(links))
		}
		for _, table := range []string{"context_graph_facts", "context_graph_evidence"} {
			if _, err := tx.ExecContext(ctx, "ALTER TABLE unused_"+table+" RENAME TO "+table); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
