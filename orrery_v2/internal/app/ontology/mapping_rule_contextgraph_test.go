package ontology

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/graphprotocol"
)

func TestCompiledMappingRuleProjectionAppliesToContextGraph(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	rules, err := ontologyRegistry.ListMappingRules(ctx, "")
	if err != nil {
		t.Fatalf("ListMappingRules: %v", err)
	}

	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	for _, rule := range rules {
		projection, err := CompileMappingRuleProjection(rule)
		if err != nil {
			t.Fatalf("CompileMappingRuleProjection %s: %v", rule.ID, err)
		}
		if err := projectionRegistry.Register(ctx, projection); err != nil {
			t.Fatalf("Register projection %s: %v", rule.ID, err)
		}
	}
	projections := projectionRegistry.ListBySourceType("mission.created")
	if len(projections) != 1 {
		t.Fatalf("mission.created projections = %d, want 1", len(projections))
	}

	event := graphprotocol.DomainEvent{
		ID:         "event-1",
		TenantID:   "tenant-1",
		Type:       "mission.created",
		Version:    1,
		OccurredAt: time.Unix(20, 0).UTC(),
		Payload: map[string]any{
			"id":     "mission-1",
			"title":  "Launch",
			"status": "active",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "mission"},
	}
	gate := graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry))
	report := gate.Check(ctx, projections[0], event)
	if !report.OK {
		t.Fatalf("conformance report = %#v", report)
	}
	result, err := projections[0].Project(ctx, event)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	graphStore := contextgraph.NewMemoryStore()
	if err := contextgraph.ApplyProjectionResult(ctx, graphStore, result); err != nil {
		t.Fatalf("ApplyProjectionResult: %v", err)
	}
	objects, links, facts, evidence, err := graphStore.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(objects) != 1 || objects[0].ObjectType != "mission" || objects[0].ID != "mission-1" {
		t.Fatalf("objects = %+v", objects)
	}
	if len(links) != 0 {
		t.Fatalf("links = %+v", links)
	}
	if len(facts) != 2 {
		t.Fatalf("facts = %+v", facts)
	}
	if len(evidence) != 1 {
		t.Fatalf("evidence = %+v", evidence)
	}
}
