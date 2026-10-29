package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	agentidentity "orrery_v2/internal/agent/identity"
	discussionsession "orrery_v2/internal/app/discussionsession"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/operations"
	memcore "orrery_v2/internal/memory/core"
	"orrery_v2/internal/platform/config"
	toolstd "orrery_v2/internal/resource/tool/std"
)

//go:embed agent_templates/*.json
var defaultAgentTemplateFS embed.FS

const (
	defaultBusinessOrganizationID      = "minvia"
	legacySystemSupportOrganizationID  = "org-system-runtime-support"
	legacyAgentCoordinationOrgID       = "org-agent-coordination"
	legacyHumanCollaboratorsOrgID      = "org-human-collaborators"
	defaultSystemSupportOrganizationID = defaultBusinessOrganizationID
	defaultSystemSupportDepartmentID   = "dept-system-runtime-support"
	defaultLeadAgentPositionID         = "position-pm-agent"
	defaultCoordinatorPositionID       = "position-coordination-agent"
	defaultQAPlanPositionID            = "position-qa-plan-agent"
	defaultQACheckPositionID           = "position-qa-check-agent"
	defaultSuperadminPositionID        = "position-superadmin"
	defaultHumanPositionID             = "position-human-collaborator"
	defaultLeadAgentID                 = agentidentity.LeadProfileID
	defaultCoordinatorAgentID          = agentidentity.CoordinatorProfileID
	defaultQAPlanAgentID               = agentidentity.QAPlanProfileID
	defaultQACheckAgentID              = agentidentity.QACheckProfileID

	defaultCoordinatorOrganizationID = defaultSystemSupportOrganizationID
	defaultCoordinatorDepartmentID   = defaultSystemSupportDepartmentID
	defaultHumanOrganizationID       = defaultSystemSupportOrganizationID
	defaultHumanDepartmentID         = defaultSystemSupportDepartmentID
)

func ensureDefaultCoordinatorAgents(ctx context.Context, cfg config.Config, profiles *agentcore.PlatformProfileRepository, orgs *operations.OrganizationCatalogStore, sourceDeletion ...operations.SourceDeletionLifecyclePort) error {
	if profiles == nil || orgs == nil {
		return nil
	}
	var lifecycle operations.SourceDeletionLifecyclePort
	if len(sourceDeletion) > 0 {
		lifecycle = sourceDeletion[0]
	}
	if err := removeLegacyDefaultOrganizationCatalog(ctx, orgs, lifecycle); err != nil {
		return err
	}
	if err := ensureDefaultSystemSupportOrganization(ctx, orgs); err != nil {
		return err
	}
	defaults, err := defaultCoordinatorAgentProfilesFromConfig(cfg)
	if err != nil {
		return err
	}
	if err := removeNonWorkflowTemplateProfiles(ctx, profiles, defaults); err != nil {
		return err
	}
	for _, profile := range defaults {
		existing, found, err := getAgentProfile(ctx, profiles, profile.ID)
		if err != nil {
			return err
		}
		if !found {
			deleted, err := profiles.IsProfileDeleted(ctx, profile.ID)
			if err != nil {
				return err
			}
			if deleted && !isDefaultSystemWorkflowProfile(profile) {
				continue
			}
		}
		if found {
			profile = mergeDefaultAgentProfile(existing, profile)
		}
		if errs := agentcore.ValidateAgentProfile(profile); len(errs) > 0 {
			return operations.ErrInvalidRequest
		}
		if err := profiles.SaveProfile(ctx, profile); err != nil {
			return err
		}
		if err := saveDefaultAgentAvailability(ctx, profiles, profile); err != nil {
			return err
		}
	}
	return migrateLegacySystemSupportProfiles(ctx, profiles)
}

