package main

import (
	"context"
	"io"
	"strings"
	"time"

	"orrery_v2/internal/app/operations"
	resourceadmin "orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/platform/graphprotocol"
	skillres "orrery_v2/internal/resource/skill"
)

func configureProductionResourceSkillStorage(query *resourceadmin.QueryService, command *resourceadmin.CommandService, store *skillres.PackageStore, bundles skillres.BundleRegistry, evaluations resourceadmin.SkillVersionEvaluationStore) {
	query.SetSkillPackageStore(store)
	query.SetSkillBundleRegistry(bundles)
	command.SetSkillPackageStore(store)
	command.SetSkillBundleRegistry(bundles)
	command.SetSkillVersionEvaluationStore(evaluations)
}

func (p productionAPI) ListResourcesFiltered(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.ListResourcesRequest) (resourceadmin.ResourceAdminViewDTO, error) {
	return p.resourceQuery.ListResourcesFiltered(ctx, viewer, req)
}

func (p productionAPI) ListSkillBundles(ctx context.Context, viewer resourceadmin.ViewerRef) (resourceadmin.SkillBundleListDTO, error) {
	return p.resourceQuery.ListSkillBundles(ctx, viewer)
}

func (p productionAPI) GetSkillDetail(ctx context.Context, viewer resourceadmin.ViewerRef, skillID string) (resourceadmin.SkillDetailDTO, error) {
	return p.resourceQuery.GetSkillDetail(ctx, viewer, skillID)
}

func (p productionAPI) GetMCPServer(ctx context.Context, viewer resourceadmin.ViewerRef, toolID string) (resourceadmin.MCPServerDTO, error) {
	return p.resourceQuery.GetMCPServer(ctx, viewer, toolID)
}

func (p productionAPI) GetMemoryProfile(ctx context.Context, viewer resourceadmin.ViewerRef, profileID string) (resourceadmin.MemoryProfileDTO, error) {
	return p.resourceQuery.GetMemoryProfile(ctx, viewer, profileID)
}

func (p productionAPI) UpdateMemoryProfile(ctx context.Context, viewer resourceadmin.ViewerRef, profileID string, req resourceadmin.UpdateMemoryProfileRequest) (resourceadmin.MemoryProfileDTO, error) {
	return p.resourceQuery.UpdateMemoryProfile(ctx, viewer, profileID, req)
}

func (p productionAPI) GetPersonalMemoryProfileByAgent(ctx context.Context, viewer resourceadmin.ViewerRef, agentID string) (resourceadmin.MemoryProfileDTO, error) {
	return p.resourceQuery.GetPersonalMemoryProfileByAgent(ctx, viewer, agentID)
}

func (p productionAPI) BuildMemoryDigest(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.BuildMemoryDigestRequest) (resourceadmin.MemoryDigestDTO, error) {
	return p.resourceQuery.BuildMemoryDigest(ctx, viewer, req)
}

func (p productionAPI) DiscoverMCPServers(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.DiscoverMCPRequest) (resourceadmin.DiscoverMCPResponseDTO, error) {
	return p.resourceQuery.DiscoverMCPServers(ctx, viewer, req)
}

func (p productionAPI) RegisterMCPServer(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.RegisterMCPServerRequest) (resourceadmin.MCPServerDTO, error) {
	dto, err := p.resourceCommand.RegisterMCPServer(ctx, viewer, req)
	if err != nil {
		return resourceadmin.MCPServerDTO{}, err
	}
	if err := p.projectMCPServerCapabilities(ctx, dto.ToolID); err != nil {
		return resourceadmin.MCPServerDTO{}, err
	}
	return dto, nil
}

func (p productionAPI) UpdateMCPServer(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.RegisterMCPServerRequest) (resourceadmin.MCPServerDTO, error) {
	dto, err := p.resourceCommand.UpdateMCPServer(ctx, viewer, req)
	if err != nil {
		return resourceadmin.MCPServerDTO{}, err
	}
	if err := p.projectMCPServerCapabilities(ctx, dto.ToolID); err != nil {
		return resourceadmin.MCPServerDTO{}, err
	}
	return dto, nil
}

