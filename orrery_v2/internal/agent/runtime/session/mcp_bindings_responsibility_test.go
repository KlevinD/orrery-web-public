package session

import (
	"encoding/json"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestRuntimeMCPBindingsUsesCanonicalResponsibilityHeaders(t *testing.T) {
	request := responsibilityMCPBindingRequest()
	request.ExecutionPrincipal = agentcore.ExecutionPrincipal{Kind: agentcore.ExecutionPrincipalUser, ID: "user-1", Source: "system_assistant", PermissionScope: []string{"mission.read"}}
	request.RuntimeOptions.MCPConfig = json.RawMessage(`{"servers":{"orrery":{"url":"http://127.0.0.1:8080/mcp","headers":{"X-Orrery-Responsibility":"forged"}}}}`)

	bindings, err := runtimeMCPBindings(request)
	if err != nil {
		t.Fatalf("runtimeMCPBindings: %v", err)
	}
	if len(bindings) != 1 || bindings[0].Headers[agentcore.RuntimeRunIDHeader] != "run-1" ||
		bindings[0].Headers[agentcore.RuntimeResponsibilityIDHeader] != "responsibility-1" || bindings[0].Headers[agentcore.RuntimeResponsibilityPolicyVersionHeader] != "4" ||
		bindings[0].Headers[agentcore.RuntimePrincipalIDHeader] != "user-1" || bindings[0].Headers[agentcore.RuntimePrincipalKindHeader] != "user" ||
		bindings[0].Headers[agentcore.RuntimePermissionScopeHeader] != "mission.read" || bindings[0].Headers[agentcore.RuntimeExecutionSourceHeader] != "system_assistant" || bindings[0].Headers["X-Agent-ID"] != "agent-1" {
		t.Fatalf("bindings = %+v", bindings)
	}
}

func TestRuntimeMCPBindingsBindsUserAuthorityOnlyToTrustedOrreryServer(t *testing.T) {
	request := agentcore.RuntimeRequest{
		TenantID: "tenant-1", RunID: "run-1", AgentID: "agent-1",
		ExecutionPrincipal: agentcore.ExecutionPrincipal{Kind: agentcore.ExecutionPrincipalUser, ID: "user-1", Source: "system_assistant", PermissionScope: []string{"mission.read"}},
		RuntimeOptions: agentcore.RuntimeOptions{
			Environment: map[string]string{"ORRERY_RUNTIME_MCP_URL": "http://127.0.0.1:8080/mcp"},
			MCPConfig:   json.RawMessage(`{"servers":{"orrery":{"url":"http://127.0.0.1:8080/mcp/","headers":{"Authorization":"Bearer runtime","x-orrery-principal-id":"forged","X-Orrery-Permission-Scope":"admin","X-Orrery-Responsibility":"forged"},"http_headers":{"x-agent-id":"forged"}},"external":{"url":"https://external.example.test/mcp","headers":{"Authorization":"Bearer external"}}}}`),
		},
	}

	bindings, err := runtimeMCPBindings(request)
	if err != nil {
		t.Fatalf("runtimeMCPBindings: %v", err)
	}
	byName := make(map[string]RuntimeMCPBinding, len(bindings))
	for _, binding := range bindings {
		byName[binding.Spec.Name] = binding
	}
	internal := byName["orrery"]
	if internal.Headers["Authorization"] != "Bearer runtime" || internal.Headers[agentcore.RuntimePrincipalIDHeader] != "user-1" ||
		internal.Headers[agentcore.RuntimePrincipalKindHeader] != "user" || internal.Headers[agentcore.RuntimePermissionScopeHeader] != "mission.read" ||
		internal.Headers[agentcore.RuntimeResponsibilityIDHeader] != "" || internal.Headers["x-agent-id"] != "" || internal.Headers["X-Agent-ID"] != "agent-1" {
		t.Fatalf("trusted binding = %+v", internal)
	}
	external := byName["external"]
	if external.Headers["Authorization"] != "Bearer external" || external.Headers[agentcore.RuntimePrincipalIDHeader] != "" || external.Headers[agentcore.RuntimePermissionScopeHeader] != "" {
		t.Fatalf("external binding = %+v", external)
	}
}

func TestRuntimeMCPBindingsRemovesForgedUserScopeWhenUserHasNone(t *testing.T) {
	request := agentcore.RuntimeRequest{
		TenantID: "tenant-1", RunID: "run-1", AgentID: "agent-1",
		ExecutionPrincipal: agentcore.ExecutionPrincipal{Kind: agentcore.ExecutionPrincipalUser, ID: "user-1", Source: "system_assistant"},
		RuntimeOptions: agentcore.RuntimeOptions{
			Environment: map[string]string{"ORRERY_RUNTIME_MCP_URL": "http://127.0.0.1:8080/mcp"},
			MCPConfig:   json.RawMessage(`{"servers":{"orrery":{"url":"http://127.0.0.1:8080/mcp","headers":{"x-orrery-permission-scope":"admin"}}}}`),
		},
	}

	bindings, err := runtimeMCPBindings(request)
	if err != nil {
		t.Fatalf("runtimeMCPBindings: %v", err)
	}
	for key := range bindings[0].Headers {
		if strings.EqualFold(key, agentcore.RuntimePermissionScopeHeader) {
			t.Fatalf("binding retained forged permission scope: %+v", bindings[0])
		}
	}
}

func TestRuntimeMCPBindingsRejectsExternalServerForResponsibilityRun(t *testing.T) {
	request := responsibilityMCPBindingRequest()
	request.RuntimeOptions.MCPConfig = json.RawMessage(`{"servers":{"github":{"url":"https://github.example.test/mcp"}}}`)

	_, err := runtimeMCPBindings(request)
	if err == nil || !strings.Contains(err.Error(), "server-owned Orrery MCP gateway") {
		t.Fatalf("runtimeMCPBindings error = %v", err)
	}
}

func TestRuntimeMCPBindingsRejectsNamedGatewayWithoutTrustedURL(t *testing.T) {
	request := responsibilityMCPBindingRequest()
	request.RuntimeOptions.Environment = nil
	request.RuntimeOptions.MCPConfig = json.RawMessage(`{"servers":{"orrery":{"url":"https://attacker.example.test/mcp"}}}`)

	_, err := runtimeMCPBindings(request)
	if err == nil || !strings.Contains(err.Error(), "server-owned Orrery MCP gateway") {
		t.Fatalf("runtimeMCPBindings error = %v", err)
	}
}

func responsibilityMCPBindingRequest() agentcore.RuntimeRequest {
	return agentcore.RuntimeRequest{
		TenantID: "tenant-1", RunID: "run-1", AgentID: "agent-1",
		RuntimeOptions: agentcore.RuntimeOptions{Environment: map[string]string{"ORRERY_RUNTIME_MCP_URL": "http://127.0.0.1:8080/mcp"}},
		ContextPack: agentcore.ContextPack{ResponsibilityContext: &agentcore.ResponsibilityContext{
			ContextID: "context-1", ResponsibilityID: "responsibility-1", ResponsibilityVersion: 2,
			PolicyID: "policy-1", PolicyVersion: 4, AgentProfileID: "profile-1", AllowedTools: []string{"repo.read"},
		}},
	}
}
