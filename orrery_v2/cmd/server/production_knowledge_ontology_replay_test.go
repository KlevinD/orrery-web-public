package main

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
	"orrery_v2/internal/platform/migration"
)

func TestProductionKnowledgeMetadataSkipsMissingProjectionTargets(t *testing.T) {
	tests := []struct {
		name       string
		event      graphprotocol.DomainEvent
		linkType   string
		fromID     string
		toID       string
		targetType string
		targetID   string
	}{
		{
			name: "document status",
			event: graphprotocol.DomainEvent{
				ID:       "knowledge-document-status:kdoc-missing:indexed",
				TenantID: "org-1",
				Type:     "knowledge.document.status.updated",
				StreamID: "knowledge_document:kdoc-missing",
				Version:  1,
				Payload: map[string]any{
					"id":     "kdoc-missing",
					"status": string(core.DocumentIndexed),
				},
				PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
			},
			targetType: "knowledge_document",
			targetID:   "kdoc-missing",
		},
		{
			name: "chunk document",
			event: graphprotocol.DomainEvent{
				ID:       "knowledge-chunk-metadata:kdoc-missing:0:rev-1",
				TenantID: "org-1",
				Type:     "knowledge.chunk.metadata.updated",
				StreamID: "knowledge_chunk:kdoc-missing:0",
				Version:  1,
				Payload: map[string]any{
					"id":                "kdoc-missing:0",
					"knowledge_base_id": "kb-1",
					"document_id":       "kdoc-missing",
					"ordinal":           0,
				},
				PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
			},
			linkType:   "knowledge_chunk_belongs_to_document",
			fromID:     "kdoc-missing:0",
			toID:       "kdoc-missing",
			targetType: "knowledge_document",
			targetID:   "kdoc-missing",
		},
		{
			name: "page document",
			event: graphprotocol.DomainEvent{
				ID:       "knowledge-page-metadata:kdoc-missing:page:1:rev-1",
				TenantID: "org-1",
				Type:     "knowledge.page.metadata.updated",
				StreamID: "knowledge_document:kdoc-missing",
				Version:  1,
				Payload: map[string]any{
					"id":                   "kdoc-missing:page:1",
					"knowledge_base_id":    "kb-1",
					"document_id":          "kdoc-missing",
					"page_number":          1,
					"provider":             string(core.ProviderPageIndex),
					"provider_document_id": "pi-doc-missing",
				},
				PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
			},
			linkType:   "knowledge_page_belongs_to_document",
			fromID:     "kdoc-missing:page:1",
			toID:       "kdoc-missing",
			targetType: "knowledge_document",
			targetID:   "kdoc-missing",
		},
		{
			name: "anchor page",
			event: graphprotocol.DomainEvent{
				ID:       "knowledge-anchor-metadata:kdoc-1:page:1:anchor:0007:rev-1",
				TenantID: "org-1",
				Type:     "knowledge.anchor.metadata.updated",
				StreamID: "knowledge_document:kdoc-1",
				Version:  1,
				Payload: map[string]any{
					"id":                   "kdoc-1:page:1:anchor:0007",
					"knowledge_base_id":    "kb-1",
					"document_id":          "kdoc-1",
					"page_id":              "kdoc-1:page:1",
					"page_number":          1,
					"provider":             string(core.ProviderPageIndex),
					"provider_document_id": "pi-doc-1",
					"provider_node_id":     "0007",
				},
				PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
			},
			linkType:   "knowledge_anchor_belongs_to_page",
			fromID:     "kdoc-1:page:1:anchor:0007",
			toID:       "kdoc-1:page:1",
			targetType: "knowledge_page",
			targetID:   "kdoc-1:page:1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
				db.RoleCore: {Driver: db.DriverMemory, Name: "production-knowledge-missing-target-" + tt.name},
			}})
			if err != nil {
				t.Fatalf("open database registry: %v", err)
			}
			defer databases.Close()
			coreDB, ok := databases.Get(db.RoleCore)
			if !ok || coreDB == nil {
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
			if err := ontology.InstallDefaultOntologyDefinitions(ctx, ontology.NewPlatformRegistry(coreDB)); err != nil {
				t.Fatalf("install default ontology: %v", err)
			}
			sink, err := newProductionOntologyDocumentMetadataSink(ctx, coreDB)
			if err != nil {
				t.Fatalf("new production ontology document sink: %v", err)
			}

			event := tt.event
			event.OccurredAt = time.Date(2026, 6, 20, 20, 10, 0, 0, time.UTC)
			if err := sink.publishOntologyEvent(ctx, event); err != nil {
				t.Fatalf("publish knowledge metadata should not fail when projection target is missing: %v", err)
			}

			_, links, _, evidence, err := contextgraph.NewPlatformStore(coreDB).Snapshot(ctx, event.TenantID)
			if err != nil {
				t.Fatalf("context graph snapshot: %v", err)
			}
			if tt.linkType != "" {
				assertNoContextGraphLink(t, links, tt.linkType, tt.fromID, tt.toID)
			}
			for _, item := range evidence {
				if item.ID == event.ID {
					t.Fatalf("evidence %s should not be written for skipped projection", event.ID)
				}
			}
			audit := assertProjectionAudit(t, ctx, contextgraph.NewPlatformStore(coreDB), event.TenantID, "projection-skipped:"+event.ID+":missing-knowledge-projection-target", event.Type, "missing_knowledge_projection_target")
			assertProjectionAuditTarget(t, audit, "missing_targets", tt.targetType, tt.targetID)
			events, err := newProductionOntologyEventLog(coreDB).List(ctx)
			if err != nil {
				t.Fatalf("list ontology events: %v", err)
			}
			assertOntologyEvent(t, events, event.ID, event.Type)
		})
	}
}

