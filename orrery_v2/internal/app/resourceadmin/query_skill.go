package resourceadmin

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	agentcore "orrery_v2/internal/agent/core"
	skillres "orrery_v2/internal/resource/skill"
)

func (s *QueryService) GetSkillDetail(ctx context.Context, viewer ViewerRef, skillID string) (SkillDetailDTO, error) {
	if s == nil || s.skills == nil || s.skillStore == nil {
		return SkillDetailDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceRead); err != nil {
		return SkillDetailDTO{}, err
	}
	definition, err := s.skills.Get(ctx, agentcore.SkillID(skillID))
	if err != nil {
		if errors.Is(err, skillres.ErrNotFound) {
			return SkillDetailDTO{}, ErrNotFound
		}
		return SkillDetailDTO{}, err
	}
	tree, err := s.skillStore.BuildFileTree(ctx, definition.ID)
	if err != nil {
		return SkillDetailDTO{}, mapSkillPackageStoreError(err)
	}
	skillDir, err := s.skillStore.SkillDirectory(definition.ID)
	if err != nil {
		return SkillDetailDTO{}, mapSkillPackageStoreError(err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	raw, err := os.ReadFile(skillPath)
	if err != nil {
		return SkillDetailDTO{}, err
	}
	info, _ := os.Stat(skillPath)
	detail := SkillDetailDTO{
		Skill: mapSkillWithTree(definition, tree),
		SkillMD: SkillMarkdownDTO{
			Path: "SKILL.md",
			Raw:  string(raw),
		},
		Roots: tree.Roots,
	}
	if info != nil {
		detail.SkillMD.UpdatedAt = info.ModTime().UTC()
	}
	return detail, nil
}
