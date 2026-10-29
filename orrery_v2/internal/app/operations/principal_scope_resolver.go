package operations

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/app/identity"
)

type RolePermissionScopeMode string

const (
	RolePermissionScopeModeHybrid        RolePermissionScopeMode = "hybrid"
	RolePermissionScopeModeAuthoritative RolePermissionScopeMode = "authoritative"
	roleScopeBreakGlassAgentProfileID                            = "system-superadmin"
)

type RolePermissionScopeResolver struct {
	roles RoleStorePort
	mode  RolePermissionScopeMode
	now   func() time.Time
}

func NewRolePermissionScopeResolver(roles RoleStorePort) *RolePermissionScopeResolver {
	return &RolePermissionScopeResolver{
		roles: roles,
		mode:  RolePermissionScopeModeAuthoritative,
		now:   func() time.Time { return time.Now().UTC() },
	}
}

func ParseRolePermissionScopeMode(value string) (RolePermissionScopeMode, error) {
	mode := RolePermissionScopeMode(strings.ToLower(strings.TrimSpace(value)))
	if mode == "" {
		return RolePermissionScopeModeAuthoritative, nil
	}
	switch mode {
	case RolePermissionScopeModeHybrid, RolePermissionScopeModeAuthoritative:
		return mode, nil
	default:
		return "", fmt.Errorf("role permission scope mode %q is not supported", value)
	}
}

func (r *RolePermissionScopeResolver) WithMode(mode RolePermissionScopeMode) *RolePermissionScopeResolver {
	if r != nil {
		parsed, err := ParseRolePermissionScopeMode(string(mode))
		if err == nil {
			r.mode = parsed
		}
	}
	return r
}

func (r *RolePermissionScopeResolver) Mode() RolePermissionScopeMode {
	if r == nil || r.mode == "" {
		return RolePermissionScopeModeAuthoritative
	}
	return r.mode
}

func (r *RolePermissionScopeResolver) ResolvePermissionScopes(ctx context.Context, principal identity.Principal, accountScopes []string) ([]string, error) {
	accountScopes = canonicalPermissionScopes(accountScopes)
	principal = identity.NormalizePrincipal(principal)
	breakGlass := isRoleScopeBreakGlassPrincipal(principal)
	if breakGlass {
		return accountScopes, nil
	}
	if r == nil || r.roles == nil {
		if r != nil && r.Mode() == RolePermissionScopeModeAuthoritative {
			return nil, ErrServiceDependency
		}
		return accountScopes, nil
	}
	roleScopes, err := r.resolveRolePermissionScopes(ctx, principal)
	if err != nil {
		return nil, err
	}
	if r.Mode() == RolePermissionScopeModeAuthoritative {
		return roleScopes, nil
	}
	return canonicalPermissionScopes(append(accountScopes, roleScopes...)), nil
}

func (r *RolePermissionScopeResolver) ResolveAuthorizationAccountScopes(ctx context.Context, principal identity.Principal, accountScopes []string) ([]string, error) {
	accountScopes = canonicalPermissionScopes(accountScopes)
	principal = identity.NormalizePrincipal(principal)
	if isRoleScopeBreakGlassPrincipal(principal) {
		return accountScopes, nil
	}
	if r == nil || r.roles == nil {
		if r != nil && r.Mode() == RolePermissionScopeModeAuthoritative {
			return nil, ErrServiceDependency
		}
		return accountScopes, nil
	}
	assignments, err := r.roles.ListRoleAssignments(ctx, RoleAssignmentQuery{})
	if err != nil {
		return nil, err
	}
	roleIDs := effectivePrincipalPlatformRoleIDs(principal, assignments, r.currentTime())
	if len(roleIDs) == 0 {
		if r.Mode() == RolePermissionScopeModeAuthoritative {
			return nil, nil
		}
		return accountScopes, nil
	}
	stored, err := r.roles.ListRoleDefinitions(ctx)
	if err != nil {
		return nil, err
	}
	roleScopes := permissionScopesForPlatformRoleIDs(roleIDs, mergeRoleDefinitions(BuiltInRoleDefinitions(), stored))
	if r.Mode() == RolePermissionScopeModeAuthoritative {
		return roleScopes, nil
	}
	return canonicalPermissionScopes(append(accountScopes, roleScopes...)), nil
}

func (r *RolePermissionScopeResolver) resolveRolePermissionScopes(ctx context.Context, principal identity.Principal) ([]string, error) {
	if r == nil || r.roles == nil {
		return nil, ErrServiceDependency
	}
	principal = identity.NormalizePrincipal(principal)
	aliases := principalSubjectAliases(principal)
	actorTypes := principalActorTypes(principal.ViewerKind)
	if len(aliases) == 0 || len(actorTypes) == 0 {
		return nil, nil
	}
	assignments, err := r.roles.ListRoleAssignments(ctx, RoleAssignmentQuery{})
	if err != nil {
		return nil, err
	}
	roleIDs := effectivePrincipalRoleIDs(principal, assignments, r.currentTime())
	if len(roleIDs) == 0 {
		return nil, nil
	}
	stored, err := r.roles.ListRoleDefinitions(ctx)
	if err != nil {
		return nil, err
	}
	return permissionScopesForRoleIDs(roleIDs, mergeRoleDefinitions(BuiltInRoleDefinitions(), stored)), nil
}

