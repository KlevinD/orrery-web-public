package operations

import (
	"context"
	"errors"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

func (s *CommandService) SetRoleStore(store RoleStorePort) {
	if s != nil {
		s.roles = store
	}
}

type roleAssignmentMutation struct {
	assignment             RoleAssignmentDTO
	previous               RoleAssignmentDTO
	organizationID         string
	previousOrganizationID string
}

type PermissionRoleBatchStore interface {
	ApplyPermissionRoleDefinitions(ctx context.Context, expectedRevision int64, roles []RoleDefinitionDTO) (int64, error)
}

func (s *CommandService) PutRoleDefinitions(ctx context.Context, viewer ViewerRef, cmd PutRoleDefinitionsCommand) (RoleDefinitionPageDTO, error) {
	if err := requireOperationsPermission(viewer, PermissionRolesManage); err != nil {
		return RoleDefinitionPageDTO{}, err
	}
	if s == nil || s.roles == nil {
		return RoleDefinitionPageDTO{}, ErrServiceDependency
	}
	if err := s.authorizeOperationObject(ctx, viewer, PermissionRolesManage, "platform", "global", "global"); err != nil {
		return RoleDefinitionPageDTO{}, err
	}
	items := make([]RoleDefinitionDTO, 0, len(cmd.Roles))
	seen := map[string]struct{}{}
	for _, role := range cmd.Roles {
		role = normalizeRoleDefinition(role)
		if _, builtIn := builtInRoleDefinition(role.RoleID); builtIn {
			return RoleDefinitionPageDTO{}, fmt.Errorf("%w: built-in role %s cannot be overridden", ErrInvalidRequest, role.RoleID)
		}
		if role.BuiltIn {
			return RoleDefinitionPageDTO{}, fmt.Errorf("%w: built_in is managed by the server", ErrInvalidRequest)
		}
		if err := validateRoleDefinition(role); err != nil {
			return RoleDefinitionPageDTO{}, err
		}
		if _, ok := seen[role.RoleID]; ok {
			return RoleDefinitionPageDTO{}, fmt.Errorf("%w: duplicate role_id %s", ErrInvalidRequest, role.RoleID)
		}
		seen[role.RoleID] = struct{}{}
		items = append(items, role)
	}
	if batch, ok := s.roles.(RoleDefinitionBatchStore); ok {
		if err := batch.SaveRoleDefinitions(ctx, items); err != nil {
			return RoleDefinitionPageDTO{}, err
		}
	} else {
		if len(items) > 1 {
			return RoleDefinitionPageDTO{}, fmt.Errorf("%w: role store does not support atomic batch updates", ErrServiceDependency)
		}
		if len(items) == 1 {
			if err := s.roles.SaveRoleDefinition(ctx, items[0]); err != nil {
				return RoleDefinitionPageDTO{}, err
			}
		}
	}
	return RoleDefinitionPageDTO{Items: items, Page: PageDTO{Total: len(items), Returned: len(items)}}, nil
}

func (s *CommandService) ApplyPermissionRoleDefinitions(ctx context.Context, viewer ViewerRef, cmd ApplyPermissionRoleDefinitionsCommand) (ApplyPermissionRoleDefinitionsResult, error) {
	if err := requireOperationsPermission(viewer, PermissionPermissionsManage); err != nil {
		return ApplyPermissionRoleDefinitionsResult{}, err
	}
	if s == nil || s.roles == nil {
		return ApplyPermissionRoleDefinitionsResult{}, ErrServiceDependency
	}
	if err := s.authorizePlatformOperation(ctx, viewer, PermissionPermissionsManage); err != nil {
		return ApplyPermissionRoleDefinitionsResult{}, err
	}
	items := make([]RoleDefinitionDTO, 0, len(cmd.Roles))
	seen := map[string]struct{}{}
	for _, role := range cmd.Roles {
		role = normalizeRoleDefinition(role)
		if _, builtIn := builtInRoleDefinition(role.RoleID); builtIn || role.BuiltIn {
			return ApplyPermissionRoleDefinitionsResult{}, fmt.Errorf("%w: built-in role %s cannot be changed", ErrInvalidRequest, role.RoleID)
		}
		if err := validateRoleDefinition(role); err != nil {
			return ApplyPermissionRoleDefinitionsResult{}, err
		}
		if _, ok := seen[role.RoleID]; ok {
			return ApplyPermissionRoleDefinitionsResult{}, fmt.Errorf("%w: duplicate role_id %s", ErrInvalidRequest, role.RoleID)
		}
		seen[role.RoleID] = struct{}{}
		items = append(items, role)
	}
	batch, ok := s.roles.(PermissionRoleBatchStore)
	if !ok {
		return ApplyPermissionRoleDefinitionsResult{}, fmt.Errorf("%w: role store does not support revisioned permission batches", ErrServiceDependency)
	}
	revision, err := batch.ApplyPermissionRoleDefinitions(ctx, cmd.ExpectedRevision, items)
	if err != nil {
		return ApplyPermissionRoleDefinitionsResult{}, err
	}
	return ApplyPermissionRoleDefinitionsResult{Items: items, Revision: revision}, nil
}

func (s *CommandService) PutRoleAssignments(ctx context.Context, viewer ViewerRef, cmd PutRoleAssignmentsCommand) (RoleAssignmentPageDTO, error) {
	if err := requireOperationsPermission(viewer, PermissionRolesManage); err != nil {
		return RoleAssignmentPageDTO{}, err
	}
	if s == nil || s.roles == nil {
		return RoleAssignmentPageDTO{}, ErrServiceDependency
	}
	storedRoles, err := s.roles.ListRoleDefinitions(ctx)
	if err != nil {
		return RoleAssignmentPageDTO{}, err
	}
	roleDefinitions := mergeRoleDefinitions(BuiltInRoleDefinitions(), storedRoles)
	mutations := make([]roleAssignmentMutation, 0, len(cmd.Assignments))
	seenAssignmentIDs := map[string]struct{}{}
	for _, assignment := range cmd.Assignments {
		assignment = normalizeRoleAssignment(assignment, viewer)
		if err := validateRoleAssignment(assignment); err != nil {
			return RoleAssignmentPageDTO{}, err
		}
		if err := validateRoleAssignmentDefinition(assignment, roleDefinitions); err != nil {
			return RoleAssignmentPageDTO{}, err
		}
		if _, exists := seenAssignmentIDs[assignment.AssignmentID]; exists {
			return RoleAssignmentPageDTO{}, fmt.Errorf("%w: duplicate assignment_id %s", ErrInvalidRequest, assignment.AssignmentID)
		}
		seenAssignmentIDs[assignment.AssignmentID] = struct{}{}
		organizationID, objectType, objectID, err := s.roleAssignmentAuthorizationTarget(ctx, assignment)
		if err != nil {
			return RoleAssignmentPageDTO{}, err
		}
		if err := s.validateRoleAssignmentActor(ctx, assignment, organizationID); err != nil {
			return RoleAssignmentPageDTO{}, err
		}
		if err := s.authorizeOperationObject(ctx, viewer, authorizationActionRoleManage, objectType, objectID, organizationID); err != nil {
			return RoleAssignmentPageDTO{}, err
		}
		previous, err := s.roleAssignmentByID(ctx, assignment.AssignmentID)
		if err != nil {
			return RoleAssignmentPageDTO{}, err
		}
		previousOrganizationID := ""
		previousObjectType := ""
		previousObjectID := ""
		if previous.AssignmentID != "" {
			if previous.Source == RoleSourceSystem {
				return RoleAssignmentPageDTO{}, fmt.Errorf("%w: system role assignments must be changed through their owning organization operation", ErrInvalidRequest)
			}
			previousOrganizationID, previousObjectType, previousObjectID, err = s.roleAssignmentAuthorizationTarget(ctx, previous)
			if err != nil {
				return RoleAssignmentPageDTO{}, err
			}
			if err := s.authorizeOperationObject(ctx, viewer, authorizationActionRoleManage, previousObjectType, previousObjectID, previousOrganizationID); err != nil {
				return RoleAssignmentPageDTO{}, err
			}
		}
		mutations = append(mutations, roleAssignmentMutation{
			assignment:             assignment,
			previous:               previous,
			organizationID:         organizationID,
			previousOrganizationID: previousOrganizationID,
		})
	}
	items := make([]RoleAssignmentDTO, 0, len(mutations))
	applied := make([]roleAssignmentMutation, 0, len(mutations))
	for _, mutation := range mutations {
		if err := s.roles.SaveRoleAssignment(ctx, mutation.assignment); err != nil {
			return RoleAssignmentPageDTO{}, errors.Join(err, s.rollbackRoleAssignmentMutations(ctx, applied))
		}
		if mutation.previous.AssignmentID != "" {
			if err := s.deleteRoleAssignmentRelation(ctx, mutation.previous, mutation.previousOrganizationID); err != nil {
				return RoleAssignmentPageDTO{}, errors.Join(err, s.rollbackRoleAssignmentMutations(ctx, append(applied, mutation)))
			}
		}
		if err := s.writeRoleAssignmentRelation(ctx, mutation.assignment, mutation.organizationID); err != nil {
			return RoleAssignmentPageDTO{}, errors.Join(err, s.rollbackRoleAssignmentMutations(ctx, append(applied, mutation)))
		}
		applied = append(applied, mutation)
		items = append(items, mutation.assignment)
	}
	return RoleAssignmentPageDTO{Items: items, Page: PageDTO{Total: len(items), Returned: len(items)}}, nil
}

func (s *CommandService) rollbackRoleAssignmentMutations(ctx context.Context, mutations []roleAssignmentMutation) error {
	var rollbackErr error
	for i := len(mutations) - 1; i >= 0; i-- {
		mutation := mutations[i]
		rollbackErr = errors.Join(rollbackErr, s.rollbackRoleAssignmentMutation(ctx, mutation.assignment, mutation.organizationID, mutation.previous, mutation.previousOrganizationID))
	}
	return rollbackErr
}

func (s *CommandService) rollbackRoleAssignmentMutation(ctx context.Context, assignment RoleAssignmentDTO, organizationID string, previous RoleAssignmentDTO, previousOrganizationID string) error {
	if s == nil || s.roles == nil {
		return ErrServiceDependency
	}
	rollbackErr := s.deleteRoleAssignmentRelation(ctx, assignment, organizationID)
	if previous.AssignmentID == "" {
		return errors.Join(rollbackErr, s.roles.DeleteRoleAssignment(ctx, assignment.AssignmentID))
	}
	rollbackErr = errors.Join(rollbackErr, s.roles.SaveRoleAssignment(ctx, previous))
	rollbackErr = errors.Join(rollbackErr, s.writeRoleAssignmentRelation(ctx, previous, previousOrganizationID))
	return rollbackErr
}

func (s *CommandService) DeleteRoleAssignment(ctx context.Context, viewer ViewerRef, assignmentID string) error {
	if err := requireOperationsPermission(viewer, PermissionRolesManage); err != nil {
		return err
	}
	if s == nil || s.roles == nil {
		return ErrServiceDependency
	}
	assignmentID = strings.TrimSpace(assignmentID)
	if assignmentID == "" {
		return fmt.Errorf("%w: assignment_id is required", ErrInvalidRequest)
	}
	assignment, err := s.roleAssignmentByID(ctx, assignmentID)
	if err != nil {
		return err
	}
	if assignment.AssignmentID == "" {
		return ErrNotFound
	}
	if assignment.Source == RoleSourceSystem {
		return fmt.Errorf("%w: system role assignments must be changed through their owning organization operation", ErrInvalidRequest)
	}
	organizationID, objectType, objectID, err := s.roleAssignmentAuthorizationTarget(ctx, assignment)
	if err != nil {
		return err
	}
	if err := s.authorizeOperationObject(ctx, viewer, authorizationActionRoleManage, objectType, objectID, organizationID); err != nil {
		return err
	}
	if err := s.roles.DeleteRoleAssignment(ctx, assignmentID); err != nil {
		return err
	}
	if err := s.deleteRoleAssignmentRelation(ctx, assignment, organizationID); err != nil {
		return errors.Join(err, s.roles.SaveRoleAssignment(ctx, assignment), s.writeRoleAssignmentRelation(ctx, assignment, organizationID))
	}
	return nil
}

func (s *CommandService) UpdateOrganizationOwner(ctx context.Context, viewer ViewerRef, cmd UpdateOrganizationOwnerCommand) (OrganizationNodeDTO, error) {
	if err := requireOperationsPermissionAny(viewer, PermissionOrgManage, PermissionRolesManage); err != nil {
		return OrganizationNodeDTO{}, err
	}
	if s == nil || s.orgs == nil || s.roles == nil {
		return OrganizationNodeDTO{}, ErrServiceDependency
	}
	graph, err := s.orgs.ListOrganizations(ctx, OrgGraphQuery{})
	if err != nil {
		return OrganizationNodeDTO{}, err
	}
	nodes, _ := buildOrganizationNodes(graph)
	node := findOrganizationNode(nodes, strings.TrimSpace(cmd.OrganizationNodeID))
	if node.ID == "" {
		return OrganizationNodeDTO{}, ErrNotFound
	}
	previousOwner := node.OwnerProfileID
	previousOwnerAssignments, err := s.roles.ListRoleAssignments(ctx, RoleAssignmentQuery{OrganizationCode: node.Code, RoleID: "org.owner"})
	if err != nil {
		return OrganizationNodeDTO{}, err
	}
	organizationID := organizationIDForNode(graph, node.ID)
	if strings.TrimSpace(organizationID) == "" {
		return OrganizationNodeDTO{}, fmt.Errorf("%w: organization node is not attached to an organization", ErrInvalidRequest)
	}
	ownerProfile, err := s.validateOrganizationOwnerProfile(ctx, cmd.OwnerProfileID, organizationID)
	if err != nil {
		return OrganizationNodeDTO{}, err
	}
	if strings.TrimSpace(cmd.OwnerProfileID) == "" {
		cmd.OwnerEmployeeNo = ""
		cmd.OwnerName = ""
	} else {
		cmd.OwnerEmployeeNo = strings.TrimSpace(ownerProfile.PersonSource.EmployeeNumber)
		cmd.OwnerName = firstNonEmpty(strings.TrimSpace(ownerProfile.DisplayName), strings.TrimSpace(ownerProfile.RealName))
	}
	authorizationAction := authorizationOwnerManageAction(viewer)
	authorizationObjectType := authorizationObjectDepartment
	authorizationObjectID := node.ID
	authorizationTenantID := organizationID
	if node.NodeType == organizationNodeTypeCompany {
		authorizationObjectType = authorizationObjectOrganization
		authorizationObjectID = organizationID
		authorizationTenantID = organizationID
	}
	if err := s.authorizeOperationObject(ctx, viewer, authorizationAction, authorizationObjectType, authorizationObjectID, authorizationTenantID); err != nil {
		return OrganizationNodeDTO{}, err
	}
	if node.NodeType == organizationNodeTypeCompany {
		for _, organization := range graph.Organizations {
			if organization.OrganizationID != node.ID {
				continue
			}
			organization.OwnerProfileID = strings.TrimSpace(cmd.OwnerProfileID)
			organization.OwnerEmployeeNo = strings.TrimSpace(cmd.OwnerEmployeeNo)
			organization.OwnerName = strings.TrimSpace(cmd.OwnerName)
			if err := s.orgs.SaveOrganization(ctx, organization); err != nil {
				return OrganizationNodeDTO{}, err
			}
			node.OwnerProfileID = organization.OwnerProfileID
			node.OwnerEmployeeNo = organization.OwnerEmployeeNo
			node.OwnerName = organization.OwnerName
			break
		}
	} else {
		for _, department := range graph.Departments {
			if department.DepartmentID != node.ID {
				continue
			}
			department.OwnerProfileID = strings.TrimSpace(cmd.OwnerProfileID)
			department.OwnerEmployeeNo = strings.TrimSpace(cmd.OwnerEmployeeNo)
			department.OwnerName = strings.TrimSpace(cmd.OwnerName)
			if err := s.orgs.SaveDepartment(ctx, department); err != nil {
				return OrganizationNodeDTO{}, err
			}
			node.OwnerProfileID = department.OwnerProfileID
			node.OwnerEmployeeNo = department.OwnerEmployeeNo
			node.OwnerName = department.OwnerName
			break
		}
	}
	if err := s.syncOrganizationOwnerRole(ctx, viewer, node, organizationID); err != nil {
		return OrganizationNodeDTO{}, errors.Join(err, s.restoreOrganizationOwnerState(ctx, graph, node, previousOwner, previousOwnerAssignments, organizationID))
	}
	if err := s.syncOrganizationOwnerRelation(ctx, node, previousOwner, organizationID); err != nil {
		return OrganizationNodeDTO{}, errors.Join(err, s.restoreOrganizationOwnerState(ctx, graph, node, previousOwner, previousOwnerAssignments, organizationID))
	}
	return node, nil
}

func (s *CommandService) validateOrganizationOwnerProfile(ctx context.Context, profileID string, organizationID string) (agentcore.AgentProfile, error) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return agentcore.AgentProfile{}, nil
	}
	if s == nil || s.profiles == nil {
		return agentcore.AgentProfile{}, ErrServiceDependency
	}
	profile, err := s.profiles.GetProfile(ctx, agentcore.AgentProfileID(profileID))
	if err != nil || profile.ID.IsZero() || profile.ID.String() != profileID || profile.AgentKind != agentcore.AgentKindHuman {
		return agentcore.AgentProfile{}, fmt.Errorf("%w: owner_profile_id must identify an existing human profile", ErrInvalidRequest)
	}
	if !profile.Enabled || !profile.Placement.IsInternal(profile.OrgIdentity) {
		return agentcore.AgentProfile{}, fmt.Errorf("%w: owner_profile_id must identify an enabled internal profile", ErrInvalidRequest)
	}
	if strings.TrimSpace(string(profile.OrgIdentity.OrganizationRef.ID)) != strings.TrimSpace(organizationID) {
		return agentcore.AgentProfile{}, fmt.Errorf("%w: owner_profile_id must belong to organization %s", ErrInvalidRequest, organizationID)
	}
	return profile, nil
}

