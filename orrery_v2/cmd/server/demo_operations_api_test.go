package main

import (
	"context"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/operations"
)

func (d *demoAPI) ListWorkforceMembers(ctx context.Context, viewer operations.ViewerRef, query operations.WorkforceQuery) (operations.WorkforcePageDTO, error) {
	out := d.workforce
	out.Members = make([]operations.WorkforceMemberDTO, 0, len(d.workforce.Members))
	for _, member := range d.workforce.Members {
		if member.PlacementStatus == "market" {
			continue
		}
		if query.EnabledOnly && !member.Enabled {
			continue
		}
		if query.OrganizationID != "" && member.OrganizationID != query.OrganizationID {
			continue
		}
		if query.DepartmentID != "" && member.DepartmentID != query.DepartmentID {
			continue
		}
		out.Members = append(out.Members, member)
	}
	out.Page = operations.PageDTO{Limit: query.Limit, Offset: query.Offset, Total: len(out.Members), Returned: len(out.Members)}
	return out, nil
}

func (d *demoAPI) ListTalentMarketMembers(ctx context.Context, viewer operations.ViewerRef, query operations.WorkforceQuery) (operations.WorkforcePageDTO, error) {
	out := d.workforce
	out.Members = make([]operations.WorkforceMemberDTO, 0, len(d.workforce.Members))
	for _, member := range d.workforce.Members {
		if member.PlacementStatus != "market" {
			continue
		}
		if query.EnabledOnly && !member.Enabled {
			continue
		}
		out.Members = append(out.Members, member)
	}
	out.Page = operations.PageDTO{Limit: query.Limit, Offset: query.Offset, Total: len(out.Members), Returned: len(out.Members)}
	return out, nil
}

func (d *demoAPI) GetOrgGraph(ctx context.Context, viewer operations.ViewerRef, query operations.OrgGraphQuery) (operations.OrgGraphDTO, error) {
	workforce, _ := d.ListWorkforceMembers(ctx, viewer, operations.WorkforceQuery{OrganizationID: query.OrganizationID, EnabledOnly: true})
	out := d.orgGraph
	if query.OrganizationID != "" {
		out.Organizations = filterOrganizations(out.Organizations, query.OrganizationID)
	}
	out.Members = workforce.Members
	out.Departments = filterDepartmentsByMembers(out.Departments, workforce.Members, query.OrganizationID)
	out.Positions = filterPositionsByDepartments(out.Positions, out.Departments, workforce.Members)
	out.ReportingEdges = filterReportingEdgesByMembers(out.ReportingEdges, workforce.Members)
	return out, nil
}

func (d *demoAPI) ListRoleDefinitions(ctx context.Context, viewer operations.ViewerRef) (operations.RoleDefinitionPageDTO, error) {
	roles := operations.BuiltInRoleDefinitions()
	return operations.RoleDefinitionPageDTO{Items: roles, Page: operations.PageDTO{Total: len(roles), Returned: len(roles)}}, nil
}

func (d *demoAPI) PutRoleDefinitions(context.Context, operations.ViewerRef, operations.PutRoleDefinitionsCommand) (operations.RoleDefinitionPageDTO, error) {
	return operations.RoleDefinitionPageDTO{}, operations.ErrServiceDependency
}

func (d *demoAPI) DeleteRoleAssignment(context.Context, operations.ViewerRef, string) error {
	return operations.ErrServiceDependency
}

func (d *demoAPI) ListRoleAssignments(ctx context.Context, viewer operations.ViewerRef, query operations.RoleAssignmentQuery) (operations.RoleAssignmentPageDTO, error) {
	return operations.RoleAssignmentPageDTO{}, operations.ErrServiceDependency
}

func (d *demoAPI) PutRoleAssignments(ctx context.Context, viewer operations.ViewerRef, cmd operations.PutRoleAssignmentsCommand) (operations.RoleAssignmentPageDTO, error) {
	return operations.RoleAssignmentPageDTO{}, operations.ErrServiceDependency
}

