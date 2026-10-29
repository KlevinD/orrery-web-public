package operations

import (
	"context"
	"strings"
	"time"
)

func (s *QueryService) roleDecisionSource(ctx context.Context, req PermissionDecisionRequestDTO) (PermissionDecisionSourceDTO, bool, error) {
	if s == nil || s.roles == nil {
		return PermissionDecisionSourceDTO{}, false, nil
	}
	assignments, err := s.roles.ListRoleAssignments(ctx, RoleAssignmentQuery{
		ActorType:        req.Subject.ActorType,
		ActorID:          req.Subject.ActorID,
		OrganizationCode: req.OrganizationCode,
	})
	if err != nil {
		return PermissionDecisionSourceDTO{}, false, err
	}
	now := time.Now().UTC()
	if req.OrganizationCode == "" {
		assignments = effectivePlatformRoleAssignments(assignments, now)
	} else {
		assignments = effectiveRoleAssignments(assignments, req.OrganizationCode, now)
	}
	roles, err := s.allRoleDefinitions(ctx)
	if err != nil {
		return PermissionDecisionSourceDTO{}, false, err
	}
	permissionRefsByRole := map[string]map[string]struct{}{}
	for _, role := range roles {
		if !strings.EqualFold(strings.TrimSpace(role.Status), organizationStatusActive) {
			continue
		}
		if req.OrganizationCode == "" && !roleRequiresPlatformAssignment(role) {
			continue
		}
		refs := map[string]struct{}{}
		for _, permission := range role.PermissionRefs {
			refs[strings.TrimSpace(permission)] = struct{}{}
		}
		permissionRefsByRole[role.RoleID] = refs
	}
	for _, assignment := range assignments {
		if _, ok := permissionRefsByRole[assignment.RoleID][req.Action]; !ok {
			continue
		}
		sourceType := permissionDecisionSourceRole
		if req.OrganizationCode != "" && normalizeOrganizationCodeForQuery(assignment.OrganizationCode) != req.OrganizationCode {
			sourceType = permissionDecisionSourceAncestorRole
		}
		return PermissionDecisionSourceDTO{
			SourceType:       sourceType,
			SourceID:         assignment.AssignmentID,
			RoleID:           assignment.RoleID,
			OrganizationCode: assignment.OrganizationCode,
			ResourceID:       req.ResourceID,
			Effect:           PermissionDecisionAllow,
			Reason:           "role grants " + req.Action,
		}, true, nil
	}
	return PermissionDecisionSourceDTO{}, false, nil
}

func effectivePlatformRoleAssignments(assignments []RoleAssignmentDTO, now time.Time) []RoleAssignmentDTO {
	out := make([]RoleAssignmentDTO, 0, len(assignments))
	for _, assignment := range assignments {
		if !roleScopeIsPlatform(assignment.ScopeMode) || strings.TrimSpace(assignment.OrganizationNodeID) != "" || normalizeOrganizationCodeForQuery(assignment.OrganizationCode) != "" {
			continue
		}
		if !assignment.ExpiresAt.IsZero() && !assignment.ExpiresAt.After(now) {
			continue
		}
		if !assignment.StartsAt.IsZero() && assignment.StartsAt.After(now) {
			continue
		}
		out = append(out, assignment)
	}
	return out
}
