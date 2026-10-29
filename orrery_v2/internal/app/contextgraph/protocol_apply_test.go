package contextgraph

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform/graphprotocol"
)

func TestApplyProjectionResultStoresAssertionsAndEvidence(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	result := graphprotocol.ProjectionResult{
		Cursor: graphprotocol.ProjectionCursor{SourceID: "event-1", SourceVersion: 3},
		Assertions: []graphprotocol.Assertion{
			graphprotocol.ObjectAssertion{
				Object:       graphprotocol.ObjectRef{TenantID: "tenant-1", Type: "mission", ID: "mission-1"},
				CanonicalKey: "mission:mission-1",
				Label:        "Launch",
				Properties:   map[string]any{"status": "active"},
				Provenance:   contextGraphTestProvenance(),
			},
			graphprotocol.ObjectAssertion{
				Object:       graphprotocol.ObjectRef{TenantID: "tenant-1", Type: "task", ID: "task-1"},
				CanonicalKey: "task:task-1",
				Label:        "Draft plan",
				Provenance:   contextGraphTestProvenance(),
			},
			graphprotocol.LinkAssertion{
				LinkType:   "mission_has_task",
				From:       graphprotocol.ObjectRef{TenantID: "tenant-1", Type: "mission", ID: "mission-1"},
				To:         graphprotocol.ObjectRef{TenantID: "tenant-1", Type: "task", ID: "task-1"},
				Provenance: contextGraphTestProvenance(),
			},
			graphprotocol.FactAssertion{
				Subject:    graphprotocol.ObjectRef{TenantID: "tenant-1", Type: "task", ID: "task-1"},
				Predicate:  "status",
				Value:      "active",
				Provenance: contextGraphTestProvenance(),
			},
		},
	}

	if err := ApplyProjectionResult(ctx, store, result); err != nil {
		t.Fatalf("ApplyProjectionResult returned error: %v", err)
	}
	objects, links, facts, evidence, err := store.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(objects) != 2 || len(links) != 1 || len(facts) != 1 || len(evidence) != 1 {
		t.Fatalf("snapshot objects=%d links=%d facts=%d evidence=%d", len(objects), len(links), len(facts), len(evidence))
	}
	if facts[0].Subject.ID != "task-1" || facts[0].Predicate != "status" || facts[0].EvidenceRefs[0].ID != "event-1" {
		t.Fatalf("fact not mapped from assertion: %#v", facts[0])
	}
	for _, resource := range []struct {
		name  string
		ref   AccessRef
		state AccessState
	}{
		{name: "object", ref: objects[0].OwnerAccessRef, state: objects[0].AccessState},
		{name: "link", ref: links[0].SourceAccessRef, state: links[0].AccessState},
		{name: "fact", ref: facts[0].SourceAccessRef, state: facts[0].AccessState},
		{name: "evidence", ref: evidence[0].SourceAccessRef, state: evidence[0].AccessState},
	} {
		if resource.state != AccessStateResolved || resource.ref.Kind != "mission" || resource.ref.ID != "mission-1" || resource.ref.TenantID != "org-1" {
			t.Fatalf("%s access metadata = %#v/%s", resource.name, resource.ref, resource.state)
		}
	}
}

func TestApplyProjectionResultCreatesAndUpgradesOutOfOrderLinkReference(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	provenance := contextGraphTestProvenance()

	child := graphprotocol.ObjectRef{TenantID: "tenant-1", Type: "organization_unit", ID: "org-child"}
	parent := graphprotocol.ObjectRef{TenantID: "tenant-1", Type: "organization_unit", ID: "org-parent"}
	if err := ApplyProjectionResult(ctx, store, graphprotocol.ProjectionResult{
		Assertions: []graphprotocol.Assertion{
			graphprotocol.ObjectAssertion{
				Object: child, CanonicalKey: child.ID, Label: "Child", Provenance: provenance,
			},
			graphprotocol.LinkAssertion{
				LinkType: "organization_unit_part_of_organization_unit",
				From:     child, To: parent, Provenance: provenance,
			},
		},
	}); err != nil {
		t.Fatalf("apply child before parent: %v", err)
	}

	objects, links, _, _, err := store.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("snapshot child reference: %v", err)
	}
	if len(objects) != 2 || len(links) != 1 {
		t.Fatalf("child projection objects=%d links=%d, want 2/1", len(objects), len(links))
	}
	placeholder := findObject(t, objects, parent.ID)
	if placeholder.Status != "reference_pending" || placeholder.Properties["_orrery_reference"] != "pending" {
		t.Fatalf("parent placeholder = %#v", placeholder)
	}

	if err := ApplyProjectionResult(ctx, store, graphprotocol.ProjectionResult{
		Assertions: []graphprotocol.Assertion{graphprotocol.ObjectAssertion{
			Object: parent, CanonicalKey: parent.ID, Label: "Parent", Properties: map[string]any{"code": "PARENT"}, Provenance: provenance,
		}},
	}); err != nil {
		t.Fatalf("apply real parent: %v", err)
	}
	objects, _, _, _, err = store.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("snapshot upgraded parent: %v", err)
	}
	upgraded := findObject(t, objects, parent.ID)
	if upgraded.Label != "Parent" || upgraded.CanonicalKey != parent.ID || upgraded.Properties["code"] != "PARENT" {
		t.Fatalf("upgraded parent = %#v", upgraded)
	}
	if _, pending := upgraded.Properties["_orrery_reference"]; pending {
		t.Fatalf("upgraded parent retained placeholder marker: %#v", upgraded)
	}
}

func contextGraphTestProvenance() graphprotocol.ProvenanceRef {
	return graphprotocol.ProvenanceRef{
		SourceEventID:   "event-1",
		SourceEventType: "mission.task.updated",
		SourceAccessRef: graphprotocol.AccessRef{Kind: "mission", ID: "mission-1", TenantID: "org-1"},
		SourceSystem:    "orrery",
		Actor:           graphprotocol.ActorRef{Kind: "service", ID: "mission"},
		ObservedAt:      time.Unix(20, 0).UTC(),
	}
}
