package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/knowledgeadmin"
	devicedirectory "orrery_v2/internal/device/directory"
	deviceidentity "orrery_v2/internal/device/identity"
	knowledgecore "orrery_v2/internal/knowledge/core"
	memcore "orrery_v2/internal/memory/core"
	memtestkit "orrery_v2/internal/memory/testkit"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/testkit"
	skillstd "orrery_v2/internal/resource/skill/std"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestNewPersonalAssistantProfileCreatesIndependentAgentIdentity(t *testing.T) {
	baseAgent := agentcore.DefaultBaseAgentSpec("组织协调智能体", "组织协调")
	baseAgent.SystemPrompt = "Act as the organization lead."
	template := agentcore.AgentProfile{
		ID:        "lead-agent",
		RealName:  "组织协调智能体",
		AgentKind: agentcore.AgentKindAI,
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef:   agentcore.OrganizationRef{ID: "tenant-a"},
			DepartmentRef:     agentcore.DepartmentRef{ID: "department-a"},
			PositionRef:       agentcore.PositionRef{ID: "position-a"},
			RoleRefs:          []agentcore.RoleRef{{ID: "organization-lead"}},
			ManagerAgentRef:   agentcore.AgentProfileRef{ID: "executive-agent"},
			ReportingLineRef:  agentcore.ReportingLineRef{ID: "executive-line"},
			AuthorityScope:    "organization_admin",
			EmploymentType:    agentcore.EmploymentTypeSystem,
			SupervisionPolicy: agentcore.SupervisionPolicyRoot,
		},
		Persona:                   agentcore.AgentPersona{ResponsibilityBoundary: "负责组织协调。"},
		BaseAgent:                 baseAgent,
		PersonalKnowledgeBaseID:   "knowledge-lead",
		CollectiveMemoryScopeRefs: []agentcore.MemoryProfileRef{{MemoryProfileID: "memory-organization", MemoryKind: agentcore.MemoryKindOrganization}},
		SkillRefs:                 []agentcore.SkillRef{{SkillID: "planning"}},
		ToolRefs:                  []agentcore.ToolRef{{ToolID: "knowledge_search"}, {ToolID: "exec_command"}},
		MCPAuthorizations:         []agentcore.MCPAuthorization{{ServerToolID: "organization-mcp", CredentialValue: "credential"}},
		RuntimeSpec:               agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi},
		WorkspaceRoot:             "/srv/workspaces/template",
		Enabled:                   true,
	}
	profileID := agentcore.AgentProfileID(discussionsession.PersonalAssistantAgentID("tenant-a", "owner-a"))

	profile := newPersonalAssistantProfile(profileID, "tenant-a", "owner-a", template)

	if profile.ID != profileID || profile.PersonalAssistantOwnerID != "owner-a" || profile.PersonalAssistantTenantID != "tenant-a" {
		t.Fatalf("personal assistant identity = %+v", profile)
	}
	if !profile.Placement.IsInternal(profile.OrgIdentity) {
		t.Fatalf("personal assistant placement = %+v", profile.Placement)
	}
	if profile.DisplayName != defaultPersonalAssistantDisplayName || strings.Contains(profile.BaseAgent.SystemPrompt, "organization lead") {
		t.Fatalf("personal assistant presentation = name %q prompt %q", profile.DisplayName, profile.BaseAgent.SystemPrompt)
	}
	if string(profile.PersonalMemoryProfileRef.MemoryProfileID) != discussionsession.PersonalAssistantMemoryProfileID(profileID.String()) {
		t.Fatalf("personal memory = %+v", profile.PersonalMemoryProfileRef)
	}
	if profile.PersonalKnowledgeBaseID != "" || profile.WorkspaceRoot != "" || len(profile.SkillRefs) != 2 ||
		!skillstd.HasWebResearchSkillRef(profile.SkillRefs) {
		t.Fatalf("seeded capabilities = knowledge %q skills %+v tools %+v", profile.PersonalKnowledgeBaseID, profile.SkillRefs, profile.ToolRefs)
	}
	if profile.OrgIdentity.OrganizationRef.ID != "tenant-a" || profile.OrgIdentity.DepartmentRef.ID != "" || profile.OrgIdentity.PositionRef.ID != "" ||
		profile.OrgIdentity.EmploymentType != agentcore.EmploymentTypeAIAgent || profile.OrgIdentity.SupervisionPolicy != agentcore.SupervisionPolicySupervised ||
		!profile.OrgIdentity.ManagerAgentRef.IsZero() || profile.OrgIdentity.ReportingLineRef.ID != "" ||
		len(profile.OrgIdentity.RoleRefs) != 0 || profile.OrgIdentity.AuthorityScope != "" ||
		len(profile.CollectiveMemoryScopeRefs) != 0 || len(profile.MCPAuthorizations) != 0 || profile.WorkspaceRoot != "" {
		t.Fatalf("inherited authority survived: org=%+v collective=%+v mcp=%+v workspace=%q", profile.OrgIdentity, profile.CollectiveMemoryScopeRefs, profile.MCPAuthorizations, profile.WorkspaceRoot)
	}
	assertPersonalAssistantReadTool(t, profile.ToolRefs, toolstd.KnowledgeSearch, toolstd.PermissionKnowledgeRead)
	assertPersonalAssistantReadTool(t, profile.ToolRefs, toolstd.MemorySearch, toolstd.PermissionMemoryRead)
	assertPersonalAssistantReadTool(t, profile.ToolRefs, toolstd.RSILearningPlan, toolstd.PermissionRSILearningRead)
	assertPersonalAssistantReadTool(t, profile.ToolRefs, toolstd.SessionSearch, toolstd.PermissionDiscussionRead)
	assertPersonalAssistantReadTool(t, profile.ToolRefs, toolstd.SessionRead, toolstd.PermissionDiscussionRead)
	assertPersonalAssistantReadTool(t, profile.ToolRefs, toolstd.EnterpriseGraphBuildContextBundle, toolstd.PermissionEnterpriseGraphRead)
	assertPersonalAssistantReadTool(t, profile.ToolRefs, toolstd.EnterpriseGraphSearchObjects, toolstd.PermissionEnterpriseGraphRead)
	assertPersonalAssistantReadTool(t, profile.ToolRefs, toolstd.WebSearch, toolstd.PermissionWebRead)
	assertPersonalAssistantReadTool(t, profile.ToolRefs, toolstd.WebFetch, toolstd.PermissionWebRead)
	assertPersonalAssistantReadTool(t, profile.ToolRefs, toolstd.PersonalBrowserRead, toolstd.PermissionPersonalBrowserRead)
	assertPersonalAssistantReadTool(t, profile.ToolRefs, toolstd.PersonalBrowserControl, toolstd.PermissionPersonalBrowserControl)
	if errs := agentcore.ValidateAgentProfile(profile); len(errs) != 0 {
		t.Fatalf("personal assistant profile validation = %+v", errs)
	}
}

