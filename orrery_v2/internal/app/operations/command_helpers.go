package operations

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/permissionscope"
	memcore "orrery_v2/internal/memory/core"
	skillstd "orrery_v2/internal/resource/skill/std"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func requireOperationsPermission(viewer ViewerRef, permission string) error {
	return requireOperationsPermissionAny(viewer, permission)
}

func requireOperationsPermissionAny(viewer ViewerRef, permissions ...string) error {
	if strings.TrimSpace(viewer.ViewerID) == "" {
		return ErrInvalidRequest
	}
	for _, granted := range viewer.PermissionScope {
		for _, permission := range permissions {
			if operationsPermissionScopeMatches(strings.TrimSpace(granted), permission) {
				return nil
			}
		}
	}
	return ErrPermissionDenied
}

func operationsPermissionScopeMatches(granted, required string) bool {
	return permissionscope.Allows(granted, required)
}

func (s *CommandService) recordAudit(ctx context.Context, viewer ViewerRef, action, entityID string, metadata map[string]string) error {
	if s == nil || s.audit == nil {
		return nil
	}
	return s.audit.RecordAuditEvent(ctx, AuditEventDTO{
		ActorID:       viewer.ViewerID,
		ViewerID:      viewer.ViewerID,
		EntityType:    "workforce_member",
		EntityID:      entityID,
		Action:        action,
		Result:        "succeeded",
		Severity:      "info",
		CorrelationID: firstNonEmpty(viewer.RequestID, viewer.IdempotencyKey),
		Metadata:      metadata,
	})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func cleanPermissionScopes(scopes []string) []string {
	out := make([]string, 0, len(scopes))
	seen := map[string]struct{}{}
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		out = append(out, scope)
	}
	return out
}

func (s *CommandService) newID(prefix string) (string, error) {
	generator := s.ids
	if generator == nil {
		generator = NewPrefixedIDGenerator()
	}
	return generator.NewID(prefix)
}

func cloneRawMessage(src json.RawMessage) json.RawMessage {
	if len(src) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), src...)
}

func roleRefs(ids []string) []agentcore.RoleRef {
	refs := make([]agentcore.RoleRef, 0, len(ids))
	seen := map[string]struct{}{}
	for _, id := range ids {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		refs = append(refs, agentcore.RoleRef{ID: agentcore.RoleID(id)})
	}
	return refs
}

func entryFromProfile(profile agentcore.AgentProfile, agentID string) agentcore.AgentDirectoryEntry {
	instanceID := agentcore.AgentInstanceID(strings.TrimSpace(agentID))
	return agentcore.AgentDirectoryEntry{
		AgentProfileID:  profile.ID,
		AgentInstanceID: instanceID,
		RealName:        profile.RealName,
		DisplayName:     profile.DisplayName,
		AgentKind:       profile.AgentKind,
		OrgIdentity:     profile.OrgIdentity,
		Placement:       profile.Placement,
		PersonaSummary:  profile.Persona.Backstory,
		SkillRefs:       append([]agentcore.SkillRef(nil), profile.SkillRefs...),
		ToolRefs:        append([]agentcore.ToolRef(nil), profile.ToolRefs...),
		RuntimeSpecSummary: agentcore.RuntimeSpecSummary{
			Kind:      profile.RuntimeSpec.Kind,
			ConfigRef: profile.RuntimeSpec.ConfigRef,
		},
		BaseAgentSummary: baseAgentSummary(profile.BaseAgent),
		KnowledgeBaseID:  profile.PersonalKnowledgeBaseID,
		MemoryProfileRef: profile.PersonalMemoryProfileRef,
		QAEnabled:        profile.QAEnabled,
		Enabled:          profile.Enabled,
	}
}

func baseAgentSummary(spec agentcore.BaseAgentSpec) agentcore.BaseAgentSummary {
	return agentcore.BaseAgentSummary{
		InheritsBaseAgent: spec.InheritsBaseAgent,
		AuditRequired:     spec.AuditRequired,
		HasSystemPrompt:   strings.TrimSpace(spec.SystemPrompt) != "",
		HasLLMAPIKeyRef:   strings.TrimSpace(spec.LLMAPIKeyRef) != "",
		LLMAPIKeyRef:      spec.LLMAPIKeyRef,
		SkillScope:        spec.SkillScope,
		ToolScope:         spec.ToolScope,
		MCPScope:          spec.MCPScope,
	}
}

