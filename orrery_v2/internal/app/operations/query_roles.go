package operations

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

type RoleStorePort interface {
	ListRoleDefinitions(ctx context.Context) ([]RoleDefinitionDTO, error)
	SaveRoleDefinition(ctx context.Context, role RoleDefinitionDTO) error
	ListRoleAssignments(ctx context.Context, query RoleAssignmentQuery) ([]RoleAssignmentDTO, error)
	SaveRoleAssignment(ctx context.Context, assignment RoleAssignmentDTO) error
	DeleteRoleAssignment(ctx context.Context, assignmentID string) error
}

type RoleDefinitionBatchStore interface {
	SaveRoleDefinitions(ctx context.Context, roles []RoleDefinitionDTO) error
}

type PermissionRoleReadStore interface {
	ListRoleDefinitionsPage(ctx context.Context, afterRoleID string, limit int) (RoleDefinitionRecordPage, error)
	GetRoleDefinition(ctx context.Context, roleID string) (RoleDefinitionDTO, bool, error)
	PermissionPolicyRevision(ctx context.Context) (int64, error)
}

type PermissionRoleAssignmentReadStore interface {
	GetRoleAssignment(ctx context.Context, assignmentID string) (RoleAssignmentDTO, bool, error)
	ListRoleAssignmentsPage(ctx context.Context, afterAssignmentID string, limit int) (RoleAssignmentRecordPage, error)
}

func (s *QueryService) SetRoleStore(store RoleStorePort) {
	if s != nil {
		s.roles = store
	}
}

func (s *QueryService) ListRoleDefinitions(ctx context.Context, viewer ViewerRef) (RoleDefinitionPageDTO, error) {
	if err := requireOperationsPermission(viewer, PermissionRolesRead); err != nil {
		return RoleDefinitionPageDTO{}, err
	}
	if err := s.authorizePlatformOperation(ctx, viewer, PermissionRolesRead); err != nil {
		return RoleDefinitionPageDTO{}, err
	}
	roles, err := s.allRoleDefinitions(ctx)
	if err != nil {
		return RoleDefinitionPageDTO{}, err
	}
	roles = UserAssignableRoleDefinitions(roles)
	return RoleDefinitionPageDTO{Items: roles, Page: PageDTO{Total: len(roles), Returned: len(roles)}}, nil
}

func (s *QueryService) ListPermissionRoles(ctx context.Context, viewer ViewerRef, query PermissionRolePageQuery) (PermissionRolePageDTO, error) {
	if err := requireOperationsPermission(viewer, PermissionPermissionsRead); err != nil {
		return PermissionRolePageDTO{}, err
	}
	if err := s.authorizePlatformOperation(ctx, viewer, PermissionPermissionsRead); err != nil {
		return PermissionRolePageDTO{}, err
	}
	if query.Limit <= 0 || query.Limit > 100 {
		return PermissionRolePageDTO{}, fmt.Errorf("%w: role page limit must be between 1 and 100", ErrInvalidRequest)
	}
	afterRoleID := strings.TrimSpace(query.AfterRoleID)
	roles := make([]RoleDefinitionDTO, 0, query.Limit+2)
	for _, role := range UserAssignableRoleDefinitions(BuiltInRoleDefinitions()) {
		if role.RoleID > afterRoleID {
			roles = append(roles, role)
		}
	}
	hasMore := false
	revision := int64(0)
	if s != nil && s.roles != nil {
		if store, ok := s.roles.(PermissionRoleReadStore); ok {
			page, err := store.ListRoleDefinitionsPage(ctx, afterRoleID, query.Limit+len(BuiltInRoleDefinitions()))
			if err != nil {
				return PermissionRolePageDTO{}, err
			}
			roles = append(roles, page.Items...)
			hasMore = page.HasMore
			revision, err = store.PermissionPolicyRevision(ctx)
			if err != nil {
				return PermissionRolePageDTO{}, err
			}
		} else {
			stored, err := s.roles.ListRoleDefinitions(ctx)
			if err != nil {
				return PermissionRolePageDTO{}, err
			}
			for _, role := range stored {
				if role.RoleID > afterRoleID {
					roles = append(roles, role)
				}
			}
		}
	}
	roles = UserAssignableRoleDefinitions(mergeRoleDefinitions(nil, roles))
	sort.SliceStable(roles, func(i, j int) bool { return roles[i].RoleID < roles[j].RoleID })
	if len(roles) > query.Limit {
		hasMore = true
		roles = roles[:query.Limit]
	}
	return PermissionRolePageDTO{Items: roles, HasMore: hasMore, Revision: revision}, nil
}

