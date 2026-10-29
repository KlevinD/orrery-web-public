package tool

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func TestResolverRejectsDisabledTool(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	recorder := &captureAuditRecorder{}
	definition := ToolDefinition{
		ID:        agentcore.ToolID("tool.disabled"),
		Kind:      KindAPITool,
		Name:      "Disabled API",
		Version:   "1.0.0",
		Enabled:   false,
		RiskLevel: RiskLow,
	}
	if err := registry.Register(ctx, definition); err != nil {
		t.Fatalf("register: %v", err)
	}
	resolver := NewResolver(registry, ResolverOptions{AuditRecorder: recorder})

	_, err := resolver.Resolve(ctx, ResolveRequest{
		Ref: agentcore.ToolRef{ToolID: definition.ID, VersionConstraint: ">=1.0.0"},
		Principal: Principal{
			AgentID:     agentcore.AgentID("agent-1"),
			Permissions: []string{"api.read"},
		},
	})

	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("Resolve disabled error = %v, want ErrDisabled", err)
	}
	if len(recorder.events) != 1 || recorder.events[0].Type != AuditToolBindingDenied {
		t.Fatalf("audit events = %+v", recorder.events)
	}
}

func TestResolverReturnsPermissionDeniedBeforeHidingKnownTool(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	definition := ToolDefinition{
		ID:      agentcore.ToolID("tool.repo.write"),
		Kind:    KindCLITool,
		Name:    "Repo writer",
		Version: "2.1.0",
		Permissions: []PermissionDeclaration{
			{Scope: "repo.write", Risk: RiskHigh},
			{Scope: "workspace.write", Risk: RiskMedium},
		},
		SecretRefs: []SecretRef{{Name: "github-token"}},
		Enabled:    true,
		RiskLevel:  RiskHigh,
		CLI:        &CLIToolDefinition{Command: "gh", Args: []string{"issue", "create"}},
	}
	if err := registry.Register(ctx, definition); err != nil {
		t.Fatalf("register: %v", err)
	}
	resolver := NewResolver(registry, ResolverOptions{})

	_, err := resolver.Resolve(ctx, ResolveRequest{
		Ref: agentcore.ToolRef{ToolID: definition.ID, VersionConstraint: "^2.0.0"},
		Principal: Principal{
			AgentID:     agentcore.AgentID("agent-1"),
			Permissions: []string{"repo.read"},
		},
	})

	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("Resolve error = %v, want ErrPermissionDenied", err)
	}
}

