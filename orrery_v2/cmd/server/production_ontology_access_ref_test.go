package main

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/operations"
	projectadmin "orrery_v2/internal/app/projectadmin"
	"orrery_v2/internal/platform/eventstore"
	"orrery_v2/internal/platform/graphprotocol"
	toolres "orrery_v2/internal/resource/tool"
)

func TestProductionOntologyPublishersWriteTrustedSourceAccessRefs(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-ontology-source-access-refs")
	gate := graphprotocol.NewConformanceGate()
	runner := ontology.NewMappingRuleProjectionRunner(
		graphprotocol.NewProjectionRegistry(gate),
		gate,
		contextgraph.NewMemoryStore(),
	)
	api := productionAPI{ontologyProject: runner, ontologyEvents: newProductionOntologyEventLog(core)}
	now := time.Unix(10, 0).UTC()

	if err := api.publishMissionCreatedToOntology(ctx, missioncontrol.ViewerRef{ViewerID: "user-1", ViewerKind: missioncontrol.ViewerKindUser}, missioncontrol.MissionDTO{
		MissionID: "mission-1", Title: "Mission", Status: "active", CreatedAt: now,
	}); err != nil {
		t.Fatalf("publish mission: %v", err)
	}
	if err := api.publishProjectCreatedToOntology(ctx, projectadmin.ViewerRef{ViewerID: "user-1", ViewerKind: projectadmin.ViewerKindUser}, projectadmin.Project{
		ProjectID: "project-1", OrganizationID: "org-1", Title: "Project", Status: "active", Version: 1, CreatedAt: now,
	}); err != nil {
		t.Fatalf("publish project: %v", err)
	}
	if err := api.publishOrganizationCreatedToOntology(ctx, operations.ViewerRef{ViewerID: "user-1", ViewerKind: "user"}, operations.OrganizationDTO{
		OrganizationID: "org-1", Name: "Organization", Status: "active",
	}); err != nil {
		t.Fatalf("publish organization: %v", err)
	}

	memorySink := productionMemoryRecordOntologySink{runner: runner, events: api.ontologyEvents}
	if err := memorySink.PublishMemoryRecordMetadata(ctx, graphprotocol.DomainEvent{
		ID: "memory-accepted:memory-1", TenantID: "mission:mission-1", Type: "memory.record.accepted", StreamID: "memory-1", Version: 1,
		SourceAccessRef: graphprotocol.AccessRef{Kind: "attacker", ID: "injected", TenantID: "other"},
	}); err != nil {
		t.Fatalf("publish memory: %v", err)
	}
	documentSink := productionDocumentOntologySink{runner: runner, events: api.ontologyEvents}
	if err := documentSink.PublishKnowledgeDocumentMetadata(ctx, graphprotocol.DomainEvent{
		ID: "knowledge-metadata:document-1", TenantID: "org-1", Type: "knowledge.document.metadata.updated", StreamID: "document-1", Version: 1,
		SourceAccessRef: graphprotocol.AccessRef{Kind: "attacker", ID: "injected", TenantID: "other"},
	}); err != nil {
		t.Fatalf("publish knowledge: %v", err)
	}

	events, err := api.ontologyEvents.List(ctx)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	want := map[string]graphprotocol.AccessRef{
		"mission.created":                     missionOntologySourceRef("global", "mission-1"),
		"project.created":                     projectOntologySourceRef("global", "project-1"),
		"organization.created":                organizationOntologySourceRef("org-1"),
		"memory.record.accepted":              trustedOntologySourceRef("memory_record", "memory-1", "mission:mission-1"),
		"knowledge.document.metadata.updated": trustedOntologySourceRef("knowledge_document", "document-1", "org-1"),
	}
	seen := map[string]bool{}
	for _, event := range events {
		expected, ok := want[event.Type]
		if !ok {
			continue
		}
		seen[event.Type] = true
		if event.SourceAccessRef != expected {
			t.Fatalf("event %s source access ref = %#v, want %#v", event.Type, event.SourceAccessRef, expected)
		}
	}
	for eventType := range want {
		if !seen[eventType] {
			t.Fatalf("missing captured event %s", eventType)
		}
	}
}