func skillRefsFromDTO(items []WorkforceSkillRefDTO) []agentcore.SkillRef {
	refs := make([]agentcore.SkillRef, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.SkillID) == "" {
			continue
		}
		ref := agentcore.SkillRef{
			SkillID:           agentcore.SkillID(strings.TrimSpace(item.SkillID)),
			VersionConstraint: strings.TrimSpace(item.VersionConstraint),
			Alias:             strings.TrimSpace(item.Alias),
			Level:             strings.TrimSpace(item.Level),
		}
		for _, evidence := range item.EvidenceRefs {
			if strings.TrimSpace(evidence) != "" {
				ref.EvidenceRefs = append(ref.EvidenceRefs, agentcore.EvidenceRef{ID: strings.TrimSpace(evidence)})
			}
		}
		refs = append(refs, ref)
	}
	return refs
}

func toolRefsFromDTO(items []WorkforceToolRefDTO) []agentcore.ToolRef {
	refs := make([]agentcore.ToolRef, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.ToolID) == "" {
			continue
		}
		refs = append(refs, agentcore.ToolRef{
			ToolID:            agentcore.ToolID(strings.TrimSpace(item.ToolID)),
			VersionConstraint: strings.TrimSpace(item.VersionConstraint),
			Alias:             strings.TrimSpace(item.Alias),
			PermissionScope:   strings.TrimSpace(item.PermissionScope),
			BindingPolicy:     strings.TrimSpace(item.BindingPolicy),
		})
	}
	return refs
}

func ensureDefaultAgentReadCapabilities(profile *agentcore.AgentProfile) {
	if profile == nil {
		return
	}
	if profile.AgentKind != agentcore.AgentKindHuman {
		if !agentcore.BaseAgentUsesSessionOnlyMemory(profile.BaseAgent) {
			profile.ToolRefs = toolstd.EnsureSessionHistoryReadToolRefs(profile.ToolRefs)
		}
		profile.SkillRefs = skillstd.EnsureWebResearchSkillRef(profile.SkillRefs)
		profile.ToolRefs = toolstd.EnsureWebSearchReadToolRef(profile.ToolRefs)
		profile.ToolRefs = toolstd.EnsureWebFetchReadToolRef(profile.ToolRefs)
	}
	if agentcore.BaseAgentUsesSessionOnlyMemory(profile.BaseAgent) {
		return
	}
	profile.ToolRefs = toolstd.EnsureKnowledgeSearchReadToolRef(profile.ToolRefs)
	profile.ToolRefs = toolstd.EnsureMemorySearchReadToolRef(profile.ToolRefs)
	if profile.AgentKind == agentcore.AgentKindHuman {
		return
	}
	profile.ToolRefs = toolstd.EnsureEnterpriseGraphReadToolRef(profile.ToolRefs)
}

func memoryRefsFromDTO(items []WorkforceMemoryProfileRefDTO) []agentcore.MemoryProfileRef {
	refs := make([]agentcore.MemoryProfileRef, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.MemoryProfileID) == "" {
			continue
		}
		refs = append(refs, agentcore.MemoryProfileRef{
			MemoryProfileID:   agentcore.MemoryProfileID(strings.TrimSpace(item.MemoryProfileID)),
			OwnerAgentID:      agentcore.AgentID(strings.TrimSpace(item.OwnerAgentID)),
			MemoryKind:        agentcore.MemoryKind(strings.TrimSpace(item.MemoryKind)),
			AccessScope:       strings.TrimSpace(item.AccessScope),
			RetrievalPolicy:   strings.TrimSpace(item.RetrievalPolicy),
			CompressionPolicy: strings.TrimSpace(item.CompressionPolicy),
		})
	}
	return refs
}

func (s *CommandService) ensurePersonalMemoryProfile(ctx context.Context, profile agentcore.AgentProfile, syncEnabled bool) error {
	if s == nil || s.memory == nil || profile.PersonalMemoryProfileRef.IsZero() {
		return nil
	}
	memoryProfile := memoryProfileFromAgent(profile)
	existing, err := s.memory.GetProfile(ctx, memoryProfile.ID)
	if err == nil && existing.ID != "" {
		if syncEnabled && existing.Enabled != memoryProfile.Enabled {
			existing.Enabled = memoryProfile.Enabled
			return s.memory.SaveProfile(ctx, existing)
		}
		return nil
	}
	if err != nil && !errors.Is(err, memcore.ErrNotFound) {
		return err
	}
	return s.memory.SaveProfile(ctx, memoryProfile)
}

