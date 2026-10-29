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
	"orrery_v2/internal/platform/graphprotocol"
	"orrery_v2/internal/platform/migration"
)

func TestProductionMemoryKnowledgeCitationWritesOntologyContextGraph(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-memory-citation-ontology"},
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

	now := time.Date(2026, 6, 20, 9, 0, 0, 0, time.UTC)
	knowledgeRunner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatalf("new production ontology knowledge runner: %v", err)
	}
	if _, err := knowledgeRunner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "knowledge-document-metadata:kdoc-1",
		TenantID: "mission:mission-1",
		Type:     "knowledge.document.metadata.updated",
		StreamID: "kdoc-1",
		Version:  1,
		Payload: map[string]any{
			"id":                   "kdoc-1",
			"knowledge_base_id":    "kb-1",
			"title":                "release-handbook.md",
			"source_uri":           "pageindex://folder-123/kdoc-1",
			"provider":             "pageindex",
			"provider_document_id": "pi-doc-kdoc-1",
			"content_type":         "text/markdown",
			"size_bytes":           int64(2048),
			"checksum":             "sha256:kdoc-1",
			"status":               "indexed",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
		OccurredAt:  now,
	}); err != nil {
		t.Fatalf("apply knowledge document metadata event: %v", err)
	}
	memoryStore := memstore.NewPlatformStore(core)
	if err := memoryStore.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            "candidate-citation-1",
		MissionID:     "mission-1",
		SourceType:    memcore.SourceEvidence,
		SourceRef:     "evidence-1",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		ProposedKind:  agentcore.MemoryKindMission,
		ProposedLayer: memcore.MemoryLayerFact,
		Topic:         "release",
		Summary:       "Release follows the handbook.",
		EvidenceRefs:  []string{"knowledge_document:kdoc-1"},
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
	}, "candidate-citation-1", memoryadmin.AcceptCandidateRequest{
		ProfileID: "memory-profile-1",
		MemoryID:  "memory-record-citation-1",
	})
	if err != nil {
		t.Fatalf("AcceptCandidate: %v", err)
	}

	_, links, _, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "mission:mission-1")
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	if len(links) != 1 ||
		links[0].LinkType != "memory_cites_knowledge_document" ||
		links[0].FromObjectID != record.MemoryID ||
		links[0].ToObjectID != "kdoc-1" {
		t.Fatalf("links = %+v", links)
	}
	assertContextGraphEvidence(t, evidence, "memory-knowledge-citation-added:memory-record-citation-1:kdoc-1")
}

