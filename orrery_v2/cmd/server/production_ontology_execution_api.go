package main

import (
	"context"
	"strings"

	"orrery_v2/internal/app/identity"
	contextview "orrery_v2/internal/app/ontologyview/context"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type ontologyExecutionContext interface {
	ListObjects(context.Context, identity.Principal, contextview.ObjectListQuery) (ontologycontract.CursorPage[contextview.ObjectItem], error)
	GetObject(context.Context, identity.Principal, string, string) (contextview.ObjectDetail, error)
	Neighborhood(context.Context, identity.Principal, contextview.NeighborhoodQuery) (contextview.Neighborhood, error)
}

type productionOntologyExecutionAPI struct {
	context ontologyExecutionContext
}

func newProductionOntologyExecutionAPI(contextAPI ontologyExecutionContext) *productionOntologyExecutionAPI {
	return &productionOntologyExecutionAPI{context: contextAPI}
}

func (a *productionOntologyExecutionAPI) ListTasks(ctx context.Context, principal identity.Principal, query contextview.ObjectListQuery) (ontologycontract.CursorPage[contextview.ObjectItem], error) {
	query.ObjectType = "agent_task"
	return a.context.ListObjects(ctx, principal, query)
}

func (a *productionOntologyExecutionAPI) GetTask(ctx context.Context, principal identity.Principal, tenantID, id string) (contextview.ObjectDetail, error) {
	return a.getObjectOfType(ctx, principal, tenantID, id, "agent_task")
}

func (a *productionOntologyExecutionAPI) GetRun(ctx context.Context, principal identity.Principal, tenantID, id string) (contextview.ObjectDetail, error) {
	return a.getObjectOfType(ctx, principal, tenantID, agentRunOntologyID(id), "agent_run")
}

func (a *productionOntologyExecutionAPI) RunLineage(ctx context.Context, principal identity.Principal, query contextview.NeighborhoodQuery) (contextview.Neighborhood, error) {
	query.RootID = agentRunOntologyID(query.RootID)
	query.Depth = 2
	query.MaxNodes = 100
	query.MaxEdges = 200
	return a.context.Neighborhood(ctx, principal, query)
}

func (a *productionOntologyExecutionAPI) getObjectOfType(ctx context.Context, principal identity.Principal, tenantID, id, objectType string) (contextview.ObjectDetail, error) {
	if a == nil || a.context == nil {
		return contextview.ObjectDetail{}, contextview.ErrUnavailable
	}
	detail, err := a.context.GetObject(ctx, principal, tenantID, id)
	if err != nil {
		return contextview.ObjectDetail{}, err
	}
	if detail.ObjectType != objectType {
		return contextview.ObjectDetail{}, contextview.ErrUnavailable
	}
	return detail, nil
}

func agentRunOntologyID(id string) string {
	id = strings.TrimSpace(id)
	if strings.HasPrefix(id, "agent-run:") {
		return id
	}
	return "agent-run:" + id
}
