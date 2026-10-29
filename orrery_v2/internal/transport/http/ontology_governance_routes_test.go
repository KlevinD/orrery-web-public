package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	governanceview "orrery_v2/internal/app/ontologyview/governance"
)

type fakeOntologyGovernanceService struct {
	principal     identity.Principal
	tenantID      string
	proposalID    string
	proposalQuery governanceview.ProposalListQuery
	receiptQuery  governanceview.ReceiptListQuery
	auditQuery    governanceview.AuditEventListQuery
	method        string
	err           error
	calls         int
}

type fakeOntologyGovernanceCommandService struct {
	principal      identity.Principal
	decision       governanceview.DecideProposalRequest
	plan           governanceview.CreatePromotionPlanRequest
	apply          governanceview.ApplyPromotionPlanRequest
	retry          governanceview.RetryProjectionRefreshRequest
	decisionResult ontology.OntologyMetadataProposal
	planResult     governanceview.PromotionPlanResult
	applyResult    ontology.MetadataProposalPromotionReceipt
	retryResult    governanceview.ProjectionRefreshResult
	err            error
	calls          int
}

func (f *fakeOntologyGovernanceCommandService) DecideProposal(_ context.Context, principal identity.Principal, request governanceview.DecideProposalRequest) (ontology.OntologyMetadataProposal, error) {
	f.principal, f.decision = principal, request
	f.calls++
	return f.decisionResult, f.err
}

func (f *fakeOntologyGovernanceCommandService) CreatePromotionPlan(_ context.Context, principal identity.Principal, request governanceview.CreatePromotionPlanRequest) (governanceview.PromotionPlanResult, error) {
	f.principal, f.plan = principal, request
	f.calls++
	return f.planResult, f.err
}

func (f *fakeOntologyGovernanceCommandService) ApplyPromotionPlan(_ context.Context, principal identity.Principal, request governanceview.ApplyPromotionPlanRequest) (ontology.MetadataProposalPromotionReceipt, error) {
	f.principal, f.apply = principal, request
	f.calls++
	return f.applyResult, f.err
}

func (f *fakeOntologyGovernanceCommandService) RetryProjectionRefresh(_ context.Context, principal identity.Principal, request governanceview.RetryProjectionRefreshRequest) (governanceview.ProjectionRefreshResult, error) {
	f.principal, f.retry = principal, request
	f.calls++
	return f.retryResult, f.err
}

func (f *fakeOntologyGovernanceService) Summary(_ context.Context, principal identity.Principal, tenantID string) (ontologycontract.Summary[governanceview.SummaryData], error) {
	f.capture("summary", principal, tenantID, "")
	return ontologycontract.Summary[governanceview.SummaryData]{}, f.err
}

func (f *fakeOntologyGovernanceService) ListProposals(_ context.Context, principal identity.Principal, query governanceview.ProposalListQuery) (ontologycontract.CursorPage[governanceview.ProposalItem], error) {
	f.capture("proposals", principal, query.TenantID, "")
	f.proposalQuery = query
	return ontologycontract.CursorPage[governanceview.ProposalItem]{Items: []governanceview.ProposalItem{}, Page: ontologycontract.Page{Limit: query.Limit}}, f.err
}

func (f *fakeOntologyGovernanceService) GetProposal(_ context.Context, principal identity.Principal, tenantID, proposalID string) (governanceview.ProposalDetail, error) {
	f.capture("detail", principal, tenantID, proposalID)
	return governanceview.ProposalDetail{ProposalItem: governanceview.ProposalItem{ID: proposalID}}, f.err
}

func (f *fakeOntologyGovernanceService) AnalyzeProposal(_ context.Context, principal identity.Principal, tenantID, proposalID string) (governanceview.AnalysisReport, error) {
	f.capture("analysis", principal, tenantID, proposalID)
	return governanceview.AnalysisReport{Issues: []governanceview.AnalysisIssue{}}, f.err
}

