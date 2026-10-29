package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/contextgraph"
	contextconsumer "orrery_v2/internal/app/contextgraph/consumer"
	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
	mcptransport "orrery_v2/internal/transport/mcp"
)

func TestMCPInitializedNotificationIsAcceptedWithoutJSONRPCResponse(t *testing.T) {
	router := newTestRouter(RouterOptions{MCPGateway: mcptransport.NewGateway(mcptransport.GatewayOptions{Registry: toolres.NewMemoryRegistry()})})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	req.RemoteAddr = "127.0.0.1:53310"
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted || rec.Body.Len() != 0 {
		t.Fatalf("notification response = %d %q; strict MCP clients require 202 with no body", rec.Code, rec.Body.String())
	}
}

func TestMCPRouteListsToolsThroughGatewayWithBM25Metadata(t *testing.T) {
	ctx := context.Background()
	registry := toolres.NewMemoryRegistry()
	for _, item := range []struct {
		id          string
		name        string
		description string
	}{
		{"tool.github.search", "GitHub issue search", "Search GitHub issues and pull requests"},
		{"tool.github.create", "GitHub issue create", "Create a GitHub issue with labels"},
		{"tool.linear.search", "Linear issue search", "Search Linear issues by project"},
		{"tool.docs.search", "Docs search", "Find internal documentation"},
		{"tool.calendar.read", "Calendar read", "Read calendar events"},
		{"tool.email.send", "Email send", "Send email"},
		{"tool.browser.open", "Browser open", "Open web pages"},
		{"tool.file.read", "File read", "Read workspace files"},
		{"tool.file.write", "File write", "Write workspace files"},
		{"tool.scheduler.list", "Scheduler list", "List scheduled tasks"},
		{"tool.scheduler.run", "Scheduler run", "Run scheduled tasks"},
	} {
		registerMCPHTTPTestTool(t, ctx, registry, item.id, item.name, item.description)
	}
	router := newTestRouter(RouterOptions{MCPGateway: mcptransport.NewGateway(mcptransport.GatewayOptions{Registry: registry})})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"mode":"filter","query":"github issue","top_k":2}}`))
	req.RemoteAddr = "127.0.0.1:53310"
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /mcp status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Result struct {
			Tools          []struct{ Name string } `json:"tools"`
			TotalAvailable int                     `json:"total_available"`
			Filtered       bool                    `json:"filtered"`
			Truncated      bool                    `json:"truncated"`
			TopK           int                     `json:"top_k"`
			Notice         string                  `json:"notice"`
		} `json:"result"`
		Error any `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode /mcp response: %v", err)
	}
	if body.Error != nil {
		t.Fatalf("unexpected mcp error: %#v", body.Error)
	}
	if !body.Result.Filtered || !body.Result.Truncated || body.Result.TotalAvailable != 11 || body.Result.TopK != 2 || len(body.Result.Tools) != 2 {
		t.Fatalf("tools/list metadata = %+v", body.Result)
	}
	if !strings.Contains(body.Result.Notice, "not all") || !strings.Contains(body.Result.Notice, "mode=all") {
		t.Fatalf("tools/list notice = %q", body.Result.Notice)
	}
	for _, tool := range body.Result.Tools {
		if !strings.Contains(tool.Name, "github") {
			t.Fatalf("expected github tools, got %+v", body.Result.Tools)
		}
	}
}

