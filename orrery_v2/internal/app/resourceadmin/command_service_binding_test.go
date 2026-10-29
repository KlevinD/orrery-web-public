package resourceadmin

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/authorization"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

func TestCommandServiceBindsAndUnbindsAgentResources(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	skillRegistry := skillres.NewMemoryRegistry()
	if err := toolRegistry.Register(ctx, toolres.ToolDefinition{
		ID:      agentcore.ToolID("tool.deploy"),
		Kind:    toolres.KindCLITool,
		Name:    "Deploy",
		Version: "1.0.0",
		Enabled: true,
		CLI:     &toolres.CLIToolDefinition{Command: "deploy"},
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	if err := toolRegistry.Register(ctx, toolres.ToolDefinition{
		ID:          agentcore.ToolID("mcp.linear"),
		Kind:        toolres.KindMCPServer,
		Name:        "Linear MCP",
		Version:     "1.0.0",
		Enabled:     true,
		Description: "Linear MCP server",
		MCP: &toolres.MCPServerDefinition{
			ServerName: "linear",
			Transport:  toolres.TransportStdio,
			Command:    "linear-mcp",
		},
	}); err != nil {
		t.Fatalf("register mcp server: %v", err)
	}
	if err := skillRegistry.Register(ctx, skillres.SkillDefinition{
		ID:      agentcore.SkillID("skill.review"),
		Name:    "Review",
		Version: "1.0.0",
		Enabled: true,
	}); err != nil {
		t.Fatalf("register skill: %v", err)
	}
	profile := agentcore.AgentProfile{
		ID:        agentcore.AgentProfileID("profile-ops"),
		RealName:  "Ops",
		AgentKind: agentcore.AgentKindAI,
		ToolRefs:  []agentcore.ToolRef{},
		SkillRefs: []agentcore.SkillRef{},
		Enabled:   true,
	}
	profiles := &fakeAgentProfileBindingStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{profile.ID: profile},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID:  profile.ID,
			AgentInstanceID: agentcore.AgentInstanceID("agent-ops:runtime"),
			DisplayName:     "Ops Agent",
		}}},
	}
	audit := &fakeResourceAuditRecorder{}
	authzSink := &resourceAuthzSink{}
	service := NewCommandService(toolRegistry, skillRegistry)
	service.SetAgentProfileBindingStore(profiles)
	service.SetAuditRecorder(audit)
	service.SetAuthorizationRelations(authorization.NewRelationProjector(authzSink))
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}, RequestID: "corr-bind"}

	boundTool, err := service.BindResourceToAgent(ctx, viewer, BindResourceRequest{
		AgentID:           "profile-ops",
		ResourceType:      "tool",
		ResourceID:        "tool.deploy",
		Alias:             "deploy-cli",
		VersionConstraint: ">=1.0.0",
		PermissionScope:   "deploy.write",
		BindingPolicy:     "approval_required",
	})
	if err != nil {
		t.Fatalf("BindResourceToAgent tool: %v", err)
	}
	if boundTool.ResourceID != "tool.deploy" || boundTool.BindingType != "agent_tool_ref" || boundTool.BoundToID != "profile-ops" {
		t.Fatalf("bound tool = %+v", boundTool)
	}
	saved := profiles.profiles[profile.ID]
	if len(saved.ToolRefs) != 1 || saved.ToolRefs[0].ToolID != "tool.deploy" || saved.ToolRefs[0].PermissionScope != "deploy.write" {
		t.Fatalf("saved tool refs = %+v", saved.ToolRefs)
	}

	if _, err := service.BindResourceToAgent(ctx, viewer, BindResourceRequest{AgentID: "profile-ops", ResourceType: "skill", ResourceID: "skill.review", Alias: "reviewer"}); err != nil {
		t.Fatalf("BindResourceToAgent skill: %v", err)
	}
	saved = profiles.profiles[profile.ID]
	if len(saved.SkillRefs) != 1 || saved.SkillRefs[0].SkillID != "skill.review" {
		t.Fatalf("saved skill refs = %+v", saved.SkillRefs)
	}

	boundMCP, err := service.BindResourceToAgent(ctx, viewer, BindResourceRequest{AgentID: "profile-ops", ResourceType: "mcp_server", ResourceID: "mcp.linear", Alias: "linear"})
	if err != nil {
		t.Fatalf("BindResourceToAgent mcp_server: %v", err)
	}
	if boundMCP.ResourceType != "mcp_server" || boundMCP.BindingType != "agent_mcp_server_ref" {
		t.Fatalf("bound mcp = %+v", boundMCP)
	}
	saved = profiles.profiles[profile.ID]
	if len(saved.ToolRefs) != 2 || saved.ToolRefs[1].ToolID != "mcp.linear" {
		t.Fatalf("saved MCP refs should be stored as profile tool refs: %+v", saved.ToolRefs)
	}

	if err := service.UnbindResourceFromAgent(ctx, viewer, UnbindResourceRequest{AgentID: "profile-ops", ResourceType: "mcp_server", ResourceID: "mcp.linear"}); err != nil {
		t.Fatalf("UnbindResourceFromAgent mcp_server: %v", err)
	}
	saved = profiles.profiles[profile.ID]
	if len(saved.ToolRefs) != 1 || saved.ToolRefs[0].ToolID != "tool.deploy" {
		t.Fatalf("saved refs after mcp unbind = %+v", saved.ToolRefs)
	}

	if err := service.UnbindResourceFromAgent(ctx, viewer, UnbindResourceRequest{AgentID: "profile-ops", ResourceType: "tool", ResourceID: "tool.deploy"}); err != nil {
		t.Fatalf("UnbindResourceFromAgent: %v", err)
	}
	saved = profiles.profiles[profile.ID]
	if len(saved.ToolRefs) != 0 || len(saved.SkillRefs) != 1 {
		t.Fatalf("saved refs after unbind = tools:%+v skills:%+v", saved.ToolRefs, saved.SkillRefs)
	}
	if len(audit.events) != 5 || audit.events[0].CorrelationID != "corr-bind" || audit.events[4].Action != "resource.binding.unbind" {
		t.Fatalf("audit events = %+v", audit.events)
	}
	if audit.events[0].Metadata["agent_id"] != "profile-ops" || audit.events[0].Metadata["resource_id"] != "tool.deploy" || audit.events[0].Metadata["resource_type"] != "tool" || audit.events[0].Metadata["permission_scope"] != "deploy.write" {
		t.Fatalf("bind audit metadata = %+v", audit.events[0].Metadata)
	}
	if audit.events[2].Metadata["agent_id"] != "profile-ops" || audit.events[2].Metadata["resource_id"] != "mcp.linear" || audit.events[2].Metadata["resource_type"] != "mcp_server" {
		t.Fatalf("mcp bind audit metadata = %+v", audit.events[2].Metadata)
	}
	if audit.events[4].Metadata["agent_id"] != "profile-ops" || audit.events[4].Metadata["resource_id"] != "tool.deploy" || audit.events[4].Metadata["resource_type"] != "tool" {
		t.Fatalf("unbind audit metadata = %+v", audit.events[4].Metadata)
	}
	if len(authzSink.written) != 3 || len(authzSink.deleted) != 2 {
		t.Fatalf("authz writes=%+v deletes=%+v", authzSink.written, authzSink.deleted)
	}
	assertResourceAuthzTuple(t, authzSink.written[0], "profile-ops", "tool.deploy")
	assertResourceAuthzTuple(t, authzSink.written[1], "profile-ops", "skill.review")
	assertResourceAuthzTuple(t, authzSink.written[2], "profile-ops", "mcp.linear")
	assertResourceAuthzTuple(t, authzSink.deleted[0], "profile-ops", "mcp.linear")
	assertResourceAuthzTuple(t, authzSink.deleted[1], "profile-ops", "tool.deploy")
}