func ensureDefaultSystemSupportOrganization(ctx context.Context, orgs *operations.OrganizationCatalogStore) error {
	if orgs == nil {
		return nil
	}
	if err := orgs.SaveOrganization(ctx, operations.OrganizationDTO{OrganizationID: defaultSystemSupportOrganizationID, Name: "Minvia", Code: "minvia", Status: "active", DefaultDepartmentID: defaultSystemSupportDepartmentID, DefaultPositionID: defaultLeadAgentPositionID}); err != nil {
		return err
	}
	if err := migrateLegacySystemSupportDepartments(ctx, orgs); err != nil {
		return err
	}
	if err := orgs.SaveDepartment(ctx, operations.DepartmentDTO{DepartmentID: defaultSystemSupportDepartmentID, OrganizationID: defaultSystemSupportOrganizationID, Name: "Orrery 系统运行支持"}); err != nil {
		return err
	}
	for _, position := range defaultSystemSupportPositions() {
		if err := orgs.SavePosition(ctx, position); err != nil {
			return err
		}
	}
	return nil
}

func migrateLegacySystemSupportDepartments(ctx context.Context, orgs *operations.OrganizationCatalogStore) error {
	graph, err := orgs.ListOrganizations(ctx, operations.OrgGraphQuery{OrganizationID: legacySystemSupportOrganizationID})
	if err != nil {
		return err
	}
	for _, department := range graph.Departments {
		department.OrganizationID = defaultBusinessOrganizationID
		if err := orgs.SaveDepartment(ctx, department); err != nil {
			return err
		}
	}
	return nil
}

func defaultSystemSupportPositions() []operations.PositionDTO {
	return []operations.PositionDTO{
		{PositionID: defaultLeadAgentPositionID, DepartmentID: defaultSystemSupportDepartmentID, Name: "PM"},
		{PositionID: defaultCoordinatorPositionID, DepartmentID: defaultSystemSupportDepartmentID, Name: "协调 Agent"},
		{PositionID: defaultQAPlanPositionID, DepartmentID: defaultSystemSupportDepartmentID, Name: "QA 计划 Agent"},
		{PositionID: defaultQACheckPositionID, DepartmentID: defaultSystemSupportDepartmentID, Name: "QA 验收 Agent"},
		{PositionID: defaultOntologyStewardPositionID, DepartmentID: defaultSystemSupportDepartmentID, Name: "本体管理员"},
		{PositionID: defaultSuperadminPositionID, DepartmentID: defaultSystemSupportDepartmentID, Name: "Superadmin"},
		{PositionID: defaultHumanPositionID, DepartmentID: defaultSystemSupportDepartmentID, Name: "真人协作用户"},
	}
}

func removeLegacyDefaultOrganizationCatalog(ctx context.Context, orgs *operations.OrganizationCatalogStore, sourceDeletion operations.SourceDeletionLifecyclePort) error {
	if orgs == nil {
		return nil
	}
	for _, positionID := range []string{
		"position-agent-coordinator",
		"position-real-human-contributor",
		"position-pm-user",
	} {
		if err := orgs.DeletePosition(ctx, positionID); err != nil {
			return err
		}
	}
	for _, departmentID := range []string{
		"dept-agent-coordination",
		"dept-human-collaborators",
	} {
		if err := orgs.DeleteDepartment(ctx, departmentID); err != nil {
			return err
		}
	}
	for _, organizationID := range []string{
		legacyAgentCoordinationOrgID,
		legacyHumanCollaboratorsOrgID,
	} {
		organization, found, err := orgs.GetOrganization(ctx, organizationID)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		rollback := func(context.Context) error { return nil }
		if sourceDeletion != nil {
			rollback, err = sourceDeletion.PrepareSourceDeletion(
				ctx, "organization", organization.OrganizationID, []string{organization.OrganizationID}, "organization.delete:system_bootstrap",
			)
			if err != nil {
				return err
			}
		}
		if err := orgs.DeleteOrganization(ctx, organizationID); err != nil {
			return errors.Join(err, rollback(ctx))
		}
		if err := rollback(ctx); err != nil {
			return err
		}
	}
	return nil
}

