package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	operations "orrery_v2/internal/app/operations"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

type countingProfileSaveHook struct {
	calls int
}

func (h *countingProfileSaveHook) BeforeProfileSave(context.Context, uow.Tx, *agentcore.AgentProfile, agentcore.AgentProfile) error {
	h.calls++
	return nil
}

func TestProductionHumanProfileBootstrapResolvesExistingStaffableHumanAgent(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	seeded := defaultHumanAgentProfile("human-operator-user", "Operator User")
	if err := profiles.SaveProfile(ctx, seeded); err != nil {
		t.Fatalf("seed human profile: %v", err)
	}
	if err := saveHumanAgentAvailability(ctx, profiles, seeded); err != nil {
		t.Fatalf("seed human availability: %v", err)
	}
	bootstrap := newProductionHumanProfileBootstrap(profiles)

	profile, err := bootstrap.EnsureHumanProfile(ctx, missioncontrol.EnsureHumanProfileCommand{HumanAgentID: "Operator User"})
	if err != nil {
		t.Fatalf("EnsureHumanProfile: %v", err)
	}
	if profile.ID != "human-operator-user" || profile.AgentKind != agentcore.AgentKindHuman || profile.RuntimeSpec.HasKind() {
		t.Fatalf("human profile identity/runtime = %+v", profile)
	}
	if profile.OrgIdentity.EmploymentType != agentcore.EmploymentTypeHuman ||
		profile.OrgIdentity.AuthorityScope != "mission_collaboration" ||
		len(profile.OrgIdentity.RoleRefs) != 1 ||
		profile.OrgIdentity.RoleRefs[0].ID != "realhumancontributor" {
		t.Fatalf("human profile org identity = %+v", profile.OrgIdentity)
	}
	if errs := agentcore.ValidateAgentProfile(profile); len(errs) > 0 {
		t.Fatalf("human profile validation errors: %+v", errs)
	}
	availability, err := profiles.GetAvailability(ctx, profile.ID)
	if err != nil {
		t.Fatalf("get human availability: %v", err)
	}
	if availability.Status != agentcore.AvailabilityAvailable || availability.AgentInstanceID != agentcore.AgentInstanceID(profile.ID) {
		t.Fatalf("human availability should keep stable agent identity: %+v", availability)
	}
}

func TestProductionHumanProfileBootstrapRejectsMissingMissionHumanWithoutWrite(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	hook := &countingProfileSaveHook{}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork).WithProfileSaveHook(hook)
	bootstrap := newProductionHumanProfileBootstrap(profiles)
	profileID := agentcore.AgentProfileID("human-orrery-pa-e2e-1786420622")

	_, err := bootstrap.EnsureHumanProfile(ctx, missioncontrol.EnsureHumanProfileCommand{HumanAgentID: profileID.String(), DisplayName: "PA E2E"})
	if !errors.Is(err, agentcore.ErrNotFound) {
		t.Fatalf("missing Mission human error = %v, want ErrNotFound", err)
	}
	if hook.calls != 0 {
		t.Fatalf("profile saves = %d, want 0", hook.calls)
	}
	if _, err := profiles.GetProfile(ctx, profileID); !errors.Is(err, agentcore.ErrNotFound) {
		t.Fatalf("derived Mission human profile error = %v, want ErrNotFound", err)
	}
}

func TestProductionHumanProfileBootstrapRejectsUnboundViewerWithoutWrite(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	hook := &countingProfileSaveHook{}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork).WithProfileSaveHook(hook)
	bootstrap := newProductionHumanProfileBootstrap(profiles)
	viewer := operations.ViewerRef{ViewerID: "Operator User", DisplayName: "Operator User"}

	if _, err := bootstrap.EnsureViewerProfile(ctx, viewer); err == nil {
		t.Fatal("unbound viewer should not receive a derived workforce profile")
	}
	if hook.calls != 0 {
		t.Fatalf("profile saves = %d, want 0", hook.calls)
	}
	if _, err := profiles.GetProfile(ctx, humanAgentProfileID(viewer.ViewerID)); !errors.Is(err, agentcore.ErrNotFound) {
		t.Fatalf("derived viewer profile error = %v, want ErrNotFound", err)
	}
}

