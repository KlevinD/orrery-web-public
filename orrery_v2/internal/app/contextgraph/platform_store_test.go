package contextgraph

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
	"orrery_v2/internal/platform/migration"
)

func TestPlatformStorePersistsProjectionResultAndSnapshotsByTenant(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "contextgraph-platform-store"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner(contextGraphStoreTestMigrations()).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	graphStore := NewPlatformStore(store)

	if err := ApplyProjectionResult(ctx, graphStore, platformStoreProjectionResult("tenant-1")); err != nil {
		t.Fatalf("ApplyProjectionResult tenant-1: %v", err)
	}
	if err := ApplyProjectionResult(ctx, graphStore, platformStoreProjectionResult("tenant-2")); err != nil {
		t.Fatalf("ApplyProjectionResult tenant-2: %v", err)
	}

	reopened := NewPlatformStore(store)
	objects, links, facts, evidence, err := reopened.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(objects) != 2 || len(links) != 1 || len(facts) != 1 || len(evidence) != 1 {
		t.Fatalf("tenant-1 snapshot objects=%d links=%d facts=%d evidence=%d", len(objects), len(links), len(facts), len(evidence))
	}
	if objects[0].TenantID != "tenant-1" || objects[1].TenantID != "tenant-1" {
		t.Fatalf("snapshot leaked objects across tenants: %#v", objects)
	}
	if facts[0].Value != "active" || facts[0].EvidenceRefs[0].ID != "event-tenant-1" {
		t.Fatalf("fact was not restored from SQL: %#v", facts[0])
	}
}

func TestPlatformStoreUpsertsObjectsByPrimaryID(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "contextgraph-platform-upsert"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner(contextGraphStoreTestMigrations()).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	graphStore := NewPlatformStore(store)
	object := Object{
		ID:           "mission-1",
		TenantID:     "tenant-1",
		ObjectType:   "mission",
		CanonicalKey: "mission:1",
		Label:        "First label",
		UpdatedAt:    time.Unix(1, 0).UTC(),
	}
	if err := graphStore.SaveObject(ctx, object); err != nil {
		t.Fatalf("SaveObject first: %v", err)
	}
	object.Label = "Updated label"
	object.Properties = map[string]any{"status": "active"}
	if err := graphStore.SaveObject(ctx, object); err != nil {
		t.Fatalf("SaveObject update: %v", err)
	}

	objects, _, _, _, err := graphStore.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(objects) != 1 || objects[0].Label != "Updated label" || objects[0].Properties["status"] != "active" {
		t.Fatalf("upserted objects = %#v", objects)
	}
}

func TestPlatformStoreDeletesLinkOnlyForOwningSourceStream(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "contextgraph-delete-link-stream"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner(contextGraphStoreTestMigrations()).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	graphStore := NewPlatformStore(store)
	for _, object := range []Object{
		{ID: "memory-1", TenantID: "tenant-1", ObjectType: "memory_record"},
		{ID: "project-1", TenantID: "tenant-1", ObjectType: "project"},
	} {
		if err := graphStore.SaveObject(ctx, object); err != nil {
			t.Fatalf("SaveObject: %v", err)
		}
	}
	link := Link{
		ID: "link-1", TenantID: "tenant-1", LinkType: "memory_mentions_project",
		FromObjectID: "memory-1", ToObjectID: "project-1", SourceStreamID: "memory_ontology:memory-1",
	}
	if err := graphStore.SaveLink(ctx, link); err != nil {
		t.Fatalf("SaveLink: %v", err)
	}
	if err := graphStore.DeleteLink(ctx, link.TenantID, link.ID, "another-stream"); err != nil {
		t.Fatalf("DeleteLink other stream: %v", err)
	}
	_, links, _, _, err := graphStore.Snapshot(ctx, link.TenantID)
	if err != nil || len(links) != 1 {
		t.Fatalf("other stream deleted link: %+v, %v", links, err)
	}
	if err := graphStore.DeleteLink(ctx, link.TenantID, link.ID, link.SourceStreamID); err != nil {
		t.Fatalf("DeleteLink owning stream: %v", err)
	}
	_, links, _, _, err = graphStore.Snapshot(ctx, link.TenantID)
	if err != nil || len(links) != 0 {
		t.Fatalf("owning stream did not delete link: %+v, %v", links, err)
	}
}

