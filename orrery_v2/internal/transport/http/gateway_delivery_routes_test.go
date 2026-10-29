package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/identity"
	gwbindings "orrery_v2/internal/gateway/binding"
	"orrery_v2/internal/gateway/channel"
	gwconfig "orrery_v2/internal/gateway/config"
	gwoutbound "orrery_v2/internal/gateway/outbound"
	"orrery_v2/internal/platform/db"
)

type routeGatewaySender struct {
	deliveries []channel.Delivery
}

type routeGatewayAccountStatuses []channel.AccountStatus

type routeErrorProfileDirectory struct {
	err error
}

type routeBotErrorProfileDirectory struct {
	profile agentcore.AgentProfile
	err     error
}

func (d routeErrorProfileDirectory) QueryDirectory(context.Context, agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	return agentcore.AgentDirectoryPage{}, d.err
}

func (d routeErrorProfileDirectory) GetProfile(context.Context, agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	return agentcore.AgentProfile{}, d.err
}

func (d routeBotErrorProfileDirectory) QueryDirectory(context.Context, agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	return agentcore.AgentDirectoryPage{}, d.err
}

func (d routeBotErrorProfileDirectory) GetProfile(context.Context, agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	return d.profile, nil
}

func (s routeGatewayAccountStatuses) GatewayAccountStatuses() []channel.AccountStatus {
	return append([]channel.AccountStatus(nil), s...)
}

func (s *routeGatewaySender) Send(_ context.Context, delivery channel.Delivery) (channel.DeliveryResult, error) {
	s.deliveries = append(s.deliveries, delivery)
	return channel.DeliveryResult{ExternalMessageID: "external-route"}, nil
}

func gatewayRoutePrincipal(tenantID, humanID string, scopes ...string) identity.Principal {
	return identity.Principal{
		SubjectProvider: "local", Subject: "user-a", UserID: "user-a", AgentProfileID: humanID,
		ViewerKind: identity.ViewerKindUser, TenantID: tenantID, CoarseScopes: scopes,
	}
}

func gatewayRouteRequest(method, path, body string, principal identity.Principal) *http.Request {
	req := authedHTTPTestRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-Tenant-ID", principal.TenantID)
	req.Header.Set("X-User-ID", principal.UserID)
	req.Header.Set("X-Agent-Profile-ID", principal.AgentProfileID)
	req.Header.Set("X-Viewer-Kind", principal.ViewerKind)
	req.Header.Set("X-Permission-Scope", strings.Join(principal.CoarseScopes, ","))
	return req.WithContext(identity.ContextWithPrincipal(req.Context(), principal))
}

