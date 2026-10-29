package operations

import (
	"context"
	"errors"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	authorizationapp "orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
)

type RolePermissionAuthorizer struct {
	roles    RoleStorePort
	orgs     OrganizationCatalogPort
	profiles AgentProfileLookupPort
	now      func() time.Time
}

type RoleAwareAuthorizationChecker struct {
	base  AuthorizationCheckerPort
	roles *RolePermissionAuthorizer
}

func NewRoleAwareAuthorizationChecker(base AuthorizationCheckerPort, roles *RolePermissionAuthorizer) *RoleAwareAuthorizationChecker {
	return &RoleAwareAuthorizationChecker{base: base, roles: roles}
}

func (c *RoleAwareAuthorizationChecker) Can(ctx context.Context, principal identity.Principal, action string, object identity.ObjectRef) error {
	if c == nil || c.base == nil {
		return ErrServiceDependency
	}
	principal = identity.NormalizePrincipal(principal)
	if principal.ViewerKind != identity.ViewerKindSystem && roleAuthorizationOwnsObject(object) {
		if c.roles == nil {
			return ErrServiceDependency
		}
		permission := rolePermissionForAuthorizationAction(action)
		if permission == "" {
			return authorizationapp.ErrDenied
		}
		allowed, err := c.roles.AuthorizeRolePermission(ctx, principal, permission, []identity.ObjectRef{object})
		if err != nil {
			return err
		}
		if allowed {
			return nil
		}
		return authorizationapp.ErrDenied
	}
	err := c.base.Can(ctx, principal, action, object)
	if err == nil || !errors.Is(err, authorizationapp.ErrDenied) || c.roles == nil {
		return err
	}
	permission := rolePermissionForAuthorizationAction(action)
	if permission == "" {
		return err
	}
	allowed, roleErr := c.roles.AuthorizeRolePermission(ctx, principal, permission, []identity.ObjectRef{object})
	if roleErr != nil {
		return roleErr
	}
	if allowed {
		return nil
	}
	return err
}

func roleAuthorizationOwnsObject(object identity.ObjectRef) bool {
	switch strings.ToLower(strings.TrimSpace(object.Type)) {
	case "platform", "organization_scope", "organization", "department", "position", "workforce", "role_assignment", "role":
		return true
	default:
		return false
	}
}

func rolePermissionForAuthorizationAction(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case authorizationActionOrganizationRead, authorizationActionDepartmentRead:
		return PermissionOrgRead
	case authorizationActionOrganizationManage, authorizationActionDepartmentManage:
		return PermissionOrgManage
	case authorizationActionWorkforceRead:
		return PermissionWorkforceRead
	case authorizationActionWorkforceManage:
		return PermissionWorkforceManage
	case authorizationActionRoleRead, authorizationActionRoleAssignmentRead:
		return PermissionRolesRead
	case authorizationActionRoleManage:
		return PermissionRolesManage
	default:
		return canonicalPermissionScope(action)
	}
}

func NewRolePermissionAuthorizer(roles RoleStorePort, orgs OrganizationCatalogPort, profiles AgentProfileLookupPort) *RolePermissionAuthorizer {
	return &RolePermissionAuthorizer{
		roles:    roles,
		orgs:     orgs,
		profiles: profiles,
		now:      func() time.Time { return time.Now().UTC() },
	}
}

