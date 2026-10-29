package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"orrery_v2/internal/app/identity"
	contextview "orrery_v2/internal/app/ontologyview/context"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type fakeOntologyExecutionService struct {
	method   string
	tenantID string
	id       string
}

func (f *fakeOntologyExecutionService) ListTasks(_ context.Context, _ identity.Principal, query contextview.ObjectListQuery) (ontologycontract.CursorPage[contextview.ObjectItem], error) {
	f.method, f.tenantID = "list_tasks", query.TenantID
	return ontologycontract.CursorPage[contextview.ObjectItem]{Items: []contextview.ObjectItem{{ID: "agent-task-1", ObjectType: "agent_task"}}}, nil
}

func (f *fakeOntologyExecutionService) GetTask(_ context.Context, _ identity.Principal, tenantID, id string) (contextview.ObjectDetail, error) {
	f.method, f.tenantID, f.id = "get_task", tenantID, id
	return contextview.ObjectDetail{ObjectItem: contextview.ObjectItem{ID: id, ObjectType: "agent_task"}}, nil
}

func (f *fakeOntologyExecutionService) GetRun(_ context.Context, _ identity.Principal, tenantID, id string) (contextview.ObjectDetail, error) {
	f.method, f.tenantID, f.id = "get_run", tenantID, id
	return contextview.ObjectDetail{ObjectItem: contextview.ObjectItem{ID: id, ObjectType: "agent_run"}}, nil
}

func (f *fakeOntologyExecutionService) RunLineage(_ context.Context, _ identity.Principal, query contextview.NeighborhoodQuery) (contextview.Neighborhood, error) {
	f.method, f.tenantID, f.id = "run_lineage", query.TenantID, query.RootID
	return contextview.Neighborhood{RootID: query.RootID}, nil
}

func TestHTTPRoutesOntologyExecutionsExposeTasksRunsAndLineage(t *testing.T) {
	service := &fakeOntologyExecutionService{}
	handler := newTestRouter(RouterOptions{OntologyExecutions: service})
	paths := []struct {
		path   string
		method string
		id     string
	}{
		{"/api/ontology/executions/tasks?tenant_id=tenant-1", "list_tasks", ""},
		{"/api/ontology/executions/tasks/agent-task-1?tenant_id=tenant-1", "get_task", "agent-task-1"},
		{"/api/ontology/executions/runs/run-1?tenant_id=tenant-1", "get_run", "run-1"},
		{"/api/ontology/executions/runs/run-1/lineage?tenant_id=tenant-1", "run_lineage", "run-1"},
	}
	for _, tc := range paths {
		req := authedRequest(http.MethodGet, tc.path, "")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d body=%s", tc.path, rec.Code, rec.Body.String())
		}
		if service.method != tc.method || service.tenantID != "tenant-1" || service.id != tc.id {
			t.Fatalf("GET %s capture = method:%s tenant:%s id:%s", tc.path, service.method, service.tenantID, service.id)
		}
	}
}

func TestOntologyExecutionRoutesRequireOperationsRead(t *testing.T) {
	capabilities := defaultPermissionCapabilities()
	for _, path := range []string{
		"/api/ontology/executions/tasks",
		"/api/ontology/executions/tasks/{task_id}",
		"/api/ontology/executions/runs/{run_id}",
		"/api/ontology/executions/runs/{run_id}/lineage",
	} {
		assertCapability(t, capabilities, http.MethodGet, path, "operations.read")
	}
}
