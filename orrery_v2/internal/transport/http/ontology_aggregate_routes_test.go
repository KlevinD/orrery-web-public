package httptransport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOntologyAggregateRoutesAllowOmittedTenantOnlyOnTopLevelReads(t *testing.T) {
	t.Run("schema", func(t *testing.T) {
		service := &fakeOntologySchemaService{}
		handler := newTestRouter(RouterOptions{OntologySchema: service})
		for _, path := range []string{"/api/ontology/schema/summary", "/api/ontology/schema/definitions", "/api/ontology/schema/graph"} {
			rec := serveOntologySchemaRequest(handler, path)
			if rec.Code != http.StatusOK || service.tenantID != "" {
				t.Fatalf("path=%s status=%d tenant=%q body=%s", path, rec.Code, service.tenantID, rec.Body.String())
			}
		}
		assertAggregateDetailRequiresTenant(t, serveOntologySchemaRequest(handler, "/api/ontology/schema/definitions/object/customer"), service.calls, 3)
	})

	t.Run("context", func(t *testing.T) {
		service := &fakeOntologyContextService{}
		handler := newTestRouter(RouterOptions{OntologyContext: service})
		for _, path := range []string{"/api/ontology/context/summary", "/api/ontology/context/objects"} {
			rec := serveOntologyContextRequest(handler, path)
			if rec.Code != http.StatusOK || service.tenantID != "" {
				t.Fatalf("path=%s status=%d tenant=%q body=%s", path, rec.Code, service.tenantID, rec.Body.String())
			}
		}
		assertAggregateDetailRequiresTenant(t, serveOntologyContextRequest(handler, "/api/ontology/context/objects/object-1"), service.calls, 2)
	})

	t.Run("executions", func(t *testing.T) {
		service := &fakeOntologyExecutionService{}
		handler := newTestRouter(RouterOptions{OntologyExecutions: service})
		rec := serveOntologyExecutionAggregateRequest(handler, "/api/ontology/executions/tasks")
		if rec.Code != http.StatusOK || service.tenantID != "" {
			t.Fatalf("tasks status=%d tenant=%q body=%s", rec.Code, service.tenantID, rec.Body.String())
		}
		rec = serveOntologyExecutionAggregateRequest(handler, "/api/ontology/executions/tasks/task-1")
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) {
			t.Fatalf("detail status=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("lineage", func(t *testing.T) {
		service := &fakeOntologyLineageService{}
		handler := newTestRouter(RouterOptions{OntologyLineage: service})
		rec := serveOntologyLineageRequest(handler, "/api/ontology/lineage/summary")
		if rec.Code != http.StatusOK || service.tenantID != "" {
			t.Fatalf("summary status=%d tenant=%q body=%s", rec.Code, service.tenantID, rec.Body.String())
		}
		rec = serveOntologyLineageRequest(handler, "/api/ontology/lineage/facts/fact-1/provenance")
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) {
			t.Fatalf("detail status=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("governance", func(t *testing.T) {
		service := &fakeOntologyGovernanceService{}
		handler := newTestRouter(RouterOptions{OntologyGovernance: service})
		for _, path := range []string{"/api/ontology/governance/summary", "/api/ontology/governance/proposals"} {
			rec := serveOntologyGovernanceRequest(handler, path)
			if rec.Code != http.StatusOK || service.tenantID != "" {
				t.Fatalf("path=%s status=%d tenant=%q body=%s", path, rec.Code, service.tenantID, rec.Body.String())
			}
		}
		assertAggregateDetailRequiresTenant(t, serveOntologyGovernanceRequest(handler, "/api/ontology/governance/proposals/proposal-1"), service.calls, 2)
	})

	t.Run("projection", func(t *testing.T) {
		service := &fakeOntologyProjectionService{}
		handler := newTestRouter(RouterOptions{OntologyProjection: service})
		for _, path := range []string{"/api/ontology/projections/summary", "/api/ontology/projections/audits"} {
			rec := serveOntologyProjectionRequest(handler, path)
			if rec.Code != http.StatusOK || service.tenantID != "" {
				t.Fatalf("path=%s status=%d tenant=%q body=%s", path, rec.Code, service.tenantID, rec.Body.String())
			}
		}
		assertAggregateDetailRequiresTenant(t, serveOntologyProjectionRequest(handler, "/api/ontology/projections/audits/audit-1"), service.calls, 2)
	})
}

func assertAggregateDetailRequiresTenant(t *testing.T, rec *httptest.ResponseRecorder, calls, expectedCalls int) {
	t.Helper()
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) || calls != expectedCalls {
		t.Fatalf("detail status=%d calls=%d body=%s", rec.Code, calls, rec.Body.String())
	}
}

func serveOntologyExecutionAggregateRequest(handler http.Handler, path string) *httptest.ResponseRecorder {
	req := authedRequest(http.MethodGet, path, "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
