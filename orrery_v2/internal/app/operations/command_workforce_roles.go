package operations

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

const workforceRoleAssignmentPrefix = "workforce:"

type workforceRoleAssignmentState struct {
	assignment     RoleAssignmentDTO
	organizationID string
}

type workforceRoleAssignmentPlan struct {
	previous []workforceRoleAssignmentState
	desired  []workforceRoleAssignmentState
}

func (s *CommandService) planWorkforceRoleAssignments(
	ctx context.Context,
	previous *agentcore.AgentProfile,
	current agentcore.AgentProfile,
	assignedBy string,
	strict bool,
) (*workforceRoleAssignmentPlan, error) {
	if s == nil || s.roles == nil {
		return nil, nil
	}
	profileID := strings.TrimSpace(current.ID.String())
	if profileID == "" {
		return nil, fmt.Errorf("%w: agent_profile_id is required", ErrInvalidRequest)
	}

	assignments, err := s.roles.ListRoleAssignments(ctx, RoleAssignmentQuery{})
	if err != nil {
		return nil, err
	}
	allByID := make(map[string]RoleAssignmentDTO, len(assignments))
	plan := &workforceRoleAssignmentPlan{}
	for _, assignment := range assignments {
		allByID[assignment.AssignmentID] = assignment
		if !isWorkforceManagedRoleAssignment(assignment, profileID) {
			continue
		}
		organizationID, resolveErr := s.organizationIDForRoleAssignment(ctx, assignment)
		if resolveErr != nil && strict {
			return nil, resolveErr
		}
		plan.previous = append(plan.previous, workforceRoleAssignmentState{
			assignment:     assignment,
			organizationID: organizationID,
		})
	}

	if !current.Placement.IsInternal(current.OrgIdentity) || len(current.OrgIdentity.RoleRefs) == 0 {
		sortWorkforceRoleAssignmentStates(plan.previous)
		return plan, nil
	}

	storedRoles, err := s.roles.ListRoleDefinitions(ctx)
	if err != nil {
		return nil, err
	}
	definitions := mergeRoleDefinitions(BuiltInRoleDefinitions(), storedRoles)
	definitionsByID := make(map[string]RoleDefinitionDTO, len(definitions))
	for _, role := range definitions {
		roleID := strings.ToLower(strings.TrimSpace(role.RoleID))
		if roleID != "" {
			definitionsByID[roleID] = role
		}
	}
	legacyRoleIDs := roleIDsFromOptionalProfile(previous)
	actorType := workforceRoleActorType(current)
	roleIDs := canonicalWorkforceRoleIDs(current)

	var departmentNode OrganizationNodeDTO
	departmentNodeLoaded := false
	loadDepartmentNode := func() (OrganizationNodeDTO, error) {
		if departmentNodeLoaded {
			return departmentNode, nil
		}
		departmentNodeLoaded = true
		if s.orgs == nil {
			return OrganizationNodeDTO{}, ErrServiceDependency
		}
		organizationID := strings.TrimSpace(string(current.OrgIdentity.OrganizationRef.ID))
		departmentID := strings.TrimSpace(string(current.OrgIdentity.DepartmentRef.ID))
		if organizationID == "" || departmentID == "" {
			return OrganizationNodeDTO{}, fmt.Errorf("%w: organization and department are required for organization role assignments", ErrInvalidRequest)
		}
		// Organization codes are allocated across the full graph. Loading a single
		// organization can produce a different synthetic code for legacy rows.
		graph, listErr := s.orgs.ListOrganizations(ctx, OrgGraphQuery{})
		if listErr != nil {
			return OrganizationNodeDTO{}, listErr
		}
		nodes, _ := buildOrganizationNodes(graph)
		departmentNode = findOrganizationNode(nodes, departmentID)
		if departmentNode.ID == "" || organizationIDForNode(graph, departmentNode.ID) != organizationID {
			return OrganizationNodeDTO{}, fmt.Errorf("%w: department is not attached to the workforce member organization", ErrInvalidRequest)
		}
		return departmentNode, nil
	}

	for _, roleID := range roleIDs {
		role, exists := definitionsByID[roleID]
		roleErr := validateWorkforceRoleDefinition(roleID, role, exists, actorType)
		if roleErr != nil {
			_, legacy := legacyRoleIDs[roleID]
			if strict && !legacy {
				return nil, roleErr
			}
			continue
		}

		assignment := RoleAssignmentDTO{
			AssignmentID: workforceRoleAssignmentID(profileID, roleID),
			Actor: ActorRefDTO{
				ActorType: actorType,
				ActorID:   profileID,
			},
			RoleID:     roleID,
			Source:     RoleSourceSystem,
			AssignedBy: strings.TrimSpace(assignedBy),
		}
		organizationID := strings.TrimSpace(string(current.OrgIdentity.OrganizationRef.ID))
		if roleRequiresPlatformAssignment(role) {
			assignment.ScopeMode = RoleScopePlatform
			organizationID = "global"
		} else {
			node, nodeErr := loadDepartmentNode()
			if nodeErr != nil {
				return nil, nodeErr
			}
			assignment.OrganizationNodeID = node.ID
			assignment.OrganizationCode = normalizeOrganizationCodeForQuery(node.Code)
			assignment.ScopeMode = RoleScopeSelf
		}
		if existing, collision := allByID[assignment.AssignmentID]; collision && !isWorkforceManagedRoleAssignment(existing, profileID) {
			return nil, fmt.Errorf("%w: role assignment id %s is reserved by another assignment", ErrInvalidRequest, assignment.AssignmentID)
		}
		plan.desired = append(plan.desired, workforceRoleAssignmentState{
			assignment:     assignment,
			organizationID: organizationID,
		})
	}

	sortWorkforceRoleAssignmentStates(plan.previous)
	sortWorkforceRoleAssignmentStates(plan.desired)
	return plan, nil
}

