package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/gateway/channel/feishu"
	gwconfig "orrery_v2/internal/gateway/config"
)

type fakeFeishuRegistrationClient struct {
	release chan struct{}
	result  feishu.RegistrationResult
}

type cancelIgnoringFeishuRegistrationClient struct {
	release chan struct{}
}

type delayedFeishuQRCodeClient struct {
	started chan struct{}
}

type feishuRegistrationAuditRecorder struct {
	events chan operations.AuditEventDTO
}

func (r feishuRegistrationAuditRecorder) RecordAuditEvent(_ context.Context, event operations.AuditEventDTO) error {
	r.events <- event
	return nil
}

func (f *delayedFeishuQRCodeClient) Register(
	ctx context.Context,
	_ func(feishu.RegistrationQRCode),
	_ func(feishu.RegistrationStatus),
) (feishu.RegistrationResult, error) {
	close(f.started)
	<-ctx.Done()
	return feishu.RegistrationResult{}, ctx.Err()
}

func (f *cancelIgnoringFeishuRegistrationClient) Register(
	_ context.Context,
	onQRCode func(feishu.RegistrationQRCode),
	_ func(feishu.RegistrationStatus),
) (feishu.RegistrationResult, error) {
	onQRCode(feishu.RegistrationQRCode{URL: "https://accounts.feishu.cn/activate?ticket=shutdown", ExpireIn: 600})
	<-f.release
	return feishu.RegistrationResult{AppID: "cli-shutdown", AppSecret: "secret-shutdown"}, nil
}

func TestFeishuRegistrationMapsLarkTenantBrandsToLarkDomain(t *testing.T) {
	for _, brand := range []string{"lark", "LARKSUITE"} {
		if domain := feishuDomainForTenantBrand(brand); domain != "lark" {
			t.Fatalf("tenant brand %q mapped to %q", brand, domain)
		}
	}
	if domain := feishuDomainForTenantBrand("feishu"); domain != "feishu" {
		t.Fatalf("Feishu tenant mapped to %q", domain)
	}
}

func (f *fakeFeishuRegistrationClient) Register(
	ctx context.Context,
	onQRCode func(feishu.RegistrationQRCode),
	onStatus func(feishu.RegistrationStatus),
) (feishu.RegistrationResult, error) {
	onQRCode(feishu.RegistrationQRCode{URL: "https://accounts.feishu.cn/activate?ticket=test", ExpireIn: 600})
	onStatus(feishu.RegistrationStatus{Status: "polling"})
	select {
	case <-ctx.Done():
		return feishu.RegistrationResult{}, ctx.Err()
	case <-f.release:
		return f.result, nil
	}
}

