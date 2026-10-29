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
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/gateway/channel/weixin"
	gwconfig "orrery_v2/internal/gateway/config"
)

func TestGatewaySettingsRoutesReadMaskedConfig(t *testing.T) {
	store := gwconfig.NewStore(filepath.Join(t.TempDir(), "config", "gateway.yaml"))
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Email.Enabled = true
	cfg.Adapters.Email.Token = "secret-token"
	cfg.Adapters.Weixin.Extra = map[string]any{
		"agentBindings":       map[string]any{"user-1@im.wechat": "staffing-agent"},
		"activeAgentBindings": map[string]any{"user-1@im.wechat": "qa-agent"},
		"weixinUserBindings":  map[string]any{"user-1@im.wechat": "human-alice"},
		"weixinAccounts": map[string]any{
			"user-1@im.wechat": map[string]any{"token": "weixin-account-token"},
		},
	}
	if _, err := store.Save(cfg); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	router := newTestRouter(RouterOptions{
		GatewaySettings: store,
		GatewayBindingsChanged: func(context.Context, gwconfig.Config) error {
			return nil
		},
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, authedHTTPTestRequest(http.MethodGet, "/api/settings/gateway", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "secret-token") {
		t.Fatalf("GET leaked secret: %s", rec.Body.String())
	}
	for _, forbidden := range []string{"user-1@im.wechat", "staffing-agent", "qa-agent", "human-alice", "weixin-account-token", "agentBindings", "weixinUserBindings", "weixinAccounts", "activeAgentBindings"} {
		if strings.Contains(rec.Body.String(), forbidden) {
			t.Fatalf("GET leaked private weixin binding %q: %s", forbidden, rec.Body.String())
		}
	}
	if !strings.Contains(rec.Body.String(), gwconfig.MaskValue) {
		t.Fatalf("GET did not include mask marker: %s", rec.Body.String())
	}
}

func TestGatewaySettingsRoutesWriteStructuredConfig(t *testing.T) {
	store := gwconfig.NewStore(filepath.Join(t.TempDir(), "config", "gateway.yaml"))
	var reloaded gwconfig.Config
	reloadCalls := 0
	router := newTestRouter(RouterOptions{
		GatewaySettings: store,
		GatewaySettingsChanged: func(ctx context.Context, cfg gwconfig.Config) error {
			reloadCalls++
			reloaded = cfg
			return nil
		},
	})

	body := `{"config":{"gateway":{"port":18790,"bind_address":"127.0.0.1","idle_session_timeout_minutes":45,"max_per_session_mcp_instances":3},"adapters":{"feishu":{"default_session_label":"general","connection_mode":"stream","domain_name":"feishu"},"email":{"enabled":true,"token":"new-token","extra":{}}}}}`
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, authedHTTPTestRequest(http.MethodPut, "/api/settings/gateway", strings.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%s", rec.Code, rec.Body.String())
	}
	loaded, err := store.LoadOrDefault()
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if loaded.Config.Gateway.Port != 18790 || !loaded.Config.Adapters.Email.Enabled {
		t.Fatalf("config not persisted: %+v", loaded.Config)
	}
	if reloadCalls != 1 || !reloaded.Adapters.Email.Enabled {
		t.Fatalf("config not reloaded: calls=%d config=%+v", reloadCalls, reloaded.Adapters.Email)
	}
}

func TestGatewayWeixinAdministrativeLoginRouteIsRemoved(t *testing.T) {
	router := newTestRouter(RouterOptions{})
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, authedHTTPTestRequest(http.MethodPost, "/api/settings/gateway/weixin/login", strings.NewReader(`{}`)))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("admin login status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestProfileWeixinLoginRoutesBindScannedUserToAgent(t *testing.T) {
	store := gwconfig.NewStore(filepath.Join(t.TempDir(), "config", "gateway.yaml"))
	cfg := gwconfig.DefaultConfig()
	if _, err := store.Save(cfg); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	var gotLocalTokens []string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ilink/bot/get_bot_qrcode":
			var body struct {
				LocalTokenList []string `json:"local_token_list"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode qrcode request: %v", err)
			}
			gotLocalTokens = body.LocalTokenList
			writeJSON(w, http.StatusOK, map[string]any{
				"qrcode":             "qr-token",
				"qrcode_img_content": "https://weixin.example.test/qr-token",
			})
		case "/ilink/bot/get_qrcode_status":
			writeJSON(w, http.StatusOK, map[string]any{
				"status":        "confirmed",
				"bot_token":     "bot-secret",
				"ilink_bot_id":  "bot-1@im.bot",
				"baseurl":       server.URL,
				"ilink_user_id": "user-1@im.wechat",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	setTestWeixinLoginBaseURL(t, store, server.URL)

	var globalReloads, bindingReloads int
	router := newTestRouter(RouterOptions{
		GatewaySettings: store,
		GatewaySettingsChanged: func(context.Context, gwconfig.Config) error {
			globalReloads++
			return nil
		},
		GatewayBindingsChanged: func(context.Context, gwconfig.Config) error {
			bindingReloads++
			return nil
		},
		AgentProfiles: &fakeProfileWeixinDirectory{entries: []agentcore.AgentDirectoryEntry{
			{AgentProfileID: "human-alice", DisplayName: "Alice", AgentKind: agentcore.AgentKindHuman, Enabled: true, OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "system"}}},
			{AgentProfileID: "staffing-agent", DisplayName: "秦若兰", AgentKind: agentcore.AgentKindAI, Enabled: true},
		}},
	})
	startBody, err := json.Marshal(profileWeixinLoginStartRequest{
		AgentID: "staffing-agent",
	})
	if err != nil {
		t.Fatalf("marshal start body: %v", err)
	}
	startRec := httptest.NewRecorder()
	startReq := authedHTTPTestRequest(http.MethodPost, "/api/profile/weixin/login/start", strings.NewReader(string(startBody)))
	startReq.Header.Set("X-Agent-Profile-ID", "human-alice")
	router.ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusOK {
		t.Fatalf("start status = %d body=%s", startRec.Code, startRec.Body.String())
	}
	if len(gotLocalTokens) != 0 {
		t.Fatalf("binding scan should not send existing local tokens: %+v", gotLocalTokens)
	}
	var start gatewayWeixinLoginStartResponseDTO
	if err := json.Unmarshal(startRec.Body.Bytes(), &start); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	if _, err := store.Update(func(current *gwconfig.Config) error {
		current.Adapters.Weixin.Extra["pollIntervalSeconds"] = 9
		return nil
	}); err != nil {
		t.Fatalf("update system config during scan: %v", err)
	}

	statusRec := httptest.NewRecorder()
	statusReq := authedHTTPTestRequest(http.MethodGet, "/api/profile/weixin/login/"+start.SessionKey, nil)
	statusReq.Header.Set("X-Agent-Profile-ID", "human-alice")
	router.ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", statusRec.Code, statusRec.Body.String())
	}
	var status gatewayWeixinLoginStatusResponseDTO
	if err := json.Unmarshal(statusRec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.BoundUserID != "user-1@im.wechat" || status.AgentID != "staffing-agent" || status.BoundHumanID != "human-alice" {
		t.Fatalf("binding status = %+v", status)
	}
	if globalReloads != 0 || bindingReloads != 1 {
		t.Fatalf("personal binding reloads: global=%d bindings=%d", globalReloads, bindingReloads)
	}

	loaded, err := store.LoadOrDefault()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	bindingKey := gwconfig.WeixinBindingKey("system", "user-1@im.wechat")
	bindings, ok := loaded.Config.Adapters.Weixin.Extra["agentBindings"].(map[string]any)
	if !ok || bindings[bindingKey] != "staffing-agent" {
		t.Fatalf("agentBindings = %+v", loaded.Config.Adapters.Weixin.Extra["agentBindings"])
	}
	activeBindings, ok := loaded.Config.Adapters.Weixin.Extra["activeAgentBindings"].(map[string]any)
	if !ok || activeBindings[bindingKey] != "staffing-agent" {
		t.Fatalf("activeAgentBindings = %+v", loaded.Config.Adapters.Weixin.Extra["activeAgentBindings"])
	}
	humanBindings, ok := loaded.Config.Adapters.Weixin.Extra["weixinUserBindings"].(map[string]any)
	if !ok || humanBindings[bindingKey] != "human-alice" {
		t.Fatalf("weixinUserBindings = %+v", loaded.Config.Adapters.Weixin.Extra["weixinUserBindings"])
	}
	accounts, ok := loaded.Config.Adapters.Weixin.Extra["weixinAccounts"].(map[string]any)
	if !ok {
		t.Fatalf("weixinAccounts missing: %+v", loaded.Config.Adapters.Weixin.Extra)
	}
	account, ok := accounts[bindingKey].(map[string]any)
	if !ok || account["token"] != "bot-secret" || account["botId"] != "bot-1@im.bot" {
		t.Fatalf("weixin account = %+v", accounts[bindingKey])
	}
	if loaded.Config.Adapters.Weixin.Extra["pollIntervalSeconds"] != 9 {
		t.Fatalf("personal binding changed system settings: %+v", loaded.Config.Adapters.Weixin)
	}

	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, authedHTTPTestRequest(http.MethodGet, "/api/settings/gateway", nil))
	for _, forbidden := range []string{"bot-secret", "user-1@im.wechat", "staffing-agent", "human-alice"} {
		if strings.Contains(getRec.Body.String(), forbidden) {
			t.Fatalf("masked gateway settings leaked private binding %q: %s", forbidden, getRec.Body.String())
		}
	}
}

func TestProfileWeixinLoginRoutesBindScannedUserToHuman(t *testing.T) {
	store := gwconfig.NewStore(filepath.Join(t.TempDir(), "config", "gateway.yaml"))
	cfg := gwconfig.DefaultConfig()
	if _, err := store.Save(cfg); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	var gotLocalTokens []string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ilink/bot/get_bot_qrcode":
			var body struct {
				LocalTokenList []string `json:"local_token_list"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode qrcode request: %v", err)
			}
			gotLocalTokens = body.LocalTokenList
			writeJSON(w, http.StatusOK, map[string]any{
				"qrcode":             "qr-token",
				"qrcode_img_content": "https://weixin.example.test/qr-token",
			})
		case "/ilink/bot/get_qrcode_status":
			writeJSON(w, http.StatusOK, map[string]any{
				"status":        "confirmed",
				"bot_token":     "bot-secret",
				"ilink_bot_id":  "bot-1@im.bot",
				"baseurl":       server.URL,
				"ilink_user_id": "user-1@im.wechat",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	setTestWeixinLoginBaseURL(t, store, server.URL)

	router := newTestRouter(RouterOptions{
		GatewaySettings: store,
		GatewayBindingsChanged: func(context.Context, gwconfig.Config) error {
			return nil
		},
	})
	startBody, err := json.Marshal(profileWeixinLoginStartRequest{})
	if err != nil {
		t.Fatalf("marshal start body: %v", err)
	}
	startRec := httptest.NewRecorder()
	startReq := authedHTTPTestRequest(http.MethodPost, "/api/profile/weixin/login/start", strings.NewReader(string(startBody)))
	startReq.Header.Set("X-Agent-Profile-ID", "human-alice")
	startReq.Header.Set("X-User-Display-Name", "Alice")
	router.ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusOK {
		t.Fatalf("start status = %d body=%s", startRec.Code, startRec.Body.String())
	}
	if len(gotLocalTokens) != 0 {
		t.Fatalf("human binding scan should not send existing local tokens: %+v", gotLocalTokens)
	}
	var start gatewayWeixinLoginStartResponseDTO
	if err := json.Unmarshal(startRec.Body.Bytes(), &start); err != nil {
		t.Fatalf("decode start: %v", err)
	}

	statusRec := httptest.NewRecorder()
	statusReq := authedHTTPTestRequest(http.MethodGet, "/api/profile/weixin/login/"+start.SessionKey, nil)
	statusReq.Header.Set("X-Agent-Profile-ID", "human-alice")
	router.ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", statusRec.Code, statusRec.Body.String())
	}
	var status gatewayWeixinLoginStatusResponseDTO
	if err := json.Unmarshal(statusRec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.BoundUserID != "user-1@im.wechat" || status.BoundHumanID != "human-alice" || status.HumanID != "human-alice" {
		t.Fatalf("binding status = %+v", status)
	}

	loaded, err := store.LoadOrDefault()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	bindingKey := gwconfig.WeixinBindingKey("system", "user-1@im.wechat")
	bindings, ok := loaded.Config.Adapters.Weixin.Extra["weixinUserBindings"].(map[string]any)
	if !ok || bindings[bindingKey] != "human-alice" {
		t.Fatalf("weixinUserBindings = %+v", loaded.Config.Adapters.Weixin.Extra["weixinUserBindings"])
	}
	accounts, ok := loaded.Config.Adapters.Weixin.Extra["weixinAccounts"].(map[string]any)
	if !ok {
		t.Fatalf("weixinAccounts missing: %+v", loaded.Config.Adapters.Weixin.Extra)
	}
	account, ok := accounts[bindingKey].(map[string]any)
	if !ok || account["token"] != "bot-secret" || account["botId"] != "bot-1@im.bot" {
		t.Fatalf("weixin account = %+v", accounts[bindingKey])
	}
}

func TestProfileWeixinLoginRoutesRebindKnownAccountWhenAlreadyConnected(t *testing.T) {
	store := gwconfig.NewStore(filepath.Join(t.TempDir(), "config", "gateway.yaml"))
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Weixin.Enabled = true
	cfg.Adapters.Weixin.Extra = map[string]any{
		"loginBaseUrl":  "http://127.0.0.1",
		"agentBindings": map[string]any{},
		"weixinAccounts": map[string]any{
			"user-1@im.wechat": map[string]any{
				"userId":       "user-1@im.wechat",
				"botId":        "bot-1@im.bot",
				"token":        "bot-secret",
				"ilinkBaseUrl": "https://ilink.example.test/ilink/bot",
			},
		},
	}
	if _, err := store.Save(cfg); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ilink/bot/get_bot_qrcode":
			writeJSON(w, http.StatusOK, map[string]any{
				"qrcode":             "qr-token",
				"qrcode_img_content": "https://weixin.example.test/qr-token",
			})
		case "/ilink/bot/get_qrcode_status":
			writeJSON(w, http.StatusOK, map[string]any{
				"status":        "binded_redirect",
				"ilink_user_id": "user-1@im.wechat",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	setTestWeixinLoginBaseURL(t, store, server.URL)

	router := newTestRouter(RouterOptions{
		GatewaySettings: store,
		GatewayBindingsChanged: func(context.Context, gwconfig.Config) error {
			return nil
		},
		AgentProfiles: &fakeProfileWeixinDirectory{entries: []agentcore.AgentDirectoryEntry{
			{AgentProfileID: "human-alice", DisplayName: "Alice", AgentKind: agentcore.AgentKindHuman, Enabled: true, OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "system"}}},
			{AgentProfileID: "agent-after-delete", DisplayName: "售后", AgentKind: agentcore.AgentKindAI, Enabled: true},
		}},
	})
	startBody, err := json.Marshal(profileWeixinLoginStartRequest{
		AgentID: "agent-after-delete",
	})
	if err != nil {
		t.Fatalf("marshal start body: %v", err)
	}
	startRec := httptest.NewRecorder()
	startReq := authedHTTPTestRequest(http.MethodPost, "/api/profile/weixin/login/start", strings.NewReader(string(startBody)))
	startReq.Header.Set("X-Agent-Profile-ID", "human-alice")
	router.ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusOK {
		t.Fatalf("start status = %d body=%s", startRec.Code, startRec.Body.String())
	}
	var start gatewayWeixinLoginStartResponseDTO
	if err := json.Unmarshal(startRec.Body.Bytes(), &start); err != nil {
		t.Fatalf("decode start: %v", err)
	}

	statusRec := httptest.NewRecorder()
	statusReq := authedHTTPTestRequest(http.MethodGet, "/api/profile/weixin/login/"+start.SessionKey, nil)
	statusReq.Header.Set("X-Agent-Profile-ID", "human-alice")
	router.ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", statusRec.Code, statusRec.Body.String())
	}
	var status gatewayWeixinLoginStatusResponseDTO
	if err := json.Unmarshal(statusRec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if !status.Connected || status.BoundUserID != "user-1@im.wechat" || status.AgentID != "agent-after-delete" || status.BoundHumanID != "human-alice" {
		t.Fatalf("status = %+v", status)
	}

	loaded, err := store.LoadOrDefault()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	bindingKey := gwconfig.WeixinBindingKey("system", "user-1@im.wechat")
	bindings, ok := loaded.Config.Adapters.Weixin.Extra["agentBindings"].(map[string]any)
	if !ok || bindings[bindingKey] != "agent-after-delete" {
		t.Fatalf("agentBindings = %+v", loaded.Config.Adapters.Weixin.Extra["agentBindings"])
	}
	humanBindings := loaded.Config.Adapters.Weixin.Extra["weixinUserBindings"].(map[string]any)
	if humanBindings[bindingKey] != "human-alice" {
		t.Fatalf("weixinUserBindings = %+v", humanBindings)
	}
	accounts := loaded.Config.Adapters.Weixin.Extra["weixinAccounts"].(map[string]any)
	account := accounts[bindingKey].(map[string]any)
	if account["token"] != "bot-secret" {
		t.Fatalf("known account token should be preserved: %+v", account)
	}
}

func TestGatewayWeixinKnownAccountRequiresExactIdentifier(t *testing.T) {
	accounts := []gatewayWeixinKnownAccount{{
		UserID: "user-1@im.wechat",
		BotID:  "bot-1@im.bot",
		Token:  "secret",
	}}
	if _, ok := gatewayWeixinKnownAccountForStatus(accounts, gatewayWeixinLoginStatusResponseDTO{}); ok {
		t.Fatal("identifier-free status must not select the only stored account")
	}
}

func TestGatewayWeixinLoginManagerRejectsHostileEndpointsAndVerifyCodes(t *testing.T) {
	manager := newGatewayWeixinLoginManager(false)
	session := &gatewayWeixinLoginSession{SessionKey: "session-1", StartedAt: time.Now().UTC()}
	manager.sessions[session.SessionKey] = session

	if _, err := manager.applyStatus(*session, weixin.IlinkQRCodeStatus{
		Status:   "confirmed",
		BotToken: "secret",
		BaseURL:  "https://attacker.example",
	}); err == nil || !strings.Contains(err.Error(), "invalid Weixin bot endpoint") {
		t.Fatalf("hostile bot endpoint error = %v", err)
	}
	if _, err := manager.applyStatus(*session, weixin.IlinkQRCodeStatus{
		Status:       "scaned_but_redirect",
		RedirectHost: "attacker.example",
	}); err == nil || !strings.Contains(err.Error(), "invalid Weixin login redirect") {
		t.Fatalf("hostile redirect error = %v", err)
	}
	for _, invalid := range []string{"", "123", "123456789", "12ab56", "１２３４"} {
		if err := manager.setVerifyCode(session.SessionKey, invalid); err == nil {
			t.Fatalf("invalid verification code %q was accepted", invalid)
		}
	}
	if err := manager.setVerifyCode(session.SessionKey, "123456"); err != nil {
		t.Fatalf("valid verification code rejected: %v", err)
	}
}

func TestGatewayWeixinLoginManagerScopesSessionsToHumanAndTenant(t *testing.T) {
	manager := newGatewayWeixinLoginManager(true)
	first := &gatewayWeixinLoginSession{SessionKey: "first", BindingHumanID: "human-1", BindingTenantID: "tenant-1", StartedAt: time.Now().UTC()}
	second := &gatewayWeixinLoginSession{SessionKey: "second", BindingHumanID: "human-1", BindingTenantID: "tenant-1", StartedAt: time.Now().UTC()}
	if err := manager.registerSession(first); err != nil {
		t.Fatalf("register first personal session: %v", err)
	}
	if err := manager.registerSession(second); err != nil {
		t.Fatalf("replace personal session: %v", err)
	}
	if _, exists := manager.sessions[first.SessionKey]; exists || manager.sessions[second.SessionKey] == nil {
		t.Fatalf("personal sessions were not replaced: %+v", manager.sessions)
	}
	if err := manager.registerSession(&gatewayWeixinLoginSession{SessionKey: "unscoped", StartedAt: time.Now().UTC()}); err == nil || !strings.Contains(err.Error(), "signed-in user and tenant") {
		t.Fatalf("unscoped session error = %v", err)
	}
	if _, err := manager.reserveSessionCapacity("", ""); err == nil || !strings.Contains(err.Error(), "signed-in user and tenant") {
		t.Fatalf("unscoped capacity error = %v", err)
	}
}

func TestGatewaySettingsRoutesReportReloadFailure(t *testing.T) {
	store := gwconfig.NewStore(filepath.Join(t.TempDir(), "config", "gateway.yaml"))
	router := newTestRouter(RouterOptions{
		GatewaySettings: store,
		GatewaySettingsChanged: func(ctx context.Context, cfg gwconfig.Config) error {
			return errors.New("port already in use")
		},
	})

	body := `{"config":{"gateway":{"port":18790,"bind_address":"127.0.0.1","idle_session_timeout_minutes":45,"max_per_session_mcp_instances":3},"adapters":{"feishu":{"default_session_label":"general","connection_mode":"stream","domain_name":"feishu"},"email":{"enabled":true,"token":"new-token","extra":{}}}}}`
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, authedHTTPTestRequest(http.MethodPut, "/api/settings/gateway", strings.NewReader(body)))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "gateway_settings_reload_failed") || !strings.Contains(rec.Body.String(), "port already in use") {
		t.Fatalf("unexpected reload error body: %s", rec.Body.String())
	}
	loaded, err := store.LoadOrDefault()
	if err != nil {
		t.Fatalf("load rolled back config: %v", err)
	}
	if loaded.Config.Gateway.Port != gwconfig.DefaultConfig().Gateway.Port || loaded.Config.Adapters.Email.Enabled {
		t.Fatalf("failed reload persisted new config: %+v", loaded.Config)
	}
}

func TestGatewaySettingsWriteFailsWhenRuntimeReloadIsUnavailable(t *testing.T) {
	store := gwconfig.NewStore(filepath.Join(t.TempDir(), "config", "gateway.yaml"))
	if _, err := store.Save(gwconfig.DefaultConfig()); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	router := newTestRouter(RouterOptions{GatewaySettings: store})
	cfg := gwconfig.DefaultConfig()
	cfg.Gateway.Port = 18791
	payload, err := json.Marshal(gatewaySettingsUpdateRequest{Config: cfg})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, authedHTTPTestRequest(http.MethodPut, "/api/settings/gateway", strings.NewReader(string(payload))))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "gateway_settings_reload_failed") {
		t.Fatalf("PUT status = %d body=%s", rec.Code, rec.Body.String())
	}
	loaded, err := store.LoadOrDefault()
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	if loaded.Config.Gateway.Port != gwconfig.DefaultConfig().Gateway.Port {
		t.Fatalf("unapplied config was persisted: %+v", loaded.Config.Gateway)
	}
}