func (p productionAPI) RegisterTool(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.ToolDefinitionRequest) (resourceadmin.ToolDTO, error) {
	return p.resourceCommand.RegisterTool(ctx, viewer, req)
}

func (p productionAPI) UpdateTool(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.ToolDefinitionRequest) (resourceadmin.ToolDTO, error) {
	return p.resourceCommand.UpdateTool(ctx, viewer, req)
}

func (p productionAPI) SyncMCPServer(ctx context.Context, viewer resourceadmin.ViewerRef, toolID string) (resourceadmin.MCPServerSyncDTO, error) {
	dto, err := p.resourceCommand.SyncMCPServer(ctx, viewer, toolID)
	if err != nil {
		return resourceadmin.MCPServerSyncDTO{}, err
	}
	if err := p.projectMCPServerCapabilities(ctx, dto.ToolID); err != nil {
		return resourceadmin.MCPServerSyncDTO{}, err
	}
	return dto, nil
}

func (p productionAPI) GetMCPServerHealth(ctx context.Context, viewer resourceadmin.ViewerRef, toolID string) (resourceadmin.MCPServerHealthDTO, error) {
	return p.resourceCommand.GetMCPServerHealth(ctx, viewer, toolID)
}

func (p productionAPI) ImportSkillPackage(ctx context.Context, viewer resourceadmin.ViewerRef, reader io.ReaderAt, size int64) (resourceadmin.SkillDTO, error) {
	return p.resourceCommand.ImportSkillPackage(ctx, viewer, reader, size)
}

func (p productionAPI) CreateSkill(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.CreateSkillRequest) (resourceadmin.SkillDetailDTO, error) {
	return p.resourceCommand.CreateSkill(ctx, viewer, req)
}

func (p productionAPI) RegisterSkillVersion(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillVersionCandidateRequest) (resourceadmin.SkillVersionDTO, error) {
	return p.resourceCommand.RegisterSkillVersion(ctx, viewer, req)
}

func (p productionAPI) PromoteSkillVersion(ctx context.Context, viewer resourceadmin.ViewerRef, skillID, version string, req resourceadmin.PromoteSkillVersionRequest) (resourceadmin.SkillVersionDTO, error) {
	return p.resourceCommand.PromoteSkillVersion(ctx, viewer, skillID, version, req)
}

func (p productionAPI) RollbackSkillVersion(ctx context.Context, viewer resourceadmin.ViewerRef, skillID, version string, req resourceadmin.RollbackSkillVersionRequest) (resourceadmin.SkillVersionDTO, error) {
	return p.resourceCommand.RollbackSkillVersion(ctx, viewer, skillID, version, req)
}

func (p productionAPI) CreateSkillBundle(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.CreateSkillBundleRequest) (resourceadmin.SkillBundleDTO, error) {
	return p.resourceCommand.CreateSkillBundle(ctx, viewer, req)
}

func (p productionAPI) AddSkillsToBundle(ctx context.Context, viewer resourceadmin.ViewerRef, bundleID string, req resourceadmin.AddSkillsToBundleRequest) (resourceadmin.SkillBundleDTO, error) {
	return p.resourceCommand.AddSkillsToBundle(ctx, viewer, bundleID, req)
}

func (p productionAPI) ImportSkillIntoBundle(ctx context.Context, viewer resourceadmin.ViewerRef, bundleID string, reader io.ReaderAt, size int64) (resourceadmin.SkillBundleDTO, error) {
	return p.resourceCommand.ImportSkillIntoBundle(ctx, viewer, bundleID, reader, size)
}

func (p productionAPI) RemoveSkillFromBundle(ctx context.Context, viewer resourceadmin.ViewerRef, bundleID, skillID string) (resourceadmin.SkillBundleDTO, error) {
	return p.resourceCommand.RemoveSkillFromBundle(ctx, viewer, bundleID, skillID)
}

func (p productionAPI) DeleteSkillBundle(ctx context.Context, viewer resourceadmin.ViewerRef, bundleID string, req resourceadmin.DeleteSkillBundleRequest) error {
	return p.resourceCommand.DeleteSkillBundle(ctx, viewer, bundleID, req)
}

