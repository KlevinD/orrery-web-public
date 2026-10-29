package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/operations"
	resourceadmin "orrery_v2/internal/app/resourceadmin"
	toolres "orrery_v2/internal/resource/tool"
	mcptransport "orrery_v2/internal/transport/mcp"
)

func TestHTTPRoutesResourceQueryFiltersThroughResourceQueryService(t *testing.T) {
	query := &fakeResourceQuery{}
	handler := newTestRouter(RouterOptions{ResourceQuery: query})

	req := authedResourceRequest(http.MethodGet, "/api/resources/skills?search=deploy&category=operations&risk_level=high&enabled=true&resource_id=skill.deploy&bound_agent_id=profile-ops&required_tool_id=tool.deploy&include_usage=true&include_bindings=true&limit=25&offset=50", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if query.req.Search != "deploy" || query.req.Kind != "skill" || query.req.Category != "operations" || query.req.RiskLevel != "high" {
		t.Fatalf("query request = %+v", query.req)
	}
	if query.req.ResourceID != "skill.deploy" || query.req.BoundAgentID != "profile-ops" || query.req.RequiredToolID != "tool.deploy" {
		t.Fatalf("query request = %+v", query.req)
	}
	if query.req.Enabled == nil || !*query.req.Enabled || !query.req.IncludeUsage || !query.req.IncludeBindings || query.req.Limit != 25 || query.req.Offset != 50 {
		t.Fatalf("query request = %+v", query.req)
	}
}

func TestBreakglassGatewayCatalogDoesNotBindReservedWorkforceProfile(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/resources/tools", nil)
	req.Header.Set("X-Agent-Profile-ID", agentcore.SystemSuperadminPrincipalID)
	req = req.WithContext(identity.ContextWithPrincipal(req.Context(), identity.Principal{
		SubjectProvider: identity.ProviderLocal,
		Subject:         "superadmin",
		UserID:          "superadmin",
		Username:        "superadmin",
		AgentProfileID:  agentcore.SystemSuperadminPrincipalID,
		ViewerKind:      identity.ViewerKindUser,
	}))
	viewer := resourceadmin.ViewerRef{ViewerID: agentcore.SystemSuperadminPrincipalID}

	if profileID := gatewayExecutionProfileID(req); profileID != "" {
		t.Fatalf("gateway profile id = %q", profileID)
	}
	if agentID := gatewayExecutionAgentID(req, viewer, ""); agentID != "" {
		t.Fatalf("gateway agent id = %q", agentID)
	}
}

func TestHTTPRoutesListsSkillBundlesThroughResourceQueryService(t *testing.T) {
	query := &fakeResourceQuery{skillBundles: resourceadmin.SkillBundleListDTO{Bundles: []resourceadmin.SkillBundleDTO{{
		BundleID: "skill-bundle-quality",
		Name:     "Quality pack",
		SkillIDs: []string{"skill.review", "skill.test"},
	}}}}
	handler := newTestRouter(RouterOptions{ResourceQuery: query})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authedResourceRequest(http.MethodGet, "/api/resources/skill-bundles", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !query.listSkillBundlesCalled {
		t.Fatal("ListSkillBundles was not called")
	}
	var got resourceadmin.SkillBundleListDTO
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode bundle list: %v", err)
	}
	if len(got.Bundles) != 1 || got.Bundles[0].BundleID != "skill-bundle-quality" {
		t.Fatalf("bundle list = %+v", got)
	}
}

func TestHTTPRoutesDiscoverMCPServersReportsUnconfiguredDiscoveryAsFailedPrecondition(t *testing.T) {
	query := &fakeResourceQuery{discoverErr: resourceadmin.ErrServiceDependency}
	handler := newTestRouter(RouterOptions{ResourceQuery: query})

	req := authedResourceRequest(http.MethodGet, "/api/resources/mcp-servers/discover?query=github", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var body ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Code != ErrorFailedPrecondition || body.Details["field"] != "discovery" || body.Details["reason"] != "not_configured" {
		t.Fatalf("error body = %+v", body)
	}
}

func TestHTTPRoutesResourceToolsExpandsCurrentViewerMCPServerBinding(t *testing.T) {
	registry := toolres.NewMemoryRegistry()
	if err := registry.Register(context.Background(), toolres.ToolDefinition{
		ID:        agentcore.ToolID("tool.deploy"),
		Kind:      toolres.KindInternalServiceTool,
		Name:      "Deploy",
		Version:   "1.0.0",
		Enabled:   true,
		RiskLevel: toolres.RiskHigh,
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	if err := registry.Register(context.Background(), toolres.ToolDefinition{
		ID:          agentcore.ToolID("mcp.system"),
		Kind:        toolres.KindMCPServer,
		Name:        "System MCP",
		Description: "System MCP tools",
		Version:     "1.0.0",
		Enabled:     true,
		RiskLevel:   toolres.RiskMedium,
		MCP: &toolres.MCPServerDefinition{
			ServerName:   "system",
			Transport:    toolres.TransportStreamHTTP,
			URL:          "/mcp",
			ExposedTools: []string{"mission_prepare"},
		},
	}); err != nil {
		t.Fatalf("register system mcp: %v", err)
	}
	if err := registry.Register(context.Background(), toolres.ToolDefinition{
		ID:          agentcore.ToolID("mcp.github"),
		Kind:        toolres.KindMCPServer,
		Name:        "GitHub MCP",
		Description: "GitHub issue tools",
		Version:     "1.0.0",
		Enabled:     true,
		RiskLevel:   toolres.RiskMedium,
		Permissions: []toolres.PermissionDeclaration{{
			Scope:    "github.read",
			Risk:     toolres.RiskMedium,
			Required: true,
		}},
		MCP: &toolres.MCPServerDefinition{
			ServerName:   "github",
			Transport:    toolres.TransportStdio,
			Command:      "github-mcp-server",
			ExposedTools: []string{"search_issues", "create_issue"},
		},
	}); err != nil {
		t.Fatalf("register mcp: %v", err)
	}
	profiles := fakeHTTPGatewayProfileStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"creator-1": {
				ID: "creator-1",
				ToolRefs: []agentcore.ToolRef{{
					ToolID:          "mcp.github",
					PermissionScope: "github.read",
				}},
			},
		},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID:  "creator-1",
			AgentInstanceID: "creator-1",
		}}},
	}
	query := &fakeResourceQuery{}
	handler := newTestRouter(RouterOptions{
		ResourceQuery: query,
		MCPGateway:    mcptransport.NewGateway(mcptransport.GatewayOptions{Registry: registry, Profiles: profiles}),
	})

	req := authedResourceRequest(http.MethodGet, "/api/resources/tools?limit=20&offset=0", "")
	req.Header.Set("X-User-ID", "creator-1")
	req.Header.Set("X-Agent-Profile-ID", "creator-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if query.listCalled {
		t.Fatalf("tools menu should use MCP server tool set, not resource query registry list")
	}
	var view resourceadmin.ResourceAdminViewDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if view.Page.Total != 2 || len(view.Tools) != 2 {
		t.Fatalf("tools view = %+v", view)
	}
	if hasToolDTO(view.Tools, "tool.deploy", "", "", true) {
		t.Fatalf("bare internal tool should not be exposed in MCP-backed tools menu: %+v", view.Tools)
	}
	if !hasToolDTO(view.Tools, "mcp.github.search_issues", "mcp.github", "search_issues", false) {
		t.Fatalf("missing expanded MCP search tool in %+v", view.Tools)
	}
	if !hasToolDTO(view.Tools, "mcp.github.create_issue", "mcp.github", "create_issue", false) {
		t.Fatalf("missing expanded MCP create tool in %+v", view.Tools)
	}
}