func (s *QueryService) GetPermissionRole(ctx context.Context, viewer ViewerRef, roleID string) (RoleDefinitionDTO, error) {
	if err := requireOperationsPermission(viewer, PermissionPermissionsRead); err != nil {
		return RoleDefinitionDTO{}, err
	}
	if err := s.authorizePlatformOperation(ctx, viewer, PermissionPermissionsRead); err != nil {
		return RoleDefinitionDTO{}, err
	}
	roleID = strings.TrimSpace(roleID)
	if builtIn, ok := builtInRoleDefinition(roleID); ok {
		for _, visible := range UserAssignableRoleDefinitions([]RoleDefinitionDTO{builtIn}) {
			return visible, nil
		}
		return RoleDefinitionDTO{}, ErrNotFound
	}
	if s == nil || s.roles == nil {
		return RoleDefinitionDTO{}, ErrServiceDependency
	}
	if store, ok := s.roles.(PermissionRoleReadStore); ok {
		role, found, err := store.GetRoleDefinition(ctx, roleID)
		if err != nil {
			return RoleDefinitionDTO{}, err
		}
		if !found {
			return RoleDefinitionDTO{}, ErrNotFound
		}
		return role, nil
	}
	roles, err := s.roles.ListRoleDefinitions(ctx)
	if err != nil {
		return RoleDefinitionDTO{}, err
	}
	for _, role := range roles {
		if role.RoleID == roleID {
			return role, nil
		}
	}
	return RoleDefinitionDTO{}, ErrNotFound
}

func (s *QueryService) ListPermissionEffectiveGrants(ctx context.Context, viewer ViewerRef, query PermissionEffectiveGrantQuery) (PermissionEffectiveGrantPageDTO, error) {
	if err := requireOperationsPermission(viewer, PermissionPermissionsRead); err != nil {
		return PermissionEffectiveGrantPageDTO{}, err
	}
	if err := s.authorizePlatformOperation(ctx, viewer, PermissionPermissionsRead); err != nil {
		return PermissionEffectiveGrantPageDTO{}, err
	}
	if s == nil || s.roles == nil {
		return PermissionEffectiveGrantPageDTO{}, ErrServiceDependency
	}
	if query.Limit <= 0 || query.Limit > 100 || query.ScopeOffset < 0 {
		return PermissionEffectiveGrantPageDTO{}, fmt.Errorf("%w: effective grant page is invalid", ErrInvalidRequest)
	}
	store, ok := s.roles.(PermissionRoleAssignmentReadStore)
	if !ok {
		return s.listPermissionEffectiveGrantsFallback(ctx, query)
	}
	result := PermissionEffectiveGrantPageDTO{Items: make([]PermissionEffectiveGrantDTO, 0, query.Limit)}
	roleCache := map[string]RoleDefinitionDTO{}
	appendAssignment := func(assignment RoleAssignmentDTO, offset int, moreAssignments bool) (bool, error) {
		grants, err := s.permissionEffectiveGrantsForAssignment(ctx, assignment, roleCache)
		if err != nil {
			return false, err
		}
		if offset > len(grants) {
			return false, fmt.Errorf("%w: effective grant cursor is invalid", ErrInvalidRequest)
		}
		for index := offset; index < len(grants); index++ {
			result.Items = append(result.Items, grants[index])
			if len(result.Items) == query.Limit {
				nextOffset := index + 1
				if nextOffset < len(grants) || moreAssignments {
					result.NextAssignmentID = assignment.AssignmentID
					result.NextScopeOffset = nextOffset
					result.HasMore = true
				}
				return true, nil
			}
		}
		return false, nil
	}
	afterAssignmentID := strings.TrimSpace(query.AssignmentID)
	if afterAssignmentID != "" {
		assignment, found, err := store.GetRoleAssignment(ctx, afterAssignmentID)
		if err != nil {
			return PermissionEffectiveGrantPageDTO{}, err
		}
		if !found {
			return PermissionEffectiveGrantPageDTO{}, fmt.Errorf("%w: effective grant cursor assignment was not found", ErrInvalidRequest)
		}
		peek, err := store.ListRoleAssignmentsPage(ctx, afterAssignmentID, 1)
		if err != nil {
			return PermissionEffectiveGrantPageDTO{}, err
		}
		if done, err := appendAssignment(assignment, query.ScopeOffset, len(peek.Items) > 0); err != nil || done {
			return result, err
		}
	}
	for {
		page, err := store.ListRoleAssignmentsPage(ctx, afterAssignmentID, max(query.Limit, defaultRoleAssignmentPermissionPageSize))
		if err != nil {
			return PermissionEffectiveGrantPageDTO{}, err
		}
		for index, assignment := range page.Items {
			moreAssignments := index+1 < len(page.Items) || page.HasMore
			if done, err := appendAssignment(assignment, 0, moreAssignments); err != nil || done {
				return result, err
			}
			afterAssignmentID = assignment.AssignmentID
		}
		if !page.HasMore || len(page.Items) == 0 {
			return result, nil
		}
	}
}