func removeNonWorkflowTemplateProfiles(ctx context.Context, profiles *agentcore.PlatformProfileRepository, workflowProfiles []agentcore.AgentProfile) error {
	workflowIDs := make(map[agentcore.AgentProfileID]struct{}, len(workflowProfiles))
	for _, profile := range workflowProfiles {
		workflowIDs[profile.ID] = struct{}{}
	}
	for _, profileID := range removedDefaultTemplateProfileIDs() {
		if profileID.IsZero() {
			continue
		}
		if _, keep := workflowIDs[profileID]; keep {
			continue
		}
		deleted, err := profiles.IsProfileDeleted(ctx, profileID)
		if err != nil {
			return err
		}
		if deleted {
			continue
		}
		if _, found, err := getAgentProfile(ctx, profiles, profileID); err != nil {
			return err
		} else if !found {
			continue
		}
		if err := profiles.DeleteProfile(ctx, profileID, "default workflow bootstrap removed retired default profile"); err != nil {
			return err
		}
	}
	for _, fileName := range defaultAgentTemplateFiles() {
		profileID := agentcore.AgentProfileID(strings.TrimSuffix(fileName, ".json"))
		if profileID.IsZero() {
			continue
		}
		if _, keep := workflowIDs[profileID]; keep {
			continue
		}
		if _, found, err := getAgentProfile(ctx, profiles, profileID); err != nil {
			return err
		} else if !found {
			continue
		}
		if err := profiles.DeleteProfile(ctx, profileID, "default workflow bootstrap removed non-workflow profile"); err != nil {
			return err
		}
	}
	return nil
}

func removedDefaultTemplateProfileIDs() []agentcore.AgentProfileID {
	return []agentcore.AgentProfileID{
		"staffing-agent",
		"legal-orchestrator-agent",
		"legal-knowledge-agent",
		"legal-contract-review-agent",
		"legal-compliance-risk-agent",
	}
}

func isDefaultSystemWorkflowProfile(profile agentcore.AgentProfile) bool {
	return isSystemSupportOrganization(profile.OrgIdentity.OrganizationRef.ID) &&
		profile.OrgIdentity.EmploymentType == agentcore.EmploymentTypeSystem
}

func isSystemSupportOrganization(id agentcore.OrganizationID) bool {
	return id == agentcore.OrganizationID(defaultSystemSupportOrganizationID) ||
		id == agentcore.OrganizationID(legacySystemSupportOrganizationID) ||
		id == agentcore.OrganizationID(legacyAgentCoordinationOrgID) ||
		id == agentcore.OrganizationID(legacyHumanCollaboratorsOrgID)
}

func migrateLegacySystemSupportProfiles(ctx context.Context, profiles *agentcore.PlatformProfileRepository) error {
	if profiles == nil {
		return nil
	}
	offset := 0
	for {
		page, err := profiles.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{
			Placement: agentcore.TalentPlacementFilterAll, IncludeAI: true, IncludeHumans: true,
			Page: agentcore.Page{Limit: 200, Offset: offset},
		})
		if err != nil {
			return err
		}
		for _, entry := range page.Entries {
			profile, err := profiles.GetProfile(ctx, entry.AgentProfileID)
			if errors.Is(err, agentcore.ErrNotFound) {
				continue
			}
			if err != nil {
				return fmt.Errorf("load agent profile %s during business tenant migration: %w", entry.AgentProfileID, err)
			}
			changed := false
			switch profile.OrgIdentity.OrganizationRef.ID {
			case agentcore.OrganizationID(legacySystemSupportOrganizationID):
				profile.OrgIdentity.OrganizationRef.ID = agentcore.OrganizationID(defaultBusinessOrganizationID)
				changed = true
			case agentcore.OrganizationID(legacyAgentCoordinationOrgID):
				profile.OrgIdentity.OrganizationRef.ID = agentcore.OrganizationID(defaultBusinessOrganizationID)
				profile.OrgIdentity.DepartmentRef.ID = agentcore.DepartmentID(defaultSystemSupportDepartmentID)
				profile.OrgIdentity.PositionRef.ID = agentcore.PositionID(defaultCoordinatorPositionID)
				changed = true
			case agentcore.OrganizationID(legacyHumanCollaboratorsOrgID):
				profile.OrgIdentity.OrganizationRef.ID = agentcore.OrganizationID(defaultBusinessOrganizationID)
				profile.OrgIdentity.DepartmentRef.ID = agentcore.DepartmentID(defaultSystemSupportDepartmentID)
				profile.OrgIdentity.PositionRef.ID = agentcore.PositionID(defaultHumanPositionID)
				changed = true
			}
			refs := removeLegacyOrganizationMemoryRefs(profile.CollectiveMemoryScopeRefs)
			if len(refs) != len(profile.CollectiveMemoryScopeRefs) {
				profile.CollectiveMemoryScopeRefs = refs
				changed = true
			}
			if !changed {
				continue
			}
			if err := profiles.SaveProfile(ctx, profile); err != nil {
				return err
			}
		}
		offset += len(page.Entries)
		if len(page.Entries) == 0 || offset >= page.Total {
			return nil
		}
	}
}