func TestHTTPRoutesResourceToolsUsesCurrentViewerConfiguredToolSet(t *testing.T) {
	ctx := context.Background()
	registry := toolres.NewMemoryRegistry()
	if err := registry.Register(ctx, toolres.ToolDefinition{
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
			ExposedTools: []string{"search_issues", "create_issue"},
		},
	}); err != nil {
		t.Fatalf("register mcp: %v", err)
	}
	profiles := fakeHTTPGatewayProfileStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"creator-1": {
				ID: "creator-1",
				ToolRefs: []agentcore.ToolRef{{
					ToolID:          "mcp.github.search_issues",
					PermissionScope: "github.read",
				}},
			},
		},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID:  "creator-1",
			AgentInstanceID: "creator-runtime",
		}}},
	}
	handler := newTestRouter(RouterOptions{
		ResourceQuery: &fakeResourceQuery{},
		MCPGateway:    mcptransport.NewGateway(mcptransport.GatewayOptions{Registry: registry, Profiles: profiles}),
	})

	req := authedResourceRequest(http.MethodGet, "/api/resources/tools?limit=20&offset=0", "")
	req.Header.Set("X-User-ID", "creator-1")
	req.Header.Set("X-Agent-Profile-ID", "creator-1")
	req.Header.Set("X-Permission-Scope", "resource.read")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var view resourceadmin.ResourceAdminViewDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if view.Page.Total != 1 || len(view.Tools) != 1 {
		t.Fatalf("tools view = %+v", view)
	}
	if !hasToolDTO(view.Tools, "mcp.github.search_issues", "mcp.github", "search_issues", false) {
		t.Fatalf("missing profile-bound tool in %+v", view.Tools)
	}
	if hasToolDTO(view.Tools, "mcp.github.create_issue", "mcp.github", "create_issue", false) {
		t.Fatalf("unbound tool leaked into profile-scoped list: %+v", view.Tools)
	}
}

func TestHTTPRoutesResourceToolsFallsBackToCurrentViewerHumanProfile(t *testing.T) {
	ctx := context.Background()
	registry := toolres.NewMemoryRegistry()
	if err := registry.Register(ctx, toolres.ToolDefinition{
		ID:        agentcore.ToolID("tool.deploy"),
		Kind:      toolres.KindInternalServiceTool,
		Name:      "Deploy",
		Version:   "1.0.0",
		Enabled:   true,
		RiskLevel: toolres.RiskHigh,
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	profiles := fakeHTTPGatewayProfileStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"human-superadmin": {
				ID:       "human-superadmin",
				ToolRefs: []agentcore.ToolRef{{ToolID: "mcp.system"}},
			},
		},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID:  "human-superadmin",
			AgentInstanceID: "human-superadmin",
		}}},
	}
	handler := newTestRouter(RouterOptions{
		ResourceQuery: &fakeResourceQuery{},
		MCPGateway:    mcptransport.NewGateway(mcptransport.GatewayOptions{Registry: registry, Profiles: profiles}),
	})

	req := authedResourceRequest(http.MethodGet, "/api/resources/tools?limit=20&offset=0", "")
	req.Header.Set("X-User-ID", "superadmin")
	req.Header.Set("X-Permission-Scope", "resource.read")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var view resourceadmin.ResourceAdminViewDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if view.Page.Total != 1 || !hasToolDTO(view.Tools, "tool.deploy", "", "", true) {
		t.Fatalf("tools view = %+v", view)
	}
}

func TestHTTPRoutesResourceToolsReturnsEmptyWhenCurrentViewerHasNoProfile(t *testing.T) {
	registry := toolres.NewMemoryRegistry()
	if err := registry.Register(context.Background(), toolres.ToolDefinition{
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
		},
	}); err != nil {
		t.Fatalf("register mcp: %v", err)
	}
	handler := newTestRouter(RouterOptions{
		ResourceQuery: &fakeResourceQuery{},
		MCPGateway: mcptransport.NewGateway(mcptransport.GatewayOptions{
			Registry: registry,
			Profiles: fakeHTTPGatewayProfileStore{
				profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{},
				page:     agentcore.AgentDirectoryPage{},
			},
		}),
	})

	req := authedResourceRequest(http.MethodGet, "/api/resources/tools?limit=20&offset=0", "")
	req.Header.Set("X-User-ID", "missing-profile-user")
	req.Header.Set("X-Agent-Profile-ID", "missing-profile")
	req.Header.Set("X-Permission-Scope", "resource.read")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var view resourceadmin.ResourceAdminViewDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if view.Page.Total != 0 || len(view.Tools) != 0 {
		t.Fatalf("missing profile should yield empty tool list, got %+v", view)
	}
}

func TestHTTPRoutesResourceToolsDoesNotFallbackToUsernameAsRawProfile(t *testing.T) {
	registry := toolres.NewMemoryRegistry()
	if err := registry.Register(context.Background(), toolres.ToolDefinition{
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
		},
	}); err != nil {
		t.Fatalf("register mcp: %v", err)
	}
	profiles := fakeHTTPGatewayProfileStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"superadmin": {
				ID: "superadmin",
				ToolRefs: []agentcore.ToolRef{{
					ToolID:          "mcp.github",
					PermissionScope: "github.read",
				}},
			},
		},
	}
	handler := newTestRouter(RouterOptions{
		ResourceQuery: &fakeResourceQuery{},
		MCPGateway:    mcptransport.NewGateway(mcptransport.GatewayOptions{Registry: registry, Profiles: profiles}),
	})

	req := authedResourceRequest(http.MethodGet, "/api/resources/tools?limit=20&offset=0", "")
	req.Header.Set("X-User-ID", "superadmin")
	req.Header.Set("X-Permission-Scope", "resource.read")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var view resourceadmin.ResourceAdminViewDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if view.Page.Total != 0 || len(view.Tools) != 0 {
		t.Fatalf("username should not be used as profile fallback, got %+v", view)
	}
}