func (s *CommandService) restoreOrganizationOwnerState(ctx context.Context, graph OrgGraphDTO, currentNode OrganizationNodeDTO, previousOwner string, previousAssignments []RoleAssignmentDTO, organizationID string) error {
	if s == nil || s.orgs == nil || s.roles == nil {
		return ErrServiceDependency
	}
	var rollbackErr error
	previousNode := currentNode
	previousNode.OwnerProfileID = strings.TrimSpace(previousOwner)
	if currentNode.NodeType == organizationNodeTypeCompany {
		for _, organization := range graph.Organizations {
			if organization.OrganizationID != currentNode.ID {
				continue
			}
			previousNode.OwnerEmployeeNo = organization.OwnerEmployeeNo
			previousNode.OwnerName = organization.OwnerName
			rollbackErr = errors.Join(rollbackErr, s.orgs.SaveOrganization(ctx, organization))
			break
		}
	} else {
		for _, department := range graph.Departments {
			if department.DepartmentID != currentNode.ID {
				continue
			}
			previousNode.OwnerEmployeeNo = department.OwnerEmployeeNo
			previousNode.OwnerName = department.OwnerName
			rollbackErr = errors.Join(rollbackErr, s.orgs.SaveDepartment(ctx, department))
			break
		}
	}
	currentAssignments, err := s.roles.ListRoleAssignments(ctx, RoleAssignmentQuery{OrganizationCode: currentNode.Code, RoleID: "org.owner"})
	rollbackErr = errors.Join(rollbackErr, err)
	for _, assignment := range currentAssignments {
		if assignment.Source != RoleSourceSystem || assignment.OrganizationNodeID != currentNode.ID {
			continue
		}
		rollbackErr = errors.Join(rollbackErr, s.deleteRoleAssignmentRelation(ctx, assignment, organizationID))
		rollbackErr = errors.Join(rollbackErr, s.roles.DeleteRoleAssignment(ctx, assignment.AssignmentID))
	}
	for _, assignment := range previousAssignments {
		if assignment.Source != RoleSourceSystem || assignment.OrganizationNodeID != currentNode.ID {
			continue
		}
		rollbackErr = errors.Join(rollbackErr, s.roles.SaveRoleAssignment(ctx, assignment))
		rollbackErr = errors.Join(rollbackErr, s.writeRoleAssignmentRelation(ctx, assignment, organizationID))
	}
	rollbackErr = errors.Join(rollbackErr, s.syncOrganizationOwnerRelation(ctx, previousNode, currentNode.OwnerProfileID, organizationID))
	return rollbackErr
}