func removeLegacyOrganizationMemoryRefs(refs []agentcore.MemoryProfileRef) []agentcore.MemoryProfileRef {
	legacyProfileIDs := make(map[agentcore.MemoryProfileID]struct{}, len(legacyBusinessOrganizationIDs()))
	legacyScopes := make(map[string]struct{}, len(legacyBusinessOrganizationIDs()))
	for _, legacyID := range legacyBusinessOrganizationIDs() {
		legacyProfileIDs[memcore.ScopeMemoryProfileID(memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: legacyID})] = struct{}{}
		legacyScopes[strings.ToLower(string(memcore.ScopeOrganization)+":"+legacyID)] = struct{}{}
	}
	out := make([]agentcore.MemoryProfileRef, 0, len(refs))
	for _, ref := range refs {
		_, legacyProfile := legacyProfileIDs[ref.MemoryProfileID]
		_, legacyScope := legacyScopes[strings.ToLower(strings.TrimSpace(ref.AccessScope))]
		if legacyProfile || legacyScope {
			continue
		}
		out = append(out, ref)
	}
	if out == nil {
		return []agentcore.MemoryProfileRef{}
	}
	return out
}

func isSystemSupportSeedIdentity(identity agentcore.AgentOrgIdentity) bool {
	return isSystemSupportOrganization(identity.OrganizationRef.ID) &&
		identity.DepartmentRef.ID == agentcore.DepartmentID(defaultSystemSupportDepartmentID)
}

func restoreSystemSupportOrgIdentity(profile *agentcore.AgentProfile, seeded agentcore.AgentOrgIdentity) {
	if profile == nil {
		return
	}
	profile.Placement = agentcore.TalentPlacement{Status: agentcore.TalentPlacementInternal}
	profile.OrgIdentity.OrganizationRef = seeded.OrganizationRef
	profile.OrgIdentity.DepartmentRef = seeded.DepartmentRef
	profile.OrgIdentity.PositionRef = seeded.PositionRef
	if len(profile.OrgIdentity.RoleRefs) == 0 && len(seeded.RoleRefs) > 0 {
		profile.OrgIdentity.RoleRefs = append([]agentcore.RoleRef(nil), seeded.RoleRefs...)
	}
	if profile.OrgIdentity.EmploymentType == "" {
		profile.OrgIdentity.EmploymentType = seeded.EmploymentType
	}
	if profile.OrgIdentity.SupervisionPolicy == "" {
		profile.OrgIdentity.SupervisionPolicy = seeded.SupervisionPolicy
	}
	if strings.TrimSpace(profile.OrgIdentity.AuthorityScope) == "" {
		profile.OrgIdentity.AuthorityScope = seeded.AuthorityScope
	}
}