func TestHTTPRoutesResourceToolCallUsesCurrentViewerIdentity(t *testing.T) {
	registry := toolres.NewMemoryRegistry()
	if err := registry.Register(context.Background(), toolres.ToolDefinition{
		ID:          agentcore.ToolID("mcp.github"),
		Kind:        toolres.KindMCPServer,
		Name:        "GitHub MCP",
		Description: "GitHub issue tools",
		Version:     "1.0.0",
		Enabled:     true,
		RiskLevel:   toolres.RiskMedium,
		Permissions: []toolres.PermissionDeclaration{{
			Scope:    "github.read",
			Risk:     toolres.RiskMedium,
			Required: true,
		}},
		MCP: &toolres.MCPServerDefinition{
			ServerName:   "github",
			Transport:    toolres.TransportStdio,
			Command:      "github-mcp-server",
			ExposedTools: []string{"search_issues"},
		},
	}); err != nil {
		t.Fatalf("register mcp: %v", err)
	}
	external := &recordingResourceExternalMCP{
		result: mcptransport.CallToolResult{Output: map[string]any{"count": float64(2)}},
	}
	profiles := fakeHTTPGatewayProfileStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"creator-1": {
				ID: "creator-1",
				ToolRefs: []agentcore.ToolRef{{
					ToolID:          "mcp.github.search_issues",
					PermissionScope: "github.read",
				}},
			},
		},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID:  "creator-1",
			AgentInstanceID: "creator-1",
		}}},
	}
	gateway := mcptransport.NewGateway(mcptransport.GatewayOptions{
		Registry: registry,
		Profiles: profiles,
		Router: mcptransport.NewToolRouter(mcptransport.ToolRouterOptions{
			Registry:    registry,
			ExternalMCP: external,
		}),
	})
	handler := newTestRouter(RouterOptions{
		ResourceQuery: &fakeResourceQuery{},
		Operations:    allowGatewayOperations(),
		MCPGateway:    gateway,
	})

	req := authedResourceRequest(http.MethodPost, "/api/resources/tools/call", `{
		"tool_id":"mcp.github.search_issues",
		"principal_id":"attacker",
		"agent_id":"attacker-agent",
		"permissions":["github.admin"],
		"arguments":{"query":"is:open"}
	}`)
	req.Header.Set("X-User-ID", "creator-1")
	req.Header.Set("X-Agent-Profile-ID", "creator-1")
	req.Header.Set("X-Permission-Scope", "resource.read,tool.call,github.read")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if external.req.PrincipalID != "creator-1" {
		t.Fatalf("principal id = %q, want current viewer", external.req.PrincipalID)
	}
	if strings.Join(external.req.Permissions, ",") != "resource.read,tool.call,github.read" {
		t.Fatalf("permissions = %+v", external.req.Permissions)
	}
	if external.req.Arguments["query"] != "is:open" {
		t.Fatalf("arguments = %+v", external.req.Arguments)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"count":2`) {
		t.Fatalf("body = %s", body)
	}
}

func TestHTTPRoutesResourceToolCallMasksOutputWithDataPolicy(t *testing.T) {
	registry := toolres.NewMemoryRegistry()
	if err := registry.Register(context.Background(), toolres.ToolDefinition{
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
		},
	}); err != nil {
		t.Fatalf("register mcp: %v", err)
	}
	external := &recordingResourceExternalMCP{
		result: mcptransport.CallToolResult{Output: map[string]any{
			"title":        "open issue",
			"secret_token": "raw-token",
		}},
	}
	policy := &fixedToolResultPolicy{decision: contextgraph.PermissionDecision{
		MaskedFields: map[string][]string{"mcp.github.search_issues": {"secret_token"}},
		Constraints: []contextgraph.PermissionConstraint{{
			Scope:        "tool_result",
			ObjectRef:    contextgraph.Ref{Kind: "tool_result", ID: "mcp.github.search_issues"},
			MaskedFields: []string{"secret_token"},
			Reason:       "secret token is not needed",
		}},
	}}
	profiles := fakeHTTPGatewayProfileStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"creator-1": {
				ID: "creator-1",
				ToolRefs: []agentcore.ToolRef{{
					ToolID:          "mcp.github.search_issues",
					PermissionScope: "github.read",
				}},
			},
		},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID:  "creator-1",
			AgentInstanceID: "creator-runtime",
		}}},
	}
	handler := newTestRouter(RouterOptions{
		ResourceQuery: &fakeResourceQuery{},
		Operations:    allowGatewayOperations(),
		MCPGateway: mcptransport.NewGateway(mcptransport.GatewayOptions{
			Registry: registry,
			Profiles: profiles,
			Router: mcptransport.NewToolRouter(mcptransport.ToolRouterOptions{
				Registry:    registry,
				ExternalMCP: external,
			}),
		}),
		ToolResultPolicy: policy,
	})

	req := authedResourceRequest(http.MethodPost, "/api/resources/tools/call", `{"tool_id":"mcp.github.search_issues","arguments":{"query":"is:open"}}`)
	req.Header.Set("X-User-ID", "creator-1")
	req.Header.Set("X-Agent-Profile-ID", "creator-1")
	req.Header.Set("X-Permission-Scope", "resource.read,tool.call,github.read")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); strings.Contains(body, "raw-token") || strings.Contains(body, `"secret_token":"`) || !strings.Contains(body, `"title":"open issue"`) {
		t.Fatalf("body was not filtered by data policy: %s", body)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"masked_fields":["secret_token"]`) || !strings.Contains(body, `"permission_constraints"`) || !strings.Contains(body, `"reason":"secret token is not needed"`) {
		t.Fatalf("body missing data policy audit summary: %s", body)
	}
	if len(policy.objects) != 1 || policy.objects[0].ID != "mcp.github.search_issues" || policy.objects[0].ObjectType != "tool_result" {
		t.Fatalf("policy objects = %+v", policy.objects)
	}
}

func TestHTTPRoutesResourceToolCallMasksNestedOutputWithDataPolicy(t *testing.T) {
	registry := toolres.NewMemoryRegistry()
	if err := registry.Register(context.Background(), toolres.ToolDefinition{
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
		},
	}); err != nil {
		t.Fatalf("register mcp: %v", err)
	}
	external := &recordingResourceExternalMCP{
		result: mcptransport.CallToolResult{Output: map[string]any{
			"profile": map[string]any{
				"name":  "Ada",
				"email": "ada@example.test",
			},
			"items": []any{
				map[string]any{
					"title": "open issue",
					"token": "item-token",
				},
			},
		}},
	}
	policy := &fixedToolResultPolicy{decision: contextgraph.PermissionDecision{
		MaskedFields: map[string][]string{"mcp.github.search_issues": {"profile.email", "/items/0/token"}},
	}}
	profiles := fakeHTTPGatewayProfileStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"creator-1": {
				ID: "creator-1",
				ToolRefs: []agentcore.ToolRef{{
					ToolID:          "mcp.github.search_issues",
					PermissionScope: "github.read",
				}},
			},
		},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID:  "creator-1",
			AgentInstanceID: "creator-runtime",
		}}},
	}
	handler := newTestRouter(RouterOptions{
		ResourceQuery: &fakeResourceQuery{},
		Operations:    allowGatewayOperations(),
		MCPGateway: mcptransport.NewGateway(mcptransport.GatewayOptions{
			Registry: registry,
			Profiles: profiles,
			Router: mcptransport.NewToolRouter(mcptransport.ToolRouterOptions{
				Registry:    registry,
				ExternalMCP: external,
			}),
		}),
		ToolResultPolicy: policy,
	})

	req := authedResourceRequest(http.MethodPost, "/api/resources/tools/call", `{"tool_id":"mcp.github.search_issues","arguments":{"query":"is:open"}}`)
	req.Header.Set("X-User-ID", "creator-1")
	req.Header.Set("X-Agent-Profile-ID", "creator-1")
	req.Header.Set("X-Permission-Scope", "resource.read,tool.call,github.read")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "ada@example.test") || strings.Contains(body, "item-token") {
		t.Fatalf("nested sensitive fields were not filtered by data policy: %s", body)
	}
	if !strings.Contains(body, `"name":"Ada"`) || !strings.Contains(body, `"title":"open issue"`) {
		t.Fatalf("non-sensitive nested fields were removed: %s", body)
	}
}

func TestHTTPRoutesResourceToolCallHidesOutputWithDataPolicy(t *testing.T) {
	registry := toolres.NewMemoryRegistry()
	if err := registry.Register(context.Background(), toolres.ToolDefinition{
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
		},
	}); err != nil {
		t.Fatalf("register mcp: %v", err)
	}
	external := &recordingResourceExternalMCP{
		result: mcptransport.CallToolResult{Output: map[string]any{
			"title":        "open issue",
			"secret_token": "raw-token",
		}},
	}
	policy := &fixedToolResultPolicy{decision: contextgraph.PermissionDecision{
		HiddenObjectIDs: []string{"mcp.github.search_issues"},
		Constraints: []contextgraph.PermissionConstraint{{
			Scope:     "tool_result",
			ObjectRef: contextgraph.Ref{Kind: "tool_result", ID: "mcp.github.search_issues"},
			Reason:    "tool result hidden for this purpose",
		}},
	}}
	profiles := fakeHTTPGatewayProfileStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"creator-1": {
				ID: "creator-1",
				ToolRefs: []agentcore.ToolRef{{
					ToolID:          "mcp.github.search_issues",
					PermissionScope: "github.read",
				}},
			},
		},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID:  "creator-1",
			AgentInstanceID: "creator-runtime",
		}}},
	}
	handler := newTestRouter(RouterOptions{
		ResourceQuery: &fakeResourceQuery{},
		Operations:    allowGatewayOperations(),
		MCPGateway: mcptransport.NewGateway(mcptransport.GatewayOptions{
			Registry: registry,
			Profiles: profiles,
			Router: mcptransport.NewToolRouter(mcptransport.ToolRouterOptions{
				Registry:    registry,
				ExternalMCP: external,
			}),
		}),
		ToolResultPolicy: policy,
	})

	req := authedResourceRequest(http.MethodPost, "/api/resources/tools/call", `{"tool_id":"mcp.github.search_issues","arguments":{"query":"is:open"}}`)
	req.Header.Set("X-User-ID", "creator-1")
	req.Header.Set("X-Agent-Profile-ID", "creator-1")
	req.Header.Set("X-Permission-Scope", "resource.read,tool.call,github.read")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); strings.Contains(body, "secret_token") || strings.Contains(body, "raw-token") || strings.Contains(body, "open issue") {
		t.Fatalf("body was not hidden by data policy: %s", body)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"hidden":true`) || !strings.Contains(body, `"permission_constraints"`) || !strings.Contains(body, `"reason":"tool result hidden for this purpose"`) {
		t.Fatalf("body missing hidden data policy audit summary: %s", body)
	}
}