const defaultRoleAssignmentPermissionPageSize = 20

func (s *QueryService) listPermissionEffectiveGrantsFallback(ctx context.Context, query PermissionEffectiveGrantQuery) (PermissionEffectiveGrantPageDTO, error) {
	assignments, err := s.roles.ListRoleAssignments(ctx, RoleAssignmentQuery{})
	if err != nil {
		return PermissionEffectiveGrantPageDTO{}, err
	}
	sort.SliceStable(assignments, func(i, j int) bool { return assignments[i].AssignmentID < assignments[j].AssignmentID })
	roleCache := map[string]RoleDefinitionDTO{}
	all := make([]PermissionEffectiveGrantDTO, 0)
	for _, assignment := range assignments {
		grants, err := s.permissionEffectiveGrantsForAssignment(ctx, assignment, roleCache)
		if err != nil {
			return PermissionEffectiveGrantPageDTO{}, err
		}
		all = append(all, grants...)
	}
	start := 0
	if query.AssignmentID != "" {
		for index, grant := range all {
			if grant.SourceID == query.AssignmentID {
				start = index + query.ScopeOffset
				break
			}
		}
	}
	end := min(len(all), start+query.Limit)
	return PermissionEffectiveGrantPageDTO{Items: all[start:end], HasMore: end < len(all)}, nil
}

func (s *QueryService) permissionEffectiveGrantsForAssignment(ctx context.Context, assignment RoleAssignmentDTO, roleCache map[string]RoleDefinitionDTO) ([]PermissionEffectiveGrantDTO, error) {
	now := time.Now().UTC()
	if (!assignment.StartsAt.IsZero() && assignment.StartsAt.After(now)) || (!assignment.ExpiresAt.IsZero() && !assignment.ExpiresAt.After(now)) {
		return nil, nil
	}
	role, ok := roleCache[assignment.RoleID]
	if !ok {
		if builtIn, found := builtInRoleDefinition(assignment.RoleID); found {
			role = builtIn
		} else if store, supported := s.roles.(PermissionRoleReadStore); supported {
			stored, found, err := store.GetRoleDefinition(ctx, assignment.RoleID)
			if err != nil {
				return nil, err
			}
			if !found {
				return nil, nil
			}
			role = stored
		} else {
			roles, err := s.roles.ListRoleDefinitions(ctx)
			if err != nil {
				return nil, err
			}
			for _, candidate := range roles {
				if candidate.RoleID == assignment.RoleID {
					role = candidate
					break
				}
			}
		}
		roleCache[assignment.RoleID] = role
	}
	if role.RoleID == "" || !strings.EqualFold(role.Status, organizationStatusActive) {
		return nil, nil
	}
	scopes := canonicalPermissionScopes(role.PermissionRefs)
	sort.Strings(scopes)
	kind := strings.ToLower(strings.TrimSpace(assignment.Actor.ActorType))
	if kind == "human" {
		kind = "user"
	}
	if kind == "ai" {
		kind = "agent"
	}
	if kind != "user" && kind != "agent" {
		return nil, nil
	}
	items := make([]PermissionEffectiveGrantDTO, 0, len(scopes))
	for _, scope := range scopes {
		items = append(items, PermissionEffectiveGrantDTO{PrincipalKind: kind, PrincipalID: assignment.Actor.ActorID, Scope: scope, Source: "role", SourceID: role.RoleID})
	}
	return items, nil
}

