package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	operations "orrery_v2/internal/app/operations"
	missionteam "orrery_v2/internal/mission/team"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/testkit"
)

func TestEnsureDefaultCoordinatorAgentsBackfillsMissingRuntimeSpecToPi(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	cfg, err := config.LoadYAML(testConfigSource(t, map[string]string{
		"discussion.agent.runtime_config": `{"executable":"codex-fixture"}`,
	}))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	orgs := operations.NewOrganizationCatalogStore(env.DBRouter)
	pmTemplate, err := readDefaultAgentTemplate("lead-agent.json")
	if err != nil {
		t.Fatalf("read lead template: %v", err)
	}
	legacy := defaultCoordinatorAgentProfile(defaultLeadAgentID, "pm", agentcore.RuntimeSpec{}, pmTemplate)
	if err := saveRawAgentProfile(ctx, env.UnitOfWork, legacy); err != nil {
		t.Fatalf("save legacy profile: %v", err)
	}

	if err := ensureDefaultCoordinatorAgents(ctx, cfg, profiles, orgs); err != nil {
		t.Fatalf("ensure default agents: %v", err)
	}

	repaired, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(defaultLeadAgentID))
	if err != nil {
		t.Fatalf("get lead profile: %v", err)
	}
	if repaired.RuntimeSpec.Kind != agentcore.RuntimeKindPi || repaired.RuntimeSpec.ConfigRef != "" || len(repaired.RuntimeSpec.Config) != 0 {
		t.Fatalf("missing runtime should be backfilled to pi: %+v", repaired.RuntimeSpec)
	}
	availability, err := profiles.GetAvailability(ctx, repaired.ID)
	if err != nil {
		t.Fatalf("get PM availability: %v", err)
	}
	if availability.Status != agentcore.AvailabilityAvailable {
		t.Fatalf("availability = %+v, want available", availability)
	}
}

func TestEnsureDefaultCoordinatorAgentsBackfillsMissingRuntimeConfigRef(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	cfg, err := config.LoadYAML(writeTestConfigYAML(t, `
agent_defaults:
  runtime_kind: pi
  runtime_config_ref: runtime-pi-orrery-native
`))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	orgs := operations.NewOrganizationCatalogStore(env.DBRouter)
	template, err := readDefaultAgentTemplate("lead-agent.json")
	if err != nil {
		t.Fatalf("read lead template: %v", err)
	}
	legacy := defaultCoordinatorAgentProfile(defaultLeadAgentID, "lead", agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi}, template)
	if err := saveRawAgentProfile(ctx, env.UnitOfWork, legacy); err != nil {
		t.Fatalf("save legacy profile: %v", err)
	}

	if err := ensureDefaultCoordinatorAgents(ctx, cfg, profiles, orgs); err != nil {
		t.Fatalf("ensure default agents: %v", err)
	}

	repaired, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(defaultLeadAgentID))
	if err != nil {
		t.Fatalf("get repaired lead profile: %v", err)
	}
	if repaired.RuntimeSpec.Kind != agentcore.RuntimeKindPi || repaired.RuntimeSpec.ConfigRef != "runtime-pi-orrery-native" {
		t.Fatalf("missing runtime config ref was not repaired: %+v", repaired.RuntimeSpec)
	}
}

