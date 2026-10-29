package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	devicedirectory "orrery_v2/internal/device/directory"
	knowledgecore "orrery_v2/internal/knowledge/core"
	memcore "orrery_v2/internal/memory/core"
	"orrery_v2/internal/platform/config"
	skillstd "orrery_v2/internal/resource/skill/std"
	toolstd "orrery_v2/internal/resource/tool/std"
)

const defaultPersonalAssistantDisplayName = "个人助理"

type productionPersonalAssistantService struct {
	directory       *devicedirectory.Service
	profiles        *agentcore.PlatformProfileRepository
	memory          defaultMemoryProfileStore
	knowledge       defaultKnowledgeEnsurer
	knowledgeBases  personalAssistantKnowledgeBaseLookup
	knowledgeRoot   string
	config          config.Config
	defaultTenantID string
	now             func() time.Time
}

type personalAssistantKnowledgeBaseLookup interface {
	GetKnowledgeBase(context.Context, knowledgecore.KnowledgeBaseID) (knowledgecore.KnowledgeBase, error)
}

func enabledDefaultKnowledgeEnsurer(disabled bool, knowledge defaultKnowledgeEnsurer) defaultKnowledgeEnsurer {
	if disabled {
		return nil
	}
	return knowledge
}

func (s *productionPersonalAssistantService) GetPersonalAssistant(ctx context.Context, scope devicedirectory.OwnerScope) (devicedirectory.PersonalAssistantView, error) {
	view, err := s.directory.GetPersonalAssistant(ctx, scope)
	if err != nil {
		return devicedirectory.PersonalAssistantView{}, err
	}
	return s.attachExistingProfile(ctx, scope, view)
}

func (s *productionPersonalAssistantService) UpdatePersonalAssistant(ctx context.Context, scope devicedirectory.OwnerScope, input devicedirectory.UpdatePersonalAssistantInput) (devicedirectory.PersonalAssistantView, error) {
	view, err := s.directory.UpdatePersonalAssistant(ctx, scope, input)
	if err != nil {
		return devicedirectory.PersonalAssistantView{}, err
	}
	return s.attachProfile(ctx, scope, view)
}

func (s *productionPersonalAssistantService) GetPersonalAssistantNotifications(ctx context.Context, scope devicedirectory.OwnerScope) (devicedirectory.PersonalAssistantNotificationPreferences, error) {
	return s.directory.GetPersonalAssistantNotifications(ctx, scope)
}

func (s *productionPersonalAssistantService) UpdatePersonalAssistantNotifications(ctx context.Context, scope devicedirectory.OwnerScope, input devicedirectory.UpdatePersonalAssistantNotificationsInput) (devicedirectory.PersonalAssistantNotificationPreferences, error) {
	return s.directory.UpdatePersonalAssistantNotifications(ctx, scope, input)
}

func (s *productionPersonalAssistantService) GetPersonalAssistantAvatar(ctx context.Context, scope devicedirectory.OwnerScope) (devicedirectory.PersonalAssistantAvatar, error) {
	return s.directory.GetPersonalAssistantAvatar(ctx, scope)
}

func (s *productionPersonalAssistantService) SavePersonalAssistantAvatar(ctx context.Context, scope devicedirectory.OwnerScope, contentType string, content []byte) (devicedirectory.PersonalAssistantAvatarInfo, error) {
	return s.directory.SavePersonalAssistantAvatar(ctx, scope, contentType, content)
}

func (s *productionPersonalAssistantService) DeletePersonalAssistantAvatar(ctx context.Context, scope devicedirectory.OwnerScope) error {
	return s.directory.DeletePersonalAssistantAvatar(ctx, scope)
}

func (s *productionPersonalAssistantService) PublishCatalog(ctx context.Context, scope devicedirectory.OwnerScope, deviceID string, input devicedirectory.PublishCatalogInput) error {
	return s.directory.PublishCatalog(ctx, scope, deviceID, input)
}

