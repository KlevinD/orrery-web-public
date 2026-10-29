package main

import (
	"context"
	"strings"
	"testing"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/config"
)

func TestDefaultOntologyBootstrapEventsCoverSupportedDefaults(t *testing.T) {
	events, err := defaultOntologyBootstrapEvents(config.Config{})
	if err != nil {
		t.Fatalf("defaultOntologyBootstrapEvents: %v", err)
	}

	want := map[string]bool{
		"organization.created":           false,
		"people.department.created":      false,
		"people.position.created":        false,
		"people.person.updated":          false,
		"people.agent.updated":           false,
		"people.position.assigned":       false,
		"resource.tool.registered":       false,
		"resource.skill.registered":      false,
		"resource.mcp_server.registered": false,
		"knowledge.base.created":         false,
		"memory.profile.created":         false,
	}
	for _, event := range events {
		for _, retiredID := range []string{"legal-orchestrator-agent", "legal-knowledge-agent", "legal-contract-review-agent", "legal-compliance-risk-agent"} {
			if strings.Contains(event.ID, retiredID) || strings.Contains(event.StreamID, retiredID) {
				t.Errorf("default bootstrap retained retired legal agent %s in event %+v", retiredID, event)
			}
		}
		if _, ok := want[event.Type]; ok {
			want[event.Type] = true
		}
	}
	for eventType, found := range want {
		if !found {
			t.Errorf("default bootstrap omitted %s", eventType)
		}
	}
	for _, event := range events {
		if event.SourceAccessRef.Kind == "bootstrap" || event.SourceAccessRef.Kind == "" {
			t.Errorf("bootstrap event %s has no resolvable source access reference", event.ID)
		}
	}
}

func TestDefaultOntologyBootstrapProjectionsWriteDefaultCatalogObjects(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "default-ontology-bootstrap-projection")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatalf("newProductionOntologyProjectionRunner: %v", err)
	}
	if err := applyDefaultOntologyBootstrapProjections(ctx, config.Config{}, runner); err != nil {
		t.Fatalf("applyDefaultOntologyBootstrapProjections: %v", err)
	}
	objects, links, facts, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, defaultSystemSupportOrganizationID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(objects) == 0 || len(links) == 0 || len(facts) == 0 || len(evidence) == 0 {
		t.Fatalf("organization projection empty: objects=%d links=%d facts=%d evidence=%d", len(objects), len(links), len(facts), len(evidence))
	}
	firstObjectCount, firstLinkCount, firstFactCount, firstEvidenceCount := len(objects), len(links), len(facts), len(evidence)
	if err := applyDefaultOntologyBootstrapProjections(ctx, config.Config{}, runner); err != nil {
		t.Fatalf("repeat applyDefaultOntologyBootstrapProjections: %v", err)
	}
	objects, links, facts, evidence, err = contextgraph.NewPlatformStore(core).Snapshot(ctx, defaultSystemSupportOrganizationID)
	if err != nil {
		t.Fatalf("repeat Snapshot: %v", err)
	}
	if len(objects) != firstObjectCount || len(links) != firstLinkCount || len(facts) != firstFactCount || len(evidence) != firstEvidenceCount {
		t.Fatalf("repeat projection duplicated graph records: first=%d/%d/%d/%d repeat=%d/%d/%d/%d", firstObjectCount, firstLinkCount, firstFactCount, firstEvidenceCount, len(objects), len(links), len(facts), len(evidence))
	}
	assertContextGraphObject(t, objects, "knowledge_base", "default-knowledge:"+defaultLeadAgentID)
	assertContextGraphObject(t, objects, "memory_profile", "memory-"+defaultLeadAgentID)
	for _, objectType := range []string{"tool", "skill", "mcp_server"} {
		if !contextGraphObjectsContainType(objects, objectType) {
			t.Errorf("missing default %s projection", objectType)
		}
	}
	systemObjects, _, _, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "system")
	if err != nil {
		t.Fatalf("system Snapshot: %v", err)
	}
	if len(systemObjects) != 0 {
		t.Fatalf("infrastructure scope exposed %d default objects", len(systemObjects))
	}
}

func contextGraphObjectsContainType(objects []contextgraph.Object, objectType string) bool {
	for _, object := range objects {
		if object.ObjectType == objectType {
			return true
		}
	}
	return false
}