func (s *CommandService) syncOrganizationOwnerRole(ctx context.Context, viewer ViewerRef, node OrganizationNodeDTO, organizationID string) error {
	existing, err := s.roles.ListRoleAssignments(ctx, RoleAssignmentQuery{OrganizationCode: node.Code, RoleID: "org.owner"})
	if err != nil {
		return err
	}
	for _, assignment := range existing {
		if assignment.Source == RoleSourceSystem && assignment.OrganizationNodeID == node.ID {
			if err := s.roles.DeleteRoleAssignment(ctx, assignment.AssignmentID); err != nil {
				return err
			}
			if err := s.deleteRoleAssignmentRelation(ctx, assignment, organizationID); err != nil {
				return err
			}
		}
	}
	if strings.TrimSpace(node.OwnerProfileID) == "" {
		return nil
	}
	assignment := RoleAssignmentDTO{
		AssignmentID:       "owner:" + node.ID,
		Actor:              ActorRefDTO{ActorType: "human", ActorID: node.OwnerProfileID},
		RoleID:             "org.owner",
		OrganizationNodeID: node.ID,
		OrganizationCode:   node.Code,
		ScopeMode:          RoleScopeSelfAndDescendants,
		Source:             RoleSourceSystem,
		AssignedBy:         viewer.ViewerID,
	}
	if err := s.roles.SaveRoleAssignment(ctx, assignment); err != nil {
		return err
	}
	return s.writeRoleAssignmentRelation(ctx, assignment, organizationID)
}