func TestEnsureDefaultCoordinatorAgentsRepairsLegacyDefaultRuntimeIdentity(t *testing.T) {
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
	legacy := defaultCoordinatorAgentProfile(defaultLeadAgentID, "lead", agentcore.RuntimeSpec{}, template)
	if err := saveRawAgentProfile(ctx, env.UnitOfWork, legacy); err != nil {
		t.Fatalf("save legacy profile: %v", err)
	}
	if err := profiles.SaveAvailability(ctx, agentcore.AgentAvailability{
		AgentProfileID:  legacy.ID,
		AgentInstanceID: agentcore.AgentInstanceID(legacy.ID.String() + ":default"),
		Status:          agentcore.AvailabilityAvailable,
		Source:          "system-bootstrap",
		UpdatedAt:       time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save legacy availability: %v", err)
	}

	if err := ensureDefaultCoordinatorAgents(ctx, cfg, profiles, orgs); err != nil {
		t.Fatalf("ensure default agents: %v", err)
	}

	availability, err := profiles.GetAvailability(ctx, legacy.ID)
	if err != nil {
		t.Fatalf("get repaired availability: %v", err)
	}
	if availability.Status != agentcore.AvailabilityAvailable || availability.AgentInstanceID != agentcore.AgentInstanceID(legacy.ID) {
		t.Fatalf("legacy default availability was not repaired to stable identity: %+v", availability)
	}
}

func TestEnsureDefaultCoordinatorAgentsRepairsLegacySystemRoleStaffingIdentity(t *testing.T) {
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
	legacy := defaultCoordinatorAgentProfile(defaultLeadAgentID, "lead", agentcore.RuntimeSpec{}, template)
	legacy.OrgIdentity.RoleRefs = nil
	legacy.OrgIdentity.AuthorityScope = ""
	if err := saveRawAgentProfile(ctx, env.UnitOfWork, legacy); err != nil {
		t.Fatalf("save legacy incomplete lead profile: %v", err)
	}

	if err := ensureDefaultCoordinatorAgents(ctx, cfg, profiles, orgs); err != nil {
		t.Fatalf("ensure default agents: %v", err)
	}

	repaired, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(defaultLeadAgentID))
	if err != nil {
		t.Fatalf("get repaired lead profile: %v", err)
	}
	if len(repaired.OrgIdentity.RoleRefs) == 0 || repaired.OrgIdentity.AuthorityScope == "" {
		t.Fatalf("lead org identity was not repaired: %+v", repaired.OrgIdentity)
	}
	staffing := missionteam.NewService(missionteam.ServiceOptions{
		UnitOfWork:  env.UnitOfWork,
		ProfileRepo: profiles,
		Provisioner: agentcore.NewStaticInstanceProvisioner(nil),
		Clock:       env.Clock,
		IDGenerator: env.IDGenerator,
		Logger:      env.Logger,
	})
	if _, err := staffing.Appoint(ctx, missionteam.AppointmentCommand{
		IDempotencyKey: "mission-bootstrap-lead",
		MissionID:      "mission-legacy-system-role",
		ProfileID:      repaired.ID,
		Role:           missionteam.MissionRoleLead,
		AuthorityScope: "mission_coordination",
		AppointedBy:    missionteam.ActorRef{ID: "mission-bootstrap", Kind: missionteam.ActorKindSystem},
		AllowAI:        true,
	}); errors.Is(err, missionteam.ErrPolicyDenied) || err != nil {
		t.Fatalf("repaired lead should be staffable, got %v", err)
	}
}

func TestEnsureDefaultCoordinatorAgentsRepairsDirectoryRejectedLegacyProfile(t *testing.T) {
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
	legacy := defaultCoordinatorAgentProfile(defaultLeadAgentID, "lead", agentcore.RuntimeSpec{}, template)
	legacy.AgentKind = ""
	legacy.OrgIdentity.RoleRefs = nil
	legacy.OrgIdentity.AuthorityScope = ""
	legacy.Persona.Backstory = "custom lead prompt should survive repair"
	if err := saveRawAgentProfile(ctx, env.UnitOfWork, legacy); err != nil {
		t.Fatalf("save directory-rejected legacy lead profile: %v", err)
	}

	if err := ensureDefaultCoordinatorAgents(ctx, cfg, profiles, orgs); err != nil {
		t.Fatalf("ensure default agents: %v", err)
	}

	repaired, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(defaultLeadAgentID))
	if err != nil {
		t.Fatalf("get repaired lead profile: %v", err)
	}
	if repaired.AgentKind != agentcore.AgentKindAI ||
		len(repaired.OrgIdentity.RoleRefs) == 0 ||
		repaired.OrgIdentity.AuthorityScope == "" {
		t.Fatalf("directory-rejected system profile was not repaired: %+v", repaired)
	}
	if repaired.Persona.Backstory != "custom lead prompt should survive repair" {
		t.Fatalf("custom prompt should be preserved, got %q", repaired.Persona.Backstory)
	}
}

