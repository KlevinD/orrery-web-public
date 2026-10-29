package identity

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOIDCLoginConsentServiceAcceptsHydraLoginWithPrincipalSubject(t *testing.T) {
	var accepted HydraAcceptLoginRequest
	var sawAdminToken bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		sawAdminToken = req.Header.Get("Authorization") == "Bearer admin-secret"
		switch req.URL.Path {
		case "/admin/oauth2/auth/requests/login":
			if req.URL.Query().Get("login_challenge") != "login-1" {
				t.Fatalf("login_challenge = %q", req.URL.Query().Get("login_challenge"))
			}
			_ = json.NewEncoder(w).Encode(HydraLoginRequest{
				Challenge:  "login-1",
				Client:     HydraClientRef{ClientID: "orrery-cli"},
				RequestURL: "https://identity.example/oauth2/auth?client_id=orrery-cli&code_challenge=test-challenge&code_challenge_method=S256",
			})
		case "/admin/oauth2/auth/requests/login/accept":
			if req.URL.Query().Get("login_challenge") != "login-1" {
				t.Fatalf("accept login_challenge = %q", req.URL.Query().Get("login_challenge"))
			}
			if err := json.NewDecoder(req.Body).Decode(&accepted); err != nil {
				t.Fatalf("decode accept login: %v", err)
			}
			_ = json.NewEncoder(w).Encode(OAuth2Redirect{RedirectTo: "https://hydra.example/oauth2/auth?accepted=1"})
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
	}))
	defer server.Close()
	hydra, err := NewHydraAdminClient(HydraAdminConfig{AdminURL: server.URL, AdminToken: "admin-secret", RememberFor: time.Hour})
	if err != nil {
		t.Fatalf("NewHydraAdminClient: %v", err)
	}
	service := NewOIDCLoginConsentService(hydra, NewDefaultOIDCClientRegistry())

	redirect, err := service.AcceptLogin(context.Background(), "login-1", testHydraPrincipal())
	if err != nil {
		t.Fatalf("AcceptLogin: %v", err)
	}
	if !sawAdminToken {
		t.Fatalf("admin token was not sent")
	}
	if redirect.RedirectTo != "https://hydra.example/oauth2/auth?accepted=1" {
		t.Fatalf("redirect = %+v", redirect)
	}
	if accepted.Subject != "human-operator" || !accepted.Remember || accepted.RememberFor != 3600 {
		t.Fatalf("accepted login = %+v", accepted)
	}
	if accepted.Context["agent_profile_id"] != "human-operator" || accepted.Context["username"] != "operator" {
		t.Fatalf("accepted context = %+v", accepted.Context)
	}
}

func TestOIDCLoginConsentServiceRejectsSkippedLoginSubjectMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/admin/oauth2/auth/requests/login/accept" {
			t.Fatalf("mismatched skip login should not be accepted")
		}
		_ = json.NewEncoder(w).Encode(HydraLoginRequest{
			Challenge:  "login-1",
			Subject:    "another-human",
			Skip:       true,
			Client:     HydraClientRef{ClientID: "orrery-cli"},
			RequestURL: "https://identity.example/oauth2/auth?client_id=orrery-cli&code_challenge=test-challenge&code_challenge_method=S256",
		})
	}))
	defer server.Close()
	hydra, err := NewHydraAdminClient(HydraAdminConfig{AdminURL: server.URL})
	if err != nil {
		t.Fatalf("NewHydraAdminClient: %v", err)
	}
	service := NewOIDCLoginConsentService(hydra, NewDefaultOIDCClientRegistry())

	if _, err := service.AcceptLogin(context.Background(), "login-1", testHydraPrincipal()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("AcceptLogin error = %v, want ErrUnauthorized", err)
	}
}

