package tool

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func TestRegistryRegistersMCPAndCLIToolDefinitions(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)

	mcp := ToolDefinition{
		ID:          agentcore.ToolID("tool.browser.mcp"),
		Kind:        KindMCPServer,
		Name:        "Browser MCP",
		Description: "Browser automation server",
		Version:     "1.2.0",
		RuntimeRequirements: RuntimeRequirements{
			Platforms: []string{"windows", "linux"},
		},
		Permissions: []PermissionDeclaration{{Scope: "browser.navigate", Risk: RiskMedium}},
		SecretRefs:  []SecretRef{{Name: "browser-token"}},
		Enabled:     true,
		RiskLevel:   RiskMedium,
		MCP: &MCPServerDefinition{
			ServerName: "browser",
			Transport:  TransportStdio,
			Command:    "npx",
			Args:       []string{"@modelcontextprotocol/server-browser"},
			EnvSecretRefs: []SecretRef{
				{Name: "browser-token", Env: "BROWSER_TOKEN"},
			},
			ExposedTools: []string{"navigate", "screenshot"},
			HealthCheck:  HealthCheckPolicy{Mode: HealthCheckStartup, Timeout: time.Second},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	cli := ToolDefinition{
		ID:          agentcore.ToolID("tool.codex.cli"),
		Kind:        KindCLITool,
		Name:        "Codex CLI",
		Version:     "0.4.0",
		Permissions: []PermissionDeclaration{{Scope: "workspace.write", Risk: RiskHigh}},
		Enabled:     true,
		RiskLevel:   RiskHigh,
		CLI: &CLIToolDefinition{
			Command:          "codex",
			Args:             []string{"--json"},
			WorkingDirPolicy: WorkingDirWorkspace,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := registry.Register(ctx, mcp); err != nil {
		t.Fatalf("register mcp: %v", err)
	}
	if err := registry.Register(ctx, cli); err != nil {
		t.Fatalf("register cli: %v", err)
	}

	got, err := registry.Get(ctx, mcp.ID)
	if err != nil {
		t.Fatalf("get mcp: %v", err)
	}
	if got.MCP == nil || got.MCP.ServerName != "browser" || got.MCP.Transport != TransportStdio {
		t.Fatalf("mcp definition not preserved: %+v", got.MCP)
	}
	if got.SecretRefs[0].Name != "browser-token" {
		t.Fatalf("secret ref not preserved: %+v", got.SecretRefs)
	}
	cliTools, err := registry.ListByKind(ctx, KindCLITool)
	if err != nil {
		t.Fatalf("list cli tools: %v", err)
	}
	if len(cliTools) != 1 || cliTools[0].ID != cli.ID {
		t.Fatalf("ListByKind(cli) = %+v", cliTools)
	}
}

func TestRegistrySupportsApprovalAndHumanConfirmationTools(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()

	approval := ToolDefinition{
		ID:        agentcore.ToolID("tool.approval.finance"),
		Kind:      KindApprovalTool,
		Name:      "Finance approval",
		Version:   "2026.05.31",
		Enabled:   true,
		RiskLevel: RiskHigh,
		Approval: &ApprovalToolDefinition{
			ApprovalType:        ApprovalExternalSystem,
			RequestSchemaRef:    "schema://approval/finance/v1",
			RequiredPermissions: []string{"approval.finance.request"},
		},
	}
	human := ToolDefinition{
		ID:        agentcore.ToolID("tool.human.confirmation"),
		Kind:      KindHumanConfirmationTool,
		Name:      "Human confirmation",
		Version:   "1.0.0",
		Enabled:   true,
		RiskLevel: RiskMedium,
		Approval: &ApprovalToolDefinition{
			ApprovalType:     ApprovalHumanConfirmation,
			RequestSchemaRef: "schema://approval/human/v1",
		},
	}

	if err := registry.Register(ctx, approval); err != nil {
		t.Fatalf("register approval: %v", err)
	}
	if err := registry.Register(ctx, human); err != nil {
		t.Fatalf("register human: %v", err)
	}

	tools, err := registry.ListByKind(ctx, KindApprovalTool)
	if err != nil {
		t.Fatalf("list approval: %v", err)
	}
	if len(tools) != 1 || tools[0].Approval.ApprovalType != ApprovalExternalSystem {
		t.Fatalf("approval tool not listed correctly: %+v", tools)
	}
}

func TestRegistryDeletesToolDefinitions(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	definition := ToolDefinition{
		ID:      agentcore.ToolID("tool.delete"),
		Kind:    KindCLITool,
		Name:    "Delete",
		Version: "1.0.0",
		Enabled: true,
		CLI:     &CLIToolDefinition{Command: "delete-tool"},
	}
	if err := registry.Register(ctx, definition); err != nil {
		t.Fatalf("register: %v", err)
	}

	if err := registry.Delete(ctx, definition.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := registry.Get(ctx, definition.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete error = %v, want ErrNotFound", err)
	}
	listed, err := registry.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("listed after delete = %+v", listed)
	}
	if err := registry.Delete(ctx, definition.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete error = %v, want ErrNotFound", err)
	}
}