func mergeDefaultAgentProfile(existing, seeded agentcore.AgentProfile) agentcore.AgentProfile {
	merged := existing
	if merged.ID.IsZero() {
		merged.ID = seeded.ID
	}
	if strings.TrimSpace(merged.RealName) == "" {
		merged.RealName = seeded.RealName
	}
	if strings.TrimSpace(merged.DisplayName) == "" {
		merged.DisplayName = seeded.DisplayName
	}
	if strings.TrimSpace(merged.RoleName) == "" {
		merged.RoleName = seeded.RoleName
	}
	if !merged.AgentKind.IsValid() {
		merged.AgentKind = seeded.AgentKind
	}
	merged.OrgIdentity = mergeDefaultAgentOrgIdentity(merged.OrgIdentity, seeded.OrgIdentity)
	if isSystemSupportSeedIdentity(seeded.OrgIdentity) {
		restoreSystemSupportOrgIdentity(&merged, seeded.OrgIdentity)
	}
	if strings.TrimSpace(merged.Persona.Backstory) == "" {
		merged.Persona.Backstory = seeded.Persona.Backstory
	}
	if strings.TrimSpace(merged.Persona.ResponsibilityBoundary) == "" {
		merged.Persona.ResponsibilityBoundary = seeded.Persona.ResponsibilityBoundary
	}
	if strings.TrimSpace(merged.Persona.BehaviorBoundary) == "" {
		merged.Persona.BehaviorBoundary = seeded.Persona.BehaviorBoundary
	}
	if !containsChinese(merged.RealName) {
		merged.RealName = seeded.RealName
	}
	if strings.TrimSpace(merged.DisplayName) == "" || !containsChinese(merged.DisplayName) {
		merged.DisplayName = seeded.DisplayName
	}
	if !containsChinese(merged.Persona.ResponsibilityBoundary) {
		merged.Persona.ResponsibilityBoundary = seeded.Persona.ResponsibilityBoundary
	}
	merged.BaseAgent = mergeDefaultBaseAgentSpec(merged.BaseAgent, seeded.BaseAgent)
	if merged.PersonalMemoryProfileRef.IsZero() {
		merged.PersonalMemoryProfileRef = seeded.PersonalMemoryProfileRef
	}
	if merged.CollectiveMemoryScopeRefs == nil {
		merged.CollectiveMemoryScopeRefs = append([]agentcore.MemoryProfileRef(nil), seeded.CollectiveMemoryScopeRefs...)
	}
	if merged.SkillRefs == nil && len(seeded.SkillRefs) > 0 {
		merged.SkillRefs = append([]agentcore.SkillRef(nil), seeded.SkillRefs...)
	}
	if merged.SkillRefs == nil {
		merged.SkillRefs = []agentcore.SkillRef{}
	}
	if merged.ToolRefs == nil && len(seeded.ToolRefs) > 0 {
		merged.ToolRefs = append([]agentcore.ToolRef(nil), seeded.ToolRefs...)
	}
	if merged.ToolRefs == nil {
		merged.ToolRefs = []agentcore.ToolRef{}
	}
	if len(merged.ToolRefs) > 0 && isDefaultSystemWorkflowProfile(merged) {
		merged.ToolRefs = mergeDefaultAgentToolRefs(merged.ToolRefs, seeded.ToolRefs)
	}
	if !merged.RuntimeSpec.HasKind() {
		merged.RuntimeSpec = seeded.RuntimeSpec
	} else {
		if strings.TrimSpace(merged.RuntimeSpec.ConfigRef) == "" {
			merged.RuntimeSpec.ConfigRef = seeded.RuntimeSpec.ConfigRef
		}
		if len(merged.RuntimeSpec.Config) == 0 && len(seeded.RuntimeSpec.Config) > 0 {
			merged.RuntimeSpec.Config = append([]byte(nil), seeded.RuntimeSpec.Config...)
		}
	}
	return merged
}