func TestDeleteMaskedOutputFieldSupportsPointerEscapesAndArrayLeaves(t *testing.T) {
	output := map[string]any{
		"items": []any{
			map[string]any{
				"a/b":  "slash-secret",
				"keep": "visible",
			},
		},
		"tokens": []any{"first-secret", "visible"},
	}

	deleteMaskedOutputField(output, "/items/0/a~1b")
	deleteMaskedOutputField(output, "/tokens/0")

	item, ok := output["items"].([]any)[0].(map[string]any)
	if !ok {
		t.Fatalf("items output = %+v", output["items"])
	}
	if _, ok := item["a/b"]; ok {
		t.Fatalf("escaped JSON pointer field was not deleted: %+v", output)
	}
	if item["keep"] != "visible" {
		t.Fatalf("non-sensitive field was removed: %+v", output)
	}
	tokens, ok := output["tokens"].([]any)
	if !ok || len(tokens) != 2 || tokens[0] != nil || tokens[1] != "visible" {
		t.Fatalf("array leaf deletion = %+v", output["tokens"])
	}
}

func TestHTTPRoutesResourceToolCallUsesActionAwarePermissionDecision(t *testing.T) {
	registry := toolres.NewMemoryRegistry()
	if err := registry.Register(context.Background(), toolres.ToolDefinition{
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
		},
	}); err != nil {
		t.Fatalf("register mcp: %v", err)
	}
	external := &recordingResourceExternalMCP{
		result: mcptransport.CallToolResult{Output: map[string]any{"count": float64(2)}},
	}
	profiles := fakeHTTPGatewayProfileStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"creator-1": {
				ID: "creator-1",
				ToolRefs: []agentcore.ToolRef{{
					ToolID:          "mcp.github.search_issues",
					PermissionScope: "github.read",
				}},
			},
		},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID:  "creator-1",
			AgentInstanceID: "creator-runtime",
		}}},
	}
	ops := &fakeOperationsService{permissionDecision: operations.PermissionDecisionDTO{
		Decision: operations.PermissionDecisionAllow,
		Sources: []operations.PermissionDecisionSourceDTO{{
			SourceType: "role_assignment",
			Effect:     operations.PermissionDecisionAllow,
		}},
	}}
	handler := newTestRouter(RouterOptions{
		ResourceQuery: &fakeResourceQuery{},
		Operations:    ops,
		MCPGateway: mcptransport.NewGateway(mcptransport.GatewayOptions{
			Registry: registry,
			Profiles: profiles,
			Router: mcptransport.NewToolRouter(mcptransport.ToolRouterOptions{
				Registry:    registry,
				ExternalMCP: external,
			}),
		}),
	})

	req := authedResourceRequest(http.MethodPost, "/api/resources/tools/call", `{"tool_id":"mcp.github.search_issues","arguments":{"query":"is:open"}}`)
	req.Header.Set("X-User-ID", "creator-1")
	req.Header.Set("X-Agent-Profile-ID", "creator-1")
	req.Header.Set("X-Permission-Scope", "resource.read,tool.call")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	decisionReq := ops.permissionDecisionReq
	if decisionReq.ActionTypeID != "mcp.tool.call" || decisionReq.ResourceType != "tool" || decisionReq.ResourceID != "mcp.github.search_issues" {
		t.Fatalf("permission decision request = %+v", decisionReq)
	}
	if decisionReq.Subject != (operations.ActorRefDTO{ActorType: "agent", ActorID: "creator-1"}) {
		t.Fatalf("decision subject = %+v", decisionReq.Subject)
	}
	if decisionReq.OnBehalfOf != (operations.ActorRefDTO{ActorType: "human", ActorID: "creator-1"}) {
		t.Fatalf("decision effective user = %+v", decisionReq.OnBehalfOf)
	}
	if len(decisionReq.ObjectRefs) != 1 || decisionReq.ObjectRefs[0].ResourceType != "tool" || decisionReq.ObjectRefs[0].ResourceID != "mcp.github.search_issues" {
		t.Fatalf("decision object refs = %+v", decisionReq.ObjectRefs)
	}
	if !strings.HasPrefix(decisionReq.ArgsHash, "sha256:") || decisionReq.ArgsHash == "sha256:" {
		t.Fatalf("decision args hash = %q", decisionReq.ArgsHash)
	}
	if external.req.DownstreamToolName != "search_issues" {
		t.Fatalf("tool call did not reach external mcp after allow: %+v", external.req)
	}
}

func TestHTTPRoutesResourceToolCallStopsWhenActionPermissionDenied(t *testing.T) {
	registry := toolres.NewMemoryRegistry()
	if err := registry.Register(context.Background(), toolres.ToolDefinition{
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
		},
	}); err != nil {
		t.Fatalf("register mcp: %v", err)
	}
	external := &recordingResourceExternalMCP{}
	ops := &fakeOperationsService{permissionDecision: operations.PermissionDecisionDTO{
		Decision: operations.PermissionDecisionDeny,
		Reason:   "missing policy denies request",
		Sources: []operations.PermissionDecisionSourceDTO{{
			SourceType: "missing_policy",
			Effect:     operations.PermissionDecisionDeny,
		}},
	}}
	handler := newTestRouter(RouterOptions{
		ResourceQuery: &fakeResourceQuery{},
		Operations:    ops,
		MCPGateway: mcptransport.NewGateway(mcptransport.GatewayOptions{
			Registry: registry,
			Router: mcptransport.NewToolRouter(mcptransport.ToolRouterOptions{
				Registry:    registry,
				ExternalMCP: external,
			}),
		}),
	})

	req := authedResourceRequest(http.MethodPost, "/api/resources/tools/call", `{"tool_id":"mcp.github.search_issues","arguments":{"query":"is:open"}}`)
	req.Header.Set("X-User-ID", "creator-1")
	req.Header.Set("X-Permission-Scope", "resource.read,tool.call")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if external.req.DownstreamToolName != "" {
		t.Fatalf("denied action should not reach external mcp: %+v", external.req)
	}
	if ops.permissionDecisionReq.ActionTypeID != "mcp.tool.call" {
		t.Fatalf("permission decision request = %+v", ops.permissionDecisionReq)
	}
}

