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

func TestOIDCTokenProviderVerifiesIntrospectionToken(t *testing.T) {
	expiresAt := time.Now().UTC().Add(time.Hour)
	var sawBasicAuth bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", req.Method)
		}
		if req.URL.Path != "/admin/oauth2/introspect" {
			t.Fatalf("path = %s, want /admin/oauth2/introspect", req.URL.Path)
		}
		if got := req.Header.Get("Content-Type"); !strings.Contains(got, "application/x-www-form-urlencoded") {
			t.Fatalf("content-type = %q", got)
		}
		clientID, clientSecret, ok := req.BasicAuth()
		sawBasicAuth = ok && clientID == "orrery-api" && clientSecret == "secret"
		if err := req.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if got := req.Form.Get("token"); got != "opaque-token" {
			t.Fatalf("token = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"active":             true,
			"sub":                "hydra-sub-1",
			"preferred_username": "Operator",
			"email":              "operator@example.com",
			"name":               "Operator One",
			"scope":              "openid profile email orrery:mission.read orrery:operations.manage orrery:offline_access",
			"aud":                []string{"orrery-api"},
			"iss":                "https://hydra.example",
			"exp":                expiresAt.Unix(),
			"agent_profile_id":   "human-operator",
			"groups":             []string{"Operations"},
		})
	}))
	defer server.Close()

	provider, err := NewOIDCTokenProvider(OIDCTokenProviderConfig{
		IntrospectionURL: server.URL + "/admin/oauth2/introspect",
		ClientID:         "orrery-api",
		ClientSecret:     "secret",
		Issuer:           "https://hydra.example",
		Audience:         "orrery-api",
	})
	if err != nil {
		t.Fatalf("NewOIDCTokenProvider: %v", err)
	}

	external, err := provider.VerifyAPIToken(context.Background(), "opaque-token")
	if err != nil {
		t.Fatalf("VerifyAPIToken: %v", err)
	}
	if !sawBasicAuth {
		t.Fatalf("introspection request did not use configured basic auth")
	}
	if external.Provider != ProviderHydra || external.Subject != "hydra-sub-1" || external.Username != "operator" {
		t.Fatalf("external identity = %+v", external)
	}
	if external.Email != "operator@example.com" || external.DisplayName != "Operator One" || external.Traits["agent_profile_id"] != "human-operator" {
		t.Fatalf("external identity names/traits = %+v", external)
	}
	if strings.Join(external.Scopes, ",") != "mission.read,operations.manage" {
		t.Fatalf("scopes = %+v", external.Scopes)
	}
	if len(external.Groups) != 1 || external.Groups[0] != "Operations" {
		t.Fatalf("groups = %+v", external.Groups)
	}
	if !external.ExpiresAt.Equal(expiresAt.Truncate(time.Second)) {
		t.Fatalf("expires_at = %s, want %s", external.ExpiresAt, expiresAt.Truncate(time.Second))
	}
}

func TestOIDCTokenProviderRejectsInactiveAndWrongAudience(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
	}{
		{
			name: "inactive",
			body: map[string]any{"active": false, "sub": "hydra-sub-1", "aud": []string{"orrery-api"}},
		},
		{
			name: "wrong audience",
			body: map[string]any{"active": true, "sub": "hydra-sub-1", "aud": []string{"other-api"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				_ = json.NewEncoder(w).Encode(tt.body)
			}))
			defer server.Close()
			provider, err := NewOIDCTokenProvider(OIDCTokenProviderConfig{
				IntrospectionURL: server.URL,
				Issuer:           "https://hydra.example",
				Audience:         "orrery-api",
			})
			if err != nil {
				t.Fatalf("NewOIDCTokenProvider: %v", err)
			}
			if _, err := provider.VerifyAPIToken(context.Background(), "opaque-token"); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("VerifyAPIToken error = %v, want ErrUnauthorized", err)
			}
		})
	}
}

func TestOIDCTokenProviderRejectsMissingIssuerOrAudienceBoundary(t *testing.T) {
	for _, cfg := range []OIDCTokenProviderConfig{
		{IntrospectionURL: "https://hydra.example/admin/oauth2/introspect", Audience: "orrery-api"},
		{IntrospectionURL: "https://hydra.example/admin/oauth2/introspect", Issuer: "https://hydra.example"},
	} {
		if _, err := NewOIDCTokenProvider(cfg); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("NewOIDCTokenProvider error = %v, want ErrInvalidRequest", err)
		}
	}
}

func TestOIDCTokenProviderRequiresHTTPURL(t *testing.T) {
	_, err := NewOIDCTokenProvider(OIDCTokenProviderConfig{IntrospectionURL: (&url.URL{Scheme: "file", Path: "/tmp/introspect"}).String()})
	if err == nil || !strings.Contains(err.Error(), "oidc introspection_url is invalid") {
		t.Fatalf("error = %v", err)
	}
}