func TestPlatformStoreDeletesEvidenceOnlyForOwningSourceRef(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "contextgraph-delete-evidence-source"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner(contextGraphStoreTestMigrations()).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	graphStore := NewPlatformStore(store)
	evidence := Evidence{
		ID: "evidence-1", TenantID: "tenant-1",
		SourceRef: Ref{Kind: "knowledge_revision", ID: "doc-1@1"}, ObservedAt: time.Unix(1, 0).UTC(),
	}
	if err := graphStore.SaveEvidence(ctx, evidence); err != nil {
		t.Fatalf("SaveEvidence: %v", err)
	}
	if err := graphStore.DeleteEvidence(ctx, evidence.TenantID, evidence.ID, Ref{Kind: "knowledge_revision", ID: "doc-2@1"}); err != nil {
		t.Fatalf("DeleteEvidence other source: %v", err)
	}
	_, _, _, items, err := graphStore.Snapshot(ctx, evidence.TenantID)
	if err != nil || len(items) != 1 {
		t.Fatalf("other source deleted evidence: %+v, %v", items, err)
	}
	if err := graphStore.DeleteEvidence(ctx, evidence.TenantID, evidence.ID, evidence.SourceRef); err != nil {
		t.Fatalf("DeleteEvidence owning source: %v", err)
	}
	_, _, _, items, err = graphStore.Snapshot(ctx, evidence.TenantID)
	if err != nil || len(items) != 0 {
		t.Fatalf("owning source did not delete evidence: %+v, %v", items, err)
	}
}