func TestHTTPRoutesResourceToolCallFailsClosedWithoutOperationsService(t *testing.T) {
	registry := registerGatewayReviewMCP(t)
	external := &recordingResourceExternalMCP{}
	handler := newTestRouter(RouterOptions{
		ResourceQuery: &fakeResourceQuery{},
		MCPGateway: mcptransport.NewGateway(mcptransport.GatewayOptions{
			Registry: registry,
			Router:   mcptransport.NewToolRouter(mcptransport.ToolRouterOptions{Registry: registry, ExternalMCP: external}),
		}),
	})

	req := authedResourceRequest(http.MethodPost, "/api/resources/tools/call", `{"tool_id":"mcp.github.search_issues","arguments":{"query":"is:open"}}`)
	req.Header.Set("X-Permission-Scope", "resource.read,tool.call")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if external.req.DownstreamToolName != "" {
		t.Fatalf("unconfigured operations service must not execute tool: %+v", external.req)
	}
}

func TestHTTPRoutesResourceToolCallRejectsPermissionReviewWithoutHILDecision(t *testing.T) {
	registry := registerGatewayReviewMCP(t)
	ops := &fakeOperationsService{permissionDecision: operations.PermissionDecisionDTO{
		Decision: operations.PermissionDecisionNeedsHIL,
		Reason:   "risk policy requires human review",
	}}
	hilService := &fakeHILService{createResult: hil.CreateDecisionResult{
		Decision: hil.DecisionRequest{ID: "hil-tool-1", Route: hil.RouteDecision{Mode: hil.ModeHIL}},
	}}
	handler := newTestRouter(RouterOptions{
		ResourceQuery: &fakeResourceQuery{},
		Operations:    ops,
		HIL:           hilService,
		MCPGateway:    mcptransport.NewGateway(mcptransport.GatewayOptions{Registry: registry}),
	})

	req := authedResourceRequest(http.MethodPost, "/api/resources/tools/call", `{"tool_id":"mcp.github.search_issues","arguments":{"query":"is:open"}}`)
	req.Header.Set("X-Permission-Scope", "resource.read,tool.call")
	req.Header.Set("Idempotency-Key", "tool-hil-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if hilService.createCalls != 0 {
		t.Fatalf("CreateDecision calls = %d, want 0", hilService.createCalls)
	}
}

func TestHTTPRoutesResourceToolCallDoesNotBypassPolicyWhenHILDisabled(t *testing.T) {
	registry := registerGatewayReviewMCP(t)
	external := &recordingResourceExternalMCP{result: mcptransport.CallToolResult{Output: map[string]any{"ok": true}}}
	ops := &fakeOperationsService{permissionDecision: operations.PermissionDecisionDTO{
		Decision: operations.PermissionDecisionNeedsHIL,
		Reason:   "risk policy requires human review",
	}}
	hilService := &fakeHILService{}
	profiles := fakeHTTPGatewayProfileStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"agent-1": {ID: "agent-1", ToolRefs: []agentcore.ToolRef{{ToolID: "mcp.github"}}},
		},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{AgentProfileID: "agent-1", AgentInstanceID: "agent-1"}}},
	}
	settings := DefaultSettings()
	settings.HILDefaultEnabled = false
	handler := newTestRouter(RouterOptions{
		ResourceQuery: &fakeResourceQuery{}, Operations: ops, HIL: hilService,
		Settings: newMemorySettingsService(settings),
		MCPGateway: mcptransport.NewGateway(mcptransport.GatewayOptions{
			Registry: registry, Profiles: profiles,
			Router: mcptransport.NewToolRouter(mcptransport.ToolRouterOptions{Registry: registry, ExternalMCP: external}),
		}),
	})

	req := authedResourceRequest(http.MethodPost, "/api/resources/tools/call", `{"tool_id":"mcp.github.search_issues","arguments":{"query":"is:open"}}`)
	req.Header.Set("X-Permission-Scope", "resource.read,tool.call")
	req.Header.Set("X-Agent-Profile-ID", "agent-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if hilService.createCalls != 0 || external.req.DownstreamToolName != "" {
		t.Fatalf("HIL calls=%d downstream=%+v", hilService.createCalls, external.req)
	}
}

func TestHTTPRoutesResourceToolCallRejectsCouncilReviewWithoutHILDecision(t *testing.T) {
	registry := registerGatewayReviewMCP(t)
	ops := &fakeOperationsService{permissionDecision: operations.PermissionDecisionDTO{
		Decision: operations.PermissionDecisionNeedsCouncil,
		Reason:   "verified ontology conflict requires council",
	}}
	hilService := &fakeHILService{createResult: hil.CreateDecisionResult{
		Decision: hil.DecisionRequest{ID: "council-tool-1", Route: hil.RouteDecision{Mode: hil.ModeCouncil}, CouncilID: "council-1"},
		Council:  &hil.CouncilSession{ID: "council-1", RequestID: "council-tool-1", Status: hil.CouncilOpen},
	}}
	handler := newTestRouter(RouterOptions{
		ResourceQuery: &fakeResourceQuery{},
		Operations:    ops,
		HIL:           hilService,
		MCPGateway:    mcptransport.NewGateway(mcptransport.GatewayOptions{Registry: registry}),
	})

	req := authedResourceRequest(http.MethodPost, "/api/resources/tools/call", `{"tool_id":"mcp.github.delete_issue","arguments":{"id":"issue-1"}}`)
	req.Header.Set("X-Permission-Scope", "resource.read,tool.call")
	req.Header.Set("Idempotency-Key", "tool-council-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if hilService.createCalls != 0 {
		t.Fatalf("CreateDecision calls = %d, want 0", hilService.createCalls)
	}
}

func TestHTTPRoutesResourceToolCallFailsClosedBeforeHILForUnknownToolRisk(t *testing.T) {
	ops := &fakeOperationsService{permissionDecision: operations.PermissionDecisionDTO{
		Decision: operations.PermissionDecisionNeedsHIL,
		Reason:   "risk policy requires human review",
	}}
	hilService := &fakeHILService{createResult: hil.CreateDecisionResult{
		Decision: hil.DecisionRequest{ID: "should-not-be-created", Route: hil.RouteDecision{Mode: hil.ModeHIL}},
	}}
	external := &recordingResourceExternalMCP{}
	registry := toolres.NewMemoryRegistry()
	handler := newTestRouter(RouterOptions{
		ResourceQuery: &fakeResourceQuery{}, Operations: ops, HIL: hilService,
		MCPGateway: mcptransport.NewGateway(mcptransport.GatewayOptions{
			Registry: registry,
			Router:   mcptransport.NewToolRouter(mcptransport.ToolRouterOptions{Registry: registry, ExternalMCP: external}),
		}),
	})

	req := authedResourceRequest(http.MethodPost, "/api/resources/tools/call", `{"tool_id":"mcp.unknown.search","arguments":{"query":"is:open"}}`)
	req.Header.Set("X-Permission-Scope", "resource.read,tool.call")
	req.Header.Set("Idempotency-Key", "unknown-tool-risk")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if ops.permissionDecisionReq.ActionTypeID != "" {
		t.Fatalf("permission policy should not run without a server-owned risk descriptor: %+v", ops.permissionDecisionReq)
	}
	if hilService.createCalls != 0 {
		t.Fatalf("unknown tool must not create a HIL decision: %d", hilService.createCalls)
	}
	if external.req.DownstreamToolName != "" {
		t.Fatalf("unknown tool must not execute: %+v", external.req)
	}
}