func TestOIDCLoginConsentServiceAcceptsConsentWithScopedGrant(t *testing.T) {
	var accepted HydraAcceptConsentRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/admin/oauth2/auth/requests/consent":
			_ = json.NewEncoder(w).Encode(HydraConsentRequest{
				Challenge:                    "consent-1",
				Subject:                      "human-operator",
				Client:                       HydraClientRef{ClientID: "orrery-cli"},
				RequestedScope:               []string{"openid", "profile", "orrery:operations.manage", "orrery:resource.manage"},
				RequestedAccessTokenAudience: []string{"orrery-api"},
			})
		case "/admin/oauth2/auth/requests/consent/accept":
			if req.URL.Query().Get("consent_challenge") != "consent-1" {
				t.Fatalf("consent_challenge = %q", req.URL.Query().Get("consent_challenge"))
			}
			if err := json.NewDecoder(req.Body).Decode(&accepted); err != nil {
				t.Fatalf("decode accept consent: %v", err)
			}
			_ = json.NewEncoder(w).Encode(OAuth2Redirect{RedirectTo: "https://hydra.example/oauth2/auth?consent=1"})
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
	}))
	defer server.Close()
	hydra, err := NewHydraAdminClient(HydraAdminConfig{AdminURL: server.URL, RememberFor: time.Hour})
	if err != nil {
		t.Fatalf("NewHydraAdminClient: %v", err)
	}
	service := NewOIDCLoginConsentService(hydra, NewDefaultOIDCClientRegistry())

	redirect, err := service.AcceptConsent(context.Background(), "consent-1", testHydraPrincipal())
	if err != nil {
		t.Fatalf("AcceptConsent: %v", err)
	}
	if redirect.RedirectTo == "" {
		t.Fatalf("redirect = %+v", redirect)
	}
	if strings.Join(accepted.GrantScope, ",") != "openid,profile,orrery:operations.manage" {
		t.Fatalf("grant_scope = %+v", accepted.GrantScope)
	}
	if strings.Join(accepted.GrantAccessTokenAudience, ",") != "orrery-api" {
		t.Fatalf("audience = %+v", accepted.GrantAccessTokenAudience)
	}
	if accepted.Session.AccessToken["agent_profile_id"] != "human-operator" || accepted.Session.AccessToken["username"] != "operator" {
		t.Fatalf("access token session = %+v", accepted.Session.AccessToken)
	}
	if accepted.Session.IDToken["preferred_username"] != "operator" || accepted.Session.IDToken["name"] != "Operator" {
		t.Fatalf("id token session = %+v", accepted.Session.IDToken)
	}
}

func TestOIDCLoginConsentServiceRequiresExplicitConsentForExternalClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/admin/oauth2/auth/requests/consent/accept" {
			t.Fatalf("external client should not be auto-consented")
		}
		_ = json.NewEncoder(w).Encode(HydraConsentRequest{
			Challenge:      "consent-1",
			Subject:        "human-operator",
			Client:         HydraClientRef{ClientID: "orrery-external-integration"},
			RequestedScope: []string{"openid", "orrery:operations.manage"},
		})
	}))
	defer server.Close()
	hydra, err := NewHydraAdminClient(HydraAdminConfig{AdminURL: server.URL})
	if err != nil {
		t.Fatalf("NewHydraAdminClient: %v", err)
	}
	service := NewOIDCLoginConsentService(hydra, NewDefaultOIDCClientRegistry())

	if _, err := service.AcceptConsent(context.Background(), "consent-1", testHydraPrincipal()); !errors.Is(err, ErrConsentRequired) {
		t.Fatalf("AcceptConsent error = %v, want ErrConsentRequired", err)
	}
}

