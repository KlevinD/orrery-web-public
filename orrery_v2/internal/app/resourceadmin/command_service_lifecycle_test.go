package resourceadmin

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/testkit"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

func TestCommandServiceUsesPlatformRegistriesForProductionPersistence(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	toolRegistry := toolres.NewPlatformRegistry(env.DB)
	skillRegistry := skillres.NewPlatformRegistry(env.DB)
	service := NewCommandService(toolRegistry, skillRegistry)
	service.SetSkillPackageStore(skillres.NewPackageStore(filepath.Join(t.TempDir(), "skills")))
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}

	registeredMCP, err := service.RegisterMCPServer(ctx, viewer, RegisterMCPServerRequest{
		ToolID:       "mcp.persisted",
		Name:         "Persisted MCP",
		Version:      "1.0.0",
		ServerName:   "persisted",
		Transport:    string(toolres.TransportStdio),
		Command:      "persisted-mcp",
		EnvSecrets:   []SecretRefDTO{{Name: "mcp.persisted.token", Env: "PERSISTED_TOKEN"}},
		ExposedTools: []string{"search"},
	})
	if err != nil {
		t.Fatalf("RegisterMCPServer: %v", err)
	}
	packageBytes := buildSkillPackage(t, "skill.persisted", "Persisted", "# Persisted\n")
	if _, err := service.ImportSkillPackage(ctx, viewer, bytes.NewReader(packageBytes), int64(len(packageBytes))); err != nil {
		t.Fatalf("ImportSkillPackage: %v", err)
	}

	query := NewQueryService(toolres.NewPlatformRegistry(env.DB), skillres.NewPlatformRegistry(env.DB))
	view, err := query.ListResources(ctx, ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceRead}})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(view.MCPServers) != 1 || view.MCPServers[0].ToolID != registeredMCP.ToolID {
		t.Fatalf("mcp server was not persisted through platform registry: %+v", view.MCPServers)
	}
	if len(view.Skills) != 1 || view.Skills[0].SkillID != "skill.persisted" {
		t.Fatalf("skill was not persisted through platform registry: %+v", view.Skills)
	}
}

func TestCommandServiceDisablesResourcesForFrontendManagement(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	skillRegistry := skillres.NewMemoryRegistry()
	if err := toolRegistry.Register(ctx, toolres.ToolDefinition{
		ID:      agentcore.ToolID("tool.to.disable"),
		Kind:    toolres.KindCLITool,
		Name:    "Disable me",
		Version: "1.0.0",
		Enabled: true,
		CLI:     &toolres.CLIToolDefinition{Command: "noop"},
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	if err := skillRegistry.Register(ctx, skillres.SkillDefinition{
		ID:      agentcore.SkillID("skill.to.disable"),
		Name:    "Disable skill",
		Version: "1.0.0",
		Enabled: true,
	}); err != nil {
		t.Fatalf("register skill: %v", err)
	}
	service := NewCommandService(toolRegistry, skillRegistry)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}

	if err := service.DisableTool(ctx, viewer, "tool.to.disable"); err != nil {
		t.Fatalf("DisableTool: %v", err)
	}
	if err := service.DisableSkill(ctx, viewer, "skill.to.disable"); err != nil {
		t.Fatalf("DisableSkill: %v", err)
	}
	toolDef, _ := toolRegistry.Get(ctx, "tool.to.disable")
	skillDef, _ := skillRegistry.Get(ctx, "skill.to.disable")
	if toolDef.Enabled || skillDef.Enabled {
		t.Fatalf("resources not disabled: tool=%+v skill=%+v", toolDef, skillDef)
	}
}

func TestCommandServiceMapsMissingRegistryResourcesToNotFound(t *testing.T) {
	ctx := context.Background()
	service := NewCommandService(toolres.NewMemoryRegistry(), skillres.NewMemoryRegistry())
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage, PermissionResourceRead}}

	cases := []struct {
		name string
		err  error
	}{
		{name: "disable tool", err: service.DisableTool(ctx, viewer, "tool.missing")},
		{name: "delete tool", err: service.DeleteTool(ctx, viewer, "tool.missing")},
		{name: "delete mcp", err: service.DeleteMCPServer(ctx, viewer, "mcp.missing")},
		{name: "sync mcp", err: func() error {
			_, err := service.SyncMCPServer(ctx, viewer, "mcp.missing")
			return err
		}()},
		{name: "mcp health", err: func() error {
			_, err := service.GetMCPServerHealth(ctx, viewer, "mcp.missing")
			return err
		}()},
		{name: "disable skill", err: service.DisableSkill(ctx, viewer, "skill.missing")},
		{name: "delete skill", err: service.DeleteSkill(ctx, viewer, "skill.missing")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.err, ErrNotFound) {
				t.Fatalf("error = %v, want ErrNotFound", tc.err)
			}
		})
	}
}

