package main

import (
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/userauth"
)

func configureProductionBrowserIdentityProviders(cfg identityAuthConfig, authState *userauth.PlatformAuthStateStore, authService *userauth.Service) ([]identity.BrowserIdentityProvider, error) {
	if cfg.LocalSuperadminMFAEnabled {
		validator, err := userauth.NewTOTPValidator(map[string]string{userauth.SuperadminUsername: cfg.LocalMFATOTPSecret})
		if err != nil {
			return nil, err
		}
		validator.WithReplayStore(authState)
		authService.WithMFAValidator(validator, false)
	}
	providers := []identity.BrowserIdentityProvider{}
	if cfg.Kratos != nil {
		providers = append(providers, cfg.Kratos)
	}
	if cfg.Feishu != nil {
		providers = append(providers, cfg.Feishu)
	}
	return providers, nil
}
