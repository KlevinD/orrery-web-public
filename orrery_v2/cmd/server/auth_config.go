package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/peopleaccount"
	"orrery_v2/internal/platform/config"
)

type identityAuthConfig struct {
	IdentityMode              string
	LocalEnabled              bool
	LocalSuperadminMFAEnabled bool
	LocalMFATOTPSecret        string
	AutoLinkEnabled           bool
	Kratos                    *identity.KratosIdentityProvider
	KratosAdmin               *peopleaccount.KratosAdminClient
	Feishu                    *identity.FeishuIdentityProvider
	FeishuAutoProvision       bool
	OIDC                      *identity.OIDCTokenProvider
	HydraAdmin                *identity.HydraAdminClient
	OIDCClients               *identity.OIDCClientRegistry
	OIDCLoginConsent          *identity.OIDCLoginConsentService
	OIDCClientAdmin           *identity.OIDCClientAdminService
}

const (
	identityModeStandalone = "standalone"
	identityModePeopleSSO  = "people_sso"
)

func identityAuthConfigFromPlatform(cfg config.Config) (identityAuthConfig, error) {
	identityMode := identityModeStandalone
	if value, ok := cfg.String("auth.identity.mode"); ok && strings.TrimSpace(value) != "" {
		identityMode = strings.ToLower(strings.TrimSpace(value))
	}
	if identityMode != identityModeStandalone && identityMode != identityModePeopleSSO {
		return identityAuthConfig{}, fmt.Errorf("auth.identity.mode must be %q or %q", identityModeStandalone, identityModePeopleSSO)
	}
	kratosPublicURL, _ := cfg.String("auth.kratos.public_url")
	kratosPublicURL = strings.TrimSpace(kratosPublicURL)
	localEnabled := false
	if value, ok := cfg.Bool("auth.local.enabled"); ok {
		localEnabled = value
	}
	autoLinkEnabled := false
	if value, ok := cfg.Bool("auth.identity.auto_link_enabled"); ok {
		autoLinkEnabled = value
	}
	if identityMode == identityModePeopleSSO && autoLinkEnabled {
		return identityAuthConfig{}, fmt.Errorf("auth.identity.auto_link_enabled cannot be enabled in people_sso mode")
	}
	clients := identity.NewDefaultOIDCClientRegistry()
	localSuperadminMFAEnabled := false
	if value, ok := cfg.Bool("auth.local.superadmin_mfa_enabled"); ok {
		localSuperadminMFAEnabled = value
	}
	localMFATOTPSecret, _ := cfg.String("auth.local.mfa_totp_secret")
	localMFATOTPSecret = strings.TrimSpace(localMFATOTPSecret)
	if localSuperadminMFAEnabled && localMFATOTPSecret == "" {
		return identityAuthConfig{}, fmt.Errorf("auth.local.mfa_totp_secret is required when superadmin MFA is enabled")
	}
	out := identityAuthConfig{
		IdentityMode:              identityMode,
		LocalEnabled:              localEnabled,
		LocalSuperadminMFAEnabled: localSuperadminMFAEnabled,
		LocalMFATOTPSecret:        localMFATOTPSecret,
		AutoLinkEnabled:           autoLinkEnabled,
		OIDCClients:               clients,
	}
	oidc, err := oidcTokenProviderFromPlatform(cfg)
	if err != nil {
		return identityAuthConfig{}, err
	}
	out.OIDC = oidc
	if identityMode == identityModeStandalone {
		feishu, autoProvision, err := feishuIdentityProviderFromPlatform(cfg)
		if err != nil {
			return identityAuthConfig{}, err
		}
		out.Feishu = feishu
		out.FeishuAutoProvision = autoProvision
	}
	hydra, err := hydraAdminClientFromPlatform(cfg)
	if err != nil {
		return identityAuthConfig{}, err
	}
	if hydra != nil {
		out.HydraAdmin = hydra
		out.OIDCLoginConsent = identity.NewOIDCLoginConsentService(hydra, clients)
	}
	out.OIDCClientAdmin = oidcClientAdminFromPlatform(cfg, clients, hydra)
	if kratosPublicURL == "" {
		if identityMode == identityModePeopleSSO {
			return identityAuthConfig{}, fmt.Errorf("auth.kratos.public_url is required in people_sso mode")
		}
		return out, nil
	}
	if identityMode == identityModePeopleSSO {
		adminURL, _ := cfg.String("auth.kratos.admin_url")
		if strings.TrimSpace(adminURL) == "" {
			return identityAuthConfig{}, fmt.Errorf("auth.kratos.admin_url is required in people_sso mode")
		}
		adminToken, _ := cfg.String("auth.kratos.admin_token")
		schemaID, _ := cfg.String("auth.kratos.schema_id")
		feishuProviderID, _ := cfg.String("auth.kratos.feishu_provider_id")
		admin, err := peopleaccount.NewKratosAdminClient(peopleaccount.KratosAdminConfig{
			AdminURL:         adminURL,
			AdminToken:       adminToken,
			SchemaID:         schemaID,
			FeishuProviderID: feishuProviderID,
			HTTPClient:       &http.Client{Timeout: 10 * time.Second},
		})
		if err != nil {
			return identityAuthConfig{}, err
		}
		out.KratosAdmin = admin
	}
	callbackURL, ok := cfg.String("auth.kratos.callback_url")
	if !ok || strings.TrimSpace(callbackURL) == "" {
		return identityAuthConfig{}, fmt.Errorf("auth.kratos.callback_url is required when auth.kratos.public_url is configured")
	}
	displayName, _ := cfg.String("auth.kratos.display_name")
	sessionCookieNames, _ := cfg.String("auth.kratos.session_cookie_names")
	provider, err := identity.NewKratosIdentityProvider(identity.KratosProviderConfig{
		PublicURL:          kratosPublicURL,
		CallbackURL:        callbackURL,
		DisplayName:        displayName,
		SessionCookieNames: strings.FieldsFunc(sessionCookieNames, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }),
		HTTPClient:         &http.Client{Timeout: 10 * time.Second},
	})
	if err != nil {
		return identityAuthConfig{}, err
	}
	out.Kratos = provider
	return out, nil
}

