package operations_test

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
	toolres "orrery_v2/internal/resource/tool"
)

func TestQueryServiceBuildsWorkforceOrgGraphAndPermissionMatrix(t *testing.T) {
	ctx := context.Background()
	agents := &fakeAgentDirectory{page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{
		{
			AgentProfileID:  "profile-ada",
			AgentInstanceID: "agent-ada",
			RealName:        "Ada Lovelace",
			DisplayName:     "Ada",
			RoleName:        "Engineer",
			AgentKind:       agentcore.AgentKindAI,
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef:  agentcore.OrganizationRef{ID: "orrery"},
				DepartmentRef:    agentcore.DepartmentRef{ID: "engineering"},
				PositionRef:      agentcore.PositionRef{ID: "backend"},
				RoleRefs:         []agentcore.RoleRef{{ID: "pm"}, {ID: "engineering-delivery"}, {ID: "primary_role"}},
				ManagerAgentRef:  agentcore.AgentProfileRef{ID: "agent-manager"},
				ReportingLineRef: agentcore.ReportingLineRef{ID: "engineering-line"},
				EmploymentType:   agentcore.EmploymentTypeAIAgent,
				AuthorityScope:   "repo:write",
			},
			ToolRefs: []agentcore.ToolRef{
				{ToolID: "tool.git"},
				knowledgeSearchReadToolRef(),
			},
			SkillRefs:    []agentcore.SkillRef{{SkillID: "go"}, {SkillID: "testing", Alias: "test automation"}},
			Availability: agentcore.AgentAvailability{Status: agentcore.AvailabilityBusy},
			Enabled:      true,
		},
		{
			AgentProfileID:  "profile-human",
			AgentInstanceID: "agent-human",
			RealName:        "Human Ops",
			DisplayName:     "Ops",
			RoleName:        "Approver",
			AgentKind:       agentcore.AgentKindHuman,
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "orrery"},
				DepartmentRef:   agentcore.DepartmentRef{ID: "operations"},
				PositionRef:     agentcore.PositionRef{ID: "approver"},
				EmploymentType:  agentcore.EmploymentTypeHuman,
			},
			Enabled: true,
		},
	}}}
	tools := &fakeToolRegistry{tools: []toolres.ToolDefinition{{
		ID:      "tool.git",
		Kind:    toolres.KindCLITool,
		Name:    "Git",
		Enabled: true,
		Permissions: []toolres.PermissionDeclaration{
			{Scope: "repo:read", Risk: toolres.RiskLow, Required: true},
			{Scope: "repo:write", Risk: toolres.RiskHigh, Required: true},
		},
	}}}
	service := operations.NewQueryService(agents, tools)

	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionOrgRead, operations.PermissionWorkforceRead, operations.PermissionPermissionsRead}}

	workforce, err := service.ListWorkforceMembers(ctx, viewer, operations.WorkforceQuery{EnabledOnly: true})
	if err != nil {
		t.Fatalf("ListWorkforceMembers returned error: %v", err)
	}
	if len(workforce.Members) != 2 || workforce.Members[0].AgentID != "profile-ada" || workforce.Members[0].AgentKind != "ai" {
		t.Fatalf("workforce = %+v", workforce)
	}
	if workforce.Members[0].AvailabilityStatus != "busy" {
		t.Fatalf("availability status = %q", workforce.Members[0].AvailabilityStatus)
	}
	if workforce.Members[0].DepartmentDisplayName != "engineering" || workforce.Members[0].PositionDisplayName != "Engineer" {
		t.Fatalf("workforce display fields = %+v", workforce.Members[0])
	}
	if workforce.Members[0].RoleName != "Engineer" {
		t.Fatalf("workforce role name = %q", workforce.Members[0].RoleName)
	}
	if got := workforce.Members[0].RoleIDs; len(got) != 1 || got[0] != "pm" {
		t.Fatalf("permission role ids = %+v", got)
	}
	if got := workforce.Members[0].FunctionalRoleIDs; len(got) != 2 || got[0] != "engineering-delivery" || got[1] != "primary_role" {
		t.Fatalf("functional role ids = %+v", got)
	}
	if got := workforce.Members[0].CapabilityTags; len(got) != 2 || got[0] != "go" || got[1] != "test automation" {
		t.Fatalf("capability tags = %+v", got)
	}

	graph, err := service.GetOrgGraph(ctx, viewer, operations.OrgGraphQuery{OrganizationID: "orrery", IncludeMembers: true})
	if err != nil {
		t.Fatalf("GetOrgGraph returned error: %v", err)
	}
	if len(graph.Departments) != 2 || len(graph.Positions) != 2 || len(graph.ReportingEdges) != 1 {
		t.Fatalf("org graph = %+v", graph)
	}

	matrix, err := service.GetPermissionMatrix(ctx, viewer, operations.PermissionMatrixQuery{})
	if err != nil {
		t.Fatalf("GetPermissionMatrix returned error: %v", err)
	}
	if len(matrix.Rows) != 2 {
		t.Fatalf("permission rows = %+v", matrix.Rows)
	}
	row := matrix.Rows[0]
	if row.AgentID != "profile-ada" || len(row.Resources) != 1 || row.Resources[0].ToolID != "tool.git" {
		t.Fatalf("permission row = %+v", row)
	}
	if row.Resources[0].Declarations[1].Effective != true || row.Resources[0].Declarations[1].Source != "agent_tool_ref+authority_scope" {
		t.Fatalf("effective permission should require tool ref and authority scope: %+v", row.Resources[0].Declarations)
	}
}