func TestGatewaySettingsRawYamlRejectsInvalidConfig(t *testing.T) {
	store := gwconfig.NewStore(filepath.Join(t.TempDir(), "config", "gateway.yaml"))
	if _, err := store.Save(gwconfig.DefaultConfig()); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	router := newTestRouter(RouterOptions{GatewaySettings: store})

	for _, body := range []string{
		`{"raw_yaml":"gateway:\n  bindAddress: 0.0.0.0\n"}`,
		`{"raw_yaml":"gateway:\n  bindAddress: 127.0.0.1\n  typoField: true\n"}`,
		`{"raw_yaml":"gateway:\n  bindAddress: 127.0.0.1\n---\nadapters: {}\n"}`,
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, authedHTTPTestRequest(http.MethodPut, "/api/settings/gateway/raw", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("raw PUT status = %d body=%s", rec.Code, rec.Body.String())
		}
	}
	loaded, err := store.LoadOrDefault()
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if loaded.Config.Gateway.BindAddress != "127.0.0.1" {
		t.Fatalf("invalid raw yaml should not overwrite prior config: %+v", loaded.Config.Gateway)
	}
}

func setTestWeixinLoginBaseURL(t *testing.T, store *gwconfig.Store, baseURL string) {
	t.Helper()
	record, err := store.LoadOrDefault()
	if err != nil {
		t.Fatalf("load weixin config: %v", err)
	}
	cfg := record.Config
	cfg.Adapters.Weixin.Enabled = true
	if cfg.Adapters.Weixin.Extra == nil {
		cfg.Adapters.Weixin.Extra = map[string]any{}
	}
	cfg.Adapters.Weixin.Extra["loginBaseUrl"] = baseURL
	if _, err := store.Save(cfg); err != nil {
		t.Fatalf("save weixin login url: %v", err)
	}
}

