package resourceadmin

import (
	"context"
	"errors"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	memretrieval "orrery_v2/internal/memory/retrieval"
	memstore "orrery_v2/internal/memory/store"
)

type memoryProfileCount struct {
	records    int
	candidates int
}

func (s *QueryService) listMemoryProfiles(ctx context.Context, req ListResourcesRequest) (ResourceAdminViewDTO, error) {
	if s.memory == nil {
		return ResourceAdminViewDTO{}, ErrServiceDependency
	}
	profiles, err := s.memory.ListProfiles(ctx)
	if err != nil {
		return ResourceAdminViewDTO{}, err
	}
	filterReq := req
	filterReq.BoundAgentID = ""
	items := make([]memcore.MemoryProfile, 0, len(profiles))
	for _, profile := range profiles {
		if isPersonalAssistantMemoryProfile(profile) {
			continue
		}
		if matchesMemoryProfileFilter(filterReq, profile) {
			items = append(items, profile)
		}
	}
	var resolvedBoundAgents map[agentcore.MemoryProfileID][]string
	if agentID := strings.TrimSpace(req.BoundAgentID); agentID != "" {
		if s.agents == nil {
			return ResourceAdminViewDTO{}, ErrServiceDependency
		}
		resolvedBoundAgents, err = s.memoryProfileAgentIDs(ctx, items, true)
		if err != nil {
			return ResourceAdminViewDTO{}, err
		}
		filtered := items[:0]
		for _, profile := range items {
			if hasStringFold(resolvedBoundAgents[profile.ID], agentID) {
				filtered = append(filtered, profile)
			}
		}
		items = filtered
	}
	total := len(items)
	offset, end := pageWindow(total, req.Offset, req.Limit)
	view := ResourceAdminViewDTO{Page: PageDTO{Limit: req.Limit, Offset: req.Offset, Total: total, Returned: end - offset, HasMore: end < total}}
	pageItems := items[offset:end]
	counts, err := s.memoryProfileCountsByID(ctx, pageItems, req.IncludeUsage || req.IncludeBindings)
	if err != nil {
		return ResourceAdminViewDTO{}, err
	}
	boundAgents := resolvedBoundAgents
	if boundAgents == nil {
		boundAgents, err = s.memoryProfileAgentIDs(ctx, pageItems, req.IncludeUsage || req.IncludeBindings)
		if err != nil {
			return ResourceAdminViewDTO{}, err
		}
	}
	for _, profile := range pageItems {
		count := counts[profile.ID]
		agentIDs := boundAgents[profile.ID]
		view.MemoryProfiles = append(view.MemoryProfiles, mapMemoryProfile(profile, count.records, count.candidates))
		if req.IncludeUsage {
			view.Usage = append(view.Usage, ResourceUsageDTO{
				ResourceID:     string(profile.ID),
				ResourceType:   "memory_profile",
				UsedByAgentIDs: agentIDs,
				UsageCount:     count.records,
			})
		}
		if req.IncludeBindings {
			for _, agentID := range agentIDs {
				view.Bindings = append(view.Bindings, ResourceBindingDTO{
					ResourceID:   string(profile.ID),
					ResourceType: "memory_profile",
					BindingType:  "agent_memory_profile_ref",
					BoundToType:  "agent",
					BoundToID:    agentID,
				})
			}
		}
	}
	return view, nil
}

func (s *QueryService) memoryProfileAgentIDs(ctx context.Context, profiles []memcore.MemoryProfile, include bool) (map[agentcore.MemoryProfileID][]string, error) {
	out := make(map[agentcore.MemoryProfileID][]string, len(profiles))
	wanted := make(map[agentcore.MemoryProfileID]struct{}, len(profiles))
	for _, profile := range profiles {
		wanted[profile.ID] = struct{}{}
		out[profile.ID] = agentIDsForMemoryProfile(profile)
	}
	if !include || s.agents == nil || len(profiles) == 0 {
		return out, nil
	}
	lookup, _ := s.agents.(AgentProfileLookupPort)
	offset := 0
	for {
		page, err := s.agents.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{
			Placement:     agentcore.TalentPlacementFilterAll,
			IncludeAI:     true,
			IncludeHumans: true,
			Page:          agentcore.Page{Limit: 200, Offset: offset},
		})
		if err != nil {
			return nil, err
		}
		for _, entry := range page.Entries {
			refs := []agentcore.MemoryProfileRef{entry.MemoryProfileRef}
			if lookup != nil {
				profile, err := lookup.GetProfile(ctx, entry.AgentProfileID)
				if err == nil {
					refs = append(refs, profile.CollectiveMemoryScopeRefs...)
				}
			}
			for _, ref := range refs {
				if _, ok := wanted[ref.MemoryProfileID]; !ok {
					continue
				}
				out[ref.MemoryProfileID] = appendUnique(out[ref.MemoryProfileID], entry.AgentProfileID.String())
			}
		}
		offset += len(page.Entries)
		if len(page.Entries) == 0 || offset >= page.Total {
			break
		}
	}
	return out, nil
}

