package resourceadmin

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
	memtestkit "orrery_v2/internal/memory/testkit"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

func TestQueryServiceListsManageableMCPAndSkills(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	skillRegistry := skillres.NewMemoryRegistry()
	if err := toolRegistry.Register(ctx, toolres.ToolDefinition{
		ID:        agentcore.ToolID("mcp.github"),
		Kind:      toolres.KindMCPServer,
		Name:      "GitHub MCP",
		Version:   "1.0.0",
		Enabled:   true,
		RiskLevel: toolres.RiskMedium,
		MCP: &toolres.MCPServerDefinition{
			ServerName:   "github",
			Transport:    toolres.TransportStdio,
			Command:      "github-mcp-server",
			ExposedTools: []string{"search_issues"},
			ExposedToolDetails: []toolres.MCPExposedToolDefinition{{
				Name:        "search_issues",
				Description: "Search issues in GitHub.",
				InputSchema: map[string]any{
					"type": "object",
				},
			}},
			EnvSecretRefs: []toolres.SecretRef{
				{Name: "mcp.github.github_token", Env: "GITHUB_TOKEN"},
			},
		},
		Tags: []string{"mcp", "registry"},
	}); err != nil {
		t.Fatalf("register mcp: %v", err)
	}
	if err := skillRegistry.Register(ctx, skillres.SkillDefinition{
		ID:           agentcore.SkillID("skill.code-review"),
		Name:         "code-review",
		Version:      "1.0.0",
		Description:  "Review code",
		DomainTags:   []string{"engineering"},
		TaskTypeTags: []string{"review"},
		Enabled:      true,
	}); err != nil {
		t.Fatalf("register skill: %v", err)
	}
	service := NewQueryServiceWithAgents(toolRegistry, skillRegistry, &fakeResourceAgentDirectory{})

	view, err := service.ListResources(ctx, ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceRead}})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(view.MCPServers) != 1 || view.MCPServers[0].ToolID != "mcp.github" {
		t.Fatalf("mcp servers = %+v", view.MCPServers)
	}
	if view.MCPServers[0].SecretRefs[0].Env != "GITHUB_TOKEN" || view.MCPServers[0].SecretRefs[0].SecretValue != "" {
		t.Fatalf("secret refs should expose refs only: %+v", view.MCPServers[0].SecretRefs)
	}
	if len(view.MCPServers[0].ExposedToolDetails) != 1 || view.MCPServers[0].ExposedToolDetails[0].Description != "Search issues in GitHub." {
		t.Fatalf("mcp exposed tool details = %+v", view.MCPServers[0].ExposedToolDetails)
	}
	if len(view.Skills) != 1 || view.Skills[0].SkillID != "skill.code-review" {
		t.Fatalf("skills = %+v", view.Skills)
	}
}

func TestQueryServiceGetsMCPServerByID(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	if err := toolRegistry.Register(ctx, toolres.ToolDefinition{
		ID:        agentcore.ToolID("mcp.github"),
		Kind:      toolres.KindMCPServer,
		Name:      "GitHub MCP",
		Version:   "1.0.0",
		Enabled:   true,
		RiskLevel: toolres.RiskMedium,
		MCP: &toolres.MCPServerDefinition{
			ServerName:   "github",
			Transport:    toolres.TransportStreamHTTP,
			URL:          "https://mcp.example.test/mcp",
			ExposedTools: []string{"search_issues"},
			ExposedToolDetails: []toolres.MCPExposedToolDefinition{{
				Name:        "search_issues",
				Description: "Search issues in GitHub.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string"},
					},
				},
			}},
		},
		Tags: []string{"mcp", "github"},
	}); err != nil {
		t.Fatalf("register mcp: %v", err)
	}
	if err := toolRegistry.Register(ctx, toolres.ToolDefinition{
		ID:      agentcore.ToolID("tool.deploy"),
		Kind:    toolres.KindInternalServiceTool,
		Name:    "deploy",
		Version: "1.0.0",
		Enabled: true,
	}); err != nil {
		t.Fatalf("register non-mcp tool: %v", err)
	}
	service := NewQueryService(toolRegistry, skillres.NewMemoryRegistry())

	got, err := service.GetMCPServer(ctx, ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceRead}}, "mcp.github")
	if err != nil {
		t.Fatalf("GetMCPServer: %v", err)
	}
	if got.ToolID != "mcp.github" || got.ServerName != "github" || got.URL != "https://mcp.example.test/mcp" {
		t.Fatalf("mcp server = %+v", got)
	}
	if len(got.ExposedToolDetails) != 1 || got.ExposedToolDetails[0].InputSchema["type"] != "object" {
		t.Fatalf("mcp tool details = %+v", got.ExposedToolDetails)
	}

	if _, err := service.GetMCPServer(ctx, ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceRead}}, "tool.deploy"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("GetMCPServer non-mcp error = %v, want ErrInvalidRequest", err)
	}
	if _, err := service.GetMCPServer(ctx, ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceRead}}, "mcp.missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetMCPServer missing error = %v, want ErrNotFound", err)
	}
}

