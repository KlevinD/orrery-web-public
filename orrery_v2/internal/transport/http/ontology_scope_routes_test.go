package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authorizationapp "orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	"orrery_v2/internal/app/operations"
)

type fakeOntologyScopeService struct {
	principal identity.Principal
	query     ontologyscope.CatalogQuery
	tenantID  string
	page      ontologycontract.CursorPage[ontologyscope.ScopeCatalogItem]
	resolved  ontologyscope.ResolvedScope
	err       error
}

func (f *fakeOntologyScopeService) List(_ context.Context, principal identity.Principal, query ontologyscope.CatalogQuery) (ontologycontract.CursorPage[ontologyscope.ScopeCatalogItem], error) {
	f.principal = principal
	f.query = query
	return f.page, f.err
}

func (f *fakeOntologyScopeService) Resolve(_ context.Context, principal identity.Principal, tenantID string) (ontologyscope.ResolvedScope, error) {
	f.principal = principal
	f.tenantID = tenantID
	return f.resolved, f.err
}

func TestHTTPRoutesOntologyScopesUseCanonicalPrincipalAndDoNotRequireTenant(t *testing.T) {
	service := &fakeOntologyScopeService{page: ontologycontract.CursorPage[ontologyscope.ScopeCatalogItem]{
		Items: []ontologyscope.ScopeCatalogItem{{TenantID: "project:1", Kind: ontologyscope.KindProject, Label: "Project One", Capabilities: []ontologyscope.ScopeCapability{ontologyscope.CapabilityRead}}},
		Page:  ontologycontract.Page{Limit: 20},
	}}
	handler := newTestRouter(RouterOptions{OntologyScopes: service})
	req := authedRequest(http.MethodGet, "/api/ontology/scopes?search=project&kind=project&limit=20", "")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !service.principal.Authenticated() || service.principal.SubjectProvider != "transport" || service.query.Search != "project" || service.query.Kind != ontologyscope.KindProject || service.query.Limit != 20 {
		t.Fatalf("principal/query = %+v / %+v", service.principal, service.query)
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

func TestHTTPRoutesOntologyScopesAllowTrustedInternalReadScope(t *testing.T) {
	service := &fakeOntologyScopeService{page: ontologycontract.CursorPage[ontologyscope.ScopeCatalogItem]{
		Page: ontologycontract.Page{Limit: 1},
	}}
	handler := NewRouter(RouterOptions{
		OntologyScopes:       service,
		TrustedViewerHeaders: true,
		InternalViewerAuth:   testInternalViewerAuthorization,
		Authorization:        &fakeHTTPAuthorizationService{err: authorizationapp.ErrDenied},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/ontology/scopes?limit=1", nil)
	req.Header.Set("X-Orrery-Internal-Viewer-Authorization", testInternalViewerAuthorization)
	req.Header.Set("X-User-ID", "development-service")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Agent-Profile-ID", "development-service")
	req.Header.Set("X-Permission-Scope", operations.PermissionOperationsRead)
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.principal.Subject != "development-service" || service.principal.AgentProfileID != "development-service" {
		t.Fatalf("principal = %+v", service.principal)
	}
}

func TestHTTPRoutesOntologyScopesRejectTrustedInternalViewerWithoutReadScope(t *testing.T) {
	service := &fakeOntologyScopeService{}
	handler := NewRouter(RouterOptions{
		OntologyScopes:       service,
		TrustedViewerHeaders: true,
		InternalViewerAuth:   testInternalViewerAuthorization,
		Authorization:        &fakeHTTPAuthorizationService{err: authorizationapp.ErrDenied},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/ontology/scopes?limit=1", nil)
	req.Header.Set("X-Orrery-Internal-Viewer-Authorization", testInternalViewerAuthorization)
	req.Header.Set("X-User-ID", "development-service")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Permission-Scope", operations.PermissionOrgRead)
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.principal.Authenticated() {
		t.Fatalf("ontology service was called with principal = %+v", service.principal)
	}
}

func TestHTTPRoutesOntologyScopesResolveAndMapNonDisclosingErrors(t *testing.T) {
	service := &fakeOntologyScopeService{resolved: ontologyscope.ResolvedScope{
		ScopeCatalogItem: ontologyscope.ScopeCatalogItem{TenantID: "mission:1", Kind: ontologyscope.KindMission, Label: "Mission One"},
		BindingVersion:   3,
	}}
	handler := newTestRouter(RouterOptions{OntologyScopes: service})
	req := authedRequest(http.MethodGet, "/api/ontology/scopes/resolve?tenant_id=mission%3A1", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || service.tenantID != "mission:1" {
		t.Fatalf("resolve status=%d tenant=%q body=%s", rec.Code, service.tenantID, rec.Body.String())
	}
	var resolved ontologyscope.ResolvedScope
	if err := json.NewDecoder(rec.Body).Decode(&resolved); err != nil || resolved.BindingVersion != 3 {
		t.Fatalf("resolved = %+v, err=%v", resolved, err)
	}

	service.err = ontologyscope.ErrBindingNotFound
	req = authedRequest(http.MethodGet, "/api/ontology/scopes/resolve?tenant_id=mission%3Asecret", "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"code":"unavailable"`) || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("non-disclosing status=%d body=%s", rec.Code, rec.Body.String())
	}

	service.err = ontologycontract.ErrCursorStale
	req = authedRequest(http.MethodGet, "/api/ontology/scopes?cursor=stale", "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"cursor_stale"`) {
		t.Fatalf("cursor status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHTTPRoutesOntologyScopesRejectInvalidResolveAndUnavailableDependency(t *testing.T) {
	handler := newTestRouter(RouterOptions{OntologyScopes: &fakeOntologyScopeService{}})
	req := authedRequest(http.MethodGet, "/api/ontology/scopes/resolve", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing tenant status=%d body=%s", rec.Code, rec.Body.String())
	}

	handler = newTestRouter(RouterOptions{})
	req = authedRequest(http.MethodGet, "/api/ontology/scopes", "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"dependency_unavailable"`) {
		t.Fatalf("dependency status=%d body=%s", rec.Code, rec.Body.String())
	}
}