func TestCommandServiceDeletesToolBindingsAndBlocksBoundSkillDeletion(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	skillRegistry := skillres.NewMemoryRegistry()
	for _, definition := range []toolres.ToolDefinition{
		{
			ID:      agentcore.ToolID("tool.deploy"),
			Kind:    toolres.KindCLITool,
			Name:    "Deploy",
			Version: "1.0.0",
			Enabled: true,
			CLI:     &toolres.CLIToolDefinition{Command: "deploy"},
		},
		{
			ID:      agentcore.ToolID("mcp.github"),
			Kind:    toolres.KindMCPServer,
			Name:    "GitHub MCP",
			Version: "1.0.0",
			Enabled: true,
			MCP: &toolres.MCPServerDefinition{
				ServerName:   "github",
				Transport:    toolres.TransportStdio,
				Command:      "github-mcp-server",
				ExposedTools: []string{"search_issues"},
			},
		},
	} {
		if err := toolRegistry.Register(ctx, definition); err != nil {
			t.Fatalf("register tool %s: %v", definition.ID, err)
		}
	}
	if err := skillRegistry.Register(ctx, skillres.SkillDefinition{
		ID:      agentcore.SkillID("skill.review"),
		Name:    "Review",
		Version: "1.0.0",
		Enabled: true,
	}); err != nil {
		t.Fatalf("register skill: %v", err)
	}
	profileOps := agentcore.AgentProfile{
		ID:        agentcore.AgentProfileID("profile-ops"),
		RealName:  "Ops",
		AgentKind: agentcore.AgentKindAI,
		ToolRefs: []agentcore.ToolRef{
			{ToolID: agentcore.ToolID("tool.deploy")},
			{ToolID: agentcore.ToolID("mcp.github")},
			{ToolID: agentcore.ToolID("mcp.github.search_issues")},
		},
		SkillRefs: []agentcore.SkillRef{{SkillID: agentcore.SkillID("skill.review")}},
		Enabled:   true,
	}
	profileQa := agentcore.AgentProfile{
		ID:        agentcore.AgentProfileID("profile-qa"),
		RealName:  "QA",
		AgentKind: agentcore.AgentKindAI,
		ToolRefs:  []agentcore.ToolRef{{ToolID: agentcore.ToolID("tool.deploy")}},
		SkillRefs: []agentcore.SkillRef{{SkillID: agentcore.SkillID("skill.review")}},
		Enabled:   true,
	}
	profiles := &fakeAgentProfileBindingStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			profileOps.ID: profileOps,
			profileQa.ID:  profileQa,
		},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{
			{AgentProfileID: profileOps.ID, AgentInstanceID: agentcore.AgentInstanceID("agent-ops"), DisplayName: "Ops Agent"},
			{AgentProfileID: profileQa.ID, AgentInstanceID: agentcore.AgentInstanceID("agent-qa"), DisplayName: "QA Agent"},
		}},
	}
	audit := &fakeResourceAuditRecorder{}
	sourceDeletion := &resourceSourceDeletionLifecycleSpy{}
	service := NewCommandService(toolRegistry, skillRegistry)
	service.SetSkillBundleRegistry(skillres.NewMemoryBundleRegistry())
	service.SetAgentProfileBindingStore(profiles)
	service.SetAuditRecorder(audit)
	service.SetSourceDeletionLifecycle(sourceDeletion)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}

	if err := service.DeleteTool(ctx, viewer, "tool.deploy"); err != nil {
		t.Fatalf("DeleteTool: %v", err)
	}
	if _, err := toolRegistry.Get(ctx, "tool.deploy"); !errors.Is(err, toolres.ErrNotFound) {
		t.Fatalf("deleted tool get error = %v, want ErrNotFound", err)
	}
	if got := profiles.profiles[profileOps.ID].ToolRefs; len(got) != 2 || got[0].ToolID != "mcp.github" || got[1].ToolID != "mcp.github.search_issues" {
		t.Fatalf("ops tool refs after tool delete = %+v", got)
	}
	if got := profiles.profiles[profileQa.ID].ToolRefs; len(got) != 0 {
		t.Fatalf("qa tool refs after tool delete = %+v", got)
	}
	if len(sourceDeletion.calls) != 1 || sourceDeletion.calls[0].sourceType != "resource" || sourceDeletion.calls[0].sourceID != "tool.deploy" || sourceDeletion.rollbacks != 1 {
		t.Fatalf("tool source deletion lifecycle = %+v rollbacks=%d", sourceDeletion.calls, sourceDeletion.rollbacks)
	}

	if err := service.DeleteSkill(ctx, viewer, "skill.review"); !errors.Is(err, ErrConflict) {
		t.Fatalf("DeleteSkill bound error = %v, want ErrConflict", err)
	}
	if _, err := skillRegistry.Get(ctx, "skill.review"); err != nil {
		t.Fatalf("bound skill should remain registered: %v", err)
	}
	if got := profiles.profiles[profileOps.ID].SkillRefs; len(got) != 1 {
		t.Fatalf("blocked delete changed ops skill refs: %+v", got)
	}
	if got := profiles.profiles[profileQa.ID].SkillRefs; len(got) != 1 {
		t.Fatalf("blocked delete changed qa skill refs: %+v", got)
	}
	profile := profiles.profiles[profileOps.ID]
	profile.SkillRefs = nil
	profiles.profiles[profileOps.ID] = profile
	profile = profiles.profiles[profileQa.ID]
	profile.SkillRefs = nil
	profiles.profiles[profileQa.ID] = profile

	if err := service.DeleteSkill(ctx, viewer, "skill.review"); err != nil {
		t.Fatalf("DeleteSkill after unbinding: %v", err)
	}
	if _, err := skillRegistry.Get(ctx, "skill.review"); !errors.Is(err, skillres.ErrNotFound) {
		t.Fatalf("deleted skill get error = %v, want ErrNotFound", err)
	}
	if got := profiles.profiles[profileOps.ID].SkillRefs; len(got) != 0 {
		t.Fatalf("ops skill refs after skill delete = %+v", got)
	}
	if got := profiles.profiles[profileQa.ID].SkillRefs; len(got) != 0 {
		t.Fatalf("qa skill refs after skill delete = %+v", got)
	}

	if err := service.DeleteMCPServer(ctx, viewer, "mcp.github"); err != nil {
		t.Fatalf("DeleteMCPServer: %v", err)
	}
	if _, err := toolRegistry.Get(ctx, "mcp.github"); !errors.Is(err, toolres.ErrNotFound) {
		t.Fatalf("deleted mcp get error = %v, want ErrNotFound", err)
	}
	if got := profiles.profiles[profileOps.ID].ToolRefs; len(got) != 0 {
		t.Fatalf("ops tool refs after mcp delete = %+v", got)
	}
	if len(sourceDeletion.calls) != 2 || sourceDeletion.calls[1].sourceID != "mcp.github" || sourceDeletion.rollbacks != 2 {
		t.Fatalf("mcp source deletion lifecycle = %+v rollbacks=%d", sourceDeletion.calls, sourceDeletion.rollbacks)
	}
	if len(audit.events) != 3 {
		t.Fatalf("audit events = %+v", audit.events)
	}
	if audit.events[0].Action != "resource.tool.delete" || audit.events[1].Action != "resource.skill.delete" || audit.events[2].Action != "resource.mcp.delete" {
		t.Fatalf("audit actions = %+v", audit.events)
	}
}