func TestQueryServiceRequiresReadPermission(t *testing.T) {
	service := NewQueryService(toolres.NewMemoryRegistry(), skillres.NewMemoryRegistry())

	_, err := service.ListResources(context.Background(), ViewerRef{ViewerID: "viewer"})
	if err == nil {
		t.Fatalf("expected permission error")
	}
}

func TestQueryServiceListsResourcesWithSearchAndPagination(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	skillRegistry := skillres.NewMemoryRegistry()
	for _, id := range []string{"mcp.alpha", "mcp.beta"} {
		if err := toolRegistry.Register(ctx, toolres.ToolDefinition{
			ID:        agentcore.ToolID(id),
			Kind:      toolres.KindMCPServer,
			Name:      id,
			Version:   "1.0.0",
			Enabled:   true,
			RiskLevel: toolres.RiskMedium,
			MCP:       &toolres.MCPServerDefinition{ServerName: id, Transport: toolres.TransportStdio, Command: "server"},
		}); err != nil {
			t.Fatalf("register mcp: %v", err)
		}
	}
	if err := skillRegistry.Register(ctx, skillres.SkillDefinition{
		ID:      agentcore.SkillID("skill.alpha"),
		Name:    "alpha skill",
		Version: "1.0.0",
		Enabled: true,
	}); err != nil {
		t.Fatalf("register skill: %v", err)
	}
	service := NewQueryServiceWithAgents(toolRegistry, skillRegistry, &fakeResourceAgentDirectory{})

	view, err := service.ListResourcesFiltered(ctx, ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{PermissionResourceRead},
	}, ListResourcesRequest{Search: "alpha", Limit: 1})
	if err != nil {
		t.Fatalf("ListResourcesFiltered: %v", err)
	}
	if view.Page.Total != 2 || view.Page.Returned != 1 || !view.Page.HasMore {
		t.Fatalf("page = %+v view=%+v", view.Page, view)
	}
	if len(view.MCPServers)+len(view.Skills)+len(view.Tools) != 1 {
		t.Fatalf("expected one returned resource: %+v", view)
	}
}

func TestQueryServiceFiltersResourcesByCategoryRiskEnabledAndAgentBinding(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	skillRegistry := skillres.NewMemoryRegistry()
	if err := toolRegistry.Register(ctx, toolres.ToolDefinition{
		ID:        agentcore.ToolID("tool.deploy"),
		Kind:      toolres.KindInternalServiceTool,
		Name:      "deploy",
		Version:   "1.0.0",
		Enabled:   true,
		RiskLevel: toolres.RiskHigh,
		Tags:      []string{"operations", "deploy"},
	}); err != nil {
		t.Fatalf("register deploy tool: %v", err)
	}
	if err := toolRegistry.Register(ctx, toolres.ToolDefinition{
		ID:        agentcore.ToolID("tool.read"),
		Kind:      toolres.KindInternalServiceTool,
		Name:      "read",
		Version:   "1.0.0",
		Enabled:   false,
		RiskLevel: toolres.RiskLow,
		Tags:      []string{"engineering"},
	}); err != nil {
		t.Fatalf("register read tool: %v", err)
	}
	if err := skillRegistry.Register(ctx, skillres.SkillDefinition{
		ID:           agentcore.SkillID("skill.ops-review"),
		Name:         "ops review",
		Version:      "1.0.0",
		DomainTags:   []string{"operations"},
		TaskTypeTags: []string{"review"},
		RiskLevel:    skillres.RiskHigh,
		RequiredTools: []agentcore.ToolRef{
			{ToolID: agentcore.ToolID("tool.deploy")},
		},
		Enabled: true,
	}); err != nil {
		t.Fatalf("register ops skill: %v", err)
	}
	enabled := true
	service := NewQueryServiceWithAgents(toolRegistry, skillRegistry, &fakeResourceAgentDirectory{})

	view, err := service.ListResourcesFiltered(ctx, ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{PermissionResourceRead},
	}, ListResourcesRequest{Category: "operations", RiskLevel: "high", Enabled: &enabled, IncludeUsage: true, IncludeBindings: true})
	if err != nil {
		t.Fatalf("ListResourcesFiltered: %v", err)
	}
	if len(view.Tools) != 1 || view.Tools[0].ToolID != "tool.deploy" {
		t.Fatalf("tools = %+v", view.Tools)
	}
	if len(view.Skills) != 1 || view.Skills[0].SkillID != "skill.ops-review" {
		t.Fatalf("skills = %+v", view.Skills)
	}
	if len(view.MCPServers) != 0 || view.Page.Total != 2 {
		t.Fatalf("view = %+v", view)
	}
}

