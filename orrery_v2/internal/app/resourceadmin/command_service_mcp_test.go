package resourceadmin

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestCommandServiceRegistersMCPFromAdminRequest(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	service := NewCommandService(toolRegistry, skillres.NewMemoryRegistry())

	result, err := service.RegisterMCPServer(ctx, ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{PermissionResourceManage},
	}, RegisterMCPServerRequest{
		ToolID:             "mcp.linear",
		Name:               "Linear MCP",
		Version:            "1.0.0",
		ServerName:         "linear",
		Transport:          string(toolres.TransportStdio),
		Command:            "linear-mcp",
		Args:               []string{"stdio"},
		EnvSecrets:         []SecretRefDTO{{Name: "mcp.linear.api_key", Env: "LINEAR_API_KEY"}},
		ExposedTools:       []string{"list_issues"},
		CallTimeoutSeconds: 60,
		ExposedToolDetails: []MCPExposedToolDTO{{
			Name:        "list_issues",
			Description: "List Linear issues.",
			InputSchema: map[string]any{
				"type": "object",
			},
			OutputSchema: map[string]any{
				"type": "object",
				"required": []any{
					"issues",
				},
			},
		}},
		Tags: []string{"mcp", "linear"},
	})
	if err != nil {
		t.Fatalf("RegisterMCPServer: %v", err)
	}
	assertOpaqueResourceID(t, "mcp id", "mcp", result.ToolID)
	definition, err := toolRegistry.Get(ctx, agentcore.ToolID(result.ToolID))
	if err != nil {
		t.Fatalf("get mcp: %v", err)
	}
	if definition.MCP == nil || definition.MCP.EnvSecretRefs[0].Env != "LINEAR_API_KEY" {
		t.Fatalf("definition = %+v", definition)
	}
	if definition.MCP.CallTimeoutSeconds != 60 || result.CallTimeoutSeconds != 60 {
		t.Fatalf("call timeout was not preserved: definition=%+v dto=%+v", definition.MCP, result)
	}
	if len(definition.MCP.ExposedToolDetails) != 1 ||
		definition.MCP.ExposedToolDetails[0].OutputSchema["type"] != "object" {
		t.Fatalf("exposed tool details = %+v", definition.MCP.ExposedToolDetails)
	}
}

func TestCommandServiceRejectsNegativeMCPCallTimeout(t *testing.T) {
	service := NewCommandService(toolres.NewMemoryRegistry(), skillres.NewMemoryRegistry())

	_, err := service.RegisterMCPServer(context.Background(), ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{PermissionResourceManage},
	}, RegisterMCPServerRequest{
		Name:               "Invalid MCP",
		ServerName:         "invalid",
		Transport:          string(toolres.TransportStdio),
		Command:            "invalid-mcp",
		CallTimeoutSeconds: -1,
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("RegisterMCPServer error = %v, want ErrInvalidRequest", err)
	}
}