func (s *productionPersonalAssistantService) attachProfile(ctx context.Context, scope devicedirectory.OwnerScope, view devicedirectory.PersonalAssistantView) (devicedirectory.PersonalAssistantView, error) {
	profile, err := s.ensureProfile(ctx, scope, view)
	if err != nil {
		return devicedirectory.PersonalAssistantView{}, err
	}
	view.AgentProfileID = profile.ID.String()
	view.DisplayName = firstNonBlank(profile.DisplayName, profile.RealName, defaultPersonalAssistantDisplayName)
	view.SystemPrompt = strings.TrimSpace(profile.BaseAgent.SystemPrompt)
	return view, nil
}

func (s *productionPersonalAssistantService) attachExistingProfile(ctx context.Context, scope devicedirectory.OwnerScope, view devicedirectory.PersonalAssistantView) (devicedirectory.PersonalAssistantView, error) {
	if s == nil || s.profiles == nil {
		return devicedirectory.PersonalAssistantView{}, fmt.Errorf("personal assistant profile dependencies are incomplete")
	}
	tenantID := firstNonBlank(scope.TenantID, s.defaultTenantID, "global")
	ownerID := strings.TrimSpace(scope.OwnerID)
	profileID := agentcore.AgentProfileID(discussionsession.PersonalAssistantAgentID(tenantID, ownerID))
	if profileID.IsZero() {
		return devicedirectory.PersonalAssistantView{}, devicedirectory.ErrInvalidRequest
	}
	profile, err := s.profiles.GetProfile(ctx, profileID)
	if errors.Is(err, agentcore.ErrNotFound) {
		view.DisplayName = defaultPersonalAssistantDisplayName
		view.SystemPrompt = defaultPersonalAssistantSystemPrompt()
		return view, nil
	}
	if err != nil {
		return devicedirectory.PersonalAssistantView{}, err
	}
	if !profile.IsPersonalAssistant() || strings.TrimSpace(profile.PersonalAssistantOwnerID) != ownerID || !strings.EqualFold(strings.TrimSpace(profile.PersonalAssistantTenantID), tenantID) {
		return devicedirectory.PersonalAssistantView{}, fmt.Errorf("personal assistant profile %s has incompatible ownership", profileID)
	}
	if err := registerPersonalAssistantProfile(profile); err != nil {
		return devicedirectory.PersonalAssistantView{}, err
	}
	view.AgentProfileID = profile.ID.String()
	view.DisplayName = firstNonBlank(profile.DisplayName, profile.RealName, defaultPersonalAssistantDisplayName)
	view.SystemPrompt = strings.TrimSpace(profile.BaseAgent.SystemPrompt)
	return view, nil
}

