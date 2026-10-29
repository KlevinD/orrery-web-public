package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"orrery_v2/internal/app/identity"
)

type fakeOntologyActionService struct {
	actionTypeID string
	tenantID     string
}

func (f *fakeOntologyActionService) InvokeAction(_ context.Context, _ identity.Principal, actionTypeID string, request OntologyActionInvokeRequest) (OntologyActionRunDTO, error) {
	f.actionTypeID, f.tenantID = actionTypeID, request.TenantID
	return OntologyActionRunDTO{ID: "action-run-1", ActionTypeID: actionTypeID, Status: "started"}, nil
}

func TestHTTPRouteInvokesGovernedOntologyAction(t *testing.T) {
	service := &fakeOntologyActionService{}
	handler := newTestRouter(RouterOptions{OntologyActions: service})
	req := authedRequest(http.MethodPost, "/api/ontology/actions/task.complete/invoke?tenant_id=tenant-1", `{"runtime_kind":"web_app","runtime_id":"orrery-web","args_hash":"sha256:args","object_refs":[{"kind":"task","id":"task-1"}]}`)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if service.actionTypeID != "task.complete" || service.tenantID != "tenant-1" {
		t.Fatalf("captured action=%q tenant=%q", service.actionTypeID, service.tenantID)
	}
	assertCapability(t, defaultPermissionCapabilities(), http.MethodPost, "/api/ontology/actions/{action_type_id}/invoke", "operations.manage")
}