func TestProductionMemoryKnowledgeChunkCitationWritesOntologyContextGraph(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-memory-chunk-citation-ontology"},
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

	now := time.Date(2026, 6, 20, 15, 0, 0, 0, time.UTC)
	knowledgeRunner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatalf("new production ontology knowledge runner: %v", err)
	}
	if _, err := knowledgeRunner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "knowledge-document-metadata:kdoc-1",
		TenantID: "mission:mission-1",
		Type:     "knowledge.document.metadata.updated",
		StreamID: "kdoc-1",
		Version:  1,
		Payload: map[string]any{
			"id":                   "kdoc-1",
			"knowledge_base_id":    "kb-1",
			"title":                "release-handbook.md",
			"source_uri":           "pageindex://folder-123/kdoc-1",
			"provider":             "pageindex",
			"provider_document_id": "pi-doc-kdoc-1",
			"content_type":         "text/markdown",
			"size_bytes":           int64(2048),
			"checksum":             "sha256:kdoc-1",
			"status":               "indexed",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
		OccurredAt:  now,
	}); err != nil {
		t.Fatalf("apply knowledge document metadata event: %v", err)
	}
	if _, err := knowledgeRunner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "knowledge-chunk-metadata:kdoc-1:0:rev-1",
		TenantID: "mission:mission-1",
		Type:     "knowledge.chunk.metadata.updated",
		StreamID: "kdoc-1:0",
		Version:  1,
		Payload: map[string]any{
			"id":                "kdoc-1:0",
			"document_id":       "kdoc-1",
			"knowledge_base_id": "kb-1",
			"ordinal":           0,
			"heading_path":      []any{"Release"},
			"start_offset":      0,
			"end_offset":        128,
			"content_hash":      "sha256:chunk",
			"vector_id":         "kdoc-1:0",
			"token_estimate":    128,
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
		OccurredAt:  now,
	}); err != nil {
		t.Fatalf("apply knowledge chunk metadata event: %v", err)
	}

	memoryStore := memstore.NewPlatformStore(core)
	if err := memoryStore.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            "candidate-chunk-citation-1",
		MissionID:     "mission-1",
		SourceType:    memcore.SourceEvidence,
		SourceRef:     "evidence-1",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		ProposedKind:  agentcore.MemoryKindMission,
		ProposedLayer: memcore.MemoryLayerFact,
		Topic:         "release",
		Summary:       "Release follows a cited handbook chunk.",
		EvidenceRefs:  []string{"knowledge_chunk:kdoc-1:0"},
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
	}, "candidate-chunk-citation-1", memoryadmin.AcceptCandidateRequest{
		ProfileID: "memory-profile-1",
		MemoryID:  "memory-record-chunk-citation-1",
	})
	if err != nil {
		t.Fatalf("AcceptCandidate: %v", err)
	}

	_, links, _, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "mission:mission-1")
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	assertContextGraphLink(t, links, "memory_cites_knowledge_chunk", record.MemoryID, "kdoc-1:0")
	assertContextGraphEvidence(t, evidence, "memory-knowledge-chunk-citation-added:memory-record-chunk-citation-1:kdoc-1:0")
}

