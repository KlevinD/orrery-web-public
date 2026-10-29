package operations

import (
	"context"
	"errors"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	knowledgecore "orrery_v2/internal/knowledge/core"
)

func (s *CommandService) CreateOrganization(ctx context.Context, viewer ViewerRef, cmd CreateOrganizationCommand) (OrganizationDTO, error) {
	if err := requireOperationsPermission(viewer, PermissionOrgManage); err != nil {
		return OrganizationDTO{}, err
	}
	if s == nil || s.orgs == nil {
		return OrganizationDTO{}, ErrServiceDependency
	}
	name := strings.TrimSpace(cmd.Name)
	if name == "" {
		return OrganizationDTO{}, fmt.Errorf("%w: name is required", ErrInvalidRequest)
	}
	if err := s.authorizePlatformOperation(ctx, viewer, PermissionOrgManage); err != nil {
		return OrganizationDTO{}, err
	}
	organizationID, err := s.newID("org")
	if err != nil {
		return OrganizationDTO{}, err
	}
	graph, err := s.orgs.ListOrganizations(ctx, OrgGraphQuery{})
	if err != nil {
		return OrganizationDTO{}, err
	}
	graph = backfillOrganizationCodes(graph)
	rootCodes := make([]string, 0, len(graph.Organizations))
	for _, organization := range graph.Organizations {
		if organization.Code != "" {
			rootCodes = append(rootCodes, organization.Code)
		}
	}
	organizationCode, err := nextOrganizationCode("", rootCodes)
	if err != nil {
		return OrganizationDTO{}, err
	}
	dto := OrganizationDTO{
		OrganizationID: organizationID,
		Name:           name,
		Code:           organizationCode,
		DisplayCode:    displayOrganizationCode(organizationCode),
		Status:         organizationStatusActive,
		NodeType:       organizationNodeTypeCompany,
	}
	knowledgeBaseID, err := s.createLifecycleKnowledgeBase(ctx, viewer, knowledgecore.OwnerOrganization, organizationID, organizationKnowledgeBaseName(name), "组织基础知识库", knowledgecore.VisibilityScoped)
	if err != nil {
		return OrganizationDTO{}, err
	}
	dto.KnowledgeBaseID = knowledgeBaseID
	if err := s.orgs.SaveOrganization(ctx, dto); err != nil {
		return OrganizationDTO{}, s.cleanupLifecycleKnowledgeBaseOnError(ctx, viewer, knowledgecore.OwnerOrganization, organizationID, knowledgeBaseID, err)
	}
	if err := s.writeOrganizationCreatedRelation(ctx, viewer, dto); err != nil {
		return OrganizationDTO{}, err
	}
	if err := s.recordAudit(ctx, viewer, "organization.create", dto.OrganizationID, map[string]string{
		"organization_id": dto.OrganizationID,
		"name":            dto.Name,
	}); err != nil {
		return OrganizationDTO{}, err
	}
	return dto, nil
}