func (d *demoAPI) CreateOrganization(ctx context.Context, viewer operations.ViewerRef, cmd operations.CreateOrganizationCommand) (operations.OrganizationDTO, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	organizationID, err := operations.NewPrefixedIDGenerator().NewID("org")
	if err != nil {
		return operations.OrganizationDTO{}, err
	}
	name := strings.TrimSpace(cmd.Name)
	if name == "" {
		return operations.OrganizationDTO{}, operations.ErrInvalidRequest
	}
	organization := operations.OrganizationDTO{
		OrganizationID: organizationID,
		Name:           name,
	}
	d.orgGraph.Organizations = append(d.orgGraph.Organizations, organization)
	if err := d.persistLocked(ctx); err != nil {
		return operations.OrganizationDTO{}, err
	}
	return organization, nil
}

func (d *demoAPI) CreateOrganizationUnit(ctx context.Context, viewer operations.ViewerRef, cmd operations.CreateOrganizationUnitCommand) (operations.DepartmentDTO, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	organizationID := strings.TrimSpace(cmd.OrganizationID)
	name := strings.TrimSpace(cmd.Name)
	defaultPositionName := strings.TrimSpace(cmd.DefaultPositionName)
	if organizationID == "" || name == "" {
		return operations.DepartmentDTO{}, operations.ErrInvalidRequest
	}
	departmentID, err := operations.NewPrefixedIDGenerator().NewID("unit")
	if err != nil {
		return operations.DepartmentDTO{}, err
	}
	defaultPositionID := ""
	if defaultPositionName != "" {
		defaultPositionID, err = operations.NewPrefixedIDGenerator().NewID("position")
		if err != nil {
			return operations.DepartmentDTO{}, err
		}
	}
	unit := operations.DepartmentDTO{
		DepartmentID:       departmentID,
		OrganizationID:     organizationID,
		Name:               name,
		ParentDepartmentID: strings.TrimSpace(cmd.ParentDepartmentID),
		DefaultPositionID:  defaultPositionID,
		MemberCount:        0,
	}
	d.orgGraph.Departments = append(d.orgGraph.Departments, unit)
	if defaultPositionName != "" {
		d.orgGraph.Positions = append(d.orgGraph.Positions, operations.PositionDTO{
			PositionID:   defaultPositionID,
			DepartmentID: departmentID,
			Name:         defaultPositionName,
			MemberCount:  0,
		})
	}
	if err := d.persistLocked(ctx); err != nil {
		return operations.DepartmentDTO{}, err
	}
	return unit, nil
}

func (d *demoAPI) CreateOrganizationPosition(ctx context.Context, viewer operations.ViewerRef, cmd operations.CreateOrganizationPositionCommand) (operations.PositionDTO, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	organizationID := strings.TrimSpace(cmd.OrganizationID)
	departmentID := strings.TrimSpace(cmd.DepartmentID)
	name := strings.TrimSpace(cmd.Name)
	if organizationID == "" || departmentID == "" || name == "" {
		return operations.PositionDTO{}, operations.ErrInvalidRequest
	}
	departmentFound := false
	for _, department := range d.orgGraph.Departments {
		if department.DepartmentID == departmentID && department.OrganizationID == organizationID {
			departmentFound = true
			break
		}
	}
	if !departmentFound {
		return operations.PositionDTO{}, operations.ErrInvalidRequest
	}
	positionID, err := operations.NewPrefixedIDGenerator().NewID("position")
	if err != nil {
		return operations.PositionDTO{}, err
	}
	position := operations.PositionDTO{
		PositionID:   positionID,
		DepartmentID: departmentID,
		Name:         name,
		MemberCount:  0,
	}
	d.orgGraph.Positions = append(d.orgGraph.Positions, position)
	if err := d.persistLocked(ctx); err != nil {
		return operations.PositionDTO{}, err
	}
	return position, nil
}

func (d *demoAPI) DeleteOrganization(ctx context.Context, viewer operations.ViewerRef, organizationID string) error {
	return operations.ErrServiceDependency
}