func TestProductionMemoryKnowledgeAnchorCitationWritesOntologyContextGraph(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-memory-anchor-citation-ontology"},
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

	now := time.Date(2026, 6, 20, 16, 30, 0, 0, time.UTC)
	knowledgeRunner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatalf("new production ontology knowledge runner: %v", err)
	}
	if _, err := knowledgeRunner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "knowledge-document-metadata:kdoc-1",
		TenantID: "mission:mission-1",
		Type:     "knowledge.document.metadata.updated",
		StreamID: "kdoc-1",
		Version:  1,
		Payload: map[string]any{
			"id":                   "kdoc-1",
			"knowledge_base_id":    "kb-1",
			"title":                "release-handbook.md",
			"source_uri":           "pageindex://folder-123/kdoc-1",
			"provider":             "pageindex",
			"provider_document_id": "pi-doc-kdoc-1",
			"content_type":         "text/markdown",
			"size_bytes":           int64(2048),
			"checksum":             "sha256:kdoc-1",
			"status":               "indexed",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
		OccurredAt:  now,
	}); err != nil {
		t.Fatalf("apply knowledge document metadata event: %v", err)
	}
	if _, err := knowledgeRunner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "knowledge-page-metadata:kdoc-1:page:1:rev-1",
		TenantID: "mission:mission-1",
		Type:     "knowledge.page.metadata.updated",
		StreamID: "kdoc-1",
		Version:  2,
		Payload: map[string]any{
			"id":                   "kdoc-1:page:1",
			"document_id":          "kdoc-1",
			"knowledge_base_id":    "kb-1",
			"page_number":          1,
			"provider":             "pageindex",
			"provider_document_id": "pi-doc-kdoc-1",
			"heading_path":         []any{"Release"},
			"source_uri":           "pageindex://folder-123/pi-doc-kdoc-1#page=1",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
		OccurredAt:  now,
	}); err != nil {
		t.Fatalf("apply knowledge page metadata event: %v", err)
	}
	anchorID := "kdoc-1:page:1:anchor:0007"
	if _, err := knowledgeRunner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "knowledge-anchor-metadata:" + anchorID + ":rev-1",
		TenantID: "mission:mission-1",
		Type:     "knowledge.anchor.metadata.updated",
		StreamID: "kdoc-1",
		Version:  3,
		Payload: map[string]any{
			"id":                   anchorID,
			"knowledge_base_id":    "kb-1",
			"document_id":          "kdoc-1",
			"page_id":              "kdoc-1:page:1",
			"page_number":          1,
			"provider":             "pageindex",
			"provider_document_id": "pi-doc-kdoc-1",
			"provider_node_id":     "0007",
			"heading_path":         []any{"Release"},
			"structure_path":       []any{"Handbook", "Release"},
			"bbox":                 map[string]any{"x": 0.1, "y": 0.2, "width": 0.3, "height": 0.4},
			"image_ref":            "pageindex://folder-123/pi-doc-kdoc-1#page=1&image=preview",
			"content_hash":         "",
			"snippet_hash":         "sha256:anchor",
			"source_uri":           "pageindex://folder-123/pi-doc-kdoc-1#page=1&anchor=0007",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
		OccurredAt:  now,
	}); err != nil {
		t.Fatalf("apply knowledge anchor metadata event: %v", err)
	}

	memoryStore := memstore.NewPlatformStore(core)
	if err := memoryStore.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            "candidate-anchor-citation-1",
		MissionID:     "mission-1",
		SourceType:    memcore.SourceEvidence,
		SourceRef:     "evidence-1",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		ProposedKind:  agentcore.MemoryKindMission,
		ProposedLayer: memcore.MemoryLayerFact,
		Topic:         "release",
		Summary:       "Release follows a cited PageIndex anchor.",
		EvidenceRefs:  []string{"knowledge_anchor:" + anchorID},
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
	}, "candidate-anchor-citation-1", memoryadmin.AcceptCandidateRequest{
		ProfileID: "memory-profile-1",
		MemoryID:  "memory-record-anchor-citation-1",
	})
	if err != nil {
		t.Fatalf("AcceptCandidate: %v", err)
	}

	_, links, _, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "mission:mission-1")
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	assertContextGraphLink(t, links, "memory_cites_knowledge_anchor", record.MemoryID, anchorID)
	assertContextGraphEvidence(t, evidence, "memory-knowledge-anchor-citation-added:memory-record-anchor-citation-1:"+anchorID)
}

