package operations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/userauth"
	knowledgecore "orrery_v2/internal/knowledge/core"
)

func (s *CommandService) CreateWorkforceMember(ctx context.Context, viewer ViewerRef, cmd UpsertWorkforceMemberCommand) (WorkforceMemberDTO, error) {
	profileID, err := s.newID("profile")
	if err != nil {
		return WorkforceMemberDTO{}, err
	}
	cmd.AgentProfileID = profileID
	return s.UpsertWorkforceMember(ctx, viewer, cmd)
}

func (s *CommandService) UpsertWorkforceMember(ctx context.Context, viewer ViewerRef, cmd UpsertWorkforceMemberCommand) (WorkforceMemberDTO, error) {
	if s == nil || s.profiles == nil {
		return WorkforceMemberDTO{}, ErrServiceDependency
	}
	if err := requireOperationsPermission(viewer, PermissionWorkforceManage); err != nil {
		return WorkforceMemberDTO{}, err
	}
	profileIDText := strings.TrimSpace(cmd.AgentProfileID)
	if profileIDText == "" {
		return WorkforceMemberDTO{}, fmt.Errorf("%w: agent_profile_id is required", ErrInvalidRequest)
	}
	if userauth.IsReservedWorkforceProfileID(profileIDText) {
		return WorkforceMemberDTO{}, fmt.Errorf("%w: agent_profile_id is reserved for a non-workforce principal", ErrInvalidRequest)
	}
	profileID := agentcore.AgentProfileID(profileIDText)
	profile, err := s.profiles.GetProfile(ctx, profileID)
	hadPreviousProfile := err == nil
	if hadPreviousProfile && profile.IsPersonalAssistant() {
		return WorkforceMemberDTO{}, ErrNotFound
	}
	if err != nil {
		if cmd.partialUpdate() {
			return WorkforceMemberDTO{}, ErrNotFound
		}
		profile = agentcore.AgentProfile{ID: profileID, SkillRefs: []agentcore.SkillRef{}, ToolRefs: []agentcore.ToolRef{}, CollectiveMemoryScopeRefs: []agentcore.MemoryProfileRef{}}
	}
	previousProfile := profile
	partial := hadPreviousProfile && cmd.partialUpdate()
	canonicalName := strings.TrimSpace(firstNonEmpty(cmd.DisplayName, cmd.RealName))
	if partial && !cmd.updatesAny("display_name", "real_name") {
		canonicalName = strings.TrimSpace(firstNonEmpty(profile.DisplayName, profile.RealName))
	}
	organizationID := strings.TrimSpace(cmd.OrganizationID)
	departmentID := strings.TrimSpace(cmd.DepartmentID)
	positionID := strings.TrimSpace(cmd.PositionID)
	if partial && !cmd.updatesField("organization_id") {
		organizationID = string(profile.OrgIdentity.OrganizationRef.ID)
	}
	if partial && !cmd.updatesField("department_id") {
		departmentID = string(profile.OrgIdentity.DepartmentRef.ID)
	}
	if partial && !cmd.updatesField("position_id") {
		positionID = string(profile.OrgIdentity.PositionRef.ID)
	}
	if canonicalName == "" || organizationID == "" || departmentID == "" || positionID == "" {
		return WorkforceMemberDTO{}, fmt.Errorf("%w: agent_profile_id, display_name, organization_id, department_id and position_id are required", ErrInvalidRequest)
	}
	validateCmd := cmd
	validateCmd.AgentProfileID = profileIDText
	validateCmd.OrganizationID = organizationID
	validateCmd.DepartmentID = departmentID
	validateCmd.PositionID = positionID
	if err := s.validateWorkforceMemberOrgRefs(ctx, validateCmd); err != nil {
		return WorkforceMemberDTO{}, err
	}
	if err := s.authorizeOperationObject(ctx, viewer, authorizationActionWorkforceManage, authorizationObjectDepartment, departmentID, organizationID); err != nil {
		return WorkforceMemberDTO{}, err
	}
	if hadPreviousProfile && previousProfile.Placement.IsInternal(previousProfile.OrgIdentity) {
		previousOrganizationID := strings.TrimSpace(string(previousProfile.OrgIdentity.OrganizationRef.ID))
		previousDepartmentID := strings.TrimSpace(string(previousProfile.OrgIdentity.DepartmentRef.ID))
		if previousOrganizationID != "" && previousDepartmentID != "" && (previousOrganizationID != organizationID || previousDepartmentID != departmentID) {
			if err := s.authorizeOperationObject(ctx, viewer, authorizationActionWorkforceManage, authorizationObjectDepartment, previousDepartmentID, previousOrganizationID); err != nil {
				return WorkforceMemberDTO{}, err
			}
		}
	}
	agentInstanceID, err := s.existingWorkforceAgentInstanceID(ctx, profileID)
	if err != nil {
		return WorkforceMemberDTO{}, err
	}
	profile.ID = profileID
	if !partial || cmd.updatesAny("display_name", "real_name") {
		profile.RealName = canonicalName
		profile.DisplayName = canonicalName
	}
	if !partial || cmd.updatesField("email") {
		profile.Email = strings.TrimSpace(cmd.Email)
	}
	if (!partial || cmd.updatesPrefix("person_source")) && !preservePeopleAuthoritativePersonSource(previousProfile, hadPreviousProfile, cmd.PersonSource) {
		profile.PersonSource = agentcore.PersonSourceFacts{
			EmployeeNumber: strings.TrimSpace(cmd.PersonSource.EmployeeNumber),
			Source:         strings.TrimSpace(cmd.PersonSource.Source),
			SourceRecordID: strings.TrimSpace(cmd.PersonSource.SourceRecordID),
			SourceTenantID: strings.TrimSpace(cmd.PersonSource.SourceTenantID),
		}
	}
	if !partial || cmd.updatesField("agent_kind") || profile.AgentKind == "" {
		profile.AgentKind = normalizeAgentKind(cmd.AgentKind)
	}
	if err := s.applyUserAccountCommand(&profile, cmd); err != nil {
		return WorkforceMemberDTO{}, err
	}
	if err := s.validateUniqueWorkforceUsername(ctx, profile); err != nil {
		return WorkforceMemberDTO{}, err
	}
	orgIdentity := agentcore.AgentOrgIdentity{}
	if partial {
		orgIdentity = profile.OrgIdentity
	}
	orgIdentity.OrganizationRef = agentcore.OrganizationRef{ID: agentcore.OrganizationID(organizationID)}
	orgIdentity.DepartmentRef = agentcore.DepartmentRef{ID: agentcore.DepartmentID(departmentID)}
	orgIdentity.PositionRef = agentcore.PositionRef{ID: agentcore.PositionID(positionID)}
	if !partial || cmd.updatesField("role_ids") {
		orgIdentity.RoleRefs = roleRefs(cmd.RoleIDs)
	}
	if !partial || cmd.updatesField("manager_agent_id") {
		orgIdentity.ManagerAgentRef = agentcore.AgentProfileRef{ID: agentcore.AgentProfileID(strings.TrimSpace(cmd.ManagerAgentID))}
	}
	if !partial || cmd.updatesField("reporting_line_id") {
		orgIdentity.ReportingLineRef = agentcore.ReportingLineRef{ID: agentcore.ReportingLineID(strings.TrimSpace(cmd.ReportingLineID))}
	}
	if !partial || cmd.updatesField("authority_scope") {
		orgIdentity.AuthorityScope = strings.TrimSpace(cmd.AuthorityScope)
	}
	if !partial || cmd.updatesField("employment_type") || orgIdentity.EmploymentType == "" {
		orgIdentity.EmploymentType = normalizeEmploymentType(cmd.EmploymentType, string(profile.AgentKind))
	}
	if !partial || cmd.updatesField("supervision_policy") || cmd.updatesField("manager_agent_id") || orgIdentity.SupervisionPolicy == "" {
		orgIdentity.SupervisionPolicy = normalizeSupervisionPolicy(cmd.SupervisionPolicy, orgIdentity.ManagerAgentRef.ID.String())
	}
	profile.OrgIdentity = orgIdentity
	rolesExplicitlyManaged := !partial || cmd.updatesField("role_ids")
	if rolesExplicitlyManaged && profile.AgentKind == agentcore.AgentKindHuman {
		// Personnel roles are authoritative. Account-level scopes are retained only
		// for untouched legacy profiles.
		profile.UserAccount.PermissionScope = nil
	}

	runtimeSpec := agentcore.RuntimeSpec{}
	if partial {
		runtimeSpec = profile.RuntimeSpec
	}
	if !partial || cmd.updatesField("runtime_kind") {
		runtimeSpec.Kind = normalizeRuntimeKind(cmd.RuntimeKind, profile.AgentKind)
	}
	if !partial || cmd.updatesField("runtime_config_ref") {
		runtimeSpec.ConfigRef = strings.TrimSpace(cmd.RuntimeConfigRef)
		if runtimeSpec.ConfigRef != "" {
			runtimeSpec.Config = nil
		}
	}
	if !partial || cmd.updatesField("runtime_config") {
		if strings.TrimSpace(runtimeSpec.ConfigRef) == "" {
			runtimeSpec.Config = cloneRawMessage(cmd.RuntimeConfig)
		} else {
			runtimeSpec.Config = nil
		}
	}
	if partial && cmd.updatesField("agent_kind") && previousProfile.AgentKind != profile.AgentKind && profile.AgentKind == agentcore.AgentKindHuman && !cmd.updatesAny("runtime_kind", "runtime_config_ref", "runtime_config") {
		runtimeSpec = agentcore.RuntimeSpec{}
	}
	profile.RuntimeSpec = runtimeSpec
	if !partial || cmd.updatesField("workspace_root") {
		profile.WorkspaceRoot = strings.TrimSpace(cmd.WorkspaceRoot)
	}

	persona := agentcore.AgentPersona{}
	if partial {
		persona = profile.Persona
	}
	if !partial || cmd.updatesField("persona.backstory") {
		persona.Backstory = strings.TrimSpace(cmd.Persona.Backstory)
	}
	if !partial || cmd.updatesField("persona.responsibility_boundary") {
		persona.ResponsibilityBoundary = strings.TrimSpace(cmd.Persona.ResponsibilityBoundary)
	}
	if !partial || cmd.updatesField("persona.behavior_boundary") {
		persona.BehaviorBoundary = strings.TrimSpace(cmd.Persona.BehaviorBoundary)
	}
	profile.Persona = persona
	ensureManagedAgentChineseIdentity(&profile)
	profile.BaseAgent = defaultManagedBaseAgentSpec(profile)
	if partial && cmd.updatesField("system_prompt") {
		profile.BaseAgent.SystemPrompt = strings.TrimSpace(cmd.SystemPrompt)
	} else if prompt := strings.TrimSpace(cmd.SystemPrompt); prompt != "" {
		profile.BaseAgent.SystemPrompt = prompt
	}
	applyBaseAgentLLMProfile(&profile.BaseAgent, cmd.BaseAgentLLMProfile)
	if !partial || cmd.updatesField("personal_memory_id") {
		if strings.TrimSpace(cmd.PersonalMemoryID) != "" {
			profile.PersonalMemoryProfileRef = agentcore.MemoryProfileRef{
				MemoryProfileID: agentcore.MemoryProfileID(strings.TrimSpace(cmd.PersonalMemoryID)),
				OwnerAgentID:    agentcore.AgentID(agentInstanceID),
				MemoryKind:      agentcore.MemoryKindPersonal,
			}
		} else if partial {
			profile.PersonalMemoryProfileRef = agentcore.MemoryProfileRef{}
		}
	}
	if (!partial && cmd.CollectiveMemoryRefs != nil) || (partial && cmd.updatesField("collective_memory_refs")) {
		profile.CollectiveMemoryScopeRefs = memoryRefsFromDTO(cmd.CollectiveMemoryRefs)
	}
	if (!partial && cmd.SkillRefs != nil) || (partial && cmd.updatesField("skill_refs")) {
		profile.SkillRefs = skillRefsFromDTO(cmd.SkillRefs)
	}
	if (!partial && cmd.ToolRefs != nil) || (partial && cmd.updatesField("tool_refs")) {
		profile.ToolRefs = toolRefsFromDTO(cmd.ToolRefs)
	}
	if !partial || cmd.updatesField("qa_enabled") {
		profile.QAEnabled = cmd.QAEnabled && profile.AgentKind != agentcore.AgentKindHuman
	}
	if profile.AgentKind == agentcore.AgentKindHuman {
		profile.QAEnabled = false
	}
	if !partial || cmd.updatesField("enabled") {
		profile.Enabled = cmd.Enabled
	}
	if profile.SkillRefs == nil {
		profile.SkillRefs = []agentcore.SkillRef{}
	}
	if profile.ToolRefs == nil {
		profile.ToolRefs = []agentcore.ToolRef{}
	}
	ensureDefaultAgentReadCapabilities(&profile)
	if profile.PersonalMemoryProfileRef.IsZero() && profile.ID != "" {
		profile.PersonalMemoryProfileRef = agentcore.MemoryProfileRef{
			MemoryProfileID: agentcore.MemoryProfileID("memory-" + profile.ID.String()),
			OwnerAgentID:    agentcore.AgentID(agentInstanceID),
			MemoryKind:      agentcore.MemoryKindPersonal,
		}
	}
	if err := s.attachRequiredCollectiveMemoryProfiles(ctx, &profile); err != nil {
		return WorkforceMemberDTO{}, err
	}
	if err := s.validateWorkforceUserAccount(profile); err != nil {
		return WorkforceMemberDTO{}, err
	}
	if errs := agentcore.ValidateAgentProfile(profile); len(errs) > 0 {
		return WorkforceMemberDTO{}, fmt.Errorf("%w: agent profile invalid: %v", ErrInvalidRequest, errs)
	}
	if err := s.authorizeWorkforceAccountSecurityChanges(ctx, viewer, optionalWorkforceProfile(previousProfile, hadPreviousProfile), profile); err != nil {
		return WorkforceMemberDTO{}, err
	}
	var previousRoleProfile *agentcore.AgentProfile
	if hadPreviousProfile {
		previousRoleProfile = &previousProfile
	}
	shouldSyncRoles := !partial || cmd.updatesAny("role_ids", "organization_id", "department_id", "agent_kind")
	var rolePlan *workforceRoleAssignmentPlan
	if shouldSyncRoles {
		rolePlan, err = s.planWorkforceRoleAssignments(ctx, previousRoleProfile, profile, viewer.ViewerID, true)
		if err != nil {
			return WorkforceMemberDTO{}, err
		}
	}
	if err := s.authorizeWorkforceRoleChanges(ctx, viewer, previousRoleProfile, profile, rolePlan); err != nil {
		return WorkforceMemberDTO{}, err
	}
	ownerType := profileKnowledgeOwnerType(string(profile.AgentKind))
	createdKnowledgeBaseID := ""
	if strings.TrimSpace(profile.PersonalKnowledgeBaseID) == "" {
		knowledgeBaseID, err := s.createLifecycleKnowledgeBase(ctx, viewer, ownerType, profile.ID.String(), personalKnowledgeBaseName(profile.DisplayName), "人员个人基础知识库", knowledgecore.VisibilityPrivate)
		if err != nil {
			return WorkforceMemberDTO{}, err
		}
		profile.PersonalKnowledgeBaseID = knowledgeBaseID
		createdKnowledgeBaseID = knowledgeBaseID
	}
	sessionUsernames := workforceSessionRevocationUsernames(previousProfile, profile, hadPreviousProfile)
	// Fail before committing a credential or account-state change if the session store is unavailable.
	if err := s.revokeWorkforceSessions(ctx, sessionUsernames...); err != nil {
		return WorkforceMemberDTO{}, s.cleanupLifecycleKnowledgeBaseOnError(ctx, viewer, ownerType, profile.ID.String(), createdKnowledgeBaseID, err)
	}
	if err := s.profiles.SaveProfile(ctx, profile); err != nil {
		return WorkforceMemberDTO{}, s.cleanupLifecycleKnowledgeBaseOnError(ctx, viewer, ownerType, profile.ID.String(), createdKnowledgeBaseID, err)
	}
	if err := s.applyWorkforceRoleAssignmentPlan(ctx, rolePlan); err != nil {
		rollbackErr := s.restoreWorkforceProfileRecord(ctx, previousProfile, profile, hadPreviousProfile)
		combined := errors.Join(err, rollbackErr)
		return WorkforceMemberDTO{}, s.cleanupLifecycleKnowledgeBaseOnError(ctx, viewer, ownerType, profile.ID.String(), createdKnowledgeBaseID, combined)
	}
	// Delete again after the profile write to close the concurrent login window.
	if err := s.revokeWorkforceSessions(ctx, sessionUsernames...); err != nil {
		failClosedErr := s.failClosedWorkforceLoginAfterRevocationFailure(ctx, previousProfile, profile, hadPreviousProfile)
		return WorkforceMemberDTO{}, errors.Join(err, failClosedErr)
	}
	if err := s.ensurePersonalMemoryProfile(ctx, profile, !partial || cmd.updatesField("enabled")); err != nil {
		return WorkforceMemberDTO{}, err
	}
	status := agentcore.AvailabilityAvailable
	if !profile.Enabled {
		status = agentcore.AvailabilityDisabled
	}
	if err := s.profiles.SaveAvailability(ctx, agentcore.AgentAvailability{
		AgentProfileID:  profile.ID,
		AgentInstanceID: agentInstanceID,
		Status:          status,
		Source:          "operations.workforce",
		UpdatedAt:       time.Now().UTC(),
	}); err != nil {
		return WorkforceMemberDTO{}, err
	}
	if hadPreviousProfile {
		if err := s.deleteWorkforceMemberPlacementRelation(ctx, previousProfile); err != nil {
			return WorkforceMemberDTO{}, errors.Join(err, s.restoreWorkforceRoleAssignmentPlan(ctx, rolePlan), s.restoreWorkforceProfile(ctx, previousProfile, profile, true, false))
		}
	}
	if err := s.writeWorkforceMemberPlacementRelation(ctx, profile); err != nil {
		return WorkforceMemberDTO{}, errors.Join(err, s.restoreWorkforceRoleAssignmentPlan(ctx, rolePlan), s.restoreWorkforceProfile(ctx, previousProfile, profile, hadPreviousProfile, hadPreviousProfile))
	}
	if err := s.recordAudit(ctx, viewer, "workforce.member.upsert", profileIDText, map[string]string{
		"agent_id":          profileIDText,
		"agent_profile_id":  profileIDText,
		"agent_instance_id": agentInstanceID.String(),
		"organization_id":   organizationID,
		"department_id":     departmentID,
		"position_id":       positionID,
	}); err != nil {
		return WorkforceMemberDTO{}, err
	}
	member := mapMemberFromProfile(entryFromProfile(profile, agentInstanceID.String()), profile)
	if err := s.applyWorkforceMemberOrgDisplayNames(ctx, &member); err != nil {
		return WorkforceMemberDTO{}, err
	}
	if err := s.applyWorkforceAccountProjection(ctx, profile, &member); err != nil {
		return WorkforceMemberDTO{}, err
	}
	return member, nil
}