func TestHTTPRoutesResourceToolCallDoesNotConsumeLegacyHILReceipt(t *testing.T) {
	registry := registerGatewayReviewMCP(t)
	external := &recordingResourceExternalMCP{
		result: mcptransport.CallToolResult{Output: map[string]any{"ok": true}},
	}
	ops := &fakeOperationsService{permissionDecision: operations.PermissionDecisionDTO{
		Decision: operations.PermissionDecisionNeedsHIL,
		Reason:   "risk policy requires human review",
	}}
	hilService := &gatewayHILReceiptStub{
		fakeHILService: &fakeHILService{createResult: hil.CreateDecisionResult{
			Decision: hil.DecisionRequest{ID: "hil-retry-1", Route: hil.RouteDecision{Mode: hil.ModeHIL}},
		}},
		consumeErrors: []error{hil.ErrNotFound, nil},
		receipt:       hil.DecisionReceipt{ID: "receipt-retry-1", Effect: hil.EffectAuthorize, SingleUse: true},
	}
	profiles := fakeHTTPGatewayProfileStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"agent-1": {
				ID: "agent-1",
				ToolRefs: []agentcore.ToolRef{{
					ToolID: "mcp.github.search_issues",
				}},
			},
		},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID:  "agent-1",
			AgentInstanceID: "agent-1",
		}}},
	}
	handler := newTestRouter(RouterOptions{
		ResourceQuery: &fakeResourceQuery{}, Operations: ops, HIL: hilService,
		MCPGateway: mcptransport.NewGateway(mcptransport.GatewayOptions{
			Registry: registry,
			Profiles: profiles,
			Router:   mcptransport.NewToolRouter(mcptransport.ToolRouterOptions{Registry: registry, ExternalMCP: external}),
		}),
	})

	makeRequest := func() *http.Request {
		req := authedResourceRequest(http.MethodPost, "/api/resources/tools/call", `{"tool_id":"mcp.github.search_issues","arguments":{"query":"is:open"}}`)
		req.Header.Set("X-Permission-Scope", "resource.read,tool.call")
		req.Header.Set("X-Agent-Profile-ID", "agent-1")
		req.Header.Set("Idempotency-Key", "gateway-retry-1")
		return req
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, makeRequest())
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if hilService.consumeCalls != 0 || hilService.createCalls != 0 || external.req.DownstreamToolName != "" {
		t.Fatalf("legacy HIL path ran: consumes=%d creates=%d external=%+v", hilService.consumeCalls, hilService.createCalls, external.req)
	}
}

func registerGatewayReviewMCP(t *testing.T) *toolres.MemoryRegistry {
	t.Helper()
	registry := toolres.NewMemoryRegistry()
	if err := registry.Register(context.Background(), toolres.ToolDefinition{
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
			ExposedTools: []string{"search_issues", "delete_issue"},
		},
	}); err != nil {
		t.Fatalf("register GitHub MCP: %v", err)
	}
	return registry
}

func allowGatewayOperations() *fakeOperationsService {
	return &fakeOperationsService{permissionDecision: operations.PermissionDecisionDTO{
		Decision: operations.PermissionDecisionAllow,
	}}
}

func TestHTTPRoutesResourceToolCallRequiresToolCallPermission(t *testing.T) {
	registry := toolres.NewMemoryRegistry()
	if err := registry.Register(context.Background(), toolres.ToolDefinition{
		ID:        agentcore.ToolID("mcp.public"),
		Kind:      toolres.KindMCPServer,
		Name:      "Public MCP",
		Version:   "1.0.0",
		Enabled:   true,
		RiskLevel: toolres.RiskMedium,
		MCP: &toolres.MCPServerDefinition{
			ServerName:   "public",
			Transport:    toolres.TransportStdio,
			Command:      "public-mcp-server",
			ExposedTools: []string{"ping"},
		},
	}); err != nil {
		t.Fatalf("register mcp: %v", err)
	}
	external := &recordingResourceExternalMCP{}
	handler := newTestRouter(RouterOptions{
		ResourceQuery: &fakeResourceQuery{},
		Operations:    allowGatewayOperations(),
		MCPGateway: mcptransport.NewGateway(mcptransport.GatewayOptions{
			Registry: registry,
			Router: mcptransport.NewToolRouter(mcptransport.ToolRouterOptions{
				Registry:    registry,
				ExternalMCP: external,
			}),
		}),
	})

	req := authedResourceRequest(http.MethodPost, "/api/resources/tools/call", `{"tool_id":"mcp.public.ping"}`)
	req.Header.Set("X-User-ID", "creator-1")
	req.Header.Set("X-Permission-Scope", "resource.read")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if external.req.DownstreamToolName != "" {
		t.Fatalf("tool call should not execute without tool.call, got %+v", external.req)
	}
}

func TestHTTPRoutesResourceToolCallDoesNotFallbackToUsernameAsRawProfile(t *testing.T) {
	registry := toolres.NewMemoryRegistry()
	if err := registry.Register(context.Background(), toolres.ToolDefinition{
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
		},
	}); err != nil {
		t.Fatalf("register mcp: %v", err)
	}
	external := &recordingResourceExternalMCP{}
	profiles := fakeHTTPGatewayProfileStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"superadmin": {
				ID: "superadmin",
				ToolRefs: []agentcore.ToolRef{{
					ToolID:          "mcp.github",
					PermissionScope: "github.read",
				}},
			},
		},
	}
	handler := newTestRouter(RouterOptions{
		ResourceQuery: &fakeResourceQuery{},
		Operations:    allowGatewayOperations(),
		MCPGateway: mcptransport.NewGateway(mcptransport.GatewayOptions{
			Registry: registry,
			Profiles: profiles,
			Router: mcptransport.NewToolRouter(mcptransport.ToolRouterOptions{
				Registry:    registry,
				ExternalMCP: external,
			}),
		}),
	})

	req := authedResourceRequest(http.MethodPost, "/api/resources/tools/call", `{"tool_id":"mcp.github.search_issues"}`)
	req.Header.Set("X-User-ID", "superadmin")
	req.Header.Set("X-Permission-Scope", "resource.read,tool.call")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if external.req.DownstreamToolName != "" {
		t.Fatalf("username fallback should not execute tool: %+v", external.req)
	}
}