func TestEnsureDefaultCoordinatorAgentsRepairsLegacyTemplateDetails(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	cfg, err := config.LoadYAML(testConfigSource(t))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	orgs := operations.NewOrganizationCatalogStore(env.DBRouter)
	legacy := agentcore.AgentProfile{
		ID:          defaultQAPlanAgentID,
		RealName:    "QA Plan Agent",
		DisplayName: "Custom QA Planner",
		AgentKind:   agentcore.AgentKindAI,
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef:   agentcore.OrganizationRef{ID: agentcore.OrganizationID(defaultCoordinatorOrganizationID)},
			DepartmentRef:     agentcore.DepartmentRef{ID: agentcore.DepartmentID(defaultCoordinatorDepartmentID)},
			PositionRef:       agentcore.PositionRef{ID: agentcore.PositionID(defaultCoordinatorPositionID)},
			RoleRefs:          []agentcore.RoleRef{{ID: "qa-plan"}},
			EmploymentType:    agentcore.EmploymentTypeSystem,
			SupervisionPolicy: agentcore.SupervisionPolicyRoot,
			AuthorityScope:    "mission_coordination",
		},
		Persona: agentcore.AgentPersona{
			Backstory: "custom prompt should be preserved",
		},
		PersonalMemoryProfileRef: agentcore.MemoryProfileRef{
			MemoryProfileID: "memory-" + defaultQAPlanAgentID,
			OwnerAgentID:    agentcore.AgentID(defaultQAPlanAgentID),
			MemoryKind:      agentcore.MemoryKindPersonal,
		},
		RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex},
		Enabled:     true,
	}
	if err := saveRawAgentProfile(ctx, env.UnitOfWork, legacy); err != nil {
		t.Fatalf("save legacy profile: %v", err)
	}

	if err := ensureDefaultCoordinatorAgents(ctx, cfg, profiles, orgs); err != nil {
		t.Fatalf("ensure default agents: %v", err)
	}

	repaired, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(defaultQAPlanAgentID))
	if err != nil {
		t.Fatalf("get repaired profile: %v", err)
	}
	if !containsChinese(repaired.DisplayName) || repaired.Persona.Backstory != "custom prompt should be preserved" {
		t.Fatalf("managed Chinese identity and custom prompt should be preserved: %+v", repaired)
	}
	if !containsChinese(repaired.Persona.ResponsibilityBoundary) || repaired.Persona.BehaviorBoundary == "" {
		t.Fatalf("template persona boundaries were not repaired: %+v", repaired.Persona)
	}
	if !repaired.BaseAgent.InheritsBaseAgent || !repaired.BaseAgent.AuditRequired || repaired.BaseAgent.LLMAPIKeyRef == "" {
		t.Fatalf("BaseAgent contract was not repaired: %+v", repaired.BaseAgent)
	}
	if len(repaired.SkillRefs) == 0 || repaired.SkillRefs[0].SkillID == "" {
		t.Fatalf("template skill refs were not repaired: %+v", repaired.SkillRefs)
	}
}

func TestEnsureDefaultCoordinatorAgentsKeepsLegacyDisabledSystemProfilesUnavailable(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	cfg, err := config.LoadYAML(testConfigSource(t))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	orgs := operations.NewOrganizationCatalogStore(env.DBRouter)
	for _, seed := range []struct {
		id           string
		templateFile string
		role         string
	}{
		{id: defaultLeadAgentID, templateFile: "lead-agent.json", role: "lead"},
	} {
		template, err := readDefaultAgentTemplate(seed.templateFile)
		if err != nil {
			t.Fatalf("read template %s: %v", seed.templateFile, err)
		}
		legacy := defaultCoordinatorAgentProfile(seed.id, seed.role, agentcore.RuntimeSpec{}, template)
		legacy.Enabled = false
		if err := saveRawAgentProfile(ctx, env.UnitOfWork, legacy); err != nil {
			t.Fatalf("save legacy profile %s: %v", seed.id, err)
		}
	}

	if err := ensureDefaultCoordinatorAgents(ctx, cfg, profiles, orgs); err != nil {
		t.Fatalf("ensure default agents: %v", err)
	}

	for _, id := range []agentcore.AgentProfileID{defaultLeadAgentID} {
		profile, err := profiles.GetProfile(ctx, id)
		if err != nil {
			t.Fatalf("get profile %s: %v", id, err)
		}
		if profile.Enabled {
			t.Fatalf("disabled default profile %s should not be silently re-enabled: %+v", id, profile)
		}
	}
	page, err := profiles.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{EnabledOnly: true, IncludeAI: true})
	if err != nil {
		t.Fatalf("query enabled directory: %v", err)
	}
	for _, entry := range page.Entries {
		if entry.AgentProfileID == defaultLeadAgentID {
			t.Fatalf("disabled default system agents should not appear in enabled directory: %+v", page.Entries)
		}
	}
}

