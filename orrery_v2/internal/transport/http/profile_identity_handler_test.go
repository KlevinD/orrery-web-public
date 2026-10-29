package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/userauth"
)

func TestHTTPProfileFeishuIdentityBindingFlow(t *testing.T) {
	expiresAt := time.Now().UTC().Add(10 * time.Minute)
	service := &fakeProfileIdentityBindingService{
		binding: identity.SelfServiceBindingDTO{
			Provider: identity.ProviderFeishu, Configured: true, Bound: true, DisplayName: "Alice", Email: "alice@example.test",
		},
		redirect: identity.LoginRedirect{
			URL: "https://accounts.feishu.example/authorize", FlowState: "signed-state", ExpiresAt: expiresAt,
		},
		returnTo: "/profile",
	}
	authStore := userauth.NewMemoryAccountStore()
	if err := authStore.SaveAccount(context.Background(), userauth.Account{
		UserID: "agent-1", Username: "agent-1", DisplayName: "Alice", AgentProfileID: "agent-1", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	authService := userauth.NewService(authStore)
	session, err := authService.CreateSessionForIdentitySubject(context.Background(), identity.IdentitySubject{
		ID: "local:agent-1", Provider: identity.ProviderLocal, Subject: "agent-1", Username: "agent-1", AgentProfileID: "agent-1", Status: identity.SubjectStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestRouter(RouterOptions{Auth: authService, ProfileIdentity: service})

	t.Run("status", func(t *testing.T) {
		req := profileIdentityRequest(http.MethodGet, "/api/profile/identity/feishu", session.AccessToken)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
		}
		var binding identity.SelfServiceBindingDTO
		if err := json.NewDecoder(rec.Body).Decode(&binding); err != nil {
			t.Fatal(err)
		}
		if !binding.Bound || binding.DisplayName != "Alice" || service.getProfileID != "agent-1" {
			t.Fatalf("binding = %+v profile = %q", binding, service.getProfileID)
		}
	})

	t.Run("begin", func(t *testing.T) {
		req := profileIdentityRequest(http.MethodGet, "/api/profile/identity/feishu/begin?return_to=%2Fprofile", session.AccessToken)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != service.redirect.URL {
			t.Fatalf("status = %d location = %q", rec.Code, rec.Header().Get("Location"))
		}
		if service.beginProfileID != "agent-1" || service.beginReturnTo != "/profile" {
			t.Fatalf("profile = %q return_to = %q", service.beginProfileID, service.beginReturnTo)
		}
		cookies := rec.Result().Cookies()
		if len(cookies) != 1 || cookies[0].Name != identity.BrowserFlowCookieName(identity.ProviderFeishu) || cookies[0].Value != "signed-state" || cookies[0].SameSite != http.SameSiteLaxMode {
			t.Fatalf("cookies = %+v", cookies)
		}
	})

	t.Run("callback", func(t *testing.T) {
		req := profileIdentityRequest(http.MethodGet, "/api/profile/identity/feishu/callback?code=code-1&state=signed-state", session.AccessToken)
		req.AddCookie(&http.Cookie{Name: identity.BrowserFlowCookieName(identity.ProviderFeishu), Value: "signed-state"})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/profile?feishu_sso=bound" {
			t.Fatalf("status = %d location = %q body = %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
		}
		if service.completeProfileID != "agent-1" || service.completeRequest.Code != "code-1" || service.completeRequest.State != "signed-state" {
			t.Fatalf("profile = %q request = %+v", service.completeProfileID, service.completeRequest)
		}
	})

	t.Run("unbind", func(t *testing.T) {
		req := profileIdentityRequest(http.MethodDelete, "/api/profile/identity/feishu", session.AccessToken)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK || service.unbindProfileID != "agent-1" {
			t.Fatalf("status = %d body = %s profile = %q", rec.Code, rec.Body.String(), service.unbindProfileID)
		}
	})
}

func TestHTTPProfileFeishuIdentityRequiresAuthentication(t *testing.T) {
	handler := newTestRouter(RouterOptions{ProfileIdentity: &fakeProfileIdentityBindingService{}})
	req := httptest.NewRequest(http.MethodGet, "/api/profile/identity/feishu", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func profileIdentityRequest(method, path, accessToken string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.AddCookie(&http.Cookie{Name: authAccessTokenCookieName, Value: accessToken})
	return req
}

type fakeProfileIdentityBindingService struct {
	binding           identity.SelfServiceBindingDTO
	redirect          identity.LoginRedirect
	returnTo          string
	getProfileID      string
	beginProfileID    string
	beginReturnTo     string
	completeProfileID string
	completeRequest   identity.CompleteLoginRequest
	unbindProfileID   string
}

func (s *fakeProfileIdentityBindingService) GetBinding(_ context.Context, agentProfileID string) (identity.SelfServiceBindingDTO, error) {
	s.getProfileID = agentProfileID
	return s.binding, nil
}

func (s *fakeProfileIdentityBindingService) BeginBinding(_ context.Context, agentProfileID, returnTo string) (identity.LoginRedirect, error) {
	s.beginProfileID = agentProfileID
	s.beginReturnTo = returnTo
	return s.redirect, nil
}

func (s *fakeProfileIdentityBindingService) CompleteBinding(_ context.Context, agentProfileID string, req identity.CompleteLoginRequest) (identity.SelfServiceBindingDTO, error) {
	s.completeProfileID = agentProfileID
	s.completeRequest = req
	return s.binding, nil
}

func (s *fakeProfileIdentityBindingService) Unbind(_ context.Context, agentProfileID string) (identity.SelfServiceBindingDTO, error) {
	s.unbindProfileID = agentProfileID
	return identity.SelfServiceBindingDTO{Provider: identity.ProviderFeishu, Configured: true}, nil
}

func (s *fakeProfileIdentityBindingService) BindingReturnTo(string) string {
	return s.returnTo
}