func TestQueryServiceOrgGraphSkipsWorkforceMembersByDefault(t *testing.T) {
	ctx := context.Background()
	agents := &fakeAgentDirectory{page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
		AgentProfileID:  "profile-ops",
		AgentInstanceID: "agent-ops",
		RealName:        "Ops Agent",
		AgentKind:       agentcore.AgentKindAI,
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef: agentcore.OrganizationRef{ID: "org-1"},
			DepartmentRef:   agentcore.DepartmentRef{ID: "dept-ops"},
			PositionRef:     agentcore.PositionRef{ID: "pos-ops"},
		},
		Enabled: true,
	}}}}
	orgs := newFakeOrganizationCatalogStore()
	if err := orgs.SaveOrganization(ctx, operations.OrganizationDTO{OrganizationID: "org-1", Name: "Orrery"}); err != nil {
		t.Fatalf("save organization: %v", err)
	}
	if err := orgs.SaveDepartment(ctx, operations.DepartmentDTO{DepartmentID: "dept-ops", OrganizationID: "org-1", Name: "Operations"}); err != nil {
		t.Fatalf("save department: %v", err)
	}
	if err := orgs.SavePosition(ctx, operations.PositionDTO{PositionID: "pos-ops", DepartmentID: "dept-ops", Name: "Operator"}); err != nil {
		t.Fatalf("save position: %v", err)
	}
	service := operations.NewQueryService(agents, &fakeToolRegistry{})
	service.SetOrganizationCatalogStore(orgs)

	graph, err := service.GetOrgGraph(ctx, operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionOrgRead}}, operations.OrgGraphQuery{OrganizationID: "org-1"})
	if err != nil {
		t.Fatalf("GetOrgGraph returned error: %v", err)
	}
	if agents.queryCalls != 0 || len(graph.Members) != 0 {
		t.Fatalf("default org graph should not query workforce members: calls=%d query=%+v members=%+v", agents.queryCalls, agents.lastQuery, graph.Members)
	}
	if len(graph.Organizations) != 1 || len(graph.Departments) != 1 || len(graph.Positions) != 1 {
		t.Fatalf("default org graph should still return catalog data: %+v", graph)
	}
}