func TestPersonalAssistantServiceEnsuresIndependentKnowledgeBase(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	profileID := agentcore.AgentProfileID(discussionsession.PersonalAssistantAgentID("tenant-a", "owner-a"))
	profile := newPersonalAssistantProfile(profileID, "tenant-a", "owner-a", agentcore.AgentProfile{
		PersonalKnowledgeBaseID: "knowledge-template",
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef: agentcore.OrganizationRef{ID: "tenant-a"},
			DepartmentRef:   agentcore.DepartmentRef{ID: "operations"},
			PositionRef:     agentcore.PositionRef{ID: "worker"},
		},
		BaseAgent: agentcore.DefaultBaseAgentSpec("Assistant", "Assistant"),
		SkillRefs: []agentcore.SkillRef{}, ToolRefs: []agentcore.ToolRef{}, RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi},
	})
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	knowledge := &recordingDefaultKnowledgeEnsurer{base: knowledgeadmin.KnowledgeBaseDTO{ID: "knowledge-personal-assistant"}}
	service := &productionPersonalAssistantService{profiles: profiles, knowledge: knowledge, knowledgeRoot: t.TempDir()}

	updated, err := service.ensureKnowledgeBase(ctx, profile)
	if err != nil {
		t.Fatalf("ensure knowledge base: %v", err)
	}
	if updated.PersonalKnowledgeBaseID != knowledge.base.ID || knowledge.cmd.OwnerID != profileID.String() {
		t.Fatalf("independent knowledge = profile %+v ensure owner %q", updated, knowledge.cmd.OwnerID)
	}
	stored, err := profiles.GetProfile(ctx, profileID)
	if err != nil || stored.PersonalKnowledgeBaseID != knowledge.base.ID {
		t.Fatalf("stored knowledge reference = %+v err=%v", stored, err)
	}
}

