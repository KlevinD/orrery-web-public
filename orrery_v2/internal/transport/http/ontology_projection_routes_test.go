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
	projectionview "orrery_v2/internal/app/ontologyview/projection"
)

type fakeOntologyProjectionService struct {
	principal    identity.Principal
	tenantID     string
	auditID      string
	auditQuery   projectionview.AuditListQuery
	historyQuery projectionview.ReplayHistoryListQuery
	method       string
	err          error
	calls        int
}

type fakeOntologyProjectionCommandService struct {
	principal identity.Principal
	replay    projectionview.ReplayRequest
	ack       projectionview.AcknowledgeRequest
	result    projectionview.MutationResult
	err       error
	calls     int
}

func (f *fakeOntologyProjectionCommandService) Replay(_ context.Context, principal identity.Principal, request projectionview.ReplayRequest) (projectionview.MutationResult, error) {
	f.principal, f.replay = principal, request
	f.calls++
	return f.result, f.err
}

func (f *fakeOntologyProjectionCommandService) Acknowledge(_ context.Context, principal identity.Principal, request projectionview.AcknowledgeRequest) (projectionview.MutationResult, error) {
	f.principal, f.ack = principal, request
	f.calls++
	return f.result, f.err
}

func (f *fakeOntologyProjectionService) Summary(_ context.Context, principal identity.Principal, tenantID string) (ontologycontract.Summary[projectionview.SummaryData], error) {
	f.capture("summary", principal, tenantID, "")
	return ontologycontract.Summary[projectionview.SummaryData]{}, f.err
}

func (f *fakeOntologyProjectionService) ListAudits(_ context.Context, principal identity.Principal, query projectionview.AuditListQuery) (ontologycontract.CursorPage[projectionview.AuditItem], error) {
	f.capture("audits", principal, query.TenantID, "")
	f.auditQuery = query
	return ontologycontract.CursorPage[projectionview.AuditItem]{Items: []projectionview.AuditItem{}, Page: ontologycontract.Page{Limit: query.Limit}}, f.err
}

func (f *fakeOntologyProjectionService) GetAudit(_ context.Context, principal identity.Principal, tenantID, auditID string) (projectionview.AuditDetail, error) {
	f.capture("detail", principal, tenantID, auditID)
	return projectionview.AuditDetail{AuditItem: projectionview.AuditItem{ID: auditID}}, f.err
}

func (f *fakeOntologyProjectionService) ListReplayHistory(_ context.Context, principal identity.Principal, query projectionview.ReplayHistoryListQuery) (ontologycontract.CursorPage[projectionview.ReplayHistoryItem], error) {
	f.capture("replay-history", principal, query.TenantID, query.AuditID)
	f.historyQuery = query
	return ontologycontract.CursorPage[projectionview.ReplayHistoryItem]{Items: []projectionview.ReplayHistoryItem{}, Page: ontologycontract.Page{Limit: query.Limit}}, f.err
}

func (f *fakeOntologyProjectionService) capture(method string, principal identity.Principal, tenantID, auditID string) {
	f.method, f.principal, f.tenantID, f.auditID = method, principal, tenantID, auditID
	f.calls++
}

func TestHTTPRoutesOntologyProjectionReadUseCanonicalPrincipalAndNarrowQueries(t *testing.T) {
	service := &fakeOntologyProjectionService{}
	handler := newTestRouter(RouterOptions{OntologyProjection: service})

	rec := serveOntologyProjectionRequest(handler, "/api/ontology/projections/summary?tenant_id=mission%3A1")
	if rec.Code != http.StatusOK || service.method != "summary" || service.tenantID != "mission:1" || !service.principal.Authenticated() || service.principal.SubjectProvider != "transport" {
		t.Fatalf("summary status=%d service=%+v body=%s", rec.Code, service, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}

	rec = serveOntologyProjectionRequest(handler, "/api/ontology/projections/audits?tenant_id=mission%3A1&status=skipped&reason_code=missing_target&limit=12&cursor=opaque")
	if rec.Code != http.StatusOK || service.method != "audits" || service.auditQuery.Status != "skipped" ||
		service.auditQuery.ReasonCode != "missing_target" || service.auditQuery.Limit != 12 || service.auditQuery.Cursor != "opaque" {
		t.Fatalf("audit list status=%d query=%+v body=%s", rec.Code, service.auditQuery, rec.Body.String())
	}

	rec = serveOntologyProjectionRequest(handler, "/api/ontology/projections/audits/audit-1?tenant_id=mission%3A1")
	if rec.Code != http.StatusOK || service.method != "detail" || service.auditID != "audit-1" {
		t.Fatalf("detail status=%d service=%+v body=%s", rec.Code, service, rec.Body.String())
	}
	rec = serveOntologyProjectionRequest(handler, "/api/ontology/projections/audits/audit-1/replay-history?tenant_id=mission%3A1&limit=7&cursor=history-cursor")
	if rec.Code != http.StatusOK || service.method != "replay-history" || service.historyQuery.AuditID != "audit-1" || service.historyQuery.Limit != 7 || service.historyQuery.Cursor != "history-cursor" {
		t.Fatalf("history status=%d query=%+v body=%s", rec.Code, service.historyQuery, rec.Body.String())
	}
}

func TestHTTPRoutesOntologyProjectionReadRejectMalformedInputBeforeService(t *testing.T) {
	longID := strings.Repeat("a", 257)
	paths := []string{
		"/api/ontology/projections/audits?tenant_id=mission%3A1&status=unknown",
		"/api/ontology/projections/audits?tenant_id=mission%3A1&limit=101",
		"/api/ontology/projections/audits?tenant_id=mission%3A1&reason_code=" + longID,
		"/api/ontology/projections/audits/" + longID + "?tenant_id=mission%3A1",
		"/api/ontology/projections/audits/audit-1/replay-history?tenant_id=mission%3A1&cursor=" + strings.Repeat("x", 4097),
	}
	for _, path := range paths {
		service := &fakeOntologyProjectionService{}
		rec := serveOntologyProjectionRequest(newTestRouter(RouterOptions{OntologyProjection: service}), path)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) || service.calls != 0 {
			t.Fatalf("path=%s status=%d calls=%d body=%s", path, rec.Code, service.calls, rec.Body.String())
		}
	}
}

