package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"orrery_v2/internal/app/identity"
)

type fakeOntologyReliabilityService struct {
	method            string
	tenantID          string
	incidentID        string
	diagnosisID       string
	ruleID            string
	decisionID        string
	authorize         OntologyReliabilityAuthorizationRequest
	decision          OntologyReliabilityAuthorizationDecisionRequest
	diagnosisCreate   OntologyReliabilityDiagnosisCreateRequest
	diagnosisDecision OntologyReliabilityDiagnosisDecisionRequest
	ruleProposal      OntologyReliabilityRuleProposalRequest
	activationReview  OntologyReliabilityActivationReviewRequest
	activation        OntologyReliabilityActivationRequest
	invoke            OntologyReliabilityInvokeRequest
}

func (f *fakeOntologyReliabilityService) GetIncident(_ context.Context, _ identity.Principal, tenantID, incidentID string) (OntologyReliabilityIncidentDetailDTO, error) {
	f.method, f.tenantID, f.incidentID = "detail", tenantID, incidentID
	return OntologyReliabilityIncidentDetailDTO{Incident: OntologyReliabilityIncidentDTO{ID: incidentID, TenantID: tenantID}, AvailableEvidence: []OntologyReliabilityEvidenceDTO{}, Diagnoses: []OntologyReliabilityDiagnosisDTO{}, Rules: []OntologyReliabilityRepairRuleDTO{}, Preflights: []OntologyReliabilityPreflightDTO{}, Executions: []OntologyReliabilityExecutionDTO{}, ActivationReviews: []OntologyReliabilityActivationReviewDTO{}, Authorizations: []OntologyReliabilityAuthorizationDTO{}}, nil
}

func (f *fakeOntologyReliabilityService) CreateDiagnosis(_ context.Context, _ identity.Principal, tenantID, incidentID string, request OntologyReliabilityDiagnosisCreateRequest) (OntologyReliabilityDiagnosisDTO, error) {
	f.method, f.tenantID, f.incidentID, f.diagnosisCreate = "create-diagnosis", tenantID, incidentID, request
	return OntologyReliabilityDiagnosisDTO{ID: "diagnosis-1", IncidentID: incidentID, Status: "proposed"}, nil
}

func (f *fakeOntologyReliabilityService) DecideDiagnosis(_ context.Context, _ identity.Principal, tenantID, incidentID, diagnosisID string, request OntologyReliabilityDiagnosisDecisionRequest) (OntologyReliabilityDiagnosisDTO, error) {
	f.method, f.tenantID, f.incidentID, f.diagnosisID, f.diagnosisDecision = "decide-diagnosis", tenantID, incidentID, diagnosisID, request
	return OntologyReliabilityDiagnosisDTO{ID: diagnosisID, IncidentID: incidentID, Status: "confirmed"}, nil
}

func (f *fakeOntologyReliabilityService) ProposeRepairRule(_ context.Context, _ identity.Principal, tenantID, incidentID, diagnosisID string, request OntologyReliabilityRuleProposalRequest) (OntologyReliabilityRepairRuleDTO, error) {
	f.method, f.tenantID, f.incidentID, f.diagnosisID, f.ruleProposal = "propose-rule", tenantID, incidentID, diagnosisID, request
	return OntologyReliabilityRepairRuleDTO{ID: "rule-learned-1", DiagnosisCaseID: diagnosisID, Strategy: request.Strategy, Status: "proposed"}, nil
}

func (f *fakeOntologyReliabilityService) RequestRuleActivationReview(_ context.Context, _ identity.Principal, tenantID, ruleID string, request OntologyReliabilityActivationReviewRequest) (OntologyReliabilityActivationReviewDTO, error) {
	f.method, f.tenantID, f.ruleID, f.activationReview = "request-activation", tenantID, ruleID, request
	return OntologyReliabilityActivationReviewDTO{ID: "activation-1", RuleID: ruleID, Status: "pending"}, nil
}

func (f *fakeOntologyReliabilityService) ResolveRuleActivationReview(_ context.Context, _ identity.Principal, tenantID, ruleID, decisionID string, request OntologyReliabilityAuthorizationDecisionRequest) (OntologyReliabilityActivationReviewResolutionDTO, error) {
	f.method, f.tenantID, f.ruleID, f.decisionID, f.decision = "decide-activation", tenantID, ruleID, decisionID, request
	return OntologyReliabilityActivationReviewResolutionDTO{Review: OntologyReliabilityActivationReviewDTO{ID: decisionID, RuleID: ruleID, Status: "resolved"}, Effect: "authorize"}, nil
}

func (f *fakeOntologyReliabilityService) ActivateRepairRule(_ context.Context, _ identity.Principal, tenantID, ruleID, decisionID string, request OntologyReliabilityActivationRequest) (OntologyReliabilityRepairRuleDTO, error) {
	f.method, f.tenantID, f.ruleID, f.decisionID, f.activation = "activate-rule", tenantID, ruleID, decisionID, request
	return OntologyReliabilityRepairRuleDTO{ID: ruleID, Status: "active", ActivationDecisionID: decisionID}, nil
}

