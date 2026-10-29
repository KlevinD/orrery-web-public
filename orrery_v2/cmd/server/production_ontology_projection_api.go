package main

import (
	"context"
	"errors"
	"fmt"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datapolicy"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	projectionview "orrery_v2/internal/app/ontologyview/projection"
	"orrery_v2/internal/app/projectadmin"
	knowledgestore "orrery_v2/internal/knowledge/store"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform/db"
)

type productionOntologyProjectionExecutor struct {
	api productionAPI
}

func (e productionOntologyProjectionExecutor) PrepareProjectionAuditReplay(ctx context.Context, audit contextgraph.ProjectionAudit, actor projectionview.CommandActor) (projectionview.ReplayPreparation, error) {
	result, plan, err := e.api.prepareProjectionAuditReplay(ctx, audit)
	if err != nil {
		return projectionview.ReplayPreparation{}, productionProjectionMutationError(err)
	}
	return projectionview.ReplayPreparation{Result: result, Effect: plan.Apply}, nil
}

func (e productionOntologyProjectionExecutor) AcknowledgeProjectionAudit(ctx context.Context, audit contextgraph.ProjectionAudit, actor projectionview.CommandActor, reason string) (contextgraph.ProjectionAudit, error) {
	result, err := e.api.computeProjectionAuditAcknowledgement(ctx, audit, contextgraph.ProjectionAuditAcknowledgeRequest{
		ActorKind: actor.Kind, ActorID: actor.ID, Reason: reason,
	})
	return result, productionProjectionMutationError(err)
}

func newProductionOntologyProjectionReadAPI(
	core *db.Store,
	records *db.Router,
	scopes *productionOntologyScopeState,
	cursors *productionOntologyCursorState,
	concurrency *ontologycontract.ConcurrencyLimiter,
	policy *datapolicy.PlatformStore,
	authorizer *authorization.Service,
	knowledge *knowledgestore.PlatformStore,
	memory *memstore.PlatformStore,
) (*projectionview.Service, error) {
	if core == nil || records == nil || scopes == nil || scopes.Gate == nil || scopes.Resolver == nil || cursors == nil || cursors.Stateful == nil ||
		concurrency == nil || policy == nil || authorizer == nil || knowledge == nil || memory == nil {
		return nil, fmt.Errorf("ontology projection read API dependencies are required")
	}
	visibility, err := newProductionOntologyProjectionVisibility(core, records, scopes, policy, authorizer, knowledge, memory)
	if err != nil {
		return nil, err
	}
	return projectionview.NewService(
		scopes.Gate,
		contextgraph.NewPlatformStore(core),
		visibility,
		projectionview.CursorSet{Encrypted: cursors.Encrypted, Stateful: cursors.Stateful},
		nil,
		concurrency,
	)
}

func newProductionOntologyProjectionCommandAPI(
	core *db.Store,
	records *db.Router,
	scopes *productionOntologyScopeState,
	policy *datapolicy.PlatformStore,
	authorizer *authorization.Service,
	knowledge *knowledgestore.PlatformStore,
	memory *memstore.PlatformStore,
	executor projectionview.CommandExecutor,
) (*projectionview.CommandService, error) {
	if executor == nil {
		return nil, fmt.Errorf("ontology projection command executor is required")
	}
	visibility, err := newProductionOntologyProjectionVisibility(core, records, scopes, policy, authorizer, knowledge, memory)
	if err != nil {
		return nil, err
	}
	return projectionview.NewCommandService(scopes.Gate, contextgraph.NewPlatformStore(core), visibility, executor)
}

func newProductionOntologyProjectionVisibility(
	core *db.Store,
	records *db.Router,
	scopes *productionOntologyScopeState,
	policy *datapolicy.PlatformStore,
	authorizer *authorization.Service,
	knowledge *knowledgestore.PlatformStore,
	memory *memstore.PlatformStore,
) (*datavisibility.Service, error) {
	if core == nil || records == nil || scopes == nil || scopes.Gate == nil || scopes.Resolver == nil || policy == nil || authorizer == nil || knowledge == nil || memory == nil {
		return nil, fmt.Errorf("ontology projection policy dependencies are required")
	}
	access := productionContextAccessPolicy{
		scopes: scopes, authorizer: authorizer, knowledge: knowledge, memory: memory,
		knowledgeACL: knowledgeadmin.NewAccessPolicy(knowledge), records: records, actions: actioncontract.NewPlatformRepository(core), snapshots: scopes.Snapshots,
	}
	data := productionContextDataPolicy{provider: policy, revisions: authorization.NewPolicyRevisionSource(policy)}
	return datavisibility.NewService(
		datavisibility.Register(datavisibility.ResourceAudit, access),
		datavisibility.Register(datavisibility.ResourceAudit, data),
	), nil
}

func productionProjectionMutationError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, projectadmin.ErrInvalidRequest), errors.Is(err, projectadmin.ErrNotFound):
		return fmt.Errorf("%w: projection audit is no longer actionable", ontologycontract.ErrStateConflict)
	case errors.Is(err, projectadmin.ErrServiceDependency):
		return fmt.Errorf("%w: projection dependency is unavailable", ontologyscope.ErrDependencyUnavailable)
	default:
		return err
	}
}