func TestProfileGatewayBindingRoutesAreSelfAndTenantScoped(t *testing.T) {
	ctx := context.Background()
	core, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name() + "-core"})
	if err != nil {
		t.Fatalf("open core: %v", err)
	}
	t.Cleanup(func() { _ = core.Close() })
	bindings := gwbindings.NewStore(core)
	settings := gwconfig.NewStore(t.TempDir() + "/gateway.yaml")
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Feishu.Enabled = true
	cfg.Adapters.Feishu.Accounts = map[string]gwconfig.FeishuAccountConfig{
		"main": {Enabled: true, AppID: "app-a", AppSecret: "secret-a", ConnectionMode: "stream", DomainName: "feishu"},
	}
	if _, err := settings.Save(cfg); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	if _, err := bindings.PutAgentChannelBinding(ctx, gwbindings.PutAgentChannelBindingInput{
		TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "main", AgentProfileID: "agent-a", InboundEnabled: true,
	}); err != nil {
		t.Fatalf("seed ownership: %v", err)
	}
	directory := &fakeProfileWeixinDirectory{entries: []agentcore.AgentDirectoryEntry{
		{AgentProfileID: "human-a", AgentKind: agentcore.AgentKindHuman, Enabled: true, OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "tenant-a"}}},
		{AgentProfileID: "agent-a", AgentKind: agentcore.AgentKindAI, Enabled: true, OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "tenant-a"}}},
	}}
	router := newTestRouter(RouterOptions{
		GatewaySettings: settings, GatewayChannelBindings: bindings, AgentProfiles: directory,
		GatewayAccounts: routeGatewayAccountStatuses{{ChannelKey: "feishu", AccountID: "main", Enabled: true, Status: "connected"}},
	})
	principal := gatewayRoutePrincipal("tenant-a", "human-a", "settings.read")

	unknownField := httptest.NewRecorder()
	router.ServeHTTP(unknownField, gatewayRouteRequest(http.MethodPost, "/api/profile/gateway-bindings/feishu/code", `{"account_id":"main","agent_id":"agent-a","unexpected":true}`, principal))
	if unknownField.Code != http.StatusBadRequest || !strings.Contains(unknownField.Body.String(), `"code":"invalid_json"`) {
		t.Fatalf("unknown binding code field status = %d body=%s", unknownField.Code, unknownField.Body.String())
	}

	create := httptest.NewRecorder()
	router.ServeHTTP(create, gatewayRouteRequest(http.MethodPost, "/api/profile/gateway-bindings/feishu/code", `{"account_id":"main","agent_id":"agent-a"}`, principal))
	if create.Code != http.StatusOK {
		t.Fatalf("create code status = %d body=%s", create.Code, create.Body.String())
	}
	var code profileGatewayBindingCodeDTO
	if err := json.Unmarshal(create.Body.Bytes(), &code); err != nil {
		t.Fatalf("decode code: %v", err)
	}
	binding, err := bindings.ConsumeCode(ctx, code.Code, gwbindings.ExternalIdentity{
		ChannelKey: "feishu", AccountID: "main", ExternalTenantID: "fs-tenant", ExternalUserID: "open-a",
	})
	if err != nil {
		t.Fatalf("ConsumeCode: %v", err)
	}

	list := httptest.NewRecorder()
	router.ServeHTTP(list, gatewayRouteRequest(http.MethodGet, "/api/profile/gateway-bindings", "", principal))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), binding.ID) {
		t.Fatalf("list status = %d body=%s", list.Code, list.Body.String())
	}

	crossTenant := httptest.NewRecorder()
	router.ServeHTTP(crossTenant, gatewayRouteRequest(http.MethodGet, "/api/profile/gateway-bindings", "", gatewayRoutePrincipal("tenant-b", "human-a", "settings.read")))
	if crossTenant.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant list status = %d body=%s", crossTenant.Code, crossTenant.Body.String())
	}

	remove := httptest.NewRecorder()
	router.ServeHTTP(remove, gatewayRouteRequest(http.MethodDelete, "/api/profile/gateway-bindings/"+binding.ID, "", principal))
	if remove.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body=%s", remove.Code, remove.Body.String())
	}
	if _, err := bindings.Resolve(ctx, gwbindings.ExternalIdentity{ChannelKey: "feishu", AccountID: "main", ExternalTenantID: "fs-tenant", ExternalUserID: "open-a"}); err != gwbindings.ErrNotFound {
		t.Fatalf("revoked binding resolve error = %v", err)
	}
}

