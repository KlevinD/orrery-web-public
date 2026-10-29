package main

import (
	"context"
	"os"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	operations "orrery_v2/internal/app/operations"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/testkit"
)

func TestEnsureDefaultCoordinatorAgentsUsesConfiguredPrimaryWithoutDefaultDuplicate(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	cfg, err := config.LoadYAML(testConfigSource(t, map[string]string{"discussion.agent_id": "custom-primary"}))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	orgs := operations.NewOrganizationCatalogStore(env.DBRouter)
	if err := orgs.SaveOrganization(ctx, operations.OrganizationDTO{OrganizationID: "org-agent-coordination", Name: "Orrery System", DefaultDepartmentID: "dept-agent-coordination", DefaultPositionID: "position-agent-coordinator"}); err != nil {
		t.Fatalf("seed legacy coordinator org: %v", err)
	}
	if err := orgs.SaveOrganization(ctx, operations.OrganizationDTO{OrganizationID: "org-human-collaborators", Name: "Orrery Human Collaborators", DefaultDepartmentID: "dept-human-collaborators", DefaultPositionID: "position-real-human-contributor"}); err != nil {
		t.Fatalf("seed legacy human org: %v", err)
	}
	if err := orgs.SaveDepartment(ctx, operations.DepartmentDTO{DepartmentID: "dept-agent-coordination", OrganizationID: "org-agent-coordination", Name: "Agent Coordination"}); err != nil {
		t.Fatalf("seed legacy coordinator dept: %v", err)
	}
	if err := orgs.SaveDepartment(ctx, operations.DepartmentDTO{DepartmentID: "dept-human-collaborators", OrganizationID: "org-human-collaborators", Name: "Human Collaborators"}); err != nil {
		t.Fatalf("seed legacy human dept: %v", err)
	}
	if err := orgs.SavePosition(ctx, operations.PositionDTO{PositionID: "position-agent-coordinator", DepartmentID: "dept-agent-coordination", Name: "Agent Coordinator"}); err != nil {
		t.Fatalf("seed legacy coordinator position: %v", err)
	}
	if err := orgs.SavePosition(ctx, operations.PositionDTO{PositionID: "position-real-human-contributor", DepartmentID: "dept-human-collaborators", Name: "Real Human Contributor"}); err != nil {
		t.Fatalf("seed legacy human position: %v", err)
	}

	if err := ensureDefaultCoordinatorAgents(ctx, cfg, profiles, orgs); err != nil {
		t.Fatalf("ensure default agents: %v", err)
	}

	if _, err := profiles.GetProfile(ctx, agentcore.AgentProfileID("custom-primary")); err != nil {
		t.Fatalf("configured primary profile was not initialized: %v", err)
	}
	if _, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(defaultLeadAgentID)); err == nil {
		t.Fatalf("default primary profile %q should not be duplicated when a configured primary is used", defaultLeadAgentID)
	}
	page, err := profiles.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{})
	if err != nil {
		t.Fatalf("query directory: %v", err)
	}
	if page.Total != 4 {
		t.Fatalf("default workflow should initialize four system agents without a dedicated staffing agent, got %d entries: %+v", page.Total, page.Entries)
	}
}