func TestHTTPRoutesOntologyProjectionReadMapNonDisclosingAndOperationalErrors(t *testing.T) {
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
		service := &fakeOntologyProjectionService{err: tc.err}
		rec := serveOntologyProjectionRequest(newTestRouter(RouterOptions{OntologyProjection: service}), "/api/ontology/projections/audits/secret?tenant_id=mission%3A1")
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), `"code":"`+tc.code+`"`) || strings.Contains(rec.Body.String(), "secret") {
			t.Fatalf("err=%v status=%d body=%s", tc.err, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Retry-After") != tc.retryAfter {
			t.Fatalf("err=%v Retry-After=%q", tc.err, rec.Header().Get("Retry-After"))
		}
	}
	rec := serveOntologyProjectionRequest(newTestRouter(RouterOptions{}), "/api/ontology/projections/summary?tenant_id=mission%3A1")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"dependency_unavailable"`) {
		t.Fatalf("missing service status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHTTPRoutesOntologyProjectionReadCapabilitiesRemainCoarseReadGates(t *testing.T) {
	for _, path := range []string{
		"/api/ontology/projections/summary",
		"/api/ontology/projections/audits",
		"/api/ontology/projections/audits/{audit_id}",
		"/api/ontology/projections/audits/{audit_id}/replay-history",
	} {
		assertCapability(t, defaultPermissionCapabilities(), http.MethodGet, path, "operations.read")
	}
}

func TestHTTPRoutesOntologyProjectionMutationUsesCanonicalPrincipalAndNarrowBodies(t *testing.T) {
	observedAt := time.Unix(30, 0).UTC()
	service := &fakeOntologyProjectionCommandService{result: projectionview.MutationResult{
		AuditID: "audit-1", Status: "replayed", ReasonCode: "missing_target", ObservedAt: &observedAt,
	}}
	handler := newTestRouter(RouterOptions{OntologyProjectionCommand: service})
	rec := serveOntologyProjectionMutationRequest(handler, "/api/ontology/projections/audits/audit-1/replays?tenant_id=mission%3A1", `{}`)
	if rec.Code != http.StatusOK || service.calls != 1 || service.replay.TenantID != "mission:1" || service.replay.AuditID != "audit-1" || service.replay.IdempotencyKey != "ontology-projection-test" || !service.principal.Authenticated() {
		t.Fatalf("replay status=%d service=%+v body=%s", rec.Code, service, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "tenant_id") || strings.Contains(rec.Body.String(), "source_event_id") || strings.Contains(rec.Body.String(), "details") {
		t.Fatalf("replay response leaks internal fields: %s", rec.Body.String())
	}

	service.result.Status = "acknowledged"
	rec = serveOntologyProjectionMutationRequest(handler, "/api/ontology/projections/audits/audit-1/acknowledgements?tenant_id=mission%3A1", `{"reason":"reviewed"}`)
	if rec.Code != http.StatusOK || service.calls != 2 || service.ack.Reason != "reviewed" || service.ack.TenantID != "mission:1" || service.ack.AuditID != "audit-1" || service.ack.IdempotencyKey != "ontology-projection-test" {
		t.Fatalf("ack status=%d service=%+v body=%s", rec.Code, service, rec.Body.String())
	}
}

func TestHTTPRoutesOntologyProjectionMutationRequiresIdempotencyKey(t *testing.T) {
	service := &fakeOntologyProjectionCommandService{}
	handler := newTestRouter(RouterOptions{OntologyProjectionCommand: service})
	req := authedOntologyRequest(http.MethodPost, "/api/ontology/projections/audits/audit-1/replays?tenant_id=mission%3A1", `{}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || service.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", rec.Code, service.calls, rec.Body.String())
	}
}

