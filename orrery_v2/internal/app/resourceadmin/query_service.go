package resourceadmin

import (
	"context"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

type AgentDirectoryPort interface {
	QueryDirectory(ctx context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error)
}

type AgentProfileLookupPort interface {
	GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error)
}

type MemoryProfileReader interface {
	GetProfile(ctx context.Context, id agentcore.MemoryProfileID) (memcore.MemoryProfile, error)
	GetRecord(ctx context.Context, id string) (memcore.MemoryRecord, error)
	ListProfiles(ctx context.Context) ([]memcore.MemoryProfile, error)
	FindRecords(ctx context.Context, query memstore.RecordQuery) ([]memcore.MemoryRecord, error)
	ListCandidates(ctx context.Context, query memstore.CandidateQuery) ([]memcore.MemoryCandidate, error)
}

type QueryService struct {
	tools        toolres.Registry
	skills       skillres.Registry
	skillStore   *skillres.PackageStore
	skillBundles skillres.BundleRegistry
	discoverer   toolres.Discoverer
	agents       AgentDirectoryPort
	memory       MemoryProfileReader
}

func NewQueryService(tools toolres.Registry, skills skillres.Registry) *QueryService {
	return &QueryService{tools: tools, skills: skills}
}

func NewQueryServiceWithDiscoverer(tools toolres.Registry, skills skillres.Registry, discoverer toolres.Discoverer) *QueryService {
	return &QueryService{tools: tools, skills: skills, discoverer: discoverer}
}

func NewQueryServiceWithAgents(tools toolres.Registry, skills skillres.Registry, agents AgentDirectoryPort) *QueryService {
	return &QueryService{tools: tools, skills: skills, agents: agents}
}

func NewQueryServiceWithMemoryProfiles(tools toolres.Registry, skills skillres.Registry, memory MemoryProfileReader) *QueryService {
	return &QueryService{tools: tools, skills: skills, memory: memory}
}

func (s *QueryService) SetMemoryProfileReader(memory MemoryProfileReader) {
	s.memory = memory
}

func (s *QueryService) SetDiscoverer(discoverer toolres.Discoverer) {
	if s != nil {
		s.discoverer = discoverer
	}
}

func (s *QueryService) SetSkillPackageStore(store *skillres.PackageStore) {
	if s != nil {
		s.skillStore = store
	}
}

func (s *QueryService) SetSkillBundleRegistry(registry skillres.BundleRegistry) {
	if s != nil {
		s.skillBundles = registry
	}
}

func (s *QueryService) ListResources(ctx context.Context, viewer ViewerRef) (ResourceAdminViewDTO, error) {
	return s.ListResourcesFiltered(ctx, viewer, ListResourcesRequest{})
}

func (s *QueryService) ListResourcesFiltered(ctx context.Context, viewer ViewerRef, req ListResourcesRequest) (ResourceAdminViewDTO, error) {
	if s == nil || s.tools == nil || s.skills == nil {
		return ResourceAdminViewDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceRead); err != nil {
		return ResourceAdminViewDTO{}, err
	}
	if strings.TrimSpace(req.Kind) == "memory_profile" {
		return s.listMemoryProfiles(ctx, req)
	}
	tools, err := s.tools.List(ctx)
	if err != nil {
		return ResourceAdminViewDTO{}, err
	}
	skills, err := s.skills.List(ctx)
	if err != nil {
		return ResourceAdminViewDTO{}, err
	}
	skills = s.packageBackedSkills(ctx, skills)
	if requiresAgentDirectory(req) && s.agents == nil {
		return ResourceAdminViewDTO{}, ErrServiceDependency
	}
	boundResourceIDs, err := s.boundResourceIDsForAgent(ctx, req.BoundAgentID)
	if err != nil {
		return ResourceAdminViewDTO{}, err
	}
	items := make([]resourceListItem, 0, len(tools)+len(skills))
	for _, definition := range tools {
		if matchesToolResourceFilter(req, definition, boundResourceIDs) {
			items = append(items, resourceListItem{tool: definition})
		}
	}
	for _, definition := range skills {
		if matchesSkillResourceFilter(req, definition, boundResourceIDs) {
			items = append(items, resourceListItem{skill: definition})
		}
	}
	total := len(items)
	offset := req.Offset
	if offset < 0 {
		offset = 0
	}
	limit := req.Limit
	if limit <= 0 {
		limit = total
	}
	end := offset + limit
	if offset > total {
		offset = total
	}
	if end > total {
		end = total
	}
	view := ResourceAdminViewDTO{Page: PageDTO{Limit: req.Limit, Offset: req.Offset, Total: total, Returned: end - offset, HasMore: end < total}}
	pageItems := items[offset:end]
	for _, item := range pageItems {
		if item.tool.ID != "" {
			if item.tool.Kind == toolres.KindMCPServer {
				view.MCPServers = append(view.MCPServers, mapMCP(item.tool))
				continue
			}
			view.Tools = append(view.Tools, mapTool(item.tool))
			continue
		}
		if s.skillStore != nil {
			tree, err := s.skillStore.BuildFileTree(ctx, item.skill.ID)
			if err != nil {
				return ResourceAdminViewDTO{}, mapSkillPackageStoreError(err)
			}
			view.Skills = append(view.Skills, mapSkillWithTree(item.skill, tree))
			continue
		}
		view.Skills = append(view.Skills, mapSkill(item.skill))
	}
	if req.IncludeUsage || req.IncludeBindings {
		usage, bindings, err := s.buildResourceRelationshipDetails(ctx, pageItems, skills, req.IncludeUsage, req.IncludeBindings)
		if err != nil {
			return ResourceAdminViewDTO{}, err
		}
		view.Usage = usage
		view.Bindings = bindings
	}
	return view, nil
}

func (s *QueryService) packageBackedSkills(ctx context.Context, definitions []skillres.SkillDefinition) []skillres.SkillDefinition {
	if s == nil || s.skillStore == nil {
		return definitions
	}
	filtered := make([]skillres.SkillDefinition, 0, len(definitions))
	for _, definition := range definitions {
		if _, err := s.skillStore.BuildFileTree(ctx, definition.ID); err == nil {
			filtered = append(filtered, definition)
		}
	}
	return filtered
}