func TestPlatformStorePreservesResolvedAccessRefAndRejectsRebinding(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "contextgraph-access-ref-rebind"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner(contextGraphStoreTestMigrations()).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	graphStore := NewPlatformStore(store)
	object := Object{
		ID: "task-1", TenantID: "mission:1", ObjectType: "task", CanonicalKey: "task:1",
		OwnerAccessRef: AccessRef{Kind: "mission", ID: "1", TenantID: "org-1"}, UpdatedAt: time.Unix(1, 0).UTC(),
	}
	if err := graphStore.SaveObject(ctx, object); err != nil {
		t.Fatalf("SaveObject resolved: %v", err)
	}
	withoutRef := object
	withoutRef.OwnerAccessRef = AccessRef{}
	withoutRef.UpdatedAt = time.Unix(2, 0).UTC()
	if err := graphStore.SaveObject(ctx, withoutRef); err != nil {
		t.Fatalf("SaveObject without ref must preserve trusted metadata: %v", err)
	}
	rebound := object
	rebound.OwnerAccessRef = AccessRef{Kind: "mission", ID: "other", TenantID: "org-1"}
	rebound.UpdatedAt = time.Unix(3, 0).UTC()
	if err := graphStore.SaveObject(ctx, rebound); !errors.Is(err, ErrAccessRefConflict) {
		t.Fatalf("SaveObject rebound error = %v", err)
	}
	objects, _, _, _, err := graphStore.Snapshot(ctx, object.TenantID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(objects) != 1 || !sameAccessRef(objects[0].OwnerAccessRef, object.OwnerAccessRef) || objects[0].AccessState != AccessStateResolved {
		t.Fatalf("stored access metadata = %#v", objects)
	}
}

func TestPlatformStoreMarksIncompleteAccessRefUnresolved(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "contextgraph-access-ref-unresolved"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner(contextGraphStoreTestMigrations()).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	graphStore := NewPlatformStore(store)
	if err := graphStore.SaveObject(ctx, Object{
		ID: "legacy", TenantID: "tenant-1", ObjectType: "legacy", CanonicalKey: "legacy:1",
		OwnerAccessRef: AccessRef{Kind: "mission", ID: "1"}, UpdatedAt: time.Unix(1, 0).UTC(),
	}); err != nil {
		t.Fatalf("SaveObject: %v", err)
	}
	objects, _, _, _, err := graphStore.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(objects) != 1 || objects[0].AccessState != AccessStateUnresolved || objects[0].OwnerAccessRef != (AccessRef{}) {
		t.Fatalf("unresolved object = %#v", objects)
	}
}

func TestPlatformStoreOrdersSameStreamFactsByEventVersion(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "contextgraph-fact-event-version"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner(contextGraphStoreTestMigrations()).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	graphStore := NewPlatformStore(store)
	newer := Fact{
		ID: "task:task-1:status", TenantID: "tenant-1", Subject: Ref{Kind: "task", ID: "task-1"}, Predicate: "status", Value: "completed",
		Provenance: Ref{Kind: "event", ID: "event-v2"}, SourceStreamID: "task:task-1", SourceVersion: 2, UpdatedAt: time.Unix(10, 0).UTC(),
	}
	if err := graphStore.SaveFact(ctx, newer); err != nil {
		t.Fatalf("SaveFact newer: %v", err)
	}
	olderVersion := newer
	olderVersion.Value = "started"
	olderVersion.Provenance.ID = "event-v1"
	olderVersion.SourceVersion = 1
	olderVersion.UpdatedAt = time.Unix(20, 0).UTC()
	if err := graphStore.SaveFact(ctx, olderVersion); err != nil {
		t.Fatalf("SaveFact older version: %v", err)
	}
	_, _, facts, _, err := graphStore.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(facts) != 1 || facts[0].Value != "completed" || facts[0].SourceVersion != 2 {
		t.Fatalf("current fact = %+v", facts)
	}
	var assertions int
	if err := store.QueryRow(ctx, `SELECT COUNT(*) FROM context_graph_fact_assertions WHERE tenant_id = ? AND fact_id = ?`, "tenant-1", newer.ID).Scan(&assertions); err != nil {
		t.Fatalf("count assertions: %v", err)
	}
	if assertions != 1 {
		t.Fatalf("fact assertion count = %d, want 1", assertions)
	}
}

func TestPlatformStoreUpsertsStaticFactClaimByStableSource(t *testing.T) {
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
	fact := Fact{
		ID: "task:task-1:status", TenantID: "tenant-1", Subject: Ref{Kind: "task", ID: "task-1"}, Predicate: "status", Value: "active",
		Provenance: Ref{Kind: "event", ID: "event-v1"}, SourceStreamID: "task:task-1", SourceVersion: 1, UpdatedAt: time.Unix(1, 0).UTC(),
	}
	if err := graphStore.SaveFact(ctx, fact); err != nil {
		t.Fatalf("SaveFact first: %v", err)
	}

	replay := fact
	replay.Value = "must-not-overwrite"
	replay.Provenance.ID = "event-v1-replay"
	replay.UpdatedAt = time.Unix(2, 0).UTC()
	if err := graphStore.SaveFact(ctx, replay); err != nil {
		t.Fatalf("SaveFact replay: %v", err)
	}
	assertStaticFactClaim(t, store, fact.ID, fact.SourceStreamID, 1, fact.UpdatedAt, "active", 1)

	newer := fact
	newer.Value = "completed"
	newer.Provenance.ID = "event-v2"
	newer.SourceVersion = 2
	newer.UpdatedAt = time.Unix(3, 0).UTC()
	if err := graphStore.SaveFact(ctx, newer); err != nil {
		t.Fatalf("SaveFact newer: %v", err)
	}
	assertStaticFactClaim(t, store, fact.ID, fact.SourceStreamID, 2, newer.UpdatedAt, "completed", 1)

	stale := fact
	stale.Value = "stale"
	stale.Provenance.ID = "event-stale"
	stale.UpdatedAt = time.Unix(4, 0).UTC()
	if err := graphStore.SaveFact(ctx, stale); err != nil {
		t.Fatalf("SaveFact stale: %v", err)
	}
	assertStaticFactClaim(t, store, fact.ID, fact.SourceStreamID, 2, newer.UpdatedAt, "completed", 1)

	otherSource := newer
	otherSource.Value = "external"
	otherSource.SourceStreamID = "external:task-1"
	otherSource.SourceVersion = 1
	otherSource.UpdatedAt = time.Unix(5, 0).UTC()
	if err := graphStore.SaveFact(ctx, otherSource); err != nil {
		t.Fatalf("SaveFact other source: %v", err)
	}
	assertStaticFactClaim(t, store, fact.ID, fact.SourceStreamID, 2, newer.UpdatedAt, "completed", 2)
}

func TestPlatformStoreUsesProvenanceAsStaticFactClaimSource(t *testing.T) {
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
	fact := Fact{
		ID: "task:task-1:owner", TenantID: "tenant-1", Subject: Ref{Kind: "task", ID: "task-1"}, Predicate: "owner", Value: "person-1",
		Provenance: Ref{Kind: "source_record", ID: "record-1"}, UpdatedAt: time.Unix(1, 0).UTC(),
	}
	if err := graphStore.SaveFact(ctx, fact); err != nil {
		t.Fatalf("SaveFact first: %v", err)
	}
	fact.Value = "person-2"
	fact.UpdatedAt = time.Unix(2, 0).UTC()
	if err := graphStore.SaveFact(ctx, fact); err != nil {
		t.Fatalf("SaveFact update: %v", err)
	}
	assertStaticFactClaim(t, store, fact.ID, fact.SourceStreamID, 0, fact.UpdatedAt, "person-2", 1)
}

func TestPlatformStoreRejectsStaticFactWithoutStableClaimSource(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner(contextGraphStoreTestMigrations()).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	fact := Fact{
		ID: "fact-1", TenantID: "tenant-1", Subject: Ref{Kind: "task", ID: "task-1"}, Predicate: "status", Value: "active", UpdatedAt: time.Unix(1, 0).UTC(),
	}
	if err := NewPlatformStore(store).SaveFact(ctx, fact); err == nil {
		t.Fatal("SaveFact error = nil, want stable claim source error")
	}
	var facts, assertions int
	if err := store.QueryRow(ctx, "SELECT COUNT(*) FROM context_graph_facts").Scan(&facts); err != nil {
		t.Fatalf("count facts: %v", err)
	}
	if err := store.QueryRow(ctx, "SELECT COUNT(*) FROM context_graph_fact_assertions").Scan(&assertions); err != nil {
		t.Fatalf("count assertions: %v", err)
	}
	if facts != 0 || assertions != 0 {
		t.Fatalf("failed static claim wrote facts=%d assertions=%d", facts, assertions)
	}
}

func TestStaticFactClaimIdentityIncludesAccessReference(t *testing.T) {
	fact := Fact{
		ID: "fact-1", TenantID: "tenant-1", SourceStreamID: "stream-1",
		SourceAccessRef: AccessRef{Kind: "mission", ID: "mission-1", TenantID: "tenant-1"},
	}
	first, err := staticFactClaimIdentity(fact)
	if err != nil {
		t.Fatalf("first identity: %v", err)
	}
	fact.SourceAccessRef.ID = "mission-2"
	second, err := staticFactClaimIdentity(fact)
	if err != nil {
		t.Fatalf("second identity: %v", err)
	}
	if first == second {
		t.Fatal("different access references produced the same static claim identity")
	}
}

func assertStaticFactClaim(t *testing.T, store *db.Store, factID, sourceStreamID string, wantVersion int64, wantObservedAt time.Time, wantValue string, wantCount int) {
	t.Helper()
	var count int
	if err := store.QueryRow(context.Background(), `SELECT COUNT(*) FROM context_graph_fact_assertions WHERE tenant_id = ? AND fact_id = ?`, "tenant-1", factID).Scan(&count); err != nil {
		t.Fatalf("count assertions: %v", err)
	}
	if count != wantCount {
		t.Fatalf("fact assertion count = %d, want %d", count, wantCount)
	}
	var sourceVersion, observedAt int64
	var valueJSON string
	if err := store.QueryRow(context.Background(), `
SELECT source_version, observed_at, value_json
FROM context_graph_fact_assertions
WHERE tenant_id = ? AND fact_id = ? AND source_stream_id = ?`,
		"tenant-1", factID, sourceStreamID,
	).Scan(&sourceVersion, &observedAt, &valueJSON); err != nil {
		t.Fatalf("read static claim: %v", err)
	}
	if sourceVersion != wantVersion || observedAt != timeToUnixNano(wantObservedAt) {
		t.Fatalf("static claim source_version=%d observed_at=%d, want %d/%d", sourceVersion, observedAt, wantVersion, timeToUnixNano(wantObservedAt))
	}
	var value string
	if err := json.Unmarshal([]byte(valueJSON), &value); err != nil {
		t.Fatalf("decode static claim value: %v", err)
	}
	if value != wantValue {
		t.Fatalf("static claim value = %q, want %q", value, wantValue)
	}
}

func TestPlatformStoreRollsBackFactAssertionWhenCurrentFactWriteFails(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "contextgraph-fact-atomic-write"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner(contextGraphStoreTestMigrations()).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	if _, err := store.Exec(ctx, `
CREATE TRIGGER reject_context_graph_fact
BEFORE INSERT ON context_graph_facts
BEGIN
	SELECT RAISE(ABORT, 'fact write rejected');
END`); err != nil {
		t.Fatalf("create rejection trigger: %v", err)
	}

	fact := Fact{
		ID: "fact-1", TenantID: "tenant-1", Subject: Ref{Kind: "task", ID: "task-1"}, Predicate: "status",
		Value: "active", SourceAccessRef: AccessRef{Kind: "mission", ID: "mission-1", TenantID: "tenant-1"},
		Provenance: Ref{Kind: "event", ID: "event-1"}, UpdatedAt: time.Unix(1, 0).UTC(),
	}
	if err := NewPlatformStore(store).SaveFact(ctx, fact); err == nil {
		t.Fatal("SaveFact error = nil, want trigger failure")
	}
	var assertions int
	if err := store.QueryRow(ctx, `SELECT COUNT(*) FROM context_graph_fact_assertions WHERE tenant_id = ?`, fact.TenantID).Scan(&assertions); err != nil {
		t.Fatalf("count assertions: %v", err)
	}
	if assertions != 0 {
		t.Fatalf("fact assertion count = %d, want transaction rollback", assertions)
	}
}

func TestPlatformStoreClearsProjectionByTenant(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "contextgraph-clear-tenant"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner(contextGraphCompleteStoreTestMigrations()).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	graphStore := NewPlatformStore(store)
	if err := ApplyProjectionResult(ctx, graphStore, platformStoreProjectionResult("tenant-1")); err != nil {
		t.Fatalf("ApplyProjectionResult tenant-1: %v", err)
	}
	if err := ApplyProjectionResult(ctx, graphStore, platformStoreProjectionResult("tenant-2")); err != nil {
		t.Fatalf("ApplyProjectionResult tenant-2: %v", err)
	}
	if err := graphStore.SaveProjectionAudit(ctx, ProjectionAudit{
		ID:              "audit-tenant-1",
		TenantID:        "tenant-1",
		SourceEventID:   "event-tenant-1",
		SourceEventType: "mission.task.updated",
		Status:          "skipped",
		ReasonCode:      "test",
		ObservedAt:      time.Unix(1, 0).UTC(),
	}); err != nil {
		t.Fatalf("SaveProjectionAudit tenant-1: %v", err)
	}

	removed, err := graphStore.ClearTenantProjection(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("ClearTenantProjection: %v", err)
	}
	if removed.Objects != 2 || removed.Links != 1 || removed.Facts != 1 || removed.Evidence != 1 || removed.ProjectionAudits != 0 {
		t.Fatalf("removed = %+v", removed)
	}
	objects, links, facts, evidence, err := graphStore.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Snapshot tenant-1: %v", err)
	}
	if len(objects) != 0 || len(links) != 0 || len(facts) != 0 || len(evidence) != 0 {
		t.Fatalf("tenant-1 projection remained objects=%d links=%d facts=%d evidence=%d", len(objects), len(links), len(facts), len(evidence))
	}
	objects, links, facts, evidence, err = graphStore.Snapshot(ctx, "tenant-2")
	if err != nil {
		t.Fatalf("Snapshot tenant-2: %v", err)
	}
	if len(objects) != 2 || len(links) != 1 || len(facts) != 1 || len(evidence) != 1 {
		t.Fatalf("tenant-2 projection was affected objects=%d links=%d facts=%d evidence=%d", len(objects), len(links), len(facts), len(evidence))
	}
	audit, err := graphStore.GetProjectionAudit(ctx, "tenant-1", "audit-tenant-1")
	if err != nil {
		t.Fatalf("GetProjectionAudit tenant-1: %v", err)
	}
	if audit.ID != "audit-tenant-1" || audit.Status != "skipped" {
		t.Fatalf("tenant-1 audit history was not preserved: %+v", audit)
	}
}