func TestProductionKnowledgeProjectionAuditReplayBackfillsSkippedChunk(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-knowledge-projection-audit-replay"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	defer databases.Close()
	coreDB, ok := databases.Get(db.RoleCore)
	if !ok || coreDB == nil {
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
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, ontology.NewPlatformRegistry(coreDB)); err != nil {
		t.Fatalf("install default ontology: %v", err)
	}
	runner, err := newProductionOntologyProjectionRunner(ctx, coreDB)
	if err != nil {
		t.Fatalf("new production ontology runner: %v", err)
	}
	api := productionAPI{
		ontologyProject:  runner,
		ontologyEvents:   newProductionOntologyEventLog(coreDB),
		contextGraphCore: coreDB,
	}
	sink := productionDocumentOntologySink{
		runner: runner,
		core:   coreDB,
		events: api.ontologyEvents,
	}
	now := time.Date(2026, 6, 20, 22, 30, 0, 0, time.UTC)
	chunkEvent := graphprotocol.DomainEvent{
		ID:         "knowledge-chunk-metadata:kdoc-replay:0:rev-1",
		TenantID:   "org-1",
		Type:       "knowledge.chunk.metadata.updated",
		StreamID:   "knowledge_chunk:kdoc-replay:0",
		Version:    1,
		OccurredAt: now,
		Payload: map[string]any{
			"id":                "kdoc-replay:0",
			"knowledge_base_id": "kb-1",
			"document_id":       "kdoc-replay",
			"ordinal":           0,
			"heading_path":      []any{"Replay"},
			"start_offset":      0,
			"end_offset":        42,
			"content_hash":      "sha256:chunk-replay",
			"vector_id":         "vec-replay",
			"token_estimate":    11,
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge.ingest"},
	}
	if err := sink.PublishKnowledgeChunkMetadata(ctx, chunkEvent); err != nil {
		t.Fatalf("PublishKnowledgeChunkMetadata: %v", err)
	}
	auditID := "projection-skipped:" + chunkEvent.ID + ":missing-knowledge-projection-target"
	audit := assertProjectionAudit(t, ctx, contextgraph.NewPlatformStore(coreDB), "org-1", auditID, "knowledge.chunk.metadata.updated", "missing_knowledge_projection_target")
	if audit.Status != "skipped" {
		t.Fatalf("audit status = %q, want skipped", audit.Status)
	}
	unresolved, err := replayProjectionAuditForTest(t, ctx, api, "org-1", auditID)
	if err != nil {
		t.Fatalf("ReplayProjectionAudit unresolved: %v", err)
	}
	if unresolved.Status != "skipped" {
		t.Fatalf("unresolved replay status = %q, want skipped", unresolved.Status)
	}

	documentEvent := graphprotocol.DomainEvent{
		ID:         "knowledge-document-metadata:kdoc-replay:rev-1",
		TenantID:   "org-1",
		Type:       "knowledge.document.metadata.updated",
		StreamID:   "knowledge_document:kdoc-replay",
		Version:    1,
		OccurredAt: now.Add(time.Second),
		Payload: map[string]any{
			"id":                   "kdoc-replay",
			"knowledge_base_id":    "kb-1",
			"title":                "Replay document",
			"source_uri":           "pageindex://document/pi-replay",
			"provider":             string(core.ProviderPageIndex),
			"provider_document_id": "pi-replay",
			"content_type":         "text/markdown",
			"size_bytes":           4096,
			"checksum":             "sha256:document-replay",
			"status":               string(core.DocumentIndexed),
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
	}
	if err := sink.PublishKnowledgeDocumentMetadata(ctx, documentEvent); err != nil {
		t.Fatalf("PublishKnowledgeDocumentMetadata: %v", err)
	}
	replayed, err := replayProjectionAuditForTest(t, ctx, api, "org-1", auditID)
	if err != nil {
		t.Fatalf("ReplayProjectionAudit: %v", err)
	}
	if replayed.Status != "replayed" {
		t.Fatalf("replayed audit status = %q, want replayed", replayed.Status)
	}
	objects, links, facts, evidence, err := contextgraph.NewPlatformStore(coreDB).Snapshot(ctx, "org-1")
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "knowledge_document", "kdoc-replay")
	assertContextGraphObject(t, objects, "knowledge_chunk", "kdoc-replay:0")
	assertContextGraphLink(t, links, "knowledge_chunk_belongs_to_document", "kdoc-replay:0", "kdoc-replay")
	assertContextGraphFact(t, facts, "knowledge_chunk", "kdoc-replay:0", "content_hash", "sha256:chunk-replay")
	assertContextGraphEvidence(t, evidence, chunkEvent.ID)
}

func TestProductionKnowledgeProjectionAuditReplayBackfillsStatusPageAndAnchor(t *testing.T) {
	tests := []struct {
		name            string
		event           graphprotocol.DomainEvent
		recoverTargets  func(context.Context, productionDocumentOntologySink, time.Time)
		assertProjected func(*testing.T, []contextgraph.Object, []contextgraph.Link, []contextgraph.Fact, []contextgraph.Evidence)
	}{
		{
			name: "document status",
			event: graphprotocol.DomainEvent{
				ID:       "knowledge-document-status:kdoc-status:indexed:rev-2",
				TenantID: "org-1",
				Type:     "knowledge.document.status.updated",
				StreamID: "knowledge_document:kdoc-status",
				Version:  2,
				Payload: map[string]any{
					"id":     "kdoc-status",
					"status": string(core.DocumentIndexed),
				},
				PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
			},
			recoverTargets: func(ctx context.Context, sink productionDocumentOntologySink, now time.Time) {
				publishKnowledgeReplayDocument(t, ctx, sink, now, "kdoc-status", "pi-status")
			},
			assertProjected: func(t *testing.T, objects []contextgraph.Object, links []contextgraph.Link, facts []contextgraph.Fact, evidence []contextgraph.Evidence) {
				assertContextGraphObject(t, objects, "knowledge_document", "kdoc-status")
				assertContextGraphFact(t, facts, "knowledge_document", "kdoc-status", "status", string(core.DocumentIndexed))
				assertContextGraphEvidence(t, evidence, "knowledge-document-status:kdoc-status:indexed:rev-2")
			},
		},
		{
			name: "page metadata",
			event: graphprotocol.DomainEvent{
				ID:       "knowledge-page-metadata:kdoc-page:page:1:rev-1",
				TenantID: "org-1",
				Type:     "knowledge.page.metadata.updated",
				StreamID: "knowledge_document:kdoc-page",
				Version:  1,
				Payload: map[string]any{
					"id":                   "kdoc-page:page:1",
					"knowledge_base_id":    "kb-1",
					"document_id":          "kdoc-page",
					"page_number":          1,
					"provider":             string(core.ProviderPageIndex),
					"provider_document_id": "pi-page",
					"heading_path":         []any{"Replay"},
					"source_uri":           "pageindex://document/pi-page#page=1",
				},
				PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
			},
			recoverTargets: func(ctx context.Context, sink productionDocumentOntologySink, now time.Time) {
				publishKnowledgeReplayDocument(t, ctx, sink, now, "kdoc-page", "pi-page")
			},
			assertProjected: func(t *testing.T, objects []contextgraph.Object, links []contextgraph.Link, facts []contextgraph.Fact, evidence []contextgraph.Evidence) {
				assertContextGraphObject(t, objects, "knowledge_page", "kdoc-page:page:1")
				assertContextGraphLink(t, links, "knowledge_page_belongs_to_document", "kdoc-page:page:1", "kdoc-page")
				assertContextGraphFact(t, facts, "knowledge_page", "kdoc-page:page:1", "provider_document_id", "pi-page")
				assertContextGraphEvidence(t, evidence, "knowledge-page-metadata:kdoc-page:page:1:rev-1")
			},
		},
		{
			name: "anchor metadata",
			event: graphprotocol.DomainEvent{
				ID:       "knowledge-anchor-metadata:kdoc-anchor:page:1:anchor:0007:rev-1",
				TenantID: "org-1",
				Type:     "knowledge.anchor.metadata.updated",
				StreamID: "knowledge_document:kdoc-anchor",
				Version:  1,
				Payload: map[string]any{
					"id":                   "kdoc-anchor:page:1:anchor:0007",
					"knowledge_base_id":    "kb-1",
					"document_id":          "kdoc-anchor",
					"page_id":              "kdoc-anchor:page:1",
					"page_number":          1,
					"provider":             string(core.ProviderPageIndex),
					"provider_document_id": "pi-anchor",
					"provider_node_id":     "0007",
					"heading_path":         []any{"Replay"},
					"structure_path":       []any{"Replay", "Anchor"},
					"bbox":                 map[string]any{"x": 0.1, "y": 0.2, "width": 0.3, "height": 0.4},
					"image_ref":            "pageindex://document/pi-anchor#page=1&image=preview",
					"content_hash":         "sha256:page-anchor",
					"snippet_hash":         "sha256:anchor",
					"source_uri":           "pageindex://document/pi-anchor#page=1&anchor=0007",
				},
				PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
			},
			recoverTargets: func(ctx context.Context, sink productionDocumentOntologySink, now time.Time) {
				publishKnowledgeReplayDocument(t, ctx, sink, now, "kdoc-anchor", "pi-anchor")
				publishKnowledgeReplayPage(t, ctx, sink, now.Add(time.Second), "kdoc-anchor", "pi-anchor")
			},
			assertProjected: func(t *testing.T, objects []contextgraph.Object, links []contextgraph.Link, facts []contextgraph.Fact, evidence []contextgraph.Evidence) {
				assertContextGraphObject(t, objects, "knowledge_anchor", "kdoc-anchor:page:1:anchor:0007")
				assertContextGraphLink(t, links, "knowledge_anchor_belongs_to_page", "kdoc-anchor:page:1:anchor:0007", "kdoc-anchor:page:1")
				assertContextGraphFact(t, facts, "knowledge_anchor", "kdoc-anchor:page:1:anchor:0007", "snippet_hash", "sha256:anchor")
				assertContextGraphEvidence(t, evidence, "knowledge-anchor-metadata:kdoc-anchor:page:1:anchor:0007:rev-1")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
				db.RoleCore: {Driver: db.DriverMemory, Name: "production-knowledge-projection-audit-replay-" + tt.name},
			}})
			if err != nil {
				t.Fatalf("open database registry: %v", err)
			}
			defer databases.Close()
			coreDB, ok := databases.Get(db.RoleCore)
			if !ok || coreDB == nil {
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
			if err := ontology.InstallDefaultOntologyDefinitions(ctx, ontology.NewPlatformRegistry(coreDB)); err != nil {
				t.Fatalf("install default ontology: %v", err)
			}
			runner, err := newProductionOntologyProjectionRunner(ctx, coreDB)
			if err != nil {
				t.Fatalf("new production ontology runner: %v", err)
			}
			api := productionAPI{
				ontologyProject:  runner,
				ontologyEvents:   newProductionOntologyEventLog(coreDB),
				contextGraphCore: coreDB,
			}
			sink := productionDocumentOntologySink{
				runner: runner,
				core:   coreDB,
				events: api.ontologyEvents,
			}
			now := time.Date(2026, 6, 20, 22, 45, 0, 0, time.UTC)
			event := tt.event
			event.OccurredAt = now
			var publishErr error
			switch event.Type {
			case "knowledge.document.status.updated":
				publishErr = sink.PublishKnowledgeDocumentStatus(ctx, event)
			case "knowledge.page.metadata.updated":
				publishErr = sink.PublishKnowledgePageMetadata(ctx, event)
			case "knowledge.anchor.metadata.updated":
				publishErr = sink.PublishKnowledgeAnchorMetadata(ctx, event)
			default:
				t.Fatalf("unsupported knowledge replay event type %q", event.Type)
			}
			if publishErr != nil {
				t.Fatalf("publish skipped event: %v", publishErr)
			}
			auditID := "projection-skipped:" + event.ID + ":missing-knowledge-projection-target"
			unresolved, err := replayProjectionAuditForTest(t, ctx, api, event.TenantID, auditID)
			if err != nil {
				t.Fatalf("ReplayProjectionAudit unresolved: %v", err)
			}
			if unresolved.Status != "skipped" {
				t.Fatalf("unresolved replay status = %q, want skipped", unresolved.Status)
			}

			tt.recoverTargets(ctx, sink, now.Add(time.Second))
			replayed, err := replayProjectionAuditForTest(t, ctx, api, event.TenantID, auditID)
			if err != nil {
				t.Fatalf("ReplayProjectionAudit: %v", err)
			}
			if replayed.Status != "replayed" {
				t.Fatalf("replayed audit status = %q, want replayed", replayed.Status)
			}
			objects, links, facts, evidence, err := contextgraph.NewPlatformStore(coreDB).Snapshot(ctx, event.TenantID)
			if err != nil {
				t.Fatalf("context graph snapshot: %v", err)
			}
			tt.assertProjected(t, objects, links, facts, evidence)
		})
	}
}