func preservePeopleAuthoritativePersonSource(previous agentcore.AgentProfile, present bool, incoming WorkforcePersonSourceFactsDTO) bool {
	return present &&
		strings.EqualFold(strings.TrimSpace(previous.PersonSource.Source), "people") &&
		strings.EqualFold(strings.TrimSpace(incoming.Source), "people.feishu")
}

func (s *CommandService) applyWorkforceMemberOrgDisplayNames(ctx context.Context, member *WorkforceMemberDTO) error {
	if s == nil || s.orgs == nil || member == nil {
		return nil
	}
	graph, err := s.orgs.ListOrganizations(ctx, OrgGraphQuery{OrganizationID: member.OrganizationID})
	if err != nil {
		return err
	}
	for _, department := range graph.Departments {
		if department.DepartmentID == member.DepartmentID && department.Name != "" {
			member.DepartmentDisplayName = department.Name
			break
		}
	}
	for _, position := range graph.Positions {
		if position.PositionID == member.PositionID && position.Name != "" {
			member.PositionDisplayName = position.Name
			break
		}
	}
	return nil
}

func (s *CommandService) validateWorkforceMemberOrgRefs(ctx context.Context, cmd UpsertWorkforceMemberCommand) error {
	if s == nil || s.orgs == nil {
		return ErrServiceDependency
	}
	organizationID := strings.TrimSpace(cmd.OrganizationID)
	departmentID := strings.TrimSpace(cmd.DepartmentID)
	positionID := strings.TrimSpace(cmd.PositionID)
	graph, err := s.orgs.ListOrganizations(ctx, OrgGraphQuery{OrganizationID: organizationID})
	if err != nil {
		return err
	}
	organizationFound := false
	for _, organization := range graph.Organizations {
		if organization.OrganizationID == organizationID {
			organizationFound = true
			break
		}
	}
	if !organizationFound {
		return fmt.Errorf("%w: organization_id %s is not in organization catalog", ErrInvalidRequest, organizationID)
	}
	departmentFound := false
	for _, department := range graph.Departments {
		if department.DepartmentID != departmentID {
			continue
		}
		if department.OrganizationID != organizationID {
			return fmt.Errorf("%w: department_id %s does not belong to organization_id %s", ErrInvalidRequest, departmentID, organizationID)
		}
		departmentFound = true
		break
	}
	if !departmentFound {
		return fmt.Errorf("%w: department_id %s is not in organization_id %s", ErrInvalidRequest, departmentID, organizationID)
	}
	for _, position := range graph.Positions {
		if position.PositionID != positionID {
			continue
		}
		if position.DepartmentID != departmentID {
			return fmt.Errorf("%w: position_id %s does not belong to department_id %s", ErrInvalidRequest, positionID, departmentID)
		}
		return nil
	}
	return fmt.Errorf("%w: position_id %s is not in department_id %s", ErrInvalidRequest, positionID, departmentID)
}

