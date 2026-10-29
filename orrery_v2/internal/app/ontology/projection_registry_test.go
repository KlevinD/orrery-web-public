package ontology_test

import (
	"context"
	"testing"

	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform/graphprotocol"
)

func TestDefaultOntologyBacksProjectionRegistry(t *testing.T) {
	ctx := context.Background()
	registry := ontology.NewMemoryRegistry()
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projections := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(registry)))

	if err := projections.Register(ctx, graphprotocol.StaticProjection{
		SpecValue: graphprotocol.ProjectionSpec{
			Name:        "mission.task.graph",
			Version:     "v1",
			SourceType:  "mission.task.created",
			ObjectTypes: []string{"mission", "task"},
			LinkTypes:   []string{"mission_has_task"},
		},
	}); err != nil {
		t.Fatalf("Register default ontology projection: %v", err)
	}

	if err := projections.Register(ctx, graphprotocol.StaticProjection{
		SpecValue: graphprotocol.ProjectionSpec{
			Name:        "unknown.graph",
			Version:     "v1",
			SourceType:  "unknown.created",
			ObjectTypes: []string{"unknown_object"},
		},
	}); err == nil {
		t.Fatalf("Register should reject projection with unknown object type")
	}
}