func TestQueryServiceUsesOrganizationCatalogNamesForWorkforceMembers(t *testing.T) {
	ctx := context.Background()
	agents := &fakeAgentDirectory{page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
		AgentProfileID:  "profile-ops",
		AgentInstanceID: "agent-ops",
		RealName:        "Ops Agent",
		AgentKind:       agentcore.AgentKindAI,
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef: agentcore.OrganizationRef{ID: "org-1"},
			DepartmentRef:   agentcore.DepartmentRef{ID: "dept-ops"},
			PositionRef:     agentcore.PositionRef{ID: "pos-lead"},
			EmploymentType:  agentcore.EmploymentTypeAIAgent,
		},
		Enabled: true,
	}}}}
	orgs := newFakeOrganizationCatalogStore()
	if err := orgs.SaveOrganization(ctx, operations.OrganizationDTO{OrganizationID: "org-1", Name: "Orrery"}); err != nil {
		t.Fatalf("save organization: %v", err)
	}
	if err := orgs.SaveDepartment(ctx, operations.DepartmentDTO{DepartmentID: "dept-ops", OrganizationID: "org-1", Name: "Operations"}); err != nil {
		t.Fatalf("save department: %v", err)
	}
	if err := orgs.SavePosition(ctx, operations.PositionDTO{PositionID: "pos-lead", DepartmentID: "dept-ops", Name: "Lead Operator"}); err != nil {
		t.Fatalf("save position: %v", err)
	}
	service := operations.NewQueryService(agents, &fakeToolRegistry{})
	service.SetOrganizationCatalogStore(orgs)

	workforce, err := service.ListWorkforceMembers(ctx, operations.ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{operations.PermissionWorkforceRead},
	}, operations.WorkforceQuery{OrganizationID: "org-1"})
	if err != nil {
		t.Fatalf("ListWorkforceMembers returned error: %v", err)
	}

	if len(workforce.Members) != 1 {
		t.Fatalf("workforce = %+v", workforce)
	}
	member := workforce.Members[0]
	if member.DepartmentDisplayName != "Operations" || member.PositionDisplayName != "Lead Operator" {
		t.Fatalf("member display names = %+v", member)
	}
}

func TestQueryServiceSkipsWorkforceEntriesWithoutStableProfileIdentity(t *testing.T) {
	ctx := context.Background()
	agents := &fakeAgentDirectory{page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{
		{
			AgentInstanceID: "runtime-only-agent",
			RealName:        "Runtime Only",
			AgentKind:       agentcore.AgentKindAI,
			Enabled:         true,
		},
		{
			AgentProfileID:  "profile-stable",
			AgentInstanceID: "profile-stable",
			RealName:        "Stable Agent",
			AgentKind:       agentcore.AgentKindAI,
			Enabled:         true,
		},
	}}}
	service := operations.NewQueryService(agents, &fakeToolRegistry{})

	workforce, err := service.ListWorkforceMembers(ctx, operations.ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{operations.PermissionWorkforceRead},
	}, operations.WorkforceQuery{})
	if err != nil {
		t.Fatalf("ListWorkforceMembers returned error: %v", err)
	}
	if len(workforce.Members) != 1 || workforce.Members[0].AgentID != "profile-stable" {
		t.Fatalf("workforce should include only stable profile identities: %+v", workforce.Members)
	}
}

func TestQueryServiceSeparatesInternalWorkforceFromTalentMarket(t *testing.T) {
	ctx := context.Background()
	agents := &fakeAgentDirectory{page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
		AgentProfileID:  "profile-market",
		AgentInstanceID: "agent-market",
		RealName:        "Market Agent",
		AgentKind:       agentcore.AgentKindAI,
		Placement:       agentcore.TalentPlacement{Status: agentcore.TalentPlacementMarket},
		Enabled:         true,
	}}}}
	service := operations.NewQueryService(agents, &fakeToolRegistry{})
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceRead}}

	if _, err := service.ListWorkforceMembers(ctx, viewer, operations.WorkforceQuery{}); err != nil {
		t.Fatalf("ListWorkforceMembers returned error: %v", err)
	}
	if agents.lastQuery.Placement != agentcore.TalentPlacementFilterInternal {
		t.Fatalf("workforce query placement = %q, want internal", agents.lastQuery.Placement)
	}

	market, err := service.ListTalentMarketMembers(ctx, viewer, operations.WorkforceQuery{})
	if err != nil {
		t.Fatalf("ListTalentMarketMembers returned error: %v", err)
	}
	if agents.lastQuery.Placement != agentcore.TalentPlacementFilterMarket {
		t.Fatalf("market query placement = %q, want market", agents.lastQuery.Placement)
	}
	if len(market.Members) != 1 || market.Members[0].PlacementStatus != "market" {
		t.Fatalf("market members = %+v", market.Members)
	}
}