type resourceSourceDeletionCall struct {
	sourceType         string
	sourceID           string
	canonicalTenantIDs []string
	changedBy          string
}

type resourceSourceDeletionLifecycleSpy struct {
	calls     []resourceSourceDeletionCall
	rollbacks int
}

func (s *resourceSourceDeletionLifecycleSpy) PrepareSourceDeletion(_ context.Context, sourceType, sourceID string, canonicalTenantIDs []string, changedBy string) (func(context.Context) error, error) {
	s.calls = append(s.calls, resourceSourceDeletionCall{
		sourceType: sourceType, sourceID: sourceID, canonicalTenantIDs: append([]string(nil), canonicalTenantIDs...), changedBy: changedBy,
	})
	return func(context.Context) error {
		s.rollbacks++
		return nil
	}, nil
}

func TestCommandServiceRollsBackSourceDeletionBindingWhenResourceDeleteFails(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	if err := toolRegistry.Register(ctx, toolres.ToolDefinition{
		ID: "tool.rollback", Kind: toolres.KindCLITool, Name: "Rollback", Version: "1.0.0", Enabled: true,
		CLI: &toolres.CLIToolDefinition{Command: "rollback"},
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	profileID := agentcore.AgentProfileID("profile-rollback")
	profiles := &fakeAgentProfileBindingStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			profileID: {ID: profileID, RealName: "Rollback", AgentKind: agentcore.AgentKindAI, ToolRefs: []agentcore.ToolRef{{ToolID: "tool.rollback"}}, Enabled: true},
		},
		page:    agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{AgentProfileID: profileID}}},
		saveErr: errors.New("profile save failed"),
	}
	lifecycle := &resourceSourceDeletionLifecycleSpy{}
	service := NewCommandService(toolRegistry, skillres.NewMemoryRegistry())
	service.SetAgentProfileBindingStore(profiles)
	service.SetSourceDeletionLifecycle(lifecycle)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}
	if err := service.DeleteTool(ctx, viewer, "tool.rollback"); err == nil || lifecycle.rollbacks != 1 {
		t.Fatalf("DeleteTool error = %v rollbacks=%d", err, lifecycle.rollbacks)
	}
	if _, err := toolRegistry.Get(ctx, "tool.rollback"); err != nil {
		t.Fatalf("tool removed after failed deletion: %v", err)
	}
}

func TestCommandServiceRecordsAuditForSuccessfulResourceCommands(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	audit := &fakeResourceAuditRecorder{}
	service := NewCommandService(toolRegistry, skillres.NewMemoryRegistry())
	service.SetAuditRecorder(audit)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}

	registeredTool, err := service.RegisterTool(ctx, viewer, ToolDefinitionRequest{
		ToolID:    "tool.deploy",
		Kind:      string(toolres.KindCLITool),
		Name:      "Deploy",
		Version:   "1.0.0",
		Command:   "deploy",
		RiskLevel: "high",
	})
	if err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}
	if len(audit.events) != 1 {
		t.Fatalf("audit events = %+v", audit.events)
	}
	event := audit.events[0]
	if event.ActorID != "admin" || event.ViewerID != "admin" || event.EntityType != "resource" || event.EntityID != registeredTool.ToolID {
		t.Fatalf("audit identity = %+v", event)
	}
	if event.Action != "resource.tool.register" || event.Result != "succeeded" || event.Severity != "info" {
		t.Fatalf("audit outcome = %+v", event)
	}
}
