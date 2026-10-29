package contextview

import (
	"context"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/ontology"
)

type GraphReader interface {
	ListContextTenantIDs(context.Context, int) (contextgraph.ContextTenantPage, error)
	ListContextScopeTenantIDs(context.Context, int) (contextgraph.ContextTenantPage, error)
	ListContextEntityAccessRefs(context.Context, contextgraph.AccessRefQuery) (contextgraph.AccessRefPage, error)
	CountVisibleContext(context.Context, string, contextgraph.VisibilityConstraint) (contextgraph.VisibleContextCounts, error)
	ReadContextOverviewGroups(context.Context, string) ([]contextgraph.ContextOverviewGroup, error)
	ListVisibleObjects(context.Context, contextgraph.ObjectQuery) (contextgraph.ObjectPage, error)
	GetVisibleObject(context.Context, string, string, contextgraph.VisibilityConstraint) (contextgraph.Object, error)
	ListVisibleFacts(context.Context, contextgraph.FactQuery) (contextgraph.FactPage, error)
	ListVisibleRelations(context.Context, contextgraph.RelationQuery) (contextgraph.RelationPage, error)
	ListVisibleEvidence(context.Context, contextgraph.EvidenceQuery) (contextgraph.EvidencePage, error)
	BuildVisibleNeighborhood(context.Context, contextgraph.NeighborhoodQuery) (contextgraph.Neighborhood, error)
}

type VisibilityPolicy interface {
	Evaluate(context.Context, datavisibility.EvaluateRequest) (datavisibility.Decision, error)
}

type DefinitionReader interface {
	GetRegistryDefinitions(context.Context, ontology.RegistryDefinitionKind, []string) ([]ontology.RegistryDefinition, error)
}