func (s *CommandService) CreateOrganizationUnit(ctx context.Context, viewer ViewerRef, cmd CreateOrganizationUnitCommand) (DepartmentDTO, error) {
	if err := requireOperationsPermission(viewer, PermissionOrgManage); err != nil {
		return DepartmentDTO{}, err
	}
	if s == nil || s.orgs == nil {
		return DepartmentDTO{}, ErrServiceDependency
	}
	organizationID := strings.TrimSpace(cmd.OrganizationID)
	name := strings.TrimSpace(cmd.Name)
	parentDepartmentID := strings.TrimSpace(cmd.ParentDepartmentID)
	defaultPositionName := strings.TrimSpace(cmd.DefaultPositionName)
	if organizationID == "" || name == "" {
		return DepartmentDTO{}, fmt.Errorf("%w: organization_id and name are required", ErrInvalidRequest)
	}
	if err := s.validateOrganizationUnitRefs(ctx, organizationID, parentDepartmentID); err != nil {
		return DepartmentDTO{}, err
	}
	if err := s.authorizeOperationObject(ctx, viewer, authorizationActionOrganizationManage, authorizationObjectOrganization, organizationID, organizationID); err != nil {
		return DepartmentDTO{}, err
	}
	graph, err := s.orgs.ListOrganizations(ctx, OrgGraphQuery{OrganizationID: organizationID})
	if err != nil {
		return DepartmentDTO{}, err
	}
	graph = backfillOrganizationCodes(graph)
	parentCode, siblingCodes, err := organizationUnitCodeContext(graph, organizationID, parentDepartmentID)
	if err != nil {
		return DepartmentDTO{}, err
	}
	unitCode, err := nextOrganizationCode(parentCode, siblingCodes)
	if err != nil {
		return DepartmentDTO{}, err
	}
	departmentID, err := s.newID("unit")
	if err != nil {
		return DepartmentDTO{}, err
	}
	defaultPositionID := ""
	if defaultPositionName != "" {
		defaultPositionID, err = s.newID("position")
		if err != nil {
			return DepartmentDTO{}, err
		}
	}
	unit := DepartmentDTO{
		DepartmentID:       departmentID,
		OrganizationID:     organizationID,
		Name:               name,
		ParentDepartmentID: parentDepartmentID,
		DefaultPositionID:  defaultPositionID,
		MemberCount:        0,
		Code:               unitCode,
		DisplayCode:        displayOrganizationCode(unitCode),
		Status:             organizationStatusActive,
		NodeType:           organizationNodeTypeDepartment,
	}
	if err := s.orgs.SaveDepartment(ctx, unit); err != nil {
		return DepartmentDTO{}, err
	}
	if err := s.writeDepartmentCreatedRelation(ctx, unit); err != nil {
		return DepartmentDTO{}, err
	}
	if defaultPositionName != "" {
		knowledgeBaseID, err := s.createLifecycleKnowledgeBase(ctx, viewer, knowledgecore.OwnerPosition, defaultPositionID, positionKnowledgeBaseName(defaultPositionName), "岗位基础知识库", knowledgecore.VisibilityScoped)
		if err != nil {
			return DepartmentDTO{}, err
		}
		defaultPosition := PositionDTO{
			PositionID:      defaultPositionID,
			DepartmentID:    departmentID,
			Name:            defaultPositionName,
			KnowledgeBaseID: knowledgeBaseID,
			MemberCount:     0,
		}
		if err := s.orgs.SavePosition(ctx, defaultPosition); err != nil {
			return DepartmentDTO{}, s.cleanupLifecycleKnowledgeBaseOnError(ctx, viewer, knowledgecore.OwnerPosition, defaultPositionID, knowledgeBaseID, err)
		}
	}
	return unit, nil
}

func (s *CommandService) CreateOrganizationPosition(ctx context.Context, viewer ViewerRef, cmd CreateOrganizationPositionCommand) (PositionDTO, error) {
	if err := requireOperationsPermission(viewer, PermissionOrgManage); err != nil {
		return PositionDTO{}, err
	}
	if s == nil || s.orgs == nil {
		return PositionDTO{}, ErrServiceDependency
	}
	organizationID := strings.TrimSpace(cmd.OrganizationID)
	departmentID := strings.TrimSpace(cmd.DepartmentID)
	name := strings.TrimSpace(cmd.Name)
	if organizationID == "" || name == "" {
		return PositionDTO{}, fmt.Errorf("%w: organization_id and name are required", ErrInvalidRequest)
	}
	departmentID, err := s.resolveOrganizationPositionDepartment(ctx, organizationID, departmentID)
	if err != nil {
		return PositionDTO{}, err
	}
	if err := s.authorizeOperationObject(ctx, viewer, authorizationActionDepartmentManage, authorizationObjectDepartment, departmentID, organizationID); err != nil {
		return PositionDTO{}, err
	}
	positionID, err := s.newID("position")
	if err != nil {
		return PositionDTO{}, err
	}
	position := PositionDTO{
		PositionID:   positionID,
		DepartmentID: departmentID,
		Name:         name,
		MemberCount:  0,
	}
	knowledgeBaseID, err := s.createLifecycleKnowledgeBase(ctx, viewer, knowledgecore.OwnerPosition, positionID, positionKnowledgeBaseName(name), "岗位基础知识库", knowledgecore.VisibilityScoped)
	if err != nil {
		return PositionDTO{}, err
	}
	position.KnowledgeBaseID = knowledgeBaseID
	if err := s.orgs.SavePosition(ctx, position); err != nil {
		return PositionDTO{}, s.cleanupLifecycleKnowledgeBaseOnError(ctx, viewer, knowledgecore.OwnerPosition, positionID, knowledgeBaseID, err)
	}
	if err := s.recordAudit(ctx, viewer, "organization.position.create", position.PositionID, map[string]string{
		"organization_id": organizationID,
		"department_id":   departmentID,
		"name":            name,
	}); err != nil {
		return PositionDTO{}, err
	}
	return position, nil
}