func (p productionAPI) SaveSkillMarkdown(ctx context.Context, viewer resourceadmin.ViewerRef, skillID, raw string) (resourceadmin.SkillDetailDTO, error) {
	return p.resourceCommand.SaveSkillMarkdown(ctx, viewer, skillID, raw)
}

func (p productionAPI) GetSkillFile(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillFileRequest) (resourceadmin.SkillFileDTO, error) {
	return p.resourceCommand.GetSkillFile(ctx, viewer, req)
}

func (p productionAPI) SaveSkillFile(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillFileWriteRequest) (resourceadmin.SkillFileDTO, error) {
	return p.resourceCommand.SaveSkillFile(ctx, viewer, req)
}

func (p productionAPI) UploadSkillFile(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillFileUploadRequest) (resourceadmin.SkillFileDTO, error) {
	return p.resourceCommand.UploadSkillFile(ctx, viewer, req)
}

func (p productionAPI) DownloadSkillFile(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillFileRequest) (resourceadmin.SkillFileDownloadDTO, error) {
	return p.resourceCommand.DownloadSkillFile(ctx, viewer, req)
}

func (p productionAPI) DeleteSkillFile(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillFileRequest) error {
	return p.resourceCommand.DeleteSkillFile(ctx, viewer, req)
}

func (p productionAPI) CreateSkillFolder(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillFileRequest) error {
	return p.resourceCommand.CreateSkillFolder(ctx, viewer, req)
}

func (p productionAPI) DisableTool(ctx context.Context, viewer resourceadmin.ViewerRef, toolID string) error {
	return p.resourceCommand.DisableTool(ctx, viewer, toolID)
}

func (p productionAPI) DisableSkill(ctx context.Context, viewer resourceadmin.ViewerRef, skillID string) error {
	return p.resourceCommand.DisableSkill(ctx, viewer, skillID)
}

func (p productionAPI) DeleteMCPServer(ctx context.Context, viewer resourceadmin.ViewerRef, toolID string) error {
	return p.resourceCommand.DeleteMCPServer(ctx, viewer, toolID)
}

func (p productionAPI) DeleteTool(ctx context.Context, viewer resourceadmin.ViewerRef, toolID string) error {
	return p.resourceCommand.DeleteTool(ctx, viewer, toolID)
}

func (p productionAPI) DeleteSkill(ctx context.Context, viewer resourceadmin.ViewerRef, skillID string) error {
	return p.resourceCommand.DeleteSkill(ctx, viewer, skillID)
}

func (p productionAPI) ExportSkillPackage(ctx context.Context, viewer resourceadmin.ViewerRef, skillID string) (resourceadmin.SkillPackageDTO, error) {
	return p.resourceCommand.ExportSkillPackage(ctx, viewer, skillID)
}

func (p productionAPI) BindResourceToAgent(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.BindResourceRequest) (resourceadmin.ResourceBindingDTO, error) {
	return p.resourceCommand.BindResourceToAgent(ctx, viewer, req)
}

func (p productionAPI) UnbindResourceFromAgent(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.UnbindResourceRequest) error {
	return p.resourceCommand.UnbindResourceFromAgent(ctx, viewer, req)
}

func (p productionAPI) ListWorkforceMembers(ctx context.Context, viewer operations.ViewerRef, query operations.WorkforceQuery) (operations.WorkforcePageDTO, error) {
	return p.operations.ListWorkforceMembers(ctx, viewer, query)
}

func (p productionAPI) ListTalentMarketMembers(ctx context.Context, viewer operations.ViewerRef, query operations.WorkforceQuery) (operations.WorkforcePageDTO, error) {
	return p.operations.ListTalentMarketMembers(ctx, viewer, query)
}

func (p productionAPI) GetOrgGraph(ctx context.Context, viewer operations.ViewerRef, query operations.OrgGraphQuery) (operations.OrgGraphDTO, error) {
	return p.operations.GetOrgGraph(ctx, viewer, query)
}

func (p productionAPI) ListRoleDefinitions(ctx context.Context, viewer operations.ViewerRef) (operations.RoleDefinitionPageDTO, error) {
	return p.operations.ListRoleDefinitions(ctx, viewer)
}