func (a *RolePermissionAuthorizer) AuthorizeRolePermission(ctx context.Context, principal identity.Principal, permission string, objects []identity.ObjectRef) (bool, error) {
	if a == nil {
		return false, ErrServiceDependency
	}
	principal = identity.NormalizePrincipal(principal)
	permission = canonicalPermissionScope(permission)
	if permission == "" || len(objects) == 0 || !roleCredentialAllowsPermission(principal, permission) {
		return false, nil
	}
	if identity.TrustedInternalServiceFromContext(ctx) {
		return rolePrincipalCoarseScopeAllowsPermission(principal, permission), nil
	}
	if isRoleScopeBreakGlassPrincipal(principal) {
		return rolePrincipalCoarseScopeAllowsPermission(principal, permission), nil
	}
	if a.roles == nil || a.orgs == nil || a.profiles == nil {
		return false, ErrServiceDependency
	}
	actorID, actorTypes := roleAuthorizationActor(principal)
	if actorID == "" || len(actorTypes) == 0 {
		return false, nil
	}
	actorProfile, active, err := a.activeAuthorizationActor(ctx, actorID, actorTypes)
	if err != nil {
		return false, err
	}
	if !active {
		return false, nil
	}
	assignments, err := a.roles.ListRoleAssignments(ctx, RoleAssignmentQuery{})
	if err != nil {
		return false, err
	}
	storedRoles, err := a.roles.ListRoleDefinitions(ctx)
	if err != nil {
		return false, err
	}
	roles := mergeRoleDefinitions(BuiltInRoleDefinitions(), storedRoles)
	now := a.currentTime()
	platformAdmin := roleAssignmentsAllowPlatformAdminPermission(assignments, roles, actorID, actorTypes, permission, now)
	var graph OrgGraphDTO
	var nodes []OrganizationNodeDTO
	graphLoaded := false
	loadGraph := func() error {
		if graphLoaded {
			return nil
		}
		next, err := a.orgs.ListOrganizations(ctx, OrgGraphQuery{})
		if err != nil {
			return err
		}
		graph = next
		nodes, _ = buildOrganizationNodes(graph)
		graphLoaded = true
		return nil
	}
	for _, object := range objects {
		if platformAdmin {
			continue
		}
		if roleAuthorizationObjectUsesPlatformScope(assignments, object) {
			if !roleAssignmentsAllowPlatformPermission(assignments, roles, actorID, actorTypes, permission, now) {
				return false, nil
			}
			continue
		}
		if roleAuthorizationObjectAcceptsPlatformScope(object) &&
			roleAssignmentsAllowPlatformPermission(assignments, roles, actorID, actorTypes, permission, now) {
			continue
		}
		if err := loadGraph(); err != nil {
			return false, err
		}
		targetCode, targetOrganizationID, ok, err := a.targetOrganizationScope(ctx, graph, nodes, assignments, object)
		if err != nil {
			return false, err
		}
		actorOrganizationID := strings.TrimSpace(string(actorProfile.OrgIdentity.OrganizationRef.ID))
		if !ok || actorOrganizationID == "" || actorOrganizationID != targetOrganizationID ||
			!roleAssignmentsAllowPermission(assignments, roles, graph, nodes, targetOrganizationID, actorID, actorTypes, permission, targetCode, now) {
			return false, nil
		}
	}
	return true, nil
}

func rolePrincipalCoarseScopeAllowsPermission(principal identity.Principal, permission string) bool {
	for _, granted := range principal.CoarseScopes {
		if operationsPermissionScopeMatches(canonicalPermissionScope(granted), permission) {
			return true
		}
	}
	return false
}

func (a *RolePermissionAuthorizer) activeAuthorizationActor(ctx context.Context, actorID string, actorTypes map[string]struct{}) (agentcore.AgentProfile, bool, error) {
	profile, err := a.profiles.GetProfile(ctx, agentcore.AgentProfileID(actorID))
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, agentcore.ErrNotFound) {
			return agentcore.AgentProfile{}, false, nil
		}
		return agentcore.AgentProfile{}, false, err
	}
	if profile.ID.IsZero() || !strings.EqualFold(strings.TrimSpace(profile.ID.String()), actorID) || !profile.AgentKind.IsValid() || !profile.Enabled || !profile.Placement.IsInternal(profile.OrgIdentity) {
		return agentcore.AgentProfile{}, false, nil
	}
	if profile.AgentKind == agentcore.AgentKindHuman {
		if !profile.UserAccount.AccountEnabled || strings.TrimSpace(profile.UserAccount.Username) == "" {
			return agentcore.AgentProfile{}, false, nil
		}
		_, human := actorTypes["human"]
		_, user := actorTypes["user"]
		return profile, human || user, nil
	}
	_, agent := actorTypes["agent"]
	_, ai := actorTypes["ai"]
	return profile, agent || ai, nil
}

func roleAuthorizationObjectAcceptsPlatformScope(object identity.ObjectRef) bool {
	return strings.EqualFold(strings.TrimSpace(object.Type), "workforce")
}

func roleAuthorizationObjectUsesPlatformScope(assignments []RoleAssignmentDTO, object identity.ObjectRef) bool {
	object.Type = strings.ToLower(strings.TrimSpace(object.Type))
	object.ID = strings.TrimSpace(object.ID)
	switch object.Type {
	case "platform":
		return true
	case "role_assignment":
		for _, assignment := range assignments {
			if strings.TrimSpace(assignment.AssignmentID) == object.ID {
				return roleScopeIsPlatform(assignment.ScopeMode)
			}
		}
	case "role":
		return strings.HasPrefix(object.ID, "global/")
	}
	return false
}