func (s *CommandService) DeleteOrganization(ctx context.Context, viewer ViewerRef, organizationID string) error {
	if err := requireOperationsPermission(viewer, PermissionOrgManage); err != nil {
		return err
	}
	if s == nil || s.orgs == nil || s.profiles == nil {
		return ErrServiceDependency
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return fmt.Errorf("%w: organization_id is required", ErrInvalidRequest)
	}
	graph, err := s.orgs.ListOrganizations(ctx, OrgGraphQuery{OrganizationID: organizationID})
	if err != nil {
		return err
	}
	var organization OrganizationDTO
	for _, candidate := range graph.Organizations {
		if candidate.OrganizationID == organizationID {
			organization = candidate
			break
		}
	}
	if organization.OrganizationID == "" {
		return ErrNotFound
	}
	if err := s.authorizeOperationObject(ctx, viewer, authorizationActionOrganizationManage, authorizationObjectOrganization, organizationID, organizationID); err != nil {
		return err
	}
	if err := s.ensureOrganizationHasNoInternalMembers(ctx, organizationID); err != nil {
		return err
	}
	rollbackSourceDeletion := func(context.Context) error { return nil }
	if s.sourceDeletion != nil {
		rollbackSourceDeletion, err = s.sourceDeletion.PrepareSourceDeletion(
			ctx, "organization", organizationID, []string{organizationID}, "organization.delete:"+viewer.ViewerID,
		)
		if err != nil {
			return err
		}
	}
	rollback := func(result error) error {
		return errors.Join(result, rollbackSourceDeletion(ctx))
	}
	positionsToDelete := make([]PositionDTO, 0, len(graph.Positions))
	for _, position := range graph.Positions {
		if strings.TrimSpace(position.PositionID) != "" {
			positionsToDelete = append(positionsToDelete, position)
		}
	}
	departmentsToDelete := make([]DepartmentDTO, 0, len(graph.Departments))
	for _, department := range graph.Departments {
		if strings.TrimSpace(department.DepartmentID) != "" {
			departmentsToDelete = append(departmentsToDelete, department)
		}
	}
	for _, position := range positionsToDelete {
		if err := s.deleteLifecycleKnowledgeBase(ctx, viewer, knowledgecore.OwnerPosition, position.PositionID, position.KnowledgeBaseID); err != nil {
			return rollback(err)
		}
	}
	if err := s.deleteLifecycleKnowledgeBase(ctx, viewer, knowledgecore.OwnerOrganization, organizationID, organization.KnowledgeBaseID); err != nil {
		return rollback(err)
	}
	for _, position := range positionsToDelete {
		if err := s.orgs.DeletePosition(ctx, position.PositionID); err != nil {
			return rollback(err)
		}
	}
	for _, department := range departmentsToDelete {
		if err := s.orgs.DeleteDepartment(ctx, department.DepartmentID); err != nil {
			return rollback(err)
		}
		if err := s.deleteDepartmentCreatedRelation(ctx, department); err != nil {
			return rollback(err)
		}
	}
	if err := s.orgs.DeleteOrganization(ctx, organizationID); err != nil {
		return rollback(err)
	}
	if s.authorizationRelations != nil && organization.OwnerProfileID != "" {
		if err := s.authorizationRelations.DeleteOrganizationAdmin(ctx, authorizationSubjectFromOwnerProfile(organization.OwnerProfileID), organizationID, organizationID); err != nil {
			return rollback(err)
		}
	}
	return rollback(s.recordAudit(ctx, viewer, "organization.delete", organizationID, map[string]string{
		"organization_id":  organizationID,
		"name":             organization.Name,
		"department_count": fmt.Sprint(len(departmentsToDelete)),
		"position_count":   fmt.Sprint(len(positionsToDelete)),
		"default_unit_id":  organization.DefaultDepartmentID,
		"default_position": organization.DefaultPositionID,
	}))
}

