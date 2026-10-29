package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	knowledgecore "orrery_v2/internal/knowledge/core"
)

const talentMarketImportExtensionKey = "talent_market_import"

type talentMarketImportMetadata struct {
	Description  string `json:"description,omitempty"`
	Category     string `json:"category,omitempty"`
	SourceSlug   string `json:"source_slug,omitempty"`
	SourcePath   string `json:"source_path,omitempty"`
	SourceHash   string `json:"source_hash,omitempty"`
	SourcePrompt string `json:"source_prompt,omitempty"`
}

func (s *CommandService) PreviewTalentMarketImport(ctx context.Context, viewer ViewerRef, req TalentMarketImportPreviewRequest) (TalentMarketImportPreviewResponseDTO, error) {
	if s == nil || s.profiles == nil || s.directory == nil {
		return TalentMarketImportPreviewResponseDTO{}, ErrServiceDependency
	}
	if err := requireOperationsPermission(viewer, PermissionWorkforceManage); err != nil {
		return TalentMarketImportPreviewResponseDTO{}, err
	}
	if err := s.authorizePlatformOperation(ctx, viewer, PermissionWorkforceManage); err != nil {
		return TalentMarketImportPreviewResponseDTO{}, err
	}
	candidates := normalizeTalentMarketImportCandidates(req.Candidates)
	if len(candidates) == 0 {
		return TalentMarketImportPreviewResponseDTO{}, fmt.Errorf("%w: candidates are required", ErrInvalidRequest)
	}
	skills := normalizeTalentMarketSkillDTOs(req.Skills)
	existing, err := s.listTalentMarketImportExistingProfiles(ctx)
	if err != nil {
		return TalentMarketImportPreviewResponseDTO{}, err
	}
	return buildTalentMarketImportPreview(candidates, existing, skills), nil
}

