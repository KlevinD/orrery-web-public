package knowledgeontology

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestPlatformStorePersistsBindingProjectionAndCursorAtomically(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "knowledge-ontology-store"})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	if err := migration.NewRunner([]migration.Migration{bridgeMigration(), associationMigration()}).Up(ctx, database); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	store := NewPlatformStore(database)
	binding := Binding{
		TenantID: "tenant-1", KnowledgeBaseID: "kb-1", FolderID: "folder-1",
		SourceAccessRef: contextgraph.AccessRef{Kind: "organization", ID: "tenant-1", TenantID: "tenant-1"},
		Enabled:         true, UpdatedAt: time.Date(2026, 7, 24, 2, 0, 0, 0, time.UTC),
	}
	if err := store.SaveBinding(ctx, binding); err != nil {
		t.Fatalf("SaveBinding: %v", err)
	}
	projection := Projection{
		TenantID: "tenant-1", KnowledgeBaseID: "kb-1", DocumentID: "doc-1", Revision: 2, ContentHash: "sha256:abc",
		Assertions: []AssertionProjection{{LinkType: "project_uses_tool", FromObjectID: "project-1", ToObjectID: "tool-1", Status: AssertionConfirmed}},
	}

	if err := store.SaveProjectionAndCursor(ctx, binding, projection, "8"); err != nil {
		t.Fatalf("SaveProjectionAndCursor: %v", err)
	}

	reopened := NewPlatformStore(database)
	gotBinding, err := reopened.GetBinding(ctx, "tenant-1", "kb-1")
	if err != nil || gotBinding.FolderID != "folder-1" || !gotBinding.Enabled {
		t.Fatalf("GetBinding = %+v, %v", gotBinding, err)
	}
	cursor, err := reopened.Cursor(ctx, binding)
	if err != nil || cursor != "8" {
		t.Fatalf("Cursor = %q, %v", cursor, err)
	}
	gotProjection, err := reopened.GetProjection(ctx, "tenant-1", "doc-1", 2)
	if err != nil || len(gotProjection.Assertions) != 1 || gotProjection.ContentHash != "sha256:abc" {
		t.Fatalf("GetProjection = %+v, %v", gotProjection, err)
	}
}

func TestPlatformStoreBindingIsTenantScoped(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "knowledge-ontology-tenant-store"})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	if err := migration.NewRunner([]migration.Migration{bridgeMigration(), associationMigration()}).Up(ctx, database); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	store := NewPlatformStore(database)
	for _, tenantID := range []string{"tenant-1", "tenant-2"} {
		if err := store.SaveBinding(ctx, Binding{TenantID: tenantID, KnowledgeBaseID: "kb-shared", FolderID: "folder-" + tenantID, Enabled: true}); err != nil {
			t.Fatalf("SaveBinding %s: %v", tenantID, err)
		}
	}
	bindings, err := store.ListBindings(ctx, "tenant-1")
	if err != nil || len(bindings) != 1 || bindings[0].FolderID != "folder-tenant-1" {
		t.Fatalf("ListBindings = %+v, %v", bindings, err)
	}
}

func TestPlatformStoreDeletesOnlyBoundDocumentProjectionState(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "knowledge-ontology-delete-document"})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	if err := migration.NewRunner([]migration.Migration{bridgeMigration(), associationMigration()}).Up(ctx, database); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	store := NewPlatformStore(database)
	binding := Binding{TenantID: "tenant-1", KnowledgeBaseID: "kb-1", FolderID: "folder-1", Enabled: true}
	if err := store.SaveBinding(ctx, binding); err != nil {
		t.Fatalf("SaveBinding: %v", err)
	}
	for _, documentID := range []string{"delete-doc", "keep-doc"} {
		if err := store.SaveProjectionAndCursor(ctx, binding, Projection{
			TenantID: "tenant-1", KnowledgeBaseID: "kb-1", DocumentID: documentID, Revision: 1,
			Subjects:   []SubjectProjection{{Reference: ObjectRef{Kind: "project"}, ObjectID: "project-1"}},
			Assertions: []AssertionProjection{{LinkType: "project_uses_tool"}},
		}, "1"); err != nil {
			t.Fatalf("SaveProjectionAndCursor(%s): %v", documentID, err)
		}
	}

	if err := store.DeleteDocument(ctx, binding, "delete-doc"); err != nil {
		t.Fatalf("DeleteDocument: %v", err)
	}
	documentIDs, err := store.ListProjectedDocumentIDs(ctx, binding)
	if err != nil || len(documentIDs) != 1 || documentIDs[0] != "keep-doc" {
		t.Fatalf("projected documents = %+v, %v", documentIDs, err)
	}
	for _, table := range []string{"knowledge_ontology_projections", "knowledge_ontology_mentions", "knowledge_ontology_claims"} {
		var count int
		if err := database.QueryRow(ctx, "SELECT COUNT(*) FROM "+table+" WHERE tenant_id = ? AND document_id = ?", "tenant-1", "delete-doc").Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s deleted count = %d, %v", table, count, err)
		}
	}
}