func organizationUnitCodeContext(graph OrgGraphDTO, organizationID, parentDepartmentID string) (string, []string, error) {
	var organization OrganizationDTO
	for _, candidate := range graph.Organizations {
		if candidate.OrganizationID == organizationID {
			organization = candidate
			break
		}
	}
	if organization.OrganizationID == "" {
		return "", nil, fmt.Errorf("%w: organization_id %s is not in organization catalog", ErrInvalidRequest, organizationID)
	}
	parentCode := organization.Code
	if !validOrganizationCode(parentCode) {
		parentCode = "01"
	}
	if parentDepartmentID != "" {
		parentCode = ""
		for _, department := range graph.Departments {
			if department.DepartmentID == parentDepartmentID {
				parentCode = department.Code
				break
			}
		}
		if !validOrganizationCode(parentCode) {
			return "", nil, fmt.Errorf("%w: parent_department_id %s has no valid organization code", ErrInvalidRequest, parentDepartmentID)
		}
	}
	siblingCodes := make([]string, 0)
	for _, department := range graph.Departments {
		if parentDepartmentID == "" {
			if department.ParentDepartmentID == "" && department.OrganizationID == organizationID {
				siblingCodes = append(siblingCodes, department.Code)
			}
			continue
		}
		if department.ParentDepartmentID == parentDepartmentID {
			siblingCodes = append(siblingCodes, department.Code)
		}
	}
	return parentCode, siblingCodes, nil
}