func TestEnsureDefaultCoordinatorAgentsKeepsExplicitlyDisabledSystemProfile(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	cfg, err := config.LoadYAML(testConfigSource(t))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	orgs := operations.NewOrganizationCatalogStore(env.DBRouter)
	pmTemplate, err := readDefaultAgentTemplate("lead-agent.json")
	if err != nil {
		t.Fatalf("read lead template: %v", err)
	}
	disabledPM := defaultCoordinatorAgentProfile(defaultLeadAgentID, "pm", agentcore.RuntimeSpec{}, pmTemplate)
	disabledPM.Enabled = false
	if err := saveRawAgentProfile(ctx, env.UnitOfWork, disabledPM); err != nil {
		t.Fatalf("save disabled lead profile: %v", err)
	}
	if err := profiles.SaveAvailability(ctx, agentcore.AgentAvailability{
		AgentProfileID:  disabledPM.ID,
		AgentInstanceID: agentcore.AgentInstanceID(disabledPM.ID.String() + ":runtime"),
		Status:          agentcore.AvailabilityDisabled,
		Source:          "operations.workforce",
		Reason:          "workforce member disabled",
		UpdatedAt:       time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save disabled PM availability: %v", err)
	}

	if err := ensureDefaultCoordinatorAgents(ctx, cfg, profiles, orgs); err != nil {
		t.Fatalf("ensure default agents: %v", err)
	}

	pm, err := profiles.GetProfile(ctx, disabledPM.ID)
	if err != nil {
		t.Fatalf("get lead profile: %v", err)
	}
	if pm.Enabled {
		t.Fatalf("explicitly disabled lead profile should stay disabled: %+v", pm)
	}
}

func TestEnsureDefaultCoordinatorAgentsBuildsProfilesFromMigratedTemplates(t *testing.T) {
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

	lead, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(defaultLeadAgentID))
	if err != nil {
		t.Fatalf("get lead profile: %v", err)
	}
	if lead.RealName != "顾明澈" ||
		lead.RoleName != "Mission Control lead and collaboration orchestrator" ||
		!strings.Contains(lead.BaseAgent.SystemPrompt, "使用当前已绑定的 Mission 工具") ||
		strings.TrimSpace(lead.Persona.Backstory) != "" {
		t.Fatalf("lead profile was not hydrated from migrated template: %+v", lead)
	}
	if lead.QAEnabled {
		t.Fatalf("lead agent should default qa_enabled=false: %+v", lead)
	}
	if !agentToolRefs(lead)["mission_prepare"] ||
		!agentToolRefs(lead)["mission_coordinator_plan_parallel"] ||
		!agentToolRefs(lead)["discussion_talent_list"] ||
		!agentToolRefs(lead)["discussion_member_add"] ||
		!agentToolRefs(lead)["discussion_close"] ||
		!agentToolRefs(lead)["discussion_ack"] {
		t.Fatalf("lead Mission tools should be profile-bound, refs = %+v", lead.ToolRefs)
	}
	if agentToolRefs(lead)["mission_brief_create"] || agentToolRefs(lead)["mission_task_create_root"] {
		t.Fatalf("lead profile should expose atomic mission_prepare instead of split preparation tools, refs = %+v", lead.ToolRefs)
	}
	if agentToolRefs(lead)["mission_context_list"] || agentToolRefs(lead)["mission_context_select"] || agentToolRefs(lead)["mission_task_delegate"] {
		t.Fatalf("lead profile should not expose unresolved context or delegate tools, refs = %+v", lead.ToolRefs)
	}
	if _, err := profiles.GetProfile(ctx, agentcore.AgentProfileID("staffing-agent")); err == nil {
		t.Fatalf("dedicated staffing profile %q should not be initialized by default", "staffing-agent")
	}
	coordinator, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(defaultCoordinatorAgentID))
	if err != nil {
		t.Fatalf("get coordinator profile: %v", err)
	}
	if coordinator.RoleName != "Parallel work coordinator" ||
		!strings.Contains(coordinator.BaseAgent.SystemPrompt, "currently bound coordination tool") ||
		strings.TrimSpace(coordinator.Persona.Backstory) != "" {
		t.Fatalf("Coordinator profile was not hydrated from migrated template: %+v", coordinator)
	}
	if coordinator.RuntimeSpec.Kind != agentcore.RuntimeKindPi || coordinator.RuntimeSpec.ConfigRef != "" || len(coordinator.RuntimeSpec.Config) != 0 {
		t.Fatalf("Coordinator default runtime should use pi: %+v", coordinator.RuntimeSpec)
	}
	if coordinator.QAEnabled {
		t.Fatalf("Coordinator should default qa_enabled=false: %+v", coordinator)
	}
}