func TestCommandServicePartiallyUpdatesMCPWithoutClearingOmittedFields(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	service := NewCommandService(toolRegistry, skillres.NewMemoryRegistry())
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}

	registered, err := service.RegisterMCPServer(ctx, viewer, RegisterMCPServerRequest{
		ToolID:             "mcp.linear",
		Name:               "Linear MCP",
		Description:        "Issue tracker MCP",
		Version:            "1.0.0",
		ServerName:         "linear",
		Transport:          string(toolres.TransportStdio),
		Command:            "linear-mcp",
		Args:               []string{"stdio"},
		EnvSecrets:         []SecretRefDTO{{Name: "mcp.linear.api_key", Env: "LINEAR_API_KEY"}},
		ExposedTools:       []string{"list_issues"},
		CallTimeoutSeconds: 60,
		Tags:               []string{"mcp", "linear"},
	})
	if err != nil {
		t.Fatalf("RegisterMCPServer: %v", err)
	}
	definition, err := toolRegistry.Get(ctx, agentcore.ToolID(registered.ToolID))
	if err != nil {
		t.Fatalf("get registered mcp: %v", err)
	}
	definition.Enabled = false
	definition.RiskLevel = toolres.RiskHigh
	definition.MCP.Auth = toolres.MCPAuthPolicy{Required: true, Mode: toolres.MCPAuthModePerAgent, HeaderName: "X-API-Key"}
	definition.MCP.Availability = toolres.MCPAvailability{Status: toolres.MCPAvailabilityRequiresAuth, Message: "credential required"}
	definition.MCP.HealthCheck = toolres.HealthCheckPolicy{Mode: toolres.HealthCheckPeriodic, Timeout: 3 * time.Second}
	if err := toolRegistry.UpdateDefinition(ctx, definition); err != nil {
		t.Fatalf("seed mcp internal metadata: %v", err)
	}

	updated, err := service.UpdateMCPServer(ctx, viewer, RegisterMCPServerRequest{
		ToolID:     registered.ToolID,
		Name:       "Linear MCP v2",
		UpdateMask: []string{"name"},
	})
	if err != nil {
		t.Fatalf("UpdateMCPServer partial: %v", err)
	}
	if updated.Name != "Linear MCP v2" || updated.Description != "Issue tracker MCP" || updated.Version != "1.0.0" || updated.ServerName != "linear" || updated.Command != "linear-mcp" {
		t.Fatalf("partial mcp update should preserve omitted scalar fields: %+v", updated)
	}
	if len(updated.Args) != 1 || updated.Args[0] != "stdio" || len(updated.SecretRefs) != 1 || updated.SecretRefs[0].Env != "LINEAR_API_KEY" || len(updated.ExposedTools) != 1 || updated.ExposedTools[0] != "list_issues" {
		t.Fatalf("partial mcp update should preserve omitted repeated fields: %+v", updated)
	}
	if updated.Enabled || updated.RiskLevel != "high" || !updated.AuthRequired || updated.AuthHeaderName != "X-API-Key" ||
		updated.AvailabilityStatus != "requires_auth" || updated.HealthCheck.Mode != "periodic" || updated.CallTimeoutSeconds != 60 {
		t.Fatalf("partial mcp update should preserve internal state: %+v", updated)
	}
}

func TestCommandServiceRegistersMCPHeadersAndRedactsSecretsInDTO(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	service := NewCommandService(toolRegistry, skillres.NewMemoryRegistry())
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}

	result, err := service.RegisterMCPServer(ctx, viewer, RegisterMCPServerRequest{
		ToolID:     "mcp.yunji-zhaopin",
		Name:       "Yunji Zhaopin MCP",
		ServerName: "yunji-zhaopin",
		Transport:  string(toolres.TransportStreamHTTP),
		URL:        "http://127.0.0.1:5000/api/mcp",
		Headers: map[string]string{
			"Authorization": "Bearer sk_test",
			"X-Tenant":      "demo",
		},
	})
	if err != nil {
		t.Fatalf("RegisterMCPServer: %v", err)
	}
	definition, err := toolRegistry.Get(ctx, agentcore.ToolID(result.ToolID))
	if err != nil {
		t.Fatalf("get mcp: %v", err)
	}
	if definition.MCP.Headers["Authorization"] != "Bearer sk_test" || definition.MCP.Headers["X-Tenant"] != "demo" {
		t.Fatalf("definition headers = %+v", definition.MCP.Headers)
	}
	if result.Headers["Authorization"] == "Bearer sk_test" || result.Headers["Authorization"] == "" {
		t.Fatalf("authorization header should be present but redacted in DTO: %+v", result.Headers)
	}
	if result.Headers["X-Tenant"] != "demo" {
		t.Fatalf("non-sensitive header should remain readable in DTO: %+v", result.Headers)
	}
}

func TestCommandServiceRegisterMCPProbesBeforeSaveAndMarksUnavailableWhenProbeFails(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	service := NewCommandService(toolRegistry, skillres.NewMemoryRegistry())
	service.SetMCPRuntime(&fakeMCPRuntime{
		err:           errors.New("connect: connection refused"),
		healthFailure: "connect: connection refused",
		latency:       25 * time.Millisecond,
	})
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}

	result, err := service.RegisterMCPServer(ctx, viewer, RegisterMCPServerRequest{
		ToolID:     "mcp.down",
		Name:       "Down MCP",
		ServerName: "down",
		Transport:  string(toolres.TransportStreamHTTP),
		URL:        "http://127.0.0.1:59999/api/mcp",
	})
	if err != nil {
		t.Fatalf("RegisterMCPServer should save even when probe fails: %v", err)
	}
	if result.Enabled {
		t.Fatalf("failed probe should mark mcp server unavailable/disabled: %+v", result)
	}
	if result.AuthRequired {
		t.Fatalf("non-401 probe failure should not require auth: %+v", result)
	}
	if result.AvailabilityStatus != "unavailable" || result.LastProbe == nil || result.LastProbe.FailureReason != "connect: connection refused" {
		t.Fatalf("probe metadata = status %q probe %+v", result.AvailabilityStatus, result.LastProbe)
	}
	definition, err := toolRegistry.Get(ctx, agentcore.ToolID(result.ToolID))
	if err != nil {
		t.Fatalf("get saved mcp: %v", err)
	}
	if definition.Enabled || definition.MCP == nil || definition.MCP.Availability.Status != toolres.MCPAvailabilityUnavailable {
		t.Fatalf("saved definition should be disabled/unavailable: %+v", definition)
	}
}