func TestQueryServiceFiltersResourcesByExplicitResourceRelationshipParameters(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	skillRegistry := skillres.NewMemoryRegistry()
	if err := toolRegistry.Register(ctx, toolres.ToolDefinition{
		ID:        agentcore.ToolID("tool.deploy"),
		Kind:      toolres.KindInternalServiceTool,
		Name:      "deploy",
		Version:   "1.0.0",
		Enabled:   true,
		RiskLevel: toolres.RiskHigh,
	}); err != nil {
		t.Fatalf("register deploy tool: %v", err)
	}
	if err := skillRegistry.Register(ctx, skillres.SkillDefinition{
		ID:      agentcore.SkillID("skill.ops-review"),
		Name:    "ops review",
		Version: "1.0.0",
		RequiredTools: []agentcore.ToolRef{
			{ToolID: agentcore.ToolID("tool.deploy")},
		},
		Enabled: true,
	}); err != nil {
		t.Fatalf("register ops skill: %v", err)
	}
	if err := skillRegistry.Register(ctx, skillres.SkillDefinition{
		ID:      agentcore.SkillID("skill.unbound"),
		Name:    "unbound",
		Version: "1.0.0",
		Enabled: true,
	}); err != nil {
		t.Fatalf("register unbound skill: %v", err)
	}
	agents := &fakeResourceAgentDirectory{page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
		AgentProfileID:  agentcore.AgentProfileID("profile-ops"),
		AgentInstanceID: agentcore.AgentInstanceID("agent-ops:runtime"),
		DisplayName:     "Ops Agent",
		ToolRefs:        []agentcore.ToolRef{{ToolID: agentcore.ToolID("tool.deploy")}},
		SkillRefs:       []agentcore.SkillRef{{SkillID: agentcore.SkillID("skill.ops-review")}},
		Enabled:         true,
	}}}}
	service := NewQueryServiceWithAgents(toolRegistry, skillRegistry, agents)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceRead}}

	byResource, err := service.ListResourcesFiltered(ctx, viewer, ListResourcesRequest{ResourceID: "tool.deploy"})
	if err != nil {
		t.Fatalf("ListResourcesFiltered resource_id: %v", err)
	}
	if len(byResource.Tools) != 1 || byResource.Tools[0].ToolID != "tool.deploy" || byResource.Page.Total != 1 {
		t.Fatalf("resource_id view = %+v", byResource)
	}

	byRequiredTool, err := service.ListResourcesFiltered(ctx, viewer, ListResourcesRequest{Kind: "skill", RequiredToolID: "tool.deploy"})
	if err != nil {
		t.Fatalf("ListResourcesFiltered required_tool_id: %v", err)
	}
	if len(byRequiredTool.Skills) != 1 || byRequiredTool.Skills[0].SkillID != "skill.ops-review" || byRequiredTool.Page.Total != 1 {
		t.Fatalf("required_tool_id view = %+v", byRequiredTool)
	}

	byBoundAgent, err := service.ListResourcesFiltered(ctx, viewer, ListResourcesRequest{BoundAgentID: "profile-ops"})
	if err != nil {
		t.Fatalf("ListResourcesFiltered bound_agent_id: %v", err)
	}
	if len(byBoundAgent.Tools) != 1 || len(byBoundAgent.Skills) != 1 || byBoundAgent.Page.Total != 2 {
		t.Fatalf("bound_agent_id view = %+v", byBoundAgent)
	}
}

