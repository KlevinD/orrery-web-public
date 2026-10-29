package tool

import (
	"fmt"

	agentcore "orrery_v2/internal/agent/core"
)

type Principal struct {
	AgentID     agentcore.AgentID
	Permissions []string
}

func checkPermissions(required []PermissionDeclaration, granted []string) error {
	grantedSet := map[string]struct{}{}
	for _, permission := range granted {
		grantedSet[permission] = struct{}{}
	}
	for _, permission := range required {
		if permission.Scope == "" {
			continue
		}
		if _, ok := grantedSet[permission.Scope]; !ok {
			return fmt.Errorf("%w: missing %s", ErrPermissionDenied, permission.Scope)
		}
	}
	return nil
}

func grantedPermissions(required []PermissionDeclaration, granted []string) []string {
	grantedSet := map[string]struct{}{}
	for _, permission := range granted {
		grantedSet[permission] = struct{}{}
	}
	out := make([]string, 0, len(required))
	for _, permission := range required {
		if _, ok := grantedSet[permission.Scope]; ok {
			out = append(out, permission.Scope)
		}
	}
	return out
}