func (s *CommandService) DeleteOrganizationUnit(ctx context.Context, viewer ViewerRef, departmentID string) error {
	if err := requireOperationsPermission(viewer, PermissionOrgManage); err != nil {
		return err
	}
	if s == nil || s.orgs == nil || s.profiles == nil {
		return ErrServiceDependency
	}
	departmentID = strings.TrimSpace(departmentID)
	if departmentID == "" {
		return fmt.Errorf("%w: department_id is required", ErrInvalidRequest)
	}
	graph, err := s.orgs.ListOrganizations(ctx, OrgGraphQuery{})
	if err != nil {
		return err
	}
	var department DepartmentDTO
	for _, candidate := range graph.Departments {
		if candidate.DepartmentID == departmentID {
			department = candidate
			break
		}
	}
	if department.DepartmentID == "" {
		return ErrNotFound
	}
	if err := s.authorizeOperationObject(ctx, viewer, authorizationActionDepartmentManage, authorizationObjectDepartment, department.DepartmentID, department.OrganizationID); err != nil {
		return err
	}
	var organization OrganizationDTO
	for _, candidate := range graph.Organizations {
		if candidate.OrganizationID == department.OrganizationID {
			organization = candidate
			break
		}
	}
	if organization.OrganizationID == "" {
		return fmt.Errorf("%w: organization_id %s is not in organization catalog", ErrInvalidRequest, department.OrganizationID)
	}
	if organization.DefaultDepartmentID != "" && organization.DefaultDepartmentID == departmentID {
		return fmt.Errorf("%w: default department cannot be deleted", ErrInvalidRequest)
	}
	for _, candidate := range graph.Departments {
		if candidate.ParentDepartmentID == departmentID {
			return fmt.Errorf("%w: department has child units", ErrInvalidRequest)
		}
	}
	defaultPositionFound := false
	positionsToDelete := make([]PositionDTO, 0)
	for _, position := range graph.Positions {
		if position.PositionID == organization.DefaultPositionID && position.DepartmentID == organization.DefaultDepartmentID {
			defaultPositionFound = true
		}
		if position.DepartmentID == departmentID {
			positionsToDelete = append(positionsToDelete, position)
		}
	}
	hasDefaultTarget := organization.DefaultDepartmentID != "" && organization.DefaultPositionID != ""
	if hasDefaultTarget && !defaultPositionFound {
		return fmt.Errorf("%w: organization default position is not in organization catalog", ErrInvalidRequest)
	}
	if hasDefaultTarget {
		if err := s.authorizeOperationObject(ctx, viewer, authorizationActionDepartmentManage, authorizationObjectDepartment, organization.DefaultDepartmentID, organization.OrganizationID); err != nil {
			return err
		}
		if err := s.moveUnitMembersToDefault(ctx, viewer, departmentID, organization); err != nil {
			return err
		}
	} else if err := s.ensureDepartmentHasNoInternalMembers(ctx, departmentID); err != nil {
		return err
	}
	for _, position := range positionsToDelete {
		if err := s.deleteLifecycleKnowledgeBase(ctx, viewer, knowledgecore.OwnerPosition, position.PositionID, position.KnowledgeBaseID); err != nil {
			return err
		}
	}
	for _, position := range positionsToDelete {
		if err := s.orgs.DeletePosition(ctx, position.PositionID); err != nil {
			return err
		}
	}
	if err := s.orgs.DeleteDepartment(ctx, departmentID); err != nil {
		return err
	}
	if err := s.deleteDepartmentCreatedRelation(ctx, department); err != nil {
		return err
	}
	return s.recordAudit(ctx, viewer, "organization.unit.delete", departmentID, map[string]string{
		"organization_id":        organization.OrganizationID,
		"department_id":          departmentID,
		"default_department_id":  organization.DefaultDepartmentID,
		"default_position_id":    organization.DefaultPositionID,
		"moved_members_to_unit":  organization.DefaultDepartmentID,
		"moved_members_position": organization.DefaultPositionID,
	})
}

func (s *CommandService) DeleteOrganizationPosition(ctx context.Context, viewer ViewerRef, positionID string) error {
	if err := requireOperationsPermission(viewer, PermissionOrgManage); err != nil {
		return err
	}
	if s == nil || s.orgs == nil || s.directory == nil {
		return ErrServiceDependency
	}
	positionID = strings.TrimSpace(positionID)
	if positionID == "" {
		return fmt.Errorf("%w: position_id is required", ErrInvalidRequest)
	}
	graph, err := s.orgs.ListOrganizations(ctx, OrgGraphQuery{})
	if err != nil {
		return err
	}
	var position PositionDTO
	for _, candidate := range graph.Positions {
		if candidate.PositionID == positionID {
			position = candidate
			break
		}
	}
	if position.PositionID == "" {
		return ErrNotFound
	}
	positionOrganizationID := ""
	for _, department := range graph.Departments {
		if department.DepartmentID == position.DepartmentID {
			positionOrganizationID = department.OrganizationID
			break
		}
	}
	if err := s.authorizeOperationObject(ctx, viewer, authorizationActionDepartmentManage, authorizationObjectDepartment, position.DepartmentID, positionOrganizationID); err != nil {
		return err
	}
	if err := s.ensurePositionHasNoInternalMembers(ctx, positionID); err != nil {
		return err
	}
	if err := s.deleteLifecycleKnowledgeBase(ctx, viewer, knowledgecore.OwnerPosition, positionID, position.KnowledgeBaseID); err != nil {
		return err
	}
	if err := s.orgs.DeletePosition(ctx, positionID); err != nil {
		return err
	}
	return s.recordAudit(ctx, viewer, "organization.position.delete", positionID, map[string]string{
		"position_id":   position.PositionID,
		"department_id": position.DepartmentID,
		"name":          position.Name,
	})
}

