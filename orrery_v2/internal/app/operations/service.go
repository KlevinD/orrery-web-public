package operations

import "context"

type Service struct {
	query   *QueryService
	command *CommandService
}

func NewService(query *QueryService, command *CommandService) *Service {
	return &Service{query: query, command: command}
}

func (s *Service) ListWorkforceMembers(ctx context.Context, viewer ViewerRef, query WorkforceQuery) (WorkforcePageDTO, error) {
	if s == nil || s.query == nil {
		return WorkforcePageDTO{}, ErrServiceDependency
	}
	return s.query.ListWorkforceMembers(ctx, viewer, query)
}

func (s *Service) ListTalentMarketMembers(ctx context.Context, viewer ViewerRef, query WorkforceQuery) (WorkforcePageDTO, error) {
	if s == nil || s.query == nil {
		return WorkforcePageDTO{}, ErrServiceDependency
	}
	return s.query.ListTalentMarketMembers(ctx, viewer, query)
}

func (s *Service) GetOrgGraph(ctx context.Context, viewer ViewerRef, query OrgGraphQuery) (OrgGraphDTO, error) {
	if s == nil || s.query == nil {
		return OrgGraphDTO{}, ErrServiceDependency
	}
	return s.query.GetOrgGraph(ctx, viewer, query)
}

func (s *Service) GetPermissionMatrix(ctx context.Context, viewer ViewerRef, query PermissionMatrixQuery) (PermissionMatrixDTO, error) {
	if s == nil || s.query == nil {
		return PermissionMatrixDTO{}, ErrServiceDependency
	}
	return s.query.GetPermissionMatrix(ctx, viewer, query)
}

func (s *Service) DecidePermission(ctx context.Context, viewer ViewerRef, req PermissionDecisionRequestDTO) (PermissionDecisionDTO, error) {
	if s == nil || s.query == nil {
		return PermissionDecisionDTO{}, ErrServiceDependency
	}
	return s.query.DecidePermission(ctx, viewer, req)
}

func (s *Service) ListRoleDefinitions(ctx context.Context, viewer ViewerRef) (RoleDefinitionPageDTO, error) {
	if s == nil || s.query == nil {
		return RoleDefinitionPageDTO{}, ErrServiceDependency
	}
	return s.query.ListRoleDefinitions(ctx, viewer)
}

func (s *Service) ListPermissionRoles(ctx context.Context, viewer ViewerRef, query PermissionRolePageQuery) (PermissionRolePageDTO, error) {
	if s == nil || s.query == nil {
		return PermissionRolePageDTO{}, ErrServiceDependency
	}
	return s.query.ListPermissionRoles(ctx, viewer, query)
}

func (s *Service) GetPermissionRole(ctx context.Context, viewer ViewerRef, roleID string) (RoleDefinitionDTO, error) {
	if s == nil || s.query == nil {
		return RoleDefinitionDTO{}, ErrServiceDependency
	}
	return s.query.GetPermissionRole(ctx, viewer, roleID)
}

func (s *Service) ListPermissionEffectiveGrants(ctx context.Context, viewer ViewerRef, query PermissionEffectiveGrantQuery) (PermissionEffectiveGrantPageDTO, error) {
	if s == nil || s.query == nil {
		return PermissionEffectiveGrantPageDTO{}, ErrServiceDependency
	}
	return s.query.ListPermissionEffectiveGrants(ctx, viewer, query)
}

func (s *Service) ListRoleAssignments(ctx context.Context, viewer ViewerRef, query RoleAssignmentQuery) (RoleAssignmentPageDTO, error) {
	if s == nil || s.query == nil {
		return RoleAssignmentPageDTO{}, ErrServiceDependency
	}
	return s.query.ListRoleAssignments(ctx, viewer, query)
}

