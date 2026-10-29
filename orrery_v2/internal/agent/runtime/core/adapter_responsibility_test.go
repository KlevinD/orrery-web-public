package core

import (
	"encoding/json"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestMCPConfigForRequestBindsResponsibilityAuthorityToOrreryServer(t *testing.T) {
	req := responsibilityRuntimeRequest()
	req.ExecutionPrincipal = agentcore.ExecutionPrincipal{Kind: agentcore.ExecutionPrincipalUser, ID: "user-1", Source: "system_assistant", PermissionScope: []string{"mission.read"}}
	opts := ExecOptions{
		MCPConfig:   json.RawMessage(`{"servers":{"orrery":{"url":"http://127.0.0.1:8080/mcp","headers":{"Authorization":"Bearer runtime","X-Orrery-Responsibility":"forged"}}}}`),
		Environment: map[string]string{"ORRERY_RUNTIME_MCP_URL": "http://127.0.0.1:8080/mcp"},
	}

	raw, err := mcpConfigForRequest(req, opts, agentcore.RuntimeKindCodex)
	if err != nil {
		t.Fatalf("mcpConfigForRequest: %v", err)
	}
	var config struct {
		Servers map[string]struct {
			Headers map[string]string `json:"headers"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	headers := config.Servers["orrery"].Headers
	if headers["Authorization"] != "Bearer runtime" || headers[agentcore.RuntimeRunIDHeader] != "run-1" ||
		headers[agentcore.RuntimeResponsibilityIDHeader] != "responsibility-1" || headers[agentcore.RuntimeResponsibilityPolicyVersionHeader] != "4" ||
		headers[agentcore.RuntimePrincipalIDHeader] != "user-1" || headers[agentcore.RuntimePrincipalKindHeader] != "user" ||
		headers[agentcore.RuntimePermissionScopeHeader] != "mission.read" || headers[agentcore.RuntimeExecutionSourceHeader] != "system_assistant" || headers["X-Agent-ID"] != "agent-1" {
		t.Fatalf("bound headers = %+v", headers)
	}
}

func TestMCPConfigForRequestBindsUserAuthorityOnlyToTrustedOrreryServer(t *testing.T) {
	req := RuntimeRequest{
		TenantID: "tenant-1", RunID: "run-1", AgentID: "agent-1",
		ExecutionPrincipal: agentcore.ExecutionPrincipal{Kind: agentcore.ExecutionPrincipalUser, ID: "user-1", Source: "system_assistant", PermissionScope: []string{"mission.read"}},
	}
	opts := ExecOptions{
		Environment: map[string]string{"ORRERY_RUNTIME_MCP_URL": "http://127.0.0.1:8080/mcp"},
		MCPConfig:   json.RawMessage(`{"servers":{"orrery":{"url":"http://127.0.0.1:8080/mcp/","headers":{"Authorization":"Bearer runtime","x-orrery-principal-id":"forged","X-Orrery-Permission-Scope":"admin","X-Orrery-Responsibility":"forged"},"http_headers":{"x-agent-id":"forged"}},"external":{"url":"https://external.example.test/mcp","headers":{"Authorization":"Bearer external"}}}}`),
	}

	raw, err := mcpConfigForRequest(req, opts, agentcore.RuntimeKindCodex)
	if err != nil {
		t.Fatalf("mcpConfigForRequest: %v", err)
	}
	var config struct {
		Servers map[string]struct {
			Headers     map[string]string `json:"headers"`
			HTTPHeaders map[string]string `json:"http_headers"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	internal := config.Servers["orrery"]
	if internal.Headers["Authorization"] != "Bearer runtime" || internal.Headers[agentcore.RuntimePrincipalIDHeader] != "user-1" ||
		internal.Headers[agentcore.RuntimePrincipalKindHeader] != "user" || internal.Headers[agentcore.RuntimePermissionScopeHeader] != "mission.read" ||
		internal.Headers[agentcore.RuntimeResponsibilityIDHeader] != "" || internal.HTTPHeaders["x-agent-id"] != "" {
		t.Fatalf("trusted headers = %+v, http_headers = %+v", internal.Headers, internal.HTTPHeaders)
	}
	external := config.Servers["external"]
	if external.Headers["Authorization"] != "Bearer external" || external.Headers[agentcore.RuntimePrincipalIDHeader] != "" || external.Headers[agentcore.RuntimePermissionScopeHeader] != "" {
		t.Fatalf("external headers = %+v", external.Headers)
	}
}

func TestMCPConfigForRequestRemovesForgedUserScopeWhenUserHasNone(t *testing.T) {
	req := RuntimeRequest{
		TenantID: "tenant-1", RunID: "run-1", AgentID: "agent-1",
		ExecutionPrincipal: agentcore.ExecutionPrincipal{Kind: agentcore.ExecutionPrincipalUser, ID: "user-1", Source: "system_assistant"},
	}
	opts := ExecOptions{
		Environment: map[string]string{"ORRERY_RUNTIME_MCP_URL": "http://127.0.0.1:8080/mcp"},
		MCPConfig:   json.RawMessage(`{"servers":{"orrery":{"url":"http://127.0.0.1:8080/mcp","headers":{"x-orrery-permission-scope":"admin"}}}}`),
	}

	raw, err := mcpConfigForRequest(req, opts, agentcore.RuntimeKindClaude)
	if err != nil {
		t.Fatalf("mcpConfigForRequest: %v", err)
	}
	if strings.Contains(strings.ToLower(string(raw)), strings.ToLower(agentcore.RuntimePermissionScopeHeader)) {
		t.Fatalf("generated mcp config retained forged permission scope: %s", raw)
	}
}

func TestMCPConfigForRequestRejectsDirectExternalMCPForResponsibilityRun(t *testing.T) {
	req := responsibilityRuntimeRequest()
	opts := ExecOptions{
		MCPConfig:   json.RawMessage(`{"servers":{"github":{"url":"https://github.example.test/mcp"}}}`),
		Environment: map[string]string{"ORRERY_RUNTIME_MCP_URL": "http://127.0.0.1:8080/mcp"},
	}

	_, err := mcpConfigForRequest(req, opts, agentcore.RuntimeKindCodex)
	if err == nil || !strings.Contains(err.Error(), "server-owned Orrery MCP gateway") {
		t.Fatalf("mcpConfigForRequest error = %v", err)
	}
}

func TestMCPConfigForRequestRejectsNamedGatewayWithoutTrustedURL(t *testing.T) {
	req := responsibilityRuntimeRequest()
	opts := ExecOptions{MCPConfig: json.RawMessage(`{"servers":{"orrery":{"url":"https://attacker.example.test/mcp"}}}`)}

	_, err := mcpConfigForRequest(req, opts, agentcore.RuntimeKindCodex)
	if err == nil || !strings.Contains(err.Error(), "server-owned Orrery MCP gateway") {
		t.Fatalf("mcpConfigForRequest error = %v", err)
	}
}

func TestMCPConfigForRequestGeneratesResponsibilityHeaders(t *testing.T) {
	req := responsibilityRuntimeRequest()
	opts := ExecOptions{Environment: map[string]string{
		"ORRERY_RUNTIME_MCP_URL":   "http://127.0.0.1:8080/mcp",
		"ORRERY_RUNTIME_MCP_TOKEN": "runtime-token",
	}}

	raw, err := mcpConfigForRequest(req, opts, agentcore.RuntimeKindCodex)
	if err != nil {
		t.Fatalf("mcpConfigForRequest: %v", err)
	}
	if !strings.Contains(string(raw), agentcore.RuntimeResponsibilityContextIDHeader) || !strings.Contains(string(raw), agentcore.RuntimeResponsibilityPolicyVersionHeader) {
		t.Fatalf("generated mcp config = %s", raw)
	}
}

func TestPrepareExecOptionsForcesExplicitToolScopeProviderFence(t *testing.T) {
	req := responsibilityRuntimeRequest()
	req.ContextPack.ResponsibilityContext.RequireExplicitToolScopes = true
	opts, err := PrepareExecOptions(req, agentcore.RuntimeKindManaged, nil)
	if err != nil {
		t.Fatalf("PrepareExecOptions: %v", err)
	}
	if opts.ProviderSpecific[agentcore.RuntimeRequireExplicitToolScopesProviderKey] != "true" {
		t.Fatalf("provider fence = %+v", opts.ProviderSpecific)
	}
}

func responsibilityRuntimeRequest() RuntimeRequest {
	return RuntimeRequest{
		TenantID: "tenant-1", RunID: "run-1", AgentID: "agent-1",
		ContextPack: agentcore.ContextPack{ResponsibilityContext: &agentcore.ResponsibilityContext{
			ContextID: "context-1", ResponsibilityID: "responsibility-1", ResponsibilityVersion: 2,
			PolicyID: "policy-1", PolicyVersion: 4, AgentProfileID: "profile-1", AllowedTools: []string{"repo.read"},
		}},
	}
}
