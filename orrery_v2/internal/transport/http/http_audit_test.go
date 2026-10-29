package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/userauth"
)

func TestClientIPFromRequestIgnoresForwardedForFromUntrustedPrivateRemote(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login/begin", nil)
	req = withDeploymentSecurity(req, deploymentSecurityConfig{})
	req.RemoteAddr = "10.0.0.5:12345"
	req.Header.Set("X-Forwarded-For", "198.51.100.10")
	req.Header.Set("X-Real-IP", "198.51.100.11")

	if got := clientIPFromRequest(req); got != "10.0.0.5" {
		t.Fatalf("clientIPFromRequest = %q, want remote private IP", got)
	}
}

func TestClientIPFromRequestTrustsConfiguredProxyCIDR(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login/begin", nil)
	req = withDeploymentSecurity(req, deploymentSecurityConfig{trustedProxyCIDRs: []string{"10.0.0.0/8"}})
	req.RemoteAddr = "10.0.0.5:12345"
	req.Header.Set("X-Forwarded-For", "198.51.100.10, 10.0.0.5")

	if got := clientIPFromRequest(req); got != "198.51.100.10" {
		t.Fatalf("clientIPFromRequest = %q, want forwarded client IP", got)
	}
}

func TestClientIPFromRequestIgnoresSpoofedLeftmostForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login/begin", nil)
	req = withDeploymentSecurity(req, deploymentSecurityConfig{trustedProxyCIDRs: []string{"10.0.0.0/8"}})
	req.RemoteAddr = "10.0.0.5:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.66, 198.51.100.10")

	if got := clientIPFromRequest(req); got != "198.51.100.10" {
		t.Fatalf("clientIPFromRequest = %q, want proxy-appended client IP", got)
	}
}

func TestClientIPFromRequestSkipsInvalidForwardedIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login/begin", nil)
	req = withDeploymentSecurity(req, deploymentSecurityConfig{trustedProxyCIDRs: []string{"10.0.0.0/8"}})
	req.RemoteAddr = "10.0.0.5:12345"
	req.Header.Set("X-Forwarded-For", "not-an-ip")
	req.Header.Set("X-Real-IP", "198.51.100.11")

	if got := clientIPFromRequest(req); got != "198.51.100.11" {
		t.Fatalf("clientIPFromRequest = %q, want valid X-Real-IP fallback", got)
	}
}

func TestHTTPAuditRecordsAuthenticatedRequestWithRealSourceIP(t *testing.T) {
	auth, token := authenticatedSuperadminToken(t)
	audit := &recordingHTTPAuditRecorder{}
	handler := newTestRouter(RouterOptions{Auth: auth, AuditRecorder: audit, HTTPAuditEnabled: true, TrustedProxyCIDRs: []string{"172.16.0.0/12"}})

	req := httptest.NewRequest(http.MethodGet, "/api/permissions/capabilities", nil)
	req.RemoteAddr = "172.18.0.4:42100"
	req.Header.Set("X-Forwarded-For", "198.51.100.10, 172.18.0.4")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(audit.events) != 1 {
		t.Fatalf("audit events = %d, want 1: %+v", len(audit.events), audit.events)
	}
	event := audit.events[0]
	if event.ActorID != "superadmin" || event.EntityType != httpAuditEntityType || event.Result != "succeeded" {
		t.Fatalf("audit event identity/result = %+v", event)
	}
	if event.Action != "http.get /api/permissions/capabilities" {
		t.Fatalf("audit action = %q", event.Action)
	}
	if event.Metadata["source_ip"] != "198.51.100.10" || event.Metadata["remote_addr"] != "172.18.0.4:42100" {
		t.Fatalf("audit source metadata = %+v", event.Metadata)
	}
	if event.Metadata["capability_scope"] != "permissions.read" {
		t.Fatalf("audit capability metadata = %+v", event.Metadata)
	}
}

func TestHTTPAuditDoesNotTrustSpoofedViewerHeaders(t *testing.T) {
	audit := &recordingHTTPAuditRecorder{}
	handler := newTestRouter(RouterOptions{AuditRecorder: audit, HTTPAuditEnabled: true})

	req := httptest.NewRequest(http.MethodGet, "/api/permissions/capabilities", nil)
	req.Header.Set("X-User-ID", "superadmin")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Permission-Scope", "operations.read")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(audit.events) != 1 {
		t.Fatalf("audit events = %d, want 1: %+v", len(audit.events), audit.events)
	}
	if event := audit.events[0]; event.ActorID != httpAuditActorAnon || event.Result != "denied" {
		t.Fatalf("audit event = %+v", event)
	}
}

func TestHTTPAuditRecordsLoginCompleteActor(t *testing.T) {
	store := userauth.NewMemoryAccountStore()
	if err := userauth.EnsureBootstrapSuperadmin(context.Background(), store, userauth.SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	auth := userauth.NewService(store)
	audit := &recordingHTTPAuditRecorder{}
	handler := newTestRouter(RouterOptions{Auth: auth, AuditRecorder: audit, HTTPAuditEnabled: true, TrustedProxyCIDRs: []string{"172.16.0.0/12"}})

	begin := httptest.NewRequest(http.MethodPost, "/api/auth/login/begin", strings.NewReader(`{"username":"superadmin"}`))
	begin.RemoteAddr = "172.18.0.4:42100"
	begin.Header.Set("X-Forwarded-For", "198.51.100.10")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, begin)
	if rec.Code != http.StatusOK {
		t.Fatalf("begin status = %d body = %s", rec.Code, rec.Body.String())
	}
	var challenge userauth.LoginChallengeDTO
	if err := json.NewDecoder(rec.Body).Decode(&challenge); err != nil {
		t.Fatalf("decode challenge: %v", err)
	}

	complete := httptest.NewRequest(http.MethodPost, "/api/auth/login/complete", strings.NewReader(`{"username":"superadmin","challenge_id":"`+challenge.ChallengeID+`","password":"`+userauth.SuperadminPassword()+`"}`))
	complete.RemoteAddr = "172.18.0.4:42101"
	complete.Header.Set("X-Forwarded-For", "198.51.100.10")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, complete)
	if rec.Code != http.StatusOK {
		t.Fatalf("complete status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(audit.events) != 2 {
		t.Fatalf("audit events = %d, want 2: %+v", len(audit.events), audit.events)
	}
	event := audit.events[1]
	if event.ActorID != "superadmin" || event.ViewerID != "superadmin" || event.Metadata["source_ip"] != "198.51.100.10" {
		t.Fatalf("complete audit event = %+v", event)
	}
}

func authenticatedSuperadminToken(t *testing.T) (*userauth.Service, string) {
	t.Helper()
	store := userauth.NewMemoryAccountStore()
	if err := userauth.EnsureBootstrapSuperadmin(context.Background(), store, userauth.SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	auth := userauth.NewService(store)
	challenge, err := auth.BeginLogin(context.Background(), userauth.BeginLoginRequest{Username: "superadmin"})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	session, err := auth.CompleteLogin(context.Background(), userauth.CompleteLoginRequest{
		Username:    "superadmin",
		ChallengeID: challenge.ChallengeID,
		Password:    userauth.SuperadminPassword(),
	})
	if err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}
	return auth, session.AccessToken
}

type recordingHTTPAuditRecorder struct {
	events []operations.AuditEventDTO
}

func (r *recordingHTTPAuditRecorder) RecordAuditEvent(ctx context.Context, event operations.AuditEventDTO) error {
	r.events = append(r.events, event)
	return nil
}