func (s *Service) PutRoleDefinitions(ctx context.Context, viewer ViewerRef, cmd PutRoleDefinitionsCommand) (RoleDefinitionPageDTO, error) {
	if s == nil || s.command == nil {
		return RoleDefinitionPageDTO{}, ErrServiceDependency
	}
	return s.command.PutRoleDefinitions(ctx, viewer, cmd)
}

func (s *Service) ApplyPermissionRoleDefinitions(ctx context.Context, viewer ViewerRef, cmd ApplyPermissionRoleDefinitionsCommand) (ApplyPermissionRoleDefinitionsResult, error) {
	if s == nil || s.command == nil {
		return ApplyPermissionRoleDefinitionsResult{}, ErrServiceDependency
	}
	return s.command.ApplyPermissionRoleDefinitions(ctx, viewer, cmd)
}

func (s *Service) DeleteRoleAssignment(ctx context.Context, viewer ViewerRef, assignmentID string) error {
	if s == nil || s.command == nil {
		return ErrServiceDependency
	}
	return s.command.DeleteRoleAssignment(ctx, viewer, assignmentID)
}

func (s *Service) ListAuditLog(ctx context.Context, viewer ViewerRef, query AuditLogQuery) (AuditEventPageDTO, error) {
	if s == nil || s.query == nil {
		return AuditEventPageDTO{}, ErrServiceDependency
	}
	return s.query.ListAuditLog(ctx, viewer, query)
}

func (s *Service) ListEvaluations(ctx context.Context, viewer ViewerRef, query EvaluationQuery) (EvaluationPageDTO, error) {
	if s == nil || s.query == nil {
		return EvaluationPageDTO{}, ErrServiceDependency
	}
	return s.query.ListEvaluations(ctx, viewer, query)
}

func (s *Service) CreateOrganization(ctx context.Context, viewer ViewerRef, cmd CreateOrganizationCommand) (OrganizationDTO, error) {
	if s == nil || s.command == nil {
		return OrganizationDTO{}, ErrServiceDependency
	}
	return s.command.CreateOrganization(ctx, viewer, cmd)
}

func (s *Service) CreateOrganizationUnit(ctx context.Context, viewer ViewerRef, cmd CreateOrganizationUnitCommand) (DepartmentDTO, error) {
	if s == nil || s.command == nil {
		return DepartmentDTO{}, ErrServiceDependency
	}
	return s.command.CreateOrganizationUnit(ctx, viewer, cmd)
}

func (s *Service) CreateOrganizationPosition(ctx context.Context, viewer ViewerRef, cmd CreateOrganizationPositionCommand) (PositionDTO, error) {
	if s == nil || s.command == nil {
		return PositionDTO{}, ErrServiceDependency
	}
	return s.command.CreateOrganizationPosition(ctx, viewer, cmd)
}

func (s *Service) DeleteOrganization(ctx context.Context, viewer ViewerRef, organizationID string) error {
	if s == nil || s.command == nil {
		return ErrServiceDependency
	}
	return s.command.DeleteOrganization(ctx, viewer, organizationID)
}

func (s *Service) DeleteOrganizationUnit(ctx context.Context, viewer ViewerRef, departmentID string) error {
	if s == nil || s.command == nil {
		return ErrServiceDependency
	}
	return s.command.DeleteOrganizationUnit(ctx, viewer, departmentID)
}

func (s *Service) DeleteOrganizationPosition(ctx context.Context, viewer ViewerRef, positionID string) error {
	if s == nil || s.command == nil {
		return ErrServiceDependency
	}
	return s.command.DeleteOrganizationPosition(ctx, viewer, positionID)
}

func (s *Service) PutRoleAssignments(ctx context.Context, viewer ViewerRef, cmd PutRoleAssignmentsCommand) (RoleAssignmentPageDTO, error) {
	if s == nil || s.command == nil {
		return RoleAssignmentPageDTO{}, ErrServiceDependency
	}
	return s.command.PutRoleAssignments(ctx, viewer, cmd)
}

