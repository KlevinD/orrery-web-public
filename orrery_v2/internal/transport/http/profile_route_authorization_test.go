package httptransport

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthenticatedProfileRouteAllowlistAllowsImplementedWeixinRoutes(t *testing.T) {
	router := &Router{}
	tests := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/profile/weixin"},
		{method: http.MethodDelete, path: "/api/profile/weixin"},
		{method: http.MethodPut, path: "/api/profile/weixin/default-agent"},
		{method: http.MethodPost, path: "/api/profile/weixin/login/start"},
		{method: http.MethodGet, path: "/api/profile/weixin/login/session-1"},
		{method: http.MethodPost, path: "/api/profile/weixin/login/session-1/verify-code"},
		{method: http.MethodGet, path: "/api/profile/identity/feishu"},
		{method: http.MethodDelete, path: "/api/profile/identity/feishu"},
		{method: http.MethodGet, path: "/api/profile/identity/feishu/begin"},
		{method: http.MethodGet, path: "/api/profile/identity/feishu/callback"},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			req := authedRequest(tt.method, tt.path, "")
			if !authenticatedProfileRouteAllowed(req) {
				t.Fatal("implemented self-service route is missing from the allowlist")
			}
			rec := httptest.NewRecorder()
			if !router.authorizeRegisteredRoute(rec, req) {
				t.Fatalf("implemented self-service route was denied: status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAuthenticatedProfileRouteAllowlistRejectsUnknownRoutesAndMethods(t *testing.T) {
	router := &Router{}
	tests := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/profile"},
		{method: http.MethodGet, path: "/api/profile/preferences"},
		{method: http.MethodPatch, path: "/api/profile/weixin"},
		{method: http.MethodGet, path: "/api/profile/weixin/export"},
		{method: http.MethodGet, path: "/api/profile/weixin/login"},
		{method: http.MethodPost, path: "/api/profile/weixin/login/session-1"},
		{method: http.MethodGet, path: "/api/profile/weixin/login/session-1/verify-code"},
		{method: http.MethodPost, path: "/api/profile/weixin/login/session-1/verify-code/extra"},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			req := authedRequest(tt.method, tt.path, "")
			if authenticatedProfileRouteAllowed(req) {
				t.Fatal("unknown profile route must not bypass registered-route authorization")
			}
			rec := httptest.NewRecorder()
			if router.authorizeRegisteredRoute(rec, req) {
				t.Fatal("unknown profile route unexpectedly bypassed registered-route authorization")
			}
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d body = %s, want forbidden", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestProfileSelfServiceRouteStillRequiresAuthentication(t *testing.T) {
	handler := newTestRouter(RouterOptions{})
	req := httptest.NewRequest(http.MethodGet, "/api/profile/weixin", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s, want unauthorized", rec.Code, rec.Body.String())
	}
}
