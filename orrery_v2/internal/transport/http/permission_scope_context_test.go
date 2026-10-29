package httptransport

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"orrery_v2/internal/app/identity"
)

func TestEnsureRequestPermissionScopeDoesNotElevateAuthenticatedPrincipal(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/org/roles?organization_code=0101", nil)
	principal := identity.Principal{
		SubjectProvider: "local",
		Subject:         "operator",
		Username:        "operator",
		AgentProfileID:  "human-operator",
		ViewerKind:      identity.ViewerKindUser,
	}
	req = req.WithContext(identity.ContextWithPrincipal(req.Context(), principal))

	ensureRequestPermissionScope(req, "roles.read")
	if req.Header.Get("X-Permission-Scope") != "roles.read" {
		t.Fatalf("request permission header = %q", req.Header.Get("X-Permission-Scope"))
	}
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		t.Fatalf("ViewerFromRequest: %v", err)
	}
	if len(viewer.PermissionScope) != 1 || viewer.PermissionScope[0] != "roles.read" {
		t.Fatalf("effective viewer permission scopes = %+v", viewer.PermissionScope)
	}
	got, ok := identity.PrincipalFromContext(req.Context())
	if !ok {
		t.Fatalf("authenticated principal missing from context")
	}
	if len(got.CoarseScopes) != 0 {
		t.Fatalf("route-scoped permission leaked into server authorization scopes: %+v", got.CoarseScopes)
	}
}

func TestPrepareOperationsQuerySeparatesEffectiveAndAuthorizationScopes(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/org/roles?organization_code=0101", nil)
	principal := identity.Principal{
		SubjectProvider: "local",
		Subject:         "operator",
		Username:        "operator",
		AgentProfileID:  "human-operator",
		ViewerKind:      identity.ViewerKindUser,
		CoarseScopes:    []string{"org.read"},
	}
	req = req.WithContext(identity.ContextWithPrincipal(req.Context(), principal))
	ensureRequestPermissionScope(req, "roles.read")
	router := &Router{operations: &fakeOperationsService{}}

	viewer, ok := router.prepareOperationsQuery(httptest.NewRecorder(), req, "corr-scope")
	if !ok {
		t.Fatalf("prepareOperationsQuery rejected authenticated viewer")
	}
	if len(viewer.PermissionScope) != 2 || viewer.PermissionScope[0] != "org.read" || viewer.PermissionScope[1] != "roles.read" {
		t.Fatalf("effective permission scopes = %+v", viewer.PermissionScope)
	}
	if len(viewer.AuthorizationScope) != 1 || viewer.AuthorizationScope[0] != "org.read" {
		t.Fatalf("authorization scopes = %+v", viewer.AuthorizationScope)
	}
}