func (d *demoAPI) DeleteOrganizationUnit(ctx context.Context, viewer operations.ViewerRef, departmentID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	departmentID = strings.TrimSpace(departmentID)
	if departmentID == "" {
		return operations.ErrInvalidRequest
	}
	departmentIndex := -1
	var department operations.DepartmentDTO
	for i, candidate := range d.orgGraph.Departments {
		if candidate.DepartmentID == departmentID {
			departmentIndex = i
			department = candidate
			break
		}
	}
	if departmentIndex < 0 {
		return operations.ErrNotFound
	}
	var organization operations.OrganizationDTO
	for _, candidate := range d.orgGraph.Organizations {
		if candidate.OrganizationID == department.OrganizationID {
			organization = candidate
			break
		}
	}
	if organization.DefaultDepartmentID != "" && organization.DefaultDepartmentID == departmentID {
		return operations.ErrInvalidRequest
	}
	for _, candidate := range d.orgGraph.Departments {
		if candidate.ParentDepartmentID == departmentID {
			return operations.ErrInvalidRequest
		}
	}
	hasDefaultTarget := organization.DefaultDepartmentID != "" && organization.DefaultPositionID != ""
	defaultPositionFound := false
	for _, position := range d.orgGraph.Positions {
		if position.PositionID == organization.DefaultPositionID && position.DepartmentID == organization.DefaultDepartmentID {
			defaultPositionFound = true
			break
		}
	}
	if hasDefaultTarget && !defaultPositionFound {
		return operations.ErrInvalidRequest
	}
	if !hasDefaultTarget {
		for _, member := range d.workforce.Members {
			if member.DepartmentID == departmentID {
				return operations.ErrInvalidRequest
			}
		}
	}
	d.orgGraph.Departments = append(d.orgGraph.Departments[:departmentIndex], d.orgGraph.Departments[departmentIndex+1:]...)
	positions := d.orgGraph.Positions[:0]
	for _, position := range d.orgGraph.Positions {
		if position.DepartmentID != departmentID {
			positions = append(positions, position)
		}
	}
	d.orgGraph.Positions = positions
	for i := range d.workforce.Members {
		if hasDefaultTarget && d.workforce.Members[i].DepartmentID == departmentID {
			d.workforce.Members[i].DepartmentID = organization.DefaultDepartmentID
			d.workforce.Members[i].PositionID = organization.DefaultPositionID
		}
	}
	return d.persistLocked(ctx)
}

func (d *demoAPI) DeleteOrganizationPosition(ctx context.Context, viewer operations.ViewerRef, positionID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	positionID = strings.TrimSpace(positionID)
	if positionID == "" {
		return operations.ErrInvalidRequest
	}
	positionIndex := -1
	for i, position := range d.orgGraph.Positions {
		if position.PositionID == positionID {
			positionIndex = i
			break
		}
	}
	if positionIndex < 0 {
		return operations.ErrNotFound
	}
	for _, member := range d.workforce.Members {
		if member.PositionID == positionID {
			return operations.ErrInvalidRequest
		}
	}
	d.orgGraph.Positions = append(d.orgGraph.Positions[:positionIndex], d.orgGraph.Positions[positionIndex+1:]...)
	return d.persistLocked(ctx)
}

func (d *demoAPI) UpdateOrganizationOwner(context.Context, operations.ViewerRef, operations.UpdateOrganizationOwnerCommand) (operations.OrganizationNodeDTO, error) {
	return operations.OrganizationNodeDTO{}, operations.ErrServiceDependency
}

func (d *demoAPI) GetPermissionMatrix(ctx context.Context, viewer operations.ViewerRef, query operations.PermissionMatrixQuery) (operations.PermissionMatrixDTO, error) {
	return d.permissions, nil
}

func (d *demoAPI) DecidePermission(ctx context.Context, viewer operations.ViewerRef, req operations.PermissionDecisionRequestDTO) (operations.PermissionDecisionDTO, error) {
	return operations.PermissionDecisionDTO{}, operations.ErrServiceDependency
}