func (f *fakeOntologyGovernanceService) ListReceipts(_ context.Context, principal identity.Principal, query governanceview.ReceiptListQuery) (ontologycontract.CursorPage[governanceview.ReceiptItem], error) {
	f.capture("receipts", principal, query.TenantID, query.ProposalID)
	f.receiptQuery = query
	return ontologycontract.CursorPage[governanceview.ReceiptItem]{Items: []governanceview.ReceiptItem{}, Page: ontologycontract.Page{Limit: query.Limit}}, f.err
}

func (f *fakeOntologyGovernanceService) ListAuditEvents(_ context.Context, principal identity.Principal, query governanceview.AuditEventListQuery) (ontologycontract.CursorPage[governanceview.AuditEventItem], error) {
	f.capture("audit-events", principal, query.TenantID, query.ProposalID)
	f.auditQuery = query
	return ontologycontract.CursorPage[governanceview.AuditEventItem]{Items: []governanceview.AuditEventItem{}, Page: ontologycontract.Page{Limit: query.Limit}}, f.err
}

func (f *fakeOntologyGovernanceService) capture(method string, principal identity.Principal, tenantID, proposalID string) {
	f.method, f.principal, f.tenantID, f.proposalID = method, principal, tenantID, proposalID
	f.calls++
}

func TestHTTPRoutesOntologyGovernanceReadUseCanonicalPrincipalAndNarrowQueries(t *testing.T) {
	service := &fakeOntologyGovernanceService{}
	handler := newTestRouter(RouterOptions{OntologyGovernance: service})

	rec := serveOntologyGovernanceRequest(handler, "/api/ontology/governance/summary?tenant_id=mission%3A1")
	if rec.Code != http.StatusOK || service.method != "summary" || service.tenantID != "mission:1" || !service.principal.Authenticated() || service.principal.SubjectProvider != "transport" {
		t.Fatalf("summary status=%d service=%+v body=%s", rec.Code, service, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}

	rec = serveOntologyGovernanceRequest(handler, "/api/ontology/governance/proposals?tenant_id=mission%3A1&queue=all&status=accepted&kind=object_type&search=owner&limit=12&cursor=opaque")
	if rec.Code != http.StatusOK || service.method != "proposals" || service.proposalQuery.Queue != governanceview.QueueAll || service.proposalQuery.Status != "accepted" ||
		service.proposalQuery.TargetKind != "object_type" || service.proposalQuery.Search != "owner" || service.proposalQuery.Limit != 12 || service.proposalQuery.Cursor != "opaque" {
		t.Fatalf("proposal list status=%d query=%+v body=%s", rec.Code, service.proposalQuery, rec.Body.String())
	}

	for _, endpoint := range []struct {
		path   string
		method string
	}{
		{"", "detail"},
		{"/analysis", "analysis"},
		{"/receipts?limit=7&cursor=receipt-cursor", "receipts"},
		{"/audit-events?limit=8&cursor=audit-cursor", "audit-events"},
	} {
		separator := "?"
		if strings.Contains(endpoint.path, "?") {
			separator = "&"
		}
		rec = serveOntologyGovernanceRequest(handler, "/api/ontology/governance/proposals/proposal-1"+endpoint.path+separator+"tenant_id=mission%3A1")
		if rec.Code != http.StatusOK || service.method != endpoint.method || service.proposalID != "proposal-1" {
			t.Fatalf("%s status=%d service=%+v body=%s", endpoint.method, rec.Code, service, rec.Body.String())
		}
	}
	if service.receiptQuery.Limit != 7 || service.receiptQuery.Cursor != "receipt-cursor" || service.auditQuery.Limit != 8 || service.auditQuery.Cursor != "audit-cursor" {
		t.Fatalf("child queries receipt=%+v audit=%+v", service.receiptQuery, service.auditQuery)
	}
}

func TestHTTPRoutesOntologyGovernanceReadRejectMalformedInputBeforeService(t *testing.T) {
	longID := strings.Repeat("a", 257)
	paths := []string{
		"/api/ontology/governance/proposals?tenant_id=mission%3A1&queue=unknown",
		"/api/ontology/governance/proposals?tenant_id=mission%3A1&status=unknown",
		"/api/ontology/governance/proposals?tenant_id=mission%3A1&limit=101",
		"/api/ontology/governance/proposals/" + longID + "?tenant_id=mission%3A1",
		"/api/ontology/governance/proposals/proposal-1/receipts?tenant_id=mission%3A1&cursor=" + strings.Repeat("x", 4097),
	}
	for _, path := range paths {
		service := &fakeOntologyGovernanceService{}
		rec := serveOntologyGovernanceRequest(newTestRouter(RouterOptions{OntologyGovernance: service}), path)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) || service.calls != 0 {
			t.Fatalf("path=%s status=%d calls=%d body=%s", path, rec.Code, service.calls, rec.Body.String())
		}
	}
}