func validateWorkforceRoleDefinition(roleID string, role RoleDefinitionDTO, exists bool, actorType string) error {
	if roleID == "org.owner" {
		return fmt.Errorf("%w: org.owner must be assigned through organization owner management", ErrInvalidRequest)
	}
	if !exists {
		return fmt.Errorf("%w: role_id %s does not exist", ErrInvalidRequest, roleID)
	}
	if !strings.EqualFold(strings.TrimSpace(role.Status), organizationStatusActive) {
		return fmt.Errorf("%w: role_id %s is not active", ErrInvalidRequest, roleID)
	}
	for _, assignable := range role.AssignableActorTypes {
		if canonicalRoleActorType(assignable) == actorType {
			return nil
		}
	}
	return fmt.Errorf("%w: role_id %s does not support actor type %s", ErrInvalidRequest, roleID, actorType)
}

func (s *CommandService) authorizeWorkforceRoleChanges(ctx context.Context, viewer ViewerRef, previous *agentcore.AgentProfile, current agentcore.AgentProfile, plan *workforceRoleAssignmentPlan) error {
	removals, upserts := workforceRoleAssignmentPlanChanges(plan)
	roleRefsChanged := workforceRoleRefsChanged(previous, current)
	if !roleRefsChanged && len(removals) == 0 && len(upserts) == 0 {
		return nil
	}
	previousRoleIDs := roleIDsFromOptionalProfile(previous)
	if _, ownerSelected := roleIDsFromOptionalProfile(&current)["org.owner"]; ownerSelected {
		if _, alreadyOwned := previousRoleIDs["org.owner"]; !alreadyOwned {
			return fmt.Errorf("%w: org.owner must be assigned through organization owner management", ErrInvalidRequest)
		}
	}
	coarsePermissionErr := requireOperationsPermission(viewer, PermissionRolesManage)
	// Authoritative role scopes are injected per route, so a workforce route may
	// carry only workforce.manage. In that case the scoped role.manage checks below
	// remain the source of truth.
	if coarsePermissionErr != nil && (s == nil || s.authorizationChecker == nil || len(removals)+len(upserts) == 0) {
		return coarsePermissionErr
	}
	if plan == nil {
		return nil
	}
	seen := map[string]struct{}{}
	for _, states := range [][]workforceRoleAssignmentState{removals, upserts} {
		for _, state := range states {
			organizationID, objectType, objectID, err := s.roleAssignmentAuthorizationTarget(ctx, state.assignment)
			if err != nil {
				return err
			}
			key := strings.Join([]string{organizationID, objectType, objectID}, "\x00")
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			if err := s.authorizeOperationObject(ctx, viewer, authorizationActionRoleManage, objectType, objectID, organizationID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *CommandService) applyWorkforceRoleAssignmentPlan(ctx context.Context, plan *workforceRoleAssignmentPlan) error {
	if s == nil || s.roles == nil || plan == nil {
		return nil
	}
	removals, upserts := workforceRoleAssignmentPlanChanges(plan)
	for _, state := range removals {
		if err := s.deleteRoleAssignmentRelation(ctx, state.assignment, state.organizationID); err != nil {
			return errors.Join(err, s.restoreWorkforceRoleAssignmentPlan(ctx, plan))
		}
		if err := s.roles.DeleteRoleAssignment(ctx, state.assignment.AssignmentID); err != nil {
			return errors.Join(err, s.restoreWorkforceRoleAssignmentPlan(ctx, plan))
		}
	}
	for _, state := range upserts {
		if err := s.roles.SaveRoleAssignment(ctx, state.assignment); err != nil {
			return errors.Join(err, s.restoreWorkforceRoleAssignmentPlan(ctx, plan))
		}
		if err := s.writeRoleAssignmentRelation(ctx, state.assignment, state.organizationID); err != nil {
			return errors.Join(err, s.restoreWorkforceRoleAssignmentPlan(ctx, plan))
		}
	}
	return nil
}

func (s *CommandService) restoreWorkforceRoleAssignmentPlan(ctx context.Context, plan *workforceRoleAssignmentPlan) error {
	if s == nil || s.roles == nil || plan == nil {
		return nil
	}
	removals, upserts := workforceRoleAssignmentPlanChanges(plan)
	var rollbackErr error
	for i := len(upserts) - 1; i >= 0; i-- {
		state := upserts[i]
		rollbackErr = errors.Join(rollbackErr, s.deleteRoleAssignmentRelation(ctx, state.assignment, state.organizationID))
		rollbackErr = errors.Join(rollbackErr, s.roles.DeleteRoleAssignment(ctx, state.assignment.AssignmentID))
	}
	for _, state := range removals {
		rollbackErr = errors.Join(rollbackErr, s.roles.SaveRoleAssignment(ctx, state.assignment))
		rollbackErr = errors.Join(rollbackErr, s.writeRoleAssignmentRelation(ctx, state.assignment, state.organizationID))
	}
	return rollbackErr
}

func workforceRoleAssignmentPlanChanges(plan *workforceRoleAssignmentPlan) ([]workforceRoleAssignmentState, []workforceRoleAssignmentState) {
	if plan == nil {
		return nil, nil
	}
	previousByID := make(map[string]workforceRoleAssignmentState, len(plan.previous))
	desiredByID := make(map[string]workforceRoleAssignmentState, len(plan.desired))
	for _, state := range plan.previous {
		previousByID[state.assignment.AssignmentID] = state
	}
	for _, state := range plan.desired {
		desiredByID[state.assignment.AssignmentID] = state
	}
	removals := make([]workforceRoleAssignmentState, 0)
	upserts := make([]workforceRoleAssignmentState, 0)
	for assignmentID, previous := range previousByID {
		desired, exists := desiredByID[assignmentID]
		if !exists || !equivalentWorkforceRoleAssignment(previous, desired) {
			removals = append(removals, previous)
		}
	}
	for assignmentID, desired := range desiredByID {
		previous, exists := previousByID[assignmentID]
		if !exists || !equivalentWorkforceRoleAssignment(previous, desired) {
			upserts = append(upserts, desired)
		}
	}
	sortWorkforceRoleAssignmentStates(removals)
	sortWorkforceRoleAssignmentStates(upserts)
	return removals, upserts
}

func equivalentWorkforceRoleAssignment(left, right workforceRoleAssignmentState) bool {
	a := left.assignment
	b := right.assignment
	return a.AssignmentID == b.AssignmentID &&
		a.Actor == b.Actor &&
		a.RoleID == b.RoleID &&
		a.OrganizationNodeID == b.OrganizationNodeID &&
		normalizeOrganizationCodeForQuery(a.OrganizationCode) == normalizeOrganizationCodeForQuery(b.OrganizationCode) &&
		a.ScopeMode == b.ScopeMode &&
		a.Source == b.Source &&
		a.StartsAt.Equal(b.StartsAt) &&
		a.ExpiresAt.Equal(b.ExpiresAt) &&
		strings.TrimSpace(left.organizationID) == strings.TrimSpace(right.organizationID)
}

func workforceRoleRefsChanged(previous *agentcore.AgentProfile, current agentcore.AgentProfile) bool {
	previousIDs := roleIDsFromOptionalProfile(previous)
	currentIDs := roleIDsFromOptionalProfile(&current)
	if len(previousIDs) != len(currentIDs) {
		return true
	}
	for roleID := range previousIDs {
		if _, ok := currentIDs[roleID]; !ok {
			return true
		}
	}
	return false
}

func roleIDsFromOptionalProfile(profile *agentcore.AgentProfile) map[string]struct{} {
	out := map[string]struct{}{}
	if profile == nil {
		return out
	}
	for _, role := range profile.OrgIdentity.RoleRefs {
		roleID := strings.ToLower(strings.TrimSpace(string(role.ID)))
		if roleID != "" {
			out[roleID] = struct{}{}
		}
	}
	return out
}

func canonicalWorkforceRoleIDs(profile agentcore.AgentProfile) []string {
	set := roleIDsFromOptionalProfile(&profile)
	out := make([]string, 0, len(set))
	for roleID := range set {
		out = append(out, roleID)
	}
	sort.Strings(out)
	return out
}

func workforceRoleActorType(profile agentcore.AgentProfile) string {
	if profile.AgentKind == agentcore.AgentKindHuman {
		return "human"
	}
	return "agent"
}

func workforceRoleAssignmentID(profileID, roleID string) string {
	return workforceRoleAssignmentPrefix + strings.TrimSpace(profileID) + ":" + strings.ToLower(strings.TrimSpace(roleID))
}

func isWorkforceManagedRoleAssignment(assignment RoleAssignmentDTO, profileID string) bool {
	profileID = strings.TrimSpace(profileID)
	return assignment.Source == RoleSourceSystem &&
		strings.TrimSpace(assignment.Actor.ActorID) == profileID &&
		strings.HasPrefix(assignment.AssignmentID, workforceRoleAssignmentPrefix+profileID+":")
}

func sortWorkforceRoleAssignmentStates(states []workforceRoleAssignmentState) {
	sort.SliceStable(states, func(i, j int) bool {
		return states[i].assignment.AssignmentID < states[j].assignment.AssignmentID
	})
}

// ReconcileWorkforceRoleAssignments migrates profile role references into the
// authoritative role-assignment store and removes stale workforce-owned rows.
func (s *CommandService) ReconcileWorkforceRoleAssignments(ctx context.Context, assignedBy string) error {
	if s == nil || s.roles == nil || s.directory == nil || s.profiles == nil {
		return ErrServiceDependency
	}
	page, err := s.directory.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{
		IncludeAI:     true,
		IncludeHumans: true,
		Placement:     agentcore.TalentPlacementFilterAll,
	})
	if err != nil {
		return err
	}
	seenProfiles := make(map[string]struct{}, len(page.Entries)+len(page.Rejected))
	for _, rejected := range page.Rejected {
		seenProfiles[rejected.AgentProfileID.String()] = struct{}{}
	}
	for _, entry := range page.Entries {
		profile, getErr := s.profiles.GetProfile(ctx, entry.AgentProfileID)
		if getErr != nil {
			return getErr
		}
		seenProfiles[profile.ID.String()] = struct{}{}
		plan, planErr := s.planWorkforceRoleAssignments(ctx, &profile, profile, assignedBy, false)
		if planErr != nil {
			// A legacy profile may still reference an organization node while the
			// startup tenant migration is repairing its placement.
			if errors.Is(planErr, ErrInvalidRequest) {
				continue
			}
			return planErr
		}
		if applyErr := s.applyWorkforceRoleAssignmentPlan(ctx, plan); applyErr != nil {
			return applyErr
		}
	}

	assignments, err := s.roles.ListRoleAssignments(ctx, RoleAssignmentQuery{})
	if err != nil {
		return err
	}
	orphanPlan := &workforceRoleAssignmentPlan{}
	for _, assignment := range assignments {
		if !strings.HasPrefix(assignment.AssignmentID, workforceRoleAssignmentPrefix) || assignment.Source != RoleSourceSystem {
			continue
		}
		if _, exists := seenProfiles[strings.TrimSpace(assignment.Actor.ActorID)]; exists {
			continue
		}
		organizationID, _ := s.organizationIDForRoleAssignment(ctx, assignment)
		orphanPlan.previous = append(orphanPlan.previous, workforceRoleAssignmentState{
			assignment:     assignment,
			organizationID: organizationID,
		})
	}
	return s.applyWorkforceRoleAssignmentPlan(ctx, orphanPlan)
}