func TestOIDCLoginConsentServiceRejectsConsentSubjectMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/admin/oauth2/auth/requests/consent/accept" {
			t.Fatalf("mismatched subject should not be accepted")
		}
		_ = json.NewEncoder(w).Encode(HydraConsentRequest{
			Challenge:      "consent-1",
			Subject:        "another-human",
			Client:         HydraClientRef{ClientID: "orrery-cli"},
			RequestedScope: []string{"openid", "orrery:operations.manage"},
		})
	}))
	defer server.Close()
	hydra, err := NewHydraAdminClient(HydraAdminConfig{AdminURL: server.URL})
	if err != nil {
		t.Fatalf("NewHydraAdminClient: %v", err)
	}
	service := NewOIDCLoginConsentService(hydra, NewDefaultOIDCClientRegistry())

	if _, err := service.AcceptConsent(context.Background(), "consent-1", testHydraPrincipal()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("AcceptConsent error = %v, want ErrUnauthorized", err)
	}
	if _, err := service.PreviewConsent(context.Background(), "consent-1", testHydraPrincipal()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("PreviewConsent error = %v, want ErrUnauthorized", err)
	}
}

func TestOIDCLoginConsentServiceRejectsConsentWithoutSubject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/admin/oauth2/auth/requests/consent/accept" {
			t.Fatalf("empty subject should not be accepted")
		}
		_ = json.NewEncoder(w).Encode(HydraConsentRequest{
			Challenge:      "consent-1",
			Client:         HydraClientRef{ClientID: "orrery-cli"},
			RequestedScope: []string{"openid", "orrery:operations.manage"},
		})
	}))
	defer server.Close()
	hydra, err := NewHydraAdminClient(HydraAdminConfig{AdminURL: server.URL})
	if err != nil {
		t.Fatalf("NewHydraAdminClient: %v", err)
	}
	service := NewOIDCLoginConsentService(hydra, NewDefaultOIDCClientRegistry())

	if _, err := service.AcceptConsent(context.Background(), "consent-1", testHydraPrincipal()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("AcceptConsent error = %v, want ErrUnauthorized", err)
	}
}

func TestOIDCLoginConsentServicePreviewsExternalConsentWithoutAccepting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/admin/oauth2/auth/requests/consent/accept" {
			t.Fatalf("preview must not accept consent")
		}
		_ = json.NewEncoder(w).Encode(HydraConsentRequest{
			Challenge:                    "consent-1",
			Subject:                      "human-operator",
			Client:                       HydraClientRef{ClientID: "orrery-external-integration"},
			RequestedScope:               []string{"openid", "profile", "orrery:operations.manage", "orrery:resource.manage"},
			RequestedAccessTokenAudience: []string{"orrery-api"},
		})
	}))
	defer server.Close()
	hydra, err := NewHydraAdminClient(HydraAdminConfig{AdminURL: server.URL})
	if err != nil {
		t.Fatalf("NewHydraAdminClient: %v", err)
	}
	service := NewOIDCLoginConsentService(hydra, NewDefaultOIDCClientRegistry())

	preview, err := service.PreviewConsent(context.Background(), "consent-1", testHydraPrincipal())
	if err != nil {
		t.Fatalf("PreviewConsent: %v", err)
	}
	if preview.Client.ClientID != "orrery-external-integration" || preview.Subject != "human-operator" {
		t.Fatalf("preview identity = %+v", preview)
	}
	if strings.Join(preview.GrantScopes, ",") != "openid,profile,orrery:operations.manage" {
		t.Fatalf("grant scopes = %+v", preview.GrantScopes)
	}
}

func TestOIDCLoginConsentServiceApprovesExternalConsent(t *testing.T) {
	var accepted HydraAcceptConsentRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/admin/oauth2/auth/requests/consent":
			_ = json.NewEncoder(w).Encode(HydraConsentRequest{
				Challenge:      "consent-1",
				Subject:        "human-operator",
				Client:         HydraClientRef{ClientID: "orrery-external-integration"},
				RequestedScope: []string{"openid", "orrery:operations.manage"},
			})
		case "/admin/oauth2/auth/requests/consent/accept":
			if err := json.NewDecoder(req.Body).Decode(&accepted); err != nil {
				t.Fatalf("decode accept consent: %v", err)
			}
			_ = json.NewEncoder(w).Encode(OAuth2Redirect{RedirectTo: "https://hydra.example/oauth2/auth?consent=1"})
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
	}))
	defer server.Close()
	hydra, err := NewHydraAdminClient(HydraAdminConfig{AdminURL: server.URL})
	if err != nil {
		t.Fatalf("NewHydraAdminClient: %v", err)
	}
	service := NewOIDCLoginConsentService(hydra, NewDefaultOIDCClientRegistry())

	redirect, err := service.ApproveConsent(context.Background(), "consent-1", testHydraPrincipal())
	if err != nil {
		t.Fatalf("ApproveConsent: %v", err)
	}
	if redirect.RedirectTo == "" {
		t.Fatalf("redirect = %+v", redirect)
	}
	if strings.Join(accepted.GrantScope, ",") != "openid,orrery:operations.manage" {
		t.Fatalf("grant_scope = %+v", accepted.GrantScope)
	}
}