func (p productionAPI) PutRoleDefinitions(ctx context.Context, viewer operations.ViewerRef, cmd operations.PutRoleDefinitionsCommand) (operations.RoleDefinitionPageDTO, error) {
	return p.operations.PutRoleDefinitions(ctx, viewer, cmd)
}

func (p productionAPI) DeleteRoleAssignment(ctx context.Context, viewer operations.ViewerRef, assignmentID string) error {
	return p.operations.DeleteRoleAssignment(ctx, viewer, assignmentID)
}

func (p productionAPI) ListRoleAssignments(ctx context.Context, viewer operations.ViewerRef, query operations.RoleAssignmentQuery) (operations.RoleAssignmentPageDTO, error) {
	return p.operations.ListRoleAssignments(ctx, viewer, query)
}

func (p productionAPI) PutRoleAssignments(ctx context.Context, viewer operations.ViewerRef, cmd operations.PutRoleAssignmentsCommand) (operations.RoleAssignmentPageDTO, error) {
	return p.operations.PutRoleAssignments(ctx, viewer, cmd)
}

func (p productionAPI) GetPermissionMatrix(ctx context.Context, viewer operations.ViewerRef, query operations.PermissionMatrixQuery) (operations.PermissionMatrixDTO, error) {
	return p.operations.GetPermissionMatrix(ctx, viewer, query)
}

func (p productionAPI) DecidePermission(ctx context.Context, viewer operations.ViewerRef, req operations.PermissionDecisionRequestDTO) (operations.PermissionDecisionDTO, error) {
	return p.operations.DecidePermission(ctx, viewer, req)
}

func (p productionAPI) ListAuditLog(ctx context.Context, viewer operations.ViewerRef, query operations.AuditLogQuery) (operations.AuditEventPageDTO, error) {
	return p.operations.ListAuditLog(ctx, viewer, query)
}

func (p productionAPI) ListEvaluations(ctx context.Context, viewer operations.ViewerRef, query operations.EvaluationQuery) (operations.EvaluationPageDTO, error) {
	return p.operations.ListEvaluations(ctx, viewer, query)
}

func (p productionAPI) CreateOrganization(ctx context.Context, viewer operations.ViewerRef, cmd operations.CreateOrganizationCommand) (operations.OrganizationDTO, error) {
	organization, err := p.operations.CreateOrganization(ctx, viewer, cmd)
	if err != nil {
		return operations.OrganizationDTO{}, err
	}
	if err := p.publishOrganizationCreatedToOntology(ctx, viewer, organization); err != nil {
		return operations.OrganizationDTO{}, err
	}
	return organization, nil
}

func (p productionAPI) publishOrganizationCreatedToOntology(ctx context.Context, viewer operations.ViewerRef, organization operations.OrganizationDTO) error {
	if p.ontologyProject == nil {
		return nil
	}
	eventID := "organization-created:" + organization.OrganizationID
	return p.applyOntologyEvent(ctx, graphprotocol.DomainEvent{
		ID:       eventID,
		TenantID: organization.OrganizationID,
		Type:     "organization.created",
		StreamID: organization.OrganizationID,
		Version:  1,
		Payload: map[string]any{
			"id":                organization.OrganizationID,
			"name":              organization.Name,
			"status":            organization.Status,
			"code":              organization.Code,
			"knowledge_base_id": organization.KnowledgeBaseID,
		},
		PublishedBy:     graphprotocol.ActorRef{Kind: viewer.ViewerKind, ID: viewer.ViewerID},
		OccurredAt:      time.Now().UTC(),
		SourceAccessRef: organizationOntologySourceRef(organization.OrganizationID),
	})
}