func TestEnsureDefaultCoordinatorAgentsSeedsMinviaBusinessOrganization(t *testing.T) {
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

	graph, err := orgs.ListOrganizations(ctx, operations.OrgGraphQuery{})
	if err != nil {
		t.Fatalf("list organizations: %v", err)
	}
	if len(graph.Organizations) != 1 || graph.Organizations[0].OrganizationID != defaultSystemSupportOrganizationID {
		t.Fatalf("default bootstrap should seed one Minvia organization, got %+v", graph.Organizations)
	}
	if graph.Organizations[0].Name != "Minvia" {
		t.Fatalf("business organization name = %q", graph.Organizations[0].Name)
	}
	if len(graph.Departments) != 1 || graph.Departments[0].DepartmentID != defaultSystemSupportDepartmentID {
		t.Fatalf("default bootstrap should seed one system support department, got %+v", graph.Departments)
	}
	positionNames := map[string]bool{}
	for _, position := range graph.Positions {
		positionNames[position.Name] = true
	}
	for _, want := range []string{"PM", "协调 Agent", "QA 计划 Agent", "QA 验收 Agent", "Superadmin"} {
		if !positionNames[want] {
			t.Fatalf("missing system support position %q in %+v", want, graph.Positions)
		}
	}
	if positionNames["系统 PM 用户"] {
		t.Fatalf("retired PM user position should not be initialized: %+v", graph.Positions)
	}
	for _, id := range []agentcore.AgentProfileID{defaultLeadAgentID, defaultCoordinatorAgentID, defaultQAPlanAgentID, defaultQACheckAgentID} {
		profile, err := profiles.GetProfile(ctx, id)
		if err != nil {
			t.Fatalf("get default profile %s: %v", id, err)
		}
		if profile.OrgIdentity.OrganizationRef.ID != agentcore.OrganizationID(defaultSystemSupportOrganizationID) ||
			profile.OrgIdentity.DepartmentRef.ID != agentcore.DepartmentID(defaultSystemSupportDepartmentID) {
			t.Fatalf("default profile %s not attached to Minvia: %+v", id, profile.OrgIdentity)
		}
	}
}

func TestEnsureDefaultCoordinatorAgentsCompletesPartialLegacyTenantMigration(t *testing.T) {
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
		t.Fatalf("initial ensure default agents: %v", err)
	}
	if err := orgs.SaveOrganization(ctx, operations.OrganizationDTO{OrganizationID: legacySystemSupportOrganizationID, Name: "legacy"}); err != nil {
		t.Fatalf("seed legacy organization: %v", err)
	}
	if err := orgs.SaveDepartment(ctx, operations.DepartmentDTO{DepartmentID: "dept-legacy-extra", OrganizationID: legacySystemSupportOrganizationID, Name: "Legacy Extra"}); err != nil {
		t.Fatalf("seed legacy department: %v", err)
	}
	profile, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(defaultLeadAgentID))
	if err != nil {
		t.Fatalf("get lead profile: %v", err)
	}
	for _, legacyID := range legacyBusinessOrganizationIDs() {
		profile.CollectiveMemoryScopeRefs = append(profile.CollectiveMemoryScopeRefs, agentcore.MemoryProfileRef{
			MemoryProfileID: agentcore.MemoryProfileID("memory-organization-" + legacyID),
			MemoryKind:      agentcore.MemoryKindOrganization,
			AccessScope:     "organization:" + legacyID,
		})
	}
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("seed partial legacy memory binding: %v", err)
	}

	if err := ensureDefaultCoordinatorAgents(ctx, cfg, profiles, orgs); err != nil {
		t.Fatalf("repeat ensure default agents: %v", err)
	}
	graph, err := orgs.ListOrganizations(ctx, operations.OrgGraphQuery{OrganizationID: defaultBusinessOrganizationID})
	if err != nil {
		t.Fatalf("list Minvia graph: %v", err)
	}
	foundDepartment := false
	for _, department := range graph.Departments {
		if department.DepartmentID == "dept-legacy-extra" {
			foundDepartment = department.OrganizationID == defaultBusinessOrganizationID
		}
	}
	if !foundDepartment {
		t.Fatalf("legacy department was not reassigned to Minvia: %+v", graph.Departments)
	}
	profile, err = profiles.GetProfile(ctx, agentcore.AgentProfileID(defaultLeadAgentID))
	if err != nil {
		t.Fatalf("reload lead profile: %v", err)
	}
	for _, ref := range profile.CollectiveMemoryScopeRefs {
		for _, legacyID := range legacyBusinessOrganizationIDs() {
			if strings.Contains(string(ref.MemoryProfileID), legacyID) || strings.Contains(ref.AccessScope, legacyID) {
				t.Fatalf("legacy memory binding %s survived retry: %+v", legacyID, profile.CollectiveMemoryScopeRefs)
			}
		}
	}
}