func (d *demoAPI) ListAuditLog(ctx context.Context, viewer operations.ViewerRef, query operations.AuditLogQuery) (operations.AuditEventPageDTO, error) {
	var events []operations.AuditEventDTO
	for _, event := range d.audit {
		if query.MissionID != "" && event.MissionID != query.MissionID {
			continue
		}
		events = append(events, event)
	}
	return operations.AuditEventPageDTO{Events: events, Page: operations.PageDTO{Total: len(events), Returned: len(events)}}, nil
}

func (d *demoAPI) ListEvaluations(ctx context.Context, viewer operations.ViewerRef, query operations.EvaluationQuery) (operations.EvaluationPageDTO, error) {
	return d.evaluations, nil
}

func (d *demoAPI) UpsertWorkforceMember(ctx context.Context, viewer operations.ViewerRef, cmd operations.UpsertWorkforceMemberCommand) (operations.WorkforceMemberDTO, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	agentInstanceID := strings.TrimSpace(cmd.AgentInstanceID)
	if agentInstanceID == "" {
		for _, existing := range d.workforce.Members {
			if existing.AgentProfileID == cmd.AgentProfileID {
				agentInstanceID = strings.TrimSpace(existing.AgentInstanceID)
				break
			}
		}
	}
	if strings.TrimSpace(cmd.AgentProfileID) == "" || agentInstanceID == "" {
		return operations.WorkforceMemberDTO{}, operations.ErrInvalidRequest
	}
	member := operations.WorkforceMemberDTO{AgentID: cmd.AgentProfileID, AgentProfileID: cmd.AgentProfileID, AgentInstanceID: agentInstanceID, RealName: cmd.RealName, DisplayName: firstNonBlank(cmd.DisplayName, cmd.RealName), AgentKind: firstNonBlank(cmd.AgentKind, string(agentcore.AgentKindAI)), EmploymentType: cmd.EmploymentType, OrganizationID: cmd.OrganizationID, DepartmentID: cmd.DepartmentID, PositionID: cmd.PositionID, RoleIDs: cmd.RoleIDs, ManagerAgentID: cmd.ManagerAgentID, ReportingLineID: cmd.ReportingLineID, AuthorityScope: cmd.AuthorityScope, Enabled: cmd.Enabled}
	replaced := false
	for i := range d.workforce.Members {
		if d.workforce.Members[i].AgentProfileID == cmd.AgentProfileID {
			d.workforce.Members[i] = member
			replaced = true
			break
		}
	}
	if !replaced {
		d.workforce.Members = append(d.workforce.Members, member)
	}
	d.workforce.Page.Total = len(d.workforce.Members)
	d.workforce.Page.Returned = len(d.workforce.Members)
	if err := d.persistLocked(ctx); err != nil {
		return operations.WorkforceMemberDTO{}, err
	}
	return member, nil
}

func (d *demoAPI) CreateWorkforceMember(ctx context.Context, viewer operations.ViewerRef, cmd operations.UpsertWorkforceMemberCommand) (operations.WorkforceMemberDTO, error) {
	profileID, err := operations.NewPrefixedIDGenerator().NewID("profile")
	if err != nil {
		return operations.WorkforceMemberDTO{}, err
	}
	agentID, err := operations.NewPrefixedIDGenerator().NewID("agent")
	if err != nil {
		return operations.WorkforceMemberDTO{}, err
	}
	cmd.AgentProfileID = profileID
	cmd.AgentInstanceID = agentID
	return d.UpsertWorkforceMember(ctx, viewer, cmd)
}

func (d *demoAPI) SetWorkforceMemberEnabled(ctx context.Context, viewer operations.ViewerRef, cmd operations.SetWorkforceMemberEnabledCommand) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range d.workforce.Members {
		if d.workforce.Members[i].AgentProfileID == cmd.AgentProfileID {
			d.workforce.Members[i].Enabled = cmd.Enabled
		}
	}
	return d.persistLocked(ctx)
}

func (d *demoAPI) DeleteWorkforceMember(ctx context.Context, viewer operations.ViewerRef, agentProfileID string) error {
	return d.ReleaseWorkforceMember(ctx, viewer, operations.ReleaseWorkforceMemberCommand{AgentProfileID: agentProfileID, Reason: "released through workforce member route"})
}