func normalizeRoleAssignment(assignment RoleAssignmentDTO, viewer ViewerRef) RoleAssignmentDTO {
	assignment.AssignmentID = strings.TrimSpace(assignment.AssignmentID)
	assignment.Actor.ActorType = canonicalRoleActorType(assignment.Actor.ActorType)
	assignment.Actor.ActorID = strings.TrimSpace(assignment.Actor.ActorID)
	assignment.RoleID = strings.ToLower(strings.TrimSpace(assignment.RoleID))
	assignment.OrganizationNodeID = strings.TrimSpace(assignment.OrganizationNodeID)
	assignment.OrganizationCode = normalizeOrganizationCodeForQuery(assignment.OrganizationCode)
	assignment.ScopeMode = strings.ToLower(strings.TrimSpace(firstNonEmpty(assignment.ScopeMode, RoleScopeSelf)))
	assignment.Source = strings.ToLower(strings.TrimSpace(firstNonEmpty(assignment.Source, RoleSourceDirect)))
	assignment.AssignedBy = strings.TrimSpace(viewer.ViewerID)
	if !assignment.StartsAt.IsZero() {
		assignment.StartsAt = assignment.StartsAt.UTC()
	}
	if !assignment.ExpiresAt.IsZero() {
		assignment.ExpiresAt = assignment.ExpiresAt.UTC()
	}
	if assignment.AssignmentID == "" {
		scopeID := assignment.OrganizationCode
		if roleScopeIsPlatform(assignment.ScopeMode) {
			scopeID = "global"
		}
		assignment.AssignmentID = strings.Join([]string{assignment.Actor.ActorType, assignment.Actor.ActorID, assignment.RoleID, scopeID, assignment.ScopeMode}, ":")
	}
	return assignment
}