func TestQueryServiceListsRealMemoryProfiles(t *testing.T) {
	ctx := context.Background()
	memoryStore := memtestkit.NewFakeStore()
	if err := memoryStore.SaveProfile(ctx, memcore.MemoryProfile{
		ID:         agentcore.MemoryProfileID("memory-pm-agent"),
		OwnerRef:   memcore.OwnerRef{AgentID: agentcore.AgentID("pm-agent"), OrganizationID: "org-agent-coordination"},
		MemoryKind: agentcore.MemoryKindPersonal,
		DefaultScope: memcore.MemoryScope{
			Kind: memcore.ScopeAgent,
			ID:   "pm-agent",
		},
		RetrievalPolicy:   memcore.RetrievalPolicy{MaxRecords: 12, Tags: []string{"pm"}},
		CompressionPolicy: memcore.CompressionPolicy{MaxSummaryBytes: 2048},
		VisibilityPolicy:  memcore.VisibilityPolicy{Mode: memcore.VisibilityOwnerOnly},
		Enabled:           true,
	}); err != nil {
		t.Fatalf("save memory profile: %v", err)
	}
	if err := memoryStore.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            "candidate-pm-review",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "pm-agent"},
		Status:        memcore.CandidateProposed,
	}); err != nil {
		t.Fatalf("save memory candidate: %v", err)
	}
	service := NewQueryServiceWithMemoryProfiles(toolres.NewMemoryRegistry(), skillres.NewMemoryRegistry(), memoryStore)

	view, err := service.ListResourcesFiltered(ctx, ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{PermissionResourceRead},
	}, ListResourcesRequest{Kind: "memory_profile", Search: "pm", IncludeUsage: true, IncludeBindings: true})
	if err != nil {
		t.Fatalf("ListResourcesFiltered: %v", err)
	}

	if view.Page.Total != 1 || view.Page.Returned != 1 || len(view.MemoryProfiles) != 1 {
		t.Fatalf("view = %+v", view)
	}
	profile := view.MemoryProfiles[0]
	if profile.MemoryProfileID != "memory-pm-agent" || profile.OwnerAgentID != "pm-agent" || profile.MemoryKind != "personal" || profile.DefaultScope.Kind != "agent" {
		t.Fatalf("profile = %+v", profile)
	}
	if profile.RetrievalPolicy.MaxRecords != 12 || profile.RecordCount != 0 || profile.CandidateCount != 1 {
		t.Fatalf("profile policy/counts = %+v", profile)
	}
}

func TestQueryServiceFiltersCollectiveMemoryProfilesByBoundMember(t *testing.T) {
	ctx := context.Background()
	memoryStore := memtestkit.NewFakeStore()
	collectiveProfileID := agentcore.MemoryProfileID("memory-department-engineering")
	if err := memoryStore.SaveProfile(ctx, memcore.MemoryProfile{
		ID:               collectiveProfileID,
		OwnerRef:         memcore.OwnerRef{OrganizationID: "org-1", DepartmentID: "engineering"},
		MemoryKind:       agentcore.MemoryKindCollective,
		DefaultScope:     memcore.MemoryScope{Kind: memcore.ScopeDepartment, ID: "engineering"},
		VisibilityPolicy: memcore.VisibilityPolicy{Mode: memcore.VisibilityScopedMembers, ScopeKind: memcore.ScopeDepartment, ScopeID: "engineering"},
		Enabled:          true,
	}); err != nil {
		t.Fatalf("save collective memory profile: %v", err)
	}
	agentID := agentcore.AgentProfileID("profile-engineer")
	agents := &fakeResourceAgentDirectory{
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{AgentProfileID: agentID, Enabled: true}}, Total: 1},
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			agentID: {
				ID:                        agentID,
				CollectiveMemoryScopeRefs: []agentcore.MemoryProfileRef{{MemoryProfileID: collectiveProfileID}},
			},
		},
	}
	service := NewQueryServiceWithAgents(toolres.NewMemoryRegistry(), skillres.NewMemoryRegistry(), agents)
	service.SetMemoryProfileReader(memoryStore)

	view, err := service.ListResourcesFiltered(ctx, ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{PermissionResourceRead},
	}, ListResourcesRequest{Kind: "memory_profile", BoundAgentID: agentID.String()})
	if err != nil {
		t.Fatalf("ListResourcesFiltered: %v", err)
	}
	if view.Page.Total != 1 || len(view.MemoryProfiles) != 1 || view.MemoryProfiles[0].MemoryProfileID != string(collectiveProfileID) {
		t.Fatalf("collective memory profiles = %+v", view)
	}
}

