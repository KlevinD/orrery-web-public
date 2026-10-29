package main

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/memoryontology"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform/graphprotocol"
)

type recordingMemoryOntologyMetadataSink struct {
	events []graphprotocol.DomainEvent
}

type fixedMemoryOntologySourceReader struct {
	records []memcore.MemoryRecord
}

func (r fixedMemoryOntologySourceReader) FindRecords(context.Context, memstore.RecordQuery) ([]memcore.MemoryRecord, error) {
	return append([]memcore.MemoryRecord(nil), r.records...), nil
}

func (s *recordingMemoryOntologyMetadataSink) PublishMemoryRecordMetadata(_ context.Context, event graphprotocol.DomainEvent) error {
	s.events = append(s.events, event)
	return nil
}

func TestReconcileMemoryOntologySourceObjectsProjectsEveryReadableRecord(t *testing.T) {
	sink := &recordingMemoryOntologyMetadataSink{}
	graph := contextgraph.NewMemoryStore()
	projector := productionMaterializedMemoryMetadataSink{sink: sink, graph: graph}
	records := []memcore.MemoryRecord{
		{ID: "memory-1", Scope: memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "agent-1"}, Summary: "First"},
		{ID: "memory-2", Scope: memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "agent-1"}, Summary: "Second"},
	}

	report, err := reconcileMemoryOntologySourceObjects(context.Background(), fixedMemoryOntologySourceReader{records: records}, projector)
	if err != nil || report.Scanned != 2 || report.Projected != 2 || report.Failed != 0 {
		t.Fatalf("report = %+v, err = %v", report, err)
	}
	objects, _, _, _, err := graph.Snapshot(context.Background(), "agent:agent-1")
	if err != nil || len(objects) != 2 {
		t.Fatalf("objects = %+v, err = %v", objects, err)
	}
}

func TestCurrentMemoryOntologyRunKeysUseContentAndEntityDictionary(t *testing.T) {
	ctx := context.Background()
	graph := contextgraph.NewMemoryStore()
	records := []memcore.MemoryRecord{
		{ID: "memory-1", Scope: memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "agent-1"}, Summary: "First"},
		{ID: "memory-2", Scope: memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "agent-1"}, Summary: "Second"},
	}
	if err := graph.SaveObject(ctx, contextgraph.Object{
		TenantID: "agent:agent-1", ID: "project-1", ObjectType: "project", Label: "Orrery",
	}); err != nil {
		t.Fatal(err)
	}
	keys, err := currentMemoryOntologyRunKeys(ctx, fixedMemoryOntologySourceReader{records: records}, graph)
	if err != nil || len(keys) != 2 {
		t.Fatalf("keys = %+v, err = %v", keys, err)
	}
	objects, _, _, _, err := graph.Snapshot(ctx, "agent:agent-1")
	if err != nil {
		t.Fatal(err)
	}
	wantVersion := memoryontology.ExtractorVersion + ":" + memoryontology.EntityDictionaryVersion(objects)
	for index, key := range keys {
		if key.TenantID != "agent:agent-1" || key.MemoryID != records[index].ID || key.ContentHash != memoryontology.ContentHash(records[index]) || key.ExtractorVersion != wantVersion {
			t.Fatalf("key[%d] = %+v", index, key)
		}
	}
}

func TestProductionMaterializedMemoryMetadataUsesAcceptedEventContract(t *testing.T) {
	sink := &recordingMemoryOntologyMetadataSink{}
	graph := contextgraph.NewMemoryStore()
	projector := productionMaterializedMemoryMetadataSink{sink: sink, graph: graph}
	record := memcore.MemoryRecord{
		ID:        "memory.record.agent-1",
		Scope:     memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "agent-1"},
		Summary:   "The user prefers autonomous retrieval.",
		UpdatedAt: time.Date(2026, 8, 22, 19, 0, 0, 0, time.UTC),
	}

	if err := projector.PublishMaterializedMemoryRecord(context.Background(), record); err != nil {
		t.Fatalf("PublishMaterializedMemoryRecord: %v", err)
	}
	if len(sink.events) != 1 {
		t.Fatalf("events = %+v", sink.events)
	}
	event := sink.events[0]
	if event.ID != "memory-record-accepted:"+record.ID || event.Type != "memory.record.accepted" || event.TenantID != "agent:agent-1" {
		t.Fatalf("event = %+v", event)
	}
	objects, _, _, _, err := graph.Snapshot(context.Background(), event.TenantID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(objects) != 1 || objects[0].ID != record.ID || objects[0].ObjectType != "memory_record" || objects[0].OwnerAccessRef.ID != record.ID {
		t.Fatalf("objects = %+v", objects)
	}
}

func TestProductionMaterializedMemoryMetadataSkipsPersonalAssistantRecords(t *testing.T) {
	assistantID := "A009994"
	if err := agentcore.RegisterPersonalAssistantProfileID("tenant-a", "owner-materialized-memory", assistantID); err != nil {
		t.Fatal(err)
	}
	sink := &recordingMemoryOntologyMetadataSink{}
	graph := contextgraph.NewMemoryStore()
	projector := productionMaterializedMemoryMetadataSink{sink: sink, graph: graph}
	record := memcore.MemoryRecord{
		ID: "memory.private", ProfileID: agentcore.MemoryProfileID(discussionsession.PersonalAssistantMemoryProfileID(assistantID)),
		Scope: memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: assistantID}, Summary: "Private preference",
	}

	if err := projector.PublishMaterializedMemoryRecord(context.Background(), record); err != nil {
		t.Fatalf("PublishMaterializedMemoryRecord: %v", err)
	}
	if len(sink.events) != 0 {
		t.Fatalf("personal assistant metadata events = %+v", sink.events)
	}
	objects, _, _, _, err := graph.Snapshot(context.Background(), "agent:"+assistantID)
	if err != nil || len(objects) != 0 {
		t.Fatalf("personal assistant ontology objects = %+v, err = %v", objects, err)
	}
}
