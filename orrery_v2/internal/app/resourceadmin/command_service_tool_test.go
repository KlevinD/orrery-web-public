package resourceadmin

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

func TestCommandServiceRegistersAndUpdatesTools(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	service := NewCommandService(toolRegistry, skillres.NewMemoryRegistry())
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}

	registered, err := service.RegisterTool(ctx, viewer, ToolDefinitionRequest{
		ToolID:      "tool.deploy",
		Kind:        string(toolres.KindCLITool),
		Name:        "Deploy",
		Description: "Deploy service",
		Version:     "1.0.0",
		RiskLevel:   string(toolres.RiskHigh),
		Tags:        []string{"operations"},
		Command:     "deploy",
		Permissions: []PermissionDTO{{Scope: "deploy.write", Risk: "high", Required: true}},
	})
	if err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}
	assertOpaqueResourceID(t, "tool id", "tool", registered.ToolID)
	if registered.Kind != string(toolres.KindCLITool) || registered.RiskLevel != "high" || registered.Permissions[0].Scope != "deploy.write" {
		t.Fatalf("registered = %+v", registered)
	}
	if registered.Command != "deploy" {
		t.Fatalf("registered command = %q", registered.Command)
	}

	updated, err := service.UpdateTool(ctx, viewer, ToolDefinitionRequest{
		ToolID:    registered.ToolID,
		Kind:      string(toolres.KindCLITool),
		Name:      "Deploy v2",
		Version:   "1.1.0",
		RiskLevel: string(toolres.RiskCritical),
		Command:   "deploy-v2",
		Enabled:   boolPtr(true),
	})
	if err != nil {
		t.Fatalf("UpdateTool: %v", err)
	}
	if updated.Name != "Deploy v2" || updated.Version != "1.1.0" || updated.RiskLevel != "critical" {
		t.Fatalf("updated = %+v", updated)
	}
	if updated.Command != "deploy-v2" {
		t.Fatalf("updated command = %q", updated.Command)
	}

	apiTool, err := service.RegisterTool(ctx, viewer, ToolDefinitionRequest{
		ToolID:  "tool.search",
		Kind:    string(toolres.KindAPITool),
		Name:    "Search API",
		Version: "1.0.0",
		URL:     "https://search.example.test/query",
	})
	if err != nil {
		t.Fatalf("RegisterTool api: %v", err)
	}
	if apiTool.URL != "https://search.example.test/query" {
		t.Fatalf("api tool url = %q", apiTool.URL)
	}
	assertOpaqueResourceID(t, "api tool id", "tool", apiTool.ToolID)

}