func TestHTTPRoutesResourceToolCallUsesViewerProfileBindingAndMCPAuthorization(t *testing.T) {
	registry := toolres.NewMemoryRegistry()
	if err := registry.Register(context.Background(), toolres.ToolDefinition{
		ID:        agentcore.ToolID("mcp.private"),
		Kind:      toolres.KindMCPServer,
		Name:      "Private MCP",
		Version:   "1.0.0",
		Enabled:   true,
		RiskLevel: toolres.RiskMedium,
		MCP: &toolres.MCPServerDefinition{
			ServerName:   "private",
			Transport:    toolres.TransportStreamHTTP,
			URL:          "http://127.0.0.1:5000/mcp",
			ExposedTools: []string{"search", "delete"},
			Auth: toolres.MCPAuthPolicy{
				Required:   true,
				Mode:       toolres.MCPAuthModePerAgent,
				HeaderName: "Authorization",
			},
		},
	}); err != nil {
		t.Fatalf("register mcp: %v", err)
	}
	profiles := fakeHTTPGatewayProfileStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"creator-profile": {
				ID: "creator-profile",
				ToolRefs: []agentcore.ToolRef{{
					ToolID:          "mcp.private.search",
					PermissionScope: "private.search",
				}},
				MCPAuthorizations: []agentcore.MCPAuthorization{{
					ServerToolID:    "mcp.private",
					HeaderName:      "Authorization",
					CredentialValue: "Bearer creator-token",
				}},
			},
		},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID:  "creator-profile",
			AgentInstanceID: "creator-runtime",
		}}},
	}
	external := &recordingResourceExternalMCP{
		result: mcptransport.CallToolResult{Output: map[string]any{"ok": true}},
	}
	handler := newTestRouter(RouterOptions{
		ResourceQuery: &fakeResourceQuery{},
		Operations:    allowGatewayOperations(),
		MCPGateway: mcptransport.NewGateway(mcptransport.GatewayOptions{
			Registry: registry,
			Profiles: profiles,
			Router: mcptransport.NewToolRouter(mcptransport.ToolRouterOptions{
				Registry:    registry,
				ExternalMCP: external,
			}),
		}),
	})

	req := authedResourceRequest(http.MethodPost, "/api/resources/tools/call", `{"tool_id":"mcp.private.search","arguments":{"query":"draft"}}`)
	req.Header.Set("X-User-ID", "creator-user")
	req.Header.Set("X-Agent-Profile-ID", "creator-profile")
	req.Header.Set("X-Permission-Scope", "resource.read,tool.call")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if external.req.PrincipalID != "creator-user" {
		t.Fatalf("principal id = %q", external.req.PrincipalID)
	}
	if external.req.Server.MCP == nil || external.req.Server.MCP.Headers["Authorization"] != "Bearer creator-token" {
		t.Fatalf("mcp authorization headers = %+v", external.req.Server.MCP)
	}

	req = authedResourceRequest(http.MethodPost, "/api/resources/tools/call", `{"tool_id":"mcp.private.delete","arguments":{"id":"issue-1"}}`)
	req.Header.Set("X-User-ID", "creator-user")
	req.Header.Set("X-Agent-Profile-ID", "creator-profile")
	req.Header.Set("X-Permission-Scope", "resource.read,tool.call")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if external.req.DownstreamToolName != "search" {
		t.Fatalf("unbound delete call should not reach external mcp: %+v", external.req)
	}
}