func TestCommandServiceRegisterMCPProbe401MarksAvailableAndRequiresAuth(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	service := NewCommandService(toolRegistry, skillres.NewMemoryRegistry())
	service.SetMCPRuntime(&fakeMCPRuntime{
		err:           errors.New(`mcp http initialize failed with status 401: {"error":"Unauthorized"}`),
		healthStatus:  "requires_auth",
		healthFailure: "Unauthorized: Invalid or missing API Key",
		httpStatus:    401,
		latency:       18 * time.Millisecond,
	})
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}

	result, err := service.RegisterMCPServer(ctx, viewer, RegisterMCPServerRequest{
		ToolID:     "mcp.secure",
		Name:       "Secure MCP",
		ServerName: "secure",
		Transport:  string(toolres.TransportStreamHTTP),
		URL:        "http://127.0.0.1:5000/api/mcp",
	})
	if err != nil {
		t.Fatalf("RegisterMCPServer should save 401 probe results: %v", err)
	}
	if !result.Enabled {
		t.Fatalf("401 means reachable but auth-required, not disabled: %+v", result)
	}
	if !result.AuthRequired || result.AuthMode != "per_agent" || result.AuthHeaderName != "Authorization" {
		t.Fatalf("auth metadata = required %v mode %q header %q", result.AuthRequired, result.AuthMode, result.AuthHeaderName)
	}
	if result.AvailabilityStatus != "requires_auth" || result.LastProbe == nil || result.LastProbe.HTTPStatus != 401 {
		t.Fatalf("probe metadata = status %q probe %+v", result.AvailabilityStatus, result.LastProbe)
	}
	definition, err := toolRegistry.Get(ctx, agentcore.ToolID(result.ToolID))
	if err != nil {
		t.Fatalf("get saved mcp: %v", err)
	}
	if !definition.Enabled || definition.MCP == nil || definition.MCP.Auth.Mode != toolres.MCPAuthModePerAgent || !definition.MCP.Auth.Required {
		t.Fatalf("saved definition auth metadata = %+v", definition)
	}
}

func TestCommandServiceRegisterMCP401WithDefaultAKDoesNotTrustGlobalAK(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	service := NewCommandService(toolRegistry, skillres.NewMemoryRegistry())
	service.SetMCPRuntime(&fakeMCPRuntime{
		err:           errors.New(`mcp http initialize failed with status 401: {"error":"Unauthorized"}`),
		healthStatus:  "unavailable",
		healthFailure: "Unauthorized: Invalid or missing API Key",
		httpStatus:    401,
		latency:       13 * time.Millisecond,
	})
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}

	result, err := service.RegisterMCPServer(ctx, viewer, RegisterMCPServerRequest{
		ToolID:     "mcp.secure",
		Name:       "Secure MCP",
		ServerName: "secure",
		Transport:  string(toolres.TransportStreamHTTP),
		URL:        "http://127.0.0.1:5000/api/mcp",
		Headers:    map[string]string{"Authorization": "Bearer wrong-token"},
	})
	if err != nil {
		t.Fatalf("RegisterMCPServer should save failed default AK probe results: %v", err)
	}
	if !result.Enabled || !result.AuthRequired || result.AuthMode != "per_agent" || result.DefaultAKConfigured {
		t.Fatalf("failed default AK should require per-agent auth instead of trusting global AK: %+v", result)
	}
}

