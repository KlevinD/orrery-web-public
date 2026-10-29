package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/userauth"
	"orrery_v2/internal/platform/config"
)

func skipDefaultProfileBootstrap() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("ORRERY_SKIP_DEFAULT_PROFILE_BOOTSTRAP")), "true")
}

func ensureProductionDefaultProfiles(
	ctx context.Context,
	cfg config.Config,
	profiles *agentcore.PlatformProfileRepository,
	organizations *operations.OrganizationCatalogStore,
	sourceDeletion operations.SourceDeletionLifecyclePort,
) (systemUserBootstrapPasswords, error) {
	if err := ensureDefaultTalentMarketProfiles(ctx, profiles); err != nil {
		return systemUserBootstrapPasswords{}, err
	}
	passwords, err := systemUserBootstrapPasswordsFromConfig(cfg)
	if err != nil {
		return systemUserBootstrapPasswords{}, err
	}
	if err := ensureDefaultSystemUserProfilesWithPasswords(ctx, profiles, passwords); err != nil {
		return systemUserBootstrapPasswords{}, err
	}
	if skipDefaultProfileBootstrap() {
		return passwords, nil
	}
	if err := ensureDefaultCoordinatorAgents(ctx, cfg, profiles, organizations, sourceDeletion); err != nil {
		return systemUserBootstrapPasswords{}, err
	}
	if err := ensureDefaultOntologyStewardAgent(ctx, cfg, profiles); err != nil {
		return systemUserBootstrapPasswords{}, err
	}
	if err := ensureDefaultWebsiteAdvisorAgent(ctx, profiles); err != nil {
		return systemUserBootstrapPasswords{}, err
	}
	return passwords, nil
}

func ensureProductionAuthenticationBootstrap(
	ctx context.Context,
	cfg config.Config,
	profiles *agentcore.PlatformProfileRepository,
	organizations *operations.OrganizationCatalogStore,
	sourceDeletion operations.SourceDeletionLifecyclePort,
	authStore userauth.AccountStore,
	roles *operations.RoleStore,
) error {
	passwords, err := ensureProductionDefaultProfiles(ctx, cfg, profiles, organizations, sourceDeletion)
	if err != nil {
		return fmt.Errorf("ensure production default profiles: %w", err)
	}
	if err := userauth.EnsureBootstrapSuperadmin(ctx, authStore, passwords.Superadmin); err != nil {
		return fmt.Errorf("ensure bootstrap superadmin account: %w", err)
	}
	if err := ensureDefaultOntologyStewardRoleAssignment(ctx, roles); err != nil {
		return fmt.Errorf("ensure ontology steward role: %w", err)
	}
	return nil
}
