package contextgraph

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestAccessRefBackfillReportsThenAppliesAuditedDeterministicRefs(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "contextgraph-access-ref-backfill"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner(contextGraphCompleteStoreTestMigrations()).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	graph := NewPlatformStore(store)
	provenance := Ref{Kind: "event", ID: "event-1"}
	observedAt := time.Unix(10, 0).UTC()
	for _, object := range []Object{
		{ID: "object-1", TenantID: "tenant-1", ObjectType: "task", CanonicalKey: "do-not-infer:owner-1", Label: "Do not infer", Provenance: provenance, UpdatedAt: observedAt},
		{ID: "object-2", TenantID: "tenant-1", ObjectType: "task", CanonicalKey: "do-not-infer:owner-2", Label: "Do not infer", Provenance: provenance, UpdatedAt: observedAt},
	} {
		if err := graph.SaveObject(ctx, object); err != nil {
			t.Fatalf("save object: %v", err)
		}
	}
	if err := graph.SaveLink(ctx, Link{ID: "link-1", TenantID: "tenant-1", LinkType: "depends_on", FromObjectID: "object-1", ToObjectID: "object-2", Provenance: provenance, UpdatedAt: observedAt}); err != nil {
		t.Fatalf("save link: %v", err)
	}
	if err := graph.SaveFact(ctx, Fact{ID: "fact-1", TenantID: "tenant-1", Subject: Ref{Kind: "task", ID: "object-1"}, Predicate: "status", Value: "active", Provenance: provenance, UpdatedAt: observedAt}); err != nil {
		t.Fatalf("save fact: %v", err)
	}
	if err := graph.SaveEvidence(ctx, Evidence{ID: "evidence-1", TenantID: "tenant-1", SourceRef: Ref{Kind: "task.updated", ID: "event-1"}, ObservedAt: observedAt}); err != nil {
		t.Fatalf("save evidence: %v", err)
	}
	if err := graph.SaveProjectionAudit(ctx, ProjectionAudit{ID: "audit-1", TenantID: "tenant-1", SourceEventID: "event-1", SourceEventType: "task.updated", Status: "skipped", ObservedAt: observedAt}); err != nil {
		t.Fatalf("save audit: %v", err)
	}
	if err := graph.SaveProjectionAuditReplayHistory(ctx, ProjectionAuditReplayHistory{ID: "history-1", AuditID: "audit-1", TenantID: "tenant-1", SourceEventID: "event-1", SourceEventType: "task.updated", Status: "failed", AttemptedAt: observedAt}); err != nil {
		t.Fatalf("save replay history: %v", err)
	}

	wantRef := AccessRef{Kind: "mission", ID: "mission-1", TenantID: "mission:mission-1"}
	resolver := AccessRefBackfillResolverFunc(func(_ context.Context, candidate AccessRefBackfillCandidate) (AccessRef, bool, error) {
		if candidate.SourceID != "event-1" {
			t.Fatalf("candidate source = %+v", candidate)
		}
		return wantRef, true, nil
	})
	report, err := BackfillAccessRefs(ctx, store, resolver, AccessRefBackfillOptions{Limit: 100})
	if err != nil {
		t.Fatalf("report-only backfill: %v", err)
	}
	if report.Apply || report.Scanned != 8 || report.Resolved != 8 || report.Updated != 0 || report.Unresolved != 0 || report.HasMore {
		t.Fatalf("report-only result = %+v", report)
	}
	var unresolved int
	if err := store.QueryRow(ctx, `SELECT COUNT(*) FROM context_graph_objects WHERE access_state = 'unresolved'`).Scan(&unresolved); err != nil {
		t.Fatalf("count unresolved objects: %v", err)
	}
	if unresolved != 2 {
		t.Fatalf("report-only changed %d unresolved objects", 2-unresolved)
	}

	report, err = BackfillAccessRefs(ctx, store, resolver, AccessRefBackfillOptions{
		Apply: true, Limit: 100, RunID: "run-1", ChangedBy: "operator-1", Now: func() time.Time { return time.Unix(20, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("apply backfill: %v", err)
	}
	if !report.Apply || report.RunID != "run-1" || report.Scanned != 8 || report.Resolved != 8 || report.Updated != 8 || report.Unresolved != 0 {
		t.Fatalf("apply result = %+v", report)
	}
	var audits int
	if err := store.QueryRow(ctx, `SELECT COUNT(*) FROM context_graph_access_ref_backfill_audit WHERE run_id = ?`, "run-1").Scan(&audits); err != nil {
		t.Fatalf("count backfill audits: %v", err)
	}
	if audits != 8 {
		t.Fatalf("backfill audit count = %d, want 8", audits)
	}
	objects, links, facts, evidence, err := graph.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if objects[0].OwnerAccessRef != wantRef || objects[1].OwnerAccessRef != wantRef || links[0].SourceAccessRef != wantRef || facts[0].SourceAccessRef != wantRef || evidence[0].SourceAccessRef != wantRef {
		t.Fatalf("backfilled snapshot access refs objects=%+v links=%+v facts=%+v evidence=%+v", objects, links, facts, evidence)
	}
}

func TestAccessRefBackfillUsesBoundedCheckpointedPages(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "contextgraph-access-ref-backfill-page"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner(contextGraphCompleteStoreTestMigrations()).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	graph := NewPlatformStore(store)
	for _, id := range []string{"a", "b", "c"} {
		if err := graph.SaveObject(ctx, Object{ID: id, TenantID: "tenant-1", ObjectType: "task", CanonicalKey: "task:" + id, Provenance: Ref{Kind: "event", ID: "event-" + id}}); err != nil {
			t.Fatalf("save object %s: %v", id, err)
		}
	}
	resolver := AccessRefBackfillResolverFunc(func(_ context.Context, candidate AccessRefBackfillCandidate) (AccessRef, bool, error) {
		return AccessRef{Kind: "mission", ID: candidate.SourceID, TenantID: "mission:1"}, true, nil
	})
	first, err := BackfillAccessRefs(ctx, store, resolver, AccessRefBackfillOptions{Limit: 2})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if first.Scanned != 2 || !first.HasMore || first.Next.ResourceKind != AccessRefResourceObject || first.Next.ResourceID != "b" {
		t.Fatalf("first page = %+v", first)
	}
	second, err := BackfillAccessRefs(ctx, store, resolver, AccessRefBackfillOptions{Limit: 2, After: first.Next})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if second.Scanned != 1 || second.HasMore {
		t.Fatalf("second page = %+v", second)
	}
	if _, err := BackfillAccessRefs(ctx, store, resolver, AccessRefBackfillOptions{Limit: 501}); err == nil {
		t.Fatal("limit 501 error = nil")
	}
}

func TestAccessRefBackfillLeavesIncompleteResolverDecisionsUnresolved(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "contextgraph-access-ref-backfill-incomplete"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner(contextGraphCompleteStoreTestMigrations()).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	if err := NewPlatformStore(store).SaveObject(ctx, Object{ID: "object-1", TenantID: "tenant-1", ObjectType: "task", CanonicalKey: "task:1"}); err != nil {
		t.Fatalf("save object: %v", err)
	}
	resolver := AccessRefBackfillResolverFunc(func(context.Context, AccessRefBackfillCandidate) (AccessRef, bool, error) {
		return AccessRef{Kind: "mission", ID: "mission-1"}, true, nil
	})
	report, err := BackfillAccessRefs(ctx, store, resolver, AccessRefBackfillOptions{Apply: true, Limit: 10, RunID: "run-1", ChangedBy: "operator-1"})
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if report.Unresolved != 1 || report.Updated != 0 {
		t.Fatalf("report = %+v", report)
	}
}

func TestAccessRefBackfillDoesNotApplyAfterSourceMetadataChanges(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "contextgraph-access-ref-backfill-source-race"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner(contextGraphCompleteStoreTestMigrations()).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	graph := NewPlatformStore(store)
	if err := graph.SaveObject(ctx, Object{
		ID: "object-1", TenantID: "tenant-1", ObjectType: "task", CanonicalKey: "task:1",
		Provenance: Ref{Kind: "event", ID: "event-1"},
	}); err != nil {
		t.Fatalf("save object: %v", err)
	}
	resolver := AccessRefBackfillResolverFunc(func(context.Context, AccessRefBackfillCandidate) (AccessRef, bool, error) {
		changed, err := json.Marshal(Ref{Kind: "event", ID: "event-2"})
		if err != nil {
			t.Fatalf("marshal changed provenance: %v", err)
		}
		if _, err := store.Exec(ctx, `UPDATE context_graph_objects SET provenance_json = ? WHERE tenant_id = ? AND id = ?`, string(changed), "tenant-1", "object-1"); err != nil {
			t.Fatalf("change provenance: %v", err)
		}
		return AccessRef{Kind: "mission", ID: "mission-1", TenantID: "mission:mission-1"}, true, nil
	})
	report, err := BackfillAccessRefs(ctx, store, resolver, AccessRefBackfillOptions{
		Apply: true, Limit: 10, RunID: "run-1", ChangedBy: "operator-1",
	})
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if report.Resolved != 1 || report.Updated != 0 {
		t.Fatalf("report = %+v", report)
	}
	var accessState string
	if err := store.QueryRow(ctx, `SELECT access_state FROM context_graph_objects WHERE tenant_id = ? AND id = ?`, "tenant-1", "object-1").Scan(&accessState); err != nil {
		t.Fatalf("read access state: %v", err)
	}
	if accessState != string(AccessStateUnresolved) {
		t.Fatalf("access state = %q, want unresolved", accessState)
	}
}