func (s *CommandService) SetWorkforceMemberEnabled(ctx context.Context, viewer ViewerRef, cmd SetWorkforceMemberEnabledCommand) error {
	if s == nil || s.profiles == nil {
		return ErrServiceDependency
	}
	if err := requireOperationsPermission(viewer, PermissionWorkforceManage); err != nil {
		return err
	}
	if strings.TrimSpace(cmd.AgentProfileID) == "" {
		return fmt.Errorf("%w: agent_profile_id is required", ErrInvalidRequest)
	}
	profile, err := s.profiles.GetProfile(ctx, agentcore.AgentProfileID(cmd.AgentProfileID))
	if err != nil {
		return mapWorkforceProfileLookupError(err)
	}
	if profile.IsPersonalAssistant() {
		return ErrNotFound
	}
	if err := s.authorizeOperationObject(ctx, viewer, authorizationActionWorkforceManage, authorizationObjectDepartment, string(profile.OrgIdentity.DepartmentRef.ID), string(profile.OrgIdentity.OrganizationRef.ID)); err != nil {
		return err
	}
	previousProfile := profile
	updatedProfile := profile
	updatedProfile.Enabled = cmd.Enabled
	if err := s.authorizeWorkforceAccountSecurityChanges(ctx, viewer, &profile, updatedProfile); err != nil {
		return err
	}
	sessionUsernames := workforceSessionRevocationUsernames(profile, updatedProfile, true)
	// The second deletion after SaveProfile closes the concurrent login window.
	if err := s.revokeWorkforceSessions(ctx, sessionUsernames...); err != nil {
		return err
	}
	profile = updatedProfile
	if err := s.profiles.SaveProfile(ctx, profile); err != nil {
		return err
	}
	if err := s.revokeWorkforceSessions(ctx, sessionUsernames...); err != nil {
		return errors.Join(err, s.failClosedWorkforceLoginAfterRevocationFailure(ctx, previousProfile, profile, true))
	}
	availability := agentcore.AgentAvailability{AgentProfileID: profile.ID}
	if lookup, ok := s.profiles.(WorkforceAvailabilityLookupPort); ok {
		if existing, err := lookup.GetAvailability(ctx, profile.ID); err == nil {
			availability = existing
		}
	}
	availability.AgentProfileID = profile.ID
	availability.AgentInstanceID = agentcore.AgentInstanceID(profile.ID)
	availability.Status = agentcore.AvailabilityAvailable
	availability.Reason = ""
	if !cmd.Enabled {
		availability.Status = agentcore.AvailabilityDisabled
		availability.Reason = "workforce member disabled"
	}
	availability.Source = "operations.workforce"
	availability.UpdatedAt = time.Now().UTC()
	if err := s.profiles.SaveAvailability(ctx, availability); err != nil {
		return err
	}
	action := "workforce.member.disable"
	if cmd.Enabled {
		action = "workforce.member.enable"
	}
	if err := s.recordAudit(ctx, viewer, action, cmd.AgentProfileID, map[string]string{
		"agent_profile_id": cmd.AgentProfileID,
		"enabled":          fmt.Sprintf("%t", cmd.Enabled),
	}); err != nil {
		return err
	}
	return s.applyWorkforceAccountProjection(ctx, profile, nil)
}

