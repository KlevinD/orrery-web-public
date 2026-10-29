package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	ontologyschema "orrery_v2/internal/app/ontologyview/schema"
)

type fakeOntologySchemaService struct {
	principal  identity.Principal
	tenantID   string
	listQuery  ontologyschema.DefinitionListQuery
	refQuery   ontologyschema.ReferenceQuery
	graphQuery ontologyschema.GraphQuery
	kind       ontologyschema.DefinitionKind
	id         string
	err        error
	calls      int
}

func (f *fakeOntologySchemaService) Summary(_ context.Context, principal identity.Principal, tenantID string) (ontologycontract.Summary[ontologyschema.SummaryData], error) {
	f.capture(principal, tenantID)
	return ontologycontract.Summary[ontologyschema.SummaryData]{}, f.err
}

func (f *fakeOntologySchemaService) ListDefinitions(_ context.Context, principal identity.Principal, query ontologyschema.DefinitionListQuery) (ontologycontract.CursorPage[ontologyschema.DefinitionItem], error) {
	f.capture(principal, query.TenantID)
	f.listQuery = query
	return ontologycontract.CursorPage[ontologyschema.DefinitionItem]{Items: []ontologyschema.DefinitionItem{}, Page: ontologycontract.Page{Limit: query.Limit}}, f.err
}

func (f *fakeOntologySchemaService) GetDefinition(_ context.Context, principal identity.Principal, tenantID string, kind ontologyschema.DefinitionKind, id string) (ontologyschema.DefinitionDetail, error) {
	f.capture(principal, tenantID)
	f.kind, f.id = kind, id
	return ontologyschema.DefinitionDetail{DefinitionItem: ontologyschema.DefinitionItem{Kind: kind, ID: id}}, f.err
}

func (f *fakeOntologySchemaService) ListReferences(_ context.Context, principal identity.Principal, query ontologyschema.ReferenceQuery) (ontologycontract.CursorPage[ontologyschema.DefinitionReference], error) {
	f.capture(principal, query.TenantID)
	f.refQuery = query
	return ontologycontract.CursorPage[ontologyschema.DefinitionReference]{Items: []ontologyschema.DefinitionReference{}, Page: ontologycontract.Page{Limit: query.Limit}}, f.err
}

func (f *fakeOntologySchemaService) Graph(_ context.Context, principal identity.Principal, query ontologyschema.GraphQuery) (ontologyschema.Graph, error) {
	f.capture(principal, query.TenantID)
	f.graphQuery = query
	return ontologyschema.Graph{View: query.View, Nodes: []ontologyschema.GraphNode{}, Edges: []ontologyschema.GraphEdge{}}, f.err
}

func (f *fakeOntologySchemaService) capture(principal identity.Principal, tenantID string) {
	f.principal = principal
	f.tenantID = tenantID
	f.calls++
}

func TestHTTPRoutesOntologySchemaUseCanonicalPrincipalAndBoundedWireQueries(t *testing.T) {
	service := &fakeOntologySchemaService{}
	handler := newTestRouter(RouterOptions{OntologySchema: service})

	rec := serveOntologySchemaRequest(handler, "/api/ontology/schema/definitions?tenant_id=mission%3A1&kind=object&origin=tenant&status=active&search=customer&limit=25&cursor=opaque")
	if rec.Code != http.StatusOK {
		t.Fatalf("definitions status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !service.principal.Authenticated() || service.principal.SubjectProvider != "transport" || service.tenantID != "mission:1" {
		t.Fatalf("principal/tenant = %+v / %q", service.principal, service.tenantID)
	}
	if service.listQuery.Kind != ontologyschema.KindObject || service.listQuery.Origin != ontologyschema.OriginTenant || service.listQuery.Status != ontologyschema.StatusActive || service.listQuery.Search != "customer" || service.listQuery.Limit != 25 || service.listQuery.Cursor != "opaque" {
		t.Fatalf("list query = %+v", service.listQuery)
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}

	rec = serveOntologySchemaRequest(handler, "/api/ontology/schema/definitions/object/customer/references?tenant_id=mission%3A1&direction=in&limit=10")
	if rec.Code != http.StatusOK || service.refQuery.Kind != ontologyschema.KindObject || service.refQuery.DefinitionID != "customer" || service.refQuery.Direction != "in" || service.refQuery.Limit != 10 {
		t.Fatalf("references status=%d query=%+v body=%s", rec.Code, service.refQuery, rec.Body.String())
	}

	rec = serveOntologySchemaRequest(handler, "/api/ontology/schema/graph?tenant_id=mission%3A1&view=projection&search=created&max_nodes=120")
	if rec.Code != http.StatusOK || service.graphQuery.View != ontologyschema.GraphProjection || service.graphQuery.Search != "created" || service.graphQuery.MaxNodes != 120 {
		t.Fatalf("graph status=%d query=%+v body=%s", rec.Code, service.graphQuery, rec.Body.String())
	}
}

func TestHTTPRoutesOntologySchemaRejectMalformedWireInputBeforeService(t *testing.T) {
	cases := []string{
		"/api/ontology/schema/definitions?tenant_id=mission%3A1&kind=unknown",
		"/api/ontology/schema/definitions?tenant_id=mission%3A1&limit=0",
		"/api/ontology/schema/definitions/object/id/references?tenant_id=mission%3A1&direction=sideways",
		"/api/ontology/schema/graph?tenant_id=mission%3A1&view=everything",
		"/api/ontology/schema/graph?tenant_id=mission%3A1&max_nodes=501",
	}
	for _, path := range cases {
		service := &fakeOntologySchemaService{}
		rec := serveOntologySchemaRequest(newTestRouter(RouterOptions{OntologySchema: service}), path)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) || service.calls != 0 {
			t.Fatalf("path=%s status=%d calls=%d body=%s", path, rec.Code, service.calls, rec.Body.String())
		}
	}
}

func TestHTTPRoutesOntologySchemaMapsNonDisclosingAndOperationalErrors(t *testing.T) {
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
		service := &fakeOntologySchemaService{err: tc.err}
		rec := serveOntologySchemaRequest(newTestRouter(RouterOptions{OntologySchema: service}), "/api/ontology/schema/definitions/object/secret?tenant_id=mission%3A1")
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), `"code":"`+tc.code+`"`) || strings.Contains(rec.Body.String(), "secret") {
			t.Fatalf("err=%v status=%d body=%s", tc.err, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Retry-After"); got != tc.retryAfter {
			t.Fatalf("err=%v Retry-After=%q want=%q", tc.err, got, tc.retryAfter)
		}
	}

	rec := serveOntologySchemaRequest(newTestRouter(RouterOptions{}), "/api/ontology/schema/summary?tenant_id=mission%3A1")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"dependency_unavailable"`) {
		t.Fatalf("missing service status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func serveOntologySchemaRequest(handler http.Handler, path string) *httptest.ResponseRecorder {
	req := authedRequest(http.MethodGet, path, "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
