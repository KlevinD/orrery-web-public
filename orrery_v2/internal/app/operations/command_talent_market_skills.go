package operations

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	skillres "orrery_v2/internal/resource/skill"
)

func (s *CommandService) ensureImportedAgentSkills(ctx context.Context, candidate TalentMarketImportCandidateDTO, packagedSkills map[agentcore.SkillID]skillres.SkillDefinition) error {
	for _, ref := range candidate.SkillRefs {
		skillID := agentcore.SkillID(strings.TrimSpace(ref.SkillID))
		if skillID == "" {
			continue
		}
		if definition, ok := packagedSkills[skillID]; ok {
			definition.ID = skillID
			if err := s.registerOrUpdateTalentMarketSkill(ctx, definition); err != nil {
				return err
			}
			continue
		}
		if !isImportedAgentSkillRef(ref) {
			continue
		}
		definition := importedAgentSkillDefinition(candidate, ref)
		if err := s.registerOrUpdateTalentMarketSkill(ctx, definition); err != nil {
			return err
		}
	}
	return nil
}

func (s *CommandService) registerOrUpdateTalentMarketSkill(ctx context.Context, definition skillres.SkillDefinition) error {
	definition = normalizeTalentMarketSkillDefinition(definition)
	if definition.ID == "" {
		return fmt.Errorf("%w: skill_id is required", ErrInvalidRequest)
	}
	if s == nil || s.skills == nil {
		return fmt.Errorf("%w: skill registry is required for imported agent skill %s", ErrServiceDependency, definition.ID)
	}
	if s.skillStore != nil {
		packaged, err := s.skillStore.UpsertDefinitionPackage(ctx, definition)
		if err != nil {
			return err
		}
		definition = normalizeTalentMarketSkillDefinition(packaged)
	}
	if err := s.skills.Register(ctx, definition); err != nil {
		if errors.Is(err, skillres.ErrAlreadyExists) {
			return skillres.ApplyLegacyManagedDefinition(ctx, s.skills, definition, skillres.ManagedUpdateSourceTalentMarket)
		}
		return err
	}
	return nil
}

func (s *CommandService) EnsureImportedAgentSkills(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("%w: imported agent skill backfill is disabled; import package-native skill definitions instead", ErrInvalidRequest)
}

func isImportedAgentSkillRef(ref WorkforceSkillRefDTO) bool {
	id := strings.TrimSpace(ref.SkillID)
	return strings.HasPrefix(id, "imported.") || strings.EqualFold(strings.TrimSpace(ref.Level), "reference")
}

func importedAgentSkillDefinition(candidate TalentMarketImportCandidateDTO, ref WorkforceSkillRefDTO) skillres.SkillDefinition {
	name := strings.TrimSpace(firstNonEmpty(ref.Alias, candidate.RealName, candidate.DisplayName, candidate.SourceSlug, ref.SkillID))
	description := strings.TrimSpace(firstNonEmpty(candidate.Description, candidate.RoleName, candidate.Persona.ResponsibilityBoundary, name))
	prompt := strings.TrimSpace(firstNonEmpty(candidate.SourcePrompt, candidate.Persona.Backstory, description))
	return skillres.SkillDefinition{
		ID:          agentcore.SkillID(strings.TrimSpace(ref.SkillID)),
		Name:        name,
		Version:     "1.0.0",
		Description: description,
		DomainTags:  importedAgentSkillTags(candidate.Category, "talent-market", "imported-agent"),
		PromptAssets: []skillres.PromptAsset{{
			Path:    "SKILL.md",
			Kind:    skillres.PromptAssetMarkdown,
			Content: prompt,
		}},
		Enabled:   true,
		SourceRef: strings.TrimSpace(firstNonEmpty(candidate.SourcePath, candidate.SourceSlug)),
	}
}