func TestCommandServicePreservesRedactedMCPHeadersOnUpdate(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	service := NewCommandService(toolRegistry, skillres.NewMemoryRegistry())
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}

	registered, err := service.RegisterMCPServer(ctx, viewer, RegisterMCPServerRequest{
		ToolID:     "mcp.yunji-zhaopin",
		Name:       "Yunji Zhaopin MCP",
		ServerName: "yunji-zhaopin",
		Transport:  string(toolres.TransportStreamHTTP),
		URL:        "http://127.0.0.1:5000/api/mcp",
		Headers:    map[string]string{"Authorization": "Bearer sk_original"},
	})
	if err != nil {
		t.Fatalf("RegisterMCPServer: %v", err)
	}

	if _, err := service.UpdateMCPServer(ctx, viewer, RegisterMCPServerRequest{
		ToolID:     registered.ToolID,
		Name:       "Yunji Zhaopin MCP",
		Version:    "1.1.0",
		ServerName: "yunji-zhaopin",
		Transport:  string(toolres.TransportStreamHTTP),
		URL:        "http://127.0.0.1:5000/api/mcp",
		Headers:    map[string]string{"Authorization": redactedHeaderValue},
	}); err != nil {
		t.Fatalf("UpdateMCPServer: %v", err)
	}
	definition, err := toolRegistry.Get(ctx, agentcore.ToolID(registered.ToolID))
	if err != nil {
		t.Fatalf("get mcp: %v", err)
	}
	if definition.MCP.Headers["Authorization"] != "Bearer sk_original" {
		t.Fatalf("authorization header should be preserved, headers = %+v", definition.MCP.Headers)
	}
}

func TestCommandServiceDeleteMCPServerRejectsNonMCPTool(t *testing.T) {
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
	service := NewCommandService(toolRegistry, skillres.NewMemoryRegistry())
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}

	if err := service.DeleteMCPServer(ctx, viewer, "tool.deploy"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("DeleteMCPServer error = %v, want ErrInvalidRequest", err)
	}
	if _, err := toolRegistry.Get(ctx, "tool.deploy"); err != nil {
		t.Fatalf("non-mcp tool should remain registered: %v", err)
	}
}

func TestCommandServiceReportsMCPRuntimeNotConnectedInsteadOfFakeSync(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	service := NewCommandService(toolRegistry, skillres.NewMemoryRegistry())
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage, PermissionResourceRead}}
	registeredMCP, err := service.RegisterMCPServer(ctx, viewer, RegisterMCPServerRequest{ToolID: "mcp.github", Name: "GitHub MCP", ServerName: "github", Command: "github-mcp-server"})
	if err != nil {
		t.Fatalf("RegisterMCPServer: %v", err)
	}

	health, err := service.GetMCPServerHealth(ctx, viewer, registeredMCP.ToolID)
	if err != nil {
		t.Fatalf("GetMCPServerHealth: %v", err)
	}
	if health.Status != "not_connected" || health.FailureReason == "" {
		t.Fatalf("health should report runtime not connected: %+v", health)
	}

	synced, err := service.SyncMCPServer(ctx, viewer, registeredMCP.ToolID)
	if err != nil {
		t.Fatalf("SyncMCPServer should return readable not-connected status, got error: %v", err)
	}
	if synced.Status != "not_connected" || synced.FailureReason == "" || synced.ExposedToolCount != 0 {
		t.Fatalf("sync should report runtime not connected: %+v", synced)
	}
	definition, err := toolRegistry.Get(ctx, agentcore.ToolID(registeredMCP.ToolID))
	if err != nil {
		t.Fatalf("get mcp definition: %v", err)
	}
	if len(definition.MCP.ExposedTools) != 0 {
		t.Fatalf("sync without runtime should not write fake exposed tools: %+v", definition.MCP.ExposedTools)
	}
}