func normalizeRoleDefinition(role RoleDefinitionDTO) RoleDefinitionDTO {
	role.RoleID = strings.ToLower(strings.TrimSpace(role.RoleID))
	role.Name = strings.TrimSpace(role.Name)
	role.Description = strings.TrimSpace(role.Description)
	role.Category = firstNonEmpty(strings.ToLower(strings.TrimSpace(role.Category)), "custom")
	role.Status = firstNonEmpty(strings.ToLower(strings.TrimSpace(role.Status)), organizationStatusActive)
	role.AssignableActorTypes = cleanPermissionScopes(role.AssignableActorTypes)
	if len(role.AssignableActorTypes) == 0 {
		role.AssignableActorTypes = []string{"human", "agent"}
	}
	role.PermissionRefs = canonicalPermissionScopes(role.PermissionRefs)
	return role
}

func validateRoleDefinition(role RoleDefinitionDTO) error {
	if role.RoleID == "" || role.Name == "" {
		return fmt.Errorf("%w: role_id and name are required", ErrInvalidRequest)
	}
	switch role.Status {
	case organizationStatusActive, "inactive", "disabled":
	default:
		return fmt.Errorf("%w: unsupported role status", ErrInvalidRequest)
	}
	for _, actorType := range role.AssignableActorTypes {
		switch strings.ToLower(strings.TrimSpace(actorType)) {
		case "human", "user", "agent", "ai":
		default:
			return fmt.Errorf("%w: unsupported assignable actor type", ErrInvalidRequest)
		}
	}
	return nil
}