func TestMCPRouteRequiresPost(t *testing.T) {
	router := newTestRouter(RouterOptions{MCPGateway: mcptransport.NewGateway(mcptransport.GatewayOptions{Registry: toolres.NewMemoryRegistry()})})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.RemoteAddr = "127.0.0.1:53310"
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /mcp status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMCPRouteEnterpriseGraphReturnsSingleStructuredPayload(t *testing.T) {
	ctx := context.Background()
	registry := toolres.NewMemoryRegistry()
	definition := toolstd.Definitions(time.Unix(0, 0).UTC())
	for _, item := range definition {
		if item.ID == toolstd.EnterpriseGraphBuildContextBundle {
			if err := registry.Register(ctx, item); err != nil {
				t.Fatal(err)
			}
		}
	}
	objects := make([]contextgraph.Object, 100)
	for index := range objects {
		objects[index] = contextgraph.Object{ID: "object", Properties: map[string]any{"large": strings.Repeat("x", 1000)}}
	}
	executor := fixedHTTPEnterpriseGraphExecutor{bundle: contextgraph.ContextBundle{
		BundleID: "bundle:tenant:hash", TenantID: "tenant-1", Consumer: "agent-runtime", Purpose: "read",
		ActorRef: contextgraph.Ref{Kind: "agent", ID: "agent-1"}, Schema: contextgraph.SchemaDictionary{VersionHash: "schema-hash"}, Objects: objects,
	}}
	toolRouter := mcptransport.NewToolRouter(mcptransport.ToolRouterOptions{Registry: registry, EnterpriseGraphExecutor: executor})
	gateway := mcptransport.NewGateway(mcptransport.GatewayOptions{Registry: registry, Router: toolRouter})
	router := newTestRouter(RouterOptions{MCPGateway: gateway})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{
		"jsonrpc":"2.0","id":1,"method":"tools/call","params":{
			"name":"enterprise_graph.build_context_bundle","principal_id":"agent-1",
			"permissions":["enterprise_graph.read"],"arguments":{
				"tenant_id":"tenant-1","consumer":"agent-runtime","purpose":"read",
				"actor_ref":{"kind":"agent","id":"agent-1"},
				"subject_refs":[{"kind":"mission","id":"mission-1"}],"token_budget":8000
			}
		}
	}`))
	request.RemoteAddr = "127.0.0.1:53310"
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Result struct {
			Content           []struct{ Type, Text string } `json:"content"`
			StructuredContent map[string]any                `json:"structuredContent"`
		} `json:"result"`
		Error any `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error != nil || len(response.Result.Content) != 1 || response.Result.Content[0].Type != "text" || len(response.Result.Content[0].Text) > 512 || strings.Contains(response.Result.Content[0].Text, `"objects"`) || response.Result.StructuredContent["bundle_id"] != "bundle:tenant:hash" {
		t.Fatalf("response = %+v", response)
	}
}

type fixedHTTPEnterpriseGraphExecutor struct {
	bundle contextgraph.ContextBundle
}

func (e fixedHTTPEnterpriseGraphExecutor) BuildContextBundle(context.Context, contextconsumer.BuildContextBundleInput) (contextgraph.ContextBundle, error) {
	return e.bundle, nil
}

func (fixedHTTPEnterpriseGraphExecutor) SearchObjects(context.Context, contextconsumer.SearchObjectsInput) (contextconsumer.SearchObjectsResult, error) {
	return contextconsumer.SearchObjectsResult{}, nil
}

func TestMCPRouteRejectsNonLocalRequestWithoutRuntimeToken(t *testing.T) {
	ctx := context.Background()
	registry := toolres.NewMemoryRegistry()
	registerMCPHTTPTestTool(t, ctx, registry, "tool.github.search", "GitHub issue search", "Search GitHub issues")
	router := newTestRouter(RouterOptions{MCPGateway: mcptransport.NewGateway(mcptransport.GatewayOptions{Registry: registry})})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"mode":"all"}}`))
	req.RemoteAddr = "203.0.113.10:53310"
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST /mcp status = %d body=%s, want unauthorized", rec.Code, rec.Body.String())
	}
}

func TestMCPRouteRequiresRuntimeTokenForProductionLoopbackRequest(t *testing.T) {
	t.Setenv("ORRERY_RUNTIME_MCP_AUTHORIZATION", "")
	t.Setenv("ORRERY_RUNTIME_MCP_TOKEN", "")
	ctx := context.Background()
	registry := toolres.NewMemoryRegistry()
	registerMCPHTTPTestTool(t, ctx, registry, "tool.github.search", "GitHub issue search", "Search GitHub issues")
	router := newTestRouter(RouterOptions{Environment: "production", MCPGateway: mcptransport.NewGateway(mcptransport.GatewayOptions{Registry: registry})})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"mode":"all"}}`))
	req.RemoteAddr = "127.0.0.1:53310"
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST /mcp status = %d body=%s, want unauthorized", rec.Code, rec.Body.String())
	}
}

func TestMCPRuntimeIdentityPrefersServerBoundHeaders(t *testing.T) {
	headers := http.Header{}
	headers.Set(agentcore.RuntimePrincipalIDHeader, "user-bound")
	headers.Set("X-Agent-ID", "agent-bound")
	headers.Set("X-Agent-Profile-ID", "profile-bound")

	if got := mcpRuntimePrincipalID(headers, "user-forged"); got != "user-bound" {
		t.Fatalf("principal = %q, want server-bound user", got)
	}
	if got := mcpBoundIdentity(headers, "X-Agent-ID", "agent-forged"); got != "agent-bound" {
		t.Fatalf("agent = %q, want server-bound agent", got)
	}
	if got := mcpBoundIdentity(headers, "X-Agent-Profile-ID", "profile-forged"); got != "profile-bound" {
		t.Fatalf("profile = %q, want server-bound profile", got)
	}
}