func (s *productionPersonalAssistantService) ensureProfile(ctx context.Context, scope devicedirectory.OwnerScope, view devicedirectory.PersonalAssistantView) (agentcore.AgentProfile, error) {
	if s == nil || s.directory == nil || s.profiles == nil {
		return agentcore.AgentProfile{}, fmt.Errorf("personal assistant profile dependencies are incomplete")
	}
	tenantID := firstNonBlank(scope.TenantID, s.defaultTenantID, "global")
	ownerID := strings.TrimSpace(scope.OwnerID)
	profileID := agentcore.AgentProfileID(discussionsession.PersonalAssistantAgentID(tenantID, ownerID))
	if profileID.IsZero() {
		return agentcore.AgentProfile{}, devicedirectory.ErrInvalidRequest
	}
	profile, err := s.profiles.GetProfile(ctx, profileID)
	if err == nil {
		if !profile.IsPersonalAssistant() || strings.TrimSpace(profile.PersonalAssistantOwnerID) != ownerID || !strings.EqualFold(strings.TrimSpace(profile.PersonalAssistantTenantID), tenantID) {
			return agentcore.AgentProfile{}, fmt.Errorf("personal assistant profile %s has incompatible ownership", profileID)
		}
		profile, authorityChanged := removeInheritedPersonalAssistantAuthority(profile)
		if !hasLearningReviewCapability(profile.ToolRefs) {
			profile.ToolRefs = toolstd.EnsureRSILearningReadToolRef(profile.ToolRefs)
			authorityChanged = true
		}
		if !toolstd.HasSessionHistoryReadToolRefs(profile.ToolRefs) {
			profile.ToolRefs = toolstd.EnsureSessionHistoryReadToolRefs(profile.ToolRefs)
			authorityChanged = true
		}
		if !toolstd.HasImageGenerateToolRef(profile.ToolRefs) {
			profile.ToolRefs = toolstd.EnsureImageGenerateToolRef(profile.ToolRefs)
			authorityChanged = true
		}
		if !toolstd.HasPersonalBrowserToolRefs(profile.ToolRefs) {
			profile.ToolRefs = toolstd.EnsurePersonalBrowserToolRefs(profile.ToolRefs)
			authorityChanged = true
		}
		if authorityChanged {
			if err := s.profiles.SaveProfile(ctx, profile); err != nil {
				return agentcore.AgentProfile{}, fmt.Errorf("save personal assistant authority boundary: %w", err)
			}
		}
		profile, err = s.ensureWorkspaceRoot(ctx, profile)
		if err != nil {
			return agentcore.AgentProfile{}, err
		}
		if err := s.ensureMemoryProfile(ctx, profile); err != nil {
			return agentcore.AgentProfile{}, err
		}
		profile, err = s.ensureKnowledgeBase(ctx, profile)
		if err != nil {
			return agentcore.AgentProfile{}, err
		}
		if err := registerPersonalAssistantProfile(profile); err != nil {
			return agentcore.AgentProfile{}, err
		}
		if err := s.ensureAvailability(ctx, profile); err != nil {
			return agentcore.AgentProfile{}, err
		}
		return profile, nil
	}
	if !errors.Is(err, agentcore.ErrNotFound) {
		return agentcore.AgentProfile{}, err
	}

	templateID := firstNonBlank(view.AssignedAgentID, configuredDiscussionAgentID(s.config))
	template, err := s.profiles.GetProfile(ctx, agentcore.AgentProfileID(templateID))
	if err != nil {
		return agentcore.AgentProfile{}, fmt.Errorf("load personal assistant profile template %s: %w", templateID, err)
	}
	profile = newPersonalAssistantProfile(profileID, tenantID, ownerID, template)
	profile.WorkspaceRoot = s.personalAssistantWorkspaceRoot(profile.ID)
	if err := s.profiles.SaveProfile(ctx, profile); err != nil {
		return agentcore.AgentProfile{}, fmt.Errorf("save personal assistant profile: %w", err)
	}
	if err := registerPersonalAssistantProfile(profile); err != nil {
		return agentcore.AgentProfile{}, err
	}
	if err := s.ensureMemoryProfile(ctx, profile); err != nil {
		return agentcore.AgentProfile{}, err
	}
	profile, err = s.ensureKnowledgeBase(ctx, profile)
	if err != nil {
		return agentcore.AgentProfile{}, err
	}
	if err := s.ensureAvailability(ctx, profile); err != nil {
		return agentcore.AgentProfile{}, err
	}
	return profile, nil
}

func registerExistingPersonalAssistantIdentities(ctx context.Context, profiles *agentcore.PlatformProfileRepository) error {
	if profiles == nil {
		return nil
	}
	const pageLimit = 200
	for offset := 0; ; offset += pageLimit {
		page, err := profiles.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{
			AgentKind:                 agentcore.AgentKindAI,
			IncludeAI:                 true,
			IncludePersonalAssistants: true,
			Placement:                 agentcore.TalentPlacementFilterAll,
			Page:                      agentcore.Page{Limit: pageLimit, Offset: offset},
		})
		if err != nil {
			return fmt.Errorf("list personal assistant profiles: %w", err)
		}
		for _, entry := range page.Entries {
			profile, err := profiles.GetProfile(ctx, entry.AgentProfileID)
			if err != nil {
				return fmt.Errorf("load personal assistant profile %s: %w", entry.AgentProfileID, err)
			}
			if !profile.IsPersonalAssistant() {
				continue
			}
			if err := registerPersonalAssistantProfile(profile); err != nil {
				return err
			}
		}
		if len(page.Entries) < pageLimit || offset+len(page.Entries) >= page.Total {
			return nil
		}
	}
}

