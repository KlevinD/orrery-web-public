package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	gwbindings "orrery_v2/internal/gateway/binding"
	"orrery_v2/internal/gateway/channel"
	gwconfig "orrery_v2/internal/gateway/config"
	"orrery_v2/internal/platform/db"
)

type workforceChannelProfileDirectory struct {
	profiles map[agentcore.AgentProfileID]agentcore.AgentProfile
}

func (d workforceChannelProfileDirectory) GetProfile(_ context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	profile, ok := d.profiles[id]
	if !ok {
		return agentcore.AgentProfile{}, agentcore.ErrNotFound
	}
	return profile, nil
}

func (d workforceChannelProfileDirectory) QueryDirectory(context.Context, agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	return agentcore.AgentDirectoryPage{}, nil
}

func TestWorkforceChannelBindingRoutesLifecycleAndSafety(t *testing.T) {
	store := newWorkforceChannelBindingStore(t)
	audit := &fakeIdentityAuditRecorder{}
	if _, err := store.PutAgentChannelBinding(context.Background(), gwbindings.PutAgentChannelBindingInput{
		TenantID: "tenant-b", ChannelKey: "feishu", AccountID: "foreign",
		AgentProfileID: "agent-other", InboundEnabled: true,
	}); err != nil {
		t.Fatalf("seed foreign ownership: %v", err)
	}
	settings := newWorkforceChannelGatewaySettings(t, "main", "offline", "foreign")
	router := newTestRouter(RouterOptions{
		GatewayChannelBindings: store,
		GatewaySettings:        settings,
		AuditRecorder:          audit,
		GatewayAccounts: routeGatewayAccountStatuses{
			{ChannelKey: "feishu", AccountID: "main", Enabled: true, Status: "connected", LastError: "secret-token@example.invalid"},
			{ChannelKey: "feishu", AccountID: "foreign", Enabled: true, Status: "connected"},
		},
		AgentProfiles: workforceChannelProfiles(),
	})

	readPath := "/api/workforce/members/agent-a/channel-bindings"
	missingSettings := httptest.NewRecorder()
	router.ServeHTTP(missingSettings, gatewayRouteRequest(http.MethodGet, readPath, "", gatewayRoutePrincipal("tenant-a", "human-admin", "workforce.read")))
	if missingSettings.Code != http.StatusForbidden {
		t.Fatalf("GET without settings.read status = %d body=%s", missingSettings.Code, missingSettings.Body.String())
	}

	read := httptest.NewRecorder()
	router.ServeHTTP(read, gatewayRouteRequest(http.MethodGet, readPath, "", gatewayRoutePrincipal("tenant-a", "human-admin", "workforce.read", "settings.read")))
	if read.Code != http.StatusOK {
		t.Fatalf("GET bindings status = %d body=%s", read.Code, read.Body.String())
	}
	if strings.Contains(read.Body.String(), "secret-token") || strings.Contains(read.Body.String(), `"account_id":"foreign"`) {
		t.Fatalf("GET bindings leaked connector or cross-tenant data: %s", read.Body.String())
	}
	var listed struct {
		AgentProfileID string `json:"agent_profile_id"`
		Accounts       []struct {
			AccountID string `json:"account_id"`
			Status    string `json:"status"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode GET bindings: %v", err)
	}
	if listed.AgentProfileID != "agent-a" || len(listed.Accounts) != 2 || listed.Accounts[0].AccountID != "main" || listed.Accounts[1].AccountID != "offline" || listed.Accounts[1].Status != "unavailable" {
		t.Fatalf("configured accounts = %+v", listed)
	}

	putPath := readPath + "/feishu/main"
	createBody := `{"inbound_enabled":true,"expected_revision":0,"expected_owner_agent_profile_id":""}`
	missingManage := httptest.NewRecorder()
	router.ServeHTTP(missingManage, gatewayRouteRequest(http.MethodPut, putPath, createBody, gatewayRoutePrincipal("tenant-a", "human-admin", "workforce.manage")))
	if missingManage.Code != http.StatusForbidden {
		t.Fatalf("PUT without settings.manage status = %d body=%s", missingManage.Code, missingManage.Body.String())
	}

	create := httptest.NewRecorder()
	router.ServeHTTP(create, gatewayRouteRequest(http.MethodPut, putPath, createBody, gatewayRoutePrincipal("tenant-a", "human-admin", "workforce.manage", "settings.manage")))
	if create.Code != http.StatusOK || !strings.Contains(create.Body.String(), `"owner_agent_profile_id":"agent-a"`) || !strings.Contains(create.Body.String(), `"revision":1`) {
		t.Fatalf("PUT create status = %d body=%s", create.Code, create.Body.String())
	}

	movePath := "/api/workforce/members/agent-b/channel-bindings/feishu/main"
	moveBody := `{"inbound_enabled":false,"expected_revision":1,"expected_owner_agent_profile_id":"agent-a"}`
	move := httptest.NewRecorder()
	router.ServeHTTP(move, gatewayRouteRequest(http.MethodPut, movePath, moveBody, gatewayRoutePrincipal("tenant-a", "human-admin", "workforce.manage", "settings.manage")))
	if move.Code != http.StatusOK || !strings.Contains(move.Body.String(), `"owner_agent_profile_id":"agent-b"`) || !strings.Contains(move.Body.String(), `"inbound_enabled":false`) || !strings.Contains(move.Body.String(), `"revision":2`) {
		t.Fatalf("PUT move status = %d body=%s", move.Code, move.Body.String())
	}

	toggleBody := `{"inbound_enabled":true,"expected_revision":2,"expected_owner_agent_profile_id":"agent-b"}`
	toggle := httptest.NewRecorder()
	router.ServeHTTP(toggle, gatewayRouteRequest(http.MethodPut, movePath, toggleBody, gatewayRoutePrincipal("tenant-a", "human-admin", "workforce.manage", "settings.manage")))
	if toggle.Code != http.StatusOK || !strings.Contains(toggle.Body.String(), `"inbound_enabled":true`) || !strings.Contains(toggle.Body.String(), `"revision":3`) {
		t.Fatalf("PUT toggle status = %d body=%s", toggle.Code, toggle.Body.String())
	}

	stale := httptest.NewRecorder()
	router.ServeHTTP(stale, gatewayRouteRequest(http.MethodPut, putPath, moveBody, gatewayRoutePrincipal("tenant-a", "human-admin", "workforce.manage", "settings.manage")))
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale PUT status = %d body=%s", stale.Code, stale.Body.String())
	}

	foreign := httptest.NewRecorder()
	router.ServeHTTP(foreign, gatewayRouteRequest(http.MethodPut, "/api/workforce/members/agent-a/channel-bindings/feishu/foreign", createBody, gatewayRoutePrincipal("tenant-a", "human-admin", "workforce.manage", "settings.manage")))
	if foreign.Code != http.StatusConflict {
		t.Fatalf("cross-tenant PUT status = %d body=%s", foreign.Code, foreign.Body.String())
	}

	remove := httptest.NewRecorder()
	removeBody := `{"expected_revision":3,"expected_owner_agent_profile_id":"agent-b"}`
	router.ServeHTTP(remove, gatewayRouteRequest(http.MethodDelete, movePath, removeBody, gatewayRoutePrincipal("tenant-a", "human-admin", "workforce.manage", "settings.manage")))
	if remove.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d body=%s", remove.Code, remove.Body.String())
	}
	if _, err := store.ResolveAgentChannelBinding(context.Background(), "feishu", "main"); !errors.Is(err, gwbindings.ErrNotFound) {
		t.Fatalf("binding after DELETE error = %v", err)
	}
	wantAuditActions := []string{
		"gateway.feishu_ownership.create",
		"gateway.feishu_ownership.move",
		"gateway.feishu_ownership.toggle_inbound",
		"gateway.feishu_ownership.delete",
	}
	if len(audit.events) != len(wantAuditActions) {
		t.Fatalf("ownership audit events = %+v", audit.events)
	}
	for i, action := range wantAuditActions {
		if audit.events[i].Action != action || audit.events[i].Metadata["account_id"] != "main" || strings.Contains(audit.events[i].Summary, "secret") {
			t.Fatalf("ownership audit event[%d] = %+v", i, audit.events[i])
		}
	}
}

func TestWorkforceChannelBindingRoutesHidePersonalAssistantProfile(t *testing.T) {
	profiles := workforceChannelProfiles()
	profiles.profiles["private-assistant"] = agentcore.AgentProfile{
		ID: "private-assistant", PersonalAssistantOwnerID: "owner-1", AgentKind: agentcore.AgentKindAI, Enabled: true,
		OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "tenant-a"}},
		Placement:   agentcore.TalentPlacement{Status: agentcore.TalentPlacementInternal},
	}
	router := newTestRouter(RouterOptions{AgentProfiles: profiles})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, gatewayRouteRequest(
		http.MethodGet,
		"/api/workforce/members/private-assistant/channel-bindings",
		"",
		gatewayRoutePrincipal("tenant-a", "human-admin", "workforce.read", "settings.read"),
	))
	if recorder.Code != http.StatusNotFound || strings.Contains(recorder.Body.String(), "private-assistant") {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestWorkforceChannelBindingRoutesRejectInvalidTargetsAndWrites(t *testing.T) {
	router := newTestRouter(RouterOptions{
		GatewayChannelBindings: newWorkforceChannelBindingStore(t),
		GatewaySettings:        newWorkforceChannelGatewaySettings(t, "main"),
		GatewayAccounts:        routeGatewayAccountStatuses{{ChannelKey: "feishu", AccountID: "main", Enabled: true, Status: "connected"}},
		AgentProfiles:          workforceChannelProfiles(),
	})
	principal := gatewayRoutePrincipal("tenant-a", "human-admin", "workforce.manage", "settings.manage")
	body := `{"inbound_enabled":true,"expected_revision":0,"expected_owner_agent_profile_id":""}`
	for _, test := range []struct {
		name       string
		agentID    string
		wantStatus int
	}{
		{name: "human", agentID: "human-admin", wantStatus: http.StatusUnprocessableEntity},
		{name: "disabled", agentID: "agent-disabled", wantStatus: http.StatusUnprocessableEntity},
		{name: "market", agentID: "agent-market", wantStatus: http.StatusUnprocessableEntity},
		{name: "cross tenant", agentID: "agent-other", wantStatus: http.StatusNotFound},
		{name: "missing", agentID: "missing", wantStatus: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			path := "/api/workforce/members/" + test.agentID + "/channel-bindings/feishu/main"
			router.ServeHTTP(rec, gatewayRouteRequest(http.MethodPut, path, body, principal))
			if rec.Code != test.wantStatus {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}

	unknownField := httptest.NewRecorder()
	router.ServeHTTP(unknownField, gatewayRouteRequest(http.MethodPut, "/api/workforce/members/agent-a/channel-bindings/feishu/main", strings.TrimSuffix(body, "}")+`,"app_secret":"secret"}`, principal))
	if unknownField.Code != http.StatusBadRequest || strings.Contains(unknownField.Body.String(), "secret") {
		t.Fatalf("unknown field status = %d body=%s", unknownField.Code, unknownField.Body.String())
	}
}

func TestWorkforceChannelBindingRoutesResolveBlankPrincipalTenantFromHumanProfile(t *testing.T) {
	store := newWorkforceChannelBindingStore(t)
	if _, err := store.PutAgentChannelBinding(context.Background(), gwbindings.PutAgentChannelBindingInput{
		TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "main",
		AgentProfileID: "agent-a", InboundEnabled: true,
	}); err != nil {
		t.Fatalf("seed ownership: %v", err)
	}
	router := newTestRouter(RouterOptions{
		GatewayChannelBindings: store,
		GatewaySettings:        newWorkforceChannelGatewaySettings(t, "main"),
		GatewayAccounts:        routeGatewayAccountStatuses{{ChannelKey: "feishu", AccountID: "main", Enabled: true, Status: "connected"}},
		AgentProfiles:          workforceChannelProfiles(),
	})

	rec := httptest.NewRecorder()
	principal := gatewayRoutePrincipal("", "human-admin", "workforce.read", "settings.read")
	router.ServeHTTP(rec, gatewayRouteRequest(http.MethodGet, "/api/workforce/members/agent-a/channel-bindings", "", principal))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"owner_agent_profile_id":"agent-a"`) {
		t.Fatalf("GET bindings status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func newWorkforceChannelBindingStore(t *testing.T) *gwbindings.Store {
	t.Helper()
	core, err := db.Open(context.Background(), db.Config{Driver: db.DriverMemory, Name: t.Name() + "-core"})
	if err != nil {
		t.Fatalf("open core database: %v", err)
	}
	t.Cleanup(func() { _ = core.Close() })
	return gwbindings.NewStore(core)
}

func newWorkforceChannelGatewaySettings(t *testing.T, aliases ...string) *gwconfig.Store {
	t.Helper()
	store := gwconfig.NewStore(filepath.Join(t.TempDir(), "gateway.yaml"))
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Feishu.Enabled = true
	cfg.Adapters.Feishu.Accounts = make(map[string]gwconfig.FeishuAccountConfig, len(aliases))
	for _, alias := range aliases {
		cfg.Adapters.Feishu.Accounts[alias] = gwconfig.FeishuAccountConfig{
			Enabled: true, AppID: "app-" + alias, AppSecret: "secret-" + alias,
			ConnectionMode: "stream", DomainName: "feishu",
		}
	}
	if _, err := store.Save(cfg); err != nil {
		t.Fatalf("seed gateway settings: %v", err)
	}
	return store
}

func workforceChannelProfiles() workforceChannelProfileDirectory {
	profile := func(id, tenant string, kind agentcore.AgentKind, enabled bool, placement agentcore.TalentPlacementStatus) agentcore.AgentProfile {
		return agentcore.AgentProfile{
			ID: agentcore.AgentProfileID(id), DisplayName: id, AgentKind: kind, Enabled: enabled,
			OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: agentcore.OrganizationID(tenant)}},
			Placement:   agentcore.TalentPlacement{Status: placement},
		}
	}
	return workforceChannelProfileDirectory{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"agent-a":        profile("agent-a", "tenant-a", agentcore.AgentKindAI, true, agentcore.TalentPlacementInternal),
		"agent-b":        profile("agent-b", "tenant-a", agentcore.AgentKindAI, true, agentcore.TalentPlacementInternal),
		"human-admin":    profile("human-admin", "tenant-a", agentcore.AgentKindHuman, true, agentcore.TalentPlacementInternal),
		"agent-disabled": profile("agent-disabled", "tenant-a", agentcore.AgentKindAI, false, agentcore.TalentPlacementInternal),
		"agent-market":   profile("agent-market", "tenant-a", agentcore.AgentKindAI, true, agentcore.TalentPlacementMarket),
		"agent-other":    profile("agent-other", "tenant-b", agentcore.AgentKindAI, true, agentcore.TalentPlacementInternal),
	}}
}

var _ channel.AccountStatusProvider = routeGatewayAccountStatuses{}
