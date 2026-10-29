package operations

import (
	"context"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

func (s *QueryService) workforcePermissionRoleIDs(ctx context.Context) (map[string]struct{}, error) {
	roles, err := s.allRoleDefinitions(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		if roleID := strings.ToLower(strings.TrimSpace(role.RoleID)); roleID != "" {
			out[roleID] = struct{}{}
		}
	}
	return out, nil
}

func splitWorkforceRoleIDs(member *WorkforceMemberDTO, permissionRoleIDs map[string]struct{}) {
	if member == nil {
		return
	}
	rawRoleIDs := append([]string(nil), member.RoleIDs...)
	member.RoleIDs = nil
	member.FunctionalRoleIDs = nil
	for _, value := range rawRoleIDs {
		roleID := strings.ToLower(strings.TrimSpace(value))
		if roleID == "" {
			continue
		}
		if _, permissionRole := permissionRoleIDs[roleID]; permissionRole {
			member.RoleIDs = append(member.RoleIDs, roleID)
			continue
		}
		member.FunctionalRoleIDs = append(member.FunctionalRoleIDs, roleID)
	}
	if strings.EqualFold(strings.TrimSpace(member.AuthorityScope), "primary_role") {
		member.AuthorityScope = firstVisibleFunctionalRole(member.FunctionalRoleIDs)
	}
}

func firstVisibleFunctionalRole(roleIDs []string) string {
	for _, roleID := range roleIDs {
		switch roleID {
		case "primary_role", "human_agent", "digital_agent", "superadmin":
			continue
		default:
			return roleID
		}
	}
	return ""
}

func (s *QueryService) mapMember(ctx context.Context, entry agentcore.AgentDirectoryEntry, includeDetails bool) WorkforceMemberDTO {
	if includeDetails {
		if lookup, ok := s.agents.(AgentProfileLookupPort); ok && !entry.AgentProfileID.IsZero() {
			if profile, err := lookup.GetProfile(ctx, entry.AgentProfileID); err == nil && !profile.ID.IsZero() {
				return mapMemberFromProfile(entry, profile)
			}
		}
	}
	return mapMember(entry)
}

func (s *QueryService) batchMemberProfiles(ctx context.Context, entries []agentcore.AgentDirectoryEntry, includeDetails bool) (map[agentcore.AgentProfileID]agentcore.AgentProfile, bool) {
	if !includeDetails {
		return nil, false
	}
	lookup, ok := s.agents.(AgentProfileBatchLookupPort)
	if !ok {
		return nil, false
	}
	ids := make([]agentcore.AgentProfileID, 0, len(entries))
	seen := make(map[agentcore.AgentProfileID]struct{}, len(entries))
	for _, entry := range entries {
		if entry.AgentProfileID.IsZero() {
			continue
		}
		if _, exists := seen[entry.AgentProfileID]; exists {
			continue
		}
		seen[entry.AgentProfileID] = struct{}{}
		ids = append(ids, entry.AgentProfileID)
	}
	profiles, err := lookup.GetProfiles(ctx, ids)
	if err != nil {
		return nil, true
	}
	return profiles, true
}

func (s *QueryService) mapMemberWithProfiles(ctx context.Context, entry agentcore.AgentDirectoryEntry, includeDetails bool, profiles map[agentcore.AgentProfileID]agentcore.AgentProfile, profilesBatched bool) WorkforceMemberDTO {
	if includeDetails && profilesBatched {
		if profile, ok := profiles[entry.AgentProfileID]; ok && !profile.ID.IsZero() {
			return mapMemberFromProfile(entry, profile)
		}
		return mapMember(entry)
	}
	return s.mapMember(ctx, entry, includeDetails)
}

func mapMember(entry agentcore.AgentDirectoryEntry) WorkforceMemberDTO {
	availabilityStatus := string(entry.Availability.Status)
	if availabilityStatus == "" {
		availabilityStatus = string(agentcore.AvailabilityUnknown)
	}
	if !entry.Enabled {
		availabilityStatus = string(agentcore.AvailabilityDisabled)
	}

	member := WorkforceMemberDTO{
		AgentID:                    operationAgentID(entry),
		AgentProfileID:             entry.AgentProfileID.String(),
		PersonalAssistant:          strings.TrimSpace(entry.PersonalAssistantOwnerID) != "",
		PersonalAssistantOwnerID:   strings.TrimSpace(entry.PersonalAssistantOwnerID),
		AgentInstanceID:            entry.AgentInstanceID.String(),
		RealName:                   entry.RealName,
		DisplayName:                entry.DisplayName,
		Email:                      strings.TrimSpace(entry.Email),
		AgentKind:                  string(entry.AgentKind),
		EmploymentType:             string(entry.OrgIdentity.EmploymentType),
		OrganizationID:             string(entry.OrgIdentity.OrganizationRef.ID),
		DepartmentID:               string(entry.OrgIdentity.DepartmentRef.ID),
		PositionID:                 string(entry.OrgIdentity.PositionRef.ID),
		DepartmentDisplayName:      string(entry.OrgIdentity.DepartmentRef.ID),
		PositionDisplayName:        firstNonEmpty(entry.RoleName, string(entry.OrgIdentity.PositionRef.ID)),
		RoleName:                   entry.RoleName,
		AvailabilityStatus:         availabilityStatus,
		CapabilityTags:             capabilityTags(entry.SkillRefs),
		ManagerAgentID:             entry.OrgIdentity.ManagerAgentRef.ID.String(),
		ReportingLineID:            string(entry.OrgIdentity.ReportingLineRef.ID),
		AuthorityScope:             entry.OrgIdentity.AuthorityScope,
		SupervisionPolicy:          string(entry.OrgIdentity.SupervisionPolicy),
		RuntimeKind:                string(entry.RuntimeSpecSummary.Kind),
		RuntimeConfigRef:           entry.RuntimeSpecSummary.ConfigRef,
		BaseAgent:                  mapBaseAgentSummary(entry.BaseAgentSummary),
		Persona:                    WorkforcePersonaDTO{Backstory: entry.PersonaSummary},
		KnowledgeBaseID:            entry.KnowledgeBaseID,
		PersonalMemoryRef:          mapMemoryRef(entry.MemoryProfileRef),
		SkillRefs:                  mapSkillRefs(entry.SkillRefs),
		ToolRefs:                   mapToolRefs(entry.ToolRefs),
		QAEnabled:                  entry.QAEnabled,
		Enabled:                    entry.Enabled,
		PlacementStatus:            string(entry.Placement.NormalizedStatus(entry.OrgIdentity)),
		ReleasedFromOrganizationID: string(entry.Placement.ReleasedFromOrganizationID),
		ReleasedFromDepartmentID:   string(entry.Placement.ReleasedFromDepartmentID),
		ReleasedFromPositionID:     string(entry.Placement.ReleasedFromPositionID),
		ReleasedAt:                 entry.Placement.ReleasedAt,
		ReleasedReason:             entry.Placement.ReleasedReason,
	}
	for _, role := range entry.OrgIdentity.RoleRefs {
		member.RoleIDs = append(member.RoleIDs, string(role.ID))
	}
	return member
}

func mapMemberFromProfile(entry agentcore.AgentDirectoryEntry, profile agentcore.AgentProfile) WorkforceMemberDTO {
	member := mapMember(entry)
	member.PersonalAssistant = profile.IsPersonalAssistant()
	member.PersonalAssistantOwnerID = strings.TrimSpace(profile.PersonalAssistantOwnerID)
	member.RealName = firstNonEmpty(profile.RealName, member.RealName)
	member.DisplayName = firstNonEmpty(profile.DisplayName, member.DisplayName)
	member.Email = strings.TrimSpace(profile.Email)
	member.PersonSource = WorkforcePersonSourceFactsDTO{
		EmployeeNumber: strings.TrimSpace(profile.PersonSource.EmployeeNumber),
		Source:         strings.TrimSpace(profile.PersonSource.Source),
		SourceRecordID: strings.TrimSpace(profile.PersonSource.SourceRecordID),
		SourceTenantID: strings.TrimSpace(profile.PersonSource.SourceTenantID),
	}
	member.UserAccount = mapUserAccountSpec(profile.UserAccount)
	if strings.TrimSpace(member.UserAccount.Nickname) != "" {
		member.DisplayName = member.UserAccount.Nickname
	}
	member.AgentKind = string(profile.AgentKind)
	member.EmploymentType = string(profile.OrgIdentity.EmploymentType)
	member.RoleName = profile.RoleName
	member.OrganizationID = string(profile.OrgIdentity.OrganizationRef.ID)
	member.DepartmentID = string(profile.OrgIdentity.DepartmentRef.ID)
	member.PositionID = string(profile.OrgIdentity.PositionRef.ID)
	member.RoleIDs = member.RoleIDs[:0]
	for _, role := range profile.OrgIdentity.RoleRefs {
		member.RoleIDs = append(member.RoleIDs, string(role.ID))
	}
	member.ManagerAgentID = profile.OrgIdentity.ManagerAgentRef.ID.String()
	member.ReportingLineID = string(profile.OrgIdentity.ReportingLineRef.ID)
	member.AuthorityScope = profile.OrgIdentity.AuthorityScope
	member.SupervisionPolicy = string(profile.OrgIdentity.SupervisionPolicy)
	member.WorkspaceRoot = profile.WorkspaceRoot
	member.RuntimeKind = string(profile.RuntimeSpec.Kind)
	member.RuntimeConfigRef = profile.RuntimeSpec.ConfigRef
	member.RuntimeConfig = cloneRawMessage(profile.RuntimeSpec.Config)
	member.BaseAgent = mapBaseAgentSpec(profile.BaseAgent)
	member.KnowledgeBaseID = profile.PersonalKnowledgeBaseID
	member.Persona = WorkforcePersonaDTO{
		Backstory:              profile.Persona.Backstory,
		ResponsibilityBoundary: profile.Persona.ResponsibilityBoundary,
		BehaviorBoundary:       profile.Persona.BehaviorBoundary,
	}
	member.PersonalMemoryRef = mapMemoryRef(profile.PersonalMemoryProfileRef)
	member.CollectiveMemoryRefs = mapMemoryRefs(profile.CollectiveMemoryScopeRefs)
	member.SkillRefs = mapSkillRefs(profile.SkillRefs)
	member.ToolRefs = mapToolRefs(profile.ToolRefs)
	member.QAEnabled = profile.QAEnabled
	member.CapabilityTags = capabilityTags(profile.SkillRefs)
	member.PlacementStatus = string(profile.Placement.NormalizedStatus(profile.OrgIdentity))
	member.ReleasedFromOrganizationID = string(profile.Placement.ReleasedFromOrganizationID)
	member.ReleasedFromDepartmentID = string(profile.Placement.ReleasedFromDepartmentID)
	member.ReleasedFromPositionID = string(profile.Placement.ReleasedFromPositionID)
	member.ReleasedAt = profile.Placement.ReleasedAt
	member.ReleasedReason = profile.Placement.ReleasedReason
	return member
}

func mapUserAccountSpec(account agentcore.UserAccountSpec) WorkforceUserAccountDTO {
	return WorkforceUserAccountDTO{
		Username:        strings.TrimSpace(account.Username),
		Nickname:        strings.TrimSpace(account.Nickname),
		Email:           strings.TrimSpace(account.Email),
		Role:            strings.TrimSpace(account.Role),
		AccountEnabled:  account.AccountEnabled,
		PermissionScope: cleanPermissionScopes(account.PermissionScope),
	}
}

func mapBaseAgentSummary(summary agentcore.BaseAgentSummary) WorkforceBaseAgentDTO {
	return WorkforceBaseAgentDTO{
		InheritsBaseAgent: summary.InheritsBaseAgent,
		AuditRequired:     summary.AuditRequired,
		HasSystemPrompt:   summary.HasSystemPrompt,
		HasLLMAPIKeyRef:   summary.HasLLMAPIKeyRef,
		LLMAPIKeyRef:      summary.LLMAPIKeyRef,
		SkillScope:        summary.SkillScope,
		ToolScope:         summary.ToolScope,
		MCPScope:          summary.MCPScope,
	}
}

func mapBaseAgentSpec(spec agentcore.BaseAgentSpec) WorkforceBaseAgentDTO {
	return WorkforceBaseAgentDTO{
		InheritsBaseAgent: spec.InheritsBaseAgent,
		AuditRequired:     spec.AuditRequired,
		SystemPrompt:      spec.SystemPrompt,
		HasSystemPrompt:   strings.TrimSpace(spec.SystemPrompt) != "",
		HasLLMAPIKeyRef:   strings.TrimSpace(spec.LLMAPIKeyRef) != "",
		LLMAPIKeyRef:      spec.LLMAPIKeyRef,
		SkillScope:        spec.SkillScope,
		ToolScope:         spec.ToolScope,
		MCPScope:          spec.MCPScope,
	}
}

func mapSkillRefs(refs []agentcore.SkillRef) []WorkforceSkillRefDTO {
	items := make([]WorkforceSkillRefDTO, 0, len(refs))
	for _, ref := range refs {
		if ref.SkillID == "" {
			continue
		}
		item := WorkforceSkillRefDTO{
			SkillID:           string(ref.SkillID),
			VersionConstraint: ref.VersionConstraint,
			Alias:             ref.Alias,
			Level:             ref.Level,
		}
		for _, evidence := range ref.EvidenceRefs {
			if evidence.ID != "" {
				item.EvidenceRefs = append(item.EvidenceRefs, evidence.ID)
			}
		}
		items = append(items, item)
	}
	return items
}

func mapToolRefs(refs []agentcore.ToolRef) []WorkforceToolRefDTO {
	items := make([]WorkforceToolRefDTO, 0, len(refs))
	for _, ref := range refs {
		if ref.ToolID == "" {
			continue
		}
		items = append(items, WorkforceToolRefDTO{
			ToolID:            string(ref.ToolID),
			VersionConstraint: ref.VersionConstraint,
			Alias:             ref.Alias,
			PermissionScope:   ref.PermissionScope,
			BindingPolicy:     ref.BindingPolicy,
		})
	}
	return items
}

func mapMemoryRefs(refs []agentcore.MemoryProfileRef) []WorkforceMemoryProfileRefDTO {
	items := make([]WorkforceMemoryProfileRefDTO, 0, len(refs))
	for _, ref := range refs {
		if ref.MemoryProfileID == "" {
			continue
		}
		items = append(items, mapMemoryRef(ref))
	}
	return items
}

func mapMemoryRef(ref agentcore.MemoryProfileRef) WorkforceMemoryProfileRefDTO {
	if ref.MemoryProfileID == "" {
		return WorkforceMemoryProfileRefDTO{}
	}
	return WorkforceMemoryProfileRefDTO{
		MemoryProfileID:   string(ref.MemoryProfileID),
		OwnerAgentID:      string(ref.OwnerAgentID),
		MemoryKind:        string(ref.MemoryKind),
		AccessScope:       ref.AccessScope,
		RetrievalPolicy:   ref.RetrievalPolicy,
		CompressionPolicy: ref.CompressionPolicy,
	}
}

func capabilityTags(refs []agentcore.SkillRef) []string {
	tags := make([]string, 0, len(refs))
	for _, ref := range refs {
		label := firstNonEmpty(ref.Alias, string(ref.SkillID))
		if label == "" {
			continue
		}
		tags = append(tags, label)
	}
	return tags
}

func operationAgentID(entry agentcore.AgentDirectoryEntry) string {
	return entry.AgentProfileID.String()
}

func operationAgentMatches(entry agentcore.AgentDirectoryEntry, agentID string) bool {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return true
	}
	return strings.EqualFold(operationAgentID(entry), agentID)
}