func TestProductionHumanProfileBootstrapUsesCanonicalViewerProfile(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	profileID := agentcore.AgentProfileID("profile-2312ad7e06e93fdbd440015f218ab938")
	profile := defaultHumanAgentProfile(profileID, "Krieg")
	profile.RoleName = "Configured People Role"
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save canonical viewer profile: %v", err)
	}
	hook := &countingProfileSaveHook{}
	bootstrap := newProductionHumanProfileBootstrap(agentcore.NewPlatformProfileRepository(env.UnitOfWork).WithProfileSaveHook(hook))

	resolved, err := bootstrap.EnsureViewerProfile(ctx, operations.ViewerRef{
		ViewerID:       "feishu-ou_98847333991e23e3bedd670b5e58903b",
		DisplayName:    "Krieg",
		AgentProfileID: profileID.String(),
	})
	if err != nil {
		t.Fatalf("EnsureViewerProfile: %v", err)
	}
	if resolved.ID != profileID || resolved.RoleName != profile.RoleName {
		t.Fatalf("resolved viewer profile = %+v, want canonical profile %+v", resolved, profile)
	}
	if hook.calls != 0 {
		t.Fatalf("canonical viewer lookup wrote profile %d times", hook.calls)
	}
	duplicateID := humanAgentProfileID("feishu-ou_98847333991e23e3bedd670b5e58903b")
	if _, err := profiles.GetProfile(ctx, duplicateID); !errors.Is(err, agentcore.ErrNotFound) {
		t.Fatalf("fallback viewer profile %s error = %v, want ErrNotFound", duplicateID, err)
	}
}

func TestProductionHumanProfileBootstrapRejectsDisabledCanonicalViewer(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	profile := defaultHumanAgentProfile("human-disabled", "Disabled")
	profile.Enabled = false
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save disabled viewer profile: %v", err)
	}

	_, err := newProductionHumanProfileBootstrap(profiles).EnsureViewerProfile(ctx, operations.ViewerRef{
		ViewerID: "disabled", AgentProfileID: profile.ID.String(),
	})
	if err == nil {
		t.Fatal("disabled canonical human profile should not be accepted")
	}
}

func TestProductionHumanProfileBootstrapRejectsUnboundDiscussionViewerWithoutWrite(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	hook := &countingProfileSaveHook{}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork).WithProfileSaveHook(hook)
	bootstrap := newProductionHumanProfileBootstrap(profiles)

	_, err := bootstrap.EnsureDiscussionHumanProfile(ctx, discussionsession.ViewerRef{
		ViewerID: "orrery-browser-e2e-1786379570670", ViewerKind: discussionsession.ViewerKindUser, DisplayName: "Browser E2E",
	})
	if err == nil {
		t.Fatal("unbound discussion viewer should not receive a derived workforce profile")
	}
	if hook.calls != 0 {
		t.Fatalf("profile saves = %d, want 0", hook.calls)
	}
	profileID := humanAgentProfileID("orrery-browser-e2e-1786379570670")
	if _, err := profiles.GetProfile(ctx, profileID); !errors.Is(err, agentcore.ErrNotFound) {
		t.Fatalf("discussion profile %s error = %v, want ErrNotFound", profileID, err)
	}
}

func TestProductionHumanProfileBootstrapUsesCanonicalDiscussionProfileWithoutWrite(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	profile := defaultHumanAgentProfile("human-operator", "Operator")
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save canonical discussion profile: %v", err)
	}
	hook := &countingProfileSaveHook{}
	bootstrap := newProductionHumanProfileBootstrap(agentcore.NewPlatformProfileRepository(env.UnitOfWork).WithProfileSaveHook(hook))

	resolved, err := bootstrap.EnsureDiscussionHumanProfile(ctx, discussionsession.ViewerRef{
		ViewerID: "operator", AgentProfileID: profile.ID.String(), ViewerKind: discussionsession.ViewerKindUser,
	})
	if err != nil {
		t.Fatalf("EnsureDiscussionHumanProfile: %v", err)
	}
	if resolved.ID != profile.ID || hook.calls != 0 {
		t.Fatalf("resolved discussion profile = %+v, saves = %d", resolved, hook.calls)
	}
}

func TestDiscussionSessionViewerUsesCanonicalProfileAsConversationIdentity(t *testing.T) {
	viewer := discussionSessionViewer(missioncontrol.ViewerRef{
		ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser, AuthorizationSubjectID: "human-operator", TenantID: "tenant-a",
	})
	if viewer.ViewerID != "human-operator" || viewer.AgentProfileID != "human-operator" || viewer.TenantID != "tenant-a" {
		t.Fatalf("discussion viewer = %+v", viewer)
	}
}