func TestCommandServiceProjectsHumanResourceBindingsAsUser(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	if err := toolRegistry.Register(ctx, toolres.ToolDefinition{
		ID:      agentcore.ToolID("tool.review"),
		Kind:    toolres.KindCLITool,
		Name:    "Review",
		Version: "1.0.0",
		Enabled: true,
		CLI:     &toolres.CLIToolDefinition{Command: "review"},
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	profile := agentcore.AgentProfile{
		ID:        agentcore.AgentProfileID("human-reviewer"),
		RealName:  "Reviewer",
		AgentKind: agentcore.AgentKindHuman,
		Enabled:   true,
	}
	profiles := &fakeAgentProfileBindingStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{profile.ID: profile},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID: profile.ID,
			AgentKind:      agentcore.AgentKindHuman,
			DisplayName:    "Reviewer",
		}}},
	}
	authzSink := &resourceAuthzSink{}
	service := NewCommandService(toolRegistry, skillres.NewMemoryRegistry())
	service.SetAgentProfileBindingStore(profiles)
	service.SetAuthorizationRelations(authorization.NewRelationProjector(authzSink))
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}

	if _, err := service.BindResourceToAgent(ctx, viewer, BindResourceRequest{
		AgentID:      profile.ID.String(),
		ResourceType: "tool",
		ResourceID:   "tool.review",
	}); err != nil {
		t.Fatalf("BindResourceToAgent: %v", err)
	}
	if err := service.UnbindResourceFromAgent(ctx, viewer, UnbindResourceRequest{
		AgentID:      profile.ID.String(),
		ResourceType: "tool",
		ResourceID:   "tool.review",
	}); err != nil {
		t.Fatalf("UnbindResourceFromAgent: %v", err)
	}
	if len(authzSink.written) != 1 || len(authzSink.deleted) != 1 {
		t.Fatalf("authorization relations: written=%d deleted=%d", len(authzSink.written), len(authzSink.deleted))
	}
	for _, tuple := range []authorization.RelationTuple{authzSink.written[0], authzSink.deleted[0]} {
		if tuple.Subject != (authorization.SubjectRef{Kind: "user", ID: profile.ID.String()}) {
			t.Fatalf("human resource relation subject = %+v", tuple.Subject)
		}
	}
}