func TestPlatformStoreReconcileOwnedBindingRemovesDuplicateTenantState(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "knowledge-ontology-owner-reconcile"})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	if err := migration.NewRunner([]migration.Migration{bridgeMigration(), associationMigration()}).Up(ctx, database); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	store := NewPlatformStore(database)
	legacy := Binding{TenantID: "legacy-org", KnowledgeBaseID: "kb-1", FolderID: "folder-1", Enabled: true}
	if err := store.SaveBinding(ctx, legacy); err != nil {
		t.Fatalf("SaveBinding: %v", err)
	}
	if err := store.SaveProjectionAndCursor(ctx, legacy, Projection{
		TenantID: "legacy-org", KnowledgeBaseID: "kb-1", DocumentID: "doc-1", Revision: 1,
		Subjects: []SubjectProjection{{Reference: ObjectRef{Kind: "project", ID: "project-1"}, ObjectID: "project-1", Resolution: ResolutionResolved}},
	}, "1"); err != nil {
		t.Fatalf("SaveProjectionAndCursor: %v", err)
	}
	if err := store.SaveOntologyDictionaryVersion(ctx, legacy, "v1"); err != nil {
		t.Fatalf("SaveOntologyDictionaryVersion: %v", err)
	}
	owned := Binding{TenantID: "minvia", KnowledgeBaseID: "kb-1", FolderID: "folder-1", Enabled: true}
	if err := store.ReconcileOwnedBinding(ctx, owned); err != nil {
		t.Fatalf("ReconcileOwnedBinding: %v", err)
	}
	legacyBindings, err := store.ListBindings(ctx, "legacy-org")
	if err != nil || len(legacyBindings) != 0 {
		t.Fatalf("legacy bindings = %+v, %v", legacyBindings, err)
	}
	current, err := store.GetBinding(ctx, "minvia", "kb-1")
	if err != nil || current.FolderID != "folder-1" {
		t.Fatalf("current binding = %+v, %v", current, err)
	}
	for _, table := range []string{"knowledge_ontology_projections", "knowledge_ontology_mentions", "knowledge_ontology_dictionary_versions"} {
		var count int
		if err := database.QueryRow(ctx, "SELECT COUNT(*) FROM "+table+" WHERE tenant_id = ?", "legacy-org").Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s legacy count = %d, %v", table, count, err)
		}
	}
}

