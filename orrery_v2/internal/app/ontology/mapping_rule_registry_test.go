package ontology

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/platform/graphprotocol"
)

func TestRegisterMappingRuleProjectionsRegistersEnabledRules(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))

	if err := RegisterMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry, ""); err != nil {
		t.Fatalf("RegisterMappingRuleProjections: %v", err)
	}
	projections := projectionRegistry.ListBySourceType("mission.created")
	if len(projections) != 1 || projections[0].Spec().Name != "mission.created" {
		t.Fatalf("mission.created projections = %+v", projections)
	}
}

func TestRegisterMappingRuleProjectionsRejectsNilProjectionRegistry(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}

	if err := RegisterMappingRuleProjections(ctx, ontologyRegistry, nil, ""); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("RegisterMappingRuleProjections error = %v", err)
	}
}