func TestProductionKnowledgeProjectionAuditBatchReplayOrdersPageBeforeAnchor(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-knowledge-projection-audit-batch-replay"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	defer databases.Close()
	coreDB, ok := databases.Get(db.RoleCore)
	if !ok || coreDB == nil {
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
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, ontology.NewPlatformRegistry(coreDB)); err != nil {
		t.Fatalf("install default ontology: %v", err)
	}
	runner, err := newProductionOntologyProjectionRunner(ctx, coreDB)
	if err != nil {
		t.Fatalf("new production ontology runner: %v", err)
	}
	api := productionAPI{
		ontologyProject:  runner,
		ontologyEvents:   newProductionOntologyEventLog(coreDB),
		contextGraphCore: coreDB,
	}
	sink := productionDocumentOntologySink{
		runner: runner,
		core:   coreDB,
		events: api.ontologyEvents,
	}
	now := time.Date(2026, 6, 20, 23, 10, 0, 0, time.UTC)
	anchorEvent := graphprotocol.DomainEvent{
		ID:         "knowledge-anchor-metadata:kdoc-batch:page:1:anchor:0007:rev-1",
		TenantID:   "org-1",
		Type:       "knowledge.anchor.metadata.updated",
		StreamID:   "knowledge_document:kdoc-batch",
		Version:    1,
		OccurredAt: now,
		Payload: map[string]any{
			"id":                   "kdoc-batch:page:1:anchor:0007",
			"knowledge_base_id":    "kb-1",
			"document_id":          "kdoc-batch",
			"page_id":              "kdoc-batch:page:1",
			"page_number":          1,
			"provider":             string(core.ProviderPageIndex),
			"provider_document_id": "pi-batch",
			"provider_node_id":     "0007",
			"heading_path":         []any{"Batch"},
			"structure_path":       []any{"Batch", "Anchor"},
			"bbox":                 map[string]any{"x": 0.1, "y": 0.2, "width": 0.3, "height": 0.4},
			"image_ref":            "pageindex://document/pi-batch#page=1&image=preview",
			"content_hash":         "sha256:page-batch",
			"snippet_hash":         "sha256:anchor-batch",
			"source_uri":           "pageindex://document/pi-batch#page=1&anchor=0007",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
	}
	pageEvent := graphprotocol.DomainEvent{
		ID:         "knowledge-page-metadata:kdoc-batch:page:1:rev-1",
		TenantID:   "org-1",
		Type:       "knowledge.page.metadata.updated",
		StreamID:   "knowledge_document:kdoc-batch",
		Version:    1,
		OccurredAt: now,
		Payload: map[string]any{
			"id":                   "kdoc-batch:page:1",
			"knowledge_base_id":    "kb-1",
			"document_id":          "kdoc-batch",
			"page_number":          1,
			"provider":             string(core.ProviderPageIndex),
			"provider_document_id": "pi-batch",
			"heading_path":         []any{"Batch"},
			"source_uri":           "pageindex://document/pi-batch#page=1",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
	}
	if err := sink.PublishKnowledgeAnchorMetadata(ctx, anchorEvent); err != nil {
		t.Fatalf("PublishKnowledgeAnchorMetadata: %v", err)
	}
	if err := sink.PublishKnowledgePageMetadata(ctx, pageEvent); err != nil {
		t.Fatalf("PublishKnowledgePageMetadata: %v", err)
	}
	documentEvent := graphprotocol.DomainEvent{
		ID:         "knowledge-document-metadata:kdoc-batch:rev-1",
		TenantID:   "org-1",
		Type:       "knowledge.document.metadata.updated",
		StreamID:   "knowledge_document:kdoc-batch",
		Version:    1,
		OccurredAt: now.Add(2 * time.Second),
		Payload: map[string]any{
			"id":                   "kdoc-batch",
			"knowledge_base_id":    "kb-1",
			"title":                "Batch replay document",
			"source_uri":           "pageindex://document/pi-batch",
			"provider":             string(core.ProviderPageIndex),
			"provider_document_id": "pi-batch",
			"content_type":         "text/markdown",
			"size_bytes":           4096,
			"checksum":             "sha256:document-batch",
			"status":               string(core.DocumentIndexed),
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
	}
	if err := sink.PublishKnowledgeDocumentMetadata(ctx, documentEvent); err != nil {
		t.Fatalf("PublishKnowledgeDocumentMetadata: %v", err)
	}
	pageAuditID := "projection-skipped:" + pageEvent.ID + ":missing-knowledge-projection-target"
	anchorAuditID := "projection-skipped:" + anchorEvent.ID + ":missing-knowledge-projection-target"

	for _, auditID := range []string{pageAuditID, anchorAuditID} {
		replayed, err := replayProjectionAuditForTest(t, ctx, api, "org-1", auditID)
		if err != nil {
			t.Fatalf("replay projection audit %s: %v", auditID, err)
		}
		if replayed.Status != "replayed" {
			t.Fatalf("projection audit %s status = %q, want replayed", auditID, replayed.Status)
		}
	}
	objects, links, facts, evidence, err := contextgraph.NewPlatformStore(coreDB).Snapshot(ctx, "org-1")
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "knowledge_page", "kdoc-batch:page:1")
	assertContextGraphObject(t, objects, "knowledge_anchor", "kdoc-batch:page:1:anchor:0007")
	assertContextGraphLink(t, links, "knowledge_page_belongs_to_document", "kdoc-batch:page:1", "kdoc-batch")
	assertContextGraphLink(t, links, "knowledge_anchor_belongs_to_page", "kdoc-batch:page:1:anchor:0007", "kdoc-batch:page:1")
	assertContextGraphFact(t, facts, "knowledge_anchor", "kdoc-batch:page:1:anchor:0007", "snippet_hash", "sha256:anchor-batch")
	assertContextGraphEvidence(t, evidence, pageEvent.ID)
	assertContextGraphEvidence(t, evidence, anchorEvent.ID)
}

func replayProjectionAuditForTest(t testing.TB, ctx context.Context, api productionAPI, tenantID, auditID string) (contextgraph.ProjectionAudit, error) {
	t.Helper()
	audit, err := contextgraph.NewPlatformStore(api.contextGraphCore).GetProjectionAudit(ctx, tenantID, auditID)
	if err != nil {
		return contextgraph.ProjectionAudit{}, err
	}
	return api.computeProjectionAuditReplay(ctx, audit)
}