func (d *demoAPI) ReleaseWorkforceMember(ctx context.Context, viewer operations.ViewerRef, cmd operations.ReleaseWorkforceMemberCommand) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	profileID := strings.TrimSpace(cmd.AgentProfileID)
	reason := firstNonBlank(strings.TrimSpace(cmd.Reason), "released to talent market")
	for i := range d.workforce.Members {
		if d.workforce.Members[i].AgentProfileID == profileID {
			d.workforce.Members[i].OrganizationID = ""
			d.workforce.Members[i].DepartmentID = ""
			d.workforce.Members[i].PositionID = ""
			d.workforce.Members[i].RoleIDs = nil
			d.workforce.Members[i].ManagerAgentID = ""
			d.workforce.Members[i].ReportingLineID = ""
			d.workforce.Members[i].AuthorityScope = ""
			d.workforce.Members[i].Enabled = false
			d.workforce.Members[i].PlacementStatus = "market"
			d.workforce.Members[i].ReleasedReason = reason
			if err := d.persistLocked(ctx); err != nil {
				return err
			}
			return nil
		}
	}
	return missioncontrol.ErrProjectionNotFound
}

func (d *demoAPI) HireTalentFromMarket(ctx context.Context, viewer operations.ViewerRef, cmd operations.HireTalentFromMarketCommand) (operations.WorkforceMemberDTO, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	profileID := strings.TrimSpace(cmd.AgentProfileID)
	for i := range d.workforce.Members {
		if d.workforce.Members[i].AgentProfileID == profileID {
			if d.workforce.Members[i].PlacementStatus != "market" {
				return operations.WorkforceMemberDTO{}, operations.ErrInvalidRequest
			}
			d.workforce.Members[i].OrganizationID = strings.TrimSpace(cmd.OrganizationID)
			d.workforce.Members[i].DepartmentID = strings.TrimSpace(cmd.DepartmentID)
			d.workforce.Members[i].PositionID = strings.TrimSpace(cmd.PositionID)
			d.workforce.Members[i].RoleIDs = append([]string(nil), cmd.RoleIDs...)
			d.workforce.Members[i].ManagerAgentID = strings.TrimSpace(cmd.ManagerAgentID)
			d.workforce.Members[i].ReportingLineID = strings.TrimSpace(cmd.ReportingLineID)
			d.workforce.Members[i].Enabled = true
			d.workforce.Members[i].PlacementStatus = "internal"
			d.workforce.Members[i].ReleasedReason = ""
			if err := d.persistLocked(ctx); err != nil {
				return operations.WorkforceMemberDTO{}, err
			}
			return d.workforce.Members[i], nil
		}
	}
	return operations.WorkforceMemberDTO{}, missioncontrol.ErrProjectionNotFound
}

func (d *demoAPI) DeleteTalentMarketMember(ctx context.Context, viewer operations.ViewerRef, cmd operations.DeleteTalentMarketMemberCommand) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	profileID := strings.TrimSpace(cmd.AgentProfileID)
	if profileID == "" {
		return operations.ErrInvalidRequest
	}
	for i, member := range d.workforce.Members {
		if member.AgentProfileID != profileID {
			continue
		}
		if member.PlacementStatus != "market" {
			return operations.ErrInvalidRequest
		}
		d.workforce.Members = append(d.workforce.Members[:i], d.workforce.Members[i+1:]...)
		d.workforce.Page.Total = len(d.workforce.Members)
		d.workforce.Page.Returned = len(d.workforce.Members)
		return d.persistLocked(ctx)
	}
	return missioncontrol.ErrProjectionNotFound
}