func TestDiscussionSessionViewerTreatsReservedPrincipalAsSystemIdentity(t *testing.T) {
	viewer := discussionSessionViewer(missioncontrol.ViewerRef{
		ViewerID: "superadmin", ViewerKind: missioncontrol.ViewerKindUser, AuthorizationSubjectID: agentcore.SystemSuperadminPrincipalID, TenantID: "tenant-a",
	})
	if viewer.ViewerID != agentcore.SystemSuperadminPrincipalID || viewer.AgentProfileID != "" || viewer.ViewerKind != discussionsession.ViewerKindSystem {
		t.Fatalf("reserved discussion viewer = %+v", viewer)
	}
}

func TestProductionHumanProfileBootstrapDoesNotMutateLegacyAvailability(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	bootstrap := newProductionHumanProfileBootstrap(profiles)
	profile := defaultHumanAgentProfile(agentcore.AgentProfileID("human-operator-user"), "Operator User")
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save human profile: %v", err)
	}
	if err := profiles.SaveAvailability(ctx, agentcore.AgentAvailability{
		AgentProfileID:  profile.ID,
		AgentInstanceID: agentcore.AgentInstanceID(profile.ID.String() + ":default"),
		Status:          agentcore.AvailabilityAvailable,
		Source:          "human-bootstrap",
		UpdatedAt:       time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save legacy human availability: %v", err)
	}

	restored, err := bootstrap.EnsureHumanProfile(ctx, missioncontrol.EnsureHumanProfileCommand{HumanAgentID: "Operator User"})
	if err != nil {
		t.Fatalf("EnsureHumanProfile: %v", err)
	}

	availability, err := profiles.GetAvailability(ctx, restored.ID)
	if err != nil {
		t.Fatalf("get repaired human availability: %v", err)
	}
	if availability.Status != agentcore.AvailabilityAvailable || availability.AgentInstanceID != agentcore.AgentInstanceID(restored.ID.String()+":default") {
		t.Fatalf("Mission lookup mutated legacy human availability: %+v", availability)
	}
}

func TestProductionHumanProfileBootstrapDoesNotMutateLegacyOrgIdentity(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	bootstrap := newProductionHumanProfileBootstrap(profiles)
	profile := defaultHumanAgentProfile(agentcore.AgentProfileID("human-legacy-operator"), "遗留用户")
	profile.OrgIdentity.PositionRef = agentcore.PositionRef{}
	profile.OrgIdentity.RoleRefs = nil
	if err := saveRawAgentProfile(ctx, env.UnitOfWork, profile); err != nil {
		t.Fatalf("save legacy incomplete human profile: %v", err)
	}

	resolved, err := bootstrap.EnsureHumanProfile(ctx, missioncontrol.EnsureHumanProfileCommand{HumanAgentID: "legacy-operator", DisplayName: "遗留用户"})
	if err != nil {
		t.Fatalf("EnsureHumanProfile should resolve existing human: %v", err)
	}

	if resolved.OrgIdentity.PositionRef.ID != "" || len(resolved.OrgIdentity.RoleRefs) != 0 {
		t.Fatalf("Mission lookup mutated human org identity: %+v", resolved.OrgIdentity)
	}
}

func TestSystemManagedDiscussionProfileTreatsHumanBaseAgentLikeAnyAgent(t *testing.T) {
	profile := defaultHumanAgentProfile(agentcore.AgentProfileID("human-operator-user"), "Operator User")
	profile.RuntimeSpec = agentcore.RuntimeSpec{}

	if !systemManagedDiscussionProfile(profile) {
		t.Fatalf("human BaseAgent profile should be eligible for system-managed discussion runtime: %+v", profile.BaseAgent)
	}
}

func TestSystemManagedDiscussionProfileRequiresBaseAgentAudit(t *testing.T) {
	profile := defaultHumanAgentProfile(agentcore.AgentProfileID("human-operator-user"), "Operator User")
	profile.BaseAgent.AuditRequired = false

	if systemManagedDiscussionProfile(profile) {
		t.Fatalf("profile without BaseAgent audit should not use system-managed discussion runtime: %+v", profile.BaseAgent)
	}
}