func TestProfileGatewayBindingCodeRequiresOwnedNamedFeishuAccount(t *testing.T) {
	ctx := context.Background()
	core, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name() + "-core"})
	if err != nil {
		t.Fatalf("open core: %v", err)
	}
	t.Cleanup(func() { _ = core.Close() })
	bindings := gwbindings.NewStore(core)
	settings := newWorkforceChannelGatewaySettings(t, "main", "disabled", "offline", "unowned", "wrong-owner", "foreign")
	record, err := settings.LoadOrDefault()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	cfg := record.Config
	disabled := cfg.Adapters.Feishu.Accounts["disabled"]
	disabled.Enabled = false
	cfg.Adapters.Feishu.Accounts["disabled"] = disabled
	if _, err := settings.Save(cfg); err != nil {
		t.Fatalf("disable account: %v", err)
	}
	for _, input := range []gwbindings.PutAgentChannelBindingInput{
		{TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "main", AgentProfileID: "agent-a", InboundEnabled: false},
		{TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "wrong-owner", AgentProfileID: "agent-b", InboundEnabled: true},
		{TenantID: "tenant-b", ChannelKey: "feishu", AccountID: "foreign", AgentProfileID: "agent-other", InboundEnabled: true},
	} {
		if _, err := bindings.PutAgentChannelBinding(ctx, input); err != nil {
			t.Fatalf("seed ownership %+v: %v", input, err)
		}
	}
	router := newTestRouter(RouterOptions{
		GatewaySettings: settings, GatewayChannelBindings: bindings, AgentProfiles: gatewayBindingProfileDirectory(),
		GatewayAccounts: routeGatewayAccountStatuses{
			{ChannelKey: "feishu", AccountID: "main", Enabled: true, Status: "connected"},
			{ChannelKey: "feishu", AccountID: "disabled", Enabled: false, Status: "connected"},
			{ChannelKey: "feishu", AccountID: "offline", Enabled: true, Status: "degraded"},
			{ChannelKey: "feishu", AccountID: "unowned", Enabled: true, Status: "connected"},
			{ChannelKey: "feishu", AccountID: "wrong-owner", Enabled: true, Status: "connected"},
			{ChannelKey: "feishu", AccountID: "foreign", Enabled: true, Status: "connected"},
		},
	})
	principal := gatewayRoutePrincipal("tenant-a", "human-admin", "settings.read")
	for _, test := range []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{name: "missing account", body: `{"agent_id":"agent-a"}`, wantStatus: http.StatusBadRequest, wantCode: "gateway_binding_account_required"},
		{name: "unknown account", body: `{"account_id":"missing","agent_id":"agent-a"}`, wantStatus: http.StatusNotFound, wantCode: "gateway_channel_account_missing"},
		{name: "disabled account", body: `{"account_id":"disabled","agent_id":"agent-a"}`, wantStatus: http.StatusPreconditionFailed, wantCode: "feishu_gateway_account_disabled"},
		{name: "disconnected account", body: `{"account_id":"offline","agent_id":"agent-a"}`, wantStatus: http.StatusPreconditionFailed, wantCode: "gateway_channel_not_connected"},
		{name: "unowned account", body: `{"account_id":"unowned","agent_id":"agent-a"}`, wantStatus: http.StatusPreconditionFailed, wantCode: "feishu_gateway_account_unowned"},
		{name: "wrong owner", body: `{"account_id":"wrong-owner","agent_id":"agent-a"}`, wantStatus: http.StatusConflict, wantCode: "feishu_gateway_account_owner_mismatch"},
		{name: "wrong tenant", body: `{"account_id":"foreign","agent_id":"agent-other"}`, wantStatus: http.StatusNotFound, wantCode: "gateway_channel_account_missing"},
		{name: "unknown field", body: `{"account_id":"main","agent_id":"agent-a","app_secret":"secret"}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_json"},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, gatewayRouteRequest(http.MethodPost, "/api/profile/gateway-bindings/feishu/code", test.body, principal))
			if recorder.Code != test.wantStatus || !strings.Contains(recorder.Body.String(), `"code":"`+test.wantCode+`"`) || strings.Contains(recorder.Body.String(), "secret") {
				t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
			}
			var active int
			if err := core.QueryRow(ctx, `SELECT COUNT(*) FROM gateway_channel_binding_codes WHERE consumed_at = 0`).Scan(&active); err != nil || active != 0 {
				t.Fatalf("active binding codes after rejection = %d, %v", active, err)
			}
		})
	}

	list := httptest.NewRecorder()
	router.ServeHTTP(list, gatewayRouteRequest(http.MethodGet, "/api/profile/gateway-bindings", "", principal))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"account_id":"main"`) || !strings.Contains(list.Body.String(), `"agent_id":"agent-a"`) || strings.Contains(list.Body.String(), `"account_id":"foreign"`) || strings.Contains(list.Body.String(), "secret") {
		t.Fatalf("available profile accounts status = %d body=%s", list.Code, list.Body.String())
	}

	create := httptest.NewRecorder()
	router.ServeHTTP(create, gatewayRouteRequest(http.MethodPost, "/api/profile/gateway-bindings/feishu/code", `{"account_id":"main","agent_id":"agent-a"}`, principal))
	if create.Code != http.StatusOK || !strings.Contains(create.Body.String(), `"account_id":"main"`) {
		t.Fatalf("create code status = %d body=%s", create.Code, create.Body.String())
	}
}