func validateRoleAssignment(assignment RoleAssignmentDTO) error {
	if assignment.Actor.ActorType == "" || assignment.Actor.ActorID == "" || assignment.RoleID == "" {
		return fmt.Errorf("%w: actor and role_id are required", ErrInvalidRequest)
	}
	switch assignment.ScopeMode {
	case RoleScopePlatform:
		if assignment.OrganizationNodeID != "" || assignment.OrganizationCode != "" {
			return fmt.Errorf("%w: platform role assignments cannot specify organization scope", ErrInvalidRequest)
		}
	case RoleScopeSelf, RoleScopeDescendants, RoleScopeSelfAndDescendants:
		if assignment.OrganizationNodeID == "" || assignment.OrganizationCode == "" {
			return fmt.Errorf("%w: organization_node_id and organization_code are required", ErrInvalidRequest)
		}
	default:
		return fmt.Errorf("%w: unsupported role assignment scope_mode", ErrInvalidRequest)
	}
	if assignment.Source != RoleSourceDirect {
		return fmt.Errorf("%w: role assignment source is managed by the server", ErrInvalidRequest)
	}
	if !assignment.StartsAt.IsZero() && !assignment.ExpiresAt.IsZero() && !assignment.ExpiresAt.After(assignment.StartsAt) {
		return fmt.Errorf("%w: expires_at must be after starts_at", ErrInvalidRequest)
	}
	return nil
}