func TestCommandServiceRequiresPerAgentMCPAKForAuthRequiredServer(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	if err := toolRegistry.Register(ctx, toolres.ToolDefinition{
		ID:        agentcore.ToolID("mcp.private"),
		Kind:      toolres.KindMCPServer,
		Name:      "Private MCP",
		Version:   "1.0.0",
		Enabled:   true,
		RiskLevel: toolres.RiskMedium,
		MCP: &toolres.MCPServerDefinition{
			ServerName: "private",
			Transport:  toolres.TransportStreamHTTP,
			URL:        "http://127.0.0.1:5000/api/mcp",
			Auth: toolres.MCPAuthPolicy{
				Required:   true,
				Mode:       toolres.MCPAuthModePerAgent,
				HeaderName: "Authorization",
			},
			Availability: toolres.MCPAvailability{Status: toolres.MCPAvailabilityRequiresAuth},
		},
	}); err != nil {
		t.Fatalf("register mcp server: %v", err)
	}
	profile := agentcore.AgentProfile{ID: "profile-ops", RealName: "Ops", AgentKind: agentcore.AgentKindAI, ToolRefs: []agentcore.ToolRef{}, SkillRefs: []agentcore.SkillRef{}, Enabled: true}
	profiles := &fakeAgentProfileBindingStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{profile.ID: profile},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID: profile.ID,
			DisplayName:    "Ops Agent",
		}}},
	}
	service := NewCommandService(toolRegistry, skillres.NewMemoryRegistry())
	service.SetAgentProfileBindingStore(profiles)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}

	_, err := service.BindResourceToAgent(ctx, viewer, BindResourceRequest{AgentID: "profile-ops", ResourceType: "mcp_server", ResourceID: "mcp.private"})
	if !errors.Is(err, ErrInvalidRequest) || !strings.Contains(err.Error(), "agent_ak") {
		t.Fatalf("BindResourceToAgent without agent ak error = %v, want agent_ak invalid request", err)
	}
	if got := profiles.profiles[profile.ID]; len(got.ToolRefs) != 0 || len(got.MCPAuthorizations) != 0 {
		t.Fatalf("failed binding should not persist tool refs or authorizations: %+v", got)
	}

	bound, err := service.BindResourceToAgent(ctx, viewer, BindResourceRequest{
		AgentID:           "profile-ops",
		ResourceType:      "mcp_server",
		ResourceID:        "mcp.private",
		AgentAK:           "Bearer agent-token",
		PermissionScope:   "private.use",
		AuthHeaderName:    "Authorization",
		BindingPolicy:     "approval_required",
		VersionConstraint: ">=1.0.0",
	})
	if err != nil {
		t.Fatalf("BindResourceToAgent with agent ak: %v", err)
	}
	if !bound.MCPAuthRequired || !bound.AgentAKConfigured || bound.AgentAKMasked != "Bear****oken" || bound.DefaultAKConfigured {
		t.Fatalf("binding auth metadata = %+v", bound)
	}
	saved := profiles.profiles[profile.ID]
	if len(saved.ToolRefs) != 1 || saved.ToolRefs[0].ToolID != "mcp.private" || saved.ToolRefs[0].PermissionScope != "private.use" {
		t.Fatalf("saved tool refs = %+v", saved.ToolRefs)
	}
	if len(saved.MCPAuthorizations) != 1 {
		t.Fatalf("saved authorizations = %+v", saved.MCPAuthorizations)
	}
	auth := saved.MCPAuthorizations[0]
	if auth.ServerToolID != "mcp.private" || auth.HeaderName != "Authorization" || auth.CredentialValue != "Bearer agent-token" {
		t.Fatalf("saved authorization = %+v", auth)
	}
}

