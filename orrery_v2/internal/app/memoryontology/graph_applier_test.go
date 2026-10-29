package memoryontology

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	memcore "orrery_v2/internal/memory/core"
)

func TestGraphApplierWritesConfirmedEvidenceBackedLinkAndReconcilesStaleLink(t *testing.T) {
	ctx := context.Background()
	registry := ontology.NewMemoryRegistry()
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("install ontology: %v", err)
	}
	graph := contextgraph.NewMemoryStore()
	access := contextgraph.AccessRef{Kind: "organization", ID: "tenant-1", TenantID: "tenant-1"}
	for _, object := range []contextgraph.Object{
		{ID: "memory-1", TenantID: "tenant-1", ObjectType: "memory_record", Label: "Memory", OwnerAccessRef: access},
		{ID: "project-1", TenantID: "tenant-1", ObjectType: "project", Label: "Orrery", OwnerAccessRef: access},
	} {
		if err := graph.SaveObject(ctx, object); err != nil {
			t.Fatalf("save object: %v", err)
		}
	}
	objects, links, _, _, _ := graph.Snapshot(ctx, "tenant-1")
	projections := ProjectMentions("tenant-1", Extraction{Mentions: []Mention{{
		Surface: "Orrery", ObjectType: "project", Confidence: 0.98, Anchor: "summary",
	}}}, objects)
	now := time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC)
	applier := NewGraphApplier(graph, registry, func() time.Time { return now })
	record := memcore.MemoryRecord{ID: "memory-1", UpdatedAt: now}
	linked, err := applier.Apply(ctx, "tenant-1", "hash-1", record, projections, objects, links)
	if err != nil || linked != 1 {
		t.Fatalf("Apply = %d, %v", linked, err)
	}
	_, links, _, evidence, err := graph.Snapshot(ctx, "tenant-1")
	if err != nil || len(links) != 1 || len(evidence) != 1 {
		t.Fatalf("snapshot links=%+v evidence=%+v err=%v", links, evidence, err)
	}
	if links[0].LinkType != "memory_mentions_project" || links[0].Status != "confirmed" || links[0].Confidence != 0.98 {
		t.Fatalf("link = %+v", links[0])
	}
	if links[0].SourceAccessRef != access || len(links[0].EvidenceRefs) != 1 {
		t.Fatalf("link access/evidence = %+v", links[0])
	}

	if _, err := applier.Apply(ctx, "tenant-1", "hash-2", record, nil, objects, links); err != nil {
		t.Fatalf("reconcile stale link: %v", err)
	}
	_, links, _, _, _ = graph.Snapshot(ctx, "tenant-1")
	if len(links) != 0 {
		t.Fatalf("stale links = %+v", links)
	}
}

func TestGraphApplierMaterializesResolvedReferenceBeforeLink(t *testing.T) {
	ctx := context.Background()
	registry := ontology.NewMemoryRegistry()
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatal(err)
	}
	graph := contextgraph.NewMemoryStore()
	if err := graph.SaveObject(ctx, contextgraph.Object{ID: "memory-1", TenantID: "agent:A000001", ObjectType: "memory_record"}); err != nil {
		t.Fatal(err)
	}
	access := contextgraph.AccessRef{Kind: "organization", ID: "minvia", TenantID: "minvia"}
	objects := []contextgraph.Object{
		{ID: "memory-1", TenantID: "agent:A000001", ObjectType: "memory_record"},
		{ID: "person:H000001", TenantID: "agent:A000001", ObjectType: "person", Label: "丁皮", OwnerAccessRef: access},
	}
	projections := ProjectMentions("agent:A000001", Extraction{Mentions: []Mention{{
		Surface: "丁皮", ObjectType: "person", Confidence: 0.99,
	}}}, objects)
	now := time.Date(2026, 8, 26, 15, 0, 0, 0, time.UTC)
	linked, err := NewGraphApplier(graph, registry, func() time.Time { return now }).Apply(
		ctx, "agent:A000001", "hash", memcore.MemoryRecord{ID: "memory-1", UpdatedAt: now}, projections, objects, nil,
	)
	if err != nil || linked != 1 {
		t.Fatalf("Apply = %d, %v", linked, err)
	}
	stored, links, _, _, _ := graph.Snapshot(ctx, "agent:A000001")
	if len(stored) != 2 || len(links) != 1 {
		t.Fatalf("objects = %+v, links = %+v", stored, links)
	}
}