func TestPersonalAssistantServicePreservesOwnedKnowledgeBase(t *testing.T) {
	profile := agentcore.AgentProfile{ID: "A000018", PersonalKnowledgeBaseID: "knowledge-owned"}
	knowledge := &recordingDefaultKnowledgeEnsurer{base: knowledgeadmin.KnowledgeBaseDTO{ID: "knowledge-new"}}
	service := &productionPersonalAssistantService{
		profiles:  agentcore.NewPlatformProfileRepository(nil),
		knowledge: knowledge,
		knowledgeBases: fixedPersonalAssistantKnowledgeLookup{bases: map[knowledgecore.KnowledgeBaseID]knowledgecore.KnowledgeBase{
			"knowledge-owned": {ID: "knowledge-owned", OwnerType: knowledgecore.OwnerAgent, OwnerID: "A000018", Status: knowledgecore.KnowledgeActive},
		}},
	}

	updated, err := service.ensureKnowledgeBase(context.Background(), profile)
	if err != nil {
		t.Fatal(err)
	}
	if updated.PersonalKnowledgeBaseID != "knowledge-owned" || knowledge.cmd.OwnerID != "" {
		t.Fatalf("owned knowledge was replaced: profile=%+v ensure=%+v", updated, knowledge.cmd)
	}
}

func TestPersonalAssistantServiceEnsuresIndependentMemoryProfile(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	service := &productionPersonalAssistantService{memory: store}
	profileID := agentcore.AgentProfileID(discussionsession.PersonalAssistantAgentID("tenant-a", "owner-a"))
	template := agentcore.AgentProfile{
		OrgIdentity: agentcore.AgentOrgIdentity{DepartmentRef: agentcore.DepartmentRef{ID: "operations"}, PositionRef: agentcore.PositionRef{ID: "worker"}},
		BaseAgent:   agentcore.DefaultBaseAgentSpec("默认助理", "默认助理"),
		SkillRefs:   []agentcore.SkillRef{}, ToolRefs: []agentcore.ToolRef{}, RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi},
	}
	profile := newPersonalAssistantProfile(profileID, "tenant-a", "owner-a", template)
	if errs := agentcore.ValidateAgentProfile(profile); len(errs) != 0 {
		t.Fatalf("empty-template personal assistant validation = %+v", errs)
	}

	if err := service.ensureMemoryProfile(ctx, profile); err != nil {
		t.Fatalf("ensure memory profile: %v", err)
	}
	memoryProfile, err := store.GetProfile(ctx, profile.PersonalMemoryProfileRef.MemoryProfileID)
	if err != nil {
		t.Fatalf("get memory profile: %v", err)
	}
	if memoryProfile.OwnerRef.AgentID != agentcore.AgentID(profileID) ||
		memoryProfile.DefaultScope.ID != profileID.String() ||
		memoryProfile.VisibilityPolicy != (memcore.VisibilityPolicy{Mode: memcore.VisibilityOwnerOnly, ScopeKind: memcore.ScopeAgent, ScopeID: profileID.String()}) ||
		!memoryProfile.Enabled {
		t.Fatalf("independent memory profile = %+v", memoryProfile)
	}
}