func (s *CommandService) ensureOrganizationHasNoInternalMembers(ctx context.Context, organizationID string) error {
	if s == nil || s.directory == nil {
		return ErrServiceDependency
	}
	page, err := s.directory.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{
		Organization:  agentcore.OrganizationID(organizationID),
		Placement:     agentcore.TalentPlacementFilterInternal,
		IncludeAI:     true,
		IncludeHumans: true,
	})
	if err != nil {
		return err
	}
	if len(page.Entries) > 0 || page.Total > 0 {
		return fmt.Errorf("%w: organization has internal members; release or move members before deleting", ErrInvalidRequest)
	}
	return nil
}

func (s *CommandService) ensureDepartmentHasNoInternalMembers(ctx context.Context, departmentID string) error {
	if s == nil || s.directory == nil {
		return ErrServiceDependency
	}
	page, err := s.directory.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{
		Department:    agentcore.DepartmentID(departmentID),
		Placement:     agentcore.TalentPlacementFilterInternal,
		IncludeAI:     true,
		IncludeHumans: true,
	})
	if err != nil {
		return err
	}
	if len(page.Entries) > 0 || page.Total > 0 {
		return fmt.Errorf("%w: department has internal members; release or move members before deleting", ErrInvalidRequest)
	}
	return nil
}

func (s *CommandService) ensurePositionHasNoInternalMembers(ctx context.Context, positionID string) error {
	if s == nil || s.directory == nil {
		return ErrServiceDependency
	}
	page, err := s.directory.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{
		Position:      agentcore.PositionID(positionID),
		Placement:     agentcore.TalentPlacementFilterInternal,
		IncludeAI:     true,
		IncludeHumans: true,
	})
	if err != nil {
		return err
	}
	if len(page.Entries) > 0 || page.Total > 0 {
		return fmt.Errorf("%w: position has internal members; release or move members before deleting", ErrInvalidRequest)
	}
	return nil
}

type organizationUnitMemberMove struct {
	previous agentcore.AgentProfile
	current  agentcore.AgentProfile
	rolePlan *workforceRoleAssignmentPlan
}

func (s *CommandService) moveUnitMembersToDefault(ctx context.Context, viewer ViewerRef, departmentID string, organization OrganizationDTO) error {
	directory, ok := s.profiles.(WorkforceProfileDirectoryPort)
	if !ok {
		return ErrServiceDependency
	}
	page, err := directory.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{
		Organization:  agentcore.OrganizationID(organization.OrganizationID),
		Department:    agentcore.DepartmentID(departmentID),
		IncludeAI:     true,
		IncludeHumans: true,
	})
	if err != nil {
		return err
	}
	moves := make([]organizationUnitMemberMove, 0, len(page.Entries))
	for _, entry := range page.Entries {
		profile, err := s.profiles.GetProfile(ctx, entry.AgentProfileID)
		if err != nil {
			return err
		}
		previous := profile
		profile.OrgIdentity.OrganizationRef.ID = agentcore.OrganizationID(organization.OrganizationID)
		profile.OrgIdentity.DepartmentRef.ID = agentcore.DepartmentID(organization.DefaultDepartmentID)
		profile.OrgIdentity.PositionRef.ID = agentcore.PositionID(organization.DefaultPositionID)
		rolePlan, err := s.planWorkforceRoleAssignments(ctx, &previous, profile, viewer.ViewerID, true)
		if err != nil {
			return err
		}
		if err := s.authorizeWorkforceRoleChanges(ctx, viewer, &previous, profile, rolePlan); err != nil {
			return err
		}
		moves = append(moves, organizationUnitMemberMove{previous: previous, current: profile, rolePlan: rolePlan})
	}

	applied := make([]organizationUnitMemberMove, 0, len(moves))
	for _, move := range moves {
		if err := s.profiles.SaveProfile(ctx, move.current); err != nil {
			return errors.Join(err, s.restoreOrganizationUnitMemberMoves(ctx, applied))
		}
		if err := s.applyWorkforceRoleAssignmentPlan(ctx, move.rolePlan); err != nil {
			return errors.Join(
				err,
				s.restoreWorkforceRoleAssignmentPlan(ctx, move.rolePlan),
				s.restoreWorkforceProfileRecord(ctx, move.previous, move.current, true),
				s.restoreOrganizationUnitMemberMoves(ctx, applied),
			)
		}
		if err := s.deleteWorkforceMemberPlacementRelation(ctx, move.previous); err != nil {
			return errors.Join(
				err,
				s.restoreWorkforceRoleAssignmentPlan(ctx, move.rolePlan),
				s.restoreWorkforceProfile(ctx, move.previous, move.current, true, true),
				s.restoreOrganizationUnitMemberMoves(ctx, applied),
			)
		}
		if err := s.writeWorkforceMemberPlacementRelation(ctx, move.current); err != nil {
			return errors.Join(
				err,
				s.restoreWorkforceRoleAssignmentPlan(ctx, move.rolePlan),
				s.restoreWorkforceProfile(ctx, move.previous, move.current, true, true),
				s.restoreOrganizationUnitMemberMoves(ctx, applied),
			)
		}
		applied = append(applied, move)
	}
	return nil
}