func TestOIDCLoginConsentServiceRejectsExternalConsent(t *testing.T) {
	var rejected HydraRejectConsentRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/admin/oauth2/auth/requests/consent":
			_ = json.NewEncoder(w).Encode(HydraConsentRequest{
				Challenge: "consent-1",
				Subject:   "human-operator",
				Client:    HydraClientRef{ClientID: "orrery-external-integration"},
			})
		case "/admin/oauth2/auth/requests/consent/reject":
			if req.URL.Query().Get("consent_challenge") != "consent-1" {
				t.Fatalf("consent_challenge = %q", req.URL.Query().Get("consent_challenge"))
			}
			if err := json.NewDecoder(req.Body).Decode(&rejected); err != nil {
				t.Fatalf("decode reject consent: %v", err)
			}
			_ = json.NewEncoder(w).Encode(OAuth2Redirect{RedirectTo: "https://hydra.example/oauth2/auth?error=access_denied"})
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
	}))
	defer server.Close()
	hydra, err := NewHydraAdminClient(HydraAdminConfig{AdminURL: server.URL})
	if err != nil {
		t.Fatalf("NewHydraAdminClient: %v", err)
	}
	service := NewOIDCLoginConsentService(hydra, NewDefaultOIDCClientRegistry())

	redirect, err := service.RejectConsent(context.Background(), "consent-1", testHydraPrincipal())
	if err != nil {
		t.Fatalf("RejectConsent: %v", err)
	}
	if redirect.RedirectTo == "" {
		t.Fatalf("redirect = %+v", redirect)
	}
	if rejected.Error != "access_denied" || rejected.ErrorDescription == "" {
		t.Fatalf("reject body = %+v", rejected)
	}
}

func TestOIDCLoginConsentServiceRejectConsentRequiresMatchingSubject(t *testing.T) {
	rejectCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/admin/oauth2/auth/requests/consent":
			_ = json.NewEncoder(w).Encode(HydraConsentRequest{
				Challenge: "consent-1",
				Subject:   "human-someone-else",
				Client:    HydraClientRef{ClientID: "orrery-external-integration"},
			})
		case "/admin/oauth2/auth/requests/consent/reject":
			rejectCalled = true
			_ = json.NewEncoder(w).Encode(OAuth2Redirect{RedirectTo: "https://hydra.example/oauth2/auth?error=access_denied"})
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
	}))
	defer server.Close()
	hydra, err := NewHydraAdminClient(HydraAdminConfig{AdminURL: server.URL})
	if err != nil {
		t.Fatalf("NewHydraAdminClient: %v", err)
	}
	service := NewOIDCLoginConsentService(hydra, NewDefaultOIDCClientRegistry())

	if _, err := service.RejectConsent(context.Background(), "consent-1", testHydraPrincipal()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("RejectConsent error = %v, want ErrUnauthorized", err)
	}
	if rejectCalled {
		t.Fatal("mismatched principal rejected another subject's consent")
	}
}