func (s *CommandService) attachRequiredCollectiveMemoryProfiles(ctx context.Context, profile *agentcore.AgentProfile) error {
	if s == nil || s.memory == nil || profile == nil {
		return nil
	}
	organizationID := strings.TrimSpace(string(profile.OrgIdentity.OrganizationRef.ID))
	departmentID := strings.TrimSpace(string(profile.OrgIdentity.DepartmentRef.ID))
	scopes := []memcore.MemoryScope{}
	if organizationID != "" {
		scopes = append(scopes, memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: organizationID})
	}
	if departmentID != "" {
		scopes = append(scopes, memcore.MemoryScope{Kind: memcore.ScopeDepartment, ID: departmentID})
	}
	for _, scope := range scopes {
		owner := memcore.OwnerRef{OrganizationID: organizationID}
		if scope.Kind == memcore.ScopeDepartment {
			owner.DepartmentID = departmentID
		}
		memoryProfile := memcore.NewScopeMemoryProfile(scope, owner)
		existing, err := s.memory.GetProfile(ctx, memoryProfile.ID)
		if err == nil && existing.ID != "" {
			memoryProfile = existing
		} else if err != nil && !errors.Is(err, memcore.ErrNotFound) {
			return err
		} else if err := s.memory.SaveProfile(ctx, memoryProfile); err != nil {
			return err
		}
		profile.CollectiveMemoryScopeRefs = appendMemoryProfileRef(profile.CollectiveMemoryScopeRefs, memcore.MemoryProfileRefForScope(memoryProfile))
	}
	if profile.CollectiveMemoryScopeRefs == nil {
		profile.CollectiveMemoryScopeRefs = []agentcore.MemoryProfileRef{}
	}
	return nil
}

func appendMemoryProfileRef(refs []agentcore.MemoryProfileRef, ref agentcore.MemoryProfileRef) []agentcore.MemoryProfileRef {
	if ref.IsZero() {
		return refs
	}
	for i := range refs {
		if refs[i].MemoryProfileID == ref.MemoryProfileID {
			refs[i] = ref
			return refs
		}
	}
	return append(refs, ref)
}

func memoryProfileFromAgent(profile agentcore.AgentProfile) memcore.MemoryProfile {
	ref := profile.PersonalMemoryProfileRef
	owner := strings.TrimSpace(string(ref.OwnerAgentID))
	if owner == "" {
		owner = profile.ID.String()
	}
	return memcore.MemoryProfile{
		ID:         ref.MemoryProfileID,
		OwnerRef:   memcore.OwnerRef{AgentID: agentcore.AgentID(owner), OrganizationID: string(profile.OrgIdentity.OrganizationRef.ID), DepartmentID: string(profile.OrgIdentity.DepartmentRef.ID)},
		MemoryKind: firstMemoryKind(ref.MemoryKind, agentcore.MemoryKindPersonal),
		DefaultScope: memcore.MemoryScope{
			Kind: memcore.ScopeAgent,
			ID:   owner,
		},
		RetrievalPolicy:   memcore.RetrievalPolicy{MaxRecords: 20},
		CompressionPolicy: memcore.CompressionPolicy{MaxSummaryBytes: 4096},
		VisibilityPolicy:  memcore.VisibilityPolicy{Mode: memcore.VisibilityOwnerOnly},
		Enabled:           profile.Enabled,
	}
}

func firstMemoryKind(value, fallback agentcore.MemoryKind) agentcore.MemoryKind {
	if value.IsValid() {
		return value
	}
	return fallback
}

func normalizeAgentKind(value string) agentcore.AgentKind {
	kind := agentcore.AgentKind(strings.TrimSpace(value))
	if kind.IsValid() {
		return kind
	}
	return agentcore.AgentKindAI
}