func (p productionAPI) CreateOrganizationUnit(ctx context.Context, viewer operations.ViewerRef, cmd operations.CreateOrganizationUnitCommand) (operations.DepartmentDTO, error) {
	department, err := p.operations.CreateOrganizationUnit(ctx, viewer, cmd)
	if err != nil {
		return operations.DepartmentDTO{}, err
	}
	if err := p.publishDepartmentCreatedToOntology(ctx, viewer, department); err != nil {
		return operations.DepartmentDTO{}, err
	}
	if strings.TrimSpace(department.DefaultPositionID) != "" {
		if err := p.publishPositionCreatedToOntology(ctx, viewer, operations.PositionDTO{
			PositionID:   department.DefaultPositionID,
			DepartmentID: department.DepartmentID,
			Name:         cmd.DefaultPositionName,
		}, department.OrganizationID); err != nil {
			return operations.DepartmentDTO{}, err
		}
	}
	return department, nil
}

func (p productionAPI) CreateOrganizationPosition(ctx context.Context, viewer operations.ViewerRef, cmd operations.CreateOrganizationPositionCommand) (operations.PositionDTO, error) {
	position, err := p.operations.CreateOrganizationPosition(ctx, viewer, cmd)
	if err != nil {
		return operations.PositionDTO{}, err
	}
	if err := p.publishPositionCreatedToOntology(ctx, viewer, position, cmd.OrganizationID); err != nil {
		return operations.PositionDTO{}, err
	}
	return position, nil
}

func (p productionAPI) publishDepartmentCreatedToOntology(ctx context.Context, viewer operations.ViewerRef, department operations.DepartmentDTO) error {
	if p.ontologyProject == nil {
		return nil
	}
	eventID := "department-created:" + department.DepartmentID
	return p.applyOntologyEvent(ctx, graphprotocol.DomainEvent{
		ID:       eventID,
		TenantID: department.OrganizationID,
		Type:     "people.department.created",
		StreamID: department.DepartmentID,
		Version:  1,
		Payload: map[string]any{
			"id":               department.DepartmentID,
			"name":             department.Name,
			"status":           department.Status,
			"owner_profile_id": department.OwnerProfileID,
			"organization_id":  department.OrganizationID,
		},
		PublishedBy:     graphprotocol.ActorRef{Kind: viewer.ViewerKind, ID: viewer.ViewerID},
		OccurredAt:      time.Now().UTC(),
		SourceAccessRef: organizationOntologySourceRef(department.OrganizationID),
	})
}

func (p productionAPI) publishPositionCreatedToOntology(ctx context.Context, viewer operations.ViewerRef, position operations.PositionDTO, organizationID string) error {
	if p.ontologyProject == nil {
		return nil
	}
	eventID := "position-created:" + position.PositionID
	return p.applyOntologyEvent(ctx, graphprotocol.DomainEvent{
		ID:       eventID,
		TenantID: strings.TrimSpace(organizationID),
		Type:     "people.position.created",
		StreamID: position.PositionID,
		Version:  1,
		Payload: map[string]any{
			"id":            position.PositionID,
			"name":          position.Name,
			"status":        "active",
			"department_id": position.DepartmentID,
		},
		PublishedBy:     graphprotocol.ActorRef{Kind: viewer.ViewerKind, ID: viewer.ViewerID},
		OccurredAt:      time.Now().UTC(),
		SourceAccessRef: organizationOntologySourceRef(organizationID),
	})
}