func TestHydraAdminClientRevokesLoginAndConsentSessionsForSubject(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodDelete {
			t.Fatalf("method = %s, want DELETE", req.Method)
		}
		if req.Header.Get("Authorization") != "Bearer admin-secret" {
			t.Fatalf("authorization = %q", req.Header.Get("Authorization"))
		}
		requests = append(requests, req.URL.RequestURI())
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	hydra, err := NewHydraAdminClient(HydraAdminConfig{AdminURL: server.URL, AdminToken: "admin-secret"})
	if err != nil {
		t.Fatalf("NewHydraAdminClient: %v", err)
	}

	if err := hydra.RevokeSubjectSessions(context.Background(), "human-ding"); err != nil {
		t.Fatalf("RevokeSubjectSessions: %v", err)
	}
	want := []string{
		"/admin/oauth2/auth/sessions/login?subject=human-ding",
		"/admin/oauth2/auth/sessions/consent?all=true&subject=human-ding",
	}
	if strings.Join(requests, " ") != strings.Join(want, " ") {
		t.Fatalf("requests = %+v, want %+v", requests, want)
	}
}

func TestHydraAdminClientRejectsBlankRevocationSubject(t *testing.T) {
	hydra, err := NewHydraAdminClient(HydraAdminConfig{AdminURL: "https://hydra.example"})
	if err != nil {
		t.Fatalf("NewHydraAdminClient: %v", err)
	}
	if err := hydra.RevokeSubjectSessions(context.Background(), " "); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("RevokeSubjectSessions error = %v, want ErrInvalidRequest", err)
	}
}

func TestHydraAdminClientUpsertCreatesOAuth2Client(t *testing.T) {
	var created HydraOAuth2Client
	var sawGet bool
	var sawPost bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/admin/clients/partner-app":
			sawGet = true
			http.NotFound(w, req)
		case req.Method == http.MethodPost && req.URL.Path == "/admin/clients":
			sawPost = true
			if req.Header.Get("Authorization") != "Bearer admin-secret" {
				t.Fatalf("authorization = %q", req.Header.Get("Authorization"))
			}
			if err := json.NewDecoder(req.Body).Decode(&created); err != nil {
				t.Fatalf("decode created client: %v", err)
			}
			_ = json.NewEncoder(w).Encode(HydraOAuth2Client{
				ClientID:                created.ClientID,
				ClientName:              created.ClientName,
				ClientSecret:            "generated-secret",
				RedirectURIs:            created.RedirectURIs,
				GrantTypes:              created.GrantTypes,
				ResponseTypes:           created.ResponseTypes,
				Scope:                   created.Scope,
				Audience:                created.Audience,
				TokenEndpointAuthMethod: created.TokenEndpointAuthMethod,
			})
		default:
			t.Fatalf("unexpected %s %s", req.Method, req.URL.Path)
		}
	}))
	defer server.Close()
	hydra, err := NewHydraAdminClient(HydraAdminConfig{AdminURL: server.URL, AdminToken: "admin-secret"})
	if err != nil {
		t.Fatalf("NewHydraAdminClient: %v", err)
	}

	client, err := hydra.UpsertClient(context.Background(), OIDCClient{
		ClientID:      "partner-app",
		DisplayName:   "Partner App",
		Kind:          "external_integration",
		AllowedScopes: []string{"openid", "orrery:operations.read"},
		Audience:      []string{"orrery-api"},
		RedirectURIs:  []string{"https://partner.example/callback"},
	})
	if err != nil {
		t.Fatalf("UpsertClient: %v", err)
	}
	if !sawGet || !sawPost {
		t.Fatalf("sawGet=%v sawPost=%v", sawGet, sawPost)
	}
	if created.ClientID != "partner-app" || created.ClientName != "Partner App" {
		t.Fatalf("created identity = %+v", created)
	}
	if strings.Join(created.RedirectURIs, ",") != "https://partner.example/callback" {
		t.Fatalf("redirect_uris = %+v", created.RedirectURIs)
	}
	if strings.Join(created.GrantTypes, ",") != "authorization_code,refresh_token" || strings.Join(created.ResponseTypes, ",") != "code" {
		t.Fatalf("grant/response types = %+v %+v", created.GrantTypes, created.ResponseTypes)
	}
	if created.Scope != "openid orrery:operations.read" || strings.Join(created.Audience, ",") != "orrery-api" {
		t.Fatalf("scope/audience = %q %+v", created.Scope, created.Audience)
	}
	if created.TokenEndpointAuthMethod != "client_secret_basic" {
		t.Fatalf("token_endpoint_auth_method = %q", created.TokenEndpointAuthMethod)
	}
	if client.ClientSecret != "generated-secret" {
		t.Fatalf("client secret = %q", client.ClientSecret)
	}
}

