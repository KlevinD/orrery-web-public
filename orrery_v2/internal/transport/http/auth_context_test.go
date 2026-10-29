package httptransport

import (
	"net/http"
	"net/http/httptest"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/identity"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

func TestViewerFromRequestPrefersPrincipalContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/workforce/members?viewer_id=query-user&viewer_kind=agent&permission_scope=spoof.scope", nil)
	req.Header.Set("X-User-ID", "spoofed-header-user")
	req.Header.Set("X-Viewer-Kind", "agent")
	req.Header.Set("X-Permission-Scope", "spoof.scope")
	req = req.WithContext(identity.ContextWithPrincipal(req.Context(), identity.Principal{
		SubjectProvider: identity.ProviderKratos,
		Subject:         "kratos-user-1",
		UserID:          "human-operator",
		Username:        "operator",
		AgentProfileID:  "profile-operator",
		ViewerKind:      identity.ViewerKindUser,
		CoarseScopes:    []string{"operations.read", "settings.manage"},
	}))

	viewer, err := ViewerFromRequest(req)
	if err != nil {
		t.Fatalf("ViewerFromRequest: %v", err)
	}
	if viewer.ViewerID != "operator" || viewer.ViewerKind != "user" {
		t.Fatalf("viewer = %+v", viewer)
	}
	if viewer.AuthorizationSubjectID != "profile-operator" {
		t.Fatalf("authorization subject = %q", viewer.AuthorizationSubjectID)
	}
	if len(viewer.PermissionScope) != 2 || viewer.PermissionScope[0] != "operations.read" || viewer.PermissionScope[1] != "settings.manage" {
		t.Fatalf("permission scope = %+v", viewer.PermissionScope)
	}
}

func TestViewerFromPrincipalPreservesCanonicalAgentIDCase(t *testing.T) {
	viewer, err := viewerFromPrincipal(identity.Principal{
		SubjectProvider: "transport",
		Subject:         "A000011",
		UserID:          "A000011",
		Username:        "A000011",
		ViewerKind:      identity.ViewerKindAgent,
	})
	if err != nil {
		t.Fatalf("viewerFromPrincipal: %v", err)
	}
	if viewer.ViewerID != "A000011" || viewer.AuthorizationSubjectID != "A000011" || viewer.ViewerKind != missioncontrol.ViewerKindAgent {
		t.Fatalf("canonical agent viewer = %+v", viewer)
	}
}

func TestViewerFromPrincipalMapsOnlyBreakglassIdentityToSystem(t *testing.T) {
	breakglass, err := viewerFromPrincipal(identity.Principal{
		SubjectProvider: identity.ProviderKratos,
		Subject:         "kratos-superadmin",
		UserID:          agentcore.SystemSuperadminPrincipalID,
		Username:        "superadmin",
		AgentProfileID:  agentcore.SystemSuperadminPrincipalID,
		ViewerKind:      identity.ViewerKindUser,
	})
	if err != nil {
		t.Fatalf("viewerFromPrincipal breakglass: %v", err)
	}
	if breakglass.ViewerID != agentcore.SystemSuperadminPrincipalID || breakglass.AuthorizationSubjectID != agentcore.SystemSuperadminPrincipalID || breakglass.ViewerKind != missioncontrol.ViewerKindSystem {
		t.Fatalf("breakglass viewer = %+v", breakglass)
	}

	forged, err := viewerFromPrincipal(identity.Principal{
		SubjectProvider: identity.ProviderKratos,
		Subject:         "kratos-other-user",
		UserID:          "other-user",
		Username:        "other-user",
		AgentProfileID:  agentcore.SystemSuperadminPrincipalID,
		ViewerKind:      identity.ViewerKindUser,
	})
	if err != nil {
		t.Fatalf("viewerFromPrincipal forged: %v", err)
	}
	if forged.ViewerKind == missioncontrol.ViewerKindSystem {
		t.Fatalf("forged breakglass profile mapped to system viewer: %+v", forged)
	}
}

func TestViewerFromRequestAcceptsEventSourceQueryCredentials(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/evaluations/stream?viewer_id=operator&viewer_kind=user&permission_scope=operations.read,operations.manage", nil)
	req.Header.Set("Accept", "text/event-stream")

	viewer, err := ViewerFromRequest(req)
	if err != nil {
		t.Fatalf("ViewerFromRequest: %v", err)
	}
	if viewer.ViewerID != "operator" || viewer.ViewerKind != "user" {
		t.Fatalf("viewer = %+v", viewer)
	}
	if len(viewer.PermissionScope) != 2 || viewer.PermissionScope[0] != "operations.read" || viewer.PermissionScope[1] != "operations.manage" {
		t.Fatalf("permission scope = %+v", viewer.PermissionScope)
	}
}