func mergeDefaultAgentToolRefs(existing []agentcore.ToolRef, seeded []agentcore.ToolRef) []agentcore.ToolRef {
	out := make([]agentcore.ToolRef, 0, len(existing)+len(seeded))
	seen := map[agentcore.ToolID]bool{}
	for _, ref := range existing {
		if strings.TrimSpace(string(ref.ToolID)) == "" || isRetiredDefaultWorkflowToolRef(ref.ToolID) {
			continue
		}
		out = append(out, ref)
		seen[ref.ToolID] = true
	}
	for _, ref := range seeded {
		if strings.TrimSpace(string(ref.ToolID)) == "" || seen[ref.ToolID] || isRetiredDefaultWorkflowToolRef(ref.ToolID) {
			continue
		}
		out = append(out, ref)
		seen[ref.ToolID] = true
	}
	return out
}

func isRetiredDefaultWorkflowToolRef(id agentcore.ToolID) bool {
	switch agentcore.ToolID(strings.TrimSpace(string(id))) {
	case toolstd.MissionContextList,
		toolstd.MissionContextSelect,
		toolstd.MissionTaskDelegate:
		return true
	default:
		return false
	}
}

func mergeDefaultBaseAgentSpec(existing, seeded agentcore.BaseAgentSpec) agentcore.BaseAgentSpec {
	if isLegacyDefaultBaseAgentPrompt(existing.SystemPrompt) && strings.TrimSpace(seeded.SystemPrompt) != "" {
		existing.SystemPrompt = seeded.SystemPrompt
	}
	return agentcore.MergeDefaultBaseAgentSpec(existing, seeded)
}

func isLegacyDefaultBaseAgentPrompt(prompt string) bool {
	prompt = strings.TrimSpace(prompt)
	return strings.Contains(prompt, "全局 BaseAgent 模板") ||
		strings.Contains(prompt, "Memory 占位：TODO(memory-team)") ||
		strings.Contains(prompt, "Knowledge 占位：TODO(knowledge-team)") ||
		strings.Contains(prompt, "你是 Orrery 系统托管的 Agent") ||
		(strings.Contains(prompt, "分析用户需求，澄清模糊目标，判断对话类型") &&
			strings.Contains(prompt, "简单任务可直接闭环并附加证据")) ||
		(strings.Contains(prompt, "Mission Control 主管 · 协作编排者，也是 Orrery Mission Control 的 Lead Agent") &&
			strings.Contains(prompt, "默认顺序是：识别意图") &&
			!strings.Contains(prompt, "所有工具调用都按可恢复操作处理"))
}

func mergeDefaultAgentOrgIdentity(existing, seeded agentcore.AgentOrgIdentity) agentcore.AgentOrgIdentity {
	merged := existing
	if merged.OrganizationRef.ID == "" {
		merged.OrganizationRef = seeded.OrganizationRef
	}
	if merged.DepartmentRef.ID == "" {
		merged.DepartmentRef = seeded.DepartmentRef
	}
	if merged.PositionRef.ID == "" {
		merged.PositionRef = seeded.PositionRef
	}
	if len(merged.RoleRefs) == 0 {
		merged.RoleRefs = append([]agentcore.RoleRef(nil), seeded.RoleRefs...)
	}
	if merged.EmploymentType == "" {
		merged.EmploymentType = seeded.EmploymentType
	}
	if merged.SupervisionPolicy == "" {
		merged.SupervisionPolicy = seeded.SupervisionPolicy
	}
	if strings.TrimSpace(merged.AuthorityScope) == "" {
		merged.AuthorityScope = seeded.AuthorityScope
	}
	return merged
}

func getAgentProfile(ctx context.Context, profiles *agentcore.PlatformProfileRepository, id agentcore.AgentProfileID) (agentcore.AgentProfile, bool, error) {
	return profiles.FindProfile(ctx, id)
}

