package httptransport

import (
	"net/http"
	"net/http/httptest"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/identity"
	devicedirectory "orrery_v2/internal/device/directory"
	gwconfig "orrery_v2/internal/gateway/config"
)

func TestProfileHumanFromRequestUsesAuthenticatedPrincipal(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/profile/weixin", nil)
	req.Header.Set("X-Agent-Profile-ID", "spoofed-profile")
	req.Header.Set("X-User-Display-Name", "Spoofed")
	req = req.WithContext(identity.ContextWithPrincipal(req.Context(), identity.Principal{
		SubjectProvider: "local",
		Subject:         "alice",
		Username:        "alice",
		DisplayName:     "Alice",
		AgentProfileID:  "human-alice",
	}))

	humanID, displayName := profileHumanFromRequest(req)
	if humanID != "human-alice" || displayName != "Alice" {
		t.Fatalf("profile identity = (%q, %q), want authenticated principal", humanID, displayName)
	}
}

func TestProfileHumanFromRequestRejectsSpoofedProfileForUnboundPrincipal(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/profile/weixin", nil)
	req.Header.Set("X-Agent-Profile-ID", "victim-profile")
	req = req.WithContext(identity.ContextWithPrincipal(req.Context(), identity.Principal{
		SubjectProvider: "local",
		Subject:         "unbound-user",
		Username:        "unbound-user",
		DisplayName:     "Unbound User",
	}))

	humanID, displayName := profileHumanFromRequest(req)
	if humanID != "" || displayName != "Unbound User" {
		t.Fatalf("profile identity = (%q, %q), want no human profile", humanID, displayName)
	}
}

func TestReplaceGatewayWeixinHumanBindingRevokesDisplacedAccount(t *testing.T) {
	extra := map[string]any{
		"weixinUserBindings":  map[string]any{"old-user": "human-alice", "other-user": "human-bob"},
		"agentBindings":       map[string]any{"old-user": "agent-old", "other-user": "agent-other"},
		"activeAgentBindings": map[string]any{"old-user": "agent-active"},
		"weixinAccounts":      map[string]any{"old-user": map[string]any{"token": "old-secret"}},
	}

	replaceGatewayWeixinHumanBinding(extra, "system", "new-user", "human-alice")

	for _, key := range []string{"agentBindings", "activeAgentBindings", "weixinAccounts"} {
		if values, ok := extra[key].(map[string]any); ok {
			if _, exists := values["old-user"]; exists {
				t.Fatalf("%s retained displaced account: %+v", key, values)
			}
		}
	}
	bindings := profileStringBindingsExtra(extra["weixinUserBindings"])
	if bindings[gwconfig.WeixinBindingKey("system", "new-user")] != "human-alice" || bindings["other-user"] != "human-bob" || bindings["old-user"] != "" {
		t.Fatalf("bindings = %+v", bindings)
	}
}

func TestGatewayWeixinBindingsAreIsolatedByTenant(t *testing.T) {
	tenantAKey := gwconfig.WeixinBindingKey("tenant-a", "shared-user")
	tenantBKey := gwconfig.WeixinBindingKey("tenant-b", "shared-user")
	extra := map[string]any{
		"weixinUserBindings": map[string]any{tenantAKey: "shared-human", tenantBKey: "shared-human"},
		"agentBindings":      map[string]any{tenantAKey: "agent-a", tenantBKey: "agent-b"},
		"weixinAccounts": map[string]any{
			tenantAKey: map[string]any{"tenantId": "tenant-a", "userId": "shared-user", "token": "token-a"},
			tenantBKey: map[string]any{"tenantId": "tenant-b", "userId": "shared-user", "token": "token-b"},
		},
	}
	if got := gatewayWeixinUserIDForHuman(extra, "tenant-a", "shared-human"); got != "shared-user" {
		t.Fatalf("tenant-a user = %q", got)
	}
	removeGatewayWeixinBinding(extra, "tenant-a", "shared-user")
	for _, key := range []string{"weixinUserBindings", "agentBindings", "weixinAccounts"} {
		values := extra[key].(map[string]any)
		if _, exists := values[tenantAKey]; exists {
			t.Fatalf("%s retained deleted tenant-a value: %+v", key, values)
		}
		if _, exists := values[tenantBKey]; !exists {
			t.Fatalf("%s deleted tenant-b value: %+v", key, values)
		}
	}
}