func TestViewerFromRequestIgnoresQueryCredentialsForJSONRequests(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/evaluations?viewer_id=operator&viewer_kind=user&permission_scope=operations.read", nil)

	if _, err := ViewerFromRequest(req); err == nil {
		t.Fatalf("ViewerFromRequest accepted query credentials without EventSource accept header")
	}
}

func TestViewerFromRequestIgnoresQueryCredentialsForNonStreamRequests(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/workforce/members?viewer_id=operator&viewer_kind=user&permission_scope=operations.read", nil)
	req.Header.Set("Accept", "text/event-stream")

	if _, err := ViewerFromRequest(req); err == nil {
		t.Fatalf("ViewerFromRequest accepted query credentials for non-stream route")
	}
}

func TestAuthTokenFromRequestIgnoresQueryTokenForJSONRequests(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/workforce/members?access_token=secret-token", nil)

	if token := authTokenFromRequest(req); token != "" {
		t.Fatalf("authTokenFromRequest accepted query token for JSON request: %q", token)
	}
}

func TestAuthTokenFromRequestIgnoresQueryTokenForNonStreamEventAccept(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/workforce/members?access_token=secret-token", nil)
	req.Header.Set("Accept", "text/event-stream")

	if token := authTokenFromRequest(req); token != "" {
		t.Fatalf("authTokenFromRequest accepted query token for non-stream route: %q", token)
	}
}

func TestAuthTokenFromRequestRejectsQueryTokenForEventSourceAndRealtime(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/evaluations/stream?access_token=event-token", nil)
	req.Header.Set("Accept", "text/event-stream")
	if token := authTokenFromRequest(req); token != "" {
		t.Fatalf("event stream query token = %q, want empty", token)
	}

	mission := httptest.NewRequest(http.MethodGet, "/api/missions/mission-1/stream?access_token=mission-token", nil)
	mission.Header.Set("Accept", "text/event-stream")
	if token := authTokenFromRequest(mission); token != "" {
		t.Fatalf("mission stream query token = %q, want empty", token)
	}

	realtime := httptest.NewRequest(http.MethodGet, "/api/realtime/connect?access_token=realtime-token", nil)
	if token := authTokenFromRequest(realtime); token != "" {
		t.Fatalf("realtime query token = %q, want empty", token)
	}
}

func TestRequestIsHTTPSTrustsForwardedProtoOnlyFromTrustedProxy(t *testing.T) {
	publicRemote := httptest.NewRequest(http.MethodGet, "/api/auth/login/complete", nil)
	publicRemote = withDeploymentSecurity(publicRemote, deploymentSecurityConfig{trustedProxyCIDRs: []string{"10.0.0.0/8"}})
	publicRemote.RemoteAddr = "203.0.113.10:1234"
	publicRemote.Header.Set("X-Forwarded-Proto", "https")
	if requestIsHTTPS(publicRemote) {
		t.Fatalf("public remote X-Forwarded-Proto should not mark request as HTTPS")
	}

	privateProxy := httptest.NewRequest(http.MethodGet, "/api/auth/login/complete", nil)
	privateProxy = withDeploymentSecurity(privateProxy, deploymentSecurityConfig{trustedProxyCIDRs: []string{"10.0.0.0/8"}})
	privateProxy.RemoteAddr = "10.0.0.5:1234"
	privateProxy.Header.Set("X-Forwarded-Proto", "https")
	if !requestIsHTTPS(privateProxy) {
		t.Fatalf("trusted proxy X-Forwarded-Proto should mark request as HTTPS")
	}

	privateProxy.Header.Set("X-Forwarded-Proto", "http, https")
	if !requestIsHTTPS(privateProxy) {
		t.Fatalf("rightmost proxy-appended X-Forwarded-Proto should define the public scheme")
	}
	privateProxy.Header.Set("X-Forwarded-Proto", "https, http")
	if requestIsHTTPS(privateProxy) {
		t.Fatalf("attacker-controlled leftmost X-Forwarded-Proto should not override the proxy-appended scheme")
	}
}