func feishuIdentityProviderFromPlatform(cfg config.Config) (*identity.FeishuIdentityProvider, bool, error) {
	appID, _ := cfg.String("auth.feishu.app_id")
	appSecret, _ := cfg.String("auth.feishu.app_secret")
	callbackURL, _ := cfg.String("auth.feishu.callback_url")
	bindingCallbackURL, _ := cfg.String("auth.feishu.binding_callback_url")
	stateSecret, _ := cfg.String("auth.feishu.state_secret")
	allowedTenantKey, _ := cfg.String("auth.feishu.allowed_tenant_key")
	appID = strings.TrimSpace(appID)
	appSecret = strings.TrimSpace(appSecret)
	callbackURL = strings.TrimSpace(callbackURL)
	bindingCallbackURL = strings.TrimSpace(bindingCallbackURL)
	stateSecret = strings.TrimSpace(stateSecret)
	autoProvision := false
	if value, ok := cfg.Bool("auth.feishu.auto_provision_enabled"); ok {
		autoProvision = value
	}
	if appID == "" && appSecret == "" && callbackURL == "" && bindingCallbackURL == "" && stateSecret == "" && strings.TrimSpace(allowedTenantKey) == "" && !autoProvision {
		return nil, autoProvision, nil
	}
	if appID == "" || appSecret == "" || callbackURL == "" || bindingCallbackURL == "" {
		return nil, false, fmt.Errorf("auth.feishu.app_id, app_secret, callback_url and binding_callback_url are all required when Feishu SSO is configured")
	}
	if stateSecret == "" {
		stateSecret = appSecret
	}
	provider, err := identity.NewFeishuIdentityProvider(identity.FeishuProviderConfig{
		AppID:              appID,
		AppSecret:          appSecret,
		CallbackURL:        callbackURL,
		BindingCallbackURL: bindingCallbackURL,
		StateSecret:        stateSecret,
		AllowedTenantKey:   allowedTenantKey,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	})
	if err != nil {
		return nil, false, err
	}
	return provider, autoProvision, nil
}

func oidcTokenProviderFromPlatform(cfg config.Config) (*identity.OIDCTokenProvider, error) {
	introspectionURL, ok := cfg.String("auth.oidc.introspection_url")
	introspectionURL = strings.TrimSpace(introspectionURL)
	if !ok || introspectionURL == "" {
		return nil, nil
	}
	clientID, _ := cfg.String("auth.oidc.client_id")
	clientSecret, _ := cfg.String("auth.oidc.client_secret")
	issuer, _ := cfg.String("auth.oidc.issuer")
	audience, _ := cfg.String("auth.oidc.audience")
	providerName, _ := cfg.String("auth.oidc.provider")
	if strings.TrimSpace(issuer) == "" {
		return nil, fmt.Errorf("auth.oidc.issuer is required when auth.oidc.introspection_url is configured")
	}
	if strings.TrimSpace(audience) == "" {
		return nil, fmt.Errorf("auth.oidc.audience is required when auth.oidc.introspection_url is configured")
	}
	return identity.NewOIDCTokenProvider(identity.OIDCTokenProviderConfig{
		ProviderName:     providerName,
		IntrospectionURL: introspectionURL,
		ClientID:         clientID,
		ClientSecret:     clientSecret,
		Issuer:           issuer,
		Audience:         audience,
		HTTPClient:       &http.Client{Timeout: 10 * time.Second},
	})
}

func hydraAdminClientFromPlatform(cfg config.Config) (*identity.HydraAdminClient, error) {
	adminURL, ok := cfg.String("auth.hydra.admin_url")
	adminURL = strings.TrimSpace(adminURL)
	if !ok || adminURL == "" {
		return nil, nil
	}
	adminToken, _ := cfg.String("auth.hydra.admin_token")
	rememberFor := 24 * time.Hour
	if rawRememberFor, ok := cfg.String("auth.hydra.remember_for"); ok && strings.TrimSpace(rawRememberFor) != "" {
		parsed, err := time.ParseDuration(strings.TrimSpace(rawRememberFor))
		if err != nil {
			return nil, fmt.Errorf("auth.hydra.remember_for is invalid: %w", err)
		}
		rememberFor = parsed
	}
	hydra, err := identity.NewHydraAdminClient(identity.HydraAdminConfig{
		AdminURL:    adminURL,
		AdminToken:  adminToken,
		HTTPClient:  &http.Client{Timeout: 10 * time.Second},
		RememberFor: rememberFor,
	})
	if err != nil {
		return nil, err
	}
	return hydra, nil
}

func oidcClientAdminFromPlatform(cfg config.Config, clients *identity.OIDCClientRegistry, hydra *identity.HydraAdminClient) *identity.OIDCClientAdminService {
	syncEnabled := false
	if value, ok := cfg.Bool("auth.hydra.client_sync_enabled"); ok {
		syncEnabled = value
	}
	if syncEnabled && hydra != nil {
		return identity.NewOIDCClientAdminService(clients, hydra)
	}
	return identity.NewOIDCClientAdminService(clients)
}