func (f *fakeOntologyReliabilityService) RequestRepairAuthorization(_ context.Context, _ identity.Principal, tenantID, incidentID, ruleID string, request OntologyReliabilityAuthorizationRequest) (OntologyReliabilityAuthorizationDTO, error) {
	f.method, f.tenantID, f.incidentID, f.ruleID, f.authorize = "authorize", tenantID, incidentID, ruleID, request
	return OntologyReliabilityAuthorizationDTO{ID: "decision-1", RuleID: ruleID, ExecutionMode: request.ExecutionMode, Status: "pending"}, nil
}

func (f *fakeOntologyReliabilityService) ResolveRepairAuthorization(_ context.Context, _ identity.Principal, tenantID, incidentID, ruleID, decisionID string, request OntologyReliabilityAuthorizationDecisionRequest) (OntologyReliabilityAuthorizationResolutionDTO, error) {
	f.method, f.tenantID, f.incidentID, f.ruleID, f.decisionID, f.decision = "decision", tenantID, incidentID, ruleID, decisionID, request
	return OntologyReliabilityAuthorizationResolutionDTO{Authorization: OntologyReliabilityAuthorizationDTO{ID: decisionID, Status: "resolved"}, Effect: "authorize"}, nil
}

func (f *fakeOntologyReliabilityService) InvokeAuthorizedRepair(_ context.Context, _ identity.Principal, tenantID, incidentID, ruleID, decisionID string, request OntologyReliabilityInvokeRequest) (OntologyActionRunDTO, error) {
	f.method, f.tenantID, f.incidentID, f.ruleID, f.decisionID, f.invoke = "invoke", tenantID, incidentID, ruleID, decisionID, request
	return OntologyActionRunDTO{ID: "action-run-1", ActionTypeID: "reliability.repair.execute", Status: "started"}, nil
}