func validateRoleAssignmentDefinition(assignment RoleAssignmentDTO, roles []RoleDefinitionDTO) error {
	for _, role := range roles {
		if strings.TrimSpace(role.RoleID) != assignment.RoleID {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(role.Status), organizationStatusActive) {
			return fmt.Errorf("%w: role_id is not active", ErrInvalidRequest)
		}
		if roleRequiresPlatformAssignment(role) != roleScopeIsPlatform(assignment.ScopeMode) {
			return fmt.Errorf("%w: role_id %s requires %s scope", ErrInvalidRequest, assignment.RoleID, map[bool]string{true: RoleScopePlatform, false: "organization"}[roleRequiresPlatformAssignment(role)])
		}
		for _, actorType := range role.AssignableActorTypes {
			if canonicalRoleActorType(actorType) == assignment.Actor.ActorType {
				return nil
			}
		}
		return fmt.Errorf("%w: role_id does not support actor type %s", ErrInvalidRequest, assignment.Actor.ActorType)
	}
	return fmt.Errorf("%w: role_id does not exist", ErrInvalidRequest)
}

func canonicalRoleActorType(actorType string) string {
	switch strings.ToLower(strings.TrimSpace(actorType)) {
	case "human", "user":
		return "human"
	case "agent", "ai":
		return "agent"
	default:
		return ""
	}
}

func findOrganizationNode(nodes []OrganizationNodeDTO, id string) OrganizationNodeDTO {
	for _, node := range nodes {
		if node.ID == id {
			return node
		}
		if child := findOrganizationNode(node.Children, id); child.ID != "" {
			return child
		}
	}
	return OrganizationNodeDTO{}
}

func (s *CommandService) roleAssignmentByID(ctx context.Context, assignmentID string) (RoleAssignmentDTO, error) {
	if s == nil || s.roles == nil {
		return RoleAssignmentDTO{}, nil
	}
	assignments, err := s.roles.ListRoleAssignments(ctx, RoleAssignmentQuery{})
	if err != nil {
		return RoleAssignmentDTO{}, err
	}
	for _, assignment := range assignments {
		if assignment.AssignmentID == assignmentID {
			return assignment, nil
		}
	}
	return RoleAssignmentDTO{}, nil
}