func (p productionAPI) publishWorkforceMemberAssignedToOntology(ctx context.Context, viewer operations.ViewerRef, member operations.WorkforceMemberDTO) error {
	if p.ontologyProject == nil {
		return nil
	}
	if member.PersonalAssistant || isPersonalAssistantProfileID(member.AgentProfileID) || isPersonalAssistantProfileID(member.AgentID) {
		return nil
	}
	tenantID := strings.TrimSpace(member.OrganizationID)
	if tenantID == "" {
		return nil
	}
	personID := workforcePersonOntologyID(member.AgentProfileID)
	agentID := workforceAgentOntologyID(member.AgentID)
	positionID := strings.TrimSpace(member.PositionID)
	if personID == "" || agentID == "" || positionID == "" {
		return nil
	}
	displayName := strings.TrimSpace(member.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(member.RealName)
	}
	now := time.Now().UTC()
	version := max(int64(1), now.UnixNano())
	if err := p.applyOntologyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "person-updated:" + personID,
		TenantID: tenantID,
		Type:     "people.person.updated",
		StreamID: personID,
		Version:  version,
		Payload: map[string]any{
			"id":           personID,
			"display_name": displayName,
			"role":         firstWorkforceMemberRoleID(member.RoleIDs),
			"email":        member.UserAccount.Email,
			"status":       workforceMemberOntologyStatus(member),
			"skills":       member.CapabilityTags,
		},
		PublishedBy:     graphprotocol.ActorRef{Kind: viewer.ViewerKind, ID: viewer.ViewerID},
		OccurredAt:      now,
		SourceAccessRef: organizationOntologySourceRef(tenantID),
	}); err != nil {
		return err
	}
	if err := p.applyOntologyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "agent-updated:" + agentID,
		TenantID: tenantID,
		Type:     "people.agent.updated",
		StreamID: agentID,
		Version:  version,
		Payload: map[string]any{
			"id":           agentID,
			"display_name": displayName,
			"runtime_kind": member.RuntimeKind,
		},
		PublishedBy:     graphprotocol.ActorRef{Kind: viewer.ViewerKind, ID: viewer.ViewerID},
		OccurredAt:      now,
		SourceAccessRef: organizationOntologySourceRef(tenantID),
	}); err != nil {
		return err
	}
	roleIDs := member.RoleIDs
	if len(roleIDs) == 0 {
		roleIDs = []string{"position.member"}
	}
	if p.contextGraphCore != nil {
		positionProjected, err := contextGraphObjectExists(ctx, p.contextGraphCore, tenantID, "position", positionID)
		if err != nil {
			return err
		}
		if !positionProjected {
			return nil
		}
	}
	for _, roleID := range roleIDs {
		roleID = strings.TrimSpace(roleID)
		if roleID == "" {
			continue
		}
		assignmentID := strings.TrimSpace(member.AgentProfileID) + ":" + positionID + ":" + roleID
		if err := p.applyOntologyEvent(ctx, graphprotocol.DomainEvent{
			ID:       "position-assigned:" + assignmentID,
			TenantID: tenantID,
			Type:     "people.position.assigned",
			StreamID: assignmentID,
			Version:  version,
			Payload: map[string]any{
				"id":          assignmentID,
				"person_id":   personID,
				"agent_id":    agentID,
				"position_id": positionID,
				"role_id":     roleID,
				"scope":       member.DepartmentID,
				"status":      workforceMemberOntologyStatus(member),
			},
			PublishedBy:     graphprotocol.ActorRef{Kind: viewer.ViewerKind, ID: viewer.ViewerID},
			OccurredAt:      now,
			SourceAccessRef: organizationOntologySourceRef(tenantID),
		}); err != nil {
			return err
		}
	}
	return nil
}

func firstWorkforceMemberRoleID(roleIDs []string) string {
	for _, roleID := range roleIDs {
		if trimmed := strings.TrimSpace(roleID); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func workforcePersonOntologyID(agentProfileID string) string {
	agentProfileID = strings.TrimSpace(agentProfileID)
	if agentProfileID == "" {
		return ""
	}
	return "person:" + agentProfileID
}

func workforceAgentOntologyID(agentID string) string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return ""
	}
	return "agent:" + agentID
}

func workforceMemberOntologyStatus(member operations.WorkforceMemberDTO) string {
	if !member.Enabled {
		return "inactive"
	}
	return "active"
}

func (p productionAPI) DeleteOrganization(ctx context.Context, viewer operations.ViewerRef, organizationID string) error {
	return p.operations.DeleteOrganization(ctx, viewer, organizationID)
}

func (p productionAPI) DeleteOrganizationUnit(ctx context.Context, viewer operations.ViewerRef, departmentID string) error {
	return p.operations.DeleteOrganizationUnit(ctx, viewer, departmentID)
}

func (p productionAPI) DeleteOrganizationPosition(ctx context.Context, viewer operations.ViewerRef, positionID string) error {
	return p.operations.DeleteOrganizationPosition(ctx, viewer, positionID)
}

func (p productionAPI) UpdateOrganizationOwner(ctx context.Context, viewer operations.ViewerRef, cmd operations.UpdateOrganizationOwnerCommand) (operations.OrganizationNodeDTO, error) {
	return p.operations.UpdateOrganizationOwner(ctx, viewer, cmd)
}