func TestReconcileExistingPersonalAssistantOwnershipRepairsInheritedConfiguration(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	knowledgeRepo, knowledgeCommand := newTestKnowledgeCommand(t, env)
	knowledgeRoot := t.TempDir()
	assistantBaseDir := t.TempDir()
	templateSpec, err := readDefaultAgentTemplate("lead-agent.json")
	if err != nil {
		t.Fatalf("read lead template: %v", err)
	}
	template := defaultCoordinatorAgentProfile(defaultLeadAgentID, "lead", agentcore.RuntimeSpec{}, templateSpec)
	templateBase, err := ensureDefaultKnowledgeBase(ctx, knowledgeCommand, knowledgecore.OwnerAgent, template.ID.String(), knowledgeRoot)
	if err != nil {
		t.Fatalf("ensure template knowledge base: %v", err)
	}

	tenantID := "tenant-personal-assistant-ownership-reconcile"
	profileIDs := make([]agentcore.AgentProfileID, 0, 2)
	for _, ownerID := range []string{"owner-reconcile-one", "owner-reconcile-two"} {
		profileID := agentcore.AgentProfileID(discussionsession.PersonalAssistantAgentID(tenantID, ownerID))
		profile := newPersonalAssistantProfile(profileID, tenantID, ownerID, template)
		// Simulate an existing session-only assistant before learning binding.
		profile.ToolRefs = filterPersonalAssistantTestLearningTool(profile.ToolRefs)
		profile.PersonalKnowledgeBaseID = templateBase.ID
		profile.OrgIdentity.OrganizationRef = agentcore.OrganizationRef{ID: "template-organization"}
		profile.OrgIdentity.DepartmentRef = agentcore.DepartmentRef{ID: "template-department"}
		profile.OrgIdentity.PositionRef = agentcore.PositionRef{ID: "template-position"}
		profile.OrgIdentity.ManagerAgentRef = agentcore.AgentProfileRef{ID: template.ID}
		profile.OrgIdentity.ReportingLineRef = agentcore.ReportingLineRef{ID: "template-line"}
		profile.OrgIdentity.RoleRefs = []agentcore.RoleRef{{ID: "template-role"}}
		profile.OrgIdentity.AuthorityScope = "template_authority"
		profile.OrgIdentity.EmploymentType = agentcore.EmploymentTypeSystem
		profile.OrgIdentity.SupervisionPolicy = agentcore.SupervisionPolicyRoot
		profile.CollectiveMemoryScopeRefs = []agentcore.MemoryProfileRef{{MemoryProfileID: "memory-template", MemoryKind: agentcore.MemoryKindOrganization}}
		profile.MCPAuthorizations = []agentcore.MCPAuthorization{{ServerToolID: "template-mcp", CredentialValue: "template-secret"}}
		profile.WorkspaceRoot = t.TempDir()
		if err := profiles.SaveProfile(ctx, profile); err != nil {
			t.Fatalf("save legacy personal assistant %s: %v", profileID, err)
		}
		profileIDs = append(profileIDs, profileID)
	}

	knowledgeFailure := errors.New("knowledge provider rate limited")
	failureService := &productionPersonalAssistantService{
		profiles:      profiles,
		knowledge:     failingDefaultKnowledgeEnsurer{err: knowledgeFailure},
		knowledgeRoot: knowledgeRoot,
		config:        config.Config{BaseDir: assistantBaseDir},
	}
	if err := failureService.reconcileExistingProfiles(ctx); !errors.Is(err, knowledgeFailure) {
		t.Fatalf("reconcile knowledge failure = %v, want %v", err, knowledgeFailure)
	}
	for _, profileID := range profileIDs {
		profile, err := profiles.GetProfile(ctx, profileID)
		if err != nil {
			t.Fatalf("get authority-reconciled personal assistant %s: %v", profileID, err)
		}
		assertPersonalAssistantReadTool(t, profile.ToolRefs, toolstd.RSILearningPlan, toolstd.PermissionRSILearningRead)
		if profile.OrgIdentity.OrganizationRef.ID != agentcore.OrganizationID(tenantID) || profile.OrgIdentity.DepartmentRef.ID != "" || profile.OrgIdentity.PositionRef.ID != "" ||
			profile.OrgIdentity.EmploymentType != agentcore.EmploymentTypeAIAgent || profile.OrgIdentity.SupervisionPolicy != agentcore.SupervisionPolicySupervised ||
			!profile.OrgIdentity.ManagerAgentRef.IsZero() || profile.OrgIdentity.ReportingLineRef.ID != "" ||
			len(profile.OrgIdentity.RoleRefs) != 0 || profile.OrgIdentity.AuthorityScope != "" ||
			len(profile.CollectiveMemoryScopeRefs) != 0 || len(profile.MCPAuthorizations) != 0 {
			t.Fatalf("personal assistant %s retained inherited authority after knowledge failure: %+v", profileID, profile)
		}
	}

	successService := &productionPersonalAssistantService{
		profiles:       profiles,
		knowledge:      knowledgeCommand,
		knowledgeBases: knowledgeRepo,
		knowledgeRoot:  knowledgeRoot,
		config:         config.Config{BaseDir: assistantBaseDir},
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := successService.reconcileExistingProfiles(ctx); err != nil {
			t.Fatalf("reconcile personal assistant ownership attempt %d: %v", attempt+1, err)
		}
	}

	for _, profileID := range profileIDs {
		profile, err := profiles.GetProfile(ctx, profileID)
		if err != nil {
			t.Fatalf("get reconciled personal assistant %s: %v", profileID, err)
		}
		page, err := knowledgeRepo.ListKnowledgeBases(ctx, knowledgecore.KnowledgeBaseQuery{OwnerType: knowledgecore.OwnerAgent, OwnerID: profileID.String()})
		if err != nil {
			t.Fatalf("list personal assistant %s knowledge bases: %v", profileID, err)
		}
		if len(page.Items) != 1 || !page.Items[0].IsDefault || page.Items[0].Visibility != knowledgecore.VisibilityPrivate ||
			profile.PersonalKnowledgeBaseID != string(page.Items[0].ID) || profile.PersonalKnowledgeBaseID == templateBase.ID {
			t.Fatalf("personal assistant %s knowledge ownership = profile:%q bases:%+v", profileID, profile.PersonalKnowledgeBaseID, page.Items)
		}
		if profile.OrgIdentity.OrganizationRef.ID != agentcore.OrganizationID(tenantID) || profile.OrgIdentity.DepartmentRef.ID != "" || profile.OrgIdentity.PositionRef.ID != "" ||
			profile.OrgIdentity.EmploymentType != agentcore.EmploymentTypeAIAgent || profile.OrgIdentity.SupervisionPolicy != agentcore.SupervisionPolicySupervised ||
			!profile.OrgIdentity.ManagerAgentRef.IsZero() || profile.OrgIdentity.ReportingLineRef.ID != "" ||
			len(profile.OrgIdentity.RoleRefs) != 0 || profile.OrgIdentity.AuthorityScope != "" ||
			len(profile.CollectiveMemoryScopeRefs) != 0 || len(profile.MCPAuthorizations) != 0 ||
			profile.WorkspaceRoot != successService.personalAssistantWorkspaceRoot(profile.ID) {
			t.Fatalf("personal assistant %s inherited authority survived: %+v", profileID, profile)
		}
		availability, err := profiles.GetAvailability(ctx, profileID)
		if err != nil {
			t.Fatalf("get personal assistant %s availability: %v", profileID, err)
		}
		if availability.Status != agentcore.AvailabilityAvailable || availability.AgentInstanceID != agentcore.AgentInstanceID(profileID) || availability.Source != "personal-assistant-profile" {
			t.Fatalf("personal assistant %s availability = %+v", profileID, availability)
		}
	}
}

