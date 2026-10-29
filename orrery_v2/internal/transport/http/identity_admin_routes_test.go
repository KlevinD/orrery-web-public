package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/operations"
)

func TestHTTPIdentityAdminRoutesLinkListDisableAndEnableSubject(t *testing.T) {
	store := identity.NewMemorySubjectStore()
	audit := &fakeIdentityAuditRecorder{}
	handler := newTestRouter(RouterOptions{
		IdentityAdmin: identity.NewSubjectAdminService(store).WithSessionRevoker(noopIdentitySubjectSessionRevoker{}),
		AuditRecorder: audit,
	})

	link := authedRequest(http.MethodPost, "/api/identity/subjects", `{"provider":"kratos","subject":"kratos-id-1","agent_profile_id":"human-operator","username":"operator","email":"operator@example.com"}`)
	link.Header.Set("X-Permission-Scope", "permissions.manage")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, link)
	if rec.Code != http.StatusCreated {
		t.Fatalf("link status = %d body = %s", rec.Code, rec.Body.String())
	}
	var linked identitySubjectDTO
	if err := json.NewDecoder(rec.Body).Decode(&linked); err != nil {
		t.Fatalf("decode linked subject: %v", err)
	}
	if linked.ID != "kratos:kratos-id-1" || linked.Username != "operator" || linked.AgentProfileID != "human-operator" {
		t.Fatalf("linked subject = %+v", linked)
	}
	if len(audit.events) != 1 || audit.events[0].Action != "identity.subject.link" || audit.events[0].EntityID != linked.ID {
		t.Fatalf("audit events = %+v", audit.events)
	}

	list := authedRequest(http.MethodGet, "/api/identity/subjects?provider=kratos&agent_profile_id=human-operator", "")
	list.Header.Set("X-Permission-Scope", "permissions.read")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, list)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", rec.Code, rec.Body.String())
	}
	var page identitySubjectPageDTO
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode subject page: %v", err)
	}
	if page.Page.Total != 1 || len(page.Subjects) != 1 || page.Subjects[0].Subject != "kratos-id-1" {
		t.Fatalf("subject page = %+v", page)
	}

	disable := authedRequest(http.MethodDelete, "/api/identity/subjects/"+linked.ID, "")
	disable.Header.Set("X-Permission-Scope", "permissions.manage")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, disable)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("disable status = %d body = %s", rec.Code, rec.Body.String())
	}
	var disabled identitySubjectDTO
	if err := json.NewDecoder(rec.Body).Decode(&disabled); err != nil {
		t.Fatalf("decode disabled subject: %v", err)
	}
	if disabled.Status != identity.SubjectStatusDisabled {
		t.Fatalf("disabled subject = %+v", disabled)
	}

	enable := authedRequest(http.MethodPost, "/api/identity/subjects/"+linked.ID+"/enable", "")
	enable.Header.Set("X-Permission-Scope", "permissions.manage")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, enable)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("enable status = %d body = %s", rec.Code, rec.Body.String())
	}
	var enabled identitySubjectDTO
	if err := json.NewDecoder(rec.Body).Decode(&enabled); err != nil {
		t.Fatalf("decode enabled subject: %v", err)
	}
	if enabled.Status != identity.SubjectStatusActive {
		t.Fatalf("enabled subject = %+v", enabled)
	}
	if len(audit.events) != 3 || audit.events[1].Action != "identity.subject.disable" || audit.events[2].Action != "identity.subject.enable" {
		t.Fatalf("audit events = %+v", audit.events)
	}
}

func TestHTTPIdentityAdminRoutesRequirePermissionScope(t *testing.T) {
	handler := newTestRouter(RouterOptions{IdentityAdmin: identity.NewSubjectAdminService(identity.NewMemorySubjectStore())})

	req := authedRequest(http.MethodPost, "/api/identity/subjects", `{"provider":"kratos","subject":"kratos-id-1","agent_profile_id":"human-operator"}`)
	req.Header.Set("X-Permission-Scope", "permissions.read")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"forbidden"`) {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

type fakeIdentityAuditRecorder struct {
	events []operations.AuditEventDTO
}

type noopIdentitySubjectSessionRevoker struct{}

func (noopIdentitySubjectSessionRevoker) RevokeSessionsForIdentitySubject(context.Context, string, string) error {
	return nil
}

func (f *fakeIdentityAuditRecorder) RecordAuditEvent(ctx context.Context, event operations.AuditEventDTO) error {
	_ = ctx
	f.events = append(f.events, event)
	return nil
}