func TestHTTPRoutesOntologyGovernanceReadMapNonDisclosingAndOperationalErrors(t *testing.T) {
	cases := []struct {
		err        error
		status     int
		code       string
		retryAfter string
	}{
		{ontologycontract.ErrUnavailable, http.StatusNotFound, ErrorUnavailable, ""},
		{ontologycontract.ErrCursorStale, http.StatusConflict, ErrorCursorStale, ""},
		{ontologycontract.ErrStateConflict, http.StatusConflict, ErrorStateConflict, ""},
		{ontologyscope.ErrDependencyUnavailable, http.StatusServiceUnavailable, ErrorDependencyUnavailable, ""},
		{ontologycontract.ErrQueryBudgetExceeded, http.StatusServiceUnavailable, ErrorQueryBudgetExceeded, ""},
		{ontologycontract.RateLimitError{RetryAfter: 1500 * time.Millisecond}, http.StatusTooManyRequests, ErrorRateLimited, "2"},
	}
	for _, tc := range cases {
		service := &fakeOntologyGovernanceService{err: tc.err}
		rec := serveOntologyGovernanceRequest(newTestRouter(RouterOptions{OntologyGovernance: service}), "/api/ontology/governance/proposals/secret?tenant_id=mission%3A1")
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), `"code":"`+tc.code+`"`) || strings.Contains(rec.Body.String(), "secret") {
			t.Fatalf("err=%v status=%d body=%s", tc.err, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Retry-After") != tc.retryAfter {
			t.Fatalf("err=%v Retry-After=%q", tc.err, rec.Header().Get("Retry-After"))
		}
	}
	rec := serveOntologyGovernanceRequest(newTestRouter(RouterOptions{}), "/api/ontology/governance/summary?tenant_id=mission%3A1")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"dependency_unavailable"`) {
		t.Fatalf("missing service status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHTTPRoutesOntologyGovernanceMutationUseNarrowBodiesAndSafeResponses(t *testing.T) {
	now := time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC)
	service := &fakeOntologyGovernanceCommandService{
		decisionResult: ontology.OntologyMetadataProposal{ID: "proposal-1", Revision: 4, Status: ontology.MetadataProposalStatusAccepted, ReviewedAt: now},
		planResult: governanceview.PromotionPlanResult{
			PlanID: "opplan-random", ProposalID: "proposal-1", ProposalRevision: 4, ExpiresAt: now.Add(10 * time.Minute),
			Changes: []governanceview.PromotionPlanChangeResult{{Operation: ontology.PromotionOperationUpdate, TargetKind: "mapping_rule", TargetID: "mission.created", Field: "source_domain", CurrentVersion: 2}},
		},
		applyResult: ontology.MetadataProposalPromotionReceipt{
			ReceiptID: "receipt-1", ProposalID: "proposal-1", ProposalRevision: 4, Status: ontology.PromotionReceiptStatusApplied,
			Applied: true, AppliedAt: now, ActorID: "server-actor", IdempotencyKey: "secret-key", ProposalContentHash: "secret-hash",
			ProjectionRefreshStatus: "pending",
			Changes: []ontology.MetadataProposalPromotionChange{{
				Operation: ontology.PromotionOperationUpdate, TargetKind: "mapping_rule", TargetID: "mission.created", Field: "source_domain",
				CurrentValue: "mission", ProposedValue: "planning", AppliedVersion: 3,
			}},
		},
		retryResult: governanceview.ProjectionRefreshResult{ReceiptID: "receipt-1", ProposalID: "proposal-1", Status: "succeeded"},
	}
	handler := newTestRouter(RouterOptions{OntologyGovernanceCommand: service})

	rec := serveOntologyGovernanceMutationRequest(handler, "/api/ontology/governance/proposals/proposal-1/decisions?tenant_id=mission%3A1", `{"decision":"accepted","reason":"reviewed","expected_revision":3,"evidence_refs":[{"kind":"run","id":"run-1"}]}`)
	if rec.Code != http.StatusOK || service.decision.TenantID != "mission:1" || service.decision.ProposalID != "proposal-1" || service.decision.ExpectedRevision != 3 || service.decision.Reason != "reviewed" || !service.principal.Authenticated() {
		t.Fatalf("decision status=%d request=%+v principal=%+v body=%s", rec.Code, service.decision, service.principal, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "server-actor") || !strings.Contains(rec.Body.String(), `"revision":4`) {
		t.Fatalf("decision response = %s", rec.Body.String())
	}

	rec = serveOntologyGovernanceMutationRequest(handler, "/api/ontology/governance/proposals/proposal-1/plans?tenant_id=mission%3A1", `{"expected_revision":4}`)
	if rec.Code != http.StatusCreated || service.plan.ExpectedRevision != 4 || strings.Contains(rec.Body.String(), "current_value") || strings.Contains(rec.Body.String(), "proposed_value") {
		t.Fatalf("plan status=%d request=%+v body=%s", rec.Code, service.plan, rec.Body.String())
	}

	rec = serveOntologyGovernanceMutationRequest(handler, "/api/ontology/governance/plans/opplan-random/apply?tenant_id=mission%3A1", `{"idempotency_key":"apply-1","confirmed":true}`)
	if rec.Code != http.StatusOK || service.apply.PlanID != "opplan-random" || service.apply.IdempotencyKey != "apply-1" || !service.apply.Confirmed {
		t.Fatalf("apply status=%d request=%+v body=%s", rec.Code, service.apply, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"projection_refresh_status":"pending"`) {
		t.Fatalf("apply response does not expose bounded refresh state: %s", rec.Body.String())
	}
	for _, forbidden := range []string{"server-actor", "secret-key", "secret-hash", "current_value", "proposed_value"} {
		if strings.Contains(rec.Body.String(), forbidden) {
			t.Fatalf("apply response exposes %q: %s", forbidden, rec.Body.String())
		}
	}

	rec = serveOntologyGovernanceMutationRequest(handler, "/api/ontology/governance/receipts/receipt-1/projection-refresh-retries?tenant_id=mission%3A1", "")
	if rec.Code != http.StatusOK || service.retry.ReceiptID != "receipt-1" || service.retry.TenantID != "mission:1" || service.retry.IdempotencyKey != "test-idempotency" ||
		!strings.Contains(rec.Body.String(), `"projection_refresh_status":"succeeded"`) {
		t.Fatalf("projection refresh retry status=%d request=%+v body=%s", rec.Code, service.retry, rec.Body.String())
	}
}