func filterPersonalAssistantTestLearningTool(refs []agentcore.ToolRef) []agentcore.ToolRef {
	out := make([]agentcore.ToolRef, 0, len(refs))
	for _, ref := range refs {
		if ref.ToolID != toolstd.RSILearningPlan {
			out = append(out, ref)
		}
	}
	return out
}

func TestPersonalAssistantAvailabilityPreservesExistingState(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	profile := agentcore.AgentProfile{ID: "A000018"}
	want := agentcore.AgentAvailability{
		AgentProfileID:  profile.ID,
		AgentInstanceID: "A000018:runtime",
		Status:          agentcore.AvailabilityOffline,
		Reason:          "operator disabled runtime",
		Source:          "operator",
	}
	if err := profiles.SaveAvailability(ctx, want); err != nil {
		t.Fatal(err)
	}
	service := &productionPersonalAssistantService{profiles: profiles}
	if err := service.ensureAvailability(ctx, profile); err != nil {
		t.Fatal(err)
	}
	got, err := profiles.GetAvailability(ctx, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("availability changed: got %+v want %+v", got, want)
	}
}

func TestRegisterExistingPersonalAssistantIdentitiesRestoresEmployeeNumber(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	profile := newPersonalAssistantProfile(
		agentcore.AgentProfileID(discussionsession.PersonalAssistantAgentID(
			"tenant-registered-personal-assistant",
			"owner-registered-personal-assistant",
		)),
		"tenant-registered-personal-assistant",
		"owner-registered-personal-assistant",
		agentcore.AgentProfile{
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "tenant-registered-personal-assistant"},
				DepartmentRef:   agentcore.DepartmentRef{ID: "operations"},
				PositionRef:     agentcore.PositionRef{ID: "worker"},
			},
			BaseAgent:   agentcore.DefaultBaseAgentSpec("Assistant", "Assistant"),
			SkillRefs:   []agentcore.SkillRef{},
			ToolRefs:    []agentcore.ToolRef{},
			RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi},
		},
	)
	profile.ID = "A000016"
	profile.PersonalMemoryProfileRef.MemoryProfileID = "memory-A000016"
	profile.PersonalMemoryProfileRef.OwnerAgentID = "A000016"
	if errs := agentcore.ValidateAgentProfile(profile); len(errs) > 0 {
		t.Fatalf("profile validation = %+v", errs)
	}
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if err := registerExistingPersonalAssistantIdentities(ctx, profiles); err != nil {
		t.Fatal(err)
	}
	if got := discussionsession.PersonalAssistantAgentID(profile.PersonalAssistantTenantID, profile.PersonalAssistantOwnerID); got != profile.ID.String() {
		t.Fatalf("registered personal assistant id = %q, want %q", got, profile.ID)
	}
	if !agentcore.IsPersonalAssistantProfileID(profile.ID.String()) {
		t.Fatal("registered canonical profile ID is not private")
	}
}

