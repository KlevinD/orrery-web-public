package identity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestFeishuIdentityProviderCompletesLoginWithBoundStateCookie(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/token":
			if req.Method != http.MethodPost || req.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("token request = %s content-type=%q", req.Method, req.Header.Get("Content-Type"))
			}
			var body map[string]string
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["client_id"] != "cli_test" || body["client_secret"] != "secret" || body["code"] != "code-1" || body["redirect_uri"] != server.URL+"/login-callback" {
				t.Fatalf("token body = %+v", body)
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"access_token":"u-token"}}`))
		case "/user":
			if req.Header.Get("Authorization") != "Bearer u-token" {
				t.Fatalf("authorization = %q", req.Header.Get("Authorization"))
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"name":"Alice","open_id":"ou_1","union_id":"on_1","user_id":"u_1","enterprise_email":"alice@example.test","tenant_key":"tenant-1"}}`))
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	provider := newFeishuProviderForTest(t, server.URL, "tenant-1")
	now := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	provider.now = func() time.Time { return now }
	redirect, err := provider.BeginBrowserLogin(context.Background(), BeginLoginRequest{ReturnTo: "/missions/active"})
	if err != nil {
		t.Fatalf("BeginBrowserLogin: %v", err)
	}
	authorizeURL, err := url.Parse(redirect.URL)
	if err != nil {
		t.Fatal(err)
	}
	if authorizeURL.Query().Get("app_id") != "cli_test" || authorizeURL.Query().Get("redirect_uri") != server.URL+"/login-callback" || authorizeURL.Query().Get("state") != redirect.FlowState {
		t.Fatalf("authorize url = %s", redirect.URL)
	}
	external, err := provider.CompleteBrowserLogin(context.Background(), CompleteLoginRequest{
		Code:   "code-1",
		State:  redirect.FlowState,
		Cookie: BrowserFlowCookieName(ProviderFeishu) + "=" + redirect.FlowState,
	})
	if err != nil {
		t.Fatalf("CompleteBrowserLogin: %v", err)
	}
	if external.Provider != ProviderFeishu || external.Subject != "tenant-1:on_1" || external.Username != "alice@example.test" || external.DisplayName != "Alice" {
		t.Fatalf("external = %+v", external)
	}
	if got := provider.CallbackReturnTo(redirect.FlowState); got != "/missions/active" {
		t.Fatalf("return_to = %q", got)
	}
}

func TestFeishuIdentityProviderRejectsMissingFlowCookieAndWrongTenant(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/token":
			_, _ = w.Write([]byte(`{"code":0,"data":{"access_token":"u-token"}}`))
		case "/user":
			_, _ = w.Write([]byte(`{"code":0,"data":{"name":"Mallory","union_id":"on_2","tenant_key":"tenant-2"}}`))
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()
	provider := newFeishuProviderForTest(t, server.URL, "tenant-1")
	redirect, err := provider.BeginBrowserLogin(context.Background(), BeginLoginRequest{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.CompleteBrowserLogin(context.Background(), CompleteLoginRequest{Code: "code", State: redirect.FlowState})
	if err != ErrFlowCookieInvalid {
		t.Fatalf("missing cookie error = %v", err)
	}
	_, err = provider.CompleteBrowserLogin(context.Background(), CompleteLoginRequest{
		Code: "code", State: redirect.FlowState, Cookie: BrowserFlowCookieName(ProviderFeishu) + "=" + redirect.FlowState,
	})
	if err != ErrUnauthorized {
		t.Fatalf("wrong tenant error = %v", err)
	}
}

func TestFeishuIdentityProviderBindsStateToProfileAndCallbackMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/token":
			_, _ = w.Write([]byte(`{"code":0,"data":{"access_token":"u-token"}}`))
		case "/user":
			_, _ = w.Write([]byte(`{"code":0,"data":{"name":"Alice","union_id":"on_1","tenant_key":"tenant-1"}}`))
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()
	provider := newFeishuProviderForTest(t, server.URL, "tenant-1")
	redirect, err := provider.BeginBrowserBinding(context.Background(), BeginBindingRequest{AgentProfileID: "human-alice", ReturnTo: "/profile"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(redirect.URL, url.QueryEscape(server.URL+"/binding-callback")) {
		t.Fatalf("binding redirect = %s", redirect.URL)
	}
	cookie := BrowserFlowCookieName(ProviderFeishu) + "=" + redirect.FlowState
	if _, err := provider.CompleteBrowserLogin(context.Background(), CompleteLoginRequest{Code: "code", State: redirect.FlowState, Cookie: cookie}); err != ErrUnauthorized {
		t.Fatalf("binding state accepted for login: %v", err)
	}
	external, err := provider.CompleteBrowserBinding(context.Background(), CompleteLoginRequest{Code: "code", State: redirect.FlowState, Cookie: cookie})
	if err != nil {
		t.Fatal(err)
	}
	if external.Traits["binding_agent_profile_id"] != "human-alice" {
		t.Fatalf("binding traits = %+v", external.Traits)
	}
}

func TestFeishuIdentityProviderRejectsTamperedAndExpiredState(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	provider := newFeishuProviderForTest(t, server.URL, "tenant-1")
	now := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	provider.now = func() time.Time { return now }
	redirect, err := provider.BeginBrowserLogin(context.Background(), BeginLoginRequest{ReturnTo: "https://evil.example"})
	if err != nil {
		t.Fatal(err)
	}
	tampered := redirect.FlowState + "x"
	if _, err := provider.CompleteBrowserLogin(context.Background(), CompleteLoginRequest{
		Code: "code", State: tampered, Cookie: BrowserFlowCookieName(ProviderFeishu) + "=" + tampered,
	}); err != ErrUnauthorized {
		t.Fatalf("tampered state error = %v", err)
	}
	if got := provider.CallbackReturnTo(redirect.FlowState); got != "/" {
		t.Fatalf("unsafe return_to = %q", got)
	}
	provider.now = func() time.Time { return now.Add(feishuOAuthStateTTL) }
	if _, err := provider.CompleteBrowserLogin(context.Background(), CompleteLoginRequest{
		Code: "code", State: redirect.FlowState, Cookie: BrowserFlowCookieName(ProviderFeishu) + "=" + redirect.FlowState,
	}); err != ErrFlowExpired {
		t.Fatalf("expired state error = %v", err)
	}
}

func newFeishuProviderForTest(t *testing.T, baseURL, tenantKey string) *FeishuIdentityProvider {
	t.Helper()
	provider, err := NewFeishuIdentityProvider(FeishuProviderConfig{
		AppID:              "cli_test",
		AppSecret:          "secret",
		StateSecret:        "state-secret",
		CallbackURL:        baseURL + "/login-callback",
		BindingCallbackURL: baseURL + "/binding-callback",
		AllowedTenantKey:   tenantKey,
		AuthorizeURL:       baseURL + "/authorize",
		TokenURL:           baseURL + "/token",
		UserInfoURL:        baseURL + "/user",
		HTTPClient:         &http.Client{Timeout: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}