func (a *RolePermissionAuthorizer) currentTime() time.Time {
	if a != nil && a.now != nil {
		return a.now().UTC()
	}
	return time.Now().UTC()
}

func (a *RolePermissionAuthorizer) targetOrganizationScope(ctx context.Context, graph OrgGraphDTO, nodes []OrganizationNodeDTO, assignments []RoleAssignmentDTO, object identity.ObjectRef) (string, string, bool, error) {
	object.Type = strings.ToLower(strings.TrimSpace(object.Type))
	object.ID = strings.TrimSpace(object.ID)
	object.TenantID = strings.TrimSpace(object.TenantID)
	switch object.Type {
	case "organization_scope":
		code := normalizeOrganizationCodeForQuery(object.ID)
		return organizationScopeForCode(graph, nodes, code)
	case "organization", "department":
		return organizationScopeForNodeID(graph, nodes, object.ID)
	case "position":
		for _, position := range graph.Positions {
			if strings.TrimSpace(position.PositionID) == object.ID {
				return organizationScopeForNodeID(graph, nodes, position.DepartmentID)
			}
		}
		return "", "", false, nil
	case "workforce":
		if a.profiles == nil || object.ID == "" {
			return "", "", false, nil
		}
		profile, err := a.profiles.GetProfile(ctx, agentcore.AgentProfileID(object.ID))
		if err != nil {
			return "", "", false, nil
		}
		nodeID := firstNonEmpty(string(profile.OrgIdentity.DepartmentRef.ID), string(profile.OrgIdentity.OrganizationRef.ID))
		return organizationScopeForNodeID(graph, nodes, nodeID)
	case "role_assignment":
		for _, assignment := range assignments {
			if strings.TrimSpace(assignment.AssignmentID) == object.ID {
				return organizationScopeForNodeID(graph, nodes, assignment.OrganizationNodeID)
			}
		}
		return "", "", false, nil
	case "role":
		organizationID, _, _ := strings.Cut(object.ID, "/")
		return organizationScopeForNodeID(graph, nodes, organizationID)
	case "platform":
		return "", "", false, nil
	default:
		// TenantID is request metadata for many routes. It is not authoritative
		// object ownership and must never select an organization role scope.
		return "", "", false, nil
	}
}

func organizationScopeForCode(graph OrgGraphDTO, nodes []OrganizationNodeDTO, code string) (string, string, bool, error) {
	code = normalizeOrganizationCodeForQuery(code)
	if code == "" {
		return "", "", false, nil
	}
	node, ok := findOrganizationNodeByCode(nodes, code)
	if !ok {
		return "", "", false, nil
	}
	return organizationScopeForNodeID(graph, nodes, node.ID)
}

func organizationScopeForNodeID(graph OrgGraphDTO, nodes []OrganizationNodeDTO, nodeID string) (string, string, bool, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return "", "", false, nil
	}
	node := findOrganizationNode(nodes, nodeID)
	code := normalizeOrganizationCodeForQuery(node.Code)
	organizationID := strings.TrimSpace(organizationIDForNode(graph, node.ID))
	return code, organizationID, code != "" && organizationID != "", nil
}

func roleAuthorizationActor(principal identity.Principal) (string, map[string]struct{}) {
	switch strings.ToLower(strings.TrimSpace(principal.ViewerKind)) {
	case identity.ViewerKindUser, "human":
		id := strings.TrimSpace(principal.AgentProfileID)
		if id == "" {
			return "", nil
		}
		return id, map[string]struct{}{"human": {}, "user": {}}
	case identity.ViewerKindAgent:
		id := firstNonEmpty(principal.AgentProfileID, principal.UserID)
		if strings.TrimSpace(id) == "" {
			return "", nil
		}
		return strings.TrimSpace(id), map[string]struct{}{"agent": {}, "ai": {}}
	default:
		return "", nil
	}
}