func TestHTTPRoutesOntologyGovernanceMutationRejectActorInjectionAndCrossOriginCookie(t *testing.T) {
	service := &fakeOntologyGovernanceCommandService{}
	handler := newTestRouter(RouterOptions{OntologyGovernanceCommand: service})
	path := "/api/ontology/governance/proposals/proposal-1/decisions?tenant_id=mission%3A1"
	rec := serveOntologyGovernanceMutationRequest(handler, path, `{"decision":"accepted","reason":"reviewed","expected_revision":3,"actor_id":"attacker"}`)
	if rec.Code != http.StatusBadRequest || service.calls != 0 {
		t.Fatalf("actor injection status=%d calls=%d body=%s", rec.Code, service.calls, rec.Body.String())
	}

	req := authedOntologyRequest(http.MethodPost, path, `{"decision":"accepted","reason":"reviewed","expected_revision":3}`)
	req.AddCookie(&http.Cookie{Name: authAccessTokenCookieName, Value: "iat-cookie"})
	req.Header.Set("Origin", "https://attacker.example")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || service.calls != 0 {
		t.Fatalf("cross-origin cookie status=%d calls=%d body=%s", rec.Code, service.calls, rec.Body.String())
	}

	req = authedOntologyRequest(http.MethodPost, path, `{"decision":"accepted","reason":"reviewed","expected_revision":3}`)
	req.AddCookie(&http.Cookie{Name: authAccessTokenCookieName, Value: "iat-cookie"})
	req.Header.Set("Origin", "http://example.com")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || service.calls != 1 {
		t.Fatalf("same-origin cookie status=%d calls=%d body=%s", rec.Code, service.calls, rec.Body.String())
	}
}

