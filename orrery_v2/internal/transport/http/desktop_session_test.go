package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/userauth"
)

func TestHTTPDesktopSessionUsesVerifiedIdentityAndLivePermissions(t *testing.T) {
	ctx := context.Background()
	accounts := userauth.NewMemoryAccountStore()
	account := userauth.Account{UserID: "user-desk", Username: "desk", DisplayName: "Desk", AgentProfileID: "H-desktop", Enabled: true, PermissionScope: []string{"mission.read", "operations.read"}}
	if err := accounts.SaveAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	subjects := identity.NewMemorySubjectStore()
	subject := identity.IdentitySubject{Provider: identity.ProviderKratos, Subject: "kratos-desk", UserID: "user-desk", Username: "desk", AgentProfileID: "H-desktop", Status: identity.SubjectStatusActive}
	if err := subjects.SaveSubject(ctx, subject); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	claims := map[string]any{
		"active": true, "client_id": "orrery-desktop", "sub": "H-desktop",
		"iss": "https://identity.example/hydra", "aud": []string{"orrery-api"}, "exp": expires.Unix(),
		"scope": "openid profile offline_access",
		"ext":   map[string]any{"subject_provider": "kratos", "subject": "kratos-desk", "agent_profile_id": "H-desktop", "username": "desk"},
	}
	introspection := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if err := req.ParseForm(); err != nil {
			t.Error(err)
		}
		if req.Form.Get("token") != "desktop-opaque-token" {
			t.Error("unexpected introspection credential")
		}
		_ = json.NewEncoder(w).Encode(claims)
	}))
	defer introspection.Close()
	provider, err := identity.NewOIDCTokenProvider(identity.OIDCTokenProviderConfig{IntrospectionURL: introspection.URL, Issuer: "https://identity.example/hydra", Audience: "orrery-api"})
	if err != nil {
		t.Fatal(err)
	}
	auth := userauth.NewService(accounts).WithIdentitySubjectStore(subjects)
	bearer := userauth.NewTokenPrincipalService(auth, identity.NewSubjectMapper(subjects), provider)
	handler := NewRouter(RouterOptions{Auth: auth, BearerAuth: bearer})
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
		req.Header.Set("Authorization", "Bearer desktop-opaque-token")
		// A browser cookie cannot mask a rejected OAuth credential.
		req.AddCookie(&http.Cookie{Name: authAccessTokenCookieName, Value: "iat-unrelated"})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	assertSession := func(want string) {
		t.Helper()
		rec := request()
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var dto userauth.SessionDTO
		if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
			t.Fatal(err)
		}
		if dto.User.AgentProfileID != "H-desktop" || dto.User.Username != "desk" || dto.Subject != "kratos-desk" || !dto.ExpiresAt.Equal(expires) {
			t.Fatalf("wrong session: %+v", dto)
		}
		if strings.Join(dto.PermissionScope, ",") != want {
			t.Fatalf("permissions=%v want=%s", dto.PermissionScope, want)
		}
		if dto.AccessToken != "" || dto.SessionID != "" || len(rec.Result().Cookies()) != 0 || strings.Contains(rec.Body.String(), "desktop-opaque-token") || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("session read leaked or minted credentials")
		}
	}
	assertSession("mission.read,operations.read")
	principal, err := bearer.AuthenticateBearer(ctx, "desktop-opaque-token")
	if err != nil || principal.CredentialScopeRestricted || strings.Join(principal.CoarseScopes, ",") != "mission.read,operations.read" {
		t.Fatalf("desktop business principal=%+v err=%v", principal, err)
	}
	account.PermissionScope = []string{"mission.read"}
	if err := accounts.SaveAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	assertSession("mission.read")
	claims["scope"] = "openid profile orrery:mission.read"
	assertSession("mission.read")
	principal, err = bearer.AuthenticateBearer(ctx, "desktop-opaque-token")
	if err != nil || !principal.CredentialScopeRestricted {
		t.Fatalf("legacy token ceiling lost: %+v %v", principal, err)
	}

	tests := []struct {
		name    string
		mutate  func()
		restore func()
	}{
		{"other client", func() { claims["client_id"] = "orrery-cli" }, func() { claims["client_id"] = "orrery-desktop" }},
		{"forged profile client", func() { delete(claims, "client_id"); claims["ext"].(map[string]any)["client_id"] = "orrery-desktop" }, func() { claims["client_id"] = "orrery-desktop"; delete(claims["ext"].(map[string]any), "client_id") }},
		{"inactive token", func() { claims["active"] = false }, func() { claims["active"] = true }},
		{"expired token", func() { claims["exp"] = time.Now().Add(-time.Hour).Unix() }, func() { claims["exp"] = expires.Unix() }},
		{"wrong audience", func() { claims["aud"] = []string{"other-api"} }, func() { claims["aud"] = []string{"orrery-api"} }},
		{"wrong issuer", func() { claims["iss"] = "https://other.example" }, func() { claims["iss"] = "https://identity.example/hydra" }},
		{"profile mismatch", func() { claims["sub"] = "H-other" }, func() { claims["sub"] = "H-desktop" }},
		{"disabled subject", func() {
			subject.Status = identity.SubjectStatusDisabled
			if err := subjects.SaveSubject(ctx, subject); err != nil {
				t.Fatal(err)
			}
		}, func() {
			subject.Status = identity.SubjectStatusActive
			if err := subjects.SaveSubject(ctx, subject); err != nil {
				t.Fatal(err)
			}
		}},
		{"disabled account", func() {
			account.Enabled = false
			if err := accounts.SaveAccount(ctx, account); err != nil {
				t.Fatal(err)
			}
		}, func() {
			account.Enabled = true
			if err := accounts.SaveAccount(ctx, account); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.mutate()
			defer tt.restore()
			rec := request()
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}