func TestQueryServiceProjectsWorkforceProfileDetailsForPeoplePage(t *testing.T) {
	ctx := context.Background()
	agents := &fakeAgentDirectory{
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID:  "profile-worker",
			AgentInstanceID: "agent-worker",
			RealName:        "Worker Agent",
			DisplayName:     "Worker",
			AgentKind:       agentcore.AgentKindAI,
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "orrery"},
				DepartmentRef:   agentcore.DepartmentRef{ID: "engineering"},
				PositionRef:     agentcore.PositionRef{ID: "backend"},
				EmploymentType:  agentcore.EmploymentTypeAIAgent,
			},
			Enabled: true,
		}}},
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"profile-worker": {
				ID:            "profile-worker",
				RealName:      "Worker Agent",
				DisplayName:   "Worker",
				WorkspaceRoot: "/srv/orrery/workspaces/profile-worker",
				RoleName:      "Backend Worker",
				AgentKind:     agentcore.AgentKindAI,
				OrgIdentity: agentcore.AgentOrgIdentity{
					OrganizationRef:   agentcore.OrganizationRef{ID: "orrery"},
					DepartmentRef:     agentcore.DepartmentRef{ID: "engineering"},
					PositionRef:       agentcore.PositionRef{ID: "backend"},
					RoleRefs:          []agentcore.RoleRef{{ID: "worker"}},
					AuthorityScope:    "repo:write",
					EmploymentType:    agentcore.EmploymentTypeAIAgent,
					SupervisionPolicy: agentcore.SupervisionPolicySupervised,
				},
				Persona: agentcore.AgentPersona{
					Backstory:              "You implement backend changes with tests.",
					ResponsibilityBoundary: "Own backend task delivery.",
					BehaviorBoundary:       "Do not modify unrelated files.",
				},
				BaseAgent: agentcore.BaseAgentSpec{
					InheritsBaseAgent: true,
					AuditRequired:     true,
					SystemPrompt:      "你是测试用托管 Agent。",
					LLMAPIKeyRef:      "secret://system/llm-api-key",
					SkillScope:        "profile-skills",
					ToolScope:         "profile-tools",
					MCPScope:          "profile-mcp",
				},
				PersonalMemoryProfileRef: agentcore.MemoryProfileRef{
					MemoryProfileID:   "memory-worker",
					OwnerAgentID:      "agent-worker",
					MemoryKind:        agentcore.MemoryKindPersonal,
					AccessScope:       "agent",
					RetrievalPolicy:   "recent",
					CompressionPolicy: "summary",
				},
				CollectiveMemoryScopeRefs: []agentcore.MemoryProfileRef{{
					MemoryProfileID: "memory-engineering",
					MemoryKind:      agentcore.MemoryKindOrganization,
					AccessScope:     "engineering",
				}},
				SkillRefs: []agentcore.SkillRef{{
					SkillID:           "skill.tdd",
					VersionConstraint: "^1.0.0",
					Alias:             "TDD",
					Level:             "senior",
					EvidenceRefs:      []agentcore.EvidenceRef{{ID: "evidence.test-log"}},
				}},
				ToolRefs: []agentcore.ToolRef{
					{
						ToolID:            "tool.git",
						VersionConstraint: ">=2",
						Alias:             "Git",
						PermissionScope:   "repo:write",
						BindingPolicy:     "approval_required",
					},
					knowledgeSearchReadToolRef(),
				},
				RuntimeSpec: agentcore.RuntimeSpec{
					Kind:      agentcore.RuntimeKindCodex,
					ConfigRef: "runtime-codex",
					Config:    []byte(`{"model":"codex-fixture","sandbox":"workspace-write"}`),
				},
				QAEnabled: true,
				Enabled:   true,
			},
		},
	}
	service := operations.NewQueryService(agents, &fakeToolRegistry{})

	workforce, err := service.ListWorkforceMembers(ctx, operations.ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{operations.PermissionWorkforceRead},
	}, operations.WorkforceQuery{IncludeDetails: true})
	if err != nil {
		t.Fatalf("ListWorkforceMembers returned error: %v", err)
	}
	if len(workforce.Members) != 1 {
		t.Fatalf("workforce = %+v", workforce)
	}
	member := workforce.Members[0]
	if member.Persona.Backstory != "You implement backend changes with tests." ||
		member.Persona.ResponsibilityBoundary != "Own backend task delivery." ||
		member.Persona.BehaviorBoundary != "Do not modify unrelated files." {
		t.Fatalf("persona was not projected from profile: %+v", member.Persona)
	}
	if member.RuntimeKind != "codex" || member.RuntimeConfigRef != "runtime-codex" || string(member.RuntimeConfig) != `{"model":"codex-fixture","sandbox":"workspace-write"}` {
		t.Fatalf("runtime details = kind:%q ref:%q config:%s", member.RuntimeKind, member.RuntimeConfigRef, member.RuntimeConfig)
	}
	if member.WorkspaceRoot != "/srv/orrery/workspaces/profile-worker" {
		t.Fatalf("workspace root = %q", member.WorkspaceRoot)
	}
	if !member.BaseAgent.InheritsBaseAgent || !member.BaseAgent.AuditRequired || !member.BaseAgent.HasSystemPrompt || !member.BaseAgent.HasLLMAPIKeyRef {
		t.Fatalf("base agent summary missing managed flags: %+v", member.BaseAgent)
	}
	if member.BaseAgent.SystemPrompt != "你是测试用托管 Agent。" {
		t.Fatalf("personnel detail should expose the editable system prompt, got %q", member.BaseAgent.SystemPrompt)
	}
	if member.BaseAgent.SkillScope != "profile-skills" || member.BaseAgent.ToolScope != "profile-tools" || member.BaseAgent.MCPScope != "profile-mcp" {
		t.Fatalf("base agent scopes = %+v", member.BaseAgent)
	}
	if len(member.SkillRefs) != 1 || member.SkillRefs[0].SkillID != "skill.tdd" || member.SkillRefs[0].EvidenceRefs[0] != "evidence.test-log" {
		t.Fatalf("skill refs = %+v", member.SkillRefs)
	}
	assertConfiguredWorkforceToolAndKnowledgeRead(t, member.ToolRefs, operations.WorkforceToolRefDTO{
		ToolID:            "tool.git",
		VersionConstraint: ">=2",
		Alias:             "Git",
		PermissionScope:   "repo:write",
		BindingPolicy:     "approval_required",
	})
	if member.PersonalMemoryRef.MemoryProfileID != "memory-worker" || len(member.CollectiveMemoryRefs) != 1 || member.CollectiveMemoryRefs[0].MemoryKind != "organization" {
		t.Fatalf("memory refs = personal:%+v collective:%+v", member.PersonalMemoryRef, member.CollectiveMemoryRefs)
	}
	if !member.QAEnabled {
		t.Fatalf("qa_enabled was not projected: %+v", member)
	}
}

