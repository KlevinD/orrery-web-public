package core

import "testing"

func TestExecutionPrincipalDefaultsOrdinaryRunsToAgentIdentity(t *testing.T) {
	principal := NormalizeExecutionPrincipal(ExecutionPrincipal{}, "tenant-a", AgentID("agent-one"))
	if principal.Kind != ExecutionPrincipalAgent || principal.ID != "agent-one" || principal.TenantID != "tenant-a" {
		t.Fatalf("principal = %+v", principal)
	}
	headers := RuntimeExecutionPrincipalHeaders(ExecutionPrincipal{}, "tenant-a", AgentID("agent-one"))
	if headers[RuntimePrincipalIDHeader] != "agent-one" || headers[RuntimePrincipalKindHeader] != string(ExecutionPrincipalAgent) {
		t.Fatalf("headers = %+v", headers)
	}
}

func TestExecutionPrincipalPreservesPersonalAssistantUserIdentity(t *testing.T) {
	headers := RuntimeExecutionPrincipalHeaders(ExecutionPrincipal{
		Kind: ExecutionPrincipalUser, ID: "user-one", Source: "system_assistant",
		PermissionScope: []string{"mission.read", "mission.read", "discussion.write"},
	}, "tenant-a", AgentID("technical-agent"))
	if headers[RuntimePrincipalIDHeader] != "user-one" || headers[RuntimePrincipalKindHeader] != string(ExecutionPrincipalUser) || headers[RuntimeExecutionSourceHeader] != "system_assistant" {
		t.Fatalf("headers = %+v", headers)
	}
	if headers[RuntimePermissionScopeHeader] != "mission.read,discussion.write" {
		t.Fatalf("permission header = %q", headers[RuntimePermissionScopeHeader])
	}
}

func TestExecutionPrincipalUsesRuntimeTenantAsAuthority(t *testing.T) {
	principal := NormalizeExecutionPrincipal(ExecutionPrincipal{
		Kind: ExecutionPrincipalUser, ID: "user-one", TenantID: "tenant-forged",
	}, "tenant-bound", AgentID("technical-agent"))
	if principal.TenantID != "tenant-bound" {
		t.Fatalf("tenant = %q, want runtime-bound tenant", principal.TenantID)
	}
}