type resourceAuthzSink struct {
	written []authorization.RelationTuple
	deleted []authorization.RelationTuple
}

func (s *resourceAuthzSink) WriteRelations(_ context.Context, tuples []authorization.RelationTuple) error {
	s.written = append(s.written, tuples...)
	return nil
}

func (s *resourceAuthzSink) DeleteRelations(_ context.Context, tuples []authorization.RelationTuple) error {
	s.deleted = append(s.deleted, tuples...)
	return nil
}

func assertResourceAuthzTuple(t *testing.T, tuple authorization.RelationTuple, agentID string, resourceID string) {
	t.Helper()
	if tuple.Subject.Kind != "agent" || tuple.Subject.ID != agentID || tuple.Relation != "operator" || tuple.Object.Type != "resource" || tuple.Object.ID != resourceID || tuple.TenantID != "global" {
		t.Fatalf("relation tuple = %+v", tuple)
	}
}

func TestCommandServiceBindsExternalMCPDownstreamToolAsToolRef(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	if err := toolRegistry.Register(ctx, toolres.ToolDefinition{
		ID:        agentcore.ToolID("mcp.yunji-zhaopin"),
		Kind:      toolres.KindMCPServer,
		Name:      "Yunji MCP",
		Version:   "1.0.0",
		Enabled:   true,
		RiskLevel: toolres.RiskMedium,
		MCP: &toolres.MCPServerDefinition{
			ServerName:   "yunji-zhaopin",
			Transport:    toolres.TransportStreamHTTP,
			URL:          "http://127.0.0.1:5000/api/mcp",
			ExposedTools: []string{"candidate:list"},
			ExposedToolDetails: []toolres.MCPExposedToolDefinition{{
				Name:        "candidate:advance",
				Description: "Advance candidate stage",
			}},
			Auth: toolres.MCPAuthPolicy{
				Required:                    true,
				Mode:                        toolres.MCPAuthModeGlobal,
				HeaderName:                  "Authorization",
				DefaultCredentialConfigured: true,
			},
		},
	}); err != nil {
		t.Fatalf("register mcp server: %v", err)
	}
	profile := agentcore.AgentProfile{ID: "profile-ops", RealName: "Ops", AgentKind: agentcore.AgentKindAI, ToolRefs: []agentcore.ToolRef{}, SkillRefs: []agentcore.SkillRef{}, Enabled: true}
	profiles := &fakeAgentProfileBindingStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{profile.ID: profile},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID: profile.ID,
			DisplayName:    "Ops Agent",
		}}},
	}
	service := NewCommandService(toolRegistry, skillres.NewMemoryRegistry())
	service.SetAgentProfileBindingStore(profiles)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}

	bound, err := service.BindResourceToAgent(ctx, viewer, BindResourceRequest{
		AgentID:           "profile-ops",
		ResourceType:      "tool",
		ResourceID:        "mcp.yunji-zhaopin.candidate:advance",
		PermissionScope:   "candidate.write",
		BindingPolicy:     "approval_required",
		VersionConstraint: ">=1.0.0",
	})
	if err != nil {
		t.Fatalf("BindResourceToAgent downstream mcp tool: %v", err)
	}
	if bound.ResourceType != "tool" || bound.ResourceID != "mcp.yunji-zhaopin.candidate:advance" || !bound.MCPAuthRequired {
		t.Fatalf("bound downstream mcp tool = %+v", bound)
	}
	saved := profiles.profiles[profile.ID]
	if len(saved.ToolRefs) != 1 || saved.ToolRefs[0].ToolID != "mcp.yunji-zhaopin.candidate:advance" || saved.ToolRefs[0].PermissionScope != "candidate.write" {
		t.Fatalf("saved downstream mcp tool refs = %+v", saved.ToolRefs)
	}
}