func TestHTTPRoutesResourceQueryDefaultsToFirstListPage(t *testing.T) {
	query := &fakeResourceQuery{}
	handler := newTestRouter(RouterOptions{ResourceQuery: query})

	req := authedResourceRequest(http.MethodGet, "/api/resources/memory-profiles", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if query.req.Limit != 20 || query.req.Offset != 0 {
		t.Fatalf("default page = %+v", query.req)
	}
}

func TestHTTPRoutesRejectResourceListAgentIDFilter(t *testing.T) {
	query := &fakeResourceQuery{}
	handler := newTestRouter(RouterOptions{ResourceQuery: query})

	req := authedResourceRequest(http.MethodGet, "/api/resources/tools?agent_id=profile-ops", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if query.listCalled {
		t.Fatalf("deprecated agent_id list filter should be rejected before resource query")
	}
	if body := rec.Body.String(); !strings.Contains(body, `"field":"agent_id"`) {
		t.Fatalf("body = %s", body)
	}
}

func TestHTTPRoutesResourceQueryDiscoversMCPServers(t *testing.T) {
	query := &fakeResourceQuery{discovered: resourceadmin.DiscoverMCPResponseDTO{
		Servers: []resourceadmin.DiscoveredMCPServerDTO{{
			DiscoveredID: "linear",
			Name:         "Linear MCP",
			PackageRefs:  []string{"npm:@linear/mcp"},
			Tools:        []string{"issue.search"},
		}},
	}}
	handler := newTestRouter(RouterOptions{ResourceQuery: query})

	req := authedResourceRequest(http.MethodGet, "/api/resources/mcp-servers/discover?query=linear&limit=5", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !query.discoverCalled || query.discoverReq.Query != "linear" || query.discoverReq.Limit != 5 {
		t.Fatalf("discover request = %+v called=%v", query.discoverReq, query.discoverCalled)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"discovered_id":"linear"`) || !strings.Contains(body, `"Linear MCP"`) {
		t.Fatalf("body = %s", body)
	}
}

func TestHTTPRoutesGetsMCPServerByID(t *testing.T) {
	query := &fakeResourceQuery{mcpServer: resourceadmin.MCPServerDTO{
		ToolDTO: resourceadmin.ToolDTO{
			ToolID:  "mcp.github",
			Kind:    "mcp",
			Name:    "GitHub MCP",
			Enabled: true,
		},
		ServerName:   "github",
		Transport:    "streamable_http",
		URL:          "https://mcp.example.test/mcp",
		ExposedTools: []string{"search_issues"},
		ExposedToolDetails: []resourceadmin.MCPExposedToolDTO{{
			Name:        "search_issues",
			Description: "Search issues in GitHub.",
			InputSchema: map[string]any{
				"type": "object",
			},
		}},
	}}
	handler := newTestRouter(RouterOptions{ResourceQuery: query})

	req := authedResourceRequest(http.MethodGet, "/api/resources/mcp-servers/mcp.github", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !query.getMCPServerCalled || query.mcpToolID != "mcp.github" {
		t.Fatalf("mcp tool id = %s called=%v", query.mcpToolID, query.getMCPServerCalled)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"exposed_tool_details"`) || !strings.Contains(body, `"search_issues"`) {
		t.Fatalf("body = %s", body)
	}
}

func TestHTTPRoutesGetsMemoryProfileByID(t *testing.T) {
	query := &fakeResourceQuery{memoryProfile: resourceadmin.MemoryProfileDTO{
		MemoryProfileID: "memory-pm-agent",
		OwnerAgentID:    "pm-agent",
		MemoryKind:      "personal",
	}}
	handler := newTestRouter(RouterOptions{ResourceQuery: query})

	req := authedResourceRequest(http.MethodGet, "/api/resources/memory-profiles/memory-pm-agent", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !query.getMemoryProfileCalled || query.profileID != "memory-pm-agent" {
		t.Fatalf("profile id = %s called=%v", query.profileID, query.getMemoryProfileCalled)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"memory_profile_id":"memory-pm-agent"`) {
		t.Fatalf("body = %s", body)
	}
}

func TestHTTPRoutesUpdatesMemoryProfileSettings(t *testing.T) {
	query := &fakeResourceQuery{memoryProfile: resourceadmin.MemoryProfileDTO{
		MemoryProfileID: "memory-pm-agent",
		MemoryKind:      "personal",
		Enabled:         false,
	}}
	handler := newTestRouter(RouterOptions{ResourceQuery: query})

	body := `{"enabled":false,"max_records":25,"max_summary_bytes":4096,"retrieval_tags":["policy","decision"]}`
	req := authedResourceRequest(http.MethodPatch, "/api/resources/memory-profiles/memory-pm-agent", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !query.updateMemoryProfileCalled || query.profileID != "memory-pm-agent" {
		t.Fatalf("profile id = %s called=%v", query.profileID, query.updateMemoryProfileCalled)
	}
	if query.updateMemoryProfileReq.MaxRecords != 25 || query.updateMemoryProfileReq.MaxSummaryBytes != 4096 || len(query.updateMemoryProfileReq.RetrievalTags) != 2 {
		t.Fatalf("update request = %+v", query.updateMemoryProfileReq)
	}
}

func TestHTTPRoutesGetsPersonalMemoryProfileByAgent(t *testing.T) {
	query := &fakeResourceQuery{personalMemoryProfile: resourceadmin.MemoryProfileDTO{
		MemoryProfileID: "memory-pm-agent",
		OwnerAgentID:    "pm-agent",
		MemoryKind:      "personal",
	}}
	handler := newTestRouter(RouterOptions{ResourceQuery: query})

	req := authedResourceRequest(http.MethodGet, "/api/resources/memory-profiles/personal?agent_id=pm-agent", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !query.getPersonalMemoryProfileCalled || query.agentID != "pm-agent" {
		t.Fatalf("agent id = %s called=%v", query.agentID, query.getPersonalMemoryProfileCalled)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"owner_agent_id":"pm-agent"`) {
		t.Fatalf("body = %s", body)
	}
}

func TestHTTPRoutesBuildsMemoryDigest(t *testing.T) {
	query := &fakeResourceQuery{digest: resourceadmin.MemoryDigestDTO{
		DigestID:         "digest.123",
		AgentID:          "pm-agent",
		MissionID:        "mission-1",
		SourceMemoryRefs: []string{"memory-record-1"},
		Summary:          "PM memory digest",
	}}
	handler := newTestRouter(RouterOptions{ResourceQuery: query})

	body := `{"agent_id":"pm-agent","mission_id":"mission-1","profile_refs":[{"memory_profile_id":"memory-pm-agent","memory_kind":"personal"}]}`
	req := authedResourceRequest(http.MethodPost, "/api/resources/memory-profiles/digest", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !query.buildDigestCalled || query.digestReq.AgentID != "pm-agent" || query.digestReq.ProfileRefs[0].MemoryProfileID != "memory-pm-agent" {
		t.Fatalf("digest request = %+v called=%v", query.digestReq, query.buildDigestCalled)
	}
	var got resourceadmin.MemoryDigestDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.DigestID != "digest.123" || got.Summary != "PM memory digest" {
		t.Fatalf("digest = %+v", got)
	}
}

type fakeResourceQuery struct {
	req                            resourceadmin.ListResourcesRequest
	listCalled                     bool
	discoverReq                    resourceadmin.DiscoverMCPRequest
	discovered                     resourceadmin.DiscoverMCPResponseDTO
	discoverErr                    error
	discoverCalled                 bool
	skillID                        string
	skillDetail                    resourceadmin.SkillDetailDTO
	getSkillDetailCalled           bool
	mcpToolID                      string
	mcpServer                      resourceadmin.MCPServerDTO
	getMCPServerCalled             bool
	profileID                      string
	memoryProfile                  resourceadmin.MemoryProfileDTO
	getMemoryProfileCalled         bool
	updateMemoryProfileReq         resourceadmin.UpdateMemoryProfileRequest
	updateMemoryProfileCalled      bool
	agentID                        string
	personalMemoryProfile          resourceadmin.MemoryProfileDTO
	getPersonalMemoryProfileCalled bool
	digestReq                      resourceadmin.BuildMemoryDigestRequest
	digest                         resourceadmin.MemoryDigestDTO
	buildDigestCalled              bool
	skillBundles                   resourceadmin.SkillBundleListDTO
	listSkillBundlesCalled         bool
}

func (f *fakeResourceQuery) ListResourcesFiltered(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.ListResourcesRequest) (resourceadmin.ResourceAdminViewDTO, error) {
	f.listCalled = true
	f.req = req
	return resourceadmin.ResourceAdminViewDTO{}, nil
}

func (f *fakeResourceQuery) DiscoverMCPServers(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.DiscoverMCPRequest) (resourceadmin.DiscoverMCPResponseDTO, error) {
	f.discoverCalled = true
	f.discoverReq = req
	if f.discoverErr != nil {
		return resourceadmin.DiscoverMCPResponseDTO{}, f.discoverErr
	}
	return f.discovered, nil
}

func (f *fakeResourceQuery) GetSkillDetail(ctx context.Context, viewer resourceadmin.ViewerRef, skillID string) (resourceadmin.SkillDetailDTO, error) {
	f.getSkillDetailCalled = true
	f.skillID = skillID
	return f.skillDetail, nil
}

func (f *fakeResourceQuery) ListSkillBundles(ctx context.Context, viewer resourceadmin.ViewerRef) (resourceadmin.SkillBundleListDTO, error) {
	f.listSkillBundlesCalled = true
	return f.skillBundles, nil
}

func (f *fakeResourceQuery) GetMCPServer(ctx context.Context, viewer resourceadmin.ViewerRef, toolID string) (resourceadmin.MCPServerDTO, error) {
	f.getMCPServerCalled = true
	f.mcpToolID = toolID
	return f.mcpServer, nil
}

func (f *fakeResourceQuery) GetMemoryProfile(ctx context.Context, viewer resourceadmin.ViewerRef, profileID string) (resourceadmin.MemoryProfileDTO, error) {
	f.getMemoryProfileCalled = true
	f.profileID = profileID
	return f.memoryProfile, nil
}

func (f *fakeResourceQuery) UpdateMemoryProfile(ctx context.Context, viewer resourceadmin.ViewerRef, profileID string, req resourceadmin.UpdateMemoryProfileRequest) (resourceadmin.MemoryProfileDTO, error) {
	f.updateMemoryProfileCalled = true
	f.profileID = profileID
	f.updateMemoryProfileReq = req
	return f.memoryProfile, nil
}

func (f *fakeResourceQuery) GetPersonalMemoryProfileByAgent(ctx context.Context, viewer resourceadmin.ViewerRef, agentID string) (resourceadmin.MemoryProfileDTO, error) {
	f.getPersonalMemoryProfileCalled = true
	f.agentID = agentID
	return f.personalMemoryProfile, nil
}

func (f *fakeResourceQuery) BuildMemoryDigest(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.BuildMemoryDigestRequest) (resourceadmin.MemoryDigestDTO, error) {
	f.buildDigestCalled = true
	f.digestReq = req
	return f.digest, nil
}

func hasToolDTO(tools []resourceadmin.ToolDTO, toolID, serverID, downstream string, manageable bool) bool {
	for _, tool := range tools {
		if tool.ToolID == toolID &&
			tool.ServerToolID == serverID &&
			tool.DownstreamToolName == downstream &&
			tool.Manageable == manageable {
			return true
		}
	}
	return false
}

type recordingResourceExternalMCP struct {
	req    mcptransport.ExternalMCPCallRequest
	result mcptransport.CallToolResult
}

func (r *recordingResourceExternalMCP) CallTool(ctx context.Context, req mcptransport.ExternalMCPCallRequest) (mcptransport.CallToolResult, error) {
	r.req = req
	return r.result, nil
}

type gatewayHILReceiptStub struct {
	*fakeHILService
	consumeErrors []error
	receipt       hil.DecisionReceipt
	consumeCalls  int
	lastInput     hil.GatewayReceiptConsumeInput
}

func (s *gatewayHILReceiptStub) ConsumeGatewayDecision(_ context.Context, input hil.GatewayReceiptConsumeInput) (hil.DecisionReceipt, error) {
	s.consumeCalls++
	s.lastInput = input
	index := s.consumeCalls - 1
	if index < len(s.consumeErrors) && s.consumeErrors[index] != nil {
		return hil.DecisionReceipt{}, s.consumeErrors[index]
	}
	return s.receipt, nil
}

type fixedToolResultPolicy struct {
	req      contextgraph.BuildContextBundleRequest
	objects  []contextgraph.Object
	decision contextgraph.PermissionDecision
}

func (p *fixedToolResultPolicy) DecideContext(ctx context.Context, req contextgraph.BuildContextBundleRequest, objects []contextgraph.Object) (contextgraph.PermissionDecision, error) {
	p.req = req
	p.objects = append([]contextgraph.Object(nil), objects...)
	return p.decision, nil
}

type fakeHTTPGatewayProfileStore struct {
	profiles map[agentcore.AgentProfileID]agentcore.AgentProfile
	page     agentcore.AgentDirectoryPage
}

func (s fakeHTTPGatewayProfileStore) GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	profile, ok := s.profiles[id]
	if !ok {
		return agentcore.AgentProfile{}, mcptransport.ErrToolNotFound
	}
	return profile, nil
}

func (s fakeHTTPGatewayProfileStore) QueryDirectory(ctx context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	return s.page, nil
}