func TestResolverReturnsSecretBindingRefsWithoutValues(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	definition := ToolDefinition{
		ID:      agentcore.ToolID("tool.github.mcp"),
		Kind:    KindMCPServer,
		Name:    "GitHub MCP",
		Version: "1.4.0",
		Permissions: []PermissionDeclaration{
			{Scope: "github.read", Risk: RiskMedium},
		},
		SecretRefs: []SecretRef{{Name: "github-token", Env: "GITHUB_TOKEN"}},
		Enabled:    true,
		RiskLevel:  RiskMedium,
		MCP: &MCPServerDefinition{
			ServerName: "github",
			Transport:  TransportStdio,
			Command:    "github-mcp-server",
			EnvSecretRefs: []SecretRef{
				{Name: "github-token", Env: "GITHUB_TOKEN"},
			},
			ExposedTools: []string{"search_issues"},
			HealthCheck:  HealthCheckPolicy{Mode: HealthCheckStartup, Timeout: 2 * time.Second},
		},
	}
	if err := registry.Register(ctx, definition); err != nil {
		t.Fatalf("register: %v", err)
	}
	recorder := &captureAuditRecorder{}
	resolver := NewResolver(registry, ResolverOptions{AuditRecorder: recorder})

	resolved, err := resolver.Resolve(ctx, ResolveRequest{
		Ref: agentcore.ToolRef{ToolID: definition.ID, VersionConstraint: "1.4.x"},
		Principal: Principal{
			AgentID:     agentcore.AgentID("agent-1"),
			Permissions: []string{"github.read"},
		},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if len(resolved.SecretBindingRefs) != 1 {
		t.Fatalf("secret binding refs = %+v", resolved.SecretBindingRefs)
	}
	if resolved.SecretBindingRefs[0].SecretName != "github-token" || resolved.SecretBindingRefs[0].Env != "GITHUB_TOKEN" {
		t.Fatalf("unexpected secret binding ref: %+v", resolved.SecretBindingRefs[0])
	}
	if resolved.SecretBindingRefs[0].SecretValue != "" {
		t.Fatalf("resolver leaked secret value: %+v", resolved.SecretBindingRefs[0])
	}
	if len(recorder.events) != 1 || recorder.events[0].Type != AuditToolResolved {
		t.Fatalf("audit events = %+v", recorder.events)
	}
}

func TestResolverResolvesExternalMCPDownstreamToolRef(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	definition := ToolDefinition{
		ID:      agentcore.ToolID("mcp.yunji-zhaopin"),
		Kind:    KindMCPServer,
		Name:    "Yunji MCP",
		Version: "1.0.0",
		Enabled: true,
		MCP: &MCPServerDefinition{
			ServerName:   "yunji-zhaopin",
			ExposedTools: []string{"candidate:list"},
			ExposedToolDetails: []MCPExposedToolDefinition{{
				Name:        "candidate:advance",
				Description: "Advance candidate stage",
			}},
		},
	}
	if err := registry.Register(ctx, definition); err != nil {
		t.Fatalf("register: %v", err)
	}
	resolver := NewResolver(registry, ResolverOptions{})

	resolved, err := resolver.Resolve(ctx, ResolveRequest{
		Ref: agentcore.ToolRef{ToolID: "mcp.yunji-zhaopin.candidate:advance"},
		Principal: Principal{
			AgentID: agentcore.AgentID("agent-1"),
		},
	})
	if err != nil {
		t.Fatalf("Resolve downstream mcp tool: %v", err)
	}
	if resolved.Ref.ToolID != "mcp.yunji-zhaopin.candidate:advance" {
		t.Fatalf("resolved ref should keep downstream tool id: %+v", resolved.Ref)
	}
	if resolved.Definition.ID != "mcp.yunji-zhaopin" || resolved.Definition.Kind != KindMCPServer {
		t.Fatalf("resolved definition = %+v", resolved.Definition)
	}
}

func TestResolverRejectsUnknownExternalMCPDownstreamToolRef(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	definition := ToolDefinition{
		ID:      agentcore.ToolID("mcp.yunji-zhaopin"),
		Kind:    KindMCPServer,
		Name:    "Yunji MCP",
		Version: "1.0.0",
		Enabled: true,
		MCP: &MCPServerDefinition{
			ServerName:   "yunji-zhaopin",
			ExposedTools: []string{"candidate:list"},
		},
	}
	if err := registry.Register(ctx, definition); err != nil {
		t.Fatalf("register: %v", err)
	}
	resolver := NewResolver(registry, ResolverOptions{})

	if _, err := resolver.Resolve(ctx, ResolveRequest{Ref: agentcore.ToolRef{ToolID: "mcp.yunji-zhaopin.candidate:advance"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve unknown downstream mcp tool error = %v, want ErrNotFound", err)
	}
}

func TestResolverRejectsVersionConstraintMismatch(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	definition := ToolDefinition{
		ID:      agentcore.ToolID("tool.browser"),
		Kind:    KindBrowserTool,
		Name:    "Browser",
		Version: "1.9.0",
		Enabled: true,
	}
	if err := registry.Register(ctx, definition); err != nil {
		t.Fatalf("register: %v", err)
	}
	resolver := NewResolver(registry, ResolverOptions{})

	_, err := resolver.Resolve(ctx, ResolveRequest{
		Ref: agentcore.ToolRef{ToolID: definition.ID, VersionConstraint: ">=2.0.0"},
		Principal: Principal{
			AgentID: agentcore.AgentID("agent-1"),
		},
	})

	if !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("Resolve error = %v, want ErrVersionMismatch", err)
	}
}

type captureAuditRecorder struct {
	events []AuditEvent
}

func (r *captureAuditRecorder) RecordToolAudit(ctx context.Context, event AuditEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.events = append(r.events, event)
	return nil
}
