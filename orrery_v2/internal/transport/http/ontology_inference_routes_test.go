package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyreasoning"
	"orrery_v2/internal/app/ontologyscope"
	"orrery_v2/internal/retrievalevidence"
)

type inferenceHTTPService struct {
	calls     int
	principal identity.Principal
	request   ontologyreasoning.EvaluateRequest
	err       error
}

func (s *inferenceHTTPService) GroundedContext(_ context.Context, p identity.Principal, input ontologyreasoning.GroundedContextRequest) (ontologyreasoning.GroundedContext, error) {
	s.calls++
	s.principal = p
	return ontologyreasoning.GroundedContext{TenantID: input.TenantID, RootID: input.RootID}, s.err
}

func TestHTTPGroundedOntologyContextUsesPrivateIdentityAndConflict(t *testing.T) {
	s := &inferenceHTTPService{}
	h := newTestRouter(RouterOptions{OntologyInference: s})
	path := "/api/ontology/inference/context"
	body := `{"tenant_id":"tenant-1","root_id":"root-1","rules":[]}`
	readRequest := func(body string) *http.Request {
		req := authedRequest(http.MethodPost, path, body)
		req.Header.Set("X-Permission-Scope", "ontology.read")
		return req
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	if rec.Code != http.StatusUnauthorized || s.calls != 0 {
		t.Fatal("anonymous context read reached service")
	}
	denied := readRequest(body)
	denied.Header.Set("X-Permission-Scope", "knowledge.read")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, denied)
	if rec.Code != http.StatusForbidden || s.calls != 0 {
		t.Fatalf("unrelated read scope reached context: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, readRequest(body))
	if rec.Code != http.StatusOK || !s.principal.Authenticated() || rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("read: %d %s", rec.Code, rec.Body.String())
	}
	for _, extra := range []string{`,"actor":"forged"}`, `,"snapshot_hash":"old"}`, `,"candidates":[{"id":"empty"}]}`} {
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, readRequest(strings.TrimSuffix(body, "}")+extra))
		if rec.Code != http.StatusBadRequest || s.calls != 1 {
			t.Fatalf("forged annotations accepted: %s %d", extra, rec.Code)
		}
	}
	s.err = retrievalevidence.ErrInputChanged
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, readRequest(body))
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale snapshot: %d", rec.Code)
	}
}

func (s *inferenceHTTPService) Evaluate(_ context.Context, p identity.Principal, req ontologyreasoning.EvaluateRequest) (ontologyreasoning.ReasoningRun, error) {
	s.calls++
	s.principal = p
	s.request = req
	return ontologyreasoning.ReasoningRun{ID: "run-1", TenantID: req.TenantID}, s.err
}
func (s *inferenceHTTPService) ExportSchema(_ context.Context, p identity.Principal, tenant string) (ontologyreasoning.SchemaExport, error) {
	s.calls++
	s.principal = p
	return ontologyreasoning.SchemaExport{TenantID: tenant}, s.err
}

func (s *inferenceHTTPService) CandidateWorkflow(_ context.Context, p identity.Principal, _ string, input ontologyreasoning.WorkflowRequest) (ontologyreasoning.CandidateState, error) {
	s.calls++
	s.principal = p
	return ontologyreasoning.CandidateState{CandidateID: input.CandidateID, Revision: input.ExpectedRevision + 1}, s.err
}

func TestHTTPInferenceWorkflowRoutesRequireIdentityAndReturnConflict(t *testing.T) {
	for _, operation := range []string{"review", "plan", "apply"} {
		service := &inferenceHTTPService{}
		handler := newTestRouter(RouterOptions{OntologyInference: service})
		path := "/api/ontology/inference/" + operation
		body := `{"tenant_id":"tenant-1","run_id":"run-1","candidate_id":"candidate-1","expected_revision":1,"support_hash":"hash"}`
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		if rec.Code != http.StatusUnauthorized || service.calls != 0 {
			t.Fatalf("anonymous %s reached service", operation)
		}
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, authedRequest(http.MethodPost, path, body))
		if rec.Code != http.StatusOK || !service.principal.Authenticated() || rec.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("workflow %s: %d %s", operation, rec.Code, rec.Body.String())
		}
		service.err = ontologyreasoning.ErrStateConflict
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, authedRequest(http.MethodPost, path, body))
		if rec.Code != http.StatusConflict {
			t.Fatalf("stale %s: %d", operation, rec.Code)
		}
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, authedRequest(http.MethodPost, path, strings.TrimSuffix(body, "}")+`,"actor":"forged"}`))
		if rec.Code != http.StatusBadRequest || service.calls != 2 {
			t.Fatalf("identity injection %s: %d", operation, rec.Code)
		}
	}
}

func TestHTTPRoutesOntologyInferenceValidatePrincipalAndBody(t *testing.T) {
	service := &inferenceHTTPService{}
	handler := newTestRouter(RouterOptions{OntologyInference: service})
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"tenant_id":"mission:1","root_id":"project-1","rules":[]}`, http.StatusOK},
		{`{"tenant_id":"mission:1","root_id":"project-1","actor":"forged","rules":[]}`, http.StatusBadRequest},
		{`{"tenant_id":"mission:1","root_id":"project-1","rules":[]} {}`, http.StatusBadRequest},
		{`{"tenant_id":"mission:1","root_id":"","rules":[]}`, http.StatusBadRequest},
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, authedRequest(http.MethodPost, "/api/ontology/inference/evaluate", tc.body))
		if rec.Code != tc.status {
			t.Fatalf("status %d want %d: %s", rec.Code, tc.status, rec.Body.String())
		}
		if rec.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("inference response can be cached")
		}
	}
	if service.calls != 1 || !service.principal.Authenticated() || service.request.TenantID != "mission:1" {
		t.Fatalf("wire boundary: %+v", service)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/ontology/inference/evaluate", strings.NewReader(`{}`)))
	if rec.Code != http.StatusUnauthorized || service.calls != 1 {
		t.Fatal("anonymous inference reached service")
	}
	service.err = ontologyscope.ErrAccessDenied
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/ontology/inference/schema-export?tenant_id=mission:1", ""))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("export authorization error: %d %s", rec.Code, rec.Body.String())
	}
}
