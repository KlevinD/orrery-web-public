package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"orrery_v2/internal/app/identity"
)

type fakeOntologyProblemService struct {
	method    string
	query     OntologyProblemListQuery
	tenantID  string
	problemID string
	route     OntologyProblemRouteRequest
}

func (f *fakeOntologyProblemService) ListProblems(_ context.Context, _ identity.Principal, query OntologyProblemListQuery) (OntologyProblemListDTO, error) {
	f.method, f.query = "list", query
	return OntologyProblemListDTO{Items: []OntologyProblemCaseDTO{}, Limit: query.Limit, Returned: 0}, nil
}

func (f *fakeOntologyProblemService) GetProblem(_ context.Context, _ identity.Principal, tenantID, problemID string) (OntologyProblemCaseDTO, error) {
	f.method, f.tenantID, f.problemID = "get", tenantID, problemID
	return OntologyProblemCaseDTO{ID: problemID, TenantID: tenantID, SourceRefs: []OntologyProblemRefDTO{}, EvidenceIDs: []string{}}, nil
}

func (f *fakeOntologyProblemService) RouteProblem(_ context.Context, _ identity.Principal, tenantID, problemID string, request OntologyProblemRouteRequest) (OntologyProblemCaseDTO, error) {
	f.method, f.tenantID, f.problemID, f.route = "route", tenantID, problemID, request
	return OntologyProblemCaseDTO{ID: problemID, TenantID: tenantID, Owner: request.Owner, SourceRefs: []OntologyProblemRefDTO{}, EvidenceIDs: []string{}}, nil
}

func TestHTTPRoutesOntologyProblemHubListDetailAndRoute(t *testing.T) {
	service := &fakeOntologyProblemService{}
	handler := newTestRouter(RouterOptions{OntologyProblems: service})

	rec := serveOntologyProblemRequest(handler, http.MethodGet, "/api/ontology/problems?tenant_id=tenant-1&status=open&source_domain=reliability&search=timeout&limit=25", "", "")
	if rec.Code != http.StatusOK || service.method != "list" || service.query.TenantID != "tenant-1" || service.query.Status != "open" || service.query.SourceDomain != "reliability" || service.query.Search != "timeout" || service.query.Limit != 25 {
		t.Fatalf("list status=%d service=%+v body=%s", rec.Code, service, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}

	rec = serveOntologyProblemRequest(handler, http.MethodGet, "/api/ontology/problems/problem-1?tenant_id=tenant-1", "", "")
	if rec.Code != http.StatusOK || service.method != "get" || service.tenantID != "tenant-1" || service.problemID != "problem-1" {
		t.Fatalf("get status=%d service=%+v body=%s", rec.Code, service, rec.Body.String())
	}

	rec = serveOntologyProblemRequest(handler, http.MethodPost, "/api/ontology/problems/problem-1/route?tenant_id=tenant-1", `{"owner":{"kind":"team","id":"platform"}}`, "route-1")
	if rec.Code != http.StatusOK || service.method != "route" || service.route.IdempotencyKey != "route-1" || service.route.Owner.Kind != "team" || service.route.Owner.ID != "platform" {
		t.Fatalf("route status=%d service=%+v body=%s", rec.Code, service, rec.Body.String())
	}

	assertCapability(t, defaultPermissionCapabilities(), http.MethodGet, "/api/ontology/problems", "operations.read")
	assertCapability(t, defaultPermissionCapabilities(), http.MethodGet, "/api/ontology/problems/{problem_id}", "operations.read")
	assertCapability(t, defaultPermissionCapabilities(), http.MethodPost, "/api/ontology/problems/{problem_id}/route", "operations.manage")
}

func TestHTTPRoutesOntologyProblemHubRejectInvalidAndClientOwnedActor(t *testing.T) {
	service := &fakeOntologyProblemService{}
	handler := newTestRouter(RouterOptions{OntologyProblems: service})
	requests := []struct {
		method string
		path   string
		body   string
		key    string
	}{
		{http.MethodGet, "/api/ontology/problems", "", ""},
		{http.MethodGet, "/api/ontology/problems?tenant_id=tenant-1&limit=101", "", ""},
		{http.MethodPost, "/api/ontology/problems/problem-1/route?tenant_id=tenant-1", `{"owner":{"kind":"team","id":"platform"}}`, ""},
		{http.MethodPost, "/api/ontology/problems/problem-1/route?tenant_id=tenant-1", `{"owner":{"kind":"team","id":"platform"},"actor":{"kind":"human","id":"spoofed"}}`, "route-2"},
	}
	for _, request := range requests {
		rec := serveOntologyProblemRequest(handler, request.method, request.path, request.body, request.key)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s %s status=%d body=%s", request.method, request.path, rec.Code, rec.Body.String())
		}
	}
	if service.method != "" {
		t.Fatalf("invalid request reached service: %+v", service)
	}
}

func serveOntologyProblemRequest(handler http.Handler, method, path, body, key string) *httptest.ResponseRecorder {
	req := authedRequest(method, path, body)
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Origin", "http://example.com")
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