func TestCommandServiceUsesRuntimeForMCPHealthAndSync(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	runtime := &fakeMCPRuntime{
		tools: []string{"search_issues", "create_issue"},
		toolDetails: []toolres.MCPExposedToolDefinition{{
			Name:        "search_issues",
			Description: "Search issues in the tracker.",
			InputSchema: map[string]any{
				"type": "object",
			},
		}},
		latency: 37 * time.Millisecond,
	}
	service := NewCommandService(toolRegistry, skillres.NewMemoryRegistry())
	service.SetMCPRuntime(runtime)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage, PermissionResourceRead}}
	registeredMCP, err := service.RegisterMCPServer(ctx, viewer, RegisterMCPServerRequest{ToolID: "mcp.github", Name: "GitHub MCP", ServerName: "github", Command: "github-mcp-server"})
	if err != nil {
		t.Fatalf("RegisterMCPServer: %v", err)
	}

	health, err := service.GetMCPServerHealth(ctx, viewer, registeredMCP.ToolID)
	if err != nil {
		t.Fatalf("GetMCPServerHealth: %v", err)
	}
	if health.Status != "available" || health.LatencyMS != 37 || health.FailureReason != "" {
		t.Fatalf("health = %+v", health)
	}
	if runtime.healthToolID != registeredMCP.ToolID {
		t.Fatalf("runtime health tool id = %q", runtime.healthToolID)
	}

	synced, err := service.SyncMCPServer(ctx, viewer, registeredMCP.ToolID)
	if err != nil {
		t.Fatalf("SyncMCPServer: %v", err)
	}
	if synced.ExposedToolCount != 2 || synced.ExposedTools[1] != "create_issue" {
		t.Fatalf("sync = %+v", synced)
	}
	if len(synced.ExposedToolDetails) != 1 || synced.ExposedToolDetails[0].Description != "Search issues in the tracker." {
		t.Fatalf("sync details = %+v", synced.ExposedToolDetails)
	}
	definition, err := toolRegistry.Get(ctx, agentcore.ToolID(registeredMCP.ToolID))
	if err != nil {
		t.Fatalf("get synced definition: %v", err)
	}
	if len(definition.MCP.ExposedTools) != 2 || definition.MCP.ExposedTools[0] != "search_issues" {
		t.Fatalf("definition tools = %+v", definition.MCP.ExposedTools)
	}
	if len(definition.MCP.ExposedToolDetails) != 1 || definition.MCP.ExposedToolDetails[0].InputSchema["type"] != "object" {
		t.Fatalf("definition tool details = %+v", definition.MCP.ExposedToolDetails)
	}
	if definition.MCP.Availability.Status != toolres.MCPAvailabilityAvailable || definition.MCP.Availability.CheckedAt.IsZero() {
		t.Fatalf("definition availability = %+v", definition.MCP.Availability)
	}
}

func TestCommandServiceUsesCachedSystemMCPHealthAndSync(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	systemMCP := toolstd.SystemMCPServerDefinition(time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC))
	if err := toolRegistry.Register(ctx, systemMCP); err != nil {
		t.Fatalf("register system mcp: %v", err)
	}
	runtime := &fakeMCPRuntime{err: errors.New("should not call runtime")}
	service := NewCommandService(toolRegistry, skillres.NewMemoryRegistry())
	service.SetMCPRuntime(runtime)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage, PermissionResourceRead}}

	health, err := service.GetMCPServerHealth(ctx, viewer, string(toolstd.SystemMCPServer))
	if err != nil {
		t.Fatalf("GetMCPServerHealth: %v", err)
	}
	if health.Status != "available" || runtime.healthToolID != "" {
		t.Fatalf("system mcp health should use cached platform status, health=%+v runtime=%+v", health, runtime)
	}

	synced, err := service.SyncMCPServer(ctx, viewer, string(toolstd.SystemMCPServer))
	if err != nil {
		t.Fatalf("SyncMCPServer: %v", err)
	}
	if synced.Status != "synced" || synced.ExposedToolCount != len(systemMCP.MCP.ExposedTools) || runtime.syncToolID != "" {
		t.Fatalf("system mcp sync should use cached tools, sync=%+v runtime=%+v", synced, runtime)
	}
}

func TestCommandServiceUsesCachedLockedMCPToolSurface(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	definition := toolres.ToolDefinition{
		ID: "mcp.restricted", Kind: toolres.KindMCPServer, Name: "Restricted", Version: "1.0.0", Enabled: true, RiskLevel: toolres.RiskMedium,
		Tags: []string{toolres.TagMCPToolSurfaceLocked},
		MCP:  &toolres.MCPServerDefinition{ServerName: "restricted", Transport: toolres.TransportStdio, Command: "restricted-mcp", ExposedTools: []string{"navigate_page"}},
	}
	if err := toolRegistry.Register(ctx, definition); err != nil {
		t.Fatalf("register locked mcp: %v", err)
	}
	runtime := &fakeMCPRuntime{tools: []string{"navigate_page", "evaluate_script"}}
	service := NewCommandService(toolRegistry, skillres.NewMemoryRegistry())
	service.SetMCPRuntime(runtime)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}

	synced, err := service.SyncMCPServer(ctx, viewer, "mcp.restricted")
	if err != nil {
		t.Fatalf("SyncMCPServer: %v", err)
	}
	if synced.Status != "synced" || synced.ExposedToolCount != 1 || synced.ExposedTools[0] != "navigate_page" || runtime.syncToolID != "" {
		t.Fatalf("locked mcp sync should preserve cached tools, sync=%+v runtime=%+v", synced, runtime)
	}
}

