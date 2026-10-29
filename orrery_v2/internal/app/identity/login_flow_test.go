package identity

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

const kratosLoginFlowID = "550dd231-4312-4a22-9dcf-4c0d43c6f608"

func TestKratosProviderLoadsSanitizedLoginFlow(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/self-service/login/flows" || req.URL.Query().Get("id") != kratosLoginFlowID {
			t.Fatalf("request = %s", req.URL.String())
		}
		passwordNode := kratosInputNode("password", "password", "password", nil, true, "current-password", "Password")
		passwordNode["messages"] = []any{map[string]any{"id": 2, "text": " Password is incorrect ", "type": "error"}}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":         kratosLoginFlowID,
			"expires_at": "2026-07-17T13:36:09Z",
			"ui": map[string]any{
				"action": server.URL + "/self-service/login?flow=" + kratosLoginFlowID,
				"method": "POST",
				"nodes": []any{
					kratosInputNode("default", "csrf_token", "hidden", "csrf-value", true, "", ""),
					kratosInputNode("default", "identifier", "text", "", true, "username", "Username"),
					passwordNode,
					kratosInputNode("password", "method", "submit", "password", false, "", "Sign in"),
					map[string]any{"type": "script", "group": "default", "attributes": map[string]any{"src": "https://evil.example/x.js"}},
				},
				"messages": []any{map[string]any{"id": 1, "text": " The provided credentials are invalid ", "type": "error"}},
			},
		})
	}))
	defer server.Close()

	provider := mustKratosFlowProvider(t, server.URL)
	flow, err := provider.GetBrowserLoginFlow(context.Background(), LoginFlowRequest{ID: kratosLoginFlowID})
	if err != nil {
		t.Fatalf("GetBrowserLoginFlow: %v", err)
	}
	action, err := url.Parse(flow.Action)
	if err != nil {
		t.Fatalf("parse action: %v", err)
	}
	serverURL, _ := url.Parse(server.URL)
	if action.Scheme != serverURL.Scheme || action.Host != serverURL.Host || action.Path != "/self-service/login" {
		t.Fatalf("action = %q", flow.Action)
	}
	if flow.ID != kratosLoginFlowID || flow.Method != http.MethodPost || flow.ExpiresAt != "2026-07-17T13:36:09Z" {
		t.Fatalf("flow = %+v", flow)
	}
	if len(flow.Nodes) != 4 {
		t.Fatalf("nodes = %+v", flow.Nodes)
	}
	if got := flow.Nodes[0].Attributes.Value; got != "csrf-value" {
		t.Fatalf("csrf value = %#v", got)
	}
	if flow.Nodes[1].Label.Text != "Username" || flow.Nodes[1].Attributes.AutoComplete != "username" {
		t.Fatalf("identifier node = %+v", flow.Nodes[1])
	}
	if len(flow.Messages) != 1 || flow.Messages[0].Text != "登录失败。" || flow.Messages[0].Type != "error" {
		t.Fatalf("messages = %+v", flow.Messages)
	}
	if len(flow.Nodes[2].Messages) != 1 || flow.Nodes[2].Messages[0].Text != "登录失败。" {
		t.Fatalf("password node messages = %+v", flow.Nodes[2].Messages)
	}
}

func TestKratosProviderForwardsOnlyKratosLoginFlowCookies(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if got := req.Header.Get("Cookie"); got != "csrf_token_flow=csrf-cookie; ory_kratos_session=session-cookie" {
			t.Fatalf("Cookie = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": kratosLoginFlowID,
			"ui": map[string]any{
				"action": server.URL + "/self-service/login?flow=" + kratosLoginFlowID,
				"method": "POST",
				"nodes": []any{
					kratosInputNode("password", "password", "password", nil, true, "current-password", "Password"),
					kratosInputNode("password", "method", "submit", "password", false, "", "Sign in"),
				},
			},
		})
	}))
	defer server.Close()

	provider := mustKratosFlowProvider(t, server.URL)
	_, err := provider.GetBrowserLoginFlow(context.Background(), LoginFlowRequest{
		ID:     kratosLoginFlowID,
		Cookie: "csrf_token_flow=csrf-cookie; orrery_session=app-cookie; ory_kratos_session=session-cookie; unrelated=secret",
	})
	if err != nil {
		t.Fatalf("GetBrowserLoginFlow: %v", err)
	}
}

