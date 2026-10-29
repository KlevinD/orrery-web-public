package httptransport

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	contextview "orrery_v2/internal/app/ontologyview/context"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type fakeOntologyContextService struct {
	principal     identity.Principal
	tenantID      string
	objectID      string
	objectQuery   contextview.ObjectListQuery
	childQuery    contextview.ChildQuery
	relationQuery contextview.RelationListQuery
	graphQuery    contextview.NeighborhoodQuery
	method        string
	err           error
	calls         int
}

func (f *fakeOntologyContextService) Summary(_ context.Context, principal identity.Principal, tenantID string) (ontologycontract.Summary[contextview.SummaryData], error) {
	f.capture("summary", principal, tenantID)
	return ontologycontract.Summary[contextview.SummaryData]{}, f.err
}

func (f *fakeOntologyContextService) CompanyOverview(_ context.Context, principal identity.Principal) (ontologycontract.Summary[contextview.CompanyOverviewData], error) {
	f.capture("company-overview", principal, "")
	return ontologycontract.Summary[contextview.CompanyOverviewData]{}, f.err
}

func (f *fakeOntologyContextService) Overview(_ context.Context, principal identity.Principal, tenantID string) (ontologycontract.Summary[contextview.CompanyOverviewData], error) {
	f.capture("overview", principal, tenantID)
	return ontologycontract.Summary[contextview.CompanyOverviewData]{}, f.err
}

func (f *fakeOntologyContextService) ListObjects(_ context.Context, principal identity.Principal, query contextview.ObjectListQuery) (ontologycontract.CursorPage[contextview.ObjectItem], error) {
	f.capture("objects", principal, query.TenantID)
	f.objectQuery = query
	return ontologycontract.CursorPage[contextview.ObjectItem]{Items: []contextview.ObjectItem{}, Page: ontologycontract.Page{Limit: query.Limit}}, f.err
}

func (f *fakeOntologyContextService) GetObject(_ context.Context, principal identity.Principal, tenantID, objectID string) (contextview.ObjectDetail, error) {
	f.capture("detail", principal, tenantID)
	f.objectID = objectID
	return contextview.ObjectDetail{ObjectItem: contextview.ObjectItem{ID: objectID}}, f.err
}

func (f *fakeOntologyContextService) ListFacts(_ context.Context, principal identity.Principal, query contextview.ChildQuery) (ontologycontract.CursorPage[contextview.FactItem], error) {
	f.capture("facts", principal, query.TenantID)
	f.childQuery = query
	return ontologycontract.CursorPage[contextview.FactItem]{Items: []contextview.FactItem{}, Page: ontologycontract.Page{Limit: query.Limit}}, f.err
}

func (f *fakeOntologyContextService) ListRelations(_ context.Context, principal identity.Principal, query contextview.RelationListQuery) (ontologycontract.CursorPage[contextview.RelationItem], error) {
	f.capture("relations", principal, query.TenantID)
	f.relationQuery = query
	return ontologycontract.CursorPage[contextview.RelationItem]{Items: []contextview.RelationItem{}, Page: ontologycontract.Page{Limit: query.Limit}}, f.err
}

func (f *fakeOntologyContextService) ListEvidence(_ context.Context, principal identity.Principal, query contextview.ChildQuery) (ontologycontract.CursorPage[contextview.EvidenceItem], error) {
	f.capture("evidence", principal, query.TenantID)
	f.childQuery = query
	return ontologycontract.CursorPage[contextview.EvidenceItem]{Items: []contextview.EvidenceItem{}, Page: ontologycontract.Page{Limit: query.Limit}}, f.err
}

func (f *fakeOntologyContextService) Neighborhood(_ context.Context, principal identity.Principal, query contextview.NeighborhoodQuery) (contextview.Neighborhood, error) {
	f.capture("neighborhood", principal, query.TenantID)
	f.graphQuery = query
	return contextview.Neighborhood{RootID: query.RootID, Nodes: []contextview.NeighborhoodNode{}, Edges: []contextview.NeighborhoodEdge{}}, f.err
}

func (f *fakeOntologyContextService) capture(method string, principal identity.Principal, tenantID string) {
	f.method, f.principal, f.tenantID = method, principal, tenantID
	f.calls++
}