func TestHTTPRoutesOntologyGovernanceDelegatesCrossScopeAuthorizationToScopeGate(t *testing.T) {
	service := &fakeOntologyGovernanceCommandService{}
	handler := newTestRouter(RouterOptions{OntologyGovernanceCommand: service})
	req := authedOntologyRequest(http.MethodPost, "/api/ontology/governance/proposals/proposal-1/decisions?tenant_id=mission%3A1", `{"decision":"accepted","reason":"reviewed","expected_revision":3}`)
	req.Header.Set("X-Tenant-ID", "mission:2")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || service.calls != 1 || service.decision.TenantID != "mission:1" {
		t.Fatalf("cross-scope mutation status=%d calls=%d request=%+v body=%s", rec.Code, service.calls, service.decision, rec.Body.String())
	}
}

func TestHTTPRoutesOntologyGovernanceReadCapabilitiesRemainCoarseReadGates(t *testing.T) {
	for _, path := range []string{
		"/api/ontology/governance/summary",
		"/api/ontology/governance/proposals",
		"/api/ontology/governance/proposals/{proposal_id}",
		"/api/ontology/governance/proposals/{proposal_id}/analysis",
		"/api/ontology/governance/proposals/{proposal_id}/receipts",
		"/api/ontology/governance/proposals/{proposal_id}/audit-events",
	} {
		assertCapability(t, defaultPermissionCapabilities(), http.MethodGet, path, "operations.read")
	}
}

func TestHTTPRoutesOntologyGovernanceMutationCapabilitiesRemainCoarseManageGates(t *testing.T) {
	for _, path := range []string{
		"/api/ontology/governance/proposals/{proposal_id}/decisions",
		"/api/ontology/governance/proposals/{proposal_id}/plans",
		"/api/ontology/governance/plans/{plan_id}/apply",
		"/api/ontology/governance/receipts/{receipt_id}/projection-refresh-retries",
	} {
		assertCapability(t, defaultPermissionCapabilities(), http.MethodPost, path, "operations.manage")
	}
}

func serveOntologyGovernanceRequest(handler http.Handler, path string) *httptest.ResponseRecorder {
	req := authedOntologyRequest(http.MethodGet, path, "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func serveOntologyGovernanceMutationRequest(handler http.Handler, path, body string) *httptest.ResponseRecorder {
	req := authedOntologyRequest(http.MethodPost, path, body)
	req.Header.Set("Idempotency-Key", "test-idempotency")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