func (r *RolePermissionScopeResolver) currentTime() time.Time {
	if r != nil && r.now != nil {
		return r.now().UTC()
	}
	return time.Now().UTC()
}

func effectivePrincipalRoleIDs(principal identity.Principal, assignments []RoleAssignmentDTO, now time.Time) map[string]struct{} {
	aliases := principalSubjectAliases(principal)
	actorTypes := principalActorTypes(principal.ViewerKind)
	roleIDs := map[string]struct{}{}
	for _, assignment := range assignments {
		if _, ok := actorTypes[strings.ToLower(strings.TrimSpace(assignment.Actor.ActorType))]; !ok {
			continue
		}
		if _, ok := aliases[strings.ToLower(strings.TrimSpace(assignment.Actor.ActorID))]; !ok {
			continue
		}
		if !assignment.StartsAt.IsZero() && assignment.StartsAt.After(now) {
			continue
		}
		if !assignment.ExpiresAt.IsZero() && !assignment.ExpiresAt.After(now) {
			continue
		}
		if roleID := strings.TrimSpace(assignment.RoleID); roleID != "" {
			roleIDs[roleID] = struct{}{}
		}
	}
	return roleIDs
}

func effectivePrincipalPlatformRoleIDs(principal identity.Principal, assignments []RoleAssignmentDTO, now time.Time) map[string]struct{} {
	aliases := principalSubjectAliases(principal)
	actorTypes := principalActorTypes(principal.ViewerKind)
	roleIDs := map[string]struct{}{}
	for _, assignment := range assignments {
		if !roleScopeIsPlatform(assignment.ScopeMode) {
			continue
		}
		if _, ok := actorTypes[strings.ToLower(strings.TrimSpace(assignment.Actor.ActorType))]; !ok {
			continue
		}
		if _, ok := aliases[strings.ToLower(strings.TrimSpace(assignment.Actor.ActorID))]; !ok {
			continue
		}
		if !assignment.StartsAt.IsZero() && assignment.StartsAt.After(now) {
			continue
		}
		if !assignment.ExpiresAt.IsZero() && !assignment.ExpiresAt.After(now) {
			continue
		}
		if roleID := strings.TrimSpace(assignment.RoleID); roleID != "" {
			roleIDs[roleID] = struct{}{}
		}
	}
	return roleIDs
}

func permissionScopesForRoleIDs(roleIDs map[string]struct{}, roles []RoleDefinitionDTO) []string {
	resolved := []string{}
	for _, role := range roles {
		if _, ok := roleIDs[strings.TrimSpace(role.RoleID)]; !ok || !strings.EqualFold(strings.TrimSpace(role.Status), organizationStatusActive) {
			continue
		}
		resolved = append(resolved, role.PermissionRefs...)
	}
	return canonicalPermissionScopes(resolved)
}

func permissionScopesForPlatformRoleIDs(roleIDs map[string]struct{}, roles []RoleDefinitionDTO) []string {
	resolved := []string{}
	for _, role := range roles {
		if _, ok := roleIDs[strings.TrimSpace(role.RoleID)]; !ok || !strings.EqualFold(strings.TrimSpace(role.Status), organizationStatusActive) || !roleRequiresPlatformAssignment(role) {
			continue
		}
		resolved = append(resolved, role.PermissionRefs...)
	}
	return canonicalPermissionScopes(resolved)
}

func isRoleScopeBreakGlassPrincipal(principal identity.Principal) bool {
	principal = identity.NormalizePrincipal(principal)
	if strings.EqualFold(strings.TrimSpace(principal.AgentProfileID), roleScopeBreakGlassAgentProfileID) {
		return true
	}
	if principal.ViewerKind != identity.ViewerKindSystem {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(principal.UserID), roleScopeBreakGlassAgentProfileID) ||
		strings.EqualFold(strings.TrimSpace(principal.Subject), roleScopeBreakGlassAgentProfileID)
}

func principalSubjectAliases(principal identity.Principal) map[string]struct{} {
	out := map[string]struct{}{}
	values := []string{principal.AgentProfileID}
	if strings.EqualFold(strings.TrimSpace(principal.ViewerKind), identity.ViewerKindUser) || strings.EqualFold(strings.TrimSpace(principal.ViewerKind), "human") {
		values = append(values, principal.UserID)
	}
	if strings.TrimSpace(principal.AgentProfileID) == "" && strings.EqualFold(strings.TrimSpace(principal.ViewerKind), identity.ViewerKindAgent) {
		values = append(values, principal.UserID)
	}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			out[value] = struct{}{}
		}
	}
	return out
}

func principalActorTypes(viewerKind string) map[string]struct{} {
	switch strings.ToLower(strings.TrimSpace(viewerKind)) {
	case identity.ViewerKindUser, "human":
		return map[string]struct{}{"human": {}, "user": {}}
	case identity.ViewerKindAgent:
		return map[string]struct{}{"agent": {}, "ai": {}}
	default:
		return nil
	}
}
