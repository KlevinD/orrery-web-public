package httptransport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orrery_v2/internal/app/identity"
)

func TestHTTPOIDCClientAdminRoutesListAndSaveWithAudit(t *testing.T) {
	registry := identity.NewDefaultOIDCClientRegistry()
	audit := &fakeIdentityAuditRecorder{}
	handler := newTestRouter(RouterOptions{
		OIDCClientAdmin: identity.NewOIDCClientAdminService(registry),
		AuditRecorder:   audit,
	})

	save := authedRequest(http.MethodPost, "/api/oauth2/clients", `{"client_id":"partner-app","display_name":"Partner App","kind":"external_integration","allowed_scopes":["openid","orrery:operations.read"],"audience":["orrery-api"],"redirect_uris":["https://partner.example/callback"],"grant_types":["authorization_code"],"response_types":["code"],"token_endpoint_auth_method":"none","client_secret":"do-not-return"}`)
	save.Header.Set("X-Permission-Scope", "permissions.manage")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, save)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("save status = %d body = %s", rec.Code, rec.Body.String())
	}
	var saved oidcClientDTO
	if err := json.NewDecoder(rec.Body).Decode(&saved); err != nil {
		t.Fatalf("decode saved client: %v", err)
	}
	if saved.ClientID != "partner-app" || strings.Join(saved.AllowedScopes, ",") != "openid,orrery:operations.read" {
		t.Fatalf("saved client = %+v", saved)
	}
	if strings.Join(saved.RedirectURIs, ",") != "https://partner.example/callback" || saved.TokenEndpointAuthMethod != "none" {
		t.Fatalf("saved hydra metadata = %+v", saved)
	}
	if saved.ClientSecret != "" {
		t.Fatalf("registry-only save returned client secret")
	}
	if len(audit.events) != 1 || audit.events[0].Action != "oidc.client.upsert" || audit.events[0].EntityID != "partner-app" {
		t.Fatalf("audit events = %+v", audit.events)
	}

	list := authedRequest(http.MethodGet, "/api/oauth2/clients", "")
	list.Header.Set("X-Permission-Scope", "permissions.read")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, list)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", rec.Code, rec.Body.String())
	}
	var page oidcClientListDTO
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode clients: %v", err)
	}
	if !containsOIDCClient(page.Clients, "partner-app") {
		t.Fatalf("clients = %+v", page.Clients)
	}
}

func TestHTTPOIDCClientAdminRoutesRequireManageScope(t *testing.T) {
	handler := newTestRouter(RouterOptions{OIDCClientAdmin: identity.NewOIDCClientAdminService(identity.NewDefaultOIDCClientRegistry())})

	req := authedRequest(http.MethodPost, "/api/oauth2/clients", `{"client_id":"partner-app","allowed_scopes":["openid"]}`)
	req.Header.Set("X-Permission-Scope", "permissions.read")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"forbidden"`) {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func containsOIDCClient(clients []oidcClientDTO, clientID string) bool {
	for _, client := range clients {
		if client.ClientID == clientID {
			return true
		}
	}
	return false
}