func TestHTTPRoutesOntologyProjectionMutationRejectsActorInjectionAndCrossOriginCookie(t *testing.T) {
	service := &fakeOntologyProjectionCommandService{result: projectionview.MutationResult{AuditID: "audit-1", Status: "acknowledged"}}
	handler := newTestRouter(RouterOptions{OntologyProjectionCommand: service})
	path := "/api/ontology/projections/audits/audit-1/acknowledgements?tenant_id=mission%3A1"
	rec := serveOntologyProjectionMutationRequest(handler, path, `{"reason":"reviewed","actor_id":"attacker"}`)
	if rec.Code != http.StatusBadRequest || service.calls != 0 {
		t.Fatalf("actor injection status=%d calls=%d body=%s", rec.Code, service.calls, rec.Body.String())
	}

	req := authedOntologyRequest(http.MethodPost, path, `{"reason":"reviewed"}`)
	req.Header.Set("Idempotency-Key", "ack-cross-origin")
	req.AddCookie(&http.Cookie{Name: authAccessTokenCookieName, Value: "iat-cookie"})
	req.Header.Set("Origin", "https://attacker.example")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || service.calls != 0 {
		t.Fatalf("cross-origin status=%d calls=%d body=%s", rec.Code, service.calls, rec.Body.String())
	}

	req = authedOntologyRequest(http.MethodPost, path, `{"reason":"reviewed"}`)
	req.Header.Set("Idempotency-Key", "ack-same-origin")
	req.AddCookie(&http.Cookie{Name: authAccessTokenCookieName, Value: "iat-cookie"})
	req.Header.Set("Origin", "http://example.com")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || service.calls != 1 {
		t.Fatalf("same-origin status=%d calls=%d body=%s", rec.Code, service.calls, rec.Body.String())
	}
}

func TestHTTPRoutesOntologyProjectionDelegatesCrossScopeAuthorizationToScopeGate(t *testing.T) {
	service := &fakeOntologyProjectionCommandService{}
	handler := newTestRouter(RouterOptions{OntologyProjectionCommand: service})
	req := authedOntologyRequest(http.MethodPost, "/api/ontology/projections/audits/audit-1/replays?tenant_id=mission%3A1", `{}`)
	req.Header.Set("X-Tenant-ID", "mission:2")
	req.Header.Set("Idempotency-Key", "cross-scope-command")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || service.calls != 1 || service.replay.TenantID != "mission:1" {
		t.Fatalf("cross-scope mutation status=%d calls=%d request=%+v body=%s", rec.Code, service.calls, service.replay, rec.Body.String())
	}
}

func TestHTTPRoutesOntologyProjectionMutationMapsTenantAndPermissionFailures(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{err: ontologycontract.ErrUnavailable, status: http.StatusNotFound, code: ErrorUnavailable},
		{err: ontologyscope.ErrAccessDenied, status: http.StatusForbidden, code: ErrorForbidden},
		{err: ontologycontract.ErrStateConflict, status: http.StatusConflict, code: ErrorStateConflict},
	} {
		service := &fakeOntologyProjectionCommandService{err: tc.err}
		rec := serveOntologyProjectionMutationRequest(newTestRouter(RouterOptions{OntologyProjectionCommand: service}), "/api/ontology/projections/audits/audit-1/replays?tenant_id=mission%3A1", `{}`)
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), `"code":"`+tc.code+`"`) || strings.Contains(rec.Body.String(), "audit-1") {
			t.Fatalf("err=%v status=%d body=%s", tc.err, rec.Code, rec.Body.String())
		}
	}
}

func TestHTTPRoutesOntologyProjectionMutationCapabilitiesRemainCoarseManageGates(t *testing.T) {
	for _, path := range []string{
		"/api/ontology/projections/audits/{audit_id}/replays",
		"/api/ontology/projections/audits/{audit_id}/acknowledgements",
	} {
		assertCapability(t, defaultPermissionCapabilities(), http.MethodPost, path, "operations.manage")
	}
}

func serveOntologyProjectionRequest(handler http.Handler, path string) *httptest.ResponseRecorder {
	req := authedOntologyRequest(http.MethodGet, path, "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func serveOntologyProjectionMutationRequest(handler http.Handler, path, body string) *httptest.ResponseRecorder {
	req := authedOntologyRequest(http.MethodPost, path, body)
	req.Header.Set("Idempotency-Key", "ontology-projection-test")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
