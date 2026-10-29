package main

import (
	"fmt"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datapolicy"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/ontology"
	contextview "orrery_v2/internal/app/ontologyview/context"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	knowledgestore "orrery_v2/internal/knowledge/store"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform/db"
)

func newProductionOntologyContextAPI(
	core *db.Store,
	records *db.Router,
	scopes *productionOntologyScopeState,
	cursors *productionOntologyCursorState,
	concurrency *ontologycontract.ConcurrencyLimiter,
	policy *datapolicy.PlatformStore,
	authorizer *authorization.Service,
	knowledge *knowledgestore.PlatformStore,
	memory *memstore.PlatformStore,
) (*contextview.Service, error) {
	if core == nil || records == nil || scopes == nil || scopes.Gate == nil || scopes.Resolver == nil || cursors == nil || cursors.Stateful == nil || concurrency == nil || policy == nil || authorizer == nil || knowledge == nil || memory == nil {
		return nil, fmt.Errorf("ontology context API dependencies are required")
	}
	access := newProductionContextAccessPolicy(core, records, scopes, authorizer, knowledge, memory)
	data := productionContextDataPolicy{provider: policy, revisions: authorization.NewPolicyRevisionSource(policy)}
	registrations := make([]datavisibility.Registration, 0, 8)
	for _, kind := range []datavisibility.ResourceKind{datavisibility.ResourceObject, datavisibility.ResourceLink, datavisibility.ResourceFact, datavisibility.ResourceEvidence} {
		registrations = append(registrations, datavisibility.Register(kind, access), datavisibility.Register(kind, data))
	}
	return contextview.NewService(
		scopes.Gate,
		contextgraph.NewPlatformStore(core),
		ontology.NewPlatformRegistry(core),
		datavisibility.NewService(registrations...),
		contextview.CursorSet{Encrypted: cursors.Encrypted, Stateful: cursors.Stateful},
		nil,
		concurrency,
	)
}