func TestQueryServiceCountsMemoryProfileUsageWithSingleRecordScan(t *testing.T) {
	ctx := context.Background()
	memoryStore := &countingMemoryProfileReader{FakeStore: memtestkit.NewFakeStore()}
	profiles := []agentcore.MemoryProfileID{"memory-a", "memory-b", "memory-c"}
	for _, id := range profiles {
		if err := memoryStore.SaveProfile(ctx, memcore.MemoryProfile{
			ID:               id,
			OwnerRef:         memcore.OwnerRef{AgentID: agentcore.AgentID("agent-" + string(id))},
			MemoryKind:       agentcore.MemoryKindPersonal,
			DefaultScope:     memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "agent-" + string(id)},
			VisibilityPolicy: memcore.VisibilityPolicy{Mode: memcore.VisibilityOwnerOnly},
			Enabled:          true,
		}); err != nil {
			t.Fatalf("save memory profile %s: %v", id, err)
		}
		if err := memoryStore.SaveCandidate(ctx, memcore.MemoryCandidate{ID: "candidate-" + string(id), Status: memcore.CandidateAccepted}); err != nil {
			t.Fatalf("save memory candidate %s: %v", id, err)
		}
		if err := memoryStore.SaveRecord(ctx, memcore.MemoryRecord{
			ID:          "record-" + string(id),
			ProfileID:   id,
			CandidateID: "candidate-" + string(id),
			Summary:     "remembered",
			Visibility:  memcore.Visibility{Mode: memcore.VisibilityOwnerOnly},
		}); err != nil {
			t.Fatalf("save memory record %s: %v", id, err)
		}
	}
	service := NewQueryServiceWithMemoryProfiles(toolres.NewMemoryRegistry(), skillres.NewMemoryRegistry(), memoryStore)

	view, err := service.ListResourcesFiltered(ctx, ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{PermissionResourceRead},
	}, ListResourcesRequest{Kind: "memory_profile", IncludeUsage: true, IncludeBindings: true, Limit: 2})
	if err != nil {
		t.Fatalf("ListResourcesFiltered: %v", err)
	}

	if memoryStore.findRecordsCalls != 1 {
		t.Fatalf("FindRecords calls = %d, want 1", memoryStore.findRecordsCalls)
	}
	if got := len(memoryStore.lastRecordQuery.ProfileIDs); got != 2 {
		t.Fatalf("count query profile ids = %d, want current page only", got)
	}
	if len(view.MemoryProfiles) != 2 || view.MemoryProfiles[0].RecordCount != 1 || view.MemoryProfiles[1].RecordCount != 1 {
		t.Fatalf("memory profiles counts = %+v", view.MemoryProfiles)
	}
}

func TestQueryServiceGetsMemoryProfileByID(t *testing.T) {
	ctx := context.Background()
	memoryStore := memtestkit.NewFakeStore()
	if err := memoryStore.SaveProfile(ctx, memcore.MemoryProfile{
		ID:               agentcore.MemoryProfileID("memory-pm-agent"),
		OwnerRef:         memcore.OwnerRef{AgentID: agentcore.AgentID("pm-agent")},
		MemoryKind:       agentcore.MemoryKindPersonal,
		DefaultScope:     memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "pm-agent"},
		VisibilityPolicy: memcore.VisibilityPolicy{Mode: memcore.VisibilityOwnerOnly},
		Enabled:          true,
	}); err != nil {
		t.Fatalf("save memory profile: %v", err)
	}
	service := NewQueryServiceWithMemoryProfiles(toolres.NewMemoryRegistry(), skillres.NewMemoryRegistry(), memoryStore)

	profile, err := service.GetMemoryProfile(ctx, ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{PermissionResourceRead},
	}, "memory-pm-agent")
	if err != nil {
		t.Fatalf("GetMemoryProfile: %v", err)
	}

	if profile.MemoryProfileID != "memory-pm-agent" || profile.OwnerAgentID != "pm-agent" || profile.DefaultScope.ID != "pm-agent" {
		t.Fatalf("profile = %+v", profile)
	}
}