func TestHTTPRoutesOntologyReliabilityExposeBoundWorkflow(t *testing.T) {
	service := &fakeOntologyReliabilityService{}
	handler := newTestRouter(RouterOptions{OntologyReliability: service})

	rec := serveOntologyReliabilityRequest(handler, http.MethodGet, "/api/ontology/reliability/incidents/incident-1?tenant_id=tenant-1", "", "")
	if rec.Code != http.StatusOK || service.method != "detail" || service.tenantID != "tenant-1" || service.incidentID != "incident-1" {
		t.Fatalf("detail status=%d service=%+v body=%s", rec.Code, service, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	rec = serveOntologyReliabilityRequest(handler, http.MethodPost, "/api/ontology/reliability/incidents/incident-1/diagnoses?tenant_id=tenant-1", `{"root_cause_category":"transient","redacted_summary":"provider timeout","confidence":0.8,"evidence_ids":["evidence-1"]}`, "diagnosis-create-1")
	if rec.Code != http.StatusCreated || service.method != "create-diagnosis" || service.diagnosisCreate.IdempotencyKey != "diagnosis-create-1" || len(service.diagnosisCreate.EvidenceIDs) != 1 {
		t.Fatalf("create diagnosis status=%d service=%+v body=%s", rec.Code, service, rec.Body.String())
	}

	rec = serveOntologyReliabilityRequest(handler, http.MethodPost, "/api/ontology/reliability/incidents/incident-1/diagnoses/diagnosis-1/decision?tenant_id=tenant-1", `{"decision":"confirm","reason":"evidence checked"}`, "diagnosis-decision-1")
	if rec.Code != http.StatusOK || service.method != "decide-diagnosis" || service.diagnosisID != "diagnosis-1" || service.diagnosisDecision.IdempotencyKey != "diagnosis-decision-1" {
		t.Fatalf("decide diagnosis status=%d service=%+v body=%s", rec.Code, service, rec.Body.String())
	}

	rec = serveOntologyReliabilityRequest(handler, http.MethodPost, "/api/ontology/reliability/incidents/incident-1/diagnoses/diagnosis-1/rules?tenant_id=tenant-1", `{"strategy":"retry","reversible":true,"idempotent":true}`, "rule-proposal-1")
	if rec.Code != http.StatusCreated || service.method != "propose-rule" || service.ruleProposal.IdempotencyKey != "rule-proposal-1" || service.ruleProposal.Strategy != "retry" {
		t.Fatalf("propose rule status=%d service=%+v body=%s", rec.Code, service, rec.Body.String())
	}

	rec = serveOntologyReliabilityRequest(handler, http.MethodPost, "/api/ontology/reliability/rules/rule-1/activation-reviews?tenant_id=tenant-1", `{"purpose":"promote tested rule"}`, "activation-request-1")
	if rec.Code != http.StatusCreated || service.method != "request-activation" || service.ruleID != "rule-1" || service.activationReview.IdempotencyKey != "activation-request-1" {
		t.Fatalf("activation request status=%d service=%+v body=%s", rec.Code, service, rec.Body.String())
	}

	rec = serveOntologyReliabilityRequest(handler, http.MethodPost, "/api/ontology/reliability/rules/rule-1/activation-reviews/activation-1/decision?tenant_id=tenant-1", `{"decision":"authorize","reason":"evidence checked"}`, "activation-decision-1")
	if rec.Code != http.StatusOK || service.method != "decide-activation" || service.decisionID != "activation-1" || service.decision.IdempotencyKey != "activation-decision-1" {
		t.Fatalf("activation decision status=%d service=%+v body=%s", rec.Code, service, rec.Body.String())
	}

	rec = serveOntologyReliabilityRequest(handler, http.MethodPost, "/api/ontology/reliability/rules/rule-1/activation-reviews/activation-1/activate?tenant_id=tenant-1", `{}`, "activate-rule-1")
	if rec.Code != http.StatusOK || service.method != "activate-rule" || service.activation.IdempotencyKey != "activate-rule-1" {
		t.Fatalf("activate rule status=%d service=%+v body=%s", rec.Code, service, rec.Body.String())
	}

	rec = serveOntologyReliabilityRequest(handler, http.MethodPost, "/api/ontology/reliability/incidents/incident-1/rules/rule-1/authorizations?tenant_id=tenant-1", `{"execution_mode":"dry_run","purpose":"verify"}`, "authorize-1")
	if rec.Code != http.StatusCreated || service.method != "authorize" || service.ruleID != "rule-1" || service.authorize.IdempotencyKey != "authorize-1" || service.authorize.ExecutionMode != "dry_run" {
		t.Fatalf("authorize status=%d service=%+v body=%s", rec.Code, service, rec.Body.String())
	}

	rec = serveOntologyReliabilityRequest(handler, http.MethodPost, "/api/ontology/reliability/incidents/incident-1/rules/rule-1/authorizations/decision-1/decision?tenant_id=tenant-1", `{"decision":"approve","reason":"checked"}`, "decision-1")
	if rec.Code != http.StatusOK || service.method != "decision" || service.decisionID != "decision-1" || service.decision.IdempotencyKey != "decision-1" {
		t.Fatalf("decision status=%d service=%+v body=%s", rec.Code, service, rec.Body.String())
	}

	rec = serveOntologyReliabilityRequest(handler, http.MethodPost, "/api/ontology/reliability/incidents/incident-1/rules/rule-1/authorizations/decision-1/invoke?tenant_id=tenant-1", `{}`, "invoke-1")
	if rec.Code != http.StatusAccepted || service.method != "invoke" || service.invoke.IdempotencyKey != "invoke-1" {
		t.Fatalf("invoke status=%d service=%+v body=%s", rec.Code, service, rec.Body.String())
	}

	assertCapability(t, defaultPermissionCapabilities(), http.MethodGet, "/api/ontology/reliability/incidents/{incident_id}", "operations.read")
	assertCapability(t, defaultPermissionCapabilities(), http.MethodPost, "/api/ontology/reliability/incidents/{incident_id}/diagnoses", "operations.manage")
	assertCapability(t, defaultPermissionCapabilities(), http.MethodPost, "/api/ontology/reliability/rules/{rule_id}/activation-reviews", "operations.manage")
	assertCapability(t, defaultPermissionCapabilities(), http.MethodPost, "/api/ontology/reliability/rules/{rule_id}/activation-reviews/{review_id}/activate", "operations.manage")
	assertCapability(t, defaultPermissionCapabilities(), http.MethodPost, "/api/ontology/reliability/incidents/{incident_id}/rules/{rule_id}/authorizations/{decision_id}/invoke", "operations.manage")
}

func TestHTTPRoutesOntologyReliabilityRequireTenantAndIdempotency(t *testing.T) {
	service := &fakeOntologyReliabilityService{}
	handler := newTestRouter(RouterOptions{OntologyReliability: service})
	for _, request := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/ontology/reliability/incidents/incident-1", ""},
		{http.MethodPost, "/api/ontology/reliability/incidents/incident-1/diagnoses?tenant_id=tenant-1", `{"root_cause_category":"transient","redacted_summary":"timeout","confidence":0.8,"evidence_ids":["evidence-1"]}`},
		{http.MethodPost, "/api/ontology/reliability/incidents/incident-1/rules/rule-1/authorizations?tenant_id=tenant-1", `{"execution_mode":"dry_run"}`},
		{http.MethodPost, "/api/ontology/reliability/rules/rule-1/activation-reviews?tenant_id=tenant-1", `{}`},
		{http.MethodPost, "/api/ontology/reliability/incidents/incident-1/rules/rule-1/authorizations/decision-1/invoke?tenant_id=tenant-1", `{}`},
	} {
		rec := serveOntologyReliabilityRequest(handler, request.method, request.path, request.body, "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s %s status=%d body=%s", request.method, request.path, rec.Code, rec.Body.String())
		}
	}
	if service.method != "" {
		t.Fatalf("invalid requests reached service: %+v", service)
	}
}

func serveOntologyReliabilityRequest(handler http.Handler, method, path, body, key string) *httptest.ResponseRecorder {
	req := authedRequest(method, path, body)
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Origin", "http://example.com")
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
