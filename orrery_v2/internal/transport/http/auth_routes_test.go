package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/userauth"
)

func TestHTTPAuthRoutesLoginWithPlaintextPassword(t *testing.T) {
	store := userauth.NewMemoryAccountStore()
	if err := userauth.EnsureBootstrapSuperadmin(context.Background(), store, userauth.SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	auth := userauth.NewService(store)
	handler := NewRouter(RouterOptions{Auth: auth})

	begin := httptest.NewRequest(http.MethodPost, "/api/auth/login/begin", strings.NewReader(`{"username":"superadmin"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, begin)
	if rec.Code != http.StatusOK {
		t.Fatalf("begin status = %d body = %s", rec.Code, rec.Body.String())
	}
	var challenge userauth.LoginChallengeDTO
	if err := json.NewDecoder(rec.Body).Decode(&challenge); err != nil {
		t.Fatalf("decode challenge: %v", err)
	}
	if challenge.ChallengeID == "" || challenge.Salt == "" {
		t.Fatalf("challenge = %+v", challenge)
	}

	plain := httptest.NewRequest(http.MethodPost, "/api/auth/login/complete", strings.NewReader(`{"username":"superadmin","challenge_id":"`+challenge.ChallengeID+`","password":"`+userauth.SuperadminPassword()+`"}`))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, plain)
	if rec.Code != http.StatusOK {
		t.Fatalf("plaintext complete status = %d body = %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != "orrery_access_token" || cookies[0].Value == "" {
		t.Fatalf("login should set an auth cookie")
	}
	if _, err := auth.AuthenticateToken(context.Background(), cookies[0].Value); err != nil {
		t.Fatalf("cookie token should authenticate: %v", err)
	}
	var session userauth.SessionDTO
	if err := json.NewDecoder(rec.Body).Decode(&session); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	if session.User.Username != "superadmin" || session.User.DisplayName != "superadmin" || session.AccessToken != "" {
		t.Fatalf("session = %+v", session)
	}
}

func TestHTTPAuthRoutesRejectOversizedAndAmbiguousBodies(t *testing.T) {
	handler := NewRouter(RouterOptions{Auth: userauth.NewService(userauth.NewMemoryAccountStore())})
	tests := []struct {
		name string
		body string
	}{
		{name: "oversized", body: `{"username":"` + strings.Repeat("x", maxAuthRequestBytes) + `"}`},
		{name: "unknown field", body: `{"username":"operator","unexpected":true}`},
		{name: "trailing object", body: `{"username":"operator"}{"username":"other"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/auth/login/begin", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHTTPAuthCurrentSessionHydratesFromCookieWithoutExposingToken(t *testing.T) {
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
	handler := NewRouter(RouterOptions{Auth: auth})
	req := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
	req.AddCookie(&http.Cookie{Name: authAccessTokenCookieName, Value: session.AccessToken})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var current userauth.SessionDTO
	if err := json.NewDecoder(rec.Body).Decode(&current); err != nil {
		t.Fatalf("decode current session: %v", err)
	}
	if current.User.Username != "superadmin" || current.User.AgentProfileID != userauth.SuperadminAgentProfileID || current.AccessToken != "" {
		t.Fatalf("current session = %+v", current)
	}
}

func TestHTTPAuthCurrentSessionRejectsExternalBearerTokens(t *testing.T) {
	store := userauth.NewMemoryAccountStore()
	auth := userauth.NewService(store)
	handler := NewRouter(RouterOptions{Auth: auth})
	req := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
	req.Header.Set("Authorization", "Bearer external-oidc-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"code":"unauthorized"`) {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPAuthAllowsProductionHTTPWhenHTTPSRequirementDisabled(t *testing.T) {
	store := userauth.NewMemoryAccountStore()
	if err := userauth.EnsureBootstrapSuperadmin(context.Background(), store, userauth.SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	handler := NewRouter(RouterOptions{Environment: "production", Auth: userauth.NewService(store)})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login/begin", strings.NewReader(`{"username":"superadmin"}`))
	req.RemoteAddr = "198.51.100.20:12345"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP login status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPAuthExplicitHTTPSRequirement(t *testing.T) {
	store := userauth.NewMemoryAccountStore()
	if err := userauth.EnsureBootstrapSuperadmin(context.Background(), store, userauth.SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	handler := NewRouter(RouterOptions{Environment: "production", AuthRequireHTTPS: true, Auth: userauth.NewService(store)})

	insecure := httptest.NewRequest(http.MethodPost, "/api/auth/login/begin", strings.NewReader(`{"username":"superadmin"}`))
	insecure.RemoteAddr = "198.51.100.20:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, insecure)
	if rec.Code != http.StatusUpgradeRequired || !strings.Contains(rec.Body.String(), `"code":"https_required"`) {
		t.Fatalf("insecure status = %d body = %s", rec.Code, rec.Body.String())
	}
	for _, request := range []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/api/auth/session"},
		{method: http.MethodGet, path: "/api/auth/sessions"},
		{method: http.MethodPost, path: "/api/auth/session/rotate"},
		{method: http.MethodPost, path: "/api/auth/password/change", body: `{"new_password":"replacement-password-1"}`},
		{method: http.MethodPost, path: "/api/auth/logout"},
		{method: http.MethodGet, path: "/api/oauth2/login?login_challenge=login-1"},
	} {
		req := httptest.NewRequest(request.method, request.path, strings.NewReader(request.body))
		req.RemoteAddr = "198.51.100.20:12345"
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUpgradeRequired || !strings.Contains(rec.Body.String(), `"code":"https_required"`) {
			t.Fatalf("%s %s status = %d body = %s", request.method, request.path, rec.Code, rec.Body.String())
		}
	}

	secure := httptest.NewRequest(http.MethodPost, "/api/auth/login/begin", strings.NewReader(`{"username":"superadmin"}`))
	secure.RemoteAddr = "127.0.0.1:12345"
	secure.Header.Set("X-Forwarded-Proto", "https")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, secure)
	if rec.Code != http.StatusOK {
		t.Fatalf("trusted HTTPS proxy status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPAuthLogoutRevokesSessionAndClearsCookie(t *testing.T) {
	store := userauth.NewMemoryAccountStore()
	if err := userauth.EnsureBootstrapSuperadmin(context.Background(), store, userauth.SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	auth := userauth.NewService(store)
	handler := NewRouter(RouterOptions{Auth: auth})
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

	logout := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	logout.Header.Set("Authorization", "Bearer "+session.AccessToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, logout)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d body = %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != "orrery_access_token" || cookies[0].MaxAge >= 0 {
		t.Fatalf("logout should clear auth cookie, got %+v", cookies)
	}
	if _, err := auth.AuthenticateToken(context.Background(), session.AccessToken); !errors.Is(err, userauth.ErrUnauthorized) {
		t.Fatalf("AuthenticateToken after logout error = %v, want ErrUnauthorized", err)
	}
}

func TestHTTPAuthLocksForwardedIPAfterThreeFailedLogins(t *testing.T) {
	store := userauth.NewMemoryAccountStore()
	if err := userauth.EnsureBootstrapSuperadmin(context.Background(), store, userauth.SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	auth := userauth.NewService(store)
	handler := NewRouter(RouterOptions{Auth: auth})

	for attempt := 1; attempt <= 3; attempt++ {
		begin := httptest.NewRequest(http.MethodPost, "/api/auth/login/begin", strings.NewReader(`{"username":"superadmin"}`))
		begin.RemoteAddr = "127.0.0.1:12345"
		begin.Header.Set("X-Forwarded-For", "198.51.100.23, 10.0.0.10")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, begin)
		if rec.Code != http.StatusOK {
			t.Fatalf("begin attempt %d status = %d body = %s", attempt, rec.Code, rec.Body.String())
		}
		var challenge userauth.LoginChallengeDTO
		if err := json.NewDecoder(rec.Body).Decode(&challenge); err != nil {
			t.Fatalf("decode challenge %d: %v", attempt, err)
		}

		complete := httptest.NewRequest(http.MethodPost, "/api/auth/login/complete", strings.NewReader(`{"username":"superadmin","challenge_id":"`+challenge.ChallengeID+`","password":"wrong-password"}`))
		complete.RemoteAddr = "127.0.0.1:12345"
		complete.Header.Set("X-Forwarded-For", "198.51.100.23, 10.0.0.10")
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, complete)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("complete attempt %d status = %d body = %s", attempt, rec.Code, rec.Body.String())
		}
	}

	begin := httptest.NewRequest(http.MethodPost, "/api/auth/login/begin", strings.NewReader(`{"username":"superadmin"}`))
	begin.RemoteAddr = "127.0.0.1:12345"
	begin.Header.Set("X-Forwarded-For", "198.51.100.23, 10.0.0.10")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, begin)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("locked begin status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"rate_limited"`) {
		t.Fatalf("locked response should use rate_limited code: %s", rec.Body.String())
	}
}

func TestHTTPAuthIPLocksCanBeListedAndUnlockedByAdmin(t *testing.T) {
	store := userauth.NewMemoryAccountStore()
	if err := userauth.EnsureBootstrapSuperadmin(context.Background(), store, userauth.SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	auth := userauth.NewService(store)
	handler := NewRouter(RouterOptions{Auth: auth, TrustedViewerHeaders: true, InternalViewerAuth: "Bearer test-internal-viewer"})
	lockLoginIP(t, handler, "198.51.100.24")

	list := httptest.NewRequest(http.MethodGet, "/api/audit/ip-locks", nil)
	list.Header.Set("X-User-ID", "superadmin")
	list.Header.Set("X-Viewer-Kind", "user")
	list.Header.Set("X-Permission-Scope", "audit.read,audit.manage")
	list.Header.Set("X-Orrery-Internal-Viewer-Authorization", "Bearer test-internal-viewer")
	list.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, list)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"ip_address":"198.51.100.24"`) {
		t.Fatalf("list response missing locked IP: %s", rec.Body.String())
	}

	unlock := httptest.NewRequest(http.MethodPost, "/api/audit/ip-locks/unlock", strings.NewReader(`{"ip_address":"198.51.100.24","reason":"operator reviewed"}`))
	unlock.Header.Set("X-User-ID", "superadmin")
	unlock.Header.Set("X-Viewer-Kind", "user")
	unlock.Header.Set("X-Permission-Scope", "audit.manage")
	unlock.Header.Set("X-Orrery-Internal-Viewer-Authorization", "Bearer test-internal-viewer")
	unlock.RemoteAddr = "127.0.0.1:12345"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, unlock)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("unlock status = %d body = %s", rec.Code, rec.Body.String())
	}

	begin := httptest.NewRequest(http.MethodPost, "/api/auth/login/begin", strings.NewReader(`{"username":"superadmin"}`))
	begin.Header.Set("X-Forwarded-For", "198.51.100.24")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, begin)
	if rec.Code != http.StatusOK {
		t.Fatalf("begin after unlock status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPAuthTokenOverridesSpoofedViewerHeaders(t *testing.T) {
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
	service := &fakeOperationsService{
		workforce: operationsWorkforceWithSuperadmin(),
	}
	handler := NewRouter(RouterOptions{Auth: auth, Operations: service})

	req := httptest.NewRequest(http.MethodGet, "/api/workforce/members", nil)
	req.Header.Set("Authorization", "Bearer "+session.AccessToken)
	req.Header.Set("X-User-ID", "local-user")
	req.Header.Set("X-Agent-Profile-ID", "attacker-profile")
	req.Header.Set("X-User-Display-Name", "%E6%9C%AC%E5%9C%B0%E7%94%A8%E6%88%B7")
	req.Header.Set("X-Permission-Scope", "workforce.read")
	for _, header := range []string{
		"X-Agent-ID", "X-Agent-IDs", "X-Human-ID", "X-Human-IDs", "X-Organization-ID", "X-Organization-IDs",
		"X-Department-ID", "X-Department-IDs", "X-Project-ID", "X-Project-IDs", "X-Mission-ID", "X-Mission-IDs",
		"X-Discussion-ID", "X-Discussion-IDs", "X-Position-ID", "X-Position-IDs", "X-Role-ID", "X-Role-IDs",
		"X-Tenant", "X-Mission-Roles",
	} {
		req.Header.Set(header, "attacker-value")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.lastViewer.ViewerID != agentcore.SystemSuperadminPrincipalID || service.lastViewer.ViewerKind != "system" || service.lastViewer.DisplayName != "superadmin" {
		t.Fatalf("viewer should come from auth token, got %+v", service.lastViewer)
	}
	if got := req.Header.Get("X-Agent-Profile-ID"); got != userauth.SuperadminAgentProfileID {
		t.Fatalf("agent profile header = %q, want session profile", got)
	}
	for _, header := range []string{
		"X-Agent-ID", "X-Agent-IDs", "X-Human-ID", "X-Human-IDs", "X-Organization-ID", "X-Organization-IDs",
		"X-Department-ID", "X-Department-IDs", "X-Project-ID", "X-Project-IDs", "X-Mission-ID", "X-Mission-IDs",
		"X-Discussion-ID", "X-Discussion-IDs", "X-Position-ID", "X-Position-IDs", "X-Role-ID", "X-Role-IDs",
		"X-Tenant", "X-Mission-Roles",
	} {
		if got := req.Header.Get(header); got != "" {
			t.Fatalf("untrusted %s survived authentication as %q", header, got)
		}
	}
}

func TestHTTPAuthAcceptsSessionCookie(t *testing.T) {
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
	service := &fakeOperationsService{
		workforce: operationsWorkforceWithSuperadmin(),
	}
	handler := NewRouter(RouterOptions{Auth: auth, Operations: service})

	req := httptest.NewRequest(http.MethodGet, "/api/workforce/members", nil)
	req.AddCookie(&http.Cookie{Name: "orrery_access_token", Value: session.AccessToken})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.lastViewer.ViewerID != agentcore.SystemSuperadminPrincipalID || service.lastViewer.ViewerKind != "system" || service.lastViewer.DisplayName != "superadmin" {
		t.Fatalf("viewer should come from auth cookie, got %+v", service.lastViewer)
	}
}

func TestCookieAuthenticatedMutationRequiresSameOrigin(t *testing.T) {
	store := userauth.NewMemoryAccountStore()
	if err := userauth.EnsureBootstrapSuperadmin(context.Background(), store, userauth.SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	auth := userauth.NewService(store)
	challenge, err := auth.BeginLogin(context.Background(), userauth.BeginLoginRequest{Username: userauth.SuperadminUsername})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	session, err := auth.CompleteLogin(context.Background(), userauth.CompleteLoginRequest{
		Username:    userauth.SuperadminUsername,
		ChallengeID: challenge.ChallengeID,
		Password:    userauth.SuperadminPassword(),
	})
	if err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}
	handler := NewRouter(RouterOptions{
		Auth:       auth,
		Operations: &fakeOperationsService{member: operations.WorkforceMemberDTO{AgentProfileID: "profile-1"}},
		AgentProfiles: &fakeProfileWeixinDirectory{entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID: agentcore.AgentProfileID(userauth.SuperadminAgentProfileID),
			AgentKind:      agentcore.AgentKindHuman,
			Enabled:        true,
		}}},
	})

	request := func(origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/workforce/members", strings.NewReader(`{"real_name":"Operator"}`))
		req.AddCookie(&http.Cookie{Name: authAccessTokenCookieName, Value: session.AccessToken})
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	if rec := request(""); rec.Code != http.StatusForbidden {
		t.Fatalf("missing-origin mutation status = %d body = %s", rec.Code, rec.Body.String())
	}
	if rec := request("http://evil.example.test"); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin mutation status = %d body = %s", rec.Code, rec.Body.String())
	}
	if rec := request("http://example.com"); rec.Code != http.StatusCreated {
		t.Fatalf("same-origin mutation status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPAuthAcceptsOIDCBearerPrincipal(t *testing.T) {
	service := &fakeOperationsService{
		workforce: operationsWorkforceWithSuperadmin(),
	}
	bearer := &fakeBearerAuthService{
		principal: identity.Principal{
			SubjectProvider: identity.ProviderHydra,
			Subject:         "hydra-sub-1",
			UserID:          "human-operator",
			Username:        "operator",
			DisplayName:     "Operator",
			AgentProfileID:  "human-operator",
			ViewerKind:      identity.ViewerKindUser,
			CoarseScopes:    []string{"workforce.read"},
		},
	}
	handler := NewRouter(RouterOptions{BearerAuth: bearer, Operations: service})

	req := httptest.NewRequest(http.MethodGet, "/api/workforce/members", nil)
	req.Header.Set("Authorization", "Bearer opaque-oidc-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if bearer.token != "opaque-oidc-token" || bearer.calls != 1 {
		t.Fatalf("bearer service token=%q calls=%d", bearer.token, bearer.calls)
	}
	if service.lastViewer.ViewerID != "operator" || service.lastViewer.DisplayName != "Operator" || req.Header.Get("X-Agent-Profile-ID") != "human-operator" {
		t.Fatalf("viewer should come from OIDC principal, got %+v", service.lastViewer)
	}
}

func TestHTTPAuthRejectsBearerPrincipalWithoutRouteScope(t *testing.T) {
	service := &fakeOperationsService{
		workforce: operationsWorkforceWithSuperadmin(),
	}
	bearer := &fakeBearerAuthService{
		principal: identity.Principal{
			SubjectProvider: identity.ProviderHydra,
			Subject:         "hydra-sub-1",
			UserID:          "human-operator",
			Username:        "operator",
			AgentProfileID:  "human-operator",
			ViewerKind:      identity.ViewerKindUser,
		},
	}
	handler := NewRouter(RouterOptions{BearerAuth: bearer, Operations: service})

	req := httptest.NewRequest(http.MethodGet, "/api/workforce/members", nil)
	req.Header.Set("Authorization", "Bearer opaque-oidc-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want forbidden", rec.Code, rec.Body.String())
	}
	if service.lastViewer.ViewerID != "" {
		t.Fatalf("handler should not be reached, viewer = %+v", service.lastViewer)
	}
}

func TestHTTPAuthBearerAppSessionStillUsesAppSessionService(t *testing.T) {
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
	service := &fakeOperationsService{
		workforce: operationsWorkforceWithSuperadmin(),
	}
	bearer := &fakeBearerAuthService{
		err: userauth.ErrUnauthorized,
	}
	handler := NewRouter(RouterOptions{Auth: auth, BearerAuth: bearer, Operations: service})

	req := httptest.NewRequest(http.MethodGet, "/api/workforce/members", nil)
	req.Header.Set("Authorization", "Bearer "+session.AccessToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if bearer.calls != 0 {
		t.Fatalf("app session bearer token should not call OIDC bearer service, calls=%d", bearer.calls)
	}
	if service.lastViewer.ViewerID != agentcore.SystemSuperadminPrincipalID || service.lastViewer.ViewerKind != "system" {
		t.Fatalf("viewer should come from app session, got %+v", service.lastViewer)
	}
}

func lockLoginIP(t *testing.T, handler http.Handler, ipAddress string) {
	t.Helper()
	for attempt := 1; attempt <= 3; attempt++ {
		begin := httptest.NewRequest(http.MethodPost, "/api/auth/login/begin", strings.NewReader(`{"username":"superadmin"}`))
		begin.RemoteAddr = "127.0.0.1:12345"
		begin.Header.Set("X-Forwarded-For", ipAddress)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, begin)
		if rec.Code != http.StatusOK {
			t.Fatalf("begin lock attempt %d status = %d body = %s", attempt, rec.Code, rec.Body.String())
		}
		var challenge userauth.LoginChallengeDTO
		if err := json.NewDecoder(rec.Body).Decode(&challenge); err != nil {
			t.Fatalf("decode challenge %d: %v", attempt, err)
		}
		complete := httptest.NewRequest(http.MethodPost, "/api/auth/login/complete", strings.NewReader(`{"username":"superadmin","challenge_id":"`+challenge.ChallengeID+`","password":"wrong-password"}`))
		complete.RemoteAddr = "127.0.0.1:12345"
		complete.Header.Set("X-Forwarded-For", ipAddress)
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, complete)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("complete lock attempt %d status = %d body = %s", attempt, rec.Code, rec.Body.String())
		}
	}
}

func TestHTTPAuthRejectsSpoofedViewerHeadersWithoutBearerToken(t *testing.T) {
	t.Setenv("ORRERY_INTERNAL_VIEWER_AUTHORIZATION", "")
	t.Setenv("ORRERY_INTERNAL_VIEWER_TOKEN", "")
	store := userauth.NewMemoryAccountStore()
	if err := userauth.EnsureBootstrapSuperadmin(context.Background(), store, userauth.SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	auth := userauth.NewService(store)
	service := &fakeOperationsService{
		workforce: operationsWorkforceWithSuperadmin(),
	}
	handler := NewRouter(RouterOptions{Auth: auth, Operations: service})

	req := httptest.NewRequest(http.MethodGet, "/api/workforce/members", nil)
	req.Header.Set("X-User-ID", "superadmin")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Permission-Scope", "workforce.read")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s, want unauthorized", rec.Code, rec.Body.String())
	}
	if service.lastViewer.ViewerID != "" {
		t.Fatalf("handler should not be reached, viewer = %+v", service.lastViewer)
	}
}

func TestHTTPAuthRejectsInternalViewerHeadersWhenTrustedHeadersDisabled(t *testing.T) {
	t.Setenv("ORRERY_INTERNAL_VIEWER_AUTHORIZATION", "Bearer internal-viewer-token")
	store := userauth.NewMemoryAccountStore()
	if err := userauth.EnsureBootstrapSuperadmin(context.Background(), store, userauth.SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	auth := userauth.NewService(store)
	service := &fakeOperationsService{
		workforce: operationsWorkforceWithSuperadmin(),
	}
	handler := NewRouter(RouterOptions{Auth: auth, Operations: service})

	req := httptest.NewRequest(http.MethodGet, "/api/workforce/members", nil)
	req.Header.Set("X-Orrery-Internal-Viewer-Authorization", "Bearer internal-viewer-token")
	req.Header.Set("X-User-ID", "superadmin")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Permission-Scope", "workforce.read")
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s, want unauthorized", rec.Code, rec.Body.String())
	}
	if service.lastViewer.ViewerID != "" {
		t.Fatalf("handler should not be reached, viewer = %+v", service.lastViewer)
	}
}

func TestHTTPAuthAllowsInternalViewerHeadersOnlyWithConfiguredAuthorization(t *testing.T) {
	store := userauth.NewMemoryAccountStore()
	if err := userauth.EnsureBootstrapSuperadmin(context.Background(), store, userauth.SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	auth := userauth.NewService(store)
	service := &fakeOperationsService{
		workforce: operationsWorkforceWithSuperadmin(),
	}
	handler := NewRouter(RouterOptions{Auth: auth, Operations: service, TrustedViewerHeaders: true, InternalViewerAuth: "Bearer internal-viewer-token"})

	req := httptest.NewRequest(http.MethodGet, "/api/workforce/members", nil)
	req.Header.Set("X-Orrery-Internal-Viewer-Authorization", "Bearer internal-viewer-token")
	req.Header.Set("X-User-ID", "superadmin")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Permission-Scope", "workforce.read")
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.lastViewer.ViewerID != "superadmin" {
		t.Fatalf("viewer = %+v", service.lastViewer)
	}
}

func TestHTTPAuthRejectsInternalViewerHeadersFromForwardedRequest(t *testing.T) {
	store := userauth.NewMemoryAccountStore()
	if err := userauth.EnsureBootstrapSuperadmin(context.Background(), store, userauth.SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	auth := userauth.NewService(store)
	service := &fakeOperationsService{
		workforce: operationsWorkforceWithSuperadmin(),
	}
	handler := NewRouter(RouterOptions{Auth: auth, Operations: service, TrustedViewerHeaders: true, InternalViewerAuth: "Bearer internal-viewer-token"})

	req := httptest.NewRequest(http.MethodGet, "/api/workforce/members", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "198.51.100.99")
	req.Header.Set("X-Orrery-Internal-Viewer-Authorization", "Bearer internal-viewer-token")
	req.Header.Set("X-User-ID", "superadmin")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Permission-Scope", "operations.read")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s, want unauthorized", rec.Code, rec.Body.String())
	}
	if service.lastViewer.ViewerID != "" {
		t.Fatalf("handler should not be reached, viewer = %+v", service.lastViewer)
	}
}

func TestHTTPAuthProvidersExposeBrowserProviderAndLocalState(t *testing.T) {
	identityAuth := &fakeIdentityAuthService{
		localEnabled: false,
		providers: []identity.ProviderInfo{{
			Name:        identity.ProviderKratos,
			DisplayName: "Company SSO",
			Kind:        "browser",
			Enabled:     true,
			Primary:     true,
		}},
	}
	handler := NewRouter(RouterOptions{IdentityAuth: identityAuth})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/providers", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var body authProviderResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode providers: %v", err)
	}
	if len(body.Providers) != 1 {
		t.Fatalf("providers = %+v", body.Providers)
	}
	provider := body.Providers[0]
	if provider.Name != identity.ProviderKratos || provider.DisplayName != "Company SSO" || provider.Kind != "browser" || provider.BeginURL != "/api/auth/identity/kratos/begin" {
		t.Fatalf("provider = %+v", provider)
	}
}

func TestHTTPAuthLocalLoginDisabledByIdentityAuth(t *testing.T) {
	store := userauth.NewMemoryAccountStore()
	if err := userauth.EnsureBootstrapSuperadmin(context.Background(), store, userauth.SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	handler := NewRouter(RouterOptions{
		Auth:         userauth.NewService(store),
		IdentityAuth: &fakeIdentityAuthService{localEnabled: false},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login/begin", strings.NewReader(`{"username":"superadmin"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPAuthBeginsIdentityLoginAsJSON(t *testing.T) {
	expiresAt := time.Now().UTC().Add(10 * time.Minute)
	identityAuth := &fakeIdentityAuthService{
		localEnabled:   true,
		redirectURL:    "https://kratos.example/self-service/login/browser?return_to=callback",
		redirectState:  "signed-state",
		redirectExpiry: expiresAt,
	}
	handler := NewRouter(RouterOptions{IdentityAuth: identityAuth})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/identity/kratos/begin?return_to=/missions", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var redirect identity.LoginRedirect
	if err := json.NewDecoder(rec.Body).Decode(&redirect); err != nil {
		t.Fatalf("decode redirect: %v", err)
	}
	if redirect.URL != identityAuth.redirectURL || identityAuth.beginProvider != identity.ProviderKratos || identityAuth.beginReturnTo != "/missions" {
		t.Fatalf("redirect = %+v provider = %q return_to = %q", redirect, identityAuth.beginProvider, identityAuth.beginReturnTo)
	}
	var flowCookie *http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == identity.BrowserFlowCookieName(identity.ProviderKratos) {
			flowCookie = cookie
			break
		}
	}
	if flowCookie == nil || flowCookie.Value != "signed-state" || !flowCookie.HttpOnly || flowCookie.SameSite != http.SameSiteLaxMode || flowCookie.Path != "/api/" {
		t.Fatalf("flow cookie = %+v", flowCookie)
	}
}

func TestHTTPAuthIdentityLogoutRedirectsCurrentBrowserToProvider(t *testing.T) {
	identityAuth := &fakeIdentityAuthService{logoutRedirectURL: "https://kratos.example/self-service/logout/browser?return_to=app"}
	handler := NewRouter(RouterOptions{IdentityAuth: identityAuth})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/identity/kratos/logout?return_to=%2Flogin", nil)
	req.Header.Set("Cookie", "ory_kratos_session=abc")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != identityAuth.logoutRedirectURL {
		t.Fatalf("location = %q", got)
	}
	if identityAuth.logoutProvider != identity.ProviderKratos || identityAuth.logoutReturnTo != "/login" || identityAuth.logoutCookie != "ory_kratos_session=abc" {
		t.Fatalf("provider = %q return_to = %q cookie = %q", identityAuth.logoutProvider, identityAuth.logoutReturnTo, identityAuth.logoutCookie)
	}
}

func TestHTTPAuthIdentityLogoutSurvivesCanceledBrowserRequest(t *testing.T) {
	identityAuth := &cancelAwareIdentityAuthService{
		fakeIdentityAuthService: &fakeIdentityAuthService{
			logoutRedirectURL: "https://kratos.example/self-service/logout/browser?return_to=app",
		},
	}
	handler := NewRouter(RouterOptions{IdentityAuth: identityAuth})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/auth/identity/kratos/logout?return_to=%2Flogin", nil).WithContext(ctx)
	req.Header.Set("Cookie", "ory_kratos_session=abc")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if identityAuth.logoutContextErr != nil {
		t.Fatalf("logout context error = %v", identityAuth.logoutContextErr)
	}
	if !identityAuth.logoutContextHasDeadline {
		t.Fatal("logout context should have a bounded deadline")
	}
}

func TestHTTPAuthIdentityLogoutDefaultsUnsafeReturnToToLogin(t *testing.T) {
	identityAuth := &fakeIdentityAuthService{logoutRedirectURL: "https://kratos.example/self-service/logout/browser?return_to=app"}
	handler := NewRouter(RouterOptions{IdentityAuth: identityAuth})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/identity/kratos/logout?return_to=https%3A%2F%2Fevil.example", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther || identityAuth.logoutReturnTo != "/login" {
		t.Fatalf("status = %d return_to = %q body = %s", rec.Code, identityAuth.logoutReturnTo, rec.Body.String())
	}
}

func TestHTTPAuthIdentityLogoutRejectsWrongMethod(t *testing.T) {
	handler := NewRouter(RouterOptions{IdentityAuth: &fakeIdentityAuthService{}})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/identity/kratos/logout", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPAuthReturnsKratosLoginFlow(t *testing.T) {
	identityAuth := &fakeIdentityAuthService{
		flow: identity.BrowserLoginFlow{
			ID:     kratosLoginFlowIDForHTTP,
			Action: "https://kratos.example/self-service/login?flow=" + kratosLoginFlowIDForHTTP,
			Method: http.MethodPost,
			Nodes: []identity.LoginFlowNode{{
				Type:  "input",
				Group: "password",
				Attributes: identity.LoginFlowNodeAttributes{
					Name:     "password",
					Type:     "password",
					Required: true,
				},
			}},
		},
	}
	handler := NewRouter(RouterOptions{IdentityAuth: identityAuth})
	req := httptest.NewRequest(http.MethodGet, "/api/auth/identity/kratos/login-flow?id="+kratosLoginFlowIDForHTTP, nil)
	req.Header.Set("Cookie", "csrf_token_flow=csrf-cookie; orrery_session=app-cookie")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var flow identity.BrowserLoginFlow
	if err := json.NewDecoder(rec.Body).Decode(&flow); err != nil {
		t.Fatalf("decode flow: %v", err)
	}
	if flow.ID != kratosLoginFlowIDForHTTP || identityAuth.flowProvider != identity.ProviderKratos || identityAuth.flowID != kratosLoginFlowIDForHTTP {
		t.Fatalf("flow = %+v provider = %q id = %q", flow, identityAuth.flowProvider, identityAuth.flowID)
	}
	if identityAuth.flowCookie != "csrf_token_flow=csrf-cookie; orrery_session=app-cookie" {
		t.Fatalf("flow cookie = %q", identityAuth.flowCookie)
	}
	assertPrivateLoginFlowHeaders(t, rec)
}

func TestHTTPAuthMapsKratosLoginFlowFailures(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "cookie invalid", err: identity.ErrFlowCookieInvalid, status: http.StatusForbidden, code: ErrorFlowCookieInvalid},
		{name: "not found", err: identity.ErrFlowNotFound, status: http.StatusNotFound, code: ErrorFlowNotFound},
		{name: "expired", err: identity.ErrFlowExpired, status: http.StatusGone, code: ErrorFlowExpired},
		{name: "unavailable", err: identity.ErrServiceUnavailable, status: http.StatusServiceUnavailable, code: ErrorResourceUnavailable},
		{name: "invalid", err: identity.ErrInvalidRequest, status: http.StatusBadRequest, code: ErrorInvalidRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewRouter(RouterOptions{IdentityAuth: &fakeIdentityAuthService{flowErr: tt.err}})
			req := httptest.NewRequest(http.MethodGet, "/api/auth/identity/kratos/login-flow?id="+kratosLoginFlowIDForHTTP, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tt.status || !strings.Contains(rec.Body.String(), `"code":"`+tt.code+`"`) {
				t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
			}
			assertPrivateLoginFlowHeaders(t, rec)
		})
	}
}

func assertPrivateLoginFlowHeaders(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := strings.Join(rec.Header().Values("Vary"), ","); !strings.Contains(got, "Cookie") {
		t.Fatalf("Vary = %q", got)
	}
}

func TestHTTPAuthKratosLoginFlowRejectsWrongMethod(t *testing.T) {
	handler := NewRouter(RouterOptions{IdentityAuth: &fakeIdentityAuthService{}})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/identity/kratos/login-flow?id="+kratosLoginFlowIDForHTTP, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

const kratosLoginFlowIDForHTTP = "550dd231-4312-4a22-9dcf-4c0d43c6f608"

func TestHTTPAuthCompletesIdentityLoginWithAppSessionCookie(t *testing.T) {
	expiresAt := time.Date(2026, 7, 2, 13, 0, 0, 0, time.UTC)
	identityAuth := &fakeIdentityAuthService{
		localEnabled: true,
		session: userauth.SessionDTO{
			AccessToken:     "iat-external-session",
			SubjectProvider: identity.ProviderKratos,
			Subject:         "kratos-id-1",
			User: userauth.UserDTO{
				UserID:         "human-operator",
				Username:       "operator",
				DisplayName:    "Operator",
				AgentProfileID: "human-operator",
			},
			PermissionScope: []string{"operations.read"},
			ExpiresAt:       expiresAt,
		},
	}
	handler := NewRouter(RouterOptions{IdentityAuth: identityAuth})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/identity/kratos/callback?code=abc&state=xyz&return_to=/missions", nil)
	req.Host = "app.example"
	req.Header.Set("Cookie", "ory_kratos_session=abc")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/missions" {
		t.Fatalf("location = %q", got)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != "orrery_access_token" || cookies[0].Value != "iat-external-session" || !cookies[0].HttpOnly {
		t.Fatalf("cookies = %+v", cookies)
	}
	if cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("session SameSite = %v, want Lax", cookies[0].SameSite)
	}
	if identityAuth.completeProvider != identity.ProviderKratos || identityAuth.completeCookie != "ory_kratos_session=abc" {
		t.Fatalf("complete provider/cookie = %q %q", identityAuth.completeProvider, identityAuth.completeCookie)
	}
	if !strings.HasPrefix(identityAuth.completeRequestURL, "http://app.example/api/auth/identity/kratos/callback") {
		t.Fatalf("complete request url = %q", identityAuth.completeRequestURL)
	}
}

func TestHTTPAuthRedirectsUnavailableKratosBrowserSessionToLogin(t *testing.T) {
	identityAuth := &fakeIdentityAuthService{completeErr: identity.ErrBrowserSessionUnavailable}
	handler := NewRouter(RouterOptions{IdentityAuth: identityAuth})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/identity/kratos/callback", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d body = %s, want redirect", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/login" {
		t.Fatalf("location = %q, want /login", got)
	}
}

func TestHTTPAuthKeepsIdentityAuthorizationFailureAsUnauthorized(t *testing.T) {
	identityAuth := &fakeIdentityAuthService{completeErr: userauth.ErrUnauthorized}
	handler := NewRouter(RouterOptions{IdentityAuth: identityAuth})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/identity/kratos/callback", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s, want unauthorized", rec.Code, rec.Body.String())
	}
}

func operationsWorkforceWithSuperadmin() operations.WorkforcePageDTO {
	return operations.WorkforcePageDTO{
		Members: []operations.WorkforceMemberDTO{{
			AgentID:        "human-superadmin",
			AgentProfileID: "human-superadmin",
			RealName:       "superadmin",
			DisplayName:    "superadmin",
			AgentKind:      "human",
			EmploymentType: "human",
			Enabled:        true,
		}},
	}
}

type cancelAwareIdentityAuthService struct {
	*fakeIdentityAuthService
	logoutContextErr         error
	logoutContextHasDeadline bool
}

func (s *cancelAwareIdentityAuthService) BeginBrowserLogout(ctx context.Context, provider string, req identity.BeginLogoutRequest) (identity.LoginRedirect, error) {
	s.logoutContextErr = ctx.Err()
	_, s.logoutContextHasDeadline = ctx.Deadline()
	if s.logoutContextErr != nil {
		return identity.LoginRedirect{}, s.logoutContextErr
	}
	return s.fakeIdentityAuthService.BeginBrowserLogout(ctx, provider, req)
}

type fakeIdentityAuthService struct {
	localEnabled       bool
	providers          []identity.ProviderInfo
	redirectURL        string
	redirectState      string
	redirectExpiry     time.Time
	session            userauth.SessionDTO
	beginProvider      string
	beginReturnTo      string
	completeProvider   string
	completeCookie     string
	completeRequestURL string
	completeErr        error
	flow               identity.BrowserLoginFlow
	flowErr            error
	flowProvider       string
	flowID             string
	flowCookie         string
	logoutRedirectURL  string
	logoutProvider     string
	logoutReturnTo     string
	logoutCookie       string
	logoutErr          error
}

func (s *fakeIdentityAuthService) LocalLoginEnabled(ctx context.Context) bool {
	_ = ctx
	return s == nil || s.localEnabled
}

func (s *fakeIdentityAuthService) Providers(ctx context.Context) []identity.ProviderInfo {
	_ = ctx
	if s == nil {
		return nil
	}
	return s.providers
}

func (s *fakeIdentityAuthService) BeginBrowserLogin(ctx context.Context, provider string, req identity.BeginLoginRequest) (identity.LoginRedirect, error) {
	_ = ctx
	s.beginProvider = provider
	s.beginReturnTo = req.ReturnTo
	return identity.LoginRedirect{URL: s.redirectURL, FlowState: s.redirectState, ExpiresAt: s.redirectExpiry}, nil
}

func (s *fakeIdentityAuthService) CompleteBrowserLogin(ctx context.Context, provider string, req identity.CompleteLoginRequest) (userauth.SessionDTO, error) {
	_ = ctx
	s.completeProvider = provider
	s.completeCookie = req.Cookie
	s.completeRequestURL = req.RequestURL
	return s.session, s.completeErr
}

func (s *fakeIdentityAuthService) BeginBrowserLogout(ctx context.Context, provider string, req identity.BeginLogoutRequest) (identity.LoginRedirect, error) {
	_ = ctx
	s.logoutProvider = provider
	s.logoutReturnTo = req.ReturnTo
	s.logoutCookie = req.Cookie
	return identity.LoginRedirect{URL: s.logoutRedirectURL}, s.logoutErr
}

func (s *fakeIdentityAuthService) GetBrowserLoginFlow(ctx context.Context, provider string, req identity.LoginFlowRequest) (identity.BrowserLoginFlow, error) {
	_ = ctx
	s.flowProvider = provider
	s.flowID = req.ID
	s.flowCookie = req.Cookie
	return s.flow, s.flowErr
}

type fakeBearerAuthService struct {
	principal identity.Principal
	err       error
	token     string
	calls     int
}

func (s *fakeBearerAuthService) AuthenticateBearer(ctx context.Context, accessToken string) (identity.Principal, error) {
	_ = ctx
	s.calls++
	s.token = accessToken
	if s.err != nil {
		return identity.Principal{}, s.err
	}
	return identity.NormalizePrincipal(s.principal), nil
}
