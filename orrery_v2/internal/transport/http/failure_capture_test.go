package httptransport

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orrery_v2/internal/app/identity"
)

func TestRequestFailureKeepsPrivateCauseAndRecoversPanic(t *testing.T) {
	logger := &captureRequestLogger{}
	handler := WithRequestLogger(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/panic" {
			panic("provider failed token=private")
		}
		// Audited handlers nest the response writer; private causes must reach the outer logger.
		nested := &loggingResponseWriter{ResponseWriter: w}
		status, body := MapError(fmt.Errorf("database connection refused password=secret"), correlationID(req))
		writeJSON(nested, status, body)
	}), logger)
	ids := map[string]bool{}
	for _, path := range []string{"/cause", "/panic", "/cause"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 500 {
			t.Fatalf("status = %d", response.Code)
		}
		if strings.Contains(response.Body.String(), "provider") || strings.Contains(response.Body.String(), "database") || strings.Contains(response.Body.String(), "secret") {
			t.Fatal("private cause leaked", response.Body)
		}
		id := response.Header().Get("X-Request-ID")
		if id == "" || ids[id] {
			t.Fatal("request ID missing or reused", id)
		}
		ids[id] = true
	}
	if len(logger.records) != 3 {
		t.Fatal(logger.records)
	}
	for _, record := range logger.records {
		cause, ok := record.fields["error_cause"].(string)
		if !ok || cause == "" || strings.Contains(cause, "private") || strings.Contains(cause, "secret") {
			t.Fatalf("cause not safely preserved: %+v", record)
		}
	}
	if !strings.Contains(logger.records[1].fields["stack"].(string), "TestRequestFailure") {
		t.Fatal("panic stack missing")
	}
}

func TestRequestPanicAfterStreamingHeaderAbortsAndLogs(t *testing.T) {
	logger := &captureRequestLogger{}
	handler := WithRequestLogger(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("partial"))
		panic("stream failed")
	}), logger)
	func() {
		defer func() {
			if recover() != http.ErrAbortHandler {
				t.Error("stream panic did not abort response")
			}
		}()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/stream", nil))
	}()
	if len(logger.records) != 1 || logger.records[0].level != "error" || logger.records[0].fields["error_code"] != "http_response_aborted" {
		t.Fatal(logger.records)
	}
}

type diagnosticServiceStub struct {
	calls     int
	err       error
	principal identity.Principal
}

func (s *diagnosticServiceStub) ReportClientError(_ context.Context, p identity.Principal, _ ClientErrorReport) (ClientErrorReceipt, error) {
	s.calls++
	s.principal = p
	return ClientErrorReceipt{ID: "accepted", Status: "queued"}, s.err
}

func TestClientDiagnosticRouteAuthenticationOriginBoundsAndRateLimit(t *testing.T) {
	service := &diagnosticServiceStub{}
	router := &Router{clientDiagnostics: service}
	handler := http.HandlerFunc(router.handleClientDiagnostics)
	for _, tc := range []struct {
		name, body, origin string
		authenticated      bool
		err                error
		want               int
	}{
		{name: "anonymous", body: `{}`, want: 401},
		{name: "cross origin", body: `{}`, origin: "https://evil.example", authenticated: true, want: 403},
		{name: "unknown field", body: `{"tenant_id":"other"}`, authenticated: true, want: 400},
		{name: "too big", body: `{"message":"` + strings.Repeat("x", 17*1024) + `"}`, authenticated: true, want: 400},
		{name: "accepted", body: `{"id":"one","kind":"javascript","message":"failure","path":"/"}`, authenticated: true, want: 202},
		{name: "rate limited", body: `{}`, authenticated: true, err: ErrDiagnosticRateLimited, want: 429},
		{name: "stale actor", body: `{}`, authenticated: true, err: ErrDiagnosticActorMismatch, want: 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service.err = tc.err
			req := httptest.NewRequest("POST", "http://orrery.local/api/diagnostics/client-errors", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
				req.AddCookie(&http.Cookie{Name: authAccessTokenCookieName, Value: "cookie"})
			}
			if tc.authenticated {
				req = req.WithContext(identity.ContextWithPrincipal(req.Context(), identity.Principal{SubjectProvider: "test", Subject: "user-a", ViewerKind: identity.ViewerKindUser, UserID: "user-a", Username: "alice", TenantID: "tenant-a"}))
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != tc.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
			if tc.want == 429 && response.Header().Get("Retry-After") != "60" {
				t.Fatal("missing retry guidance")
			}
		})
	}
	if service.calls != 3 || service.principal.TenantID != "tenant-a" {
		t.Fatalf("untrusted request reached service: %+v", service)
	}
	// MapError's public representation must never contain its private cause.
	_, body := MapError(errors.New("private"), "request")
	if body.cause == nil {
		t.Fatal("cause missing")
	}
}

type diagnosticBearerStub struct{}

func (diagnosticBearerStub) AuthenticateBearer(context.Context, string) (identity.Principal, error) {
	return identity.Principal{SubjectProvider: "test", Subject: "user-a", ViewerKind: identity.ViewerKindUser, UserID: "user-a", Username: "alice", TenantID: "tenant-a"}, nil
}

func TestClientDiagnosticRouteUsesAuthenticatedSessionRatherThanViewerHeaders(t *testing.T) {
	service := &diagnosticServiceStub{}
	handler := NewRouter(RouterOptions{ClientDiagnostics: service, BearerAuth: diagnosticBearerStub{}})
	for _, authenticated := range []bool{false, true} {
		req := httptest.NewRequest("POST", "http://orrery.local/api/diagnostics/client-errors", strings.NewReader(`{"id":"one","kind":"javascript","message":"failure","path":"/"}`))
		req.Header.Set("X-User-ID", "forged")
		req.Header.Set("X-Tenant-ID", "forged")
		if authenticated {
			req.Header.Set("Authorization", "Bearer test-session")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		want := 401
		if authenticated {
			want = 202
		}
		if response.Code != want {
			t.Fatalf("status=%d body=%s", response.Code, response.Body)
		}
	}
	if service.calls != 1 || service.principal.UserID != "user-a" || service.principal.TenantID != "tenant-a" {
		t.Fatalf("session not used: %+v", service)
	}
}