func registerPersonalAssistantProfile(profile agentcore.AgentProfile) error {
	if !profile.IsPersonalAssistant() {
		return nil
	}
	if err := discussionsession.RegisterPersonalAssistantAgentID(
		profile.PersonalAssistantTenantID,
		profile.PersonalAssistantOwnerID,
		profile.ID.String(),
	); err != nil {
		return fmt.Errorf("register personal assistant profile %s: %w", profile.ID, err)
	}
	return nil
}

func (s *productionPersonalAssistantService) ensureMemoryProfile(ctx context.Context, profile agentcore.AgentProfile) error {
	if s.memory == nil || profile.PersonalMemoryProfileRef.IsZero() {
		return nil
	}
	memoryProfile := memoryProfileFromAgentProfile(profile)
	existing, err := s.memory.GetProfile(ctx, memoryProfile.ID)
	if err == nil && existing.ID != "" {
		return nil
	}
	if err != nil && !errors.Is(err, memcore.ErrNotFound) {
		return fmt.Errorf("load personal assistant memory profile: %w", err)
	}
	if err := s.memory.SaveProfile(ctx, memoryProfile); err != nil {
		return fmt.Errorf("save personal assistant memory profile: %w", err)
	}
	return nil
}

func (s *productionPersonalAssistantService) ensureKnowledgeBase(ctx context.Context, profile agentcore.AgentProfile) (agentcore.AgentProfile, error) {
	if s == nil || s.knowledge == nil {
		return profile, nil
	}
	if s.profiles == nil {
		return agentcore.AgentProfile{}, fmt.Errorf("personal assistant profile store is unavailable")
	}
	if currentID := strings.TrimSpace(profile.PersonalKnowledgeBaseID); currentID != "" && s.knowledgeBases != nil {
		base, err := s.knowledgeBases.GetKnowledgeBase(ctx, knowledgecore.KnowledgeBaseID(currentID))
		if err == nil && base.OwnerType == knowledgecore.OwnerAgent && strings.TrimSpace(base.OwnerID) == profile.ID.String() && base.Status == knowledgecore.KnowledgeActive {
			return profile, nil
		}
	}
	base, err := ensureDefaultKnowledgeBase(ctx, s.knowledge, knowledgecore.OwnerAgent, profile.ID.String(), s.knowledgeRoot)
	if err != nil {
		return agentcore.AgentProfile{}, fmt.Errorf("ensure personal assistant knowledge base: %w", err)
	}
	if strings.TrimSpace(base.ID) == "" {
		return agentcore.AgentProfile{}, fmt.Errorf("ensure personal assistant knowledge base: empty knowledge base id")
	}
	if strings.TrimSpace(profile.PersonalKnowledgeBaseID) == strings.TrimSpace(base.ID) {
		return profile, nil
	}
	profile.PersonalKnowledgeBaseID = strings.TrimSpace(base.ID)
	if err := s.profiles.SaveProfile(ctx, profile); err != nil {
		return agentcore.AgentProfile{}, fmt.Errorf("save personal assistant knowledge base reference: %w", err)
	}
	return profile, nil
}

func (s *productionPersonalAssistantService) personalAssistantWorkspaceRoot(profileID agentcore.AgentProfileID) string {
	baseDir := strings.TrimSpace(s.config.BaseDir)
	if baseDir == "" || profileID.IsZero() {
		return ""
	}
	return filepath.Join(baseDir, "workspaces", profileID.String())
}