func TestProductionMCPProjectionWritesSystemResourceAccessRefs(t *testing.T) {
	ctx, core := openProductionMCPProjectionCore(t, "production-mcp-source-access-ref")
	definition := toolres.ToolDefinition{
		ID: agentcore.ToolID("mcp.test"), Kind: toolres.KindMCPServer, Name: "Test MCP", Enabled: true,
		MCP: &toolres.MCPServerDefinition{ServerName: "test", Transport: toolres.TransportStdio, ExposedTools: []string{"search"}},
	}
	if err := saveMCPServerCapabilityProjection(ctx, contextgraph.NewPlatformStore(core), definition, mcpCapabilityToolProjections(definition.MCP)); err != nil {
		t.Fatalf("save MCP projection: %v", err)
	}
	objects, links, facts, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "system")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	want := systemResourceOntologySourceRef("mcp.test")
	for _, object := range objects {
		if object.AccessState != contextgraph.AccessStateResolved || object.OwnerAccessRef != want {
			t.Fatalf("object %s access metadata = %#v/%s", object.ID, object.OwnerAccessRef, object.AccessState)
		}
	}
	for _, link := range links {
		if link.AccessState != contextgraph.AccessStateResolved || link.SourceAccessRef != want {
			t.Fatalf("link %s access metadata = %#v/%s", link.ID, link.SourceAccessRef, link.AccessState)
		}
	}
	for _, fact := range facts {
		if fact.AccessState != contextgraph.AccessStateResolved || fact.SourceAccessRef != want {
			t.Fatalf("fact %s access metadata = %#v/%s", fact.ID, fact.SourceAccessRef, fact.AccessState)
		}
	}
	for _, item := range evidence {
		if item.AccessState != contextgraph.AccessStateResolved || item.SourceAccessRef != want {
			t.Fatalf("evidence %s access metadata = %#v/%s", item.ID, item.SourceAccessRef, item.AccessState)
		}
	}
}

func TestProductionAccessRefBackfillResolverUsesExactStoredEventMetadata(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-access-ref-backfill-resolver")
	log := newProductionOntologyEventLog(core)
	want := graphprotocol.AccessRef{Kind: "project", ID: "project-1", TenantID: "project:global/project-1"}
	if err := log.Record(ctx, graphprotocol.DomainEvent{
		ID: "project-created:project-1", TenantID: "project:global/project-1", Type: "project.created", StreamID: "project-1", Version: 1, SourceAccessRef: want,
	}); err != nil {
		t.Fatalf("record event: %v", err)
	}
	resolver := productionAccessRefBackfillResolver{events: log.store}
	got, found, err := resolver.ResolveAccessRef(ctx, contextgraph.AccessRefBackfillCandidate{
		ResourceKind: contextgraph.AccessRefResourceObject,
		TenantID:     "project:global/project-1",
		ResourceID:   "project-1",
		SourceKind:   "event",
		SourceID:     "project-created:project-1",
	})
	if err != nil {
		t.Fatalf("ResolveAccessRef: %v", err)
	}
	if !found || got != contextGraphAccessRef(want) {
		t.Fatalf("resolved ref = %#v found=%t", got, found)
	}
	if _, found, err := resolver.ResolveAccessRef(ctx, contextgraph.AccessRefBackfillCandidate{
		ResourceKind: contextgraph.AccessRefResourceObject,
		TenantID:     "project:other",
		ResourceID:   "project-1",
		SourceID:     "project-created:project-1",
	}); err != nil || found {
		t.Fatalf("cross-tenant resolution found=%t err=%v", found, err)
	}
	if _, found, err := resolver.ResolveAccessRef(ctx, contextgraph.AccessRefBackfillCandidate{
		ResourceKind: contextgraph.AccessRefResourceAudit,
		TenantID:     "project:project-1",
		SourceKind:   "project.archived",
		SourceID:     "project-created:project-1",
	}); err != nil || found {
		t.Fatalf("mismatched event type resolution found=%t err=%v", found, err)
	}

	metadata, err := json.Marshal(ontologyEventMetadata{TenantID: "project:project-1", SourceAccessRef: want})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	if err := log.store.Append(ctx, eventstore.Envelope{
		EventID: "non-ontology-event", StreamID: "non-ontology", StreamVersion: 1,
		EventType: "project.created", EventVersion: 1, Metadata: metadata,
	}); err != nil {
		t.Fatalf("append non-ontology event: %v", err)
	}
	if _, found, err := resolver.ResolveAccessRef(ctx, contextgraph.AccessRefBackfillCandidate{
		ResourceKind: contextgraph.AccessRefResourceObject,
		TenantID:     "project:project-1",
		SourceKind:   "event",
		SourceID:     "non-ontology-event",
	}); err != nil || found {
		t.Fatalf("non-ontology event resolution found=%t err=%v", found, err)
	}
}

func TestRunRejectsConflictingOntologyAccessRefBackfillModes(t *testing.T) {
	err := run(context.Background(), []string{"-ontology-access-ref-backfill", "-ontology-access-ref-backfill-apply"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("run error = %v", err)
	}
}
