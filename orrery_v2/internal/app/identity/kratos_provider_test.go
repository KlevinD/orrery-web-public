package identity

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestKratosProviderBeginsBrowserLoginWithCallbackReturnTo(t *testing.T) {
	provider, err := NewKratosIdentityProvider(KratosProviderConfig{
		PublicURL:   "https://kratos.example",
		CallbackURL: "https://app.example/api/auth/identity/kratos/callback",
	})
	if err != nil {
		t.Fatalf("NewKratosIdentityProvider: %v", err)
	}

	redirect, err := provider.BeginBrowserLogin(context.Background(), BeginLoginRequest{ReturnTo: "/missions?tab=inbox"})
	if err != nil {
		t.Fatalf("BeginBrowserLogin: %v", err)
	}

	parsed, err := url.Parse(redirect.URL)
	if err != nil {
		t.Fatalf("parse redirect url: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "kratos.example" || parsed.Path != "/self-service/login/browser" {
		t.Fatalf("redirect url = %s", redirect.URL)
	}
	callback, err := url.Parse(parsed.Query().Get("return_to"))
	if err != nil {
		t.Fatalf("parse callback return_to: %v", err)
	}
	if callback.Scheme != "https" || callback.Host != "app.example" || callback.Path != "/api/auth/identity/kratos/callback" {
		t.Fatalf("callback = %s", callback.String())
	}
	if got := callback.Query().Get("return_to"); got != "/missions?tab=inbox" {
		t.Fatalf("callback return_to = %q", got)
	}
}

func TestKratosProviderBeginsCurrentBrowserLogout(t *testing.T) {
	provider, err := NewKratosIdentityProvider(KratosProviderConfig{
		PublicURL:   "https://kratos.example/kratos",
		CallbackURL: "https://app.example/api/auth/identity/kratos/callback",
	})
	if err != nil {
		t.Fatalf("NewKratosIdentityProvider: %v", err)
	}

	for _, test := range []struct {
		name     string
		returnTo string
	}{
		{name: "relative return", returnTo: "/login"},
		{name: "external return", returnTo: "https://evil.example"},
	} {
		t.Run(test.name, func(t *testing.T) {
			redirect, err := provider.BeginBrowserLogout(context.Background(), BeginLogoutRequest{ReturnTo: test.returnTo})
			if err != nil {
				t.Fatalf("BeginBrowserLogout: %v", err)
			}

			parsed, err := url.Parse(redirect.URL)
			if err != nil {
				t.Fatalf("parse redirect url: %v", err)
			}
			if parsed.Scheme != "https" || parsed.Host != "app.example" || parsed.Path != "/login" {
				t.Fatalf("redirect url = %s", redirect.URL)
			}
		})
	}
}

func TestKratosProviderResolvesBrowserLogoutFlow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/self-service/logout/browser" {
			t.Fatalf("path = %s, want /self-service/logout/browser", req.URL.Path)
		}
		if got := req.Header.Get("Cookie"); got != "ory_kratos_session=abc" {
			t.Fatalf("cookie header = %q", got)
		}
		if got := req.URL.Query().Get("return_to"); got != "https://app.example/login" {
			t.Fatalf("return_to = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"logout_url": serverURL(req) + "/self-service/logout?token=logout-token",
		})
	}))
	defer server.Close()
	provider, err := NewKratosIdentityProvider(KratosProviderConfig{
		PublicURL:   server.URL,
		CallbackURL: "https://app.example/api/auth/identity/kratos/callback",
	})
	if err != nil {
		t.Fatalf("NewKratosIdentityProvider: %v", err)
	}

	redirect, err := provider.BeginBrowserLogout(context.Background(), BeginLogoutRequest{
		ReturnTo: "/login",
		Cookie:   "orrery_access_token=local; ory_kratos_session=abc",
	})
	if err != nil {
		t.Fatalf("BeginBrowserLogout: %v", err)
	}
	if !strings.Contains(redirect.URL, "/self-service/logout?token=logout-token") {
		t.Fatalf("redirect url = %q, want resolved logout url", redirect.URL)
	}
}

