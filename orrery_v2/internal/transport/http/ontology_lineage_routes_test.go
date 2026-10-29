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
	lineageview "orrery_v2/internal/app/ontologyview/lineage"
)

type fakeOntologyLineageService struct {
	principal identity.Principal
	tenantID  string
	factID    string
	query     lineageview.FactPageQuery
	method    string
	err       error
	calls     int
}

func (f *fakeOntologyLineageService) Summary(_ context.Context, principal identity.Principal, tenantID string) (ontologycontract.Summary[lineageview.SummaryData], error) {
	f.capture("summary", principal, tenantID, "")
	return ontologycontract.Summary[lineageview.SummaryData]{}, f.err
}

func (f *fakeOntologyLineageService) GetProvenance(_ context.Context, principal identity.Principal, tenantID, factID string) (lineageview.ProvenanceAnchor, error) {
	f.capture("provenance", principal, tenantID, factID)
	return lineageview.ProvenanceAnchor{Fact: lineageview.FactIdentity{ID: factID}}, f.err
}

func (f *fakeOntologyLineageService) ListAssertions(_ context.Context, principal identity.Principal, query lineageview.FactPageQuery) (ontologycontract.CursorPage[lineageview.AssertionItem], error) {
	f.captureQuery("assertions", principal, query)
	return ontologycontract.CursorPage[lineageview.AssertionItem]{Items: []lineageview.AssertionItem{}, Page: ontologycontract.Page{Limit: query.Limit}}, f.err
}

func (f *fakeOntologyLineageService) ListEvidence(_ context.Context, principal identity.Principal, query lineageview.FactPageQuery) (ontologycontract.CursorPage[lineageview.EvidenceItem], error) {
	f.captureQuery("evidence", principal, query)
	return ontologycontract.CursorPage[lineageview.EvidenceItem]{Items: []lineageview.EvidenceItem{}, Page: ontologycontract.Page{Limit: query.Limit}}, f.err
}

func (f *fakeOntologyLineageService) ListProjectionTrace(_ context.Context, principal identity.Principal, query lineageview.FactPageQuery) (ontologycontract.CursorPage[lineageview.ProjectionTraceItem], error) {
	f.captureQuery("projection-trace", principal, query)
	return ontologycontract.CursorPage[lineageview.ProjectionTraceItem]{Items: []lineageview.ProjectionTraceItem{}, Page: ontologycontract.Page{Limit: query.Limit}}, f.err
}

func (f *fakeOntologyLineageService) capture(method string, principal identity.Principal, tenantID, factID string) {
	f.method, f.principal, f.tenantID, f.factID = method, principal, tenantID, factID
	f.calls++
}

func (f *fakeOntologyLineageService) captureQuery(method string, principal identity.Principal, query lineageview.FactPageQuery) {
	f.capture(method, principal, query.TenantID, query.FactID)
	f.query = query
}

func TestHTTPRoutesOntologyLineageUseCanonicalPrincipalAndNarrowQueries(t *testing.T) {
	service := &fakeOntologyLineageService{}
	handler := newTestRouter(RouterOptions{OntologyLineage: service})

	rec := serveOntologyLineageRequest(handler, "/api/ontology/lineage/summary?tenant_id=mission%3A1")
	if rec.Code != http.StatusOK || service.method != "summary" || service.tenantID != "mission:1" || !service.principal.Authenticated() || service.principal.SubjectProvider != "transport" {
		t.Fatalf("summary status=%d service=%+v body=%s", rec.Code, service, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}

	rec = serveOntologyLineageRequest(handler, "/api/ontology/lineage/facts/fact-1/provenance?tenant_id=mission%3A1")
	if rec.Code != http.StatusOK || service.method != "provenance" || service.factID != "fact-1" {
		t.Fatalf("provenance status=%d service=%+v", rec.Code, service)
	}
	for _, endpoint := range []struct {
		path   string
		method string
	}{
		{"assertions", "assertions"},
		{"evidence", "evidence"},
		{"projection-trace", "projection-trace"},
	} {
		rec = serveOntologyLineageRequest(handler, "/api/ontology/lineage/facts/fact-1/"+endpoint.path+"?tenant_id=mission%3A1&limit=12&cursor=opaque")
		if rec.Code != http.StatusOK || service.method != endpoint.method || service.query.FactID != "fact-1" || service.query.Limit != 12 || service.query.Cursor != "opaque" {
			t.Fatalf("%s status=%d query=%+v body=%s", endpoint.path, rec.Code, service.query, rec.Body.String())
		}
	}
}

func TestHTTPRoutesOntologyLineageRejectMalformedInputBeforeService(t *testing.T) {
	longID := strings.Repeat("a", 257)
	paths := []string{
		"/api/ontology/lineage/facts/" + longID + "/provenance?tenant_id=mission%3A1",
		"/api/ontology/lineage/facts/fact-1/assertions?tenant_id=mission%3A1&limit=0",
		"/api/ontology/lineage/facts/fact-1/evidence?tenant_id=mission%3A1&limit=101",
		"/api/ontology/lineage/facts/fact-1/evidence?tenant_id=mission%3A1&cursor=" + strings.Repeat("x", 4097),
	}
	for _, path := range paths {
		service := &fakeOntologyLineageService{}
		rec := serveOntologyLineageRequest(newTestRouter(RouterOptions{OntologyLineage: service}), path)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) || service.calls != 0 {
			t.Fatalf("path=%s status=%d calls=%d body=%s", path, rec.Code, service.calls, rec.Body.String())
		}
	}
}

func TestHTTPRoutesOntologyLineageMapNonDisclosingAndOperationalErrors(t *testing.T) {
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
		service := &fakeOntologyLineageService{err: tc.err}
		rec := serveOntologyLineageRequest(newTestRouter(RouterOptions{OntologyLineage: service}), "/api/ontology/lineage/facts/secret/provenance?tenant_id=mission%3A1")
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), `"code":"`+tc.code+`"`) || strings.Contains(rec.Body.String(), "secret") {
			t.Fatalf("err=%v status=%d body=%s", tc.err, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Retry-After") != tc.retryAfter {
			t.Fatalf("err=%v Retry-After=%q", tc.err, rec.Header().Get("Retry-After"))
		}
	}
	rec := serveOntologyLineageRequest(newTestRouter(RouterOptions{}), "/api/ontology/lineage/summary?tenant_id=mission%3A1")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"dependency_unavailable"`) {
		t.Fatalf("missing service status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHTTPRoutesOntologyLineageCapabilitiesRemainCoarseReadGates(t *testing.T) {
	for _, path := range []string{
		"/api/ontology/lineage/summary",
		"/api/ontology/lineage/facts/{fact_id}/provenance",
		"/api/ontology/lineage/facts/{fact_id}/assertions",
		"/api/ontology/lineage/facts/{fact_id}/evidence",
		"/api/ontology/lineage/facts/{fact_id}/projection-trace",
	} {
		assertCapability(t, defaultPermissionCapabilities(), http.MethodGet, path, "operations.read")
	}
}

func serveOntologyLineageRequest(handler http.Handler, path string) *httptest.ResponseRecorder {
	req := authedRequest(http.MethodGet, path, "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