func TestProductionMemoryKnowledgeCitationSkipsMissingTargetAndReplays(t *testing.T) {
	tests := []struct {
		name        string
		evidenceRef string
		eventID     string
		eventType   string
		targetField string
		targetType  string
		targetID    string
		linkType    string
	}{
		{
			name:        "document",
			evidenceRef: "knowledge_document:kdoc-missing",
			eventID:     "memory-knowledge-citation-added:memory-record-citation-missing-document:kdoc-missing",
			eventType:   "memory.knowledge.citation.added",
			targetField: "knowledge_document_id",
			targetType:  "knowledge_document",
			targetID:    "kdoc-missing",
			linkType:    "memory_cites_knowledge_document",
		},
		{
			name:        "chunk",
			evidenceRef: "knowledge_chunk:kdoc-missing:0",
			eventID:     "memory-knowledge-chunk-citation-added:memory-record-citation-missing-chunk:kdoc-missing:0",
			eventType:   "memory.knowledge_chunk.citation.added",
			targetField: "knowledge_chunk_id",
			targetType:  "knowledge_chunk",
			targetID:    "kdoc-missing:0",
			linkType:    "memory_cites_knowledge_chunk",
		},
		{
			name:        "anchor",
			evidenceRef: "knowledge_anchor:kdoc-missing:page:1:anchor:0007",
			eventID:     "memory-knowledge-anchor-citation-added:memory-record-citation-missing-anchor:kdoc-missing:page:1:anchor:0007",
			eventType:   "memory.knowledge_anchor.citation.added",
			targetField: "knowledge_anchor_id",
			targetType:  "knowledge_anchor",
			targetID:    "kdoc-missing:page:1:anchor:0007",
			linkType:    "memory_cites_knowledge_anchor",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
				db.RoleCore: {Driver: db.DriverMemory, Name: "production-memory-citation-missing-" + tt.name},
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

			now := time.Date(2026, 6, 20, 18, 30, 0, 0, time.UTC)
			missionID := "mission-citation-missing-" + tt.name
			tenantID := "mission:" + missionID
			recordID := "memory-record-citation-missing-" + tt.name
			memoryStore := memstore.NewPlatformStore(core)
			if err := memoryStore.SaveCandidate(ctx, memcore.MemoryCandidate{
				ID:            "candidate-citation-missing-" + tt.name,
				MissionID:     missionID,
				SourceType:    memcore.SourceEvidence,
				SourceRef:     "evidence-citation-missing-" + tt.name,
				ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: missionID},
				ProposedKind:  agentcore.MemoryKindMission,
				ProposedLayer: memcore.MemoryLayerFact,
				Topic:         "citation",
				Summary:       "Accepted memory should wait for projected Knowledge citation target.",
				EvidenceRefs:  []string{tt.evidenceRef},
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
			productionSink := sink.(productionMemoryRecordOntologySink)
			api := productionAPI{
				contextGraphCore: core,
				ontologyProject:  productionSink.runner,
				ontologyEvents:   newProductionOntologyEventLog(core),
			}
			memoryAdmin := memoryadmin.NewService(memoryStore, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now }).
				WithMemoryRecordMetadataSink(sink)

			record, err := memoryAdmin.AcceptCandidate(ctx, memoryadmin.ViewerRef{
				ViewerID:        "reviewer-1",
				PermissionScope: []string{memoryadmin.PermissionMemoryReview},
				MissionIDs:      []string{missionID},
			}, "candidate-citation-missing-"+tt.name, memoryadmin.AcceptCandidateRequest{
				ProfileID: "memory-profile-1",
				MemoryID:  recordID,
			})
			if err != nil {
				t.Fatalf("AcceptCandidate should not fail when citation target is missing from ContextGraph: %v", err)
			}

			_, links, _, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, tenantID)
			if err != nil {
				t.Fatalf("context graph snapshot: %v", err)
			}
			assertNoContextGraphLink(t, links, tt.linkType, record.MemoryID, tt.targetID)
			assertContextGraphEvidence(t, evidence, "memory-record-accepted:"+record.MemoryID)

			auditID := "projection-skipped:" + tt.eventID + ":missing-citation-target"
			audit := assertProjectionAudit(t, ctx, contextgraph.NewPlatformStore(core), tenantID, auditID, tt.eventType, "missing_citation_target")
			if audit.Details["memory_record_id"] != record.MemoryID {
				t.Fatalf("projection audit %s memory_record_id = %#v, want %q", audit.ID, audit.Details["memory_record_id"], record.MemoryID)
			}
			if audit.Details[tt.targetField] != tt.targetID {
				t.Fatalf("projection audit %s %s = %#v, want %q", audit.ID, tt.targetField, audit.Details[tt.targetField], tt.targetID)
			}
			assertProjectionAuditTarget(t, audit, "missing_targets", tt.targetType, tt.targetID)
			events, err := newProductionOntologyEventLog(core).List(ctx)
			if err != nil {
				t.Fatalf("list ontology events: %v", err)
			}
			assertOntologyEvent(t, events, tt.eventID, tt.eventType)
			assertOntologyEventPayload(t, events, tt.eventID, tt.targetField, tt.targetID)

			unresolvedAudit, err := replayProjectionAuditForTest(t, ctx, api, tenantID, audit.ID)
			if err != nil {
				t.Fatalf("ReplayProjectionAudit unresolved: %v", err)
			}
			if unresolvedAudit.Status != "skipped" {
				t.Fatalf("unresolved replay status = %q, want skipped", unresolvedAudit.Status)
			}
			_, links, _, _, err = contextgraph.NewPlatformStore(core).Snapshot(ctx, tenantID)
			if err != nil {
				t.Fatalf("context graph snapshot after unresolved replay: %v", err)
			}
			assertNoContextGraphLink(t, links, tt.linkType, record.MemoryID, tt.targetID)

			if err := contextgraph.NewPlatformStore(core).SaveObject(ctx, contextgraph.Object{
				ID:           tt.targetID,
				TenantID:     tenantID,
				ObjectType:   tt.targetType,
				CanonicalKey: tt.targetType + ":" + tt.targetID,
				Label:        tt.targetID,
				Status:       "active",
				UpdatedAt:    now,
			}); err != nil {
				t.Fatalf("save recovered citation target: %v", err)
			}
			replayedAudit, err := replayProjectionAuditForTest(t, ctx, api, tenantID, audit.ID)
			if err != nil {
				t.Fatalf("ReplayProjectionAudit: %v", err)
			}
			if replayedAudit.Status != "replayed" {
				t.Fatalf("replayed audit status = %q, want replayed", replayedAudit.Status)
			}
			if replayedAudit.ReasonCode != "missing_citation_target" {
				t.Fatalf("replayed audit reason code = %q, want original missing_citation_target", replayedAudit.ReasonCode)
			}
			_, links, _, _, err = contextgraph.NewPlatformStore(core).Snapshot(ctx, tenantID)
			if err != nil {
				t.Fatalf("context graph snapshot after replay: %v", err)
			}
			assertContextGraphLink(t, links, tt.linkType, record.MemoryID, tt.targetID)
		})
	}
}

