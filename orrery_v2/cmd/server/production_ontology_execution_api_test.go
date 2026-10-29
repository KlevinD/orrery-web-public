package main

import (
	"context"
	"testing"

	"orrery_v2/internal/app/identity"
	contextview "orrery_v2/internal/app/ontologyview/context"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type fakeOntologyExecutionContext struct {
	listQuery contextview.ObjectListQuery
	objectID  string
	lineage   contextview.NeighborhoodQuery
	detail    contextview.ObjectDetail
	details   map[string]contextview.ObjectDetail
}

func (f *fakeOntologyExecutionContext) ListObjects(_ context.Context, _ identity.Principal, query contextview.ObjectListQuery) (ontologycontract.CursorPage[contextview.ObjectItem], error) {
	f.listQuery = query
	return ontologycontract.CursorPage[contextview.ObjectItem]{}, nil
}

func (f *fakeOntologyExecutionContext) GetObject(_ context.Context, _ identity.Principal, _, objectID string) (contextview.ObjectDetail, error) {
	f.objectID = objectID
	if detail, ok := f.details[objectID]; ok {
		return detail, nil
	}
	return f.detail, nil
}

func (f *fakeOntologyExecutionContext) Neighborhood(_ context.Context, _ identity.Principal, query contextview.NeighborhoodQuery) (contextview.Neighborhood, error) {
	f.lineage = query
	return contextview.Neighborhood{RootID: query.RootID}, nil
}

func TestProductionOntologyExecutionAPIScopesTypesAndRunIDs(t *testing.T) {
	ctx := &fakeOntologyExecutionContext{detail: contextview.ObjectDetail{ObjectItem: contextview.ObjectItem{ObjectType: "agent_run"}}}
	api := newProductionOntologyExecutionAPI(ctx)
	principal := identity.Principal{UserID: "human-1", ViewerKind: identity.ViewerKindUser}

	if _, err := api.ListTasks(context.Background(), principal, contextview.ObjectListQuery{TenantID: "tenant-1", ObjectType: "mission"}); err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if ctx.listQuery.ObjectType != "agent_task" {
		t.Fatalf("object type = %q, want agent_task", ctx.listQuery.ObjectType)
	}
	if _, err := api.GetRun(context.Background(), principal, "tenant-1", "runtime-1"); err != nil {
		t.Fatalf("get run: %v", err)
	}
	if ctx.objectID != "agent-run:runtime-1" {
		t.Fatalf("run object ID = %q", ctx.objectID)
	}
	if _, err := api.RunLineage(context.Background(), principal, contextview.NeighborhoodQuery{TenantID: "tenant-1", RootID: "runtime-1"}); err != nil {
		t.Fatalf("run lineage: %v", err)
	}
	if ctx.lineage.RootID != "agent-run:runtime-1" || ctx.lineage.Depth != 2 || ctx.lineage.MaxNodes != 100 || ctx.lineage.MaxEdges != 200 {
		t.Fatalf("lineage query = %+v", ctx.lineage)
	}
}

func TestProductionOntologyExecutionAPIRejectsWrongObjectType(t *testing.T) {
	ctx := &fakeOntologyExecutionContext{detail: contextview.ObjectDetail{ObjectItem: contextview.ObjectItem{ObjectType: "artifact"}}}
	api := newProductionOntologyExecutionAPI(ctx)
	if _, err := api.GetTask(context.Background(), identity.Principal{UserID: "human-1", ViewerKind: identity.ViewerKindUser}, "tenant-1", "task-1"); err != contextview.ErrUnavailable {
		t.Fatalf("GetTask error = %v, want unavailable", err)
	}
}