func TestLegacyBusinessOrganizationIDsAreCompleteAndUnique(t *testing.T) {
	want := map[string]bool{
		"org-system-runtime-support": false,
		"org-agent-coordination":     false,
		"org-human-collaborators":    false,
	}
	for _, id := range legacyBusinessOrganizationIDs() {
		if _, ok := want[id]; !ok {
			t.Fatalf("unexpected legacy organization %q", id)
		}
		if want[id] {
			t.Fatalf("duplicate legacy organization %q", id)
		}
		want[id] = true
		if !isLegacyBusinessOrganizationID("  " + id + "  ") {
			t.Fatalf("legacy organization %q is not recognized", id)
		}
	}
	for id, found := range want {
		if !found {
			t.Fatalf("legacy organization %q is missing", id)
		}
	}
	if isLegacyBusinessOrganizationID(defaultBusinessOrganizationID) {
		t.Fatal("Minvia must not be treated as a legacy organization")
	}
}

func TestEnsureDefaultCoordinatorAgentsMigratesRetiredOrganizationProfiles(t *testing.T) {
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
		t.Fatalf("initial ensure default agents: %v", err)
	}
	seed, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(defaultCoordinatorAgentID))
	if err != nil {
		t.Fatalf("get coordinator seed: %v", err)
	}
	legacyProfiles := []agentcore.AgentProfile{seed, seed}
	legacyProfiles[0].ID = "legacy-coordination-profile"
	legacyProfiles[0].OrgIdentity.OrganizationRef.ID = "org-agent-coordination"
	legacyProfiles[0].OrgIdentity.DepartmentRef.ID = "dept-agent-coordination"
	legacyProfiles[0].OrgIdentity.PositionRef.ID = "position-agent-coordinator"
	legacyProfiles[1].ID = "legacy-human-profile"
	legacyProfiles[1].OrgIdentity.OrganizationRef.ID = "org-human-collaborators"
	legacyProfiles[1].OrgIdentity.DepartmentRef.ID = "dept-human-collaborators"
	legacyProfiles[1].OrgIdentity.PositionRef.ID = "position-real-human-contributor"
	for _, profile := range legacyProfiles {
		if err := profiles.SaveProfile(ctx, profile); err != nil {
			t.Fatalf("save legacy profile %s: %v", profile.ID, err)
		}
	}

	if err := ensureDefaultCoordinatorAgents(ctx, cfg, profiles, orgs); err != nil {
		t.Fatalf("repeat ensure default agents: %v", err)
	}

	wants := map[agentcore.AgentProfileID]agentcore.PositionID{
		"legacy-coordination-profile": defaultCoordinatorPositionID,
		"legacy-human-profile":        defaultHumanPositionID,
	}
	for profileID, positionID := range wants {
		profile, err := profiles.GetProfile(ctx, profileID)
		if err != nil {
			t.Fatalf("get migrated profile %s: %v", profileID, err)
		}
		if profile.OrgIdentity.OrganizationRef.ID != agentcore.OrganizationID(defaultBusinessOrganizationID) ||
			profile.OrgIdentity.DepartmentRef.ID != agentcore.DepartmentID(defaultSystemSupportDepartmentID) ||
			profile.OrgIdentity.PositionRef.ID != positionID {
			t.Fatalf("legacy profile %s was not migrated: %+v", profileID, profile.OrgIdentity)
		}
	}
}

func TestDefaultCoordinatorAgentProfilesKeepQASetting(t *testing.T) {
	profiles, err := defaultCoordinatorAgentProfiles(agentcore.RuntimeSpec{})
	if err != nil {
		t.Fatalf("defaultCoordinatorAgentProfiles: %v", err)
	}
	if len(profiles) == 0 {
		t.Fatal("default coordinator profiles should not be empty")
	}
	for _, profile := range profiles {
		if strings.TrimSpace(profile.RealName) == "" {
			t.Fatalf("%s should keep a usable identity: %+v", profile.ID, profile)
		}
	}
}