func (s *QueryService) allRoleDefinitions(ctx context.Context) ([]RoleDefinitionDTO, error) {
	roles := BuiltInRoleDefinitions()
	if s != nil && s.roles != nil {
		stored, err := s.roles.ListRoleDefinitions(ctx)
		if err != nil {
			return nil, err
		}
		roles = mergeRoleDefinitions(roles, stored)
	}
	return roles, nil
}

func (s *QueryService) ListRoleAssignments(ctx context.Context, viewer ViewerRef, query RoleAssignmentQuery) (RoleAssignmentPageDTO, error) {
	if err := requireOperationsPermission(viewer, PermissionRolesRead); err != nil {
		return RoleAssignmentPageDTO{}, err
	}
	if s == nil || s.roles == nil {
		return RoleAssignmentPageDTO{}, ErrServiceDependency
	}
	if err := s.authorizeRoleAssignmentRead(ctx, viewer, query); err != nil {
		return RoleAssignmentPageDTO{}, err
	}
	assignments, err := s.roles.ListRoleAssignments(ctx, query)
	if err != nil {
		return RoleAssignmentPageDTO{}, err
	}
	if query.EffectiveOnly {
		assignments = effectiveRoleAssignments(assignments, query.OrganizationCode, time.Now().UTC())
	}
	return RoleAssignmentPageDTO{Items: assignments, Page: PageDTO{Total: len(assignments), Returned: len(assignments)}}, nil
}

func mergeRoleDefinitions(base, overrides []RoleDefinitionDTO) []RoleDefinitionDTO {
	byID := map[string]RoleDefinitionDTO{}
	order := make([]string, 0, len(base)+len(overrides))
	for _, role := range base {
		role.PermissionRefs = canonicalPermissionScopes(role.PermissionRefs)
		if role.RoleID == "" {
			continue
		}
		if _, ok := byID[role.RoleID]; !ok {
			order = append(order, role.RoleID)
		}
		byID[role.RoleID] = role
	}
	for _, role := range overrides {
		role.PermissionRefs = canonicalPermissionScopes(role.PermissionRefs)
		if role.RoleID == "" {
			continue
		}
		if existing, ok := byID[role.RoleID]; ok && existing.BuiltIn {
			continue
		}
		if _, ok := byID[role.RoleID]; !ok {
			order = append(order, role.RoleID)
		}
		byID[role.RoleID] = role
	}
	out := make([]RoleDefinitionDTO, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out
}

func effectiveRoleAssignments(assignments []RoleAssignmentDTO, targetCode string, now time.Time) []RoleAssignmentDTO {
	targetCode = normalizeOrganizationCodeForQuery(targetCode)
	out := make([]RoleAssignmentDTO, 0, len(assignments))
	for _, assignment := range assignments {
		if roleScopeIsPlatform(assignment.ScopeMode) {
			continue
		}
		if !assignment.ExpiresAt.IsZero() && !assignment.ExpiresAt.After(now) {
			continue
		}
		if !assignment.StartsAt.IsZero() && assignment.StartsAt.After(now) {
			continue
		}
		if targetCode != "" && !roleAssignmentAppliesToCode(assignment, targetCode) {
			continue
		}
		if targetCode != "" && normalizeOrganizationCodeForQuery(assignment.OrganizationCode) != targetCode {
			assignment.Source = RoleSourceInherited
		}
		out = append(out, assignment)
	}
	return out
}