func (s *CommandService) CommitTalentMarketImport(ctx context.Context, viewer ViewerRef, req TalentMarketImportCommitRequest) (TalentMarketImportCommitResponseDTO, error) {
	if s == nil || s.profiles == nil || s.directory == nil {
		return TalentMarketImportCommitResponseDTO{}, ErrServiceDependency
	}
	if err := requireOperationsPermission(viewer, PermissionWorkforceManage); err != nil {
		return TalentMarketImportCommitResponseDTO{}, err
	}
	if err := s.authorizePlatformOperation(ctx, viewer, PermissionWorkforceManage); err != nil {
		return TalentMarketImportCommitResponseDTO{}, err
	}
	candidates := normalizeTalentMarketImportCandidates(req.Candidates)
	if len(candidates) == 0 {
		return TalentMarketImportCommitResponseDTO{}, fmt.Errorf("%w: candidates are required", ErrInvalidRequest)
	}
	skills := normalizeTalentMarketSkillDTOs(req.Skills)
	packagedSkills := talentMarketSkillDefinitionsByID(skills)
	existing, err := s.listTalentMarketImportExistingProfiles(ctx)
	if err != nil {
		return TalentMarketImportCommitResponseDTO{}, err
	}
	preview := buildTalentMarketImportPreview(candidates, existing, skills)
	response := TalentMarketImportCommitResponseDTO{Preview: preview}
	for i, candidate := range candidates {
		resolution := strings.ToLower(strings.TrimSpace(candidate.ConflictResolution))
		if resolution == "" {
			resolution = "create"
		}
		if resolution == "skip" {
			response.Skipped = append(response.Skipped, TalentMarketImportSkippedDTO{CandidateIndex: i, RealName: candidate.RealName, Reason: "skipped"})
			continue
		}
		itemConflicts := previewConflictsForCandidate(preview.Conflicts, i)
		if len(itemConflicts) > 0 && resolution == "create" {
			response.Conflicts = append(response.Conflicts, itemConflicts...)
			continue
		}

		targetID := agentcore.AgentProfileID(strings.TrimSpace(candidate.AgentProfileID))
		if resolution == "overwrite" {
			conflict, ok := firstOverwriteTarget(itemConflicts)
			if !ok {
				response.Skipped = append(response.Skipped, TalentMarketImportSkippedDTO{CandidateIndex: i, RealName: candidate.RealName, Reason: "overwrite_target_not_found"})
				continue
			}
			if conflict.ExistingPlacementStatus != string(agentcore.TalentPlacementMarket) {
				response.Conflicts = append(response.Conflicts, conflict)
				continue
			}
			targetID = agentcore.AgentProfileID(conflict.ExistingAgentProfileID)
			candidate.AgentProfileID = targetID.String()
		}
		if resolution == "rename" {
			renamed := strings.TrimSpace(candidate.ResolvedRealName)
			if renamed == "" {
				response.Skipped = append(response.Skipped, TalentMarketImportSkippedDTO{CandidateIndex: i, RealName: candidate.RealName, Reason: "resolved_real_name_required"})
				continue
			}
			candidate.RealName = renamed
			candidate.DisplayName = firstNonEmpty(candidate.DisplayName, renamed)
			if strings.TrimSpace(candidate.DisplayName) == strings.TrimSpace(req.Candidates[i].RealName) {
				candidate.DisplayName = renamed
			}
			if hasNameConflict(candidate, existing, targetID) {
				response.Skipped = append(response.Skipped, TalentMarketImportSkippedDTO{CandidateIndex: i, RealName: candidate.RealName, Reason: "resolved_name_conflict"})
				continue
			}
		}
		if targetID.IsZero() {
			generatedID, err := s.newID("profile")
			if err != nil {
				return TalentMarketImportCommitResponseDTO{}, err
			}
			targetID = agentcore.AgentProfileID(generatedID)
			candidate.AgentProfileID = generatedID
		}
		if agentcore.IsReservedNonWorkforceProfileID(targetID.String()) {
			return TalentMarketImportCommitResponseDTO{}, fmt.Errorf("%w: profile id is reserved for a non-workforce principal", ErrInvalidRequest)
		}

		profile, err := s.profileFromTalentMarketCandidate(ctx, candidate, targetID)
		if err != nil {
			return TalentMarketImportCommitResponseDTO{}, err
		}
		ownerType := profileKnowledgeOwnerType(string(profile.AgentKind))
		createdKnowledgeBaseID := ""
		if strings.TrimSpace(profile.PersonalKnowledgeBaseID) == "" {
			knowledgeBaseID, err := s.createLifecycleKnowledgeBase(ctx, viewer, ownerType, profile.ID.String(), personalKnowledgeBaseName(profile.DisplayName), "人员个人基础知识库", knowledgecore.VisibilityPrivate)
			if err != nil {
				return TalentMarketImportCommitResponseDTO{}, err
			}
			profile.PersonalKnowledgeBaseID = knowledgeBaseID
			createdKnowledgeBaseID = knowledgeBaseID
		}
		if err := s.ensureImportedAgentSkills(ctx, candidate, packagedSkills); err != nil {
			return TalentMarketImportCommitResponseDTO{}, s.cleanupLifecycleKnowledgeBaseOnError(ctx, viewer, ownerType, profile.ID.String(), createdKnowledgeBaseID, err)
		}
		if err := s.profiles.SaveProfile(ctx, profile); err != nil {
			return TalentMarketImportCommitResponseDTO{}, s.cleanupLifecycleKnowledgeBaseOnError(ctx, viewer, ownerType, profile.ID.String(), createdKnowledgeBaseID, err)
		}
		if err := s.ensurePersonalMemoryProfile(ctx, profile, true); err != nil {
			return TalentMarketImportCommitResponseDTO{}, err
		}
		availabilityStatus := agentcore.AvailabilityAvailable
		if !profile.Enabled {
			availabilityStatus = agentcore.AvailabilityDisabled
		}
		if err := s.profiles.SaveAvailability(ctx, agentcore.AgentAvailability{
			AgentProfileID:  profile.ID,
			AgentInstanceID: agentcore.AgentInstanceID(profile.ID),
			Status:          availabilityStatus,
			Source:          "operations.talent_market.import",
			UpdatedAt:       time.Now().UTC(),
		}); err != nil {
			return TalentMarketImportCommitResponseDTO{}, err
		}
		response.Imported = append(response.Imported, mapMemberFromProfile(entryFromProfile(profile, profile.ID.String()), profile))
		existing = replaceTalentMarketImportExisting(existing, profile)
		if err := s.recordAudit(ctx, viewer, "talent_market.import", profile.ID.String(), map[string]string{
			"agent_profile_id": profile.ID.String(),
			"real_name":        profile.RealName,
			"resolution":       resolution,
			"placement":        "market",
		}); err != nil {
			return TalentMarketImportCommitResponseDTO{}, err
		}
	}
	return response, nil
}

