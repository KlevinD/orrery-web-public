package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/userauth"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/testkit"
)

func TestEnsureDefaultSystemUserProfilesRemovesReservedWorkforceProfiles(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	reserved := []string{
		userauth.SuperadminAgentProfileID,
		userauth.LegacySuperadminAgentProfileID,
		userauth.LegacyLocalAdminProfileID,
		userauth.LegacyLocalUserProfileID,
	}
	for _, id := range reserved {
		profile := defaultHumanAgentProfile(agentcore.AgentProfileID(id), "Reserved profile")
		profile.UserAccount = agentcore.UserAccountSpec{Username: "reserved-" + id, AccountEnabled: true}
		if err := saveRawAgentProfile(ctx, env.UnitOfWork, profile); err != nil {
			t.Fatalf("save raw reserved profile %s: %v", id, err)
		}
	}

	if err := ensureDefaultSystemUserProfiles(ctx, profiles); err != nil {
		t.Fatalf("ensure system users: %v", err)
	}
	for _, id := range reserved {
		if _, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(id)); !errors.Is(err, agentcore.ErrNotFound) {
			t.Fatalf("reserved profile %s lookup error = %v, want ErrNotFound", id, err)
		}
		deleted, err := profiles.IsProfileDeleted(ctx, agentcore.AgentProfileID(id))
		if err != nil || !deleted {
			t.Fatalf("reserved profile %s tombstone: deleted=%t err=%v", id, deleted, err)
		}
	}
}

func TestEnsureDefaultSystemUserProfilesDoesNotCreateReservedRecords(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)

	if err := ensureDefaultSystemUserProfiles(ctx, profiles); err != nil {
		t.Fatalf("ensure system users: %v", err)
	}
	for _, id := range []string{
		userauth.SuperadminAgentProfileID,
		userauth.LegacySuperadminAgentProfileID,
		userauth.LegacyLocalAdminProfileID,
		userauth.LegacyLocalUserProfileID,
	} {
		deleted, err := profiles.IsProfileDeleted(ctx, agentcore.AgentProfileID(id))
		if err != nil || deleted {
			t.Fatalf("bootstrap created a reserved record for %s: deleted=%t err=%v", id, deleted, err)
		}
	}
}

func TestSkippedDefaultProfileBootstrapStillRemovesReservedWorkforceProfiles(t *testing.T) {
	t.Setenv("ORRERY_SKIP_DEFAULT_PROFILE_BOOTSTRAP", "true")
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	profile := defaultHumanAgentProfile(agentcore.LegacyLocalUserAgentProfileID, "Legacy local user")
	if err := saveRawAgentProfile(ctx, env.UnitOfWork, profile); err != nil {
		t.Fatalf("save reserved profile: %v", err)
	}

	if _, err := ensureProductionDefaultProfiles(ctx, config.Config{}, profiles, nil, nil); err != nil {
		t.Fatalf("ensure production profiles: %v", err)
	}
	if _, err := profiles.GetProfile(ctx, profile.ID); !errors.Is(err, agentcore.ErrNotFound) {
		t.Fatalf("reserved profile lookup error = %v, want ErrNotFound", err)
	}
	deleted, err := profiles.IsProfileDeleted(ctx, profile.ID)
	if err != nil || !deleted {
		t.Fatalf("reserved profile tombstone: deleted=%t err=%v", deleted, err)
	}
}

func TestProductionHumanProfileLookupRejectsReservedPrincipal(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	profile := defaultHumanAgentProfile(agentcore.LegacyLocalUserAgentProfileID, "Legacy local user")
	if err := saveRawAgentProfile(ctx, env.UnitOfWork, profile); err != nil {
		t.Fatalf("save raw legacy profile: %v", err)
	}

	bootstrap := newProductionHumanProfileBootstrap(profiles)
	if _, err := bootstrap.lookupEnabledHumanProfile(ctx, profile.ID); !errors.Is(err, agentcore.ErrNotFound) {
		t.Fatalf("reserved profile lookup error = %v, want ErrNotFound", err)
	}
}

func TestEnsureDefaultSystemUserProfilesRemovesRetiredBuiltInPMUser(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	existing := defaultHumanAgentProfile(agentcore.AgentProfileID("human-pm"), "Existing PM")
	existing.UserAccount = agentcore.UserAccountSpec{Username: "pm", AccountEnabled: true}
	if err := saveRawAgentProfile(ctx, env.UnitOfWork, existing); err != nil {
		t.Fatalf("save existing PM: %v", err)
	}
	if err := saveHumanAgentAvailability(ctx, profiles, existing); err != nil {
		t.Fatalf("save existing PM availability: %v", err)
	}

	if err := ensureDefaultSystemUserProfiles(ctx, profiles); err != nil {
		t.Fatalf("ensure system users: %v", err)
	}
	if _, err := profiles.GetProfile(ctx, agentcore.AgentProfileID("human-pm")); err == nil {
		t.Fatal("retired PM profile still exists")
	}
	deleted, err := profiles.IsProfileDeleted(ctx, agentcore.AgentProfileID("human-pm"))
	if err != nil || !deleted {
		t.Fatalf("retired PM tombstone: deleted=%t err=%v", deleted, err)
	}
}

func TestRemoveRetiredDefaultPMUserProfilePropagatesLookupFailure(t *testing.T) {
	profiles := agentcore.NewPlatformProfileRepository(nil)

	err := removeRetiredDefaultPMUserProfile(context.Background(), profiles)
	if err == nil || !strings.Contains(err.Error(), "requires unit of work") {
		t.Fatalf("expected profile lookup failure, got %v", err)
	}
}

func TestSystemUserBootstrapPasswordsRequireProductionSecrets(t *testing.T) {
	t.Setenv(bootstrapSuperadminPasswordEnv, "")
	_, err := systemUserBootstrapPasswordsFromConfig(config.Config{Environment: "production"})
	if err == nil || !strings.Contains(err.Error(), bootstrapSuperadminPasswordEnv) {
		t.Fatalf("expected missing superadmin password error, got %v", err)
	}

	t.Setenv(bootstrapSuperadminPasswordEnv, "super-secret")
	passwords, err := systemUserBootstrapPasswordsFromConfig(config.Config{Environment: "production"})
	if err != nil {
		t.Fatalf("passwords from config: %v", err)
	}
	if passwords.Superadmin != "super-secret" {
		t.Fatalf("passwords = %+v", passwords)
	}
}

func TestSystemUserBootstrapPasswordsRejectDefaultProductionSecret(t *testing.T) {
	t.Setenv(bootstrapSuperadminPasswordEnv, userauth.SuperadminPassword())
	_, err := systemUserBootstrapPasswordsFromConfig(config.Config{Environment: "production"})
	if err == nil || !strings.Contains(err.Error(), bootstrapSuperadminPasswordEnv) {
		t.Fatalf("expected weak superadmin password error, got %v", err)
	}

	t.Setenv(bootstrapSuperadminPasswordEnv, "change-me-superadmin-password")
	_, err = systemUserBootstrapPasswordsFromConfig(config.Config{Environment: "production"})
	if err == nil || !strings.Contains(err.Error(), bootstrapSuperadminPasswordEnv) {
		t.Fatalf("expected placeholder superadmin password error, got %v", err)
	}
}