func (s *productionPersonalAssistantService) ensureWorkspaceRoot(ctx context.Context, profile agentcore.AgentProfile) (agentcore.AgentProfile, error) {
	desired := s.personalAssistantWorkspaceRoot(profile.ID)
	if desired == "" || filepath.Clean(strings.TrimSpace(profile.WorkspaceRoot)) == filepath.Clean(desired) {
		return profile, nil
	}
	profile.WorkspaceRoot = desired
	if err := s.profiles.SaveProfile(ctx, profile); err != nil {
		return agentcore.AgentProfile{}, fmt.Errorf("save personal assistant workspace root: %w", err)
	}
	return profile, nil
}

func (s *productionPersonalAssistantService) ensureAvailability(ctx context.Context, profile agentcore.AgentProfile) error {
	availability, err := s.profiles.GetAvailability(ctx, profile.ID)
	if err != nil {
		return fmt.Errorf("load personal assistant availability: %w", err)
	}
	if availability.Status != agentcore.AvailabilityUnknown || !availability.AgentInstanceID.IsZero() {
		return nil
	}
	now := time.Now().UTC()
	if s.now != nil {
		now = s.now().UTC()
	}
	if err := s.profiles.SaveAvailability(ctx, agentcore.AgentAvailability{
		AgentProfileID:  profile.ID,
		AgentInstanceID: agentcore.AgentInstanceID(profile.ID),
		Status:          agentcore.AvailabilityAvailable,
		Capacity:        1,
		Source:          "personal-assistant-profile",
		UpdatedAt:       now,
	}); err != nil {
		return fmt.Errorf("save personal assistant availability: %w", err)
	}
	return nil
}

func (s *productionPersonalAssistantService) reconcileExistingProfiles(ctx context.Context) error {
	if s == nil || s.profiles == nil {
		return nil
	}
	assistants := make([]agentcore.AgentProfile, 0)
	const pageLimit = 200
	for offset := 0; ; offset += pageLimit {
		page, err := s.profiles.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{
			AgentKind:                 agentcore.AgentKindAI,
			IncludeAI:                 true,
			IncludePersonalAssistants: true,
			Placement:                 agentcore.TalentPlacementFilterAll,
			Page:                      agentcore.Page{Limit: pageLimit, Offset: offset},
		})
		if err != nil {
			return fmt.Errorf("list personal assistant profiles: %w", err)
		}
		for _, entry := range page.Entries {
			profile, err := s.profiles.GetProfile(ctx, entry.AgentProfileID)
			if err != nil {
				return fmt.Errorf("load personal assistant profile %s: %w", entry.AgentProfileID, err)
			}
			if !profile.IsPersonalAssistant() {
				continue
			}
			assistants = append(assistants, profile)
		}
		if len(page.Entries) < pageLimit || offset+len(page.Entries) >= page.Total {
			break
		}
	}

	// Persist every authority removal before touching external resource providers.
	for index, profile := range assistants {
		profile, changed := removeInheritedPersonalAssistantAuthority(profile)
		if !hasLearningReviewCapability(profile.ToolRefs) {
			profile.ToolRefs = toolstd.EnsureRSILearningReadToolRef(profile.ToolRefs)
			changed = true
		}
		if !toolstd.HasSessionHistoryReadToolRefs(profile.ToolRefs) {
			profile.ToolRefs = toolstd.EnsureSessionHistoryReadToolRefs(profile.ToolRefs)
			changed = true
		}
		if !toolstd.HasImageGenerateToolRef(profile.ToolRefs) {
			profile.ToolRefs = toolstd.EnsureImageGenerateToolRef(profile.ToolRefs)
			changed = true
		}
		if !toolstd.HasPersonalBrowserToolRefs(profile.ToolRefs) {
			profile.ToolRefs = toolstd.EnsurePersonalBrowserToolRefs(profile.ToolRefs)
			changed = true
		}
		if changed {
			if err := s.profiles.SaveProfile(ctx, profile); err != nil {
				return fmt.Errorf("save personal assistant profile %s authority boundary: %w", profile.ID, err)
			}
		}
		assistants[index] = profile
	}
	for _, profile := range assistants {
		var err error
		profile, err = s.ensureWorkspaceRoot(ctx, profile)
		if err != nil {
			return err
		}
		if err := s.ensureMemoryProfile(ctx, profile); err != nil {
			return err
		}
		profile, err = s.ensureKnowledgeBase(ctx, profile)
		if err != nil {
			return err
		}
		if err := s.ensureAvailability(ctx, profile); err != nil {
			return err
		}
	}
	return nil
}