func TestQueryServiceListsWorkforceWithoutProfileLookupsByDefault(t *testing.T) {
	ctx := context.Background()
	agents := &fakeAgentDirectory{
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID:  "profile-worker",
			AgentInstanceID: "agent-worker",
			RealName:        "Worker Agent",
			DisplayName:     "Worker",
			AgentKind:       agentcore.AgentKindAI,
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "orrery"},
				DepartmentRef:   agentcore.DepartmentRef{ID: "engineering"},
				PositionRef:     agentcore.PositionRef{ID: "backend"},
				EmploymentType:  agentcore.EmploymentTypeAIAgent,
			},
			Enabled: true,
		}}},
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"profile-worker": {
				ID:          "profile-worker",
				RealName:    "Worker Agent",
				DisplayName: "Worker",
				Persona: agentcore.AgentPersona{
					ResponsibilityBoundary: "full profile detail should not be loaded for list cards",
				},
				Enabled: true,
			},
		},
	}
	service := operations.NewQueryService(agents, &fakeToolRegistry{})

	workforce, err := service.ListWorkforceMembers(ctx, operations.ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{operations.PermissionWorkforceRead},
	}, operations.WorkforceQuery{})
	if err != nil {
		t.Fatalf("ListWorkforceMembers returned error: %v", err)
	}

	if agents.profileLookups != 0 {
		t.Fatalf("profile lookups = %d, want 0 for default list query", agents.profileLookups)
	}
	if len(workforce.Members) != 1 || workforce.Members[0].Persona.ResponsibilityBoundary != "" {
		t.Fatalf("default list should return lean member cards: %+v", workforce.Members)
	}
}