func mapWorkforceProfileLookupError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, agentcore.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

func (s *CommandService) ReleaseWorkforceMember(ctx context.Context, viewer ViewerRef, cmd ReleaseWorkforceMemberCommand) error {
	if s == nil || s.profiles == nil {
		return ErrServiceDependency
	}
	if err := requireOperationsPermission(viewer, PermissionWorkforceManage); err != nil {
		return err
	}
	agentProfileID := strings.TrimSpace(cmd.AgentProfileID)
	if agentProfileID == "" {
		return fmt.Errorf("%w: agent_profile_id is required", ErrInvalidRequest)
	}
	profileID := agentcore.AgentProfileID(agentProfileID)
	profile, err := s.profiles.GetProfile(ctx, profileID)
	if err != nil {
		return ErrNotFound
	}
	if profile.IsPersonalAssistant() {
		return ErrNotFound
	}
	if err := s.authorizeOperationObject(ctx, viewer, authorizationActionWorkforceManage, authorizationObjectDepartment, string(profile.OrgIdentity.DepartmentRef.ID), string(profile.OrgIdentity.OrganizationRef.ID)); err != nil {
		return err
	}
	sessionUsername := ""
	if profile.AgentKind == agentcore.AgentKindHuman {
		sessionUsername = profile.UserAccount.Username
	}
	previousProfile := profile
	agentInstanceID, err := s.existingWorkforceAgentInstanceID(ctx, profileID)
	if err != nil {
		return err
	}
	releasedAt := time.Now().UTC()
	reason := strings.TrimSpace(cmd.Reason)
	if reason == "" || reason == "workforce.member.delete" {
		reason = "released to talent market"
	}
	profile.Placement = agentcore.TalentPlacement{
		Status:                     agentcore.TalentPlacementMarket,
		ReleasedFromOrganizationID: profile.OrgIdentity.OrganizationRef.ID,
		ReleasedFromDepartmentID:   profile.OrgIdentity.DepartmentRef.ID,
		ReleasedFromPositionID:     profile.OrgIdentity.PositionRef.ID,
		ReleasedAt:                 releasedAt,
		ReleasedReason:             reason,
	}
	profile.OrgIdentity.OrganizationRef = agentcore.OrganizationRef{}
	profile.OrgIdentity.DepartmentRef = agentcore.DepartmentRef{}
	profile.OrgIdentity.PositionRef = agentcore.PositionRef{}
	profile.OrgIdentity.RoleRefs = nil
	profile.OrgIdentity.ManagerAgentRef = agentcore.AgentProfileRef{}
	profile.OrgIdentity.ReportingLineRef = agentcore.ReportingLineRef{}
	profile.OrgIdentity.AuthorityScope = ""
	rolePlan, err := s.planWorkforceRoleAssignments(ctx, &previousProfile, profile, viewer.ViewerID, true)
	if err != nil {
		return err
	}
	if err := s.authorizeWorkforceAccountSecurityChanges(ctx, viewer, &previousProfile, profile); err != nil {
		return err
	}
	if err := s.authorizeWorkforceRoleChanges(ctx, viewer, &previousProfile, profile, rolePlan); err != nil {
		return err
	}
	// The second deletion after SaveProfile closes the concurrent login window.
	if err := s.revokeWorkforceSessions(ctx, sessionUsername); err != nil {
		return err
	}
	if err := s.profiles.SaveProfile(ctx, profile); err != nil {
		return err
	}
	if err := s.applyWorkforceRoleAssignmentPlan(ctx, rolePlan); err != nil {
		return errors.Join(err, s.restoreWorkforceProfileRecord(ctx, previousProfile, profile, true))
	}
	if err := s.revokeWorkforceSessions(ctx, sessionUsername); err != nil {
		return errors.Join(err, s.failClosedWorkforceLoginAfterRevocationFailure(ctx, previousProfile, profile, true))
	}
	if err := s.profiles.SaveAvailability(ctx, agentcore.AgentAvailability{
		AgentProfileID:  profileID,
		AgentInstanceID: agentInstanceID,
		Status:          agentcore.AvailabilityDisabled,
		Reason:          "workforce member released to talent market",
		Source:          "operations.workforce",
		UpdatedAt:       releasedAt,
	}); err != nil {
		return err
	}
	if err := s.deleteWorkforceMemberPlacementRelation(ctx, previousProfile); err != nil {
		return errors.Join(err, s.restoreWorkforceRoleAssignmentPlan(ctx, rolePlan), s.restoreWorkforceProfile(ctx, previousProfile, profile, true, true))
	}
	return s.recordAudit(ctx, viewer, "workforce.member.release_to_market", agentProfileID, map[string]string{
		"agent_id":          agentProfileID,
		"agent_profile_id":  agentProfileID,
		"agent_instance_id": agentInstanceID.String(),
		"enabled":           "false",
		"placement":         "market",
		"reason":            reason,
	})
}