func TestHydraAdminClientUpsertUpdatesExistingOAuth2Client(t *testing.T) {
	var sawPost bool
	var updated HydraOAuth2Client
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/admin/clients/partner-app":
			_ = json.NewEncoder(w).Encode(HydraOAuth2Client{ClientID: "partner-app"})
		case req.Method == http.MethodPut && req.URL.Path == "/admin/clients/partner-app":
			if err := json.NewDecoder(req.Body).Decode(&updated); err != nil {
				t.Fatalf("decode updated client: %v", err)
			}
			_ = json.NewEncoder(w).Encode(HydraOAuth2Client{
				ClientID:                updated.ClientID,
				ClientName:              updated.ClientName,
				RedirectURIs:            updated.RedirectURIs,
				GrantTypes:              updated.GrantTypes,
				ResponseTypes:           updated.ResponseTypes,
				Scope:                   updated.Scope,
				TokenEndpointAuthMethod: updated.TokenEndpointAuthMethod,
			})
		case req.Method == http.MethodPost && req.URL.Path == "/admin/clients":
			sawPost = true
		default:
			t.Fatalf("unexpected %s %s", req.Method, req.URL.Path)
		}
	}))
	defer server.Close()
	hydra, err := NewHydraAdminClient(HydraAdminConfig{AdminURL: server.URL})
	if err != nil {
		t.Fatalf("NewHydraAdminClient: %v", err)
	}

	client, err := hydra.UpsertClient(context.Background(), OIDCClient{
		ClientID:                "partner-app",
		DisplayName:             "Partner App",
		Kind:                    "external_integration",
		AllowedScopes:           []string{"openid", "orrery:operations.manage"},
		RedirectURIs:            []string{"https://partner.example/callback"},
		TokenEndpointAuthMethod: "none",
	})
	if err != nil {
		t.Fatalf("UpsertClient: %v", err)
	}
	if sawPost {
		t.Fatalf("existing client should be updated, not created")
	}
	if updated.TokenEndpointAuthMethod != "none" || updated.Scope != "openid orrery:operations.manage" {
		t.Fatalf("updated client = %+v", updated)
	}
	if client.TokenEndpointAuthMethod != "none" {
		t.Fatalf("returned client = %+v", client)
	}
}

func TestHydraAdminClientUpsertRequiresRedirectURIForAuthorizationCode(t *testing.T) {
	hydra, err := NewHydraAdminClient(HydraAdminConfig{AdminURL: "https://hydra.example"})
	if err != nil {
		t.Fatalf("NewHydraAdminClient: %v", err)
	}
	_, err = hydra.UpsertClient(context.Background(), OIDCClient{
		ClientID:      "partner-app",
		AllowedScopes: []string{"openid"},
	})
	if !errors.Is(err, ErrInvalidRequest) || !strings.Contains(err.Error(), "redirect_uris") {
		t.Fatalf("error = %v", err)
	}
}

func testHydraPrincipal() Principal {
	return NormalizePrincipal(Principal{
		SubjectProvider: ProviderKratos,
		Subject:         "kratos-id-1",
		UserID:          "human-operator",
		Username:        "operator",
		DisplayName:     "Operator",
		AgentProfileID:  "human-operator",
		ViewerKind:      ViewerKindUser,
		CoarseScopes:    []string{"operations.manage", "mission.read"},
	})
}
