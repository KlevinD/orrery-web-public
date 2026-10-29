package main

import (
	"context"
	"reflect"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	knowledgecore "orrery_v2/internal/knowledge/core"
	memorycore "orrery_v2/internal/memory/core"
	"orrery_v2/internal/platform/graphprotocol"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

type catalogCoverageExpectation struct {
	eventType, objectType, id, property string
	value                               any
	systemResource                      bool
}

func catalogCoverageExpectations(tenantID string) []catalogCoverageExpectation {
	return []catalogCoverageExpectation{
		{"resource.tool.registered", "tool", "catalog-tool", "name", "Catalog tool", true},
		{"resource.skill.registered", "skill", "catalog-skill", "version", "1.2", true},
		{"resource.mcp_server.registered", "mcp_server", "catalog-mcp", "server_name", "catalog-mcp", true},
		{"knowledge.base.created", "knowledge_base", "catalog-kb", "owner_id", tenantID, false},
		{"memory.profile.created", "memory_profile", "catalog-memory", "scope_id", tenantID, false},
	}
}

func catalogCoverageSource(tenantID string) fakeOntologyInitializationCatalogSource {
	now := time.Date(2026, 10, 18, 1, 0, 0, 0, time.UTC)
	return fakeOntologyInitializationCatalogSource{
		tools: []toolres.ToolDefinition{{ID: "catalog-tool", Name: "Catalog tool", RiskLevel: toolres.RiskLow, UpdatedAt: now,
			MCP: &toolres.MCPServerDefinition{ServerName: "catalog-mcp", Transport: toolres.TransportStreamHTTP}}},
		skills: []skillres.SkillDefinition{{ID: "catalog-skill", Name: "Catalog skill", Version: "1.2", Enabled: true, RiskLevel: skillres.RiskLow, UpdatedAt: now}},
		bases: []knowledgecore.KnowledgeBase{{ID: "catalog-kb", Name: "Catalog knowledge", OwnerType: knowledgecore.OwnerOrganization,
			OwnerID: tenantID, Visibility: knowledgecore.VisibilityScoped, Status: knowledgecore.KnowledgeActive, UpdatedAt: now}},
		profiles: []memorycore.MemoryProfile{{ID: "catalog-memory", OwnerRef: memorycore.OwnerRef{OrganizationID: tenantID},
			MemoryKind: "organization", DefaultScope: memorycore.MemoryScope{Kind: memorycore.ScopeOrganization, ID: tenantID}, Enabled: true}},
	}
}

func runCatalogCoverageProjection(ctx context.Context, source fakeOntologyInitializationCatalogSource, runner *ontology.MappingRuleProjectionRunner) error {
	coordinator := newOntologyInitializationCoordinator([]ontologyInitializationAdapter{
		newOntologyInitializationResourceAdapter(source, source, runner),
		newOntologyInitializationKnowledgeAdapter(source, runner),
		newOntologyInitializationMemoryAdapter(source, runner),
	})
	_, err := coordinator.Run(ctx, ontologyInitializationRequest{Mode: ontologyInitializationApply, Operator: "catalog-coverage-test", PageSize: 1})
	return err
}

func catalogCoverageSnapshot(t *testing.T, ctx context.Context, graph *contextgraph.PlatformStore, tenantID string) contextgraph.ContextGraphSnapshot {
	t.Helper()
	objects, links, facts, evidence, err := graph.Snapshot(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	return contextgraph.ContextGraphSnapshot{Objects: objects, Links: links, Facts: facts, Evidence: evidence}
}

func assertCatalogCoverageProjection(t *testing.T, snapshot contextgraph.ContextGraphSnapshot, tenantID string, want catalogCoverageExpectation) {
	t.Helper()
	assertContextGraphObject(t, snapshot.Objects, want.objectType, want.id)
	sourceTenantID := tenantID
	if want.systemResource {
		sourceTenantID = "system"
	}
	foundFact, foundEvidence := false, false
	for _, fact := range snapshot.Facts {
		if fact.Subject.ID == want.id && fact.Predicate == want.property {
			if fact.TenantID != tenantID || !reflect.DeepEqual(fact.Value, want.value) || !fact.SourceAccessRef.Resolved() || fact.SourceAccessRef.TenantID != sourceTenantID {
				t.Fatalf("%s source-bound fact = %+v, want tenant=%s value=%v", want.eventType, fact, tenantID, want.value)
			}
			foundFact = true
		}
	}
	for _, evidence := range snapshot.Evidence {
		if evidence.SourceRef.ID == "ontology-initialize:"+want.eventType+":"+tenantID+":"+want.id+":1" {
			if evidence.TenantID != tenantID || evidence.SourceRef.Kind != want.eventType || !evidence.SourceAccessRef.Resolved() || evidence.SourceAccessRef.TenantID != sourceTenantID {
				t.Fatalf("%s source-bound evidence = %+v", want.eventType, evidence)
			}
			foundEvidence = true
		}
	}
	if !foundFact || !foundEvidence {
		t.Fatalf("%s missing graph fact/evidence: fact=%v evidence=%v snapshot=%+v", want.eventType, foundFact, foundEvidence, snapshot)
	}
}

func semanticCatalogCoverageSnapshot(snapshot contextgraph.ContextGraphSnapshot) contextgraph.ContextGraphSnapshot {
	// Static graph rows record the latest projection processing time. Replaying
	// them must preserve semantic data and source/business times, but need not
	// preserve OrreryIngestedAt (the documented temporal identity excludes it).
	snapshot.Objects = append([]contextgraph.Object(nil), snapshot.Objects...)
	snapshot.Links = append([]contextgraph.Link(nil), snapshot.Links...)
	snapshot.Facts = append([]contextgraph.Fact(nil), snapshot.Facts...)
	for i := range snapshot.Objects {
		snapshot.Objects[i].Temporal.OrreryIngestedAt = time.Time{}
	}
	for i := range snapshot.Links {
		snapshot.Links[i].Temporal.OrreryIngestedAt = time.Time{}
	}
	for i := range snapshot.Facts {
		snapshot.Facts[i].Temporal.OrreryIngestedAt = time.Time{}
	}
	return snapshot
}

func TestOntologyCatalogCoverageSuccessAndReplay(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "catalog-coverage-replay")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	graph := contextgraph.NewPlatformStore(core)
	source := catalogCoverageSource("catalog-tenant-a")
	if err := runCatalogCoverageProjection(ctx, source, runner); err != nil {
		t.Fatal(err)
	}
	before := map[string]contextgraph.ContextGraphSnapshot{}
	for _, tenantID := range []string{"catalog-tenant-a", defaultSystemSupportOrganizationID} {
		before[tenantID] = catalogCoverageSnapshot(t, ctx, graph, tenantID)
	}
	for _, want := range catalogCoverageExpectations("catalog-tenant-a") {
		t.Run(want.eventType, func(t *testing.T) {
			tenantID := "catalog-tenant-a"
			if want.systemResource {
				tenantID = defaultSystemSupportOrganizationID
			}
			assertCatalogCoverageProjection(t, before[tenantID], tenantID, want)
		})
	}
	historyCounts := map[string]int{}
	for _, query := range []string{
		"SELECT COUNT(*) FROM context_graph_resource_versions",
		"SELECT COUNT(*) FROM context_graph_fact_assertions",
	} {
		var count int
		if err := core.QueryRow(ctx, query).Scan(&count); err != nil {
			t.Fatal(err)
		}
		historyCounts[query] = count
	}
	if historyCounts["SELECT COUNT(*) FROM context_graph_resource_versions"] != 0 || historyCounts["SELECT COUNT(*) FROM context_graph_fact_assertions"] != len(before["catalog-tenant-a"].Facts)+len(before[defaultSystemSupportOrganizationID].Facts) {
		t.Fatalf("static catalog must retain current fact claims without resource versions: %+v", historyCounts)
	}
	if err := runCatalogCoverageProjection(ctx, source, runner); err != nil {
		t.Fatalf("replay production catalog: %v", err)
	}
	for tenantID, snapshot := range before {
		if after := catalogCoverageSnapshot(t, ctx, graph, tenantID); !reflect.DeepEqual(semanticCatalogCoverageSnapshot(snapshot), semanticCatalogCoverageSnapshot(after)) {
			t.Fatalf("catalog replay changed objects/facts/evidence for %s: before=%+v after=%+v", tenantID, snapshot, after)
		}
	}
	for query, beforeCount := range historyCounts {
		var count int
		if err := core.QueryRow(ctx, query).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != beforeCount {
			t.Fatalf("static catalog replay accumulated history: %s before=%d after=%d", query, beforeCount, count)
		}
		t.Logf("catalog replay bounded rows: %s before=%d after=%d", query, beforeCount, count)
	}
}

func TestOntologyCatalogCoverageRejectsMissingIdentity(t *testing.T) {
	source := catalogCoverageSource("catalog-tenant-a")
	events := toolOntologyInitializationEvents(source.tools[0])
	events = append(events, skillOntologyInitializationEvent(source.skills[0]), knowledgeBaseOntologyInitializationEvent(source.bases[0]), memoryProfileOntologyInitializationEvent(source.profiles[0]))
	for _, original := range events {
		t.Run(original.Type, func(t *testing.T) {
			ctx := context.Background()
			core := newOntologyEventLogTestCore(t, "catalog-coverage-malformed")
			runner, err := newProductionOntologyProjectionRunner(ctx, core)
			if err != nil {
				t.Fatal(err)
			}
			delete(original.Payload, "id")
			if err := applyOntologyInitializationEvents(ctx, runner, []graphprotocol.DomainEvent{original}); err == nil {
				t.Fatal("catalog event without source identity was accepted")
			}
			snapshot := catalogCoverageSnapshot(t, ctx, contextgraph.NewPlatformStore(core), original.TenantID)
			if len(snapshot.Objects)+len(snapshot.Links)+len(snapshot.Facts)+len(snapshot.Evidence) != 0 {
				t.Fatalf("malformed catalog event mutated graph: %+v", snapshot)
			}
		})
	}
}

func TestOntologyCatalogCoverageTenantAndSystemResourceIsolation(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "catalog-coverage-tenants")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	graph := contextgraph.NewPlatformStore(core)
	if err := runCatalogCoverageProjection(ctx, catalogCoverageSource("catalog-tenant-a"), runner); err != nil {
		t.Fatal(err)
	}
	a := catalogCoverageSnapshot(t, ctx, graph, "catalog-tenant-a")
	if b := catalogCoverageSnapshot(t, ctx, graph, "catalog-tenant-b"); len(b.Objects)+len(b.Links)+len(b.Facts)+len(b.Evidence) != 0 {
		t.Fatalf("tenant a catalog leaked into tenant b: %+v", b)
	}
	// Identical source IDs in a second organization must not overwrite tenant a.
	if err := runCatalogCoverageProjection(ctx, catalogCoverageSource("catalog-tenant-b"), runner); err != nil {
		t.Fatal(err)
	}
	if after := catalogCoverageSnapshot(t, ctx, graph, "catalog-tenant-a"); !reflect.DeepEqual(a, after) {
		t.Fatalf("tenant b catalog changed tenant a: before=%+v after=%+v", a, after)
	}
	for _, tenantID := range []string{"catalog-tenant-a", "catalog-tenant-b"} {
		snapshot := catalogCoverageSnapshot(t, ctx, graph, tenantID)
		for _, want := range catalogCoverageExpectations(tenantID) {
			if want.systemResource {
				for _, object := range snapshot.Objects {
					if object.ObjectType == want.objectType {
						t.Fatalf("%s system resource projected as tenant data: %+v", want.eventType, object)
					}
				}
				continue
			}
			assertCatalogCoverageProjection(t, snapshot, tenantID, want)
		}
	}
	for _, want := range catalogCoverageExpectations("catalog-tenant-a") {
		if want.systemResource {
			assertCatalogCoverageProjection(t, catalogCoverageSnapshot(t, ctx, graph, defaultSystemSupportOrganizationID), defaultSystemSupportOrganizationID, want)
		}
	}
}