func TestCommandServiceReturnsUnavailableMCPHealthWithout500(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	service := NewCommandService(toolRegistry, skillres.NewMemoryRegistry())
	service.SetMCPRuntime(&fakeMCPRuntime{err: errors.New("connection refused"), latency: 12 * time.Millisecond})
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage, PermissionResourceRead}}
	registeredMCP, err := service.RegisterMCPServer(ctx, viewer, RegisterMCPServerRequest{ToolID: "mcp.down", Name: "Down MCP", ServerName: "down", Command: "down-mcp"})
	if err != nil {
		t.Fatalf("RegisterMCPServer: %v", err)
	}

	health, err := service.GetMCPServerHealth(ctx, viewer, registeredMCP.ToolID)
	if err != nil {
		t.Fatalf("GetMCPServerHealth should return readable unavailable status, got error: %v", err)
	}
	if health.Status != "unavailable" || health.FailureReason != "connection refused" || health.LatencyMS != 12 {
		t.Fatalf("health = %+v", health)
	}
}

func TestCommandServiceReturnsFailedMCPSyncWithout500(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	audit := &fakeResourceAuditRecorder{}
	service := NewCommandService(toolRegistry, skillres.NewMemoryRegistry())
	service.SetAuditRecorder(audit)
	service.SetMCPRuntime(&fakeMCPRuntime{err: errors.New("list tools failed")})
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}
	registeredMCP, err := service.RegisterMCPServer(ctx, viewer, RegisterMCPServerRequest{ToolID: "mcp.failing", Name: "Failing MCP", ServerName: "failing", Command: "failing-mcp"})
	if err != nil {
		t.Fatalf("RegisterMCPServer: %v", err)
	}

	synced, err := service.SyncMCPServer(ctx, viewer, registeredMCP.ToolID)
	if err != nil {
		t.Fatalf("SyncMCPServer should return failed status without error, got: %v", err)
	}
	if synced.Status != "failed" || synced.FailureReason != "list tools failed" || synced.ExposedToolCount != 0 {
		t.Fatalf("failed sync dto = %+v", synced)
	}
	if len(audit.events) < 2 {
		t.Fatalf("audit events = %+v", audit.events)
	}
	event := audit.events[len(audit.events)-1]
	if event.Action != "resource.mcp.sync_failed" || event.Result != "failed" || event.Severity != "error" {
		t.Fatalf("failed sync audit = %+v", event)
	}
}

func TestCommandServiceUpdatesMCPAndValidatesTransport(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	service := NewCommandService(toolRegistry, skillres.NewMemoryRegistry())
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}
	registeredMCP, err := service.RegisterMCPServer(ctx, viewer, RegisterMCPServerRequest{
		ToolID:     "mcp.github",
		Name:       "GitHub MCP",
		ServerName: "github",
		Command:    "github-mcp-server",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	updated, err := service.UpdateMCPServer(ctx, viewer, RegisterMCPServerRequest{
		ToolID:       registeredMCP.ToolID,
		Name:         "GitHub MCP",
		Version:      "1.1.0",
		ServerName:   "github",
		Transport:    string(toolres.TransportStdio),
		Command:      "github-mcp-server",
		Args:         []string{"stdio"},
		EnvSecrets:   []SecretRefDTO{{Name: "github-token", Env: "GITHUB_TOKEN"}},
		ExposedTools: []string{"search_issues"},
	})
	if err != nil {
		t.Fatalf("UpdateMCPServer: %v", err)
	}
	if updated.Version != "1.1.0" || updated.Args[0] != "stdio" {
		t.Fatalf("updated = %+v", updated)
	}

	if _, err := service.RegisterMCPServer(ctx, viewer, RegisterMCPServerRequest{
		ToolID:     "mcp.bad",
		Name:       "Bad MCP",
		ServerName: "bad",
		Transport:  string(toolres.TransportStdio),
	}); err == nil {
		t.Fatalf("expected stdio command validation error")
	}
	if _, err := service.RegisterMCPServer(ctx, viewer, RegisterMCPServerRequest{
		ToolID:     "mcp.bad-http",
		Name:       "Bad HTTP MCP",
		ServerName: "bad-http",
		Transport:  string(toolres.TransportStreamHTTP),
	}); err == nil {
		t.Fatalf("expected stream http url validation error")
	}
}