func newPersonalAssistantProfile(profileID agentcore.AgentProfileID, tenantID, ownerID string, template agentcore.AgentProfile) agentcore.AgentProfile {
	profile := template
	profile.ID = profileID
	profile.PersonalAssistantOwnerID = strings.TrimSpace(ownerID)
	profile.PersonalAssistantTenantID = strings.TrimSpace(tenantID)
	profile.RealName = defaultPersonalAssistantDisplayName
	profile.DisplayName = defaultPersonalAssistantDisplayName
	profile.RoleName = defaultPersonalAssistantDisplayName
	profile.Email = ""
	profile.PersonSource = agentcore.PersonSourceFacts{}
	profile.AgentKind = agentcore.AgentKindAI
	profile.UserAccount = agentcore.UserAccountSpec{}
	profile.OrgIdentity.OrganizationRef = agentcore.OrganizationRef{ID: agentcore.OrganizationID(strings.TrimSpace(tenantID))}
	profile.OrgIdentity.DepartmentRef = agentcore.DepartmentRef{}
	profile.OrgIdentity.PositionRef = agentcore.PositionRef{}
	profile.OrgIdentity.EmploymentType = agentcore.EmploymentTypeAIAgent
	profile.OrgIdentity.SupervisionPolicy = agentcore.SupervisionPolicySupervised
	profile.OrgIdentity.ManagerAgentRef = agentcore.AgentProfileRef{}
	profile.OrgIdentity.ReportingLineRef = agentcore.ReportingLineRef{}
	profile.OrgIdentity.RoleRefs = []agentcore.RoleRef{}
	profile.OrgIdentity.AuthorityScope = ""
	profile.Placement = agentcore.TalentPlacement{Status: agentcore.TalentPlacementInternal}
	profile.Persona = agentcore.AgentPersona{
		Backstory:              "用户拥有并可独立配置的 Orrery 私人智能体。",
		ResponsibilityBoundary: "仅代表所属用户处理私人会话与用户明确授权的任务，并遵守用户当前权限、工具授权和工作区边界。",
		BehaviorBoundary:       "不得继承组织智能体身份，不得突破所属用户权限；有副作用的操作继续遵守审批与审计策略。",
	}
	profile.BaseAgent.SystemPrompt = defaultPersonalAssistantSystemPrompt()
	profile.PersonalKnowledgeBaseID = ""
	profile.WorkspaceRoot = ""
	profile.PersonalMemoryProfileRef = agentcore.MemoryProfileRef{
		MemoryProfileID: agentcore.MemoryProfileID(discussionsession.PersonalAssistantMemoryProfileID(profileID.String())),
		OwnerAgentID:    agentcore.AgentID(profileID),
		MemoryKind:      agentcore.MemoryKindPersonal,
		AccessScope:     "owner_only",
	}
	profile.CollectiveMemoryScopeRefs = []agentcore.MemoryProfileRef{}
	profile.SkillRefs = append(make([]agentcore.SkillRef, 0, len(template.SkillRefs)), template.SkillRefs...)
	profile.SkillRefs = skillstd.EnsureWebResearchSkillRef(profile.SkillRefs)
	profile.ToolRefs = append(make([]agentcore.ToolRef, 0, len(template.ToolRefs)), template.ToolRefs...)
	profile.ToolRefs = toolstd.EnsureKnowledgeSearchReadToolRef(profile.ToolRefs)
	profile.ToolRefs = toolstd.EnsureMemorySearchReadToolRef(profile.ToolRefs)
	profile.ToolRefs = toolstd.EnsureRSILearningReadToolRef(profile.ToolRefs)
	profile.ToolRefs = toolstd.EnsureSessionHistoryReadToolRefs(profile.ToolRefs)
	profile.ToolRefs = toolstd.EnsureEnterpriseGraphReadToolRef(profile.ToolRefs)
	profile.ToolRefs = toolstd.EnsureWebSearchReadToolRef(profile.ToolRefs)
	profile.ToolRefs = toolstd.EnsureWebFetchReadToolRef(profile.ToolRefs)
	profile.ToolRefs = toolstd.EnsureImageGenerateToolRef(profile.ToolRefs)
	profile.ToolRefs = toolstd.EnsurePersonalBrowserToolRefs(profile.ToolRefs)
	profile.MCPAuthorizations = nil
	profile.QAEnabled = false
	profile.Enabled = true
	return profile
}