func TestRemoveGatewayWeixinPersonalAssistantBindingSupportsTypedMap(t *testing.T) {
	tenantAKey := gwconfig.WeixinBindingKey("tenant-a", "shared-user")
	tenantBKey := gwconfig.WeixinBindingKey("tenant-b", "shared-user")
	extra := map[string]any{
		"personalAssistantBindings": map[string]string{tenantAKey: "account-a", tenantBKey: "account-b"},
	}

	removeGatewayWeixinPersonalAssistantBinding(extra, "tenant-a", "shared-user")

	bindings := profileStringBindingsExtra(extra["personalAssistantBindings"])
	if bindings[tenantAKey] != "" || bindings[tenantBKey] != "account-b" {
		t.Fatalf("personal assistant bindings = %+v", bindings)
	}
}

func TestProfileWeixinTenantUsesAuthenticatedPrincipalInsteadOfHeader(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/profile/weixin", nil)
	req.Header.Set("X-Tenant-ID", "tenant-b")
	req = req.WithContext(identity.ContextWithPrincipal(req.Context(), identity.Principal{
		SubjectProvider: "local",
		Subject:         "user-1",
		UserID:          "user-1",
		ViewerKind:      identity.ViewerKindUser,
		TenantID:        "tenant-a",
	}))
	rec := httptest.NewRecorder()
	router := &Router{}
	tenantID, ok := router.profileWeixinTenant(rec, req)
	if !ok || tenantID != "tenant-a" {
		t.Fatalf("tenant = %q ok=%v body=%s", tenantID, ok, rec.Body.String())
	}
}

func TestProfileWeixinBotsQueryIsTenantScoped(t *testing.T) {
	directory := &fakeProfileWeixinDirectory{}
	router := &Router{agentProfiles: directory}
	req := httptest.NewRequest("GET", "/api/profile/weixin", nil)
	if _, err := router.profileWeixinBots(req, "tenant-a"); err != nil {
		t.Fatalf("profileWeixinBots: %v", err)
	}
	if directory.lastQuery.Organization != agentcore.OrganizationID("tenant-a") {
		t.Fatalf("organization filter = %q", directory.lastQuery.Organization)
	}
}

func TestProfileWeixinTenantDerivesOrganizationFromHumanProfile(t *testing.T) {
	directory := &fakeProfileWeixinDirectory{entries: []agentcore.AgentDirectoryEntry{{
		AgentProfileID: "human-alice",
		AgentKind:      agentcore.AgentKindHuman,
		Enabled:        true,
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef: agentcore.OrganizationRef{ID: "tenant-a"},
		},
	}}}
	router := &Router{agentProfiles: directory}
	req := httptest.NewRequest("GET", "/api/profile/weixin", nil)
	req = req.WithContext(identity.ContextWithPrincipal(req.Context(), identity.Principal{
		SubjectProvider: "local",
		Subject:         "user-1",
		UserID:          "user-1",
		AgentProfileID:  "human-alice",
		ViewerKind:      identity.ViewerKindUser,
	}))
	rec := httptest.NewRecorder()
	tenantID, ok := router.profileWeixinTenant(rec, req)
	if !ok || tenantID != "tenant-a" {
		t.Fatalf("tenant = %q ok=%v body=%s", tenantID, ok, rec.Body.String())
	}
}

func TestProfileWeixinPersonalAssistantReadyUsesCanonicalProfileIdentity(t *testing.T) {
	tests := []struct {
		name       string
		view       devicedirectory.PersonalAssistantView
		wantReady  bool
		wantStatus int
	}{
		{
			name:       "canonical profile without legacy template",
			view:       devicedirectory.PersonalAssistantView{AgentProfileID: "A000016"},
			wantReady:  true,
			wantStatus: http.StatusOK,
		},
		{
			name:       "legacy template without canonical profile",
			view:       devicedirectory.PersonalAssistantView{AssignedAgentID: "A000001"},
			wantReady:  false,
			wantStatus: http.StatusPreconditionFailed,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := &Router{personalAssistant: &fakePersonalAssistantService{view: test.view}}
			req := httptest.NewRequest(http.MethodPost, "/api/profile/weixin/login/start", nil)
			recorder := httptest.NewRecorder()

			ready := router.profileWeixinPersonalAssistantReady(recorder, req, "minvia", "quzheng")

			if ready != test.wantReady || recorder.Code != test.wantStatus {
				t.Fatalf("ready=%v status=%d body=%s", ready, recorder.Code, recorder.Body.String())
			}
		})
	}
}