func (s *QueryService) memoryProfileCountsByID(ctx context.Context, profiles []memcore.MemoryProfile, count bool) (map[agentcore.MemoryProfileID]memoryProfileCount, error) {
	counts := make(map[agentcore.MemoryProfileID]memoryProfileCount, len(profiles))
	if !count || len(profiles) == 0 {
		return counts, nil
	}
	profileIDs := make([]agentcore.MemoryProfileID, 0, len(profiles))
	seen := map[agentcore.MemoryProfileID]struct{}{}
	for _, profile := range profiles {
		if profile.ID == "" {
			continue
		}
		if _, ok := seen[profile.ID]; ok {
			continue
		}
		seen[profile.ID] = struct{}{}
		profileIDs = append(profileIDs, profile.ID)
	}
	if len(profileIDs) == 0 {
		return counts, nil
	}
	records, err := s.memory.FindRecords(ctx, memstore.RecordQuery{ProfileIDs: profileIDs})
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if record.ProfileID == "" {
			continue
		}
		count := counts[record.ProfileID]
		count.records++
		counts[record.ProfileID] = count
	}
	candidates, err := s.memory.ListCandidates(ctx, memstore.CandidateQuery{Status: memcore.CandidateProposed})
	if err != nil {
		return nil, err
	}
	profileByScope := make(map[string]agentcore.MemoryProfileID, len(profiles))
	for _, profile := range profiles {
		profileByScope[memoryScopeKey(profile.DefaultScope)] = profile.ID
	}
	for _, candidate := range candidates {
		profileID := profileByScope[memoryScopeKey(candidate.ProposedScope)]
		if profileID == "" {
			continue
		}
		count := counts[profileID]
		count.candidates++
		counts[profileID] = count
	}
	return counts, nil
}

func (s *QueryService) memoryProfileCounts(ctx context.Context, profileID agentcore.MemoryProfileID, count bool) (int, int, error) {
	if !count {
		return 0, 0, nil
	}
	records, err := s.memory.FindRecords(ctx, memstore.RecordQuery{ProfileIDs: []agentcore.MemoryProfileID{profileID}})
	if err != nil {
		return 0, 0, err
	}
	profile, err := s.memory.GetProfile(ctx, profileID)
	if err != nil {
		return 0, 0, err
	}
	candidates, err := s.memory.ListCandidates(ctx, memstore.CandidateQuery{
		ScopeKind: profile.DefaultScope.Kind,
		ScopeID:   profile.DefaultScope.ID,
		Status:    memcore.CandidateProposed,
	})
	if err != nil {
		return 0, 0, err
	}
	return len(records), len(candidates), nil
}

func memoryScopeKey(scope memcore.MemoryScope) string {
	return strings.ToLower(strings.TrimSpace(string(scope.Kind))) + "\x00" + strings.ToLower(strings.TrimSpace(scope.ID))
}

func (s *QueryService) GetMemoryProfile(ctx context.Context, viewer ViewerRef, profileID string) (MemoryProfileDTO, error) {
	if s == nil || s.memory == nil {
		return MemoryProfileDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceRead); err != nil {
		return MemoryProfileDTO{}, err
	}
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return MemoryProfileDTO{}, ErrInvalidRequest
	}
	profile, err := s.memory.GetProfile(ctx, agentcore.MemoryProfileID(profileID))
	if err != nil {
		if errors.Is(err, memcore.ErrNotFound) {
			return MemoryProfileDTO{}, ErrNotFound
		}
		return MemoryProfileDTO{}, err
	}
	if isPersonalAssistantMemoryProfile(profile) {
		return MemoryProfileDTO{}, ErrNotFound
	}
	recordCount, candidateCount, err := s.memoryProfileCounts(ctx, profile.ID, true)
	if err != nil {
		return MemoryProfileDTO{}, err
	}
	return mapMemoryProfile(profile, recordCount, candidateCount), nil
}