func roleAssignmentsAllowPermission(assignments []RoleAssignmentDTO, roles []RoleDefinitionDTO, graph OrgGraphDTO, nodes []OrganizationNodeDTO, targetOrganizationID string, actorID string, actorTypes map[string]struct{}, permission string, targetCode string, now time.Time) bool {
	rolePermissions := map[string][]string{}
	for _, role := range roles {
		if !strings.EqualFold(strings.TrimSpace(role.Status), organizationStatusActive) {
			continue
		}
		rolePermissions[strings.TrimSpace(role.RoleID)] = canonicalPermissionScopes(role.PermissionRefs)
	}
	for _, assignment := range effectiveRoleAssignments(assignments, targetCode, now) {
		if roleScopeIsPlatform(assignment.ScopeMode) {
			continue
		}
		assignmentCode, assignmentOrganizationID, validScope, _ := organizationScopeForNodeID(graph, nodes, assignment.OrganizationNodeID)
		if !validScope || assignmentOrganizationID != targetOrganizationID || assignmentCode != normalizeOrganizationCodeForQuery(assignment.OrganizationCode) {
			continue
		}
		if _, ok := actorTypes[strings.ToLower(strings.TrimSpace(assignment.Actor.ActorType))]; !ok {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(assignment.Actor.ActorID), actorID) {
			continue
		}
		for _, granted := range rolePermissions[strings.TrimSpace(assignment.RoleID)] {
			if operationsPermissionScopeMatches(granted, permission) {
				return true
			}
		}
	}
	return false
}

func roleAssignmentsAllowPlatformPermission(assignments []RoleAssignmentDTO, roles []RoleDefinitionDTO, actorID string, actorTypes map[string]struct{}, permission string, now time.Time) bool {
	rolePermissions := map[string][]string{}
	for _, role := range roles {
		if !strings.EqualFold(strings.TrimSpace(role.Status), organizationStatusActive) || !roleRequiresPlatformAssignment(role) {
			continue
		}
		rolePermissions[strings.TrimSpace(role.RoleID)] = canonicalPermissionScopes(role.PermissionRefs)
	}
	for _, assignment := range assignments {
		if !roleScopeIsPlatform(assignment.ScopeMode) {
			continue
		}
		if strings.TrimSpace(assignment.OrganizationNodeID) != "" || normalizeOrganizationCodeForQuery(assignment.OrganizationCode) != "" {
			continue
		}
		if !assignment.StartsAt.IsZero() && assignment.StartsAt.After(now) {
			continue
		}
		if !assignment.ExpiresAt.IsZero() && !assignment.ExpiresAt.After(now) {
			continue
		}
		if _, ok := actorTypes[strings.ToLower(strings.TrimSpace(assignment.Actor.ActorType))]; !ok {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(assignment.Actor.ActorID), actorID) {
			continue
		}
		for _, granted := range rolePermissions[strings.TrimSpace(assignment.RoleID)] {
			if operationsPermissionScopeMatches(granted, permission) {
				return true
			}
		}
	}
	return false
}

func roleAssignmentsAllowPlatformAdminPermission(assignments []RoleAssignmentDTO, roles []RoleDefinitionDTO, actorID string, actorTypes map[string]struct{}, permission string, now time.Time) bool {
	grantsPermission := false
	for _, role := range roles {
		if !strings.EqualFold(strings.TrimSpace(role.RoleID), RoleIDPlatformAdmin) ||
			!strings.EqualFold(strings.TrimSpace(role.Status), organizationStatusActive) ||
			!roleRequiresPlatformAssignment(role) {
			continue
		}
		for _, granted := range canonicalPermissionScopes(role.PermissionRefs) {
			if operationsPermissionScopeMatches(granted, permission) {
				grantsPermission = true
				break
			}
		}
	}
	if !grantsPermission {
		return false
	}
	for _, assignment := range assignments {
		if !strings.EqualFold(strings.TrimSpace(assignment.RoleID), RoleIDPlatformAdmin) || !roleScopeIsPlatform(assignment.ScopeMode) {
			continue
		}
		if strings.TrimSpace(assignment.OrganizationNodeID) != "" || normalizeOrganizationCodeForQuery(assignment.OrganizationCode) != "" {
			continue
		}
		if !assignment.StartsAt.IsZero() && assignment.StartsAt.After(now) {
			continue
		}
		if !assignment.ExpiresAt.IsZero() && !assignment.ExpiresAt.After(now) {
			continue
		}
		if _, ok := actorTypes[strings.ToLower(strings.TrimSpace(assignment.Actor.ActorType))]; !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(assignment.Actor.ActorID), actorID) {
			return true
		}
	}
	return false
}

func roleCredentialAllowsPermission(principal identity.Principal, permission string) bool {
	if !principal.CredentialScopeRestricted {
		return true
	}
	for _, granted := range principal.CredentialScopes {
		if operationsPermissionScopeMatches(canonicalPermissionScope(granted), permission) {
			return true
		}
	}
	return false
}