func TestQueryServiceBatchesDetailedWorkforceProfiles(t *testing.T) {
	base := &fakeAgentDirectory{
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{
			{AgentProfileID: "profile-a", DisplayName: "A", AgentKind: agentcore.AgentKindAI, Enabled: true},
			{AgentProfileID: "profile-b", DisplayName: "B", AgentKind: agentcore.AgentKindAI, Enabled: true},
		}},
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"profile-a": {ID: "profile-a", DisplayName: "A", AgentKind: agentcore.AgentKindAI, WorkspaceRoot: "/srv/a", Enabled: true},
			"profile-b": {ID: "profile-b", DisplayName: "B", AgentKind: agentcore.AgentKindAI, WorkspaceRoot: "/srv/b", Enabled: true},
		},
	}
	agents := &fakeBatchAgentDirectory{fakeAgentDirectory: base}
	service := operations.NewQueryService(agents, &fakeToolRegistry{})

	page, err := service.ListWorkforceMembers(context.Background(), operations.ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{operations.PermissionWorkforceRead},
	}, operations.WorkforceQuery{IncludeDetails: true})
	if err != nil {
		t.Fatalf("ListWorkforceMembers: %v", err)
	}
	if agents.batchProfileLookups != 1 || base.profileLookups != 0 {
		t.Fatalf("batch lookups=%d point lookups=%d", agents.batchProfileLookups, base.profileLookups)
	}
	if len(page.Members) != 2 || page.Members[0].WorkspaceRoot != "/srv/a" || page.Members[1].WorkspaceRoot != "/srv/b" {
		t.Fatalf("members = %+v", page.Members)
	}
}

func TestQueryServiceKeepsWorkerInternalQAProfilesEditableInWorkforceDirectory(t *testing.T) {
	ctx := context.Background()
	agents := &fakeAgentDirectory{
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID:  "qa-plan-agent",
			AgentInstanceID: "qa-plan-agent",
			RealName:        "验收计划",
			DisplayName:     "验收计划",
			RoleName:        "QAPlan",
			AgentKind:       agentcore.AgentKindAI,
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "orrery"},
				DepartmentRef:   agentcore.DepartmentRef{ID: "quality"},
				PositionRef:     agentcore.PositionRef{ID: "qa-plan"},
				RoleRefs:        []agentcore.RoleRef{{ID: "QAPlan"}},
				EmploymentType:  agentcore.EmploymentTypeSystem,
			},
			Enabled: true,
		}}},
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"qa-plan-agent": {
				ID:          "qa-plan-agent",
				RealName:    "验收计划",
				DisplayName: "验收计划",
				RoleName:    "QAPlan",
				AgentKind:   agentcore.AgentKindAI,
				OrgIdentity: agentcore.AgentOrgIdentity{
					OrganizationRef: agentcore.OrganizationRef{ID: "orrery"},
					DepartmentRef:   agentcore.DepartmentRef{ID: "quality"},
					PositionRef:     agentcore.PositionRef{ID: "qa-plan"},
					RoleRefs:        []agentcore.RoleRef{{ID: "QAPlan"}},
					EmploymentType:  agentcore.EmploymentTypeSystem,
				},
				Persona: agentcore.AgentPersona{
					Backstory:              "Generate acceptance plans before worker execution.",
					ResponsibilityBoundary: "Own worker QA planning.",
					BehaviorBoundary:       "Do not execute implementation work.",
				},
				BaseAgent: agentcore.BaseAgentSpec{
					InheritsBaseAgent: true,
					AuditRequired:     true,
					SystemPrompt:      "You are the worker QA planner.",
					LLMAPIKeyRef:      "secret://system/llm-api-key",
					SkillScope:        "profile-bound",
					ToolScope:         "profile-bound",
					MCPScope:          "profile-bound",
				},
				PersonalMemoryProfileRef: agentcore.MemoryProfileRef{MemoryProfileID: "memory-qa-plan", OwnerAgentID: "qa-plan-agent", MemoryKind: agentcore.MemoryKindPersonal},
				SkillRefs:                []agentcore.SkillRef{},
				ToolRefs:                 []agentcore.ToolRef{},
				Enabled:                  true,
			},
		},
	}
	service := operations.NewQueryService(agents, &fakeToolRegistry{})

	workforce, err := service.ListWorkforceMembers(ctx, operations.ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{operations.PermissionWorkforceRead},
	}, operations.WorkforceQuery{IncludeDetails: true})
	if err != nil {
		t.Fatalf("ListWorkforceMembers returned error: %v", err)
	}

	if len(workforce.Members) != 1 || workforce.Members[0].AgentID != "qa-plan-agent" {
		t.Fatalf("worker-internal QA profile should remain visible for editing: %+v", workforce.Members)
	}
	member := workforce.Members[0]
	if member.Persona.Backstory != "Generate acceptance plans before worker execution." ||
		!member.BaseAgent.HasSystemPrompt ||
		member.BaseAgent.SkillScope != "profile-bound" {
		t.Fatalf("editable QA profile details were not projected: %+v", member)
	}
}