func (p productionAPI) CreateWorkforceMember(ctx context.Context, viewer operations.ViewerRef, cmd operations.UpsertWorkforceMemberCommand) (operations.WorkforceMemberDTO, error) {
	member, err := p.operations.CreateWorkforceMember(ctx, viewer, cmd)
	if err != nil {
		return operations.WorkforceMemberDTO{}, err
	}
	if err := p.publishWorkforceMemberAssignedToOntology(ctx, viewer, member); err != nil {
		return operations.WorkforceMemberDTO{}, err
	}
	return member, nil
}

func (p productionAPI) UpsertWorkforceMember(ctx context.Context, viewer operations.ViewerRef, cmd operations.UpsertWorkforceMemberCommand) (operations.WorkforceMemberDTO, error) {
	member, err := p.operations.UpsertWorkforceMember(ctx, viewer, cmd)
	if err != nil {
		return operations.WorkforceMemberDTO{}, err
	}
	if err := p.publishWorkforceMemberAssignedToOntology(ctx, viewer, member); err != nil {
		return operations.WorkforceMemberDTO{}, err
	}
	return member, nil
}

func (p productionAPI) SetWorkforceMemberEnabled(ctx context.Context, viewer operations.ViewerRef, cmd operations.SetWorkforceMemberEnabledCommand) error {
	return p.operations.SetWorkforceMemberEnabled(ctx, viewer, cmd)
}

func (p productionAPI) DeleteWorkforceMember(ctx context.Context, viewer operations.ViewerRef, agentProfileID string) error {
	return p.operations.ReleaseWorkforceMember(ctx, viewer, operations.ReleaseWorkforceMemberCommand{AgentProfileID: agentProfileID, Reason: "released through workforce member route"})
}

func (p productionAPI) ReleaseWorkforceMember(ctx context.Context, viewer operations.ViewerRef, cmd operations.ReleaseWorkforceMemberCommand) error {
	return p.operations.ReleaseWorkforceMember(ctx, viewer, cmd)
}

func (p productionAPI) HireTalentFromMarket(ctx context.Context, viewer operations.ViewerRef, cmd operations.HireTalentFromMarketCommand) (operations.WorkforceMemberDTO, error) {
	member, err := p.operations.HireTalentFromMarket(ctx, viewer, cmd)
	if err != nil {
		return operations.WorkforceMemberDTO{}, err
	}
	if err := p.publishWorkforceMemberAssignedToOntology(ctx, viewer, member); err != nil {
		return operations.WorkforceMemberDTO{}, err
	}
	return member, nil
}

func (p productionAPI) DeleteTalentMarketMember(ctx context.Context, viewer operations.ViewerRef, cmd operations.DeleteTalentMarketMemberCommand) error {
	return p.operations.DeleteTalentMarketMember(ctx, viewer, cmd)
}

func (p productionAPI) DeleteTalentMarketMembers(ctx context.Context, viewer operations.ViewerRef, cmd operations.DeleteTalentMarketMembersCommand) (operations.DeleteTalentMarketMembersResponseDTO, error) {
	return p.operations.DeleteTalentMarketMembers(ctx, viewer, cmd)
}

func (p productionAPI) PreviewTalentMarketImport(ctx context.Context, viewer operations.ViewerRef, req operations.TalentMarketImportPreviewRequest) (operations.TalentMarketImportPreviewResponseDTO, error) {
	return p.operations.PreviewTalentMarketImport(ctx, viewer, req)
}

func (p productionAPI) CommitTalentMarketImport(ctx context.Context, viewer operations.ViewerRef, req operations.TalentMarketImportCommitRequest) (operations.TalentMarketImportCommitResponseDTO, error) {
	return p.operations.CommitTalentMarketImport(ctx, viewer, req)
}

func (p productionAPI) ExportTalentMarket(ctx context.Context, viewer operations.ViewerRef, req operations.TalentMarketExportRequest) (operations.TalentMarketExportPackageDTO, error) {
	return p.operations.ExportTalentMarket(ctx, viewer, req)
}