func (s *CommandService) HireTalentFromMarket(ctx context.Context, viewer ViewerRef, cmd HireTalentFromMarketCommand) (WorkforceMemberDTO, error) {
	if s == nil || s.profiles == nil {
		return WorkforceMemberDTO{}, ErrServiceDependency
	}
	if err := requireOperationsPermission(viewer, PermissionWorkforceManage); err != nil {
		return WorkforceMemberDTO{}, err
	}
	profileID := agentcore.AgentProfileID(strings.TrimSpace(cmd.AgentProfileID))
	if profileID.IsZero() {
		return WorkforceMemberDTO{}, fmt.Errorf("%w: agent_profile_id is required", ErrInvalidRequest)
	}
	profile, err := s.profiles.GetProfile(ctx, profileID)
	if err != nil {
		return WorkforceMemberDTO{}, ErrNotFound
	}
	if profile.IsPersonalAssistant() {
		return WorkforceMemberDTO{}, ErrNotFound
	}
	if !profile.Placement.IsMarket(profile.OrgIdentity) {
		return WorkforceMemberDTO{}, fmt.Errorf("%w: talent is already internal", ErrInvalidRequest)
	}
	previousProfile := profile
	if !profile.Enabled {
		return WorkforceMemberDTO{}, fmt.Errorf("%w: disabled market talent cannot be hired", ErrInvalidRequest)
	}
	validateCmd := UpsertWorkforceMemberCommand{
		AgentProfileID: string(profileID),
		OrganizationID: strings.TrimSpace(cmd.OrganizationID),
		DepartmentID:   strings.TrimSpace(cmd.DepartmentID),
		PositionID:     strings.TrimSpace(cmd.PositionID),
	}
	if validateCmd.OrganizationID == "" || validateCmd.DepartmentID == "" || validateCmd.PositionID == "" {
		return WorkforceMemberDTO{}, fmt.Errorf("%w: organization_id, department_id and position_id are required", ErrInvalidRequest)
	}
	if err := s.validateWorkforceMemberOrgRefs(ctx, validateCmd); err != nil {
		return WorkforceMemberDTO{}, err
	}
	if err := s.authorizeOperationObject(ctx, viewer, authorizationActionWorkforceManage, authorizationObjectDepartment, validateCmd.DepartmentID, validateCmd.OrganizationID); err != nil {
		return WorkforceMemberDTO{}, err
	}
	sessionUsername := ""
	if profile.AgentKind == agentcore.AgentKindHuman {
		sessionUsername = profile.UserAccount.Username
	}
	agentInstanceID, err := s.existingWorkforceAgentInstanceID(ctx, profile.ID)
	if err != nil {
		return WorkforceMemberDTO{}, err
	}
	profile.Placement = agentcore.TalentPlacement{Status: agentcore.TalentPlacementInternal}
	profile.OrgIdentity.OrganizationRef = agentcore.OrganizationRef{ID: agentcore.OrganizationID(validateCmd.OrganizationID)}
	profile.OrgIdentity.DepartmentRef = agentcore.DepartmentRef{ID: agentcore.DepartmentID(validateCmd.DepartmentID)}
	profile.OrgIdentity.PositionRef = agentcore.PositionRef{ID: agentcore.PositionID(validateCmd.PositionID)}
	profile.OrgIdentity.RoleRefs = roleRefs(cmd.RoleIDs)
	profile.OrgIdentity.ManagerAgentRef = agentcore.AgentProfileRef{ID: agentcore.AgentProfileID(strings.TrimSpace(cmd.ManagerAgentID))}
	profile.OrgIdentity.ReportingLineRef = agentcore.ReportingLineRef{ID: agentcore.ReportingLineID(strings.TrimSpace(cmd.ReportingLineID))}
	if profile.OrgIdentity.SupervisionPolicy == "" || (profile.OrgIdentity.ManagerAgentRef.ID != "" && profile.OrgIdentity.SupervisionPolicy == agentcore.SupervisionPolicyRoot) {
		profile.OrgIdentity.SupervisionPolicy = normalizeSupervisionPolicy("", cmd.ManagerAgentID)
	}
	if err := s.validateUniqueWorkforceUsername(ctx, profile); err != nil {
		return WorkforceMemberDTO{}, err
	}
	if err := s.validateWorkforceUserAccount(profile); err != nil {
		return WorkforceMemberDTO{}, err
	}
	if err := s.authorizeWorkforceAccountSecurityChanges(ctx, viewer, &previousProfile, profile); err != nil {
		return WorkforceMemberDTO{}, err
	}
	rolePlan, err := s.planWorkforceRoleAssignments(ctx, &previousProfile, profile, viewer.ViewerID, true)
	if err != nil {
		return WorkforceMemberDTO{}, err
	}
	if err := s.authorizeWorkforceRoleChanges(ctx, viewer, &previousProfile, profile, rolePlan); err != nil {
		return WorkforceMemberDTO{}, err
	}
	// Rehired human accounts must start with a fresh authenticated session.
	if err := s.revokeWorkforceSessions(ctx, sessionUsername); err != nil {
		return WorkforceMemberDTO{}, err
	}
	if err := s.profiles.SaveProfile(ctx, profile); err != nil {
		return WorkforceMemberDTO{}, err
	}
	if err := s.applyWorkforceRoleAssignmentPlan(ctx, rolePlan); err != nil {
		return WorkforceMemberDTO{}, errors.Join(err, s.restoreWorkforceProfileRecord(ctx, previousProfile, profile, true))
	}
	if err := s.revokeWorkforceSessions(ctx, sessionUsername); err != nil {
		return WorkforceMemberDTO{}, errors.Join(err, s.failClosedWorkforceLoginAfterRevocationFailure(ctx, previousProfile, profile, true))
	}
	if err := s.ensurePersonalMemoryProfile(ctx, profile, true); err != nil {
		return WorkforceMemberDTO{}, err
	}
	if err := s.profiles.SaveAvailability(ctx, agentcore.AgentAvailability{
		AgentProfileID:  profile.ID,
		AgentInstanceID: agentInstanceID,
		Status:          agentcore.AvailabilityAvailable,
		Source:          "operations.workforce",
		UpdatedAt:       time.Now().UTC(),
	}); err != nil {
		return WorkforceMemberDTO{}, err
	}
	if err := s.writeWorkforceMemberPlacementRelation(ctx, profile); err != nil {
		return WorkforceMemberDTO{}, errors.Join(err, s.restoreWorkforceRoleAssignmentPlan(ctx, rolePlan), s.restoreWorkforceProfile(ctx, previousProfile, profile, true, false))
	}
	if err := s.recordAudit(ctx, viewer, "talent_market.member.hire", profile.ID.String(), map[string]string{
		"agent_id":          profile.ID.String(),
		"agent_profile_id":  profile.ID.String(),
		"agent_instance_id": agentInstanceID.String(),
		"organization_id":   validateCmd.OrganizationID,
		"department_id":     validateCmd.DepartmentID,
		"position_id":       validateCmd.PositionID,
		"placement":         "internal",
	}); err != nil {
		return WorkforceMemberDTO{}, err
	}
	member := mapMemberFromProfile(entryFromProfile(profile, agentInstanceID.String()), profile)
	if err := s.applyWorkforceMemberOrgDisplayNames(ctx, &member); err != nil {
		return WorkforceMemberDTO{}, err
	}
	return member, nil
}