func TestCommandServiceBindsAuthRequiredMCPWithoutAgentAKWhenGlobalAKConfigured(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	if err := toolRegistry.Register(ctx, toolres.ToolDefinition{
		ID:        agentcore.ToolID("mcp.shared"),
		Kind:      toolres.KindMCPServer,
		Name:      "Shared MCP",
		Version:   "1.0.0",
		Enabled:   true,
		RiskLevel: toolres.RiskMedium,
		MCP: &toolres.MCPServerDefinition{
			ServerName: "shared",
			Transport:  toolres.TransportStreamHTTP,
			URL:        "http://127.0.0.1:5000/api/mcp",
			Headers:    map[string]string{"Authorization": "Bearer global-token"},
			Auth: toolres.MCPAuthPolicy{
				Required:                    true,
				Mode:                        toolres.MCPAuthModeGlobal,
				HeaderName:                  "Authorization",
				DefaultCredentialConfigured: true,
			},
			Availability: toolres.MCPAvailability{Status: toolres.MCPAvailabilityAvailable},
		},
	}); err != nil {
		t.Fatalf("register mcp server: %v", err)
	}
	profile := agentcore.AgentProfile{ID: "profile-ops", RealName: "Ops", AgentKind: agentcore.AgentKindAI, ToolRefs: []agentcore.ToolRef{}, SkillRefs: []agentcore.SkillRef{}, Enabled: true}
	profiles := &fakeAgentProfileBindingStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{profile.ID: profile},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID: profile.ID,
			DisplayName:    "Ops Agent",
		}}},
	}
	service := NewCommandService(toolRegistry, skillres.NewMemoryRegistry())
	service.SetAgentProfileBindingStore(profiles)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}

	bound, err := service.BindResourceToAgent(ctx, viewer, BindResourceRequest{AgentID: "profile-ops", ResourceType: "mcp_server", ResourceID: "mcp.shared"})
	if err != nil {
		t.Fatalf("BindResourceToAgent with global ak: %v", err)
	}
	if !bound.MCPAuthRequired || !bound.DefaultAKConfigured || bound.AgentAKConfigured {
		t.Fatalf("binding auth metadata = %+v", bound)
	}
	if got := profiles.profiles[profile.ID]; len(got.ToolRefs) != 1 || len(got.MCPAuthorizations) != 0 {
		t.Fatalf("global ak binding should not persist agent ak: %+v", got)
	}
}

func TestCommandServiceRejectsMismatchedToolAndMCPBindingTypes(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	if err := toolRegistry.Register(ctx, toolres.ToolDefinition{
		ID:      agentcore.ToolID("tool.deploy"),
		Kind:    toolres.KindCLITool,
		Name:    "Deploy",
		Version: "1.0.0",
		Enabled: true,
		CLI:     &toolres.CLIToolDefinition{Command: "deploy"},
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	if err := toolRegistry.Register(ctx, toolres.ToolDefinition{
		ID:      agentcore.ToolID("mcp.linear"),
		Kind:    toolres.KindMCPServer,
		Name:    "Linear MCP",
		Version: "1.0.0",
		Enabled: true,
		MCP:     &toolres.MCPServerDefinition{ServerName: "linear", Transport: toolres.TransportStdio, Command: "linear-mcp"},
	}); err != nil {
		t.Fatalf("register mcp server: %v", err)
	}
	profile := agentcore.AgentProfile{ID: "profile-ops", RealName: "Ops", AgentKind: agentcore.AgentKindAI, ToolRefs: []agentcore.ToolRef{}, SkillRefs: []agentcore.SkillRef{}, Enabled: true}
	profiles := &fakeAgentProfileBindingStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{profile.ID: profile},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID:  profile.ID,
			AgentInstanceID: "agent-ops:runtime",
		}}},
	}
	service := NewCommandService(toolRegistry, skillres.NewMemoryRegistry())
	service.SetAgentProfileBindingStore(profiles)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}

	if _, err := service.BindResourceToAgent(ctx, viewer, BindResourceRequest{AgentID: "profile-ops", ResourceType: "tool", ResourceID: "mcp.linear"}); err == nil {
		t.Fatalf("binding mcp server as tool should fail")
	}
	if _, err := service.BindResourceToAgent(ctx, viewer, BindResourceRequest{AgentID: "profile-ops", ResourceType: "mcp_server", ResourceID: "tool.deploy"}); err == nil {
		t.Fatalf("binding ordinary tool as mcp_server should fail")
	}
	if len(profiles.profiles[profile.ID].ToolRefs) != 0 {
		t.Fatalf("mismatched binding types should not persist refs: %+v", profiles.profiles[profile.ID].ToolRefs)
	}
}
