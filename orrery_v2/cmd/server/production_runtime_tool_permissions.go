package main

import (
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	mcptransport "orrery_v2/internal/transport/mcp"
)

func runtimeExecutionPrincipal(call runtimecore.RuntimeToolCallRequest) agentcore.ExecutionPrincipal {
	return agentcore.NormalizeExecutionPrincipal(call.ExecutionPrincipal, call.TenantID, call.AgentID)
}

func runtimeExecutionPrincipalID(call runtimecore.RuntimeToolCallRequest) string {
	return runtimeExecutionPrincipal(call).ID
}

func runtimeExecutionPrincipalKind(call runtimecore.RuntimeToolCallRequest) string {
	return string(runtimeExecutionPrincipal(call).Kind)
}

func runtimeDataActorRef(call runtimecore.RuntimeToolCallRequest) map[string]any {
	principal := runtimeExecutionPrincipal(call)
	return map[string]any{
		"kind": string(principal.Kind),
		"id":   principal.ID,
	}
}

func runtimeGatewayPermissions(call runtimecore.RuntimeToolCallRequest, binding agentcore.RuntimeToolBinding) []string {
	principal := runtimeExecutionPrincipal(call)
	if principal.Kind == agentcore.ExecutionPrincipalUser {
		return mergeRuntimePermissionScopes(principal.PermissionScope, binding.GrantedPermissions)
	}
	return mergeRuntimePermissionScopes(binding.GrantedPermissions)
}

func runtimeGatewayPermissionsAuthoritative(call runtimecore.RuntimeToolCallRequest) bool {
	return runtimeExecutionPrincipal(call).Kind == agentcore.ExecutionPrincipalUser
}

func mergeRuntimePermissionScopes(groups ...[]string) []string {
	merged := make([]string, 0)
	seen := map[string]bool{}
	for _, group := range groups {
		for _, permission := range group {
			permission = strings.TrimSpace(permission)
			if permission == "" || seen[permission] {
				continue
			}
			seen[permission] = true
			merged = append(merged, permission)
		}
	}
	return merged
}

func runtimePermissionScopesContain(scopes []string, required string) bool {
	required = strings.TrimSpace(required)
	if required == "" {
		return false
	}
	for _, permission := range scopes {
		if strings.TrimSpace(permission) == required {
			return true
		}
	}
	return false
}

// validateRuntimeExecutionEnvelope keeps native tool branches subject to the
// same signed authorization snapshot as MCP-backed branches. Older direct
// callers may omit an envelope, but every envelope that is supplied is
// verified before a tool is allowed to run.
func validateRuntimeExecutionEnvelope(call runtimecore.RuntimeToolCallRequest, binding agentcore.RuntimeToolBinding) error {
	envelope := call.ExecutionEnvelope
	if envelope == nil {
		return nil
	}
	if err := envelope.Verify(); err != nil {
		return mcptransport.ErrPermissionDenied
	}
	if !envelope.ExpiresAt.IsZero() && !envelope.ExpiresAt.After(time.Now().UTC()) {
		return mcptransport.ErrPermissionDenied
	}
	if tenantID := strings.TrimSpace(call.TenantID); tenantID != "" && tenantID != strings.TrimSpace(envelope.TenantID) {
		return mcptransport.ErrPermissionDenied
	}
	if runID := strings.TrimSpace(call.RunID.String()); runID != "" && runID != strings.TrimSpace(envelope.RunID) {
		return mcptransport.ErrPermissionDenied
	}
	if agentID := strings.TrimSpace(call.AgentID.String()); agentID != "" && agentID != strings.TrimSpace(envelope.AgentID) {
		return mcptransport.ErrPermissionDenied
	}
	if responsibility := call.ResponsibilityContext; responsibility != nil {
		claimed := envelope.ResponsibilityContext
		if claimed == nil || strings.TrimSpace(claimed.ContextID) != strings.TrimSpace(responsibility.ContextID) ||
			claimed.ResponsibilityVersion != responsibility.ResponsibilityVersion ||
			strings.TrimSpace(claimed.PolicyID) != strings.TrimSpace(responsibility.PolicyID) ||
			claimed.PolicyVersion != responsibility.PolicyVersion {
			return mcptransport.ErrPermissionDenied
		}
	} else if envelope.ResponsibilityContext != nil {
		return mcptransport.ErrPermissionDenied
	}
	toolID := strings.TrimSpace(string(binding.ToolID))
	if toolID == "" {
		toolID = strings.TrimSpace(call.Name)
	}
	if toolID != "" && envelope.AllowsTool(toolID, binding.GrantedPermissions) {
		return nil
	}
	return mcptransport.ErrPermissionDenied
}
