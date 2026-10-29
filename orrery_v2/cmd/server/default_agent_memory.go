package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	"orrery_v2/internal/platform/config"
	skillres "orrery_v2/internal/resource/skill"
	skillstd "orrery_v2/internal/resource/skill/std"
	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
)

type defaultMemoryProfileStore interface {
	SaveProfile(ctx context.Context, profile memcore.MemoryProfile) error
	GetProfile(ctx context.Context, id agentcore.MemoryProfileID) (memcore.MemoryProfile, error)
}

type agentMemoryProfileDirectory interface {
	QueryDirectory(ctx context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error)
	GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error)
	SaveProfile(ctx context.Context, profile agentcore.AgentProfile) error
}

func ensureProductionAgentRuntimeDefaults(
	ctx context.Context,
	cfg config.Config,
	profiles agentMemoryProfileDirectory,
	memory defaultMemoryProfileStore,
	tools toolres.Registry,
	skills skillres.Registry,
	packages *skillres.PackageStore,
) error {
	if err := ensureDefaultCoordinatorMemoryProfiles(ctx, memory); err != nil {
		return err
	}
	if err := ensureDefaultResourceCatalogWithConfig(ctx, tools, skills, cfg, packages); err != nil {
		return err
	}
	for _, agentID := range configuredServerBrowserAgentIDs(cfg) {
		if err := ensureAgentServerBrowser(ctx, profiles, tools, agentID); err != nil {
			return err
		}
	}
	if err := ensureAgentReadCapabilities(ctx, profiles); err != nil {
		return err
	}
	if err := ensureAgentMemoryProfiles(ctx, profiles, memory); err != nil {
		return err
	}
	customAgentID := configuredDiscussionAgentID(cfg)
	if customAgentID == "" || customAgentID == defaultLeadAgentID {
		return nil
	}
	return ensureCoordinatorMemoryProfilesForAgents(ctx, memory, []struct {
		agentID string
		role    string
	}{{agentID: customAgentID, role: "discussion"}})
}

func ensureDefaultCoordinatorMemoryProfiles(ctx context.Context, memory defaultMemoryProfileStore) error {
	return ensureCoordinatorMemoryProfilesForAgents(ctx, memory, []struct {
		agentID string
		role    string
	}{
		{agentID: defaultLeadAgentID, role: "lead"},
		{agentID: defaultCoordinatorAgentID, role: "coordinator"},
		{agentID: defaultQAPlanAgentID, role: "qa-plan"},
		{agentID: defaultQACheckAgentID, role: "qa-check"},
	})
}

func ensureCoordinatorMemoryProfilesForAgents(ctx context.Context, memory defaultMemoryProfileStore, seeds []struct {
	agentID string
	role    string
}) error {
	if memory == nil {
		return nil
	}
	for _, seed := range seeds {
		if strings.TrimSpace(seed.agentID) == "" {
			continue
		}
		profile := defaultCoordinatorMemoryProfile(seed.agentID, seed.role)
		existing, err := memory.GetProfile(ctx, profile.ID)
		if err == nil && existing.ID != "" {
			continue
		}
		if err != nil && !errors.Is(err, memcore.ErrNotFound) {
			return err
		}
		if err := memory.SaveProfile(ctx, profile); err != nil {
			return err
		}
	}
	return nil
}

func ensureAgentMemoryProfiles(ctx context.Context, profiles agentMemoryProfileDirectory, memory defaultMemoryProfileStore) error {
	if profiles == nil || memory == nil {
		return nil
	}
	offset := 0
	for {
		page, err := profiles.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{
			Placement:     agentcore.TalentPlacementFilterAll,
			IncludeAI:     true,
			IncludeHumans: true,
			Page:          agentcore.Page{Limit: 200, Offset: offset},
		})
		if err != nil {
			return err
		}
		for _, entry := range page.Entries {
			workforceProfile, err := profiles.GetProfile(ctx, entry.AgentProfileID)
			if err != nil {
				return fmt.Errorf("load agent profile %s while ensuring memory profiles: %w", entry.AgentProfileID, err)
			}
			if agentcore.BaseAgentUsesSessionOnlyMemory(workforceProfile.BaseAgent) {
				continue
			}
			if !workforceProfile.PersonalMemoryProfileRef.IsZero() {
				profile := memoryProfileFromAgentProfile(workforceProfile)
				existing, err := memory.GetProfile(ctx, profile.ID)
				if err != nil && !errors.Is(err, memcore.ErrNotFound) {
					return err
				}
				if errors.Is(err, memcore.ErrNotFound) || existing.ID == "" {
					if err := memory.SaveProfile(ctx, profile); err != nil {
						return err
					}
				}
			}
			changed, err := ensureWorkforceCollectiveMemoryRefs(ctx, memory, &workforceProfile)
			if err != nil {
				return err
			}
			if changed {
				if err := profiles.SaveProfile(ctx, workforceProfile); err != nil {
					return err
				}
			}
		}
		offset += len(page.Entries)
		if len(page.Entries) == 0 || offset >= page.Total {
			break
		}
	}
	return nil
}