func TestEnsureDefaultCoordinatorAgentsRefreshesSystemWorkflowToolBindings(t *testing.T) {
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
	lead, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(defaultLeadAgentID))
	if err != nil {
		t.Fatalf("get lead profile: %v", err)
	}
	if !agentToolRefs(lead)["mission_prepare"] || !agentToolRefs(lead)["discussion_ack"] {
		t.Fatalf("lead profile missing seeded tool refs before unbind: %+v", lead.ToolRefs)
	}
	lead.ToolRefs = []agentcore.ToolRef{{
		ToolID:          "discussion_ack",
		PermissionScope: "discussion.write",
		BindingPolicy:   "allow",
	}}
	if err := profiles.SaveProfile(ctx, lead); err != nil {
		t.Fatalf("save configured lead profile: %v", err)
	}

	if err := ensureDefaultCoordinatorAgents(ctx, cfg, profiles, orgs); err != nil {
		t.Fatalf("ensure default agents after configured unbind: %v", err)
	}

	reloaded, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(defaultLeadAgentID))
	if err != nil {
		t.Fatalf("reload lead profile: %v", err)
	}
	if !agentToolRefs(reloaded)["mission_prepare"] || !agentToolRefs(reloaded)["discussion_ack"] {
		t.Fatalf("system workflow profile should refresh missing template tool refs: %+v", reloaded.ToolRefs)
	}
}

func TestEnsureDefaultCoordinatorAgentsPrunesRetiredWorkflowToolBindings(t *testing.T) {
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
	lead, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(defaultLeadAgentID))
	if err != nil {
		t.Fatalf("get lead profile: %v", err)
	}
	lead.ToolRefs = append(lead.ToolRefs,
		agentcore.ToolRef{ToolID: "mission_context_list", PermissionScope: "mission.write"},
		agentcore.ToolRef{ToolID: "mission_context_select", PermissionScope: "mission.write"},
		agentcore.ToolRef{ToolID: "mission_task_delegate", PermissionScope: "mission.write"},
	)
	if err := profiles.SaveProfile(ctx, lead); err != nil {
		t.Fatalf("save legacy lead profile: %v", err)
	}

	if err := ensureDefaultCoordinatorAgents(ctx, cfg, profiles, orgs); err != nil {
		t.Fatalf("ensure default agents after legacy tools: %v", err)
	}

	reloaded, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(defaultLeadAgentID))
	if err != nil {
		t.Fatalf("reload lead profile: %v", err)
	}
	if agentToolRefs(reloaded)["mission_context_list"] || agentToolRefs(reloaded)["mission_context_select"] || agentToolRefs(reloaded)["mission_task_delegate"] {
		t.Fatalf("retired workflow tools should be pruned from default lead profile: %+v", reloaded.ToolRefs)
	}
	if !agentToolRefs(reloaded)["mission_prepare"] || !agentToolRefs(reloaded)["discussion_ack"] {
		t.Fatalf("active workflow tools should remain bound: %+v", reloaded.ToolRefs)
	}
}

