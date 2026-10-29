package resourceadmin

import (
	"context"
	"errors"

	agentcore "orrery_v2/internal/agent/core"
	skillres "orrery_v2/internal/resource/skill"
)

func (s *QueryService) ListSkillBundles(ctx context.Context, viewer ViewerRef) (SkillBundleListDTO, error) {
	if s == nil || s.skills == nil || s.skillBundles == nil {
		return SkillBundleListDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceRead); err != nil {
		return SkillBundleListDTO{}, err
	}
	bundles, err := s.skillBundles.List(ctx)
	if err != nil {
		return SkillBundleListDTO{}, mapSkillPackageStoreError(err)
	}
	out := SkillBundleListDTO{Bundles: make([]SkillBundleDTO, 0, len(bundles))}
	for _, bundle := range bundles {
		dto, err := mapSkillBundle(ctx, s.skills, s.skillStore, bundle)
		if err != nil {
			return SkillBundleListDTO{}, err
		}
		out.Bundles = append(out.Bundles, dto)
	}
	return out, nil
}

func mapSkillBundle(ctx context.Context, registry skillres.Registry, store *skillres.PackageStore, bundle skillres.SkillBundle) (SkillBundleDTO, error) {
	dto := SkillBundleDTO{
		BundleID:    bundle.ID,
		Name:        bundle.Name,
		Description: bundle.Description,
		CreatedBy:   bundle.CreatedBy,
		CreatedAt:   bundle.CreatedAt,
		UpdatedAt:   bundle.UpdatedAt,
		SkillIDs:    make([]string, 0, len(bundle.SkillIDs)),
		Skills:      make([]SkillDTO, 0, len(bundle.SkillIDs)),
	}
	for _, skillID := range bundle.SkillIDs {
		dto.SkillIDs = append(dto.SkillIDs, string(skillID))
		definition, err := registry.Get(ctx, agentcore.SkillID(skillID))
		if err != nil {
			if errors.Is(err, skillres.ErrNotFound) {
				continue
			}
			return SkillBundleDTO{}, mapSkillPackageStoreError(err)
		}
		skillDTO := mapSkill(definition)
		if store != nil {
			if tree, err := store.BuildFileTree(ctx, definition.ID); err == nil {
				skillDTO = mapSkillWithTree(definition, tree)
			} else if !errors.Is(err, skillres.ErrNotFound) {
				return SkillBundleDTO{}, mapSkillPackageStoreError(err)
			}
		}
		dto.Skills = append(dto.Skills, skillDTO)
	}
	return dto, nil
}