func TestQueryServiceOperationsReadsRequireReadPermission(t *testing.T) {
	ctx := context.Background()
	service := operations.NewQueryService(&fakeAgentDirectory{}, &fakeToolRegistry{})
	viewer := operations.ViewerRef{ViewerID: "admin"}

	if _, err := service.ListWorkforceMembers(ctx, viewer, operations.WorkforceQuery{}); !errors.Is(err, operations.ErrPermissionDenied) {
		t.Fatalf("ListWorkforceMembers error = %v, want ErrPermissionDenied", err)
	}
	if _, err := service.GetOrgGraph(ctx, viewer, operations.OrgGraphQuery{}); !errors.Is(err, operations.ErrPermissionDenied) {
		t.Fatalf("GetOrgGraph error = %v, want ErrPermissionDenied", err)
	}
	if _, err := service.GetPermissionMatrix(ctx, viewer, operations.PermissionMatrixQuery{}); !errors.Is(err, operations.ErrPermissionDenied) {
		t.Fatalf("GetPermissionMatrix error = %v, want ErrPermissionDenied", err)
	}
}

func TestPermissionMatrixAppliesTemporaryGrantsAndApprovalDecisions(t *testing.T) {
	ctx := context.Background()
	agents := &fakeAgentDirectory{page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{
		{
			AgentProfileID:  "profile-ops",
			AgentInstanceID: "agent-ops",
			RealName:        "Ops Agent",
			DisplayName:     "Ops",
			AgentKind:       agentcore.AgentKindAI,
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "orrery"},
				DepartmentRef:   agentcore.DepartmentRef{ID: "operations"},
				PositionRef:     agentcore.PositionRef{ID: "operator"},
				AuthorityScope:  "deploy:read",
			},
			ToolRefs: []agentcore.ToolRef{{ToolID: "tool.deploy"}},
			Enabled:  true,
		},
	}}}
	tools := &fakeToolRegistry{tools: []toolres.ToolDefinition{
		{
			ID:      "tool.deploy",
			Kind:    toolres.KindInternalServiceTool,
			Name:    "Deploy",
			Enabled: true,
			Permissions: []toolres.PermissionDeclaration{
				{Scope: "deploy:read", Risk: toolres.RiskLow, Required: true},
				{Scope: "deploy:write", Risk: toolres.RiskHigh, Required: true},
				{Scope: "deploy:delete", Risk: toolres.RiskCritical, Required: true},
			},
		},
	}}
	service := operations.NewQueryService(agents, tools)
	service.SetPermissionGrants([]operations.TemporaryPermissionGrantDTO{{
		GrantID:   "grant-1",
		AgentID:   "profile-ops",
		ToolID:    "tool.deploy",
		Scope:     "deploy:write",
		Reason:    "incident window",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}})
	service.SetAuditStore(&fakeAuditStore{events: []operations.AuditEventDTO{{
		AuditID:    "approval-1",
		ActorID:    "ops-lead",
		MissionID:  "mission-1",
		EntityType: "approval",
		EntityID:   "delete-approval",
		Action:     "approval.tool_access.approved",
		Result:     "succeeded",
		Severity:   "info",
		Summary:    "approved delete permission",
		Metadata:   map[string]string{"agent_id": "profile-ops", "tool_id": "tool.deploy", "scope": "deploy:delete"},
	}}})

	matrix, err := service.GetPermissionMatrix(ctx, operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionPermissionsRead}}, operations.PermissionMatrixQuery{AgentID: "profile-ops", ToolID: "tool.deploy"})
	if err != nil {
		t.Fatalf("GetPermissionMatrix returned error: %v", err)
	}
	if len(matrix.Rows) != 1 || len(matrix.Rows[0].Resources) != 1 {
		t.Fatalf("matrix = %+v", matrix)
	}
	declarations := declarationsByScope(matrix.Rows[0].Resources[0].Declarations)
	if !declarations["deploy:write"].Effective || declarations["deploy:write"].Source != "agent_tool_ref+temporary_grant" {
		t.Fatalf("temporary grant declaration = %+v", declarations["deploy:write"])
	}
	if !declarations["deploy:delete"].Effective || declarations["deploy:delete"].Source != "agent_tool_ref+approval_decision" {
		t.Fatalf("approval decision declaration = %+v", declarations["deploy:delete"])
	}
}