func TestHTTPRoutesOntologyContextUseCanonicalPrincipalAndNarrowWireQueries(t *testing.T) {
	service := &fakeOntologyContextService{}
	handler := newTestRouter(RouterOptions{OntologyContext: service})
	rec := serveOntologyContextRequest(handler, "/api/ontology/context/company-overview")
	if rec.Code != http.StatusOK || service.method != "company-overview" || service.tenantID != "" || !service.principal.Authenticated() || service.principal.SubjectProvider != "transport" {
		t.Fatalf("company overview status=%d method=%s principal=%+v tenant=%q body=%s", rec.Code, service.method, service.principal, service.tenantID, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("company overview Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}

	rec = serveOntologyContextRequest(handler, "/api/ontology/context/overview?tenant_id=mission%3A1")
	if rec.Code != http.StatusOK || service.method != "overview" || service.tenantID != "mission:1" || !service.principal.Authenticated() || service.principal.SubjectProvider != "transport" {
		t.Fatalf("overview status=%d method=%s principal=%+v tenant=%q body=%s", rec.Code, service.method, service.principal, service.tenantID, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("overview Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}

	rec = serveOntologyContextRequest(handler, "/api/ontology/context/objects?tenant_id=mission%3A1&object_type=task&status=active&source=mission&search=launch&limit=25&cursor=opaque")
	if rec.Code != http.StatusOK || service.method != "objects" || service.tenantID != "mission:1" || !service.principal.Authenticated() || service.principal.SubjectProvider != "transport" {
		t.Fatalf("objects status=%d method=%s principal=%+v tenant=%q body=%s", rec.Code, service.method, service.principal, service.tenantID, rec.Body.String())
	}
	if service.objectQuery.ObjectType != "task" || service.objectQuery.Status != "active" || service.objectQuery.Source != "mission" || service.objectQuery.Search != "launch" || service.objectQuery.Limit != 25 || service.objectQuery.Cursor != "opaque" {
		t.Fatalf("object query = %+v", service.objectQuery)
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}

	rec = serveOntologyContextRequest(handler, "/api/ontology/context/objects/object-1?tenant_id=mission%3A1")
	if rec.Code != http.StatusOK || service.method != "detail" || service.objectID != "object-1" {
		t.Fatalf("detail status=%d service=%+v", rec.Code, service)
	}
	rec = serveOntologyContextRequest(handler, "/api/ontology/context/objects/object-1/facts?tenant_id=mission%3A1&limit=10&cursor=fact-cursor")
	if rec.Code != http.StatusOK || service.method != "facts" || service.childQuery.ObjectID != "object-1" || service.childQuery.Limit != 10 || service.childQuery.Cursor != "fact-cursor" {
		t.Fatalf("facts status=%d query=%+v", rec.Code, service.childQuery)
	}
	rec = serveOntologyContextRequest(handler, "/api/ontology/context/objects/object-1/relations?tenant_id=mission%3A1&direction=in&link_type=depends_on&limit=12&cursor=relation-cursor")
	if rec.Code != http.StatusOK || service.method != "relations" || service.relationQuery.Direction != "in" || service.relationQuery.LinkType != "depends_on" || service.relationQuery.Limit != 12 {
		t.Fatalf("relations status=%d query=%+v", rec.Code, service.relationQuery)
	}
	rec = serveOntologyContextRequest(handler, "/api/ontology/context/objects/object-1/evidence?tenant_id=mission%3A1&limit=8")
	if rec.Code != http.StatusOK || service.method != "evidence" || service.childQuery.Limit != 8 {
		t.Fatalf("evidence status=%d query=%+v", rec.Code, service.childQuery)
	}
	rec = serveOntologyContextRequest(handler, "/api/ontology/context/neighborhood?tenant_id=mission%3A1&root_id=object-1&link_type=depends_on&depth=2&max_nodes=120&max_edges=240")
	if rec.Code != http.StatusOK || service.method != "neighborhood" || service.graphQuery.RootID != "object-1" || service.graphQuery.LinkType != "depends_on" || service.graphQuery.Depth != 2 || service.graphQuery.MaxNodes != 120 || service.graphQuery.MaxEdges != 240 {
		t.Fatalf("neighborhood status=%d query=%+v", rec.Code, service.graphQuery)
	}
	rec = serveOntologyContextRequest(handler, "/api/ontology/context/neighborhood?tenant_id=mission%3A1&root_id=object-1&link_types=has_task%2Cassigned_to&depth=2")
	if rec.Code != http.StatusOK || len(service.graphQuery.LinkTypes) != 2 || service.graphQuery.LinkTypes[0] != "has_task" || service.graphQuery.LinkTypes[1] != "assigned_to" {
		t.Fatalf("allowlisted neighborhood status=%d query=%+v", rec.Code, service.graphQuery)
	}
}

func TestHTTPRoutesOntologyContextRejectMalformedWireInputBeforeService(t *testing.T) {
	longID := strings.Repeat("a", 257)
	longSearch := strings.Repeat("界", 129)
	paths := []string{
		"/api/ontology/context/company-overview?tenant_id=mission%3A1",
		"/api/ontology/context/overview",
		"/api/ontology/context/objects?tenant_id=mission%3A1&limit=0",
		"/api/ontology/context/objects?tenant_id=mission%3A1&search=" + longSearch,
		"/api/ontology/context/objects/" + longID + "?tenant_id=mission%3A1",
		"/api/ontology/context/objects/object-1/relations?tenant_id=mission%3A1&direction=sideways",
		"/api/ontology/context/neighborhood?tenant_id=mission%3A1&root_id=object-1&depth=3",
		"/api/ontology/context/neighborhood?tenant_id=mission%3A1&root_id=object-1&max_nodes=201",
		"/api/ontology/context/neighborhood?tenant_id=mission%3A1&root_id=object-1&max_edges=401",
		"/api/ontology/context/neighborhood?tenant_id=mission%3A1&root_id=object-1&link_type=" + longID,
		"/api/ontology/context/neighborhood?tenant_id=mission%3A1&root_id=object-1&link_types=has_task%2C",
		"/api/ontology/context/neighborhood?tenant_id=mission%3A1&root_id=object-1&link_type=has_task&link_types=assigned_to",
		"/api/ontology/context/neighborhood?tenant_id=mission%3A1&root_id=object-1&link_types=one%2Ctwo%2Cthree%2Cfour%2Cfive%2Csix%2Cseven%2Ceight%2Cnine",
		"/api/ontology/context/neighborhood?tenant_id=mission%3A1",
	}
	for _, path := range paths {
		service := &fakeOntologyContextService{}
		rec := serveOntologyContextRequest(newTestRouter(RouterOptions{OntologyContext: service}), path)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) || service.calls != 0 {
			t.Fatalf("path=%s status=%d calls=%d body=%s", path, rec.Code, service.calls, rec.Body.String())
		}
	}
}

func TestHTTPRoutesOntologyContextMapsNonDisclosingAndOperationalErrors(t *testing.T) {
	cases := []struct {
		err        error
		status     int
		code       string
		retryAfter string
	}{
		{ontologycontract.ErrUnavailable, http.StatusNotFound, ErrorUnavailable, ""},
		{ontologycontract.ErrCursorStale, http.StatusConflict, ErrorCursorStale, ""},
		{ontologyscope.ErrDependencyUnavailable, http.StatusServiceUnavailable, ErrorDependencyUnavailable, ""},
		{ontologycontract.ErrQueryBudgetExceeded, http.StatusServiceUnavailable, ErrorQueryBudgetExceeded, ""},
		{ontologycontract.RateLimitError{RetryAfter: 1500 * time.Millisecond}, http.StatusTooManyRequests, ErrorRateLimited, "2"},
	}
	for _, tc := range cases {
		service := &fakeOntologyContextService{err: tc.err}
		rec := serveOntologyContextRequest(newTestRouter(RouterOptions{OntologyContext: service}), "/api/ontology/context/objects/secret?tenant_id=mission%3A1")
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), `"code":"`+tc.code+`"`) || strings.Contains(rec.Body.String(), "secret") {
			t.Fatalf("err=%v status=%d body=%s", tc.err, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Retry-After") != tc.retryAfter {
			t.Fatalf("err=%v Retry-After=%q", tc.err, rec.Header().Get("Retry-After"))
		}
	}
	rec := serveOntologyContextRequest(newTestRouter(RouterOptions{}), "/api/ontology/context/summary?tenant_id=mission%3A1")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"dependency_unavailable"`) {
		t.Fatalf("missing service status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHTTPRoutesOntologyCompanyOverviewLogsInternalErrorWithoutDisclosingIt(t *testing.T) {
	want := "visibility adapter failed on internal source"
	logger := &captureRequestLogger{}
	service := &fakeOntologyContextService{err: fmt.Errorf("%w: %s", ontologyscope.ErrDependencyUnavailable, want)}
	handler := newTestRouter(RouterOptions{OntologyContext: service, Logger: logger})

	rec := serveOntologyContextRequest(handler, "/api/ontology/context/company-overview")
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), want) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	for _, record := range logger.records {
		if record.msg == "ontology company overview failed" && strings.Contains(fmt.Sprint(record.fields["error"]), want) {
			return
		}
	}
	t.Fatalf("internal error was not logged: %+v", logger.records)
}

func TestHTTPRoutesOntologyContextCapabilitiesRemainCoarseReadGates(t *testing.T) {
	capabilities := defaultPermissionCapabilities()
	for _, path := range []string{
		"/api/ontology/context/company-overview",
		"/api/ontology/context/overview",
		"/api/ontology/context/summary",
		"/api/ontology/context/objects",
		"/api/ontology/context/objects/{object_id}",
		"/api/ontology/context/objects/{object_id}/facts",
		"/api/ontology/context/objects/{object_id}/relations",
		"/api/ontology/context/objects/{object_id}/evidence",
		"/api/ontology/context/neighborhood",
	} {
		assertCapability(t, capabilities, http.MethodGet, path, "operations.read")
	}
}

func serveOntologyContextRequest(handler http.Handler, path string) *httptest.ResponseRecorder {
	req := authedRequest(http.MethodGet, path, "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
