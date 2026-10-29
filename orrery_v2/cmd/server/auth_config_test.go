package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/platform/config"
)

func TestIdentityAuthConfigDefaultsLocalDisabledWhenKratosConfigured(t *testing.T) {
	cfg := loadIdentityAuthTestConfig(t, map[string]string{
		"auth.kratos.public_url":   "https://kratos.example",
		"auth.kratos.callback_url": "https://app.example/api/auth/identity/kratos/callback",
		"auth.kratos.display_name": "Company SSO",
	})

	authCfg, err := identityAuthConfigFromPlatform(cfg)
	if err != nil {
		t.Fatalf("identityAuthConfigFromPlatform: %v", err)
	}
	if authCfg.LocalEnabled {
		t.Fatalf("local login should default disabled when Kratos is configured")
	}
	if authCfg.Kratos == nil || authCfg.Kratos.DisplayName() != "Company SSO" {
		t.Fatalf("kratos provider = %+v", authCfg.Kratos)
	}
}

func TestIdentityAuthConfigAllowsExplicitLocalBreakGlass(t *testing.T) {
	cfg := loadIdentityAuthTestConfig(t, map[string]string{
		"auth.local.enabled":       "true",
		"auth.kratos.public_url":   "https://kratos.example",
		"auth.kratos.callback_url": "https://app.example/api/auth/identity/kratos/callback",
	})

	authCfg, err := identityAuthConfigFromPlatform(cfg)
	if err != nil {
		t.Fatalf("identityAuthConfigFromPlatform: %v", err)
	}
	if !authCfg.LocalEnabled || authCfg.Kratos == nil {
		t.Fatalf("auth config = %+v", authCfg)
	}
}

func TestIdentityAuthConfigDefaultsSuperadminMFADisabled(t *testing.T) {
	cfg := loadIdentityAuthTestConfig(t, map[string]string{
		"auth.local.enabled":                "true",
		"ORRERY_AUTH_LOCAL_MFA_TOTP_SECRET": "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP",
	})
	cfg.Environment = "production"

	authCfg, err := identityAuthConfigFromPlatform(cfg)
	if err != nil {
		t.Fatalf("identityAuthConfigFromPlatform: %v", err)
	}
	if authCfg.LocalSuperadminMFAEnabled {
		t.Fatal("superadmin MFA should default disabled")
	}
}