func (d *demoAPI) DeleteTalentMarketMembers(ctx context.Context, viewer operations.ViewerRef, cmd operations.DeleteTalentMarketMembersCommand) (operations.DeleteTalentMarketMembersResponseDTO, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(cmd.AgentProfileIDs) == 0 {
		return operations.DeleteTalentMarketMembersResponseDTO{}, operations.ErrInvalidRequest
	}
	var response operations.DeleteTalentMarketMembersResponseDTO
	seen := map[string]bool{}
	for _, rawID := range cmd.AgentProfileIDs {
		profileID := strings.TrimSpace(rawID)
		if profileID == "" {
			response.Skipped = append(response.Skipped, operations.TalentMarketDeleteSkippedDTO{AgentProfileID: "", Reason: "invalid_id"})
			continue
		}
		if seen[profileID] {
			response.Skipped = append(response.Skipped, operations.TalentMarketDeleteSkippedDTO{AgentProfileID: profileID, Reason: "duplicate"})
			continue
		}
		seen[profileID] = true
		index := -1
		for i, member := range d.workforce.Members {
			if member.AgentProfileID == profileID {
				index = i
				if member.PlacementStatus != "market" {
					response.Skipped = append(response.Skipped, operations.TalentMarketDeleteSkippedDTO{AgentProfileID: profileID, Reason: "not_market"})
				}
				break
			}
		}
		if index < 0 {
			response.Skipped = append(response.Skipped, operations.TalentMarketDeleteSkippedDTO{AgentProfileID: profileID, Reason: "not_found"})
			continue
		}
		if len(response.Skipped) > 0 && response.Skipped[len(response.Skipped)-1].AgentProfileID == profileID && response.Skipped[len(response.Skipped)-1].Reason == "not_market" {
			continue
		}
		d.workforce.Members = append(d.workforce.Members[:index], d.workforce.Members[index+1:]...)
		response.Deleted = append(response.Deleted, profileID)
	}
	d.workforce.Page.Total = len(d.workforce.Members)
	d.workforce.Page.Returned = len(d.workforce.Members)
	response.DeletedCount = len(response.Deleted)
	if err := d.persistLocked(ctx); err != nil {
		return operations.DeleteTalentMarketMembersResponseDTO{}, err
	}
	return response, nil
}

func (d *demoAPI) PreviewTalentMarketImport(ctx context.Context, viewer operations.ViewerRef, req operations.TalentMarketImportPreviewRequest) (operations.TalentMarketImportPreviewResponseDTO, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	response := operations.TalentMarketImportPreviewResponseDTO{}
	for i, candidate := range req.Candidates {
		status := "create"
		var conflicts []operations.TalentMarketImportConflictDTO
		for _, member := range d.workforce.Members {
			if member.RealName != "" && candidate.RealName != "" && strings.EqualFold(member.RealName, candidate.RealName) {
				status = "conflict"
				conflict := operations.TalentMarketImportConflictDTO{
					CandidateIndex:          i,
					ConflictType:            "real_name",
					ExistingAgentProfileID:  member.AgentProfileID,
					ExistingRealName:        member.RealName,
					ExistingDisplayName:     member.DisplayName,
					ExistingPlacementStatus: firstNonBlank(member.PlacementStatus, "internal"),
					CandidateRealName:       candidate.RealName,
					CandidateDisplayName:    candidate.DisplayName,
					CandidateSlug:           candidate.SourceSlug,
				}
				conflicts = append(conflicts, conflict)
				response.Conflicts = append(response.Conflicts, conflict)
				break
			}
		}
		response.Items = append(response.Items, operations.TalentMarketImportPreviewItemDTO{CandidateIndex: i, Candidate: candidate, Status: status, Conflicts: conflicts})
	}
	return response, nil
}

