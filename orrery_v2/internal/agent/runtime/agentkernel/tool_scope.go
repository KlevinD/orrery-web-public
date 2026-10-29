package agentkernel

import (
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/permissionscope"
)

func explicitTaskScopePresent(scope *agentcore.ResponsibilityContext) bool {
	for _, permission := range scope.TaskPermissionScopes {
		if strings.TrimSpace(permission) != "" {
			return true
		}
	}
	return false
}

// This is an additional local fence over trusted binding grants. The host
// policy and gateway still resolve current authority, data ACLs and approvals.
// Empty grants cannot establish a tool's task scope and fail closed.
func explicitToolScopeAllows(scope *agentcore.ResponsibilityContext, binding agentcore.RuntimeToolBinding) bool {
	if scope == nil || !scope.RequireExplicitToolScopes {
		return true
	}
	allows := func(selectors []string, required string) bool {
		required = strings.TrimSpace(required)
		if required == "" {
			return false
		}
		for _, selector := range selectors {
			selector = strings.TrimSpace(selector)
			if selector == required || selector == "*" || permissionscope.Allows(selector, required) || (strings.HasSuffix(selector, "*") && strings.HasPrefix(required, strings.TrimSuffix(selector, "*"))) {
				return true
			}
		}
		return false
	}
	if !explicitTaskScopePresent(scope) || len(binding.GrantedPermissions) == 0 || allows(scope.ForbiddenActions, string(binding.ToolID)) || allows(scope.ForbiddenActions, binding.ServerToolID) {
		return false
	}
	selected := len(scope.AllowedTools) == 0 || allows(scope.AllowedTools, string(binding.ToolID)) || allows(scope.AllowedTools, binding.ServerToolID)
	allPermissionsSelected := true
	for _, permission := range binding.GrantedPermissions {
		if strings.TrimSpace(permission) == "" || !allows(scope.TaskPermissionScopes, permission) || allows(scope.ForbiddenActions, permission) || (len(scope.PolicyToolScopes) > 0 && !allows(scope.PolicyToolScopes, permission)) {
			return false
		}
		allPermissionsSelected = allPermissionsSelected && allows(scope.AllowedTools, permission)
	}
	return selected || allPermissionsSelected
}