func removeInheritedPersonalAssistantAuthority(profile agentcore.AgentProfile) (agentcore.AgentProfile, bool) {
	if !profile.IsPersonalAssistant() {
		return profile, false
	}
	changed := false
	tenantID := agentcore.OrganizationID(strings.TrimSpace(profile.PersonalAssistantTenantID))
	if profile.OrgIdentity.OrganizationRef.ID != tenantID {
		profile.OrgIdentity.OrganizationRef = agentcore.OrganizationRef{ID: tenantID}
		changed = true
	}
	if profile.OrgIdentity.DepartmentRef.ID != "" {
		profile.OrgIdentity.DepartmentRef = agentcore.DepartmentRef{}
		changed = true
	}
	if profile.OrgIdentity.PositionRef.ID != "" {
		profile.OrgIdentity.PositionRef = agentcore.PositionRef{}
		changed = true
	}
	if !profile.OrgIdentity.ManagerAgentRef.IsZero() {
		profile.OrgIdentity.ManagerAgentRef = agentcore.AgentProfileRef{}
		changed = true
	}
	if profile.OrgIdentity.ReportingLineRef.ID != "" {
		profile.OrgIdentity.ReportingLineRef = agentcore.ReportingLineRef{}
		changed = true
	}
	if len(profile.OrgIdentity.RoleRefs) != 0 {
		profile.OrgIdentity.RoleRefs = []agentcore.RoleRef{}
		changed = true
	}
	if strings.TrimSpace(profile.OrgIdentity.AuthorityScope) != "" {
		profile.OrgIdentity.AuthorityScope = ""
		changed = true
	}
	if profile.OrgIdentity.EmploymentType != agentcore.EmploymentTypeAIAgent {
		profile.OrgIdentity.EmploymentType = agentcore.EmploymentTypeAIAgent
		changed = true
	}
	if profile.OrgIdentity.SupervisionPolicy != agentcore.SupervisionPolicySupervised {
		profile.OrgIdentity.SupervisionPolicy = agentcore.SupervisionPolicySupervised
		changed = true
	}
	if len(profile.CollectiveMemoryScopeRefs) != 0 {
		profile.CollectiveMemoryScopeRefs = []agentcore.MemoryProfileRef{}
		changed = true
	}
	if len(profile.MCPAuthorizations) != 0 {
		profile.MCPAuthorizations = nil
		changed = true
	}
	return profile, changed
}

func defaultPersonalAssistantSystemPrompt() string {
	return "你是用户在 Orrery 中独立拥有和配置的个人助理。仅代表当前所属用户处理私人会话与明确授权的任务。你的有效权限不得超过当前用户权限，并且还受到当前工具绑定、渠道、工作区、审批和审计策略约束。使用配置给你的名称、人格、Skills、工具、知识库和记忆库，不要冒用任何组织智能体的身份或职责。"
}