func normalizeEmploymentType(value, agentKind string) agentcore.EmploymentType {
	switch agentcore.EmploymentType(strings.TrimSpace(value)) {
	case agentcore.EmploymentTypeAIAgent, agentcore.EmploymentTypeHuman, agentcore.EmploymentTypeSystem:
		return agentcore.EmploymentType(strings.TrimSpace(value))
	}
	if agentcore.AgentKind(agentKind) == agentcore.AgentKindHuman {
		return agentcore.EmploymentTypeHuman
	}
	return agentcore.EmploymentTypeAIAgent
}

func normalizeSupervisionPolicy(value, managerAgentID string) agentcore.SupervisionPolicy {
	switch agentcore.SupervisionPolicy(strings.TrimSpace(value)) {
	case agentcore.SupervisionPolicyManaged, agentcore.SupervisionPolicyRoot, agentcore.SupervisionPolicySupervised:
		return agentcore.SupervisionPolicy(strings.TrimSpace(value))
	}
	if strings.TrimSpace(managerAgentID) == "" {
		return agentcore.SupervisionPolicyRoot
	}
	return agentcore.SupervisionPolicyManaged
}

func normalizeRuntimeKind(value string, agentKind agentcore.AgentKind) agentcore.RuntimeKind {
	kind := agentcore.RuntimeKind(strings.TrimSpace(value))
	if kind.IsValid() {
		return kind
	}
	return ""
}

func defaultManagedBaseAgentSpec(profile agentcore.AgentProfile) agentcore.BaseAgentSpec {
	name := strings.TrimSpace(profile.RealName)
	if name == "" {
		name = profile.ID.String()
	}
	return agentcore.ApplyDefaultBaseAgentSpec(profile.BaseAgent, name, profile.RoleName)
}

func applyBaseAgentLLMProfile(spec *agentcore.BaseAgentSpec, profile agentcore.BaseAgentLLMProfileSpec) {
	if spec == nil || strings.TrimSpace(profile.ProfileID) == "" {
		return
	}
	profile.ProfileID = strings.TrimSpace(profile.ProfileID)
	profile.DisplayName = strings.TrimSpace(profile.DisplayName)
	profile.Provider = strings.TrimSpace(profile.Provider)
	profile.BaseURL = strings.TrimSpace(profile.BaseURL)
	profile.APIKeyRef = strings.TrimSpace(profile.APIKeyRef)
	profile.Model = strings.TrimSpace(profile.Model)
	if strings.TrimSpace(profile.APIKeyRef) != "" {
		spec.LLMAPIKeyRef = profile.APIKeyRef
	}
	if spec.Extensions == nil {
		spec.Extensions = map[string]json.RawMessage{}
	}
	payload, err := json.Marshal(profile)
	if err != nil {
		return
	}
	spec.Extensions[agentcore.BaseAgentLLMProfileExtensionKey] = payload
}

func ensureManagedAgentChineseIdentity(profile *agentcore.AgentProfile) {
	if profile == nil || profile.AgentKind == agentcore.AgentKindHuman {
		return
	}
	if !containsChinese(profile.RealName) {
		profile.RealName = generatedChineseAgentName(profile.ID, profile.RealName)
	}
	if strings.TrimSpace(profile.DisplayName) == "" {
		profile.DisplayName = profile.RealName
	}
	if strings.TrimSpace(profile.Persona.ResponsibilityBoundary) != "" && !containsChinese(profile.Persona.ResponsibilityBoundary) {
		profile.Persona.ResponsibilityBoundary = "负责" + profile.Persona.ResponsibilityBoundary + "，并按 BaseAgent 审计要求提交过程、结果和证据。"
	}
	if strings.TrimSpace(profile.Persona.ResponsibilityBoundary) == "" && strings.TrimSpace(profile.Persona.Backstory) != "" {
		profile.Persona.ResponsibilityBoundary = "负责" + firstNonEmpty(profile.RoleName, profile.OrgIdentity.AuthorityScope, "任务执行") + "，并按 BaseAgent 审计要求提交过程、结果和证据。"
	}
}

func generatedChineseAgentName(profileID agentcore.AgentProfileID, fallback string) string {
	stem := strings.TrimSpace(profileID.String())
	if stem == "" {
		stem = strings.TrimSpace(fallback)
	}
	if stem == "" {
		stem = "agent"
	}
	runes := []rune(stem)
	return "智员" + string(runes[len(runes)-1])
}

func containsChinese(value string) bool {
	for _, r := range value {
		if r >= '\u4e00' && r <= '\u9fff' {
			return true
		}
	}
	return false
}
