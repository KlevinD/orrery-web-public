package httptransport

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type recordingWebsiteAdvisorHandler struct {
	paths []string
}

func (h *recordingWebsiteAdvisorHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.paths = append(h.paths, r.Method+" "+r.URL.Path)
	w.WriteHeader(http.StatusCreated)
}

func TestWebsiteAdvisorRoutesBypassLoginOnlyForExactReservedRoutes(t *testing.T) {
	advisor := &recordingWebsiteAdvisorHandler{}
	router := NewRouter(RouterOptions{WebsiteAdvisor: advisor})

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/api/advisor/sessions", nil),
		httptest.NewRequest(http.MethodGet, "/api/advisor/faq-suggestions", nil),
		httptest.NewRequest(http.MethodPost, "/api/advisor/human-verification", nil),
		httptest.NewRequest(http.MethodPost, "/api/advisor/sessions/adv_123/messages", nil),
		httptest.NewRequest(http.MethodPost, "/api/advisor/sessions/adv_123/feedback", nil),
		httptest.NewRequest(http.MethodPost, "/api/advisor/sessions/adv_123/leads", nil),
		httptest.NewRequest(http.MethodPost, "/api/advisor/sessions/adv_123/adopt", nil),
		httptest.NewRequest(http.MethodGet, "/api/advisor/account", nil),
		httptest.NewRequest(http.MethodGet, "/api/advisor/conversations", nil),
		httptest.NewRequest(http.MethodGet, "/api/advisor/conversations/webconv_123", nil),
		httptest.NewRequest(http.MethodDelete, "/api/advisor/conversations/webconv_123", nil),
		httptest.NewRequest(http.MethodPost, "/api/advisor/conversations/webconv_123/resume", nil),
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, request)
		if rec.Code != http.StatusCreated {
			t.Fatalf("anonymous advisor route %s %s status = %d", request.Method, request.URL.Path, rec.Code)
		}
	}
	if len(advisor.paths) != 12 {
		t.Fatalf("advisor calls = %+v", advisor.paths)
	}

	protected := httptest.NewRecorder()
	router.ServeHTTP(protected, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if protected.Code != http.StatusUnauthorized {
		t.Fatalf("settings without login status = %d", protected.Code)
	}
}

func TestWebsiteAdvisorReservedNamespaceRejectsSimilarPathsAndMethods(t *testing.T) {
	advisor := &recordingWebsiteAdvisorHandler{}
	router := NewRouter(RouterOptions{WebsiteAdvisor: advisor})
	tests := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/advisor/sessions"},
		{method: http.MethodPost, path: "/api/advisor/faq-suggestions"},
		{method: http.MethodPost, path: "/api/advisor/sessions/"},
		{method: http.MethodPost, path: "/api/advisor/session"},
		{method: http.MethodPost, path: "/api/advisor/sessions/adv_123/messages/"},
		{method: http.MethodPost, path: "/api/advisor/sessions/adv_123/messages/extra"},
		{method: http.MethodPost, path: "/api/advisor/sessions/../settings"},
	}
	for _, test := range tests {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(test.method, test.path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s status = %d", test.method, test.path, rec.Code)
		}
	}
	if len(advisor.paths) != 0 {
		t.Fatalf("similar paths reached advisor handler: %+v", advisor.paths)
	}
}