func TestPlatformStoreListsLatestDocumentRelationsInBothDirections(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "knowledge-ontology-relations"})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	if err := migration.NewRunner([]migration.Migration{bridgeMigration(), associationMigration()}).Up(ctx, database); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	store := NewPlatformStore(database)
	binding := Binding{TenantID: "tenant-1", KnowledgeBaseID: "kb-1", FolderID: "folder-1", Enabled: true}
	if err := store.SaveBinding(ctx, binding); err != nil {
		t.Fatalf("SaveBinding: %v", err)
	}
	assertion := func(from, to string, anchor string) AssertionProjection {
		return AssertionProjection{
			LinkType: DocumentReferenceLinkType, FromObjectID: from, ToObjectID: to,
			Status: AssertionConfirmed, Evidence: Evidence{Anchor: anchor},
		}
	}
	for _, projection := range []Projection{
		{TenantID: "tenant-1", KnowledgeBaseID: "kb-1", DocumentID: "provider-a", Revision: 1, Assertions: []AssertionProjection{assertion("doc-a", "doc-stale", "stale")}},
		{TenantID: "tenant-1", KnowledgeBaseID: "kb-1", DocumentID: "provider-a", Revision: 2, Assertions: []AssertionProjection{assertion("doc-a", "doc-b", "outgoing")}},
		{TenantID: "tenant-1", KnowledgeBaseID: "kb-1", DocumentID: "provider-c", Revision: 1, Assertions: []AssertionProjection{assertion("doc-c", "doc-a", "incoming")}},
	} {
		if err := store.SaveProjectionAndCursor(ctx, binding, projection, ""); err != nil {
			t.Fatalf("SaveProjectionAndCursor(%s): %v", projection.DocumentID, err)
		}
	}

	relations, err := store.ListDocumentRelations(ctx, "tenant-1", "kb-1", "doc-a", DocumentRelationBoth, 10)
	if err != nil {
		t.Fatalf("ListDocumentRelations: %v", err)
	}
	if len(relations) != 2 {
		t.Fatalf("relations = %+v", relations)
	}
	if relations[0].Direction != DocumentRelationOutgoing || relations[0].OtherDocumentID != "doc-b" || relations[0].Anchor != "outgoing" {
		t.Fatalf("outgoing relation = %+v", relations[0])
	}
	if relations[1].Direction != DocumentRelationIncoming || relations[1].OtherDocumentID != "doc-c" || relations[1].Anchor != "incoming" {
		t.Fatalf("incoming relation = %+v", relations[1])
	}

	incoming, err := store.ListDocumentRelations(ctx, "tenant-1", "kb-1", "doc-a", DocumentRelationIncoming, 10)
	if err != nil || len(incoming) != 1 || incoming[0].OtherDocumentID != "doc-c" {
		t.Fatalf("incoming = %+v, %v", incoming, err)
	}
}

func TestPlatformStoreListsLatestObjectAssociations(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "knowledge-ontology-associations"})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	if err := migration.NewRunner([]migration.Migration{bridgeMigration(), associationMigration()}).Up(ctx, database); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	store := NewPlatformStore(database)
	binding := Binding{TenantID: "tenant-1", KnowledgeBaseID: "kb-1", FolderID: "folder-1", Enabled: true}
	if err := store.SaveBinding(ctx, binding); err != nil {
		t.Fatalf("SaveBinding: %v", err)
	}
	mention := func(revision int, anchor string) Projection {
		return Projection{
			TenantID: "tenant-1", KnowledgeBaseID: "kb-1", DocumentID: "provider-doc", Revision: revision,
			Subjects: []SubjectProjection{{
				Reference: ObjectRef{Kind: "project", ID: "project-1"}, ObjectID: "project-1",
				Resolution: ResolutionResolved, Status: AssertionSuggested, Surface: "Orrery", Confidence: 0.85,
				Evidence: Evidence{Anchor: anchor, ExtractionMethod: "deterministic_exact_entity_match"},
			}},
			Assertions: []AssertionProjection{{
				LinkType: "project_uses_tool", From: ObjectRef{Kind: "project", ID: "project-1"},
				To: ObjectRef{Kind: "tool", ID: "tool-1"}, FromObjectID: "project-1", ToObjectID: "tool-1",
				Status: AssertionConfirmed, Evidence: Evidence{Anchor: "claim", ExtractionMethod: "explicit_frontmatter"},
			}},
		}
	}
	for revision, anchor := range []string{"stale", "current"} {
		if err := store.SaveProjectionAndCursor(ctx, binding, mention(revision+1, anchor), ""); err != nil {
			t.Fatalf("SaveProjectionAndCursor: %v", err)
		}
	}

	associations, err := store.ListObjectAssociations(ctx, "tenant-1", []contextgraph.Ref{{Kind: "project", ID: "project-1"}}, 10)
	if err != nil {
		t.Fatalf("ListObjectAssociations: %v", err)
	}
	if len(associations) != 2 {
		t.Fatalf("associations = %+v", associations)
	}
	for _, association := range associations {
		if association.Revision != 2 || association.KnowledgeBaseID != "kb-1" || association.DocumentID != "provider-doc" {
			t.Fatalf("association = %+v", association)
		}
	}
	if associations[0].Kind != AssociationMention || associations[0].Anchor != "current" {
		t.Fatalf("mention association = %+v", associations[0])
	}
}