func TestPlatformStorePersistsProjectionAudit(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "contextgraph-projection-audit"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner(contextGraphCompleteStoreTestMigrations()).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	graphStore := NewPlatformStore(store)
	observedAt := time.Unix(10, 0).UTC()
	audit := ProjectionAudit{
		ID:              "projection-skipped:event-1:missing-scope-targets",
		TenantID:        "tenant-1",
		SourceEventID:   "event-1",
		SourceEventType: "memory.scope.linked",
		Status:          "skipped",
		ReasonCode:      "missing_scope_target",
		Reason:          "scope target object is not projected",
		Details: map[string]any{
			"dropped_targets": []any{
				map[string]any{"object_type": "mission", "object_id": "mission-1"},
			},
		},
		SourceAccessRef: AccessRef{Kind: "memory", ID: "memory-1", TenantID: "tenant-1"},
		ObservedAt:      observedAt,
	}

	if err := graphStore.SaveProjectionAudit(ctx, audit); err != nil {
		t.Fatalf("SaveProjectionAudit: %v", err)
	}
	got, err := graphStore.GetProjectionAudit(ctx, "tenant-1", audit.ID)
	if err != nil {
		t.Fatalf("GetProjectionAudit: %v", err)
	}
	if got.ID != audit.ID || got.SourceEventID != "event-1" || got.SourceEventType != "memory.scope.linked" || got.Status != "skipped" || got.ReasonCode != "missing_scope_target" {
		t.Fatalf("audit metadata = %#v", got)
	}
	if got.AccessState != AccessStateResolved || !sameAccessRef(got.SourceAccessRef, audit.SourceAccessRef) {
		t.Fatalf("audit access metadata = %#v", got)
	}
	if !got.ObservedAt.Equal(observedAt) {
		t.Fatalf("audit observed_at = %s, want %s", got.ObservedAt, observedAt)
	}
	targets, ok := got.Details["dropped_targets"].([]any)
	if !ok || len(targets) != 1 {
		t.Fatalf("audit details = %#v", got.Details)
	}
}