func ensureAgentReadCapabilities(ctx context.Context, profiles agentMemoryProfileDirectory) error {
	if profiles == nil {
		return nil
	}
	offset := 0
	for {
		page, err := profiles.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{
			Placement:     agentcore.TalentPlacementFilterAll,
			IncludeAI:     true,
			IncludeHumans: true,
			Page:          agentcore.Page{Limit: 200, Offset: offset},
		})
		if err != nil {
			return err
		}
		for _, entry := range page.Entries {
			profile, err := profiles.GetProfile(ctx, entry.AgentProfileID)
			if err != nil {
				return fmt.Errorf("load agent profile %s while ensuring read capabilities: %w", entry.AgentProfileID, err)
			}
			if profile.ID.IsZero() {
				continue
			}

			changed := false
			if profile.SkillRefs == nil {
				profile.SkillRefs = []agentcore.SkillRef{}
				changed = true
			}
			if profile.AgentKind != agentcore.AgentKindHuman {
				if !agentcore.BaseAgentUsesSessionOnlyMemory(profile.BaseAgent) && !toolstd.HasSessionHistoryReadToolRefs(profile.ToolRefs) {
					profile.ToolRefs = toolstd.EnsureSessionHistoryReadToolRefs(profile.ToolRefs)
					changed = true
				}
				if !skillstd.HasWebResearchSkillRef(profile.SkillRefs) {
					profile.SkillRefs = skillstd.EnsureWebResearchSkillRef(profile.SkillRefs)
					changed = true
				}
				if !hasWebSearchReadCapability(profile.ToolRefs) {
					profile.ToolRefs = toolstd.EnsureWebSearchReadToolRef(profile.ToolRefs)
					changed = true
				}
				if !hasWebFetchReadCapability(profile.ToolRefs) {
					profile.ToolRefs = toolstd.EnsureWebFetchReadToolRef(profile.ToolRefs)
					changed = true
				}
			}
			if agentcore.BaseAgentUsesSessionOnlyMemory(profile.BaseAgent) {
				if changed {
					if err := profiles.SaveProfile(ctx, profile); err != nil {
						return err
					}
				}
				continue
			}
			if profile.PersonalMemoryProfileRef.IsZero() {
				profile.PersonalMemoryProfileRef = agentcore.MemoryProfileRef{
					MemoryProfileID: agentcore.MemoryProfileID("memory-" + profile.ID.String()),
					OwnerAgentID:    agentcore.AgentID(profile.ID),
					MemoryKind:      agentcore.MemoryKindPersonal,
				}
				changed = true
			}
			if profile.CollectiveMemoryScopeRefs == nil {
				profile.CollectiveMemoryScopeRefs = []agentcore.MemoryProfileRef{}
				changed = true
			}
			if !hasKnowledgeSearchReadCapability(profile.ToolRefs) {
				profile.ToolRefs = toolstd.EnsureKnowledgeSearchReadToolRef(profile.ToolRefs)
				changed = true
			}
			if !hasMemorySearchReadCapability(profile.ToolRefs) {
				profile.ToolRefs = toolstd.EnsureMemorySearchReadToolRef(profile.ToolRefs)
				changed = true
			}
			if profile.AgentKind != agentcore.AgentKindHuman && !hasLearningReviewCapability(profile.ToolRefs) {
				profile.ToolRefs = toolstd.EnsureRSILearningReadToolRef(profile.ToolRefs)
				changed = true
			}
			if !hasEnterpriseGraphReadCapability(profile.ToolRefs) {
				profile.ToolRefs = toolstd.EnsureEnterpriseGraphReadToolRef(profile.ToolRefs)
				changed = true
			}
			if changed {
				if err := profiles.SaveProfile(ctx, profile); err != nil {
					return err
				}
			}
		}
		offset += len(page.Entries)
		if len(page.Entries) == 0 || offset >= page.Total {
			break
		}
	}
	return nil
}

func hasKnowledgeSearchReadCapability(refs []agentcore.ToolRef) bool {
	for _, ref := range refs {
		if ref.ToolID == toolstd.KnowledgeSearch && strings.TrimSpace(ref.PermissionScope) == toolstd.PermissionKnowledgeRead {
			return true
		}
	}
	return false
}

func hasMemorySearchReadCapability(refs []agentcore.ToolRef) bool {
	for _, ref := range refs {
		if ref.ToolID == toolstd.MemorySearch && strings.TrimSpace(ref.PermissionScope) == toolstd.PermissionMemoryRead {
			return true
		}
	}
	return false
}

func hasEnterpriseGraphReadCapability(refs []agentcore.ToolRef) bool {
	buildContext := false
	searchObjects := false
	for _, ref := range refs {
		if strings.TrimSpace(ref.PermissionScope) != toolstd.PermissionEnterpriseGraphRead {
			continue
		}
		switch ref.ToolID {
		case toolstd.EnterpriseGraphBuildContextBundle:
			buildContext = true
		case toolstd.EnterpriseGraphSearchObjects:
			searchObjects = true
		}
	}
	return buildContext && searchObjects
}