func TestMCPRuntimeIdentityUsesLegacyRequestOnlyWithoutBoundHeaders(t *testing.T) {
	headers := http.Header{}
	if got := mcpRuntimePrincipalID(headers, "legacy-user"); got != "legacy-user" {
		t.Fatalf("principal = %q, want legacy request fallback", got)
	}
	if got := mcpBoundIdentity(headers, "X-Agent-ID", "legacy-agent"); got != "legacy-agent" {
		t.Fatalf("agent = %q, want legacy request fallback", got)
	}
}

func TestMCPRuntimePermissionsUseServerBoundUserScopes(t *testing.T) {
	headers := http.Header{}
	headers.Set(agentcore.RuntimePrincipalKindHeader, string(agentcore.ExecutionPrincipalUser))
	headers.Set(agentcore.RuntimePermissionScopeHeader, "mission.read, discussion.write")

	permissions, authoritative := mcpRuntimePermissions(headers, []string{"operations.manage"})
	if !authoritative || strings.Join(permissions, ",") != "mission.read,discussion.write" {
		t.Fatalf("permissions = %v authoritative=%v", permissions, authoritative)
	}
}

func TestMCPRuntimePermissionsFailClosedForUserWithoutScopes(t *testing.T) {
	headers := http.Header{}
	headers.Set(agentcore.RuntimePrincipalKindHeader, string(agentcore.ExecutionPrincipalUser))

	permissions, authoritative := mcpRuntimePermissions(headers, []string{"operations.manage"})
	if !authoritative || len(permissions) != 0 {
		t.Fatalf("permissions = %v authoritative=%v", permissions, authoritative)
	}
}

func TestMCPResponsibilityContextFromHeadersParsesExecutionAuthority(t *testing.T) {
	want := &agentcore.ResponsibilityContext{
		ContextID: "context-1", ResponsibilityID: "responsibility-1", ResponsibilityVersion: 2,
		PolicyID: "policy-1", PolicyVersion: 4, AgentProfileID: "profile-1",
		AllowedTools: []string{"repo.read"}, AllowedDataScopes: []string{"mission"},
		PolicyToolScopes: []string{"repo.*"}, PolicyDataScopes: []string{"mission"}, TaskPermissionScopes: []string{"repo.read"},
		RequireExplicitToolScopes: true,
	}
	headers := http.Header{}
	for name, value := range agentcore.ResponsibilityMCPHeaders(want) {
		headers.Set(name, value)
	}

	got, err := mcpResponsibilityContextFromHeaders(headers)
	if err != nil {
		t.Fatalf("mcpResponsibilityContextFromHeaders: %v", err)
	}
	if got == nil || got.ContextID != want.ContextID || got.ResponsibilityID != want.ResponsibilityID || got.ResponsibilityVersion != want.ResponsibilityVersion ||
		got.PolicyID != want.PolicyID || got.PolicyVersion != want.PolicyVersion || got.AgentProfileID != want.AgentProfileID ||
		len(got.AllowedTools) != 1 || got.AllowedTools[0] != "repo.read" || len(got.TaskPermissionScopes) != 1 || got.TaskPermissionScopes[0] != "repo.read" || !got.RequireExplicitToolScopes {
		t.Fatalf("parsed responsibility = %+v", got)
	}
}

func TestMCPResponsibilityContextFromHeadersRejectsPartialIdentity(t *testing.T) {
	headers := http.Header{}
	headers.Set(agentcore.RuntimeResponsibilityContextIDHeader, "context-1")
	headers.Set(agentcore.RuntimeResponsibilityIDHeader, "responsibility-1")
	headers.Set(agentcore.RuntimeResponsibilityVersionHeader, "2")
	if _, err := mcpResponsibilityContextFromHeaders(headers); err == nil {
		t.Fatal("mcpResponsibilityContextFromHeaders accepted responsibility headers without policy authority")
	}
}

func registerMCPHTTPTestTool(t *testing.T, ctx context.Context, registry toolres.Registry, id, name, description string) {
	t.Helper()
	if err := registry.Register(ctx, toolres.ToolDefinition{
		ID:          agentcore.ToolID(id),
		Kind:        toolres.KindInternalServiceTool,
		Name:        name,
		Description: description,
		Version:     "1.0.0",
		Enabled:     true,
		RiskLevel:   toolres.RiskLow,
		CreatedAt:   time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("register %s: %v", id, err)
	}
}