func TestQueryServiceGetsPersonalMemoryProfileByAgentID(t *testing.T) {
	ctx := context.Background()
	memoryStore := memtestkit.NewFakeStore()
	if err := memoryStore.SaveProfile(ctx, memcore.MemoryProfile{
		ID:               agentcore.MemoryProfileID("memory-pm-agent"),
		OwnerRef:         memcore.OwnerRef{AgentID: agentcore.AgentID("pm-agent")},
		MemoryKind:       agentcore.MemoryKindPersonal,
		DefaultScope:     memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "pm-agent"},
		VisibilityPolicy: memcore.VisibilityPolicy{Mode: memcore.VisibilityOwnerOnly},
		Enabled:          true,
	}); err != nil {
		t.Fatalf("save pm memory profile: %v", err)
	}
	if err := memoryStore.SaveProfile(ctx, memcore.MemoryProfile{
		ID:               agentcore.MemoryProfileID("memory-review-agent"),
		OwnerRef:         memcore.OwnerRef{AgentID: agentcore.AgentID("review-agent")},
		MemoryKind:       agentcore.MemoryKindPersonal,
		DefaultScope:     memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "review-agent"},
		VisibilityPolicy: memcore.VisibilityPolicy{Mode: memcore.VisibilityOwnerOnly},
		Enabled:          true,
	}); err != nil {
		t.Fatalf("save review memory profile: %v", err)
	}
	service := NewQueryServiceWithMemoryProfiles(toolres.NewMemoryRegistry(), skillres.NewMemoryRegistry(), memoryStore)

	profile, err := service.GetPersonalMemoryProfileByAgent(ctx, ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{PermissionResourceRead},
	}, "pm-agent")
	if err != nil {
		t.Fatalf("GetPersonalMemoryProfileByAgent: %v", err)
	}

	if profile.MemoryProfileID != "memory-pm-agent" || profile.MemoryKind != "personal" {
		t.Fatalf("profile = %+v", profile)
	}
}

func TestQueryServiceBuildsMemoryDigestFromProfileRefs(t *testing.T) {
	ctx := context.Background()
	memoryStore := memtestkit.NewFakeStore()
	if err := memoryStore.SaveProfile(ctx, memcore.MemoryProfile{
		ID:               agentcore.MemoryProfileID("memory-pm-agent"),
		OwnerRef:         memcore.OwnerRef{AgentID: agentcore.AgentID("pm-agent")},
		MemoryKind:       agentcore.MemoryKindPersonal,
		DefaultScope:     memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "pm-agent"},
		VisibilityPolicy: memcore.VisibilityPolicy{Mode: memcore.VisibilityOwnerOnly},
		Enabled:          true,
	}); err != nil {
		t.Fatalf("save memory profile: %v", err)
	}
	if err := memoryStore.SaveCandidate(ctx, memcore.MemoryCandidate{ID: "candidate-pm-1", Status: memcore.CandidateAccepted}); err != nil {
		t.Fatalf("save memory candidate: %v", err)
	}
	if err := memoryStore.SaveRecord(ctx, memcore.MemoryRecord{
		ID:                 "memory-record-1",
		ProfileID:          agentcore.MemoryProfileID("memory-pm-agent"),
		CandidateID:        "candidate-pm-1",
		Summary:            "PM prefers concise planning with explicit blockers.",
		Facts:              []string{"PM owns cross-agent coordination."},
		Preferences:        []string{"Prefer streamed status updates."},
		SourceEvidenceRefs: []string{"evidence-1"},
		Confidence:         0.9,
		Visibility:         memcore.Visibility{Mode: memcore.VisibilityOwnerOnly},
	}); err != nil {
		t.Fatalf("save memory record: %v", err)
	}
	service := NewQueryServiceWithMemoryProfiles(toolres.NewMemoryRegistry(), skillres.NewMemoryRegistry(), memoryStore)

	digest, err := service.BuildMemoryDigest(ctx, ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{PermissionResourceRead},
	}, BuildMemoryDigestRequest{
		AgentID:   "pm-agent",
		MissionID: "mission-1",
		ProfileRefs: []MemoryProfileRefRequestDTO{{
			MemoryProfileID: "memory-pm-agent",
			MemoryKind:      "personal",
		}},
	})
	if err != nil {
		t.Fatalf("BuildMemoryDigest: %v", err)
	}

	if digest.AgentID != "pm-agent" || digest.MissionID != "mission-1" || digest.Scope.ID != "pm-agent" {
		t.Fatalf("digest identity/scope = %+v", digest)
	}
	if len(digest.SourceMemoryRefs) != 1 || digest.SourceMemoryRefs[0] != "memory-record-1" {
		t.Fatalf("digest source memory refs = %+v", digest.SourceMemoryRefs)
	}
	if !strings.Contains(digest.Summary, "Facts:") || !strings.Contains(digest.Summary, "PM prefers concise planning with explicit blockers.") ||
		len(digest.Facts) != 1 || digest.Facts[0] != "PM owns cross-agent coordination." {
		t.Fatalf("digest content = %+v", digest)
	}
}

