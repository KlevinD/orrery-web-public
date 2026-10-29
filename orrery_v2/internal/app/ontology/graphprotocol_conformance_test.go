package ontology_test

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform/graphprotocol"
)

func TestDefaultOntologySupportsGraphProtocolConformanceGate(t *testing.T) {
	ctx := context.Background()
	registry := ontology.NewMemoryRegistry()
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projection := graphprotocol.StaticProjection{
		SpecValue: graphprotocol.ProjectionSpec{
			Name:        "mission.task.default-ontology",
			Version:     "v1",
			SourceType:  "mission.task.created",
			ObjectTypes: []string{"mission", "task"},
			LinkTypes:   []string{"mission_has_task"},
		},
		ProjectFunc: func(context.Context, graphprotocol.DomainEvent) (graphprotocol.ProjectionResult, error) {
			return graphprotocol.ProjectionResult{
				Cursor: graphprotocol.ProjectionCursor{SourceID: "event-1", SourceVersion: 1},
				Assertions: []graphprotocol.Assertion{
					graphprotocol.ObjectAssertion{
						Object:       graphprotocol.ObjectRef{TenantID: "tenant-1", Type: "mission", ID: "mission-1"},
						CanonicalKey: "mission:mission-1",
						Provenance:   defaultOntologyConformanceProvenance(),
					},
					graphprotocol.ObjectAssertion{
						Object:       graphprotocol.ObjectRef{TenantID: "tenant-1", Type: "task", ID: "task-1"},
						CanonicalKey: "task:task-1",
						Provenance:   defaultOntologyConformanceProvenance(),
					},
					graphprotocol.LinkAssertion{
						LinkType:   "mission_has_task",
						From:       graphprotocol.ObjectRef{TenantID: "tenant-1", Type: "mission", ID: "mission-1"},
						To:         graphprotocol.ObjectRef{TenantID: "tenant-1", Type: "task", ID: "task-1"},
						Provenance: defaultOntologyConformanceProvenance(),
					},
				},
			}, nil
		},
	}

	report := graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(registry)).Check(ctx, projection, graphprotocol.DomainEvent{
		ID:       "event-1",
		TenantID: "tenant-1",
		Type:     "mission.task.created",
		Version:  1,
	})
	if !report.OK {
		t.Fatalf("conformance report should pass with default ontology: %#v", report)
	}
}

func defaultOntologyConformanceProvenance() graphprotocol.ProvenanceRef {
	return graphprotocol.ProvenanceRef{
		SourceEventID:   "event-1",
		SourceEventType: "mission.task.created",
		SourceSystem:    "orrery",
		Actor:           graphprotocol.ActorRef{Kind: "service", ID: "mission"},
		ObservedAt:      time.Unix(10, 0).UTC(),
	}
}