func (s *QueryService) GetPersonalMemoryProfileByAgent(ctx context.Context, viewer ViewerRef, agentID string) (MemoryProfileDTO, error) {
	if s == nil || s.memory == nil {
		return MemoryProfileDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceRead); err != nil {
		return MemoryProfileDTO{}, err
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return MemoryProfileDTO{}, ErrInvalidRequest
	}
	if agentcore.IsPersonalAssistantProfileID(agentID) {
		return MemoryProfileDTO{}, ErrNotFound
	}
	profiles, err := s.memory.ListProfiles(ctx)
	if err != nil {
		return MemoryProfileDTO{}, err
	}
	for _, profile := range profiles {
		if isPersonalAssistantMemoryProfile(profile) {
			continue
		}
		if profile.MemoryKind != agentcore.MemoryKindPersonal {
			continue
		}
		if memoryProfileBelongsToAgent(profile, agentID) {
			recordCount, candidateCount, err := s.memoryProfileCounts(ctx, profile.ID, true)
			if err != nil {
				return MemoryProfileDTO{}, err
			}
			return mapMemoryProfile(profile, recordCount, candidateCount), nil
		}
	}
	return MemoryProfileDTO{}, ErrNotFound
}

func (s *QueryService) BuildMemoryDigest(ctx context.Context, viewer ViewerRef, req BuildMemoryDigestRequest) (MemoryDigestDTO, error) {
	if s == nil || s.memory == nil {
		return MemoryDigestDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceRead); err != nil {
		return MemoryDigestDTO{}, err
	}
	agentID := strings.TrimSpace(req.AgentID)
	if agentID == "" || len(req.ProfileRefs) == 0 {
		return MemoryDigestDTO{}, ErrInvalidRequest
	}
	if agentcore.IsPersonalAssistantProfileID(agentID) {
		return MemoryDigestDTO{}, ErrNotFound
	}
	profileRefs := make([]agentcore.MemoryProfileRef, 0, len(req.ProfileRefs))
	for _, ref := range req.ProfileRefs {
		profileID := strings.TrimSpace(ref.MemoryProfileID)
		if profileID == "" {
			return MemoryDigestDTO{}, ErrInvalidRequest
		}
		memoryKind := agentcore.MemoryKind(strings.TrimSpace(ref.MemoryKind))
		if memoryKind != "" && !memoryKind.IsValid() {
			return MemoryDigestDTO{}, ErrInvalidRequest
		}
		profileRefs = append(profileRefs, agentcore.MemoryProfileRef{
			MemoryProfileID:   agentcore.MemoryProfileID(profileID),
			OwnerAgentID:      agentcore.AgentID(strings.TrimSpace(ref.OwnerAgentID)),
			MemoryKind:        memoryKind,
			AccessScope:       strings.TrimSpace(ref.AccessScope),
			RetrievalPolicy:   strings.TrimSpace(ref.RetrievalPolicy),
			CompressionPolicy: strings.TrimSpace(ref.CompressionPolicy),
		})
	}
	retriever := memretrieval.NewRetriever(s.memory, memretrieval.NewCompressor(memretrieval.CompressionOptions{}))
	digest, err := retriever.Retrieve(ctx, memretrieval.RetrieveRequest{
		AgentID:   agentcore.AgentID(agentID),
		MissionID: strings.TrimSpace(req.MissionID),
		TaskID:    strings.TrimSpace(req.TaskID),
		Subject: memcore.AccessSubject{
			AgentID:         agentcore.AgentID(agentID),
			OrganizationIDs: append([]string(nil), req.OrganizationIDs...),
			DepartmentIDs:   append([]string(nil), req.DepartmentIDs...),
			ProjectIDs:      append([]string(nil), req.ProjectIDs...),
			MissionIDs:      []string{strings.TrimSpace(req.MissionID)},
			DiscussionIDs:   append([]string(nil), req.DiscussionIDs...),
		},
		ProfileRefs: profileRefs,
	})
	if err != nil {
		if errors.Is(err, memcore.ErrNotFound) {
			return MemoryDigestDTO{}, ErrNotFound
		}
		return MemoryDigestDTO{}, err
	}
	return mapMemoryDigest(digest), nil
}

func isPersonalAssistantMemoryProfile(profile memcore.MemoryProfile) bool {
	if agentcore.IsPersonalAssistantProfileID(string(profile.OwnerRef.AgentID)) {
		return true
	}
	if profile.DefaultScope.Kind == memcore.ScopeAgent && agentcore.IsPersonalAssistantProfileID(profile.DefaultScope.ID) {
		return true
	}
	return agentcore.IsPersonalAssistantProfileID(strings.TrimPrefix(string(profile.ID), "memory-"))
}