func hasWebSearchReadCapability(refs []agentcore.ToolRef) bool {
	for _, ref := range refs {
		if ref.ToolID == toolstd.WebSearch && strings.TrimSpace(ref.PermissionScope) == toolstd.PermissionWebRead {
			return true
		}
	}
	return false
}

func hasWebFetchReadCapability(refs []agentcore.ToolRef) bool {
	for _, ref := range refs {
		if ref.ToolID == toolstd.WebFetch && strings.TrimSpace(ref.PermissionScope) == toolstd.PermissionWebRead {
			return true
		}
	}
	return false
}

func ensureWorkforceCollectiveMemoryRefs(ctx context.Context, memory defaultMemoryProfileStore, profile *agentcore.AgentProfile) (bool, error) {
	if profile == nil || memory == nil {
		return false, nil
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
	changed := false
	for _, scope := range scopes {
		owner := memcore.OwnerRef{OrganizationID: organizationID}
		if scope.Kind == memcore.ScopeDepartment {
			owner.DepartmentID = departmentID
		}
		memoryProfile := memcore.NewScopeMemoryProfile(scope, owner)
		existing, err := memory.GetProfile(ctx, memoryProfile.ID)
		if err == nil && existing.ID != "" {
			memoryProfile = existing
		} else if err != nil && !errors.Is(err, memcore.ErrNotFound) {
			return false, err
		} else if err := memory.SaveProfile(ctx, memoryProfile); err != nil {
			return false, err
		}
		ref := memcore.MemoryProfileRefForScope(memoryProfile)
		found := false
		for i := range profile.CollectiveMemoryScopeRefs {
			if profile.CollectiveMemoryScopeRefs[i].MemoryProfileID != ref.MemoryProfileID {
				continue
			}
			found = true
			if profile.CollectiveMemoryScopeRefs[i] != ref {
				profile.CollectiveMemoryScopeRefs[i] = ref
				changed = true
			}
			break
		}
		if !found {
			profile.CollectiveMemoryScopeRefs = append(profile.CollectiveMemoryScopeRefs, ref)
			changed = true
		}
	}
	if profile.CollectiveMemoryScopeRefs == nil {
		profile.CollectiveMemoryScopeRefs = []agentcore.MemoryProfileRef{}
		changed = true
	}
	return changed, nil
}

func memoryProfileFromAgentProfile(profile agentcore.AgentProfile) memcore.MemoryProfile {
	ref := profile.PersonalMemoryProfileRef
	owner := strings.TrimSpace(string(ref.OwnerAgentID))
	if owner == "" {
		owner = profile.ID.String()
	}
	kind := ref.MemoryKind
	if !kind.IsValid() {
		kind = agentcore.MemoryKindPersonal
	}
	return memcore.MemoryProfile{
		ID:         ref.MemoryProfileID,
		OwnerRef:   memcore.OwnerRef{AgentID: agentcore.AgentID(owner), OrganizationID: string(profile.OrgIdentity.OrganizationRef.ID), DepartmentID: string(profile.OrgIdentity.DepartmentRef.ID)},
		MemoryKind: kind,
		DefaultScope: memcore.MemoryScope{
			Kind: memcore.ScopeAgent,
			ID:   owner,
		},
		RetrievalPolicy:   memcore.RetrievalPolicy{MaxRecords: 20},
		CompressionPolicy: memcore.CompressionPolicy{MaxSummaryBytes: 4096},
		VisibilityPolicy:  memcore.VisibilityPolicy{Mode: memcore.VisibilityOwnerOnly, ScopeKind: memcore.ScopeAgent, ScopeID: owner},
		Enabled:           profile.Enabled,
	}
}

func defaultCoordinatorMemoryProfile(agentID, role string) memcore.MemoryProfile {
	return memcore.MemoryProfile{
		ID: agentcore.MemoryProfileID("memory-" + agentID),
		OwnerRef: memcore.OwnerRef{
			AgentID:        agentcore.AgentID(agentID),
			OrganizationID: defaultCoordinatorOrganizationID,
			DepartmentID:   defaultCoordinatorDepartmentID,
		},
		MemoryKind: agentcore.MemoryKindPersonal,
		DefaultScope: memcore.MemoryScope{
			Kind: memcore.ScopeAgent,
			ID:   agentID,
		},
		RetrievalPolicy:   memcore.RetrievalPolicy{MaxRecords: 20, Tags: []string{"system", "coordination", role}},
		CompressionPolicy: memcore.CompressionPolicy{MaxSummaryBytes: 4096},
		VisibilityPolicy:  memcore.VisibilityPolicy{Mode: memcore.VisibilityOwnerOnly, ScopeKind: memcore.ScopeAgent, ScopeID: agentID},
		Enabled:           true,
	}
}