func (d *demoAPI) CommitTalentMarketImport(ctx context.Context, viewer operations.ViewerRef, req operations.TalentMarketImportCommitRequest) (operations.TalentMarketImportCommitResponseDTO, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	response := operations.TalentMarketImportCommitResponseDTO{}
	for i, candidate := range req.Candidates {
		resolution := strings.TrimSpace(candidate.ConflictResolution)
		if resolution == "skip" {
			response.Skipped = append(response.Skipped, operations.TalentMarketImportSkippedDTO{CandidateIndex: i, RealName: candidate.RealName, Reason: "skipped"})
			continue
		}
		profileID := strings.TrimSpace(candidate.AgentProfileID)
		if profileID == "" {
			generated, err := operations.NewPrefixedIDGenerator().NewID("profile")
			if err != nil {
				return operations.TalentMarketImportCommitResponseDTO{}, err
			}
			profileID = generated
		}
		realName := strings.TrimSpace(candidate.RealName)
		if resolution == "rename" && strings.TrimSpace(candidate.ResolvedRealName) != "" {
			realName = strings.TrimSpace(candidate.ResolvedRealName)
		}
		if realName == "" {
			realName = firstNonBlank(candidate.DisplayName, candidate.SourceSlug, "导入 Agent")
		}
		member := operations.WorkforceMemberDTO{
			AgentID:              profileID,
			AgentProfileID:       profileID,
			AgentInstanceID:      profileID,
			RealName:             realName,
			DisplayName:          firstNonBlank(candidate.DisplayName, realName),
			RoleName:             firstNonBlank(candidate.RoleName, candidate.Category),
			AgentKind:            firstNonBlank(candidate.AgentKind, string(agentcore.AgentKindAI)),
			EmploymentType:       "ai_agent",
			PlacementStatus:      "market",
			RuntimeKind:          candidate.RuntimeKind,
			RuntimeConfigRef:     candidate.RuntimeConfigRef,
			RuntimeConfig:        candidate.RuntimeConfig,
			Persona:              candidate.Persona,
			PersonalMemoryRef:    candidate.PersonalMemoryRef,
			CollectiveMemoryRefs: candidate.CollectiveMemoryRefs,
			SkillRefs:            candidate.SkillRefs,
			ToolRefs:             candidate.ToolRefs,
			QAEnabled:            candidate.QAEnabled,
			Enabled:              candidate.Enabled,
		}
		if !member.Enabled {
			member.AvailabilityStatus = "disabled"
		}
		replaced := false
		if resolution == "overwrite" {
			for idx := range d.workforce.Members {
				if strings.EqualFold(d.workforce.Members[idx].RealName, candidate.RealName) || d.workforce.Members[idx].AgentProfileID == profileID {
					member.AgentID = d.workforce.Members[idx].AgentID
					member.AgentProfileID = d.workforce.Members[idx].AgentProfileID
					member.AgentInstanceID = d.workforce.Members[idx].AgentInstanceID
					d.workforce.Members[idx] = member
					replaced = true
					break
				}
			}
		}
		if !replaced {
			d.workforce.Members = append(d.workforce.Members, member)
		}
		response.Imported = append(response.Imported, member)
	}
	d.workforce.Page.Total = len(d.workforce.Members)
	d.workforce.Page.Returned = len(d.workforce.Members)
	if err := d.persistLocked(ctx); err != nil {
		return operations.TalentMarketImportCommitResponseDTO{}, err
	}
	return response, nil
}

func (d *demoAPI) ExportTalentMarket(ctx context.Context, viewer operations.ViewerRef, req operations.TalentMarketExportRequest) (operations.TalentMarketExportPackageDTO, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	wanted := map[string]bool{}
	for _, id := range req.AgentProfileIDs {
		wanted[strings.TrimSpace(id)] = true
	}
	pkg := operations.TalentMarketExportPackageDTO{
		Manifest: operations.TalentMarketExportManifestDTO{Format: "orrery.talent-market", Version: 1, SourceApp: "orrery-demo"},
	}
	for _, member := range d.workforce.Members {
		if !wanted[member.AgentProfileID] || member.PlacementStatus != "market" {
			continue
		}
		pkg.Agents = append(pkg.Agents, operations.TalentMarketImportCandidateDTO{
			AgentProfileID:       member.AgentProfileID,
			RealName:             member.RealName,
			DisplayName:          member.DisplayName,
			RoleName:             member.RoleName,
			AgentKind:            member.AgentKind,
			RuntimeKind:          member.RuntimeKind,
			RuntimeConfigRef:     member.RuntimeConfigRef,
			RuntimeConfig:        member.RuntimeConfig,
			Persona:              member.Persona,
			PersonalMemoryRef:    member.PersonalMemoryRef,
			CollectiveMemoryRefs: member.CollectiveMemoryRefs,
			SkillRefs:            member.SkillRefs,
			ToolRefs:             member.ToolRefs,
			QAEnabled:            member.QAEnabled,
			Enabled:              member.Enabled,
		})
	}
	pkg.Manifest.Count = len(pkg.Agents)
	return pkg, nil
}