func TestDefaultLegalAgentProfilesBindLegalMCPServer(t *testing.T) {
	profiles, err := defaultLegalAgentProfiles(agentcore.RuntimeSpec{})
	if err != nil {
		t.Fatalf("defaultLegalAgentProfiles: %v", err)
	}
	if len(profiles) != 4 {
		t.Fatalf("default legal profile count = %d, profiles = %+v", len(profiles), profiles)
	}
	seen := map[agentcore.AgentProfileID]agentcore.AgentProfile{}
	for _, profile := range profiles {
		seen[profile.ID] = profile
		if errs := agentcore.ValidateAgentProfile(profile); len(errs) > 0 {
			t.Fatalf("legal profile %s should validate: %+v", profile.ID, errs)
		}
		if !hasToolRef(profile.ToolRefs, defaultLegalMCPServerToolID) {
			t.Fatalf("legal profile %s missing legal MCP tool ref: %+v", profile.ID, profile.ToolRefs)
		}
		if !hasAgentRoleRef(profile.OrgIdentity.RoleRefs, "worker") {
			t.Fatalf("legal profile %s cannot accept scheduled Mission worker assignments: %+v", profile.ID, profile.OrgIdentity.RoleRefs)
		}
	}
	for _, id := range []agentcore.AgentProfileID{
		defaultLegalOrchestratorAgentID,
		defaultLegalKnowledgeAgentID,
		defaultLegalContractAgentID,
		defaultLegalComplianceAgentID,
	} {
		if _, ok := seen[id]; !ok {
			t.Fatalf("missing legal profile %s; profiles = %+v", id, profiles)
		}
	}
}

func TestEnsureDefaultLegalAgentsSeedsLegalAgents(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	cfg, err := config.LoadYAML(testConfigSource(t))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)

	if err := ensureDefaultLegalAgents(ctx, cfg, profiles); err != nil {
		t.Fatalf("ensure default legal agents: %v", err)
	}

	profile, err := profiles.GetProfile(ctx, defaultLegalOrchestratorAgentID)
	if err != nil {
		t.Fatalf("legal orchestrator profile was not initialized: %v", err)
	}
	if !hasToolRef(profile.ToolRefs, defaultLegalMCPServerToolID) {
		t.Fatalf("legal orchestrator should bind legal MCP server: %+v", profile.ToolRefs)
	}
	page, err := profiles.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{})
	if err != nil {
		t.Fatalf("query directory: %v", err)
	}
	if page.Total != 4 {
		t.Fatalf("default legal bootstrap should initialize four legal system agents, got %d entries", page.Total)
	}
}

func TestEnsureDefaultLegalAgentsRepairsScheduledMissionWorkerRole(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	cfg, err := config.LoadYAML(testConfigSource(t))
	if err != nil {
		t.Fatal(err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	if err := ensureDefaultLegalAgents(ctx, cfg, profiles); err != nil {
		t.Fatal(err)
	}
	profile, err := profiles.GetProfile(ctx, defaultLegalKnowledgeAgentID)
	if err != nil {
		t.Fatal(err)
	}
	profile.OrgIdentity.RoleRefs = []agentcore.RoleRef{{ID: "legal-knowledge"}}
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}

	if err := ensureDefaultLegalAgents(ctx, cfg, profiles); err != nil {
		t.Fatal(err)
	}
	repaired, err := profiles.GetProfile(ctx, defaultLegalKnowledgeAgentID)
	if err != nil {
		t.Fatal(err)
	}
	if !hasAgentRoleRef(repaired.OrgIdentity.RoleRefs, "legal-knowledge") || !hasAgentRoleRef(repaired.OrgIdentity.RoleRefs, "worker") {
		t.Fatalf("repaired legal roles = %+v", repaired.OrgIdentity.RoleRefs)
	}
}

func TestDefaultAgentProductionCodeDoesNotCarryFreelanceAgentDefaults(t *testing.T) {
	data, err := os.ReadFile("default_agents.go")
	if err != nil {
		t.Fatalf("read default_agents.go: %v", err)
	}
	source := string(data)
	for _, forbidden := range []string{
		"defaultFreelance",
		"policy.freelance-agent.default",
		"summary.freelance-agent.default",
		"deliverable.freelance-task.default",
		"evidence.freelance-task.required",
		"qa.freelance-agent.standard",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("default_agents.go contains %q; default Agent bootstrap must use configured neutral Agent resources", forbidden)
		}
	}
}

func hasToolRef(refs []agentcore.ToolRef, toolID agentcore.ToolID) bool {
	for _, ref := range refs {
		if ref.ToolID == toolID {
			return true
		}
	}
	return false
}

func hasAgentRoleRef(refs []agentcore.RoleRef, roleID agentcore.RoleID) bool {
	for _, ref := range refs {
		if ref.ID == roleID {
			return true
		}
	}
	return false
}