func saveDefaultAgentAvailability(ctx context.Context, profiles *agentcore.PlatformProfileRepository, profile agentcore.AgentProfile) error {
	if !profile.Enabled {
		return nil
	}
	targetID := defaultBootstrapAgentIdentity(profile.ID)
	availability, err := profiles.GetAvailability(ctx, profile.ID)
	if err == nil && availability.Status == agentcore.AvailabilityAvailable && availability.AgentInstanceID == targetID {
		return nil
	}
	return profiles.SaveAvailability(ctx, agentcore.AgentAvailability{
		AgentProfileID:  profile.ID,
		AgentInstanceID: targetID,
		Status:          agentcore.AvailabilityAvailable,
		Capacity:        1,
		Source:          "system-bootstrap",
		UpdatedAt:       time.Now().UTC(),
	})
}

func defaultBootstrapAgentIdentity(profileID agentcore.AgentProfileID) agentcore.AgentInstanceID {
	if profileID.IsZero() {
		return ""
	}
	return agentcore.AgentInstanceID(profileID.String())
}

func defaultCoordinatorAgentProfiles(runtime agentcore.RuntimeSpec) ([]agentcore.AgentProfile, error) {
	return defaultCoordinatorAgentProfilesWithRuntime("", func(string) agentcore.RuntimeSpec { return runtime })
}

func defaultCoordinatorAgentProfilesFromConfig(cfg config.Config) ([]agentcore.AgentProfile, error) {
	return defaultCoordinatorAgentProfilesWithRuntime(configuredDiscussionAgentID(cfg), func(agentID string) agentcore.RuntimeSpec {
		return defaultCoordinatorAgentRuntimeSpec(cfg, agentID)
	})
}

func defaultCoordinatorAgentRuntimeSpec(cfg config.Config, agentID string) agentcore.RuntimeSpec {
	return defaultAgentRuntimeSpec(cfg, agentID)
}

func defaultCoordinatorAgentProfilesWithRuntime(primaryAgentID string, runtimeFor func(string) agentcore.RuntimeSpec) ([]agentcore.AgentProfile, error) {
	seeds := defaultCoordinatorAgentSeeds()
	profiles := make([]agentcore.AgentProfile, 0, len(seeds))
	for i, seed := range seeds {
		template, err := readDefaultAgentTemplate(seed.templateFile)
		if err != nil {
			return nil, err
		}
		agentID := seed.id
		if i == 0 && strings.TrimSpace(primaryAgentID) != "" {
			agentID = strings.TrimSpace(primaryAgentID)
		}
		profiles = append(profiles, defaultCoordinatorAgentProfile(agentID, seed.role, runtimeFor(agentID), template))
	}
	return profiles, nil
}

func initialAgentMemberSeedsFromConfig(cfg config.Config) ([]discussionsession.InitialAgentMemberSeed, error) {
	templates, err := defaultCoordinatorAgentTemplates()
	if err != nil {
		return nil, err
	}
	return initialAgentMemberSeedsFromTemplates(configuredDiscussionAgentID(cfg), templates), nil
}

func defaultSystemRoleBootstrapsFromConfig(cfg config.Config) ([]missioncontrol.SystemRoleBootstrap, error) {
	templates, err := defaultCoordinatorAgentTemplates()
	if err != nil {
		return nil, err
	}
	return defaultSystemRoleBootstrapsFromTemplates(configuredDiscussionAgentID(cfg), templates), nil
}

func defaultCoordinatorAgentTemplates() ([]defaultAgentTemplate, error) {
	seeds := defaultCoordinatorAgentSeeds()
	templates := make([]defaultAgentTemplate, 0, len(seeds))
	for _, seed := range seeds {
		template, err := readDefaultAgentTemplate(seed.templateFile)
		if err != nil {
			return nil, err
		}
		templates = append(templates, template)
	}
	return templates, nil
}

type defaultCoordinatorAgentSeed struct {
	templateFile string
	id           string
	role         string
}