func TestEnsureDefaultCoordinatorAgentsRefreshesLegacySystemPrompt(t *testing.T) {
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
	lead, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(defaultLeadAgentID))
	if err != nil {
		t.Fatalf("get lead profile: %v", err)
	}
	lead.BaseAgent.SystemPrompt = "全局 BaseAgent 模板：所有 Orrery Agent 必须经同一审计壳运行，遵守权限、资源范围、生命周期信号、运行追踪、事件 hook、证据和交付提交要求。Memory 占位：TODO(memory-team) 注入个人和组织记忆摘要。Knowledge 占位：TODO(knowledge-team) 注入可用知识库上下文。 你是 Orrery 系统托管的 Agent「顾明澈」。 岗位职责：Mission Control lead and collaboration orchestrator。"
	if err := profiles.SaveProfile(ctx, lead); err != nil {
		t.Fatalf("save legacy lead profile: %v", err)
	}

	if err := ensureDefaultCoordinatorAgents(ctx, cfg, profiles, orgs); err != nil {
		t.Fatalf("ensure default agents after legacy prompt: %v", err)
	}

	reloaded, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(defaultLeadAgentID))
	if err != nil {
		t.Fatalf("reload lead profile: %v", err)
	}
	if strings.Contains(reloaded.BaseAgent.SystemPrompt, "全局 BaseAgent 模板") ||
		!strings.Contains(reloaded.BaseAgent.SystemPrompt, "你是顾明澈，Mission Control 主管") ||
		!strings.Contains(reloaded.BaseAgent.SystemPrompt, "使用当前已绑定的 Mission 工具") {
		t.Fatalf("legacy system prompt was not refreshed from template: %s", reloaded.BaseAgent.SystemPrompt)
	}
}

func TestEnsureDefaultCoordinatorAgentsPreservesExplicitEmptyResourceBindings(t *testing.T) {
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
	lead, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(defaultLeadAgentID))
	if err != nil {
		t.Fatalf("get lead profile: %v", err)
	}
	lead.SkillRefs = []agentcore.SkillRef{}
	lead.ToolRefs = []agentcore.ToolRef{}
	if err := profiles.SaveProfile(ctx, lead); err != nil {
		t.Fatalf("save explicitly unbound lead profile: %v", err)
	}

	if err := ensureDefaultCoordinatorAgents(ctx, cfg, profiles, orgs); err != nil {
		t.Fatalf("ensure default agents after explicit unbind: %v", err)
	}

	reloaded, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(defaultLeadAgentID))
	if err != nil {
		t.Fatalf("reload lead profile: %v", err)
	}
	if len(reloaded.SkillRefs) != 0 || len(reloaded.ToolRefs) != 0 {
		t.Fatalf("explicit empty bindings should not be restored by defaults, skills=%+v tools=%+v", reloaded.SkillRefs, reloaded.ToolRefs)
	}
}

func TestEnsureDefaultCoordinatorAgentsRestoresDeletedSystemWorkflowProfiles(t *testing.T) {
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
	for _, id := range []agentcore.AgentProfileID{defaultLeadAgentID, defaultCoordinatorAgentID} {
		if err := profiles.DeleteProfile(ctx, id, "legacy bad delete"); err != nil {
			t.Fatalf("delete default profile %s: %v", id, err)
		}
	}

	if err := ensureDefaultCoordinatorAgents(ctx, cfg, profiles, orgs); err != nil {
		t.Fatalf("ensure default agents after delete: %v", err)
	}

	for _, id := range []agentcore.AgentProfileID{defaultLeadAgentID, defaultCoordinatorAgentID} {
		profile, err := profiles.GetProfile(ctx, id)
		if err != nil {
			t.Fatalf("restored default profile %s missing: %v", id, err)
		}
		if !profile.Enabled {
			t.Fatalf("restored default profile %s should be enabled: %+v", id, profile)
		}
		deleted, err := profiles.IsProfileDeleted(ctx, id)
		if err != nil {
			t.Fatalf("is profile deleted %s: %v", id, err)
		}
		if deleted {
			t.Fatalf("restored default profile %s should clear delete tombstone", id)
		}
		availability, err := profiles.GetAvailability(ctx, id)
		if err != nil {
			t.Fatalf("get availability %s: %v", id, err)
		}
		if availability.Status != agentcore.AvailabilityAvailable || availability.AgentInstanceID == "" {
			t.Fatalf("restored default profile %s should be available: %+v", id, availability)
		}
	}
}