func (s *CommandService) restoreWorkforceProfile(ctx context.Context, previous agentcore.AgentProfile, current agentcore.AgentProfile, hadPrevious bool, restorePreviousRelation bool) error {
	if s == nil || s.profiles == nil {
		return ErrServiceDependency
	}
	var rollbackErr error
	rollbackErr = errors.Join(rollbackErr, s.deleteWorkforceMemberPlacementRelation(ctx, current))
	if hadPrevious {
		rollbackErr = errors.Join(rollbackErr, s.profiles.SaveProfile(ctx, previous))
		if restorePreviousRelation {
			rollbackErr = errors.Join(rollbackErr, s.writeWorkforceMemberPlacementRelation(ctx, previous))
		}
		return rollbackErr
	}
	deleter, ok := s.profiles.(WorkforceProfileDeletePort)
	if !ok {
		return errors.Join(rollbackErr, ErrServiceDependency)
	}
	rollbackErr = errors.Join(rollbackErr, deleter.DeleteProfile(ctx, current.ID, "rollback failed authorization relation synchronization"))
	return rollbackErr
}

func optionalWorkforceProfile(profile agentcore.AgentProfile, present bool) *agentcore.AgentProfile {
	if !present {
		return nil
	}
	return &profile
}

func (s *CommandService) restoreWorkforceProfileRecord(ctx context.Context, previous agentcore.AgentProfile, current agentcore.AgentProfile, hadPrevious bool) error {
	if s == nil || s.profiles == nil {
		return ErrServiceDependency
	}
	if hadPrevious {
		return s.profiles.SaveProfile(ctx, previous)
	}
	deleter, ok := s.profiles.(WorkforceProfileDeletePort)
	if !ok {
		return ErrServiceDependency
	}
	return deleter.DeleteProfile(ctx, current.ID, "rollback failed workforce role synchronization")
}

func (s *CommandService) existingWorkforceAgentInstanceID(ctx context.Context, profileID agentcore.AgentProfileID) (agentcore.AgentInstanceID, error) {
	if profileID.IsZero() {
		return "", fmt.Errorf("%w: agent_profile_id is required", ErrInvalidRequest)
	}
	return agentcore.AgentInstanceID(profileID), nil
}