func TestProductionHumanProfileBootstrapDoesNotRestoreDeletedViewerProfile(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	bootstrap := newProductionHumanProfileBootstrap(profiles)
	profile := defaultHumanAgentProfile("human-operator-user", "Operator User")
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("seed human profile: %v", err)
	}
	viewer := operations.ViewerRef{ViewerID: "Operator User", DisplayName: "Operator User", AgentProfileID: profile.ID.String()}
	if err := profiles.DeleteProfile(ctx, profile.ID, "workforce.member.delete"); err != nil {
		t.Fatalf("delete viewer profile: %v", err)
	}

	if _, err := bootstrap.EnsureViewerProfile(ctx, viewer); !errors.Is(err, agentcore.ErrNotFound) {
		t.Fatalf("EnsureViewerProfile after delete error = %v, want ErrNotFound", err)
	}

	if _, err := profiles.GetProfile(ctx, profile.ID); !errors.Is(err, agentcore.ErrNotFound) {
		t.Fatalf("deleted viewer profile error = %v, want ErrNotFound", err)
	}
	deleted, err := profiles.IsProfileDeleted(ctx, profile.ID)
	if err != nil {
		t.Fatalf("is viewer profile deleted: %v", err)
	}
	if !deleted {
		t.Fatalf("viewer profile tombstone was not preserved")
	}
}

func TestEnsureDefaultCoordinatorAgentsSeedsOnlySystemWorkflowAgents(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	cfg, err := config.LoadYAML(testConfigSource(t))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	orgs := operations.NewOrganizationCatalogStore(env.DBRouter)

	if err := ensureDefaultCoordinatorAgents(ctx, cfg, profiles, orgs); err != nil {
		t.Fatalf("ensure default agents: %v", err)
	}

	for _, want := range []struct {
		profileID string
		position  agentcore.PositionID
	}{
		{profileID: defaultLeadAgentID, position: agentcore.PositionID(defaultLeadAgentPositionID)},
		{profileID: defaultCoordinatorAgentID, position: agentcore.PositionID(defaultCoordinatorPositionID)},
		{profileID: defaultQAPlanAgentID, position: agentcore.PositionID(defaultQAPlanPositionID)},
		{profileID: defaultQACheckAgentID, position: agentcore.PositionID(defaultQACheckPositionID)},
	} {
		profile, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(want.profileID))
		if err != nil {
			t.Fatalf("get system workflow profile %s: %v", want.profileID, err)
		}
		if profile.AgentKind != agentcore.AgentKindAI ||
			profile.OrgIdentity.EmploymentType != agentcore.EmploymentTypeSystem ||
			profile.OrgIdentity.OrganizationRef.ID != agentcore.OrganizationID(defaultCoordinatorOrganizationID) ||
			profile.OrgIdentity.DepartmentRef.ID != agentcore.DepartmentID(defaultCoordinatorDepartmentID) ||
			profile.OrgIdentity.PositionRef.ID != want.position {
			t.Fatalf("system workflow profile identity mismatch for %s: %+v", want.profileID, profile.OrgIdentity)
		}
		if profile.RoleName == "" || profile.Persona.ResponsibilityBoundary == "" {
			t.Fatalf("system workflow profile was not hydrated from template: %+v", profile)
		}
		if strings.TrimSpace(profile.Persona.Backstory) != "" {
			t.Fatalf("system workflow profile should leave background empty for later injection: %+v", profile.Persona)
		}
		availability, err := profiles.GetAvailability(ctx, profile.ID)
		if err != nil {
			t.Fatalf("get availability for %s: %v", want.profileID, err)
		}
		if availability.Status != agentcore.AvailabilityAvailable || availability.AgentInstanceID != agentcore.AgentInstanceID(want.profileID) {
			t.Fatalf("system workflow availability for %s should keep stable agent identity: %+v", want.profileID, availability)
		}
	}

	page, err := profiles.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{})
	if err != nil {
		t.Fatalf("query directory: %v", err)
	}
	if page.Total != 4 {
		t.Fatalf("default workflow should initialize only four system agents, got %d entries: %+v", page.Total, page.Entries)
	}
	for _, removedID := range []agentcore.AgentProfileID{"staffing-agent", "code-reviewer", "frontend-builder", "bug-fixer", "subagent-worker"} {
		if _, err := profiles.GetProfile(ctx, removedID); err == nil {
			t.Fatalf("non-system template %s should not be initialized as a workforce member", removedID)
		}
	}
}