func (s *CommandService) ExportTalentMarket(ctx context.Context, viewer ViewerRef, req TalentMarketExportRequest) (TalentMarketExportPackageDTO, error) {
	if s == nil || s.profiles == nil || s.directory == nil {
		return TalentMarketExportPackageDTO{}, ErrServiceDependency
	}
	if err := requireOperationsPermission(viewer, PermissionWorkforceRead); err != nil {
		return TalentMarketExportPackageDTO{}, err
	}
	if err := s.authorizePlatformOperation(ctx, viewer, PermissionWorkforceRead); err != nil {
		return TalentMarketExportPackageDTO{}, err
	}
	wanted := map[string]bool{}
	for _, id := range req.AgentProfileIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			wanted[id] = true
		}
	}
	if len(wanted) == 0 {
		return TalentMarketExportPackageDTO{}, fmt.Errorf("%w: agent_profile_ids are required", ErrInvalidRequest)
	}
	page, err := s.directory.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{Placement: agentcore.TalentPlacementFilterMarket, IncludeAI: true, IncludeHumans: true})
	if err != nil {
		return TalentMarketExportPackageDTO{}, err
	}
	agents := make([]TalentMarketImportCandidateDTO, 0, len(wanted))
	for _, entry := range page.Entries {
		id := entry.AgentProfileID.String()
		if !wanted[id] {
			continue
		}
		profile, err := s.profiles.GetProfile(ctx, entry.AgentProfileID)
		if err != nil {
			return TalentMarketExportPackageDTO{}, err
		}
		if !profile.Placement.IsMarket(profile.OrgIdentity) {
			continue
		}
		agents = append(agents, candidateFromTalentMarketProfile(profile))
		delete(wanted, id)
	}
	sort.Slice(agents, func(i, j int) bool {
		return strings.Compare(agents[i].RealName, agents[j].RealName) < 0
	})
	skills, err := s.collectTalentMarketExportSkills(ctx, agents)
	if err != nil {
		return TalentMarketExportPackageDTO{}, err
	}
	return TalentMarketExportPackageDTO{
		Manifest: TalentMarketExportManifestDTO{
			Format:      "orrery.talent-market",
			Version:     1,
			Count:       len(agents),
			ExportedAt:  time.Now().UTC().Format(time.RFC3339),
			SourceApp:   "orrery",
			Description: "Orrery Talent Market export package",
		},
		Agents: agents,
		Skills: skills,
	}, nil
}