func defaultCoordinatorAgentSeeds() []defaultCoordinatorAgentSeed {
	return []defaultCoordinatorAgentSeed{
		{templateFile: "lead-agent.json", id: defaultLeadAgentID, role: "lead"},
		{templateFile: "coordinator-agent.json", id: defaultCoordinatorAgentID, role: "coordinator"},
		{templateFile: "qa-plan-agent.json", id: defaultQAPlanAgentID, role: "qa-plan"},
		{templateFile: "qa-check-agent.json", id: defaultQACheckAgentID, role: "qa-check"},
	}
}

func defaultCoordinatorAgentProfile(id, role string, runtime agentcore.RuntimeSpec, template defaultAgentTemplate) agentcore.AgentProfile {
	name := defaultAgentChineseName(strings.TrimSpace(template.Slug), strings.TrimSpace(template.Name))
	roleName := strings.TrimSpace(template.WorkerConfig.Role)
	authorityScope := strings.TrimSpace(template.WorkerConfig.AuthorityScope)
	if authorityScope == "" {
		authorityScope = "mission_coordination"
	}
	return agentcore.AgentProfile{
		ID:          agentcore.AgentProfileID(id),
		RealName:    name,
		DisplayName: name,
		RoleName:    roleName,
		AgentKind:   agentcore.AgentKindAI,
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef:   agentcore.OrganizationRef{ID: agentcore.OrganizationID(defaultCoordinatorOrganizationID)},
			DepartmentRef:     agentcore.DepartmentRef{ID: agentcore.DepartmentID(defaultCoordinatorDepartmentID)},
			PositionRef:       agentcore.PositionRef{ID: defaultSystemAgentPositionID(role)},
			RoleRefs:          []agentcore.RoleRef{{ID: agentcore.RoleID(role)}},
			EmploymentType:    agentcore.EmploymentTypeSystem,
			SupervisionPolicy: agentcore.SupervisionPolicyRoot,
			AuthorityScope:    authorityScope,
		},
		Persona: agentcore.AgentPersona{
			Backstory:              "",
			ResponsibilityBoundary: defaultAgentChineseResponsibility(strings.TrimSpace(template.Slug), roleName),
			BehaviorBoundary:       "Coordinate and report through Orrery Mission-native records. Do not bypass configured Agent permissions, resource bindings, or audit boundaries.",
		},
		BaseAgent: defaultTemplateBaseAgentSpec(name, roleName, template),
		PersonalMemoryProfileRef: agentcore.MemoryProfileRef{
			MemoryProfileID: agentcore.MemoryProfileID("memory-" + id),
			OwnerAgentID:    agentcore.AgentID(id),
			MemoryKind:      agentcore.MemoryKindPersonal,
			AccessScope:     "system",
		},
		CollectiveMemoryScopeRefs: []agentcore.MemoryProfileRef{},
		SkillRefs:                 defaultTemplateSkillRefs(template),
		ToolRefs:                  defaultTemplateToolRefs(template),
		RuntimeSpec:               runtime,
		QAEnabled:                 template.WorkerConfig.QA.Enabled,
		Enabled:                   true,
	}
}

func defaultSystemAgentPositionID(role string) agentcore.PositionID {
	switch strings.TrimSpace(role) {
	case "lead":
		return agentcore.PositionID(defaultLeadAgentPositionID)
	case "coordinator":
		return agentcore.PositionID(defaultCoordinatorPositionID)
	case "qa-plan":
		return agentcore.PositionID(defaultQAPlanPositionID)
	case "qa-check":
		return agentcore.PositionID(defaultQACheckPositionID)
	default:
		return agentcore.PositionID(defaultCoordinatorPositionID)
	}
}

func defaultBaseAgentSpec(name, roleName string) agentcore.BaseAgentSpec {
	return agentcore.DefaultBaseAgentSpec(name, roleName)
}

func defaultTemplateBaseAgentSpec(name, roleName string, template defaultAgentTemplate) agentcore.BaseAgentSpec {
	spec := defaultBaseAgentSpec(name, roleName)
	if prompt := strings.TrimSpace(template.Instructions); prompt != "" {
		spec.SystemPrompt = prompt
	}
	return spec
}
