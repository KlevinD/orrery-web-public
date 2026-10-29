package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProductionHandlerWiresDevelopmentProjectAndSCMConnectionRoutes(t *testing.T) {
	_, handler := newProductionRuntimeHandlerForTest(t)
	for _, path := range []string{"/api/development-projects", "/api/scm-connections"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-User-ID", "development-route-user")
		req.Header.Set("X-Agent-Profile-ID", "human-development-route-user")
		req.Header.Set("X-Viewer-Kind", "user")
		req.Header.Set("X-Tenant-ID", "global")
		req.Header.Set("X-Permission-Scope", "development.read,connection.read")
		recorder := httptest.NewRecorder()
		serveServerTestHTTP(t, handler, recorder, req)
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
}