func (s *CommandService) restoreOrganizationUnitMemberMoves(ctx context.Context, moves []organizationUnitMemberMove) error {
	var rollbackErr error
	for i := len(moves) - 1; i >= 0; i-- {
		move := moves[i]
		rollbackErr = errors.Join(rollbackErr, s.restoreWorkforceRoleAssignmentPlan(ctx, move.rolePlan))
		rollbackErr = errors.Join(rollbackErr, s.restoreWorkforceProfile(ctx, move.previous, move.current, true, true))
	}
	return rollbackErr
}

func (s *CommandService) validateOrganizationUnitRefs(ctx context.Context, organizationID, parentDepartmentID string) error {
	if s == nil || s.orgs == nil {
		return ErrServiceDependency
	}
	graph, err := s.orgs.ListOrganizations(ctx, OrgGraphQuery{OrganizationID: organizationID})
	if err != nil {
		return err
	}
	if len(graph.Organizations) == 0 {
		return fmt.Errorf("%w: organization_id %s is not in organization catalog", ErrInvalidRequest, organizationID)
	}
	if parentDepartmentID == "" {
		return nil
	}
	for _, department := range graph.Departments {
		if department.DepartmentID == parentDepartmentID {
			return nil
		}
	}
	return fmt.Errorf("%w: parent_department_id %s is not in organization catalog", ErrInvalidRequest, parentDepartmentID)
}

func (s *CommandService) resolveOrganizationPositionDepartment(ctx context.Context, organizationID, departmentID string) (string, error) {
	graph, err := s.orgs.ListOrganizations(ctx, OrgGraphQuery{OrganizationID: organizationID})
	if err != nil {
		return "", err
	}
	var organization OrganizationDTO
	for _, candidate := range graph.Organizations {
		if candidate.OrganizationID == organizationID {
			organization = candidate
			break
		}
	}
	if organization.OrganizationID == "" {
		return "", fmt.Errorf("%w: organization_id %s is not in organization catalog", ErrInvalidRequest, organizationID)
	}
	if departmentID == "" {
		departmentID = organization.DefaultDepartmentID
	}
	if departmentID == "" {
		return "", fmt.Errorf("%w: department_id is required when organization has no default department", ErrInvalidRequest)
	}
	for _, department := range graph.Departments {
		if department.DepartmentID == departmentID && department.OrganizationID == organizationID {
			return departmentID, nil
		}
	}
	return "", fmt.Errorf("%w: department_id %s is not in organization catalog", ErrInvalidRequest, departmentID)
}