func TestQueryServiceRequiresAgentDirectoryForBoundResourceFilters(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	skillRegistry := skillres.NewMemoryRegistry()
	if err := toolRegistry.Register(ctx, toolres.ToolDefinition{
		ID:      agentcore.ToolID("tool.deploy"),
		Kind:    toolres.KindInternalServiceTool,
		Name:    "deploy",
		Version: "1.0.0",
		Enabled: true,
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	service := NewQueryService(toolRegistry, skillRegistry)

	_, err := service.ListResourcesFiltered(ctx, ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{PermissionResourceRead},
	}, ListResourcesRequest{BoundAgentID: "profile-ops"})
	if err == nil {
		t.Fatalf("expected bound_agent_id query to require agent directory dependency")
	}
}

func TestQueryServiceRequiresAgentDirectoryForRelationshipDetails(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	skillRegistry := skillres.NewMemoryRegistry()
	if err := toolRegistry.Register(ctx, toolres.ToolDefinition{
		ID:      agentcore.ToolID("tool.deploy"),
		Kind:    toolres.KindInternalServiceTool,
		Name:    "deploy",
		Version: "1.0.0",
		Enabled: true,
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	service := NewQueryService(toolRegistry, skillRegistry)

	_, err := service.ListResourcesFiltered(ctx, ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{PermissionResourceRead},
	}, ListResourcesRequest{IncludeUsage: true})
	if err == nil {
		t.Fatalf("expected usage query to require agent directory dependency")
	}
}

func TestQueryServiceIncludesResourceUsageAndBindings(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	skillRegistry := skillres.NewMemoryRegistry()
	if err := toolRegistry.Register(ctx, toolres.ToolDefinition{
		ID:        agentcore.ToolID("tool.deploy"),
		Kind:      toolres.KindInternalServiceTool,
		Name:      "deploy",
		Version:   "1.0.0",
		Enabled:   true,
		RiskLevel: toolres.RiskHigh,
	}); err != nil {
		t.Fatalf("register deploy tool: %v", err)
	}
	if err := skillRegistry.Register(ctx, skillres.SkillDefinition{
		ID:      agentcore.SkillID("skill.ops-review"),
		Name:    "ops review",
		Version: "1.0.0",
		RequiredTools: []agentcore.ToolRef{
			{ToolID: agentcore.ToolID("tool.deploy"), Alias: "deployer", VersionConstraint: ">=1.0.0"},
		},
		Enabled: true,
	}); err != nil {
		t.Fatalf("register ops skill: %v", err)
	}
	agents := &fakeResourceAgentDirectory{page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
		AgentProfileID:  agentcore.AgentProfileID("profile-ops"),
		AgentInstanceID: agentcore.AgentInstanceID("agent-ops:runtime"),
		DisplayName:     "Ops Agent",
		AgentKind:       agentcore.AgentKindAI,
		ToolRefs: []agentcore.ToolRef{
			{ToolID: agentcore.ToolID("tool.deploy"), Alias: "deploy-cli"},
		},
		SkillRefs: []agentcore.SkillRef{
			{SkillID: agentcore.SkillID("skill.ops-review"), Alias: "ops-review"},
		},
		Enabled: true,
	}}}}
	service := NewQueryServiceWithAgents(toolRegistry, skillRegistry, agents)

	view, err := service.ListResourcesFiltered(ctx, ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{PermissionResourceRead},
	}, ListResourcesRequest{Search: "deploy", IncludeUsage: true, IncludeBindings: true})
	if err != nil {
		t.Fatalf("ListResourcesFiltered: %v", err)
	}
	if len(view.Usage) != 1 {
		t.Fatalf("usage = %+v", view.Usage)
	}
	usage := view.Usage[0]
	if usage.ResourceID != "tool.deploy" || usage.ResourceType != "tool" || usage.UsageCount != 2 {
		t.Fatalf("usage summary = %+v", usage)
	}
	if !containsString(usage.UsedByAgentIDs, "profile-ops") || !containsString(usage.RequiredBySkillIDs, "skill.ops-review") {
		t.Fatalf("usage refs = %+v", usage)
	}
	if len(view.Bindings) != 2 {
		t.Fatalf("bindings = %+v", view.Bindings)
	}
	if !hasResourceBinding(view.Bindings, "tool.deploy", "agent_tool_ref", "agent", "profile-ops", "deploy-cli") {
		t.Fatalf("missing agent tool binding: %+v", view.Bindings)
	}
	if !hasResourceBinding(view.Bindings, "tool.deploy", "skill_required_tool", "skill", "skill.ops-review", "deployer") {
		t.Fatalf("missing skill tool binding: %+v", view.Bindings)
	}
}