func (s *CommandService) listTalentMarketImportExistingProfiles(ctx context.Context) ([]agentcore.AgentProfile, error) {
	page, err := s.directory.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{Placement: agentcore.TalentPlacementFilterAll, IncludeAI: true, IncludeHumans: true})
	if err != nil {
		return nil, err
	}
	profiles := make([]agentcore.AgentProfile, 0, len(page.Entries))
	for _, entry := range page.Entries {
		if entry.AgentProfileID.IsZero() {
			continue
		}
		profile, err := s.profiles.GetProfile(ctx, entry.AgentProfileID)
		if err != nil {
			continue
		}
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

func buildTalentMarketImportPreview(candidates []TalentMarketImportCandidateDTO, existing []agentcore.AgentProfile, skills []TalentMarketSkillDefinitionDTO) TalentMarketImportPreviewResponseDTO {
	response := TalentMarketImportPreviewResponseDTO{
		Items:  make([]TalentMarketImportPreviewItemDTO, 0, len(candidates)),
		Skills: normalizeTalentMarketSkillDTOs(skills),
	}
	for i, candidate := range candidates {
		conflicts := conflictsForTalentMarketCandidate(candidate, existing, i, "")
		status := "create"
		if len(conflicts) > 0 {
			status = "conflict"
			response.Conflicts = append(response.Conflicts, conflicts...)
		}
		response.Items = append(response.Items, TalentMarketImportPreviewItemDTO{
			CandidateIndex: i,
			Candidate:      candidate,
			Status:         status,
			Conflicts:      conflicts,
		})
	}
	return response
}

func conflictsForTalentMarketCandidate(candidate TalentMarketImportCandidateDTO, existing []agentcore.AgentProfile, candidateIndex int, ignoreProfileID agentcore.AgentProfileID) []TalentMarketImportConflictDTO {
	var conflicts []TalentMarketImportConflictDTO
	candidateID := strings.TrimSpace(candidate.AgentProfileID)
	candidateRealName := normalizedName(candidate.RealName)
	candidateDisplayName := normalizedName(candidate.DisplayName)
	for _, profile := range existing {
		if !ignoreProfileID.IsZero() && profile.ID == ignoreProfileID {
			continue
		}
		placement := profile.Placement.NormalizedStatus(profile.OrgIdentity)
		if candidateID != "" && strings.EqualFold(profile.ID.String(), candidateID) {
			conflicts = append(conflicts, talentMarketImportConflict(candidateIndex, "agent_profile_id", profile, candidate))
			continue
		}
		if candidateRealName != "" && normalizedName(profile.RealName) == candidateRealName {
			conflicts = append(conflicts, talentMarketImportConflict(candidateIndex, "real_name", profile, candidate))
			continue
		}
		if candidateRealName != "" && normalizedName(profile.DisplayName) == candidateRealName {
			conflicts = append(conflicts, talentMarketImportConflict(candidateIndex, "real_name", profile, candidate))
			continue
		}
		if candidateDisplayName != "" && normalizedName(profile.RealName) == candidateDisplayName {
			conflicts = append(conflicts, talentMarketImportConflict(candidateIndex, "display_name", profile, candidate))
			continue
		}
		if candidateDisplayName != "" && normalizedName(profile.DisplayName) == candidateDisplayName {
			conflicts = append(conflicts, talentMarketImportConflict(candidateIndex, "display_name", profile, candidate))
			continue
		}
		_ = placement
	}
	return conflicts
}

func talentMarketImportConflict(candidateIndex int, conflictType string, profile agentcore.AgentProfile, candidate TalentMarketImportCandidateDTO) TalentMarketImportConflictDTO {
	return TalentMarketImportConflictDTO{
		CandidateIndex:          candidateIndex,
		ConflictType:            conflictType,
		ExistingAgentProfileID:  profile.ID.String(),
		ExistingRealName:        profile.RealName,
		ExistingDisplayName:     profile.DisplayName,
		ExistingPlacementStatus: string(profile.Placement.NormalizedStatus(profile.OrgIdentity)),
		CandidateRealName:       candidate.RealName,
		CandidateDisplayName:    candidate.DisplayName,
		CandidateSlug:           candidate.SourceSlug,
	}
}

func previewConflictsForCandidate(conflicts []TalentMarketImportConflictDTO, candidateIndex int) []TalentMarketImportConflictDTO {
	items := make([]TalentMarketImportConflictDTO, 0)
	for _, conflict := range conflicts {
		if conflict.CandidateIndex == candidateIndex {
			items = append(items, conflict)
		}
	}
	return items
}

func firstOverwriteTarget(conflicts []TalentMarketImportConflictDTO) (TalentMarketImportConflictDTO, bool) {
	for _, conflict := range conflicts {
		if conflict.ConflictType == "real_name" || conflict.ConflictType == "agent_profile_id" || conflict.ConflictType == "display_name" {
			return conflict, true
		}
	}
	return TalentMarketImportConflictDTO{}, false
}

func hasNameConflict(candidate TalentMarketImportCandidateDTO, existing []agentcore.AgentProfile, ignoreProfileID agentcore.AgentProfileID) bool {
	for _, conflict := range conflictsForTalentMarketCandidate(candidate, existing, 0, ignoreProfileID) {
		if conflict.ConflictType == "real_name" || conflict.ConflictType == "display_name" {
			return true
		}
	}
	return false
}

func replaceTalentMarketImportExisting(existing []agentcore.AgentProfile, profile agentcore.AgentProfile) []agentcore.AgentProfile {
	for i, item := range existing {
		if item.ID == profile.ID {
			existing[i] = profile
			return existing
		}
	}
	return append(existing, profile)
}

func (s *CommandService) profileFromTalentMarketCandidate(ctx context.Context, candidate TalentMarketImportCandidateDTO, profileID agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	profile := agentcore.AgentProfile{
		ID:        profileID,
		SkillRefs: []agentcore.SkillRef{},
		ToolRefs:  []agentcore.ToolRef{},
	}
	if existing, err := s.profiles.GetProfile(ctx, profileID); err == nil && !existing.ID.IsZero() {
		profile = existing
	}
	profile.ID = profileID
	profile.RealName = strings.TrimSpace(candidate.RealName)
	profile.DisplayName = strings.TrimSpace(firstNonEmpty(candidate.DisplayName, candidate.RealName))
	profile.RoleName = strings.TrimSpace(firstNonEmpty(candidate.RoleName, candidate.Category, candidate.Description))
	profile.AgentKind = normalizeAgentKind(candidate.AgentKind)
	profile.Placement = agentcore.TalentPlacement{Status: agentcore.TalentPlacementMarket, ReleasedReason: "imported to talent market"}
	profile.OrgIdentity = agentcore.AgentOrgIdentity{
		EmploymentType:    normalizeEmploymentType("", string(profile.AgentKind)),
		SupervisionPolicy: agentcore.SupervisionPolicyRoot,
	}
	profile.RuntimeSpec = agentcore.RuntimeSpec{
		Kind:      normalizeRuntimeKind(candidate.RuntimeKind, profile.AgentKind),
		ConfigRef: strings.TrimSpace(candidate.RuntimeConfigRef),
		Config:    cloneRawMessage(candidate.RuntimeConfig),
	}
	profile.Persona = agentcore.AgentPersona{
		Backstory:              strings.TrimSpace(firstNonEmpty(candidate.Persona.Backstory, candidate.SourcePrompt, candidate.Description)),
		ResponsibilityBoundary: strings.TrimSpace(firstNonEmpty(candidate.Persona.ResponsibilityBoundary, candidate.Description, "负责人才市场导入 Agent 的任务执行，并按 BaseAgent 审计要求提交过程、结果和证据。")),
		BehaviorBoundary:       strings.TrimSpace(candidate.Persona.BehaviorBoundary),
	}
	ensureManagedAgentChineseIdentity(&profile)
	profile.BaseAgent = defaultManagedBaseAgentSpec(profile)
	if strings.TrimSpace(candidate.SourcePrompt) != "" {
		profile.BaseAgent.SystemPrompt = strings.TrimSpace(candidate.SourcePrompt)
	}
	applyTalentMarketImportMetadata(&profile, candidate)
	profile.SkillRefs = skillRefsFromDTO(candidate.SkillRefs)
	profile.ToolRefs = toolRefsFromDTO(candidate.ToolRefs)
	if candidate.CollectiveMemoryRefs != nil {
		profile.CollectiveMemoryScopeRefs = memoryRefsFromDTO(candidate.CollectiveMemoryRefs)
	}
	profile.PersonalMemoryProfileRef = memoryRefFromImportCandidate(candidate.PersonalMemoryRef, profile.ID)
	profile.QAEnabled = candidate.QAEnabled
	profile.Enabled = candidate.Enabled
	if profile.SkillRefs == nil {
		profile.SkillRefs = []agentcore.SkillRef{}
	}
	if profile.ToolRefs == nil {
		profile.ToolRefs = []agentcore.ToolRef{}
	}
	ensureDefaultAgentReadCapabilities(&profile)
	if profile.CollectiveMemoryScopeRefs == nil {
		profile.CollectiveMemoryScopeRefs = []agentcore.MemoryProfileRef{}
	}
	if errs := agentcore.ValidateAgentProfile(profile); len(errs) > 0 {
		return agentcore.AgentProfile{}, fmt.Errorf("%w: agent profile invalid: %v", ErrInvalidRequest, errs)
	}
	return profile, nil
}

func applyTalentMarketImportMetadata(profile *agentcore.AgentProfile, candidate TalentMarketImportCandidateDTO) {
	if profile == nil {
		return
	}
	metadata := talentMarketImportMetadata{
		Description:  strings.TrimSpace(candidate.Description),
		Category:     strings.TrimSpace(candidate.Category),
		SourceSlug:   strings.TrimSpace(candidate.SourceSlug),
		SourcePath:   strings.TrimSpace(candidate.SourcePath),
		SourceHash:   strings.TrimSpace(candidate.SourceHash),
		SourcePrompt: strings.TrimSpace(candidate.SourcePrompt),
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		return
	}
	if profile.BaseAgent.Extensions == nil {
		profile.BaseAgent.Extensions = map[string]json.RawMessage{}
	}
	profile.BaseAgent.Extensions[talentMarketImportExtensionKey] = payload
}

func memoryRefFromImportCandidate(ref WorkforceMemoryProfileRefDTO, profileID agentcore.AgentProfileID) agentcore.MemoryProfileRef {
	memoryID := strings.TrimSpace(ref.MemoryProfileID)
	if memoryID == "" {
		memoryID = "memory-" + profileID.String()
	}
	kind := agentcore.MemoryKind(strings.TrimSpace(ref.MemoryKind))
	if !kind.IsValid() || kind != agentcore.MemoryKindPersonal {
		kind = agentcore.MemoryKindPersonal
	}
	owner := strings.TrimSpace(ref.OwnerAgentID)
	if owner == "" {
		owner = profileID.String()
	}
	return agentcore.MemoryProfileRef{
		MemoryProfileID:   agentcore.MemoryProfileID(memoryID),
		OwnerAgentID:      agentcore.AgentID(owner),
		MemoryKind:        kind,
		AccessScope:       strings.TrimSpace(ref.AccessScope),
		RetrievalPolicy:   strings.TrimSpace(ref.RetrievalPolicy),
		CompressionPolicy: strings.TrimSpace(ref.CompressionPolicy),
	}
}

func candidateFromTalentMarketProfile(profile agentcore.AgentProfile) TalentMarketImportCandidateDTO {
	metadata := metadataFromTalentMarketProfile(profile)
	return TalentMarketImportCandidateDTO{
		AgentProfileID:       profile.ID.String(),
		RealName:             profile.RealName,
		DisplayName:          profile.DisplayName,
		RoleName:             profile.RoleName,
		AgentKind:            string(profile.AgentKind),
		Description:          metadata.Description,
		Category:             metadata.Category,
		SourceSlug:           metadata.SourceSlug,
		SourcePath:           metadata.SourcePath,
		SourceHash:           metadata.SourceHash,
		SourcePrompt:         firstNonEmpty(metadata.SourcePrompt, profile.Persona.Backstory, profile.BaseAgent.SystemPrompt),
		RuntimeKind:          string(profile.RuntimeSpec.Kind),
		RuntimeConfigRef:     profile.RuntimeSpec.ConfigRef,
		RuntimeConfig:        cloneRawMessage(profile.RuntimeSpec.Config),
		Persona:              WorkforcePersonaDTO{Backstory: profile.Persona.Backstory, ResponsibilityBoundary: profile.Persona.ResponsibilityBoundary, BehaviorBoundary: profile.Persona.BehaviorBoundary},
		PersonalMemoryRef:    mapMemoryRef(profile.PersonalMemoryProfileRef),
		CollectiveMemoryRefs: mapMemoryRefs(profile.CollectiveMemoryScopeRefs),
		SkillRefs:            mapSkillRefs(profile.SkillRefs),
		ToolRefs:             mapToolRefs(profile.ToolRefs),
		QAEnabled:            profile.QAEnabled,
		Enabled:              profile.Enabled,
	}
}

func metadataFromTalentMarketProfile(profile agentcore.AgentProfile) talentMarketImportMetadata {
	if profile.BaseAgent.Extensions == nil {
		return talentMarketImportMetadata{}
	}
	raw := profile.BaseAgent.Extensions[talentMarketImportExtensionKey]
	if len(raw) == 0 {
		return talentMarketImportMetadata{}
	}
	var metadata talentMarketImportMetadata
	_ = json.Unmarshal(raw, &metadata)
	return metadata
}

func normalizeTalentMarketImportCandidates(candidates []TalentMarketImportCandidateDTO) []TalentMarketImportCandidateDTO {
	normalized := make([]TalentMarketImportCandidateDTO, 0, len(candidates))
	for _, candidate := range candidates {
		candidate.AgentProfileID = strings.TrimSpace(candidate.AgentProfileID)
		candidate.RealName = strings.TrimSpace(candidate.RealName)
		candidate.DisplayName = strings.TrimSpace(candidate.DisplayName)
		candidate.RoleName = strings.TrimSpace(candidate.RoleName)
		candidate.AgentKind = strings.TrimSpace(candidate.AgentKind)
		candidate.Description = strings.TrimSpace(candidate.Description)
		candidate.Category = strings.TrimSpace(candidate.Category)
		candidate.SourceSlug = strings.TrimSpace(candidate.SourceSlug)
		candidate.SourcePath = strings.TrimSpace(candidate.SourcePath)
		candidate.SourceHash = strings.TrimSpace(candidate.SourceHash)
		candidate.SourcePrompt = strings.TrimSpace(candidate.SourcePrompt)
		candidate.RuntimeKind = strings.TrimSpace(candidate.RuntimeKind)
		candidate.RuntimeConfigRef = strings.TrimSpace(candidate.RuntimeConfigRef)
		candidate.ConflictResolution = strings.TrimSpace(candidate.ConflictResolution)
		candidate.ResolvedRealName = strings.TrimSpace(candidate.ResolvedRealName)
		if candidate.AgentProfileID == "" {
			candidate.AgentProfileID = profileIDFromImportCandidate(candidate)
		}
		if candidate.RealName == "" {
			candidate.RealName = firstNonEmpty(candidate.DisplayName, candidate.SourceSlug, "导入 Agent")
		}
		if candidate.DisplayName == "" {
			candidate.DisplayName = candidate.RealName
		}
		if candidate.AgentKind == "" {
			candidate.AgentKind = string(agentcore.AgentKindAI)
		}
		normalized = append(normalized, candidate)
	}
	return normalized
}

func profileIDFromImportCandidate(candidate TalentMarketImportCandidateDTO) string {
	stem := strings.TrimSpace(firstNonEmpty(candidate.SourceSlug, candidate.RealName, candidate.DisplayName))
	if stem == "" {
		return ""
	}
	stem = strings.ToLower(stem)
	stem = regexp.MustCompile(`[^a-z0-9\p{Han}]+`).ReplaceAllString(stem, "-")
	stem = strings.Trim(stem, "-")
	if stem == "" {
		return ""
	}
	return "profile-market-" + stem
}

func normalizedName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
