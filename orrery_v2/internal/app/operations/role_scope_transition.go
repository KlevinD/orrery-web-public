package operations

import (
	"context"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/identity"
)

type RoleScopeTransitionProfileReader interface {
	QueryDirectory(ctx context.Context, query agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error)
	GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error)
}

type RoleScopeTransitionDiagnostics struct {
	Mode                      RolePermissionScopeMode
	Ready                     bool
	TransitionReady           bool
	Principals                int
	BreakGlassPrincipals      int
	LegacyDependentPrincipals int
	AccountScopes             int
	EffectiveRoleScopes       int
	DroppedScopes             int
	BreakGlassRetainedScopes  int
	ObjectScopeConflicts      int
}

func (r *RolePermissionScopeResolver) DiagnoseTransition(ctx context.Context, profiles RoleScopeTransitionProfileReader) (RoleScopeTransitionDiagnostics, error) {
	report := RoleScopeTransitionDiagnostics{Mode: r.Mode()}
	if r == nil || r.roles == nil || profiles == nil {
		return report, ErrServiceDependency
	}
	page, err := profiles.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{
		AgentKind:     agentcore.AgentKindHuman,
		EnabledOnly:   true,
		IncludeHumans: true,
		Placement:     agentcore.TalentPlacementFilterInternal,
	})
	if err != nil {
		return report, err
	}
	assignments, err := r.roles.ListRoleAssignments(ctx, RoleAssignmentQuery{})
	if err != nil {
		return report, err
	}
	storedRoles, err := r.roles.ListRoleDefinitions(ctx)
	if err != nil {
		return report, err
	}
	roles := mergeRoleDefinitions(BuiltInRoleDefinitions(), storedRoles)
	now := r.currentTime()
	for _, entry := range page.Entries {
		profile, err := profiles.GetProfile(ctx, entry.AgentProfileID)
		if err != nil {
			return report, err
		}
		if profile.AgentKind != agentcore.AgentKindHuman || !profile.Enabled || !profile.Placement.IsInternal(profile.OrgIdentity) || !profile.UserAccount.AccountEnabled || profile.UserAccount.Username == "" {
			continue
		}
		principal := identity.NormalizePrincipal(identity.Principal{
			SubjectProvider: "local",
			Subject:         profile.UserAccount.Username,
			UserID:          profile.UserAccount.Username,
			Username:        profile.UserAccount.Username,
			AgentProfileID:  profile.ID.String(),
			ViewerKind:      identity.ViewerKindUser,
		})
		accountScopes := canonicalPermissionScopes(profile.UserAccount.PermissionScope)
		roleScopes := permissionScopesForRoleIDs(effectivePrincipalRoleIDs(principal, assignments, now), roles)
		// Account scopes are platform-global grants. Organization role scopes with
		// the same name are not equivalent replacements for those grants.
		dropped := append([]string(nil), accountScopes...)
		report.ObjectScopeConflicts += len(permissionScopeIntersection(accountScopes, roleScopes))
		report.Principals++
		report.AccountScopes += len(accountScopes)
		report.EffectiveRoleScopes += len(roleScopes)
		if isRoleScopeBreakGlassPrincipal(principal) {
			report.BreakGlassPrincipals++
			report.BreakGlassRetainedScopes += len(dropped)
			continue
		}
		if len(dropped) > 0 {
			report.LegacyDependentPrincipals++
			report.DroppedScopes += len(dropped)
		}
	}
	report.TransitionReady = report.LegacyDependentPrincipals == 0
	report.Ready = report.Mode != RolePermissionScopeModeAuthoritative || report.TransitionReady
	return report, nil
}

func permissionScopeIntersection(left, right []string) []string {
	rightSet := make(map[string]struct{}, len(right))
	for _, scope := range canonicalPermissionScopes(right) {
		rightSet[scope] = struct{}{}
	}
	out := []string{}
	for _, scope := range canonicalPermissionScopes(left) {
		if _, ok := rightSet[scope]; ok {
			out = append(out, scope)
		}
	}
	return out
}

func permissionScopeDifference(left, right []string) []string {
	rightSet := make(map[string]struct{}, len(right))
	for _, scope := range canonicalPermissionScopes(right) {
		rightSet[scope] = struct{}{}
	}
	out := []string{}
	for _, scope := range canonicalPermissionScopes(left) {
		if _, ok := rightSet[scope]; !ok {
			out = append(out, scope)
		}
	}
	return out
}
