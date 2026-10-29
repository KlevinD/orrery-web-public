package main

import (
	"fmt"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyreasoning"
	ontologyaggregate "orrery_v2/internal/app/ontologyview/aggregate"
	"orrery_v2/internal/app/operations"
	knowledgestore "orrery_v2/internal/knowledge/store"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform/db"
	httptransport "orrery_v2/internal/transport/http"
)

type productionOntologyAggregateAPIs struct {
	Inference   *productionOntologyInferenceAPI
	Schema      *ontologyaggregate.SchemaService
	Context     *ontologyaggregate.ContextService
	Executions  *ontologyaggregate.ExecutionService
	Lineage     *ontologyaggregate.LineageService
	Governance  *ontologyaggregate.GovernanceService
	Projections *ontologyaggregate.ProjectionService
}

func withProductionOntologyReadAPIs(opts httptransport.RouterOptions, apis *productionOntologyAggregateAPIs) httptransport.RouterOptions {
	opts.OntologySchema, opts.OntologyContext, opts.OntologyInference = apis.Schema, apis.Context, apis.Inference
	opts.OntologyExecutions, opts.OntologyLineage = apis.Executions, apis.Lineage
	opts.OntologyGovernance, opts.OntologyProjection = apis.Governance, apis.Projections
	return opts
}

func newProductionOntologyAggregateReadAPIs(
	core *db.Store,
	records *db.Router,
	security *productionOntologySecurityState,
	catalog ontologyaggregate.ScopeCatalog,
	schema ontologyaggregate.SchemaReader,
	authorizer *authorization.Service,
	knowledge *knowledgestore.PlatformStore,
	memory *memstore.PlatformStore,
	audit *operations.AuditStore,
) (*productionOntologyAggregateAPIs, *productionOntologyExecutionAPI, error) {
	contextAPI, executionAPI, err := newProductionOntologyContextAndExecutionAPIs(core, records, security, authorizer, knowledge, memory)
	if err != nil {
		return nil, nil, err
	}
	lineageAPI, err := newProductionOntologyLineageAPI(core, records, security.Scopes, security.Cursors, security.Concurrency, security.PolicyStore, authorizer, knowledge, memory)
	if err != nil {
		return nil, nil, err
	}
	governanceAPI, err := newProductionOntologyGovernanceReadAPI(core, records, security.Scopes, security.Cursors, security.Concurrency, security.PolicyStore, authorizer, knowledge, memory, audit)
	if err != nil {
		return nil, nil, err
	}
	projectionAPI, err := newProductionOntologyProjectionReadAPI(core, records, security.Scopes, security.Cursors, security.Concurrency, security.PolicyStore, authorizer, knowledge, memory)
	if err != nil {
		return nil, nil, err
	}
	aggregates, err := newProductionOntologyAggregateAPIs(catalog, security.Cursors, schema, contextAPI, executionAPI, lineageAPI, governanceAPI, projectionAPI)
	if err != nil {
		return nil, nil, err
	}
	aggregates.Inference = &productionOntologyInferenceAPI{gate: security.Scopes.Gate, context: contextAPI, registry: ontology.NewPlatformRegistry(core), policy: security.PolicyStore, concurrency: security.Concurrency, lifecycle: &ontologyreasoning.LifecycleStore{Store: core}, service: ontologyreasoning.NewService(ontologyreasoning.ServiceOptions{Store: ontologyreasoning.NewPlatformStore(core)})}
	return aggregates, executionAPI, nil
}

func newProductionOntologyAggregateAPIs(
	catalog ontologyaggregate.ScopeCatalog,
	cursors *productionOntologyCursorState,
	schema ontologyaggregate.SchemaReader,
	contextReader ontologyaggregate.ContextReader,
	executions ontologyaggregate.ExecutionReader,
	lineage ontologyaggregate.LineageReader,
	governance ontologyaggregate.GovernanceReader,
	projections ontologyaggregate.ProjectionReader,
) (*productionOntologyAggregateAPIs, error) {
	if catalog == nil || cursors == nil || cursors.Stateful == nil || schema == nil || contextReader == nil || executions == nil || lineage == nil || governance == nil || projections == nil {
		return nil, fmt.Errorf("ontology aggregate API dependencies are required")
	}
	scopes, err := ontologyaggregate.NewScopeEnumerator(catalog, cursors.Stateful)
	if err != nil {
		return nil, fmt.Errorf("create ontology aggregate scope enumerator: %w", err)
	}
	schemaAPI, err := ontologyaggregate.NewSchemaService(scopes, schema)
	if err != nil {
		return nil, err
	}
	contextAPI, err := ontologyaggregate.NewContextService(scopes, contextReader)
	if err != nil {
		return nil, err
	}
	executionAPI, err := ontologyaggregate.NewExecutionService(scopes, executions)
	if err != nil {
		return nil, err
	}
	lineageAPI, err := ontologyaggregate.NewLineageService(scopes, lineage)
	if err != nil {
		return nil, err
	}
	governanceAPI, err := ontologyaggregate.NewGovernanceService(scopes, governance)
	if err != nil {
		return nil, err
	}
	projectionAPI, err := ontologyaggregate.NewProjectionService(scopes, projections)
	if err != nil {
		return nil, err
	}
	return &productionOntologyAggregateAPIs{
		Schema: schemaAPI, Context: contextAPI, Executions: executionAPI,
		Lineage: lineageAPI, Governance: governanceAPI, Projections: projectionAPI,
	}, nil
}