func (s *Service) UpdateOrganizationOwner(ctx context.Context, viewer ViewerRef, cmd UpdateOrganizationOwnerCommand) (OrganizationNodeDTO, error) {
	if s == nil || s.command == nil {
		return OrganizationNodeDTO{}, ErrServiceDependency
	}
	return s.command.UpdateOrganizationOwner(ctx, viewer, cmd)
}

func (s *Service) CreateWorkforceMember(ctx context.Context, viewer ViewerRef, cmd UpsertWorkforceMemberCommand) (WorkforceMemberDTO, error) {
	if s == nil || s.command == nil {
		return WorkforceMemberDTO{}, ErrServiceDependency
	}
	return s.command.CreateWorkforceMember(ctx, viewer, cmd)
}

func (s *Service) UpsertWorkforceMember(ctx context.Context, viewer ViewerRef, cmd UpsertWorkforceMemberCommand) (WorkforceMemberDTO, error) {
	if s == nil || s.command == nil {
		return WorkforceMemberDTO{}, ErrServiceDependency
	}
	return s.command.UpsertWorkforceMember(ctx, viewer, cmd)
}

func (s *Service) SetWorkforceMemberEnabled(ctx context.Context, viewer ViewerRef, cmd SetWorkforceMemberEnabledCommand) error {
	if s == nil || s.command == nil {
		return ErrServiceDependency
	}
	return s.command.SetWorkforceMemberEnabled(ctx, viewer, cmd)
}

func (s *Service) ReleaseWorkforceMember(ctx context.Context, viewer ViewerRef, cmd ReleaseWorkforceMemberCommand) error {
	if s == nil || s.command == nil {
		return ErrServiceDependency
	}
	return s.command.ReleaseWorkforceMember(ctx, viewer, cmd)
}

func (s *Service) HireTalentFromMarket(ctx context.Context, viewer ViewerRef, cmd HireTalentFromMarketCommand) (WorkforceMemberDTO, error) {
	if s == nil || s.command == nil {
		return WorkforceMemberDTO{}, ErrServiceDependency
	}
	return s.command.HireTalentFromMarket(ctx, viewer, cmd)
}

func (s *Service) DeleteTalentMarketMember(ctx context.Context, viewer ViewerRef, cmd DeleteTalentMarketMemberCommand) error {
	if s == nil || s.command == nil {
		return ErrServiceDependency
	}
	return s.command.DeleteTalentMarketMember(ctx, viewer, cmd)
}

func (s *Service) DeleteTalentMarketMembers(ctx context.Context, viewer ViewerRef, cmd DeleteTalentMarketMembersCommand) (DeleteTalentMarketMembersResponseDTO, error) {
	if s == nil || s.command == nil {
		return DeleteTalentMarketMembersResponseDTO{}, ErrServiceDependency
	}
	return s.command.DeleteTalentMarketMembers(ctx, viewer, cmd)
}

func (s *Service) PreviewTalentMarketImport(ctx context.Context, viewer ViewerRef, req TalentMarketImportPreviewRequest) (TalentMarketImportPreviewResponseDTO, error) {
	if s == nil || s.command == nil {
		return TalentMarketImportPreviewResponseDTO{}, ErrServiceDependency
	}
	return s.command.PreviewTalentMarketImport(ctx, viewer, req)
}

func (s *Service) CommitTalentMarketImport(ctx context.Context, viewer ViewerRef, req TalentMarketImportCommitRequest) (TalentMarketImportCommitResponseDTO, error) {
	if s == nil || s.command == nil {
		return TalentMarketImportCommitResponseDTO{}, ErrServiceDependency
	}
	return s.command.CommitTalentMarketImport(ctx, viewer, req)
}

func (s *Service) ExportTalentMarket(ctx context.Context, viewer ViewerRef, req TalentMarketExportRequest) (TalentMarketExportPackageDTO, error) {
	if s == nil || s.command == nil {
		return TalentMarketExportPackageDTO{}, ErrServiceDependency
	}
	return s.command.ExportTalentMarket(ctx, viewer, req)
}