func TestQueryServiceTreatsToolKindAsNonMCPToolCollection(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	skillRegistry := skillres.NewMemoryRegistry()
	if err := toolRegistry.Register(ctx, toolres.ToolDefinition{
		ID:        agentcore.ToolID("tool.deploy"),
		Kind:      toolres.KindInternalServiceTool,
		Name:      "deploy",
		Version:   "1.0.0",
		Enabled:   true,
		RiskLevel: toolres.RiskHigh,
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	if err := toolRegistry.Register(ctx, toolres.ToolDefinition{
		ID:        agentcore.ToolID("mcp.github"),
		Kind:      toolres.KindMCPServer,
		Name:      "github",
		Version:   "1.0.0",
		Enabled:   true,
		RiskLevel: toolres.RiskMedium,
		MCP:       &toolres.MCPServerDefinition{ServerName: "github", Transport: toolres.TransportStdio, Command: "github-mcp-server"},
	}); err != nil {
		t.Fatalf("register mcp: %v", err)
	}
	if err := skillRegistry.Register(ctx, skillres.SkillDefinition{ID: agentcore.SkillID("skill.review"), Name: "review", Version: "1.0.0", Enabled: true}); err != nil {
		t.Fatalf("register skill: %v", err)
	}
	service := NewQueryService(toolRegistry, skillRegistry)

	view, err := service.ListResourcesFiltered(ctx, ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{PermissionResourceRead},
	}, ListResourcesRequest{Kind: "tool"})
	if err != nil {
		t.Fatalf("ListResourcesFiltered: %v", err)
	}
	if len(view.Tools) != 1 || view.Tools[0].ToolID != "tool.deploy" {
		t.Fatalf("tools = %+v", view.Tools)
	}
	if len(view.MCPServers) != 0 || len(view.Skills) != 0 || view.Page.Total != 1 {
		t.Fatalf("view = %+v", view)
	}
}

type fakeResourceAgentDirectory struct {
	page     agentcore.AgentDirectoryPage
	profiles map[agentcore.AgentProfileID]agentcore.AgentProfile
}

func (f *fakeResourceAgentDirectory) QueryDirectory(ctx context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	return f.page, nil
}

func (f *fakeResourceAgentDirectory) GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	if f.profiles == nil {
		return agentcore.AgentProfile{}, ErrNotFound
	}
	profile, ok := f.profiles[id]
	if !ok {
		return agentcore.AgentProfile{}, ErrNotFound
	}
	return profile, nil
}

type countingMemoryProfileReader struct {
	*memtestkit.FakeStore
	findRecordsCalls int
	lastRecordQuery  memstore.RecordQuery
}

func (s *countingMemoryProfileReader) FindRecords(ctx context.Context, query memstore.RecordQuery) ([]memcore.MemoryRecord, error) {
	s.findRecordsCalls++
	s.lastRecordQuery = query
	return s.FakeStore.FindRecords(ctx, query)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasResourceBinding(bindings []ResourceBindingDTO, resourceID, bindingType, boundToType, boundToID, alias string) bool {
	for _, binding := range bindings {
		if binding.ResourceID == resourceID &&
			binding.BindingType == bindingType &&
			binding.BoundToType == boundToType &&
			binding.BoundToID == boundToID &&
			binding.Alias == alias {
			return true
		}
	}
	return false
}