func TestPlatformStorePersistsProjectionAuditReplayHistory(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "contextgraph-projection-audit-replay-history"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner(contextGraphAuditStoreTestMigrations()).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	graphStore := NewPlatformStore(store)
	sourceAccessRef := AccessRef{Kind: "organization", ID: "tenant-1", TenantID: "tenant-1"}
	first := ProjectionAuditReplayHistory{
		ID:              "replay-history-audit-1-1",
		AuditID:         "audit-1",
		TenantID:        "tenant-1",
		SourceEventID:   "event-1",
		SourceEventType: "memory.scope.linked",
		Status:          "failed",
		Error:           "target still missing",
		Details:         map[string]any{"attempt": float64(1)},
		SourceAccessRef: sourceAccessRef,
		AttemptedAt:     time.Unix(10, 0).UTC(),
	}
	second := ProjectionAuditReplayHistory{
		ID:              "replay-history-audit-1-2",
		AuditID:         "audit-1",
		TenantID:        "tenant-1",
		SourceEventID:   "event-1",
		SourceEventType: "memory.scope.linked",
		Status:          "replayed",
		Details:         map[string]any{"attempt": float64(2)},
		SourceAccessRef: sourceAccessRef,
		AttemptedAt:     time.Unix(20, 0).UTC(),
	}
	if err := graphStore.SaveProjectionAuditReplayHistory(ctx, first); err != nil {
		t.Fatalf("SaveProjectionAuditReplayHistory first: %v", err)
	}
	if err := graphStore.SaveProjectionAuditReplayHistory(ctx, second); err != nil {
		t.Fatalf("SaveProjectionAuditReplayHistory second: %v", err)
	}
	if err := graphStore.SaveProjectionAuditReplayHistory(ctx, ProjectionAuditReplayHistory{
		ID:              "replay-history-other-tenant",
		AuditID:         "audit-1",
		TenantID:        "tenant-2",
		SourceEventID:   "event-1",
		SourceEventType: "memory.scope.linked",
		Status:          "failed",
		SourceAccessRef: AccessRef{Kind: "organization", ID: "tenant-2", TenantID: "tenant-2"},
		AttemptedAt:     time.Unix(30, 0).UTC(),
	}); err != nil {
		t.Fatalf("SaveProjectionAuditReplayHistory other tenant: %v", err)
	}
	page, err := graphStore.ListVisibleProjectionReplayHistory(ctx, ProjectionReplayHistoryCursorQuery{
		TenantID:   "tenant-1",
		AuditID:    "audit-1",
		Visibility: VisibilityConstraint{AllowedAccessRefs: []AccessRef{sourceAccessRef}},
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("ListVisibleProjectionReplayHistory: %v", err)
	}
	if len(page.Items) != 2 || page.HasMore || page.Items[0].ID != second.ID || page.Items[1].ID != first.ID {
		t.Fatalf("history order = %+v", page)
	}
	if page.Items[0].Status != "replayed" || page.Items[1].Error != "target still missing" {
		t.Fatalf("history values = %+v", page.Items)
	}
	if page.Items[1].Details["attempt"] != float64(1) {
		t.Fatalf("history details = %#v", page.Items[1].Details)
	}
}

func platformStoreProjectionResult(tenantID string) graphprotocol.ProjectionResult {
	provenance := graphprotocol.ProvenanceRef{
		SourceEventID:   "event-" + tenantID,
		SourceEventType: "mission.task.updated",
		SourceSystem:    "orrery",
		SourceAccessRef: graphprotocol.AccessRef{Kind: "mission", ID: "mission-" + tenantID, TenantID: tenantID},
		Actor:           graphprotocol.ActorRef{Kind: "service", ID: "mission"},
		ObservedAt:      time.Unix(30, 0).UTC(),
	}
	return graphprotocol.ProjectionResult{
		Cursor: graphprotocol.ProjectionCursor{SourceID: provenance.SourceEventID, SourceVersion: 1},
		Assertions: []graphprotocol.Assertion{
			graphprotocol.ObjectAssertion{
				Object:       graphprotocol.ObjectRef{TenantID: tenantID, Type: "mission", ID: "mission-" + tenantID},
				CanonicalKey: "mission:" + tenantID,
				Label:        "Launch",
				Provenance:   provenance,
			},
			graphprotocol.ObjectAssertion{
				Object:       graphprotocol.ObjectRef{TenantID: tenantID, Type: "task", ID: "task-" + tenantID},
				CanonicalKey: "task:" + tenantID,
				Label:        "Draft",
				Provenance:   provenance,
			},
			graphprotocol.LinkAssertion{
				LinkType:   "mission_has_task",
				From:       graphprotocol.ObjectRef{TenantID: tenantID, Type: "mission", ID: "mission-" + tenantID},
				To:         graphprotocol.ObjectRef{TenantID: tenantID, Type: "task", ID: "task-" + tenantID},
				Provenance: provenance,
			},
			graphprotocol.FactAssertion{
				Subject:    graphprotocol.ObjectRef{TenantID: tenantID, Type: "task", ID: "task-" + tenantID},
				Predicate:  "status",
				Value:      "active",
				Provenance: provenance,
			},
		},
	}
}