type fakeProfileWeixinDirectory struct {
	entries   []agentcore.AgentDirectoryEntry
	lastQuery agentcore.AgentDirectoryQuery
}

func (d *fakeProfileWeixinDirectory) QueryDirectory(ctx context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	d.lastQuery = q
	entries := make([]agentcore.AgentDirectoryEntry, 0, len(d.entries))
	for _, entry := range d.entries {
		if q.EnabledOnly && !entry.Enabled {
			continue
		}
		if q.IncludeAI && !q.IncludeHumans && entry.AgentKind != agentcore.AgentKindAI {
			continue
		}
		entries = append(entries, entry)
	}
	return agentcore.AgentDirectoryPage{Entries: entries, Total: len(entries)}, nil
}

func (d *fakeProfileWeixinDirectory) GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	for _, entry := range d.entries {
		if entry.AgentProfileID == id {
			return agentcore.AgentProfile{
				ID:          entry.AgentProfileID,
				DisplayName: entry.DisplayName,
				RealName:    entry.RealName,
				RoleName:    entry.RoleName,
				AgentKind:   entry.AgentKind,
				OrgIdentity: entry.OrgIdentity,
				Enabled:     entry.Enabled,
			}, nil
		}
	}
	return agentcore.AgentProfile{}, agentcore.ErrNotFound
}
