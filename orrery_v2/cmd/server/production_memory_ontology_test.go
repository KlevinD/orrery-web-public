package main

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/memoryadmin"
	"orrery_v2/internal/app/ontology"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
	memtestkit "orrery_v2/internal/memory/testkit"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestProductionMemoryAcceptedRecordWritesOntologyContextGraph(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-memory-ontology"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	defer databases.Close()
	core, ok := databases.Get(db.RoleCore)
	if !ok || core == nil {
		t.Fatalf("core database missing")
	}
	coordinator := migration.NewCoordinator(databases)
	if err := ontology.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register ontology migrations: %v", err)
	}
	if err := contextgraph.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register context graph migrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migrate core db: %v", err)
	}
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, ontology.NewPlatformRegistry(core)); err != nil {
		t.Fatalf("install default ontology: %v", err)
	}

	now := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	memoryStore := memstore.NewPlatformStore(core)
	if err := memoryStore.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            "candidate-production-1",
		MissionID:     "mission-1",
		SourceType:    memcore.SourceEvidence,
		SourceRef:     "evidence-1",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		ProposedKind:  agentcore.MemoryKindMission,
		ProposedLayer: memcore.MemoryLayerFact,
		Topic:         "qa",
		Summary:       "QA passed in production wiring.",
		EvidenceRefs:  []string{"evidence:evidence-1"},
		LineageRefs:   []string{"qa_result:qa-1"},
		Confidence:    0.92,
		Status:        memcore.CandidateProposed,
		CreatedBy:     agentcore.AgentID("agent-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("save candidate: %v", err)
	}
	sink, err := newProductionOntologyMemoryRecordMetadataSink(ctx, core)
	if err != nil {
		t.Fatalf("new production ontology memory sink: %v", err)
	}
	memoryAdmin := memoryadmin.NewService(memoryStore, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now }).
		WithMemoryRecordMetadataSink(sink)

	record, err := memoryAdmin.AcceptCandidate(ctx, memoryadmin.ViewerRef{
		ViewerID:        "reviewer-1",
		PermissionScope: []string{memoryadmin.PermissionMemoryReview},
		MissionIDs:      []string{"mission-1"},
	}, "candidate-production-1", memoryadmin.AcceptCandidateRequest{
		ProfileID: "memory-profile-1",
		MemoryID:  "memory-record-production-1",
	})
	if err != nil {
		t.Fatalf("AcceptCandidate: %v", err)
	}

	objects, _, facts, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "mission:mission-1")
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	if len(objects) != 1 || objects[0].ObjectType != "memory_record" || objects[0].ID != record.MemoryID {
		t.Fatalf("objects = %+v", objects)
	}
	if len(facts) < 8 {
		t.Fatalf("facts = %+v", facts)
	}
	if len(evidence) != 1 {
		t.Fatalf("evidence = %+v", evidence)
	}
	events, err := newProductionOntologyEventLog(core).List(ctx)
	if err != nil {
		t.Fatalf("list ontology events: %v", err)
	}
	assertOntologyEvent(t, events, "memory-record-accepted:"+record.MemoryID, "memory.record.accepted")
}