func TestProfileGatewayBindingCodePreservesDingTalkAccountDerivation(t *testing.T) {
	store := newWorkforceChannelBindingStore(t)
	settings := gwconfig.NewStore(t.TempDir() + "/gateway.yaml")
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.DingTalk.Enabled = true
	cfg.Adapters.DingTalk.APIKey = "app-b"
	cfg.Adapters.DingTalk.Token = "secret-b"
	if _, err := settings.Save(cfg); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	router := newTestRouter(RouterOptions{
		GatewaySettings: settings, GatewayChannelBindings: store, AgentProfiles: gatewayBindingProfileDirectory(),
		GatewayAccounts: routeGatewayAccountStatuses{{ChannelKey: "dingtalk", AccountID: "dingtalk:app-b", Enabled: true, Status: "connected"}},
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, gatewayRouteRequest(http.MethodPost, "/api/profile/gateway-bindings/dingtalk/code", `{"account_id":"ignored","agent_id":"agent-a"}`, gatewayRoutePrincipal("tenant-a", "human-admin", "settings.read")))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"account_id":"dingtalk:app-b"`) {
		t.Fatalf("DingTalk derived account status = %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func gatewayBindingProfileDirectory() *fakeProfileWeixinDirectory {
	entry := func(id, tenant string, kind agentcore.AgentKind) agentcore.AgentDirectoryEntry {
		return agentcore.AgentDirectoryEntry{
			AgentProfileID: agentcore.AgentProfileID(id), DisplayName: id, AgentKind: kind, Enabled: true,
			OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: agentcore.OrganizationID(tenant)}},
		}
	}
	return &fakeProfileWeixinDirectory{entries: []agentcore.AgentDirectoryEntry{
		entry("human-admin", "tenant-a", agentcore.AgentKindHuman),
		entry("agent-a", "tenant-a", agentcore.AgentKindAI),
		entry("agent-b", "tenant-a", agentcore.AgentKindAI),
		entry("agent-other", "tenant-b", agentcore.AgentKindAI),
	}}
}

func TestProfileGatewayBindingRoutesReportInfrastructureOutages(t *testing.T) {
	ctx := context.Background()
	core, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name() + "-core"})
	if err != nil {
		t.Fatalf("open core: %v", err)
	}
	bindings := gwbindings.NewStore(core)
	if _, err := bindings.PutAgentChannelBinding(ctx, gwbindings.PutAgentChannelBindingInput{
		TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "main", AgentProfileID: "agent-a", InboundEnabled: true,
	}); err != nil {
		t.Fatalf("seed ownership: %v", err)
	}
	if err := core.Close(); err != nil {
		t.Fatalf("close core: %v", err)
	}
	settings := gwconfig.NewStore(t.TempDir() + "/gateway.yaml")
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Feishu.Enabled = true
	cfg.Adapters.Feishu.Accounts = map[string]gwconfig.FeishuAccountConfig{
		"main": {Enabled: true, AppID: "app-a", AppSecret: "secret-a", ConnectionMode: "stream", DomainName: "feishu"},
	}
	if _, err := settings.Save(cfg); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	directory := &fakeProfileWeixinDirectory{entries: []agentcore.AgentDirectoryEntry{
		{AgentProfileID: "human-a", AgentKind: agentcore.AgentKindHuman, Enabled: true, OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "tenant-a"}}},
		{AgentProfileID: "agent-a", AgentKind: agentcore.AgentKindAI, Enabled: true, OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "tenant-a"}}},
	}}
	router := newTestRouter(RouterOptions{
		GatewaySettings: settings, GatewayChannelBindings: bindings, AgentProfiles: directory,
		GatewayAccounts: routeGatewayAccountStatuses{{ChannelKey: "feishu", AccountID: "main", Enabled: true, Status: "connected"}},
	})
	principal := gatewayRoutePrincipal("tenant-a", "human-a", "settings.read")

	for _, request := range []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/api/profile/gateway-bindings"},
		{method: http.MethodPost, path: "/api/profile/gateway-bindings/feishu/code", body: `{"account_id":"main","agent_id":"agent-a"}`},
		{method: http.MethodDelete, path: "/api/profile/gateway-bindings/gwb_missing"},
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, gatewayRouteRequest(request.method, request.path, request.body, principal))
		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s status = %d body=%s", request.method, request.path, recorder.Code, recorder.Body.String())
		}
	}

	agentOutageBindings := gwbindings.NewStore(mustOpenGatewayRouteDB(t))
	if _, err := agentOutageBindings.PutAgentChannelBinding(ctx, gwbindings.PutAgentChannelBindingInput{
		TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "main", AgentProfileID: "agent-a", InboundEnabled: true,
	}); err != nil {
		t.Fatalf("seed agent outage ownership: %v", err)
	}
	agentOutage := newTestRouter(RouterOptions{
		GatewaySettings: settings, GatewayChannelBindings: agentOutageBindings,
		AgentProfiles: routeBotErrorProfileDirectory{
			profile: agentcore.AgentProfile{
				ID: "human-a", AgentKind: agentcore.AgentKindHuman, Enabled: true,
				OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "tenant-a"}},
			},
			err: errors.New("profile database unavailable"),
		},
		GatewayAccounts: routeGatewayAccountStatuses{{ChannelKey: "feishu", AccountID: "main", Enabled: true, Status: "connected"}},
	})
	recorder := httptest.NewRecorder()
	agentOutage.ServeHTTP(recorder, gatewayRouteRequest(http.MethodPost, "/api/profile/gateway-bindings/feishu/code", `{"account_id":"main","agent_id":"agent-a"}`, principal))
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), `"code":"gateway_binding_agents_unavailable"`) {
		t.Fatalf("agent validation outage status = %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func mustOpenGatewayRouteDB(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(context.Background(), db.Config{Driver: db.DriverMemory, Name: t.Name() + "-available-core"})
	if err != nil {
		t.Fatalf("open route database: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestGatewayDeliveryRoutesEnforcePermissionAndTenant(t *testing.T) {
	ctx := context.Background()
	core, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name() + "-core"})
	if err != nil {
		t.Fatalf("open core: %v", err)
	}
	jobs, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name() + "-jobs"})
	if err != nil {
		t.Fatalf("open jobs: %v", err)
	}
	t.Cleanup(func() { _ = core.Close(); _ = jobs.Close() })
	bindings := gwbindings.NewStore(core)
	_, err = bindings.UpsertVerified(ctx, gwbindings.UpsertInput{
		TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "feishu:app-a", ExternalTenantID: "fs-tenant",
		ExternalUserID: "open-a", HumanProfileID: "human-a", DefaultAgentID: "agent-a",
	})
	if err != nil {
		t.Fatalf("UpsertVerified: %v", err)
	}
	sender := &routeGatewaySender{}
	deliveries := gwoutbound.NewService(jobs, bindings, sender)
	directory := &fakeProfileWeixinDirectory{entries: []agentcore.AgentDirectoryEntry{
		{AgentProfileID: "human-a", AgentKind: agentcore.AgentKindHuman, Enabled: true, OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "tenant-a"}}},
	}}
	router := newTestRouter(RouterOptions{GatewayDeliveries: deliveries, AgentProfiles: directory})
	body := `{"human_profile_id":"human-a","channels":["feishu"],"event_type":"manual","body":"hello","format":"text","idempotency_key":"route-event-1"}`
	validationOutage := httptest.NewRecorder()
	newTestRouter(RouterOptions{
		GatewayDeliveries: deliveries, AgentProfiles: routeErrorProfileDirectory{err: errors.New("profile database unavailable")},
	}).ServeHTTP(validationOutage, gatewayRouteRequest(http.MethodPost, "/api/gateway/deliveries", body, gatewayRoutePrincipal("tenant-a", "", "gateway.delivery.send")))
	if validationOutage.Code != http.StatusServiceUnavailable || !strings.Contains(validationOutage.Body.String(), `"code":"gateway_delivery_target_validation_unavailable"`) {
		t.Fatalf("target validation outage status = %d body=%s", validationOutage.Code, validationOutage.Body.String())
	}

	forbidden := httptest.NewRecorder()
	router.ServeHTTP(forbidden, gatewayRouteRequest(http.MethodPost, "/api/gateway/deliveries", body, gatewayRoutePrincipal("tenant-a", "", "settings.read")))
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("unprivileged delivery status = %d body=%s", forbidden.Code, forbidden.Body.String())
	}

	created := httptest.NewRecorder()
	router.ServeHTTP(created, gatewayRouteRequest(http.MethodPost, "/api/gateway/deliveries", body, gatewayRoutePrincipal("tenant-a", "", "gateway.delivery.send")))
	if created.Code != http.StatusAccepted {
		t.Fatalf("create delivery status = %d body=%s", created.Code, created.Body.String())
	}
	var response gatewayDeliveriesDTO
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil || len(response.Deliveries) != 1 {
		t.Fatalf("decode deliveries = %+v err=%v body=%s", response, err, created.Body.String())
	}
	deliveryID := response.Deliveries[0].ID
	if strings.Contains(created.Body.String(), "open-a") || strings.Contains(created.Body.String(), "fs-tenant") || strings.Contains(created.Body.String(), "feishu:app-a") {
		t.Fatalf("delivery response leaked external identifiers: %s", created.Body.String())
	}
	unsupported := httptest.NewRecorder()
	unsupportedBody := `{"human_profile_id":"human-a","channels":["feishu"],"event_type":"manual","body":"hello","format":"text","idempotency_key":"route-event-2","action_url":"/approvals/1"}`
	router.ServeHTTP(unsupported, gatewayRouteRequest(http.MethodPost, "/api/gateway/deliveries", unsupportedBody, gatewayRoutePrincipal("tenant-a", "", "gateway.delivery.send")))
	if unsupported.Code != http.StatusBadRequest || !strings.Contains(unsupported.Body.String(), `"code":"invalid_json"`) {
		t.Fatalf("unsupported delivery field status = %d body=%s", unsupported.Code, unsupported.Body.String())
	}
	for table, want := range map[string]int{
		"gateway_message_deliveries": 1,
		"platform_outbox_jobs":       1,
	} {
		var count int
		if err := jobs.QueryRow(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != want {
			t.Fatalf("%s count = %d, want %d after rejected request", table, count, want)
		}
	}

	crossTenant := httptest.NewRecorder()
	router.ServeHTTP(crossTenant, gatewayRouteRequest(http.MethodGet, "/api/gateway/deliveries/"+deliveryID, "", gatewayRoutePrincipal("tenant-b", "", "settings.read")))
	if crossTenant.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant delivery status = %d body=%s", crossTenant.Code, crossTenant.Body.String())
	}

	read := httptest.NewRecorder()
	router.ServeHTTP(read, gatewayRouteRequest(http.MethodGet, "/api/gateway/deliveries/"+deliveryID, "", gatewayRoutePrincipal("tenant-a", "", "settings.read")))
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), deliveryID) {
		t.Fatalf("read delivery status = %d body=%s", read.Code, read.Body.String())
	}
}

func TestGatewayAccountStatusRouteReportsRuntimeHealth(t *testing.T) {
	router := newTestRouter(RouterOptions{GatewayAccounts: routeGatewayAccountStatuses{
		{ChannelKey: "feishu", AccountID: "feishu:app-a", Enabled: true, Status: "connected"},
		{ChannelKey: "dingtalk", AccountID: "dingtalk:app-b", Enabled: true, Status: "degraded", LastError: "stream unavailable"},
	}})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, gatewayRouteRequest(http.MethodGet, "/api/settings/gateway/accounts", "", gatewayRoutePrincipal("tenant-a", "", "settings.read")))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"connected"`) || !strings.Contains(rec.Body.String(), `"status":"degraded"`) {
		t.Fatalf("account status = %d body=%s", rec.Code, rec.Body.String())
	}
}