func TestKratosProviderRejectsInvalidLoginFlowIDBeforeRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()

	provider := mustKratosFlowProvider(t, server.URL)
	_, err := provider.GetBrowserLoginFlow(context.Background(), LoginFlowRequest{ID: "../../sessions"})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("error = %v, want ErrInvalidRequest", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

func TestKratosProviderRejectsForeignLoginFlowAction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": kratosLoginFlowID,
			"ui": map[string]any{
				"action": "https://evil.example/self-service/login?flow=" + kratosLoginFlowID,
				"method": "POST",
				"nodes":  []any{kratosInputNode("password", "password", "password", nil, true, "current-password", "Password")},
			},
		})
	}))
	defer server.Close()

	provider := mustKratosFlowProvider(t, server.URL)
	_, err := provider.GetBrowserLoginFlow(context.Background(), LoginFlowRequest{ID: kratosLoginFlowID})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("error = %v, want ErrInvalidRequest", err)
	}
}

func TestKratosProviderMapsLoginFlowFailures(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		want       error
	}{
		{name: "cookie invalid", statusCode: http.StatusForbidden, want: ErrFlowCookieInvalid},
		{name: "missing", statusCode: http.StatusNotFound, want: ErrFlowNotFound},
		{name: "expired", statusCode: http.StatusGone, want: ErrFlowExpired},
		{name: "invalid", statusCode: http.StatusBadRequest, want: ErrInvalidRequest},
		{name: "unavailable", statusCode: http.StatusBadGateway, want: ErrServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()
			provider := mustKratosFlowProvider(t, server.URL)
			_, err := provider.GetBrowserLoginFlow(context.Background(), LoginFlowRequest{ID: kratosLoginFlowID})
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestKratosProviderRejectsIncompleteLoginFlows(t *testing.T) {
	tests := []struct {
		name  string
		nodes []any
	}{
		{
			name: "hidden and submit only",
			nodes: []any{
				kratosInputNode("default", "csrf_token", "hidden", "csrf-value", true, "", ""),
				kratosInputNode("password", "method", "submit", "password", false, "", "Sign in"),
			},
		},
		{
			name:  "credential without submit",
			nodes: []any{kratosInputNode("default", "identifier", "text", "", true, "username", "Username")},
		},
		{
			name: "disabled credential",
			nodes: []any{
				kratosInputNodeWithDisabled("default", "identifier", "text", "", true, true, "username", "Username"),
				kratosInputNode("password", "method", "submit", "password", false, "", "Sign in"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id": kratosLoginFlowID,
					"ui": map[string]any{
						"action": server.URL + "/self-service/login?flow=" + kratosLoginFlowID,
						"method": "POST",
						"nodes":  tt.nodes,
					},
				})
			}))
			defer server.Close()

			provider := mustKratosFlowProvider(t, server.URL)
			_, err := provider.GetBrowserLoginFlow(context.Background(), LoginFlowRequest{ID: kratosLoginFlowID})
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestKratosProviderRejectsOversizedLoginFlow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.WriteString(w, `{"padding":"`+strings.Repeat("x", int(MaxBrowserLoginFlowResponseBytes))+`"}`)
	}))
	defer server.Close()

	provider := mustKratosFlowProvider(t, server.URL)
	_, err := provider.GetBrowserLoginFlow(context.Background(), LoginFlowRequest{ID: kratosLoginFlowID})
	if !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("error = %v, want ErrServiceUnavailable", err)
	}
}

func kratosInputNode(group, name, inputType string, value any, required bool, autocomplete, label string) map[string]any {
	return kratosInputNodeWithDisabled(group, name, inputType, value, required, false, autocomplete, label)
}

func kratosInputNodeWithDisabled(group, name, inputType string, value any, required, disabled bool, autocomplete, label string) map[string]any {
	return map[string]any{
		"type":  "input",
		"group": group,
		"attributes": map[string]any{
			"name":         name,
			"type":         inputType,
			"value":        value,
			"required":     required,
			"disabled":     disabled,
			"autocomplete": autocomplete,
		},
		"messages": []any{},
		"meta":     map[string]any{"label": map[string]any{"text": label}},
	}
}

func mustKratosFlowProvider(t *testing.T, publicURL string) *KratosIdentityProvider {
	t.Helper()
	provider, err := NewKratosIdentityProvider(KratosProviderConfig{
		PublicURL:   publicURL,
		CallbackURL: "http://127.0.0.1:5178/api/auth/identity/kratos/callback",
	})
	if err != nil {
		t.Fatalf("NewKratosIdentityProvider: %v", err)
	}
	return provider
}
