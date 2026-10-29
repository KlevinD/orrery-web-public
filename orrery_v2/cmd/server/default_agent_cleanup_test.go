package main

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	operations "orrery_v2/internal/app/operations"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/testkit"
)

func TestEnsureDefaultCoordinatorAgentsRemovesDeprecatedFreelanceDefaults(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	cfg, err := config.LoadYAML(testConfigSource(t))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	orgs := operations.NewOrganizationCatalogStore(env.DBRouter)

	for _, fileName := range []string{"frontend-builder.json", "code-reviewer.json"} {
		template, err := readDefaultAgentTemplate(fileName)
		if err != nil {
			t.Fatalf("read template %s: %v", fileName, err)
		}
		profile := defaultFreelanceAgentProfile(agentcore.RuntimeSpec{}, template)
		if err := profiles.SaveProfile(ctx, profile); err != nil {
			t.Fatalf("save deprecated profile %s: %v", profile.ID, err)
		}
		if err := profiles.SaveAvailability(ctx, agentcore.AgentAvailability{AgentProfileID: profile.ID, AgentInstanceID: agentcore.AgentInstanceID(profile.ID.String() + ":runtime"), Status: agentcore.AvailabilityAvailable, UpdatedAt: time.Now().UTC()}); err != nil {
			t.Fatalf("save deprecated availability %s: %v", profile.ID, err)
		}
	}
	customTemplate, err := readDefaultAgentTemplate("bug-fixer.json")
	if err != nil {
		t.Fatalf("read custom base template: %v", err)
	}
	custom := defaultFreelanceAgentProfile(agentcore.RuntimeSpec{}, customTemplate)
	custom.ID = "custom-worker"
	custom.RealName = "叶定制"
	custom.DisplayName = "叶定制"
	custom.PersonalMemoryProfileRef.MemoryProfileID = "memory-custom-worker"
	custom.PersonalMemoryProfileRef.OwnerAgentID = "custom-worker"
	if err := profiles.SaveProfile(ctx, custom); err != nil {
		t.Fatalf("save custom profile: %v", err)
	}

	if err := ensureDefaultCoordinatorAgents(ctx, cfg, profiles, orgs); err != nil {
		t.Fatalf("ensure default agents: %v", err)
	}

	for _, removedID := range []agentcore.AgentProfileID{"frontend-builder", "code-reviewer"} {
		if _, err := profiles.GetProfile(ctx, removedID); err == nil {
			t.Fatalf("deprecated default profile %s should be removed", removedID)
		}
		deleted, err := profiles.IsProfileDeleted(ctx, removedID)
		if err != nil {
			t.Fatalf("is profile deleted %s: %v", removedID, err)
		}
		if !deleted {
			t.Fatalf("deprecated default profile %s should leave a tombstone", removedID)
		}
	}
	if _, err := profiles.GetProfile(ctx, custom.ID); err != nil {
		t.Fatalf("custom profile should not be removed: %v", err)
	}
}

func TestEnsureDefaultCoordinatorAgentsRemovesRetiredStaffingDefault(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	cfg, err := config.LoadYAML(testConfigSource(t))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	orgs := operations.NewOrganizationCatalogStore(env.DBRouter)
	template, err := readDefaultAgentTemplate("lead-agent.json")
	if err != nil {
		t.Fatalf("read lead template: %v", err)
	}
	retired := defaultCoordinatorAgentProfile("staffing-agent", "staffing", agentcore.RuntimeSpec{}, template)
	retired.RealName = "秦若兰"
	retired.DisplayName = "秦若兰"
	retired.RoleName = "Staffing"
	if err := profiles.SaveProfile(ctx, retired); err != nil {
		t.Fatalf("save retired profile: %v", err)
	}

	if err := ensureDefaultCoordinatorAgents(ctx, cfg, profiles, orgs); err != nil {
		t.Fatalf("ensure default agents: %v", err)
	}

	if _, err := profiles.GetProfile(ctx, "staffing-agent"); err == nil {
		t.Fatalf("retired staffing default should be removed")
	}
	deleted, err := profiles.IsProfileDeleted(ctx, "staffing-agent")
	if err != nil {
		t.Fatalf("is profile deleted staffing-agent: %v", err)
	}
	if !deleted {
		t.Fatalf("retired staffing default should leave a tombstone")
	}
}

func TestEnsureDefaultCoordinatorAgentsRemovesRetiredLegalAgents(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	cfg, err := config.LoadYAML(testConfigSource(t))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	orgs := operations.NewOrganizationCatalogStore(env.DBRouter)
	legalProfiles, err := defaultLegalAgentProfiles(agentcore.RuntimeSpec{})
	if err != nil {
		t.Fatalf("build retired legal profiles: %v", err)
	}
	for _, profile := range legalProfiles {
		if err := profiles.SaveProfile(ctx, profile); err != nil {
			t.Fatalf("save retired legal profile %s: %v", profile.ID, err)
		}
		if err := saveDefaultAgentAvailability(ctx, profiles, profile); err != nil {
			t.Fatalf("save retired legal availability %s: %v", profile.ID, err)
		}
	}

	if err := ensureDefaultCoordinatorAgents(ctx, cfg, profiles, orgs); err != nil {
		t.Fatalf("ensure default agents: %v", err)
	}

	for _, profile := range legalProfiles {
		if _, err := profiles.GetProfile(ctx, profile.ID); err == nil {
			t.Fatalf("retired legal profile %s should be removed", profile.ID)
		}
		deleted, err := profiles.IsProfileDeleted(ctx, profile.ID)
		if err != nil || !deleted {
			t.Fatalf("retired legal profile %s tombstone: deleted=%t err=%v", profile.ID, deleted, err)
		}
	}
	if err := ensureDefaultCoordinatorAgents(ctx, cfg, profiles, orgs); err != nil {
		t.Fatalf("repeat ensure default agents with legal tombstones: %v", err)
	}
}

func TestEnsureProductionDefaultProfilesDoesNotSeedRetiredLegalAgents(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	cfg, err := config.LoadYAML(testConfigSource(t))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	orgs := operations.NewOrganizationCatalogStore(env.DBRouter)

	if _, err := ensureProductionDefaultProfiles(ctx, cfg, profiles, orgs, nil); err != nil {
		t.Fatalf("ensure production default profiles: %v", err)
	}

	for _, profileID := range []agentcore.AgentProfileID{
		"legal-orchestrator-agent",
		"legal-knowledge-agent",
		"legal-contract-review-agent",
		"legal-compliance-risk-agent",
	} {
		if _, err := profiles.GetProfile(ctx, profileID); err == nil {
			t.Fatalf("production bootstrap seeded retired legal profile %s", profileID)
		}
	}
}