func filterAuditByMission(events []operations.AuditEventDTO, missionID string) []operations.AuditEventDTO {
	next := events[:0]
	for _, event := range events {
		if event.MissionID != missionID {
			next = append(next, event)
		}
	}
	return next
}

func filterEvaluationsByMission(items []operations.EvaluationItemDTO, missionID string) []operations.EvaluationItemDTO {
	next := items[:0]
	for _, item := range items {
		if item.MissionID != missionID {
			next = append(next, item)
		}
	}
	return next
}

func filterEvaluationsByAgents(items []operations.EvaluationItemDTO, agentIDs map[string]bool) []operations.EvaluationItemDTO {
	next := items[:0]
	for _, item := range items {
		if !agentIDs[item.EntityID] {
			next = append(next, item)
		}
	}
	return next
}

func filterPermissionRows(rows []operations.EffectivePermissionRowDTO, agentIDs map[string]bool) []operations.EffectivePermissionRowDTO {
	next := rows[:0]
	for _, row := range rows {
		if !agentIDs[row.AgentID] {
			next = append(next, row)
		}
	}
	return next
}

func filterOrganizations(organizations []operations.OrganizationDTO, organizationID string) []operations.OrganizationDTO {
	next := organizations[:0]
	for _, organization := range organizations {
		if organization.OrganizationID == organizationID {
			next = append(next, organization)
		}
	}
	return next
}

func filterDepartmentsByMembers(departments []operations.DepartmentDTO, members []operations.WorkforceMemberDTO, organizationID string) []operations.DepartmentDTO {
	counts := map[string]int{}
	for _, member := range members {
		if member.DepartmentID != "" {
			counts[member.DepartmentID]++
		}
	}
	next := departments[:0]
	for _, department := range departments {
		if organizationID != "" && department.OrganizationID != organizationID {
			continue
		}
		count := counts[department.DepartmentID]
		if count == 0 && department.OrganizationID == "" {
			continue
		}
		department.MemberCount = count
		next = append(next, department)
	}
	return next
}

func filterPositionsByMembers(positions []operations.PositionDTO, members []operations.WorkforceMemberDTO) []operations.PositionDTO {
	counts := map[string]int{}
	for _, member := range members {
		if member.PositionID != "" {
			counts[member.PositionID]++
		}
	}
	next := positions[:0]
	for _, position := range positions {
		count := counts[position.PositionID]
		if count == 0 && position.DepartmentID == "" {
			continue
		}
		position.MemberCount = count
		next = append(next, position)
	}
	return next
}

func filterPositionsByDepartments(positions []operations.PositionDTO, departments []operations.DepartmentDTO, members []operations.WorkforceMemberDTO) []operations.PositionDTO {
	departmentIDs := map[string]bool{}
	for _, department := range departments {
		departmentIDs[department.DepartmentID] = true
	}
	counts := map[string]int{}
	for _, member := range members {
		if member.PositionID != "" {
			counts[member.PositionID]++
		}
	}
	next := positions[:0]
	for _, position := range positions {
		if position.DepartmentID != "" && !departmentIDs[position.DepartmentID] {
			continue
		}
		position.MemberCount = counts[position.PositionID]
		next = append(next, position)
	}
	return next
}

func filterReportingEdgesByMembers(edges []operations.ReportingEdgeDTO, members []operations.WorkforceMemberDTO) []operations.ReportingEdgeDTO {
	ids := map[string]bool{}
	for _, member := range members {
		ids[member.AgentID] = true
		ids[member.AgentProfileID] = true
	}
	next := edges[:0]
	for _, edge := range edges {
		if ids[edge.FromAgentID] && ids[edge.ToManagerAgentID] {
			next = append(next, edge)
		}
	}
	return next
}

func clampOffset(offset, total int) int {
	if offset < 0 {
		return 0
	}
	if offset > total {
		return total
	}
	return offset
}