func importedAgentSkillTags(values ...string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func (s *CommandService) collectTalentMarketExportSkills(ctx context.Context, agents []TalentMarketImportCandidateDTO) ([]TalentMarketSkillDefinitionDTO, error) {
	byID := map[agentcore.SkillID]skillres.SkillDefinition{}
	for _, agent := range agents {
		for _, ref := range agent.SkillRefs {
			skillID := agentcore.SkillID(strings.TrimSpace(ref.SkillID))
			if skillID == "" {
				continue
			}
			if _, exists := byID[skillID]; exists {
				continue
			}
			if s != nil && s.skills != nil {
				definition, err := s.skills.Get(ctx, skillID)
				if err == nil {
					byID[skillID] = normalizeTalentMarketSkillDefinition(definition)
					continue
				}
				if !errors.Is(err, skillres.ErrNotFound) {
					return nil, err
				}
			}
			if isImportedAgentSkillRef(ref) {
				byID[skillID] = importedAgentSkillDefinition(agent, ref)
			}
		}
	}
	skills := make([]TalentMarketSkillDefinitionDTO, 0, len(byID))
	for _, definition := range byID {
		skills = append(skills, talentMarketSkillDTOFromDefinition(definition))
	}
	sort.Slice(skills, func(i, j int) bool {
		return strings.Compare(skills[i].SkillID, skills[j].SkillID) < 0
	})
	return skills, nil
}

func normalizeTalentMarketSkillDTOs(skills []TalentMarketSkillDefinitionDTO) []TalentMarketSkillDefinitionDTO {
	byID := map[string]TalentMarketSkillDefinitionDTO{}
	for _, skill := range skills {
		skill = normalizeTalentMarketSkillDTO(skill)
		if skill.SkillID == "" {
			continue
		}
		if _, exists := byID[skill.SkillID]; exists {
			continue
		}
		byID[skill.SkillID] = skill
	}
	normalized := make([]TalentMarketSkillDefinitionDTO, 0, len(byID))
	for _, skill := range byID {
		normalized = append(normalized, skill)
	}
	sort.Slice(normalized, func(i, j int) bool {
		return strings.Compare(normalized[i].SkillID, normalized[j].SkillID) < 0
	})
	return normalized
}

func normalizeTalentMarketSkillDTO(skill TalentMarketSkillDefinitionDTO) TalentMarketSkillDefinitionDTO {
	skill.SkillID = strings.TrimSpace(skill.SkillID)
	skill.Name = strings.TrimSpace(firstNonEmpty(skill.Name, skill.SkillID))
	skill.Version = strings.TrimSpace(firstNonEmpty(skill.Version, "1.0.0"))
	skill.Description = strings.TrimSpace(skill.Description)
	skill.SourceRef = strings.TrimSpace(skill.SourceRef)
	skill.DomainTags = normalizeTalentMarketStringSlice(skill.DomainTags)
	skill.TaskTypeTags = normalizeTalentMarketStringSlice(skill.TaskTypeTags)
	for i := range skill.PromptAssets {
		skill.PromptAssets[i].Path = strings.TrimSpace(skill.PromptAssets[i].Path)
		skill.PromptAssets[i].ContentRef = strings.TrimSpace(skill.PromptAssets[i].ContentRef)
		if skill.PromptAssets[i].Kind == "" {
			skill.PromptAssets[i].Kind = skillres.PromptAssetMarkdown
		}
	}
	for i := range skill.RequiredTools {
		skill.RequiredTools[i].ToolID = strings.TrimSpace(skill.RequiredTools[i].ToolID)
		skill.RequiredTools[i].VersionConstraint = strings.TrimSpace(skill.RequiredTools[i].VersionConstraint)
		skill.RequiredTools[i].Alias = strings.TrimSpace(skill.RequiredTools[i].Alias)
	}
	return skill
}

func normalizeTalentMarketSkillDefinition(definition skillres.SkillDefinition) skillres.SkillDefinition {
	definition = definition.Clone()
	definition.ID = agentcore.SkillID(strings.TrimSpace(string(definition.ID)))
	definition.Name = strings.TrimSpace(firstNonEmpty(definition.Name, string(definition.ID)))
	definition.Version = strings.TrimSpace(firstNonEmpty(definition.Version, "1.0.0"))
	definition.Description = strings.TrimSpace(definition.Description)
	definition.SourceRef = strings.TrimSpace(definition.SourceRef)
	definition.DomainTags = normalizeTalentMarketStringSlice(definition.DomainTags)
	definition.TaskTypeTags = normalizeTalentMarketStringSlice(definition.TaskTypeTags)
	for i := range definition.PromptAssets {
		definition.PromptAssets[i].Path = strings.TrimSpace(definition.PromptAssets[i].Path)
		definition.PromptAssets[i].ContentRef = strings.TrimSpace(definition.PromptAssets[i].ContentRef)
		if definition.PromptAssets[i].Kind == "" {
			definition.PromptAssets[i].Kind = skillres.PromptAssetMarkdown
		}
	}
	return definition
}

func talentMarketSkillDTOFromDefinition(definition skillres.SkillDefinition) TalentMarketSkillDefinitionDTO {
	definition = normalizeTalentMarketSkillDefinition(definition)
	dto := TalentMarketSkillDefinitionDTO{
		SkillID:      string(definition.ID),
		Name:         definition.Name,
		Version:      definition.Version,
		Description:  definition.Description,
		DomainTags:   append([]string(nil), definition.DomainTags...),
		TaskTypeTags: append([]string(nil), definition.TaskTypeTags...),
		PromptAssets: append([]skillres.PromptAsset(nil), definition.PromptAssets...),
		Enabled:      definition.Enabled,
		SourceRef:    definition.SourceRef,
	}
	if !definition.CreatedAt.IsZero() {
		dto.CreatedAt = definition.CreatedAt.Format(time.RFC3339)
	}
	if !definition.UpdatedAt.IsZero() {
		dto.UpdatedAt = definition.UpdatedAt.Format(time.RFC3339)
	}
	for _, ref := range definition.RequiredTools {
		dto.RequiredTools = append(dto.RequiredTools, WorkforceToolRefDTO{
			ToolID:            string(ref.ToolID),
			VersionConstraint: ref.VersionConstraint,
			Alias:             ref.Alias,
			PermissionScope:   ref.PermissionScope,
			BindingPolicy:     ref.BindingPolicy,
		})
	}
	for _, ref := range definition.EvidenceRefs {
		dto.EvidenceRefs = append(dto.EvidenceRefs, ref.ID)
	}
	return normalizeTalentMarketSkillDTO(dto)
}

func talentMarketSkillDefinitionFromDTO(dto TalentMarketSkillDefinitionDTO) skillres.SkillDefinition {
	dto = normalizeTalentMarketSkillDTO(dto)
	definition := skillres.SkillDefinition{
		ID:           agentcore.SkillID(dto.SkillID),
		Name:         dto.Name,
		Version:      dto.Version,
		Description:  dto.Description,
		DomainTags:   append([]string(nil), dto.DomainTags...),
		TaskTypeTags: append([]string(nil), dto.TaskTypeTags...),
		PromptAssets: append([]skillres.PromptAsset(nil), dto.PromptAssets...),
		Enabled:      dto.Enabled,
		SourceRef:    dto.SourceRef,
	}
	for _, ref := range dto.RequiredTools {
		if strings.TrimSpace(ref.ToolID) == "" {
			continue
		}
		definition.RequiredTools = append(definition.RequiredTools, agentcore.ToolRef{
			ToolID:            agentcore.ToolID(ref.ToolID),
			VersionConstraint: ref.VersionConstraint,
			Alias:             ref.Alias,
			PermissionScope:   ref.PermissionScope,
			BindingPolicy:     ref.BindingPolicy,
		})
	}
	for _, ref := range dto.EvidenceRefs {
		ref = strings.TrimSpace(ref)
		if ref != "" {
			definition.EvidenceRefs = append(definition.EvidenceRefs, agentcore.EvidenceRef{ID: ref})
		}
	}
	return normalizeTalentMarketSkillDefinition(definition)
}

func normalizeTalentMarketStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func talentMarketSkillDefinitionsByID(skills []TalentMarketSkillDefinitionDTO) map[agentcore.SkillID]skillres.SkillDefinition {
	byID := map[agentcore.SkillID]skillres.SkillDefinition{}
	for _, skill := range normalizeTalentMarketSkillDTOs(skills) {
		definition := talentMarketSkillDefinitionFromDTO(skill)
		byID[definition.ID] = definition
	}
	return byID
}