func TestFeishuRegistrationManagerScopesSessionsByTenantAndOwner(t *testing.T) {
	client := &fakeFeishuRegistrationClient{
		release: make(chan struct{}),
		result:  feishu.RegistrationResult{AppID: "cli-test", AppSecret: "secret-test"},
	}
	manager := newFeishuRegistrationManager(client)
	dto, err := manager.Start(context.Background(), "tenant-a", "admin-a", "main", func(context.Context, feishu.RegistrationResult) error { return nil })
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if dto.Status != "starting" || dto.RegistrationID == "" {
		t.Fatalf("registration = %+v", dto)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		dto, err = manager.Get("tenant-a", "admin-a", dto.RegistrationID)
		if err == nil && dto.Status == "pending" && dto.VerificationURL != "" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if dto.Status != "pending" || dto.VerificationURL == "" {
		t.Fatalf("registration did not produce a QR code: %+v", dto)
	}
	if _, err := manager.Get("tenant-b", "admin-a", dto.RegistrationID); err == nil {
		t.Fatal("cross-tenant registration read succeeded")
	}
	if _, err := manager.Get("tenant-a", "admin-b", dto.RegistrationID); err == nil {
		t.Fatal("cross-owner registration read succeeded")
	}
	if _, err := manager.Start(context.Background(), "tenant-a", "admin-a", "secondary", nil); err != errFeishuRegistrationBusy {
		t.Fatalf("concurrent owner registration error = %v", err)
	}
	close(client.release)
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current, err := manager.Get("tenant-a", "admin-a", dto.RegistrationID)
		if err == nil && current.Status == "connected" {
			if current.AccountID != "main" {
				t.Fatalf("account id = %q", current.AccountID)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("registration did not complete")
}

func TestFeishuRegistrationStartReturnsBeforeQRCodeGeneration(t *testing.T) {
	client := &delayedFeishuQRCodeClient{started: make(chan struct{})}
	manager := newFeishuRegistrationManager(client)
	dto, err := manager.Start(context.Background(), "tenant-a", "admin-a", "main", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if dto.Status != "starting" || dto.RegistrationID == "" {
		t.Fatalf("registration = %+v", dto)
	}
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("registration worker did not start")
	}
	if _, err := manager.Cancel("tenant-a", "admin-a", dto.RegistrationID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
}

func TestFeishuRegistrationRoutesPersistEncryptedCredentials(t *testing.T) {
	client := &fakeFeishuRegistrationClient{
		release: make(chan struct{}),
		result:  feishu.RegistrationResult{AppID: "cli-route", AppSecret: "route-secret", TenantBrand: "feishu"},
	}
	store := gwconfig.NewStore(filepath.Join(t.TempDir(), "gateway.yaml"))
	audit := feishuRegistrationAuditRecorder{events: make(chan operations.AuditEventDTO, 1)}
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Feishu.Accounts = map[string]gwconfig.FeishuAccountConfig{
		"main": {
			Enabled: true, AppID: "cli-main", AppSecret: "existing-secret",
			EncryptKey: "existing-encrypt", VerifyToken: "existing-verify",
			DefaultSessionLabel: "general", ConnectionMode: feishu.ModeStream, DomainName: "feishu",
		},
	}
	if _, err := store.Save(cfg); err != nil {
		t.Fatalf("seed gateway settings: %v", err)
	}
	reloaded := make(chan gwconfig.Config, 1)
	router := newTestRouter(RouterOptions{
		GatewaySettings:          store,
		FeishuRegistrationClient: client,
		AuditRecorder:            audit,
		GatewaySettingsChanged: func(_ context.Context, cfg gwconfig.Config) error {
			reloaded <- cfg
			return nil
		},
	})
	req := authedHTTPTestRequest(http.MethodPost, "/api/settings/gateway/feishu/registrations", strings.NewReader(`{"account_id":"jordan-belfort"}`))
	req.Header.Set("X-Tenant-ID", "tenant-a")
	req.Header.Set("X-User-ID", "admin-a")
	req = req.WithContext(identity.ContextWithPrincipal(req.Context(), identity.Principal{
		SubjectProvider: "local", Subject: "admin-a", UserID: "admin-a", ViewerKind: identity.ViewerKindUser, TenantID: "tenant-a", CoarseScopes: []string{"settings.manage"},
	}))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST status = %d body=%s", rec.Code, rec.Body.String())
	}
	var started feishuRegistrationDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode registration: %v", err)
	}
	if started.AccountID != "jordan-belfort" || strings.Contains(rec.Body.String(), "route-secret") {
		t.Fatalf("registration response = %s", rec.Body.String())
	}
	close(client.release)
	select {
	case cfg := <-reloaded:
		account := cfg.Adapters.Feishu.Accounts["jordan-belfort"]
		main := cfg.Adapters.Feishu.Accounts["main"]
		if account.AppID != "cli-route" || account.AppSecret != "route-secret" || !account.Enabled ||
			cfg.Adapters.Feishu.AppID != "" || cfg.Adapters.Feishu.AppSecret != "" || !cfg.Adapters.Feishu.Enabled ||
			main.AppSecret != "existing-secret" || main.EncryptKey != "existing-encrypt" || main.VerifyToken != "existing-verify" {
			t.Fatalf("reloaded config = %+v", cfg.Adapters.Feishu)
		}
	case <-time.After(time.Second):
		t.Fatal("registered credentials were not applied")
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		statusReq := authedHTTPTestRequest(http.MethodGet, "/api/settings/gateway/feishu/registrations/"+started.RegistrationID, nil)
		statusReq.Header.Set("X-Tenant-ID", "tenant-a")
		statusReq.Header.Set("X-User-ID", "admin-a")
		statusReq = statusReq.WithContext(identity.ContextWithPrincipal(statusReq.Context(), identity.Principal{
			SubjectProvider: "local", Subject: "admin-a", UserID: "admin-a", ViewerKind: identity.ViewerKindUser, TenantID: "tenant-a", CoarseScopes: []string{"settings.manage"},
		}))
		statusRec := httptest.NewRecorder()
		router.ServeHTTP(statusRec, statusReq)
		if statusRec.Code == http.StatusOK && strings.Contains(statusRec.Body.String(), `"status":"connected"`) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	record, err := store.LoadOrDefault()
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	if record.Config.Adapters.Feishu.Accounts["jordan-belfort"].AppSecret != "route-secret" || record.Config.Adapters.Feishu.Accounts["main"].AppSecret != "existing-secret" {
		t.Fatalf("stored decrypted accounts = %+v", record.Config.Adapters.Feishu.Accounts)
	}
	if strings.Contains(record.RawYAML, "route-secret") || strings.Contains(record.RawYAML, "existing-secret") {
		t.Fatalf("stored YAML leaked secret: %s", record.RawYAML)
	}
	select {
	case event := <-audit.events:
		if event.Action != "gateway.feishu_registration" || event.Result != "succeeded" ||
			event.Metadata["tenant_id"] != "tenant-a" || event.Metadata["actor_id"] != "admin-a" ||
			event.Metadata["account_id"] != "jordan-belfort" || strings.Contains(event.Summary, "secret") {
			t.Fatalf("registration audit = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("registration completion audit was not recorded")
	}
}

func TestFeishuRegistrationRoutesRejectInvalidAccountRequests(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"account_id":"bad:alias"}`,
		`{"account_id":"bad/alias"}`,
		`{"account_id":"main","app_secret":"secret"}`,
	} {
		t.Run(body, func(t *testing.T) {
			lifecycle, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			client := &delayedFeishuQRCodeClient{started: make(chan struct{})}
			router := newTestRouter(RouterOptions{
				GatewaySettings:          gwconfig.NewStore(filepath.Join(t.TempDir(), "gateway.yaml")),
				FeishuRegistrationClient: client,
				LifecycleContext:         lifecycle,
			})
			req := authedHTTPTestRequest(http.MethodPost, "/api/settings/gateway/feishu/registrations", strings.NewReader(body))
			req = req.WithContext(identity.ContextWithPrincipal(req.Context(), identity.Principal{
				SubjectProvider: "local", Subject: "admin-a", UserID: "admin-a", ViewerKind: identity.ViewerKindUser,
				TenantID: "tenant-a", CoarseScopes: []string{"settings.manage"},
			}))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest || strings.Contains(rec.Body.String(), "secret") {
				t.Fatalf("POST status = %d body=%s", rec.Code, rec.Body.String())
			}
			select {
			case <-client.started:
				t.Fatal("invalid request started registration")
			default:
			}
		})
	}
}

func TestGatewayRegistrationActorResolvesTenantFromAuthenticatedProfile(t *testing.T) {
	router := &Router{agentProfiles: &fakeProfileWeixinDirectory{entries: []agentcore.AgentDirectoryEntry{{
		AgentProfileID: "human-admin",
		AgentKind:      agentcore.AgentKindHuman,
		Enabled:        true,
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef: agentcore.OrganizationRef{ID: "org-a"},
		},
	}}}}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/gateway/feishu/registrations", nil)
	req = req.WithContext(identity.ContextWithPrincipal(req.Context(), identity.Principal{
		SubjectProvider: "local",
		Subject:         "admin-a",
		UserID:          "admin-a",
		AgentProfileID:  "human-admin",
		ViewerKind:      identity.ViewerKindUser,
	}))
	rec := httptest.NewRecorder()

	tenantID, ownerID, ok := router.gatewayRegistrationActor(rec, req)
	if !ok || tenantID != "org-a" || ownerID != "admin-a" {
		t.Fatalf("actor = tenant %q owner %q ok %v; response=%d %s", tenantID, ownerID, ok, rec.Code, rec.Body.String())
	}
}

func TestGatewayRegistrationActorRejectsProfileTenantMismatch(t *testing.T) {
	router := &Router{agentProfiles: &fakeProfileWeixinDirectory{entries: []agentcore.AgentDirectoryEntry{{
		AgentProfileID: "human-admin",
		AgentKind:      agentcore.AgentKindHuman,
		Enabled:        true,
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef: agentcore.OrganizationRef{ID: "org-profile"},
		},
	}}}}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/gateway/feishu/registrations", nil)
	req = req.WithContext(identity.ContextWithPrincipal(req.Context(), identity.Principal{
		SubjectProvider: "oidc",
		Subject:         "admin-a",
		UserID:          "admin-a",
		AgentProfileID:  "human-admin",
		ViewerKind:      identity.ViewerKindUser,
		TenantID:        "org-token",
	}))
	rec := httptest.NewRecorder()

	if _, _, ok := router.gatewayRegistrationActor(rec, req); ok {
		t.Fatal("mismatched authenticated and profile tenants were accepted")
	}
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"feishu_registration_tenant_mismatch"`) {
		t.Fatalf("response=%d %s", rec.Code, rec.Body.String())
	}
}

func TestFeishuRegistrationLifecycleCancellationPreventsCredentialApply(t *testing.T) {
	lifecycle, shutdown := context.WithCancel(context.Background())
	client := &cancelIgnoringFeishuRegistrationClient{release: make(chan struct{})}
	manager := newFeishuRegistrationManager(client, lifecycle)
	applied := make(chan struct{}, 1)
	dto, err := manager.Start(context.Background(), "tenant-a", "admin-a", "main", func(context.Context, feishu.RegistrationResult) error {
		applied <- struct{}{}
		return nil
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	shutdown()
	close(client.release)

	deadline := time.Now().Add(time.Second)
	expired := false
	for time.Now().Before(deadline) {
		current, getErr := manager.Get("tenant-a", "admin-a", dto.RegistrationID)
		if getErr == nil && current.Status == "expired" {
			expired = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !expired {
		t.Fatal("registration did not expire after service shutdown")
	}
	select {
	case <-applied:
		t.Fatal("registration applied credentials after service shutdown")
	default:
	}
	if _, err := manager.Start(context.Background(), "tenant-a", "admin-b", "main", nil); err == nil || !strings.Contains(err.Error(), "shutting down") {
		t.Fatalf("registration after shutdown error = %v", err)
	}
}