func (s *CommandService) organizationIDForRoleAssignment(ctx context.Context, assignment RoleAssignmentDTO) (string, error) {
	if s == nil {
		return "", nil
	}
	if roleScopeIsPlatform(assignment.ScopeMode) {
		return "global", nil
	}
	if s.orgs == nil {
		if s.authorizationRelations == nil && s.authorizationChecker == nil {
			return "", nil
		}
		return "", ErrServiceDependency
	}
	graph, err := s.orgs.ListOrganizations(ctx, OrgGraphQuery{})
	if err != nil {
		return "", err
	}
	return organizationIDForNode(graph, assignment.OrganizationNodeID), nil
}

func (s *CommandService) roleAssignmentAuthorizationTarget(ctx context.Context, assignment RoleAssignmentDTO) (string, string, string, error) {
	if roleScopeIsPlatform(assignment.ScopeMode) {
		return "global", "platform", "global", nil
	}
	if s == nil || s.orgs == nil {
		return "", "", "", ErrServiceDependency
	}
	graph, err := s.orgs.ListOrganizations(ctx, OrgGraphQuery{})
	if err != nil {
		return "", "", "", err
	}
	nodes, _ := buildOrganizationNodes(graph)
	node := findOrganizationNode(nodes, strings.TrimSpace(assignment.OrganizationNodeID))
	if node.ID == "" {
		return "", "", "", ErrNotFound
	}
	if normalizeOrganizationCodeForQuery(node.Code) != normalizeOrganizationCodeForQuery(assignment.OrganizationCode) {
		return "", "", "", fmt.Errorf("%w: organization_node_id and organization_code must identify the same organization node", ErrInvalidRequest)
	}
	organizationID := organizationIDForNode(graph, node.ID)
	if strings.TrimSpace(organizationID) == "" {
		return "", "", "", fmt.Errorf("%w: organization node is not attached to an organization", ErrInvalidRequest)
	}
	if node.NodeType == organizationNodeTypeCompany {
		return organizationID, authorizationObjectOrganization, organizationID, nil
	}
	return organizationID, authorizationObjectDepartment, node.ID, nil
}

func (s *CommandService) validateRoleAssignmentActor(ctx context.Context, assignment RoleAssignmentDTO, organizationID string) error {
	if s == nil || s.profiles == nil {
		return ErrServiceDependency
	}
	profile, err := s.profiles.GetProfile(ctx, agentcore.AgentProfileID(assignment.Actor.ActorID))
	if err != nil || profile.ID.IsZero() || profile.ID.String() != assignment.Actor.ActorID || profile.IsPersonalAssistant() {
		return fmt.Errorf("%w: role assignment actor %s does not exist", ErrInvalidRequest, assignment.Actor.ActorID)
	}
	if !profile.Enabled || !profile.Placement.IsInternal(profile.OrgIdentity) {
		return fmt.Errorf("%w: role assignment actor %s must be an enabled internal profile", ErrInvalidRequest, assignment.Actor.ActorID)
	}
	switch assignment.Actor.ActorType {
	case "human":
		if profile.AgentKind != agentcore.AgentKindHuman {
			return fmt.Errorf("%w: actor %s is not a human profile", ErrInvalidRequest, assignment.Actor.ActorID)
		}
	case "agent":
		if profile.AgentKind == agentcore.AgentKindHuman {
			return fmt.Errorf("%w: actor %s is not an agent profile", ErrInvalidRequest, assignment.Actor.ActorID)
		}
	default:
		return fmt.Errorf("%w: unsupported role actor type", ErrInvalidRequest)
	}
	if !roleScopeIsPlatform(assignment.ScopeMode) {
		actorOrganizationID := strings.TrimSpace(string(profile.OrgIdentity.OrganizationRef.ID))
		if actorOrganizationID == "" || actorOrganizationID != strings.TrimSpace(organizationID) {
			return fmt.Errorf("%w: role assignment actor %s must belong to organization %s", ErrInvalidRequest, assignment.Actor.ActorID, organizationID)
		}
	}
	return nil
}

func organizationIDForNode(graph OrgGraphDTO, nodeID string) string {
	for _, organization := range graph.Organizations {
		if organization.OrganizationID == nodeID {
			return organization.OrganizationID
		}
	}
	for _, department := range graph.Departments {
		if department.DepartmentID == nodeID {
			return department.OrganizationID
		}
	}
	return ""
}