func TestPersonalAssistantReadDoesNotProvisionAgentProfile(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	template, err := readDefaultAgentTemplate("lead-agent.json")
	if err != nil {
		t.Fatalf("read lead template: %v", err)
	}
	if err := profiles.SaveProfile(ctx, defaultCoordinatorAgentProfile(defaultLeadAgentID, "lead", agentcore.RuntimeSpec{}, template)); err != nil {
		t.Fatalf("save lead profile: %v", err)
	}
	if _, err := deviceidentity.NewSQLStore(ctx, env.DB); err != nil {
		t.Fatalf("new device identity store: %v", err)
	}
	directoryStore, err := devicedirectory.NewSQLStore(ctx, env.DB)
	if err != nil {
		t.Fatalf("new directory store: %v", err)
	}
	directory, err := devicedirectory.NewService(devicedirectory.Options{Store: directoryStore, DefaultTenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("new directory service: %v", err)
	}
	service := &productionPersonalAssistantService{
		directory: directory, profiles: profiles, config: config.Config{}, defaultTenantID: "tenant-a",
	}
	scope := devicedirectory.OwnerScope{TenantID: "tenant-a", OwnerID: "owner-a"}
	profileID := agentcore.AgentProfileID(discussionsession.PersonalAssistantAgentID(scope.TenantID, scope.OwnerID))

	view, err := service.GetPersonalAssistant(ctx, scope)
	if err != nil {
		t.Fatalf("get personal assistant: %v", err)
	}
	if view.AgentProfileID != "" || view.DisplayName != defaultPersonalAssistantDisplayName {
		t.Fatalf("unprovisioned view = %+v", view)
	}
	if _, err := profiles.GetProfile(ctx, profileID); !errors.Is(err, agentcore.ErrNotFound) {
		t.Fatalf("read provisioned profile %s: %v", profileID, err)
	}
	storedTemplate, err := profiles.GetProfile(ctx, defaultLeadAgentID)
	if err != nil {
		t.Fatalf("get stored template: %v", err)
	}
	if validationErrors := agentcore.ValidateAgentProfile(newPersonalAssistantProfile(profileID, scope.TenantID, scope.OwnerID, storedTemplate)); len(validationErrors) != 0 {
		t.Fatalf("generated personal assistant profile validation = %+v", validationErrors)
	}

	view, err = service.UpdatePersonalAssistant(ctx, scope, devicedirectory.UpdatePersonalAssistantInput{
		AssistantEnabled: true, HostingMode: devicedirectory.HostingModeManaged, Revision: 0,
	})
	if err != nil {
		t.Fatalf("update personal assistant: %v", err)
	}
	if view.AgentProfileID != profileID.String() {
		t.Fatalf("updated view profile = %q, want %q", view.AgentProfileID, profileID)
	}
	if _, err := profiles.GetProfile(ctx, profileID); err != nil {
		t.Fatalf("explicit update did not provision profile: %v", err)
	}
}

func assertPersonalAssistantReadTool(t *testing.T, refs []agentcore.ToolRef, toolID agentcore.ToolID, permission string) {
	t.Helper()
	for _, ref := range refs {
		if ref.ToolID == toolID {
			if ref.PermissionScope != permission {
				t.Fatalf("tool %s permission = %q, want %q", toolID, ref.PermissionScope, permission)
			}
			return
		}
	}
	t.Fatalf("tool %s missing from %+v", toolID, refs)
}

type fixedPersonalAssistantKnowledgeLookup struct {
	bases map[knowledgecore.KnowledgeBaseID]knowledgecore.KnowledgeBase
}

func (f fixedPersonalAssistantKnowledgeLookup) GetKnowledgeBase(_ context.Context, id knowledgecore.KnowledgeBaseID) (knowledgecore.KnowledgeBase, error) {
	base, ok := f.bases[id]
	if !ok {
		return knowledgecore.KnowledgeBase{}, knowledgecore.ErrNotFound
	}
	return base, nil
}

type failingDefaultKnowledgeEnsurer struct {
	err error
}

func (f failingDefaultKnowledgeEnsurer) EnsureDefaultPersonalKnowledgeBase(context.Context, knowledgeadmin.ViewerRef, knowledgeadmin.EnsureDefaultCommand) (knowledgeadmin.KnowledgeBaseDTO, error) {
	return knowledgeadmin.KnowledgeBaseDTO{}, f.err
}