func TestCommandServicePartiallyUpdatesToolsWithoutClearingOmittedFields(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	service := NewCommandService(toolRegistry, skillres.NewMemoryRegistry())
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}

	registered, err := service.RegisterTool(ctx, viewer, ToolDefinitionRequest{
		ToolID:      "tool.deploy",
		Kind:        string(toolres.KindCLITool),
		Name:        "Deploy",
		Description: "Deploy service",
		Version:     "1.0.0",
		RiskLevel:   string(toolres.RiskHigh),
		Tags:        []string{"operations"},
		Command:     "deploy",
		Permissions: []PermissionDTO{{Scope: "deploy.write", Risk: "high", Required: true}},
	})
	if err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}
	definition, err := toolRegistry.Get(ctx, agentcore.ToolID(registered.ToolID))
	if err != nil {
		t.Fatalf("get registered tool: %v", err)
	}
	definition.InputSchema = map[string]any{"type": "object"}
	definition.OutputSchema = map[string]any{"type": "object"}
	definition.RuntimeRequirements.Platforms = []string{"linux"}
	definition.RuntimeRequirements.Packages = []string{"kubectl"}
	definition.RuntimeRequirements.Notes = "operator-only"
	if err := toolRegistry.UpdateDefinition(ctx, definition); err != nil {
		t.Fatalf("seed internal tool metadata: %v", err)
	}

	updated, err := service.UpdateTool(ctx, viewer, ToolDefinitionRequest{
		ToolID:     registered.ToolID,
		Name:       "Deploy v2",
		UpdateMask: []string{"name"},
	})
	if err != nil {
		t.Fatalf("UpdateTool partial: %v", err)
	}
	if updated.Name != "Deploy v2" || updated.Description != "Deploy service" || updated.Version != "1.0.0" || updated.Command != "deploy" || updated.RiskLevel != "high" {
		t.Fatalf("partial update should preserve omitted scalar fields: %+v", updated)
	}
	if len(updated.Tags) != 1 || updated.Tags[0] != "operations" || len(updated.Permissions) != 1 || updated.Permissions[0].Scope != "deploy.write" {
		t.Fatalf("partial update should preserve omitted repeated fields: %+v", updated)
	}
	definition, err = toolRegistry.Get(ctx, agentcore.ToolID(registered.ToolID))
	if err != nil {
		t.Fatalf("get updated tool: %v", err)
	}
	if definition.InputSchema["type"] != "object" || definition.OutputSchema["type"] != "object" ||
		len(definition.RuntimeRequirements.Platforms) != 1 || definition.RuntimeRequirements.Platforms[0] != "linux" ||
		len(definition.RuntimeRequirements.Packages) != 1 || definition.RuntimeRequirements.Packages[0] != "kubectl" ||
		definition.RuntimeRequirements.Notes != "operator-only" {
		t.Fatalf("partial update should preserve internal metadata: %+v", definition)
	}
}

func TestCommandServiceGeneratesOpaqueIDsForCreatedResources(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	service := NewCommandService(toolRegistry, skillres.NewMemoryRegistry())
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}

	tool, err := service.RegisterTool(ctx, viewer, ToolDefinitionRequest{
		ToolID:  "tool.frontend",
		Kind:    string(toolres.KindCLITool),
		Name:    "Deploy",
		Version: "1.0.0",
		Command: "deploy",
	})
	if err != nil {
		t.Fatalf("RegisterTool without frontend id: %v", err)
	}
	assertOpaqueResourceID(t, "tool id", "tool", tool.ToolID)

	mcp, err := service.RegisterMCPServer(ctx, viewer, RegisterMCPServerRequest{
		ToolID:     "mcp.frontend",
		Name:       "GitHub MCP",
		Version:    "1.0.0",
		ServerName: "github",
		Transport:  string(toolres.TransportStdio),
		Command:    "github-mcp-server",
	})
	if err != nil {
		t.Fatalf("RegisterMCPServer without frontend id: %v", err)
	}
	assertOpaqueResourceID(t, "mcp id", "mcp", mcp.ToolID)
}

func TestCommandServicePreservesExplicitDisabledToolState(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	service := NewCommandService(toolRegistry, skillres.NewMemoryRegistry())
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}
	disabled := false

	registered, err := service.RegisterTool(ctx, viewer, ToolDefinitionRequest{
		ToolID:  "tool.paused",
		Kind:    string(toolres.KindCLITool),
		Name:    "Paused",
		Version: "1.0.0",
		Command: "paused",
		Enabled: &disabled,
	})
	if err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}
	if registered.Enabled {
		t.Fatalf("registered tool should remain disabled: %+v", registered)
	}
	definition, err := toolRegistry.Get(ctx, agentcore.ToolID(registered.ToolID))
	if err != nil {
		t.Fatalf("get registered tool: %v", err)
	}
	if definition.Enabled {
		t.Fatalf("stored registered tool should remain disabled: %+v", definition)
	}

	updated, err := service.UpdateTool(ctx, viewer, ToolDefinitionRequest{
		ToolID:  registered.ToolID,
		Kind:    string(toolres.KindCLITool),
		Name:    "Paused v2",
		Version: "1.1.0",
		Command: "paused-v2",
		Enabled: &disabled,
	})
	if err != nil {
		t.Fatalf("UpdateTool: %v", err)
	}
	if updated.Enabled {
		t.Fatalf("updated tool should remain disabled: %+v", updated)
	}
}