func TestIdentityAuthConfigRequiresSecretWhenSuperadminMFAEnabled(t *testing.T) {
	cfg := loadIdentityAuthTestConfig(t, map[string]string{
		"auth.local.superadmin_mfa_enabled": "true",
	})

	_, err := identityAuthConfigFromPlatform(cfg)
	if err == nil || !strings.Contains(err.Error(), "mfa_totp_secret is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestIdentityAuthConfigEnablesSuperadminMFAExplicitly(t *testing.T) {
	cfg := loadIdentityAuthTestConfig(t, map[string]string{
		"auth.local.superadmin_mfa_enabled": "true",
		"ORRERY_AUTH_LOCAL_MFA_TOTP_SECRET": "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP",
	})

	authCfg, err := identityAuthConfigFromPlatform(cfg)
	if err != nil {
		t.Fatalf("identityAuthConfigFromPlatform: %v", err)
	}
	if !authCfg.LocalSuperadminMFAEnabled {
		t.Fatal("superadmin MFA should honor explicit opt-in")
	}
}

func TestIdentityAuthConfigRequiresExplicitSubjectAutoLinkOptIn(t *testing.T) {
	defaultCfg, err := identityAuthConfigFromPlatform(loadIdentityAuthTestConfig(t, nil))
	if err != nil {
		t.Fatalf("identityAuthConfigFromPlatform default: %v", err)
	}
	if defaultCfg.AutoLinkEnabled {
		t.Fatalf("identity subject auto-link should default disabled")
	}

	enabledCfg, err := identityAuthConfigFromPlatform(loadIdentityAuthTestConfig(t, map[string]string{
		"auth.identity.auto_link_enabled": "true",
	}))
	if err != nil {
		t.Fatalf("identityAuthConfigFromPlatform enabled: %v", err)
	}
	if !enabledCfg.AutoLinkEnabled {
		t.Fatalf("identity subject auto-link should honor explicit opt-in")
	}
}

func TestIdentityAuthConfigRequiresKratosCallback(t *testing.T) {
	cfg := loadIdentityAuthTestConfig(t, map[string]string{
		"auth.kratos.public_url": "https://kratos.example",
	})

	_, err := identityAuthConfigFromPlatform(cfg)
	if err == nil || !strings.Contains(err.Error(), "auth.kratos.callback_url is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestIdentityAuthConfigEnablesFeishuLoginAndOptionalProvisioning(t *testing.T) {
	cfg := loadIdentityAuthTestConfig(t, map[string]string{
		"auth.feishu.app_id":                 "cli_test",
		"ORRERY_FEISHU_APP_SECRET":           "secret",
		"auth.feishu.callback_url":           "https://app.example/api/auth/identity/feishu/callback",
		"auth.feishu.binding_callback_url":   "https://app.example/api/profile/identity/feishu/callback",
		"auth.feishu.allowed_tenant_key":     "tenant-1",
		"auth.feishu.auto_provision_enabled": "true",
	})

	authCfg, err := identityAuthConfigFromPlatform(cfg)
	if err != nil {
		t.Fatalf("identityAuthConfigFromPlatform: %v", err)
	}
	if authCfg.Feishu == nil || authCfg.Feishu.Name() != identity.ProviderFeishu || !authCfg.FeishuAutoProvision {
		t.Fatalf("feishu config = %+v", authCfg)
	}
}

func TestIdentityAuthConfigRequiresCompleteFeishuCredentials(t *testing.T) {
	cfg := loadIdentityAuthTestConfig(t, map[string]string{"auth.feishu.app_id": "cli_test"})
	if _, err := identityAuthConfigFromPlatform(cfg); err == nil || !strings.Contains(err.Error(), "app_secret") {
		t.Fatalf("error = %v", err)
	}
}

func TestIdentityAuthConfigRejectsFeishuProvisioningWithoutCredentials(t *testing.T) {
	cfg := loadIdentityAuthTestConfig(t, map[string]string{"auth.feishu.auto_provision_enabled": "true"})
	if _, err := identityAuthConfigFromPlatform(cfg); err == nil || !strings.Contains(err.Error(), "app_id") {
		t.Fatalf("error = %v", err)
	}
}

func TestIdentityAuthConfigPeopleSSORequiresKratos(t *testing.T) {
	cfg := loadIdentityAuthTestConfig(t, map[string]string{
		"auth.identity.mode": "people_sso",
	})

	_, err := identityAuthConfigFromPlatform(cfg)
	if err == nil || !strings.Contains(err.Error(), "auth.kratos.public_url") {
		t.Fatalf("error = %v, want missing Kratos configuration", err)
	}
}

func TestIdentityAuthConfigPeopleSSORequiresKratosAdminProjection(t *testing.T) {
	cfg := loadIdentityAuthTestConfig(t, map[string]string{
		"auth.identity.mode":       "people_sso",
		"auth.kratos.public_url":   "https://kratos.example",
		"auth.kratos.callback_url": "https://app.example/api/auth/identity/kratos/callback",
	})

	_, err := identityAuthConfigFromPlatform(cfg)
	if err == nil || !strings.Contains(err.Error(), "auth.kratos.admin_url") {
		t.Fatalf("error = %v, want missing Kratos Admin configuration", err)
	}
}

func TestIdentityAuthConfigPeopleSSOExcludesDirectFeishuProvider(t *testing.T) {
	cfg := loadIdentityAuthTestConfig(t, map[string]string{
		"auth.identity.mode":                 "people_sso",
		"auth.kratos.public_url":             "https://kratos.example",
		"auth.kratos.callback_url":           "https://app.example/api/auth/identity/kratos/callback",
		"auth.kratos.admin_url":              "https://kratos-admin.example",
		"ORRERY_KRATOS_ADMIN_TOKEN":          "admin-token",
		"auth.feishu.app_id":                 "cli_test",
		"ORRERY_FEISHU_APP_SECRET":           "secret",
		"auth.feishu.callback_url":           "https://app.example/api/auth/identity/feishu/callback",
		"auth.feishu.binding_callback_url":   "https://app.example/api/profile/identity/feishu/callback",
		"auth.feishu.auto_provision_enabled": "true",
	})

	authCfg, err := identityAuthConfigFromPlatform(cfg)
	if err != nil {
		t.Fatalf("identityAuthConfigFromPlatform: %v", err)
	}
	if authCfg.Kratos == nil || authCfg.Feishu != nil || authCfg.FeishuAutoProvision {
		t.Fatalf("people SSO providers = kratos:%v feishu:%v auto_provision:%v", authCfg.Kratos, authCfg.Feishu, authCfg.FeishuAutoProvision)
	}
}

func TestIdentityAuthConfigPeopleSSORejectsArbitrarySubjectAutoLink(t *testing.T) {
	cfg := loadIdentityAuthTestConfig(t, map[string]string{
		"auth.identity.mode":              "people_sso",
		"auth.identity.auto_link_enabled": "true",
		"auth.kratos.public_url":          "https://kratos.example",
		"auth.kratos.callback_url":        "https://app.example/api/auth/identity/kratos/callback",
	})

	_, err := identityAuthConfigFromPlatform(cfg)
	if err == nil || !strings.Contains(err.Error(), "auto_link_enabled") {
		t.Fatalf("error = %v, want auto-link rejection", err)
	}
}

func TestIdentityAuthConfigEnablesOIDCTokenProviderWithoutKratos(t *testing.T) {
	cfg := loadIdentityAuthTestConfig(t, map[string]string{
		"auth.oidc.introspection_url": "https://hydra.example/admin/oauth2/introspect",
		"auth.oidc.client_id":         "orrery-api",
		"ORRERY_OIDC_CLIENT_SECRET":   "secret",
		"auth.oidc.issuer":            "https://hydra.example",
		"auth.oidc.audience":          "orrery-api",
	})

	authCfg, err := identityAuthConfigFromPlatform(cfg)
	if err != nil {
		t.Fatalf("identityAuthConfigFromPlatform: %v", err)
	}
	if authCfg.OIDC == nil || authCfg.OIDC.Name() != "hydra" {
		t.Fatalf("oidc provider = %+v", authCfg.OIDC)
	}
	if authCfg.LocalEnabled || authCfg.Kratos != nil {
		t.Fatalf("auth config = %+v", authCfg)
	}
}

func TestIdentityAuthConfigRequiresOIDCIssuerAndAudience(t *testing.T) {
	cfg := loadIdentityAuthTestConfig(t, map[string]string{
		"auth.oidc.introspection_url": "https://hydra.example/admin/oauth2/introspect",
		"auth.oidc.audience":          "orrery-api",
	})
	if _, err := identityAuthConfigFromPlatform(cfg); err == nil || !strings.Contains(err.Error(), "auth.oidc.issuer") {
		t.Fatalf("missing issuer error = %v", err)
	}

	cfg = loadIdentityAuthTestConfig(t, map[string]string{
		"auth.oidc.introspection_url": "https://hydra.example/admin/oauth2/introspect",
		"auth.oidc.issuer":            "https://hydra.example",
	})
	if _, err := identityAuthConfigFromPlatform(cfg); err == nil || !strings.Contains(err.Error(), "auth.oidc.audience") {
		t.Fatalf("missing audience error = %v", err)
	}
}

func TestIdentityAuthConfigEnablesHydraLoginConsent(t *testing.T) {
	cfg := loadIdentityAuthTestConfig(t, map[string]string{
		"auth.hydra.admin_url":     "https://hydra.example",
		"ORRERY_HYDRA_ADMIN_TOKEN": "secret",
		"auth.hydra.remember_for":  "2h",
	})

	authCfg, err := identityAuthConfigFromPlatform(cfg)
	if err != nil {
		t.Fatalf("identityAuthConfigFromPlatform: %v", err)
	}
	if authCfg.HydraAdmin == nil || authCfg.OIDCLoginConsent == nil {
		t.Fatalf("Hydra admin and OIDC login consent should be configured")
	}
	if authCfg.OIDCClients == nil {
		t.Fatalf("OIDC client registry should be configured")
	}
}

func TestIdentityAuthConfigRejectsInvalidHydraRememberFor(t *testing.T) {
	cfg := loadIdentityAuthTestConfig(t, map[string]string{
		"auth.hydra.admin_url":    "https://hydra.example",
		"auth.hydra.remember_for": "forever",
	})

	_, err := identityAuthConfigFromPlatform(cfg)
	if err == nil || !strings.Contains(err.Error(), "auth.hydra.remember_for is invalid") {
		t.Fatalf("error = %v", err)
	}
}

func TestIdentityAuthConfigEnablesHydraClientSync(t *testing.T) {
	var synced bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/admin/clients/partner-app":
			http.NotFound(w, req)
		case req.Method == http.MethodPost && req.URL.Path == "/admin/clients":
			synced = true
			var body identity.HydraOAuth2Client
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode hydra client: %v", err)
			}
			if body.ClientID != "partner-app" || strings.Join(body.RedirectURIs, ",") != "https://partner.example/callback" {
				t.Fatalf("hydra client body = %+v", body)
			}
			body.ClientSecret = "generated-secret"
			_ = json.NewEncoder(w).Encode(body)
		default:
			t.Fatalf("unexpected %s %s", req.Method, req.URL.Path)
		}
	}))
	defer server.Close()
	cfg := loadIdentityAuthTestConfig(t, map[string]string{
		"auth.hydra.admin_url":           server.URL,
		"auth.hydra.client_sync_enabled": "true",
	})

	authCfg, err := identityAuthConfigFromPlatform(cfg)
	if err != nil {
		t.Fatalf("identityAuthConfigFromPlatform: %v", err)
	}
	client, err := authCfg.OIDCClientAdmin.SaveClient(context.Background(), identity.OIDCClient{
		ClientID:      "partner-app",
		DisplayName:   "Partner App",
		Kind:          "external_integration",
		AllowedScopes: []string{"openid"},
		RedirectURIs:  []string{"https://partner.example/callback"},
	})
	if err != nil {
		t.Fatalf("SaveClient: %v", err)
	}
	if !synced || client.ClientSecret != "generated-secret" {
		t.Fatalf("synced=%v client=%+v", synced, client)
	}
}

func loadIdentityAuthTestConfig(t *testing.T, env map[string]string) config.Config {
	t.Helper()
	return loadTestConfig(t, env)
}