func TestPermissionMatrixIgnoresApprovalSummaryTokensWithoutMetadata(t *testing.T) {
	ctx := context.Background()
	agents := &fakeAgentDirectory{page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
		AgentProfileID:  "profile-ops",
		AgentInstanceID: "agent-ops",
		AgentKind:       agentcore.AgentKindAI,
		ToolRefs:        []agentcore.ToolRef{{ToolID: "tool.deploy"}},
		Enabled:         true,
	}}}}
	tools := &fakeToolRegistry{tools: []toolres.ToolDefinition{{
		ID:          "tool.deploy",
		Name:        "Deploy",
		Permissions: []toolres.PermissionDeclaration{{Scope: "deploy:delete", Risk: toolres.RiskCritical, Required: true}},
	}}}
	service := operations.NewQueryService(agents, tools)
	service.SetAuditStore(&fakeAuditStore{events: []operations.AuditEventDTO{{
		EntityType: "approval",
		Action:     "approval.tool_access.approved",
		Result:     "succeeded",
		Summary:    "agent_id=agent-ops tool_id=tool.deploy scope=deploy:delete",
	}}})

	matrix, err := service.GetPermissionMatrix(ctx, operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionPermissionsRead}}, operations.PermissionMatrixQuery{})
	if err != nil {
		t.Fatalf("GetPermissionMatrix returned error: %v", err)
	}
	declarations := matrix.Rows[0].Resources[0].Declarations
	if declarations[0].Effective || declarations[0].Source == "agent_tool_ref+approval_decision" {
		t.Fatalf("summary tokens should not grant permission without metadata: %+v", declarations[0])
	}
}

type fakeAgentDirectory struct {
	page           agentcore.AgentDirectoryPage
	profiles       map[agentcore.AgentProfileID]agentcore.AgentProfile
	lastQuery      agentcore.AgentDirectoryQuery
	queryCalls     int
	profileLookups int
}

type fakeBatchAgentDirectory struct {
	*fakeAgentDirectory
	batchProfileLookups int
}

func (f *fakeBatchAgentDirectory) GetProfiles(_ context.Context, ids []agentcore.AgentProfileID) (map[agentcore.AgentProfileID]agentcore.AgentProfile, error) {
	f.batchProfileLookups++
	out := make(map[agentcore.AgentProfileID]agentcore.AgentProfile, len(ids))
	for _, id := range ids {
		if profile, ok := f.profiles[id]; ok {
			out[id] = profile
		}
	}
	return out, nil
}

func (f *fakeAgentDirectory) QueryDirectory(ctx context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	f.queryCalls++
	f.lastQuery = q
	return f.page, nil
}

func (f *fakeAgentDirectory) GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	f.profileLookups++
	profile, ok := f.profiles[id]
	if !ok {
		return agentcore.AgentProfile{}, operations.ErrNotFound
	}
	return profile, nil
}

type fakeToolRegistry struct {
	tools []toolres.ToolDefinition
}

func (f *fakeToolRegistry) List(ctx context.Context) ([]toolres.ToolDefinition, error) {
	return f.tools, nil
}

func declarationsByScope(items []operations.EffectivePermissionDeclarationDTO) map[string]operations.EffectivePermissionDeclarationDTO {
	byScope := make(map[string]operations.EffectivePermissionDeclarationDTO, len(items))
	for _, item := range items {
		byScope[item.Scope] = item
	}
	return byScope
}