func serverURL(req *http.Request) string {
	return "http://" + req.Host
}

func TestKratosProviderCompletesBrowserLoginFromWhoamiSession(t *testing.T) {
	expiresAt := time.Date(2026, 7, 2, 12, 30, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/sessions/whoami" {
			t.Fatalf("path = %s, want /sessions/whoami", req.URL.Path)
		}
		if got := req.Header.Get("Cookie"); !strings.Contains(got, "ory_kratos_session=abc") {
			t.Fatalf("cookie header = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"active":     true,
			"expires_at": expiresAt,
			"identity": map[string]any{
				"id":    "kratos-id-1",
				"state": "active",
				"traits": map[string]any{
					"username":         "Operator",
					"email":            "operator@example.com",
					"display_name":     "Operator One",
					"agent_profile_id": "human-operator",
					"person_id":        "human-quzheng",
					"person_revision":  7,
				},
			},
		})
	}))
	defer server.Close()
	provider, err := NewKratosIdentityProvider(KratosProviderConfig{
		PublicURL:   server.URL,
		CallbackURL: "https://app.example/api/auth/identity/kratos/callback",
	})
	if err != nil {
		t.Fatalf("NewKratosIdentityProvider: %v", err)
	}

	external, err := provider.CompleteBrowserLogin(context.Background(), CompleteLoginRequest{
		Cookie: "ory_kratos_session=abc",
	})
	if err != nil {
		t.Fatalf("CompleteBrowserLogin: %v", err)
	}
	if external.Provider != ProviderKratos || external.Subject != "kratos-id-1" || external.Username != "operator" {
		t.Fatalf("external identity = %+v", external)
	}
	if external.Email != "operator@example.com" || external.DisplayName != "Operator One" {
		t.Fatalf("external identity names = %+v", external)
	}
	if external.Traits["agent_profile_id"] != "human-operator" || external.Traits["person_id"] != "human-quzheng" || external.Traits["person_revision"] != "7" || !external.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("external identity traits/expires = %+v", external)
	}
}

func TestKratosProviderRejectsMissingOrInactiveSession(t *testing.T) {
	provider, err := NewKratosIdentityProvider(KratosProviderConfig{
		PublicURL:   "https://kratos.example",
		CallbackURL: "https://app.example/api/auth/identity/kratos/callback",
	})
	if err != nil {
		t.Fatalf("NewKratosIdentityProvider: %v", err)
	}
	if _, err := provider.CompleteBrowserLogin(context.Background(), CompleteLoginRequest{}); !errors.Is(err, ErrBrowserSessionUnavailable) {
		t.Fatalf("missing cookie error = %v, want ErrBrowserSessionUnavailable", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"active": false,
			"identity": map[string]any{
				"id": "kratos-id-1",
			},
		})
	}))
	defer server.Close()
	provider, err = NewKratosIdentityProvider(KratosProviderConfig{
		PublicURL:   server.URL,
		CallbackURL: "https://app.example/api/auth/identity/kratos/callback",
	})
	if err != nil {
		t.Fatalf("NewKratosIdentityProvider inactive: %v", err)
	}
	if _, err := provider.CompleteBrowserLogin(context.Background(), CompleteLoginRequest{Cookie: "ory=abc"}); !errors.Is(err, ErrBrowserSessionUnavailable) {
		t.Fatalf("inactive session error = %v, want ErrBrowserSessionUnavailable", err)
	}
}

func TestKratosProviderClassifiesRejectedBrowserSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	provider, err := NewKratosIdentityProvider(KratosProviderConfig{
		PublicURL:   server.URL,
		CallbackURL: "https://app.example/api/auth/identity/kratos/callback",
	})
	if err != nil {
		t.Fatalf("NewKratosIdentityProvider: %v", err)
	}

	if _, err := provider.CompleteBrowserLogin(context.Background(), CompleteLoginRequest{Cookie: "ory=abc"}); !errors.Is(err, ErrBrowserSessionUnavailable) {
		t.Fatalf("rejected session error = %v, want ErrBrowserSessionUnavailable", err)
	}
}