func TestProductionMemoryKnowledgeCitationRequiresTargetInSameTenant(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-memory-citation-tenant-target"},
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

	now := time.Date(2026, 6, 20, 19, 15, 0, 0, time.UTC)
	if err := contextgraph.NewPlatformStore(core).SaveObject(ctx, contextgraph.Object{
		ID:           "shared-kdoc",
		TenantID:     "mission:other-tenant",
		ObjectType:   "knowledge_document",
		CanonicalKey: "knowledge_document:shared-kdoc",
		Label:        "Other tenant document",
		Status:       "active",
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("save other tenant knowledge document: %v", err)
	}

	memoryStore := memstore.NewPlatformStore(core)
	if err := memoryStore.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            "candidate-citation-tenant-target",
		MissionID:     "tenant-target",
		SourceType:    memcore.SourceEvidence,
		SourceRef:     "evidence-citation-tenant-target",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "tenant-target"},
		ProposedKind:  agentcore.MemoryKindMission,
		ProposedLayer: memcore.MemoryLayerFact,
		Topic:         "citation",
		Summary:       "Citation targets must belong to the same tenant as the memory record.",
		EvidenceRefs:  []string{"knowledge_document:shared-kdoc"},
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
		MissionIDs:      []string{"tenant-target"},
	}, "candidate-citation-tenant-target", memoryadmin.AcceptCandidateRequest{
		ProfileID: "memory-profile-1",
		MemoryID:  "memory-record-citation-tenant-target",
	})
	if err != nil {
		t.Fatalf("AcceptCandidate should not use another tenant citation target: %v", err)
	}

	_, links, _, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "mission:tenant-target")
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	assertNoContextGraphLink(t, links, "memory_cites_knowledge_document", record.MemoryID, "shared-kdoc")
	assertContextGraphEvidence(t, evidence, "memory-record-accepted:"+record.MemoryID)
	audit := assertProjectionAudit(t, ctx, contextgraph.NewPlatformStore(core), "mission:tenant-target", "projection-skipped:memory-knowledge-citation-added:"+record.MemoryID+":shared-kdoc:missing-citation-target", "memory.knowledge.citation.added", "missing_citation_target")
	assertProjectionAuditTarget(t, audit, "missing_targets", "knowledge_document", "shared-kdoc")
}
