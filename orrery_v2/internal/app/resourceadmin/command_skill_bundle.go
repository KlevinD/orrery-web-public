package resourceadmin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	skillres "orrery_v2/internal/resource/skill"
)

func (s *CommandService) CreateSkillBundle(ctx context.Context, viewer ViewerRef, req CreateSkillBundleRequest) (SkillBundleDTO, error) {
	if s == nil || s.skills == nil || s.skillBundles == nil {
		return SkillBundleDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return SkillBundleDTO{}, err
	}
	s.skillReferences.Lock()
	defer s.skillReferences.Unlock()
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return SkillBundleDTO{}, fmt.Errorf("%w: name is required", ErrInvalidRequest)
	}
	skillIDs := requestedSkillIDs(req.SkillIDs)
	if err := s.ensureSkillsExist(ctx, skillIDs); err != nil {
		return SkillBundleDTO{}, err
	}
	bundleID := ""
	if err := s.generateResourceID(&bundleID, "skill-bundle"); err != nil {
		return SkillBundleDTO{}, err
	}
	now := time.Now().UTC()
	bundle := skillres.SkillBundle{
		ID:          bundleID,
		Name:        name,
		Description: strings.TrimSpace(req.Description),
		SkillIDs:    skillIDs,
		CreatedBy:   viewer.ViewerID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.skillBundles.Create(ctx, bundle); err != nil {
		return SkillBundleDTO{}, mapSkillPackageStoreError(err)
	}
	if err := s.recordAuditWithMetadata(ctx, viewer, "resource.skill_bundle.create", bundle.ID, "created skill bundle", map[string]string{
		"skill_count": fmt.Sprint(len(bundle.SkillIDs)),
	}); err != nil {
		return SkillBundleDTO{}, errors.Join(err, s.removeSkillBundleForRollback(context.WithoutCancel(ctx), bundle.ID))
	}
	return mapSkillBundle(ctx, s.skills, s.skillStore, bundle)
}

func (s *CommandService) AddSkillsToBundle(ctx context.Context, viewer ViewerRef, bundleID string, req AddSkillsToBundleRequest) (SkillBundleDTO, error) {
	if s == nil || s.skills == nil || s.skillBundles == nil {
		return SkillBundleDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return SkillBundleDTO{}, err
	}
	s.skillReferences.Lock()
	defer s.skillReferences.Unlock()
	return s.addSkillsToBundleLocked(ctx, viewer, bundleID, req)
}

func (s *CommandService) addSkillsToBundleLocked(ctx context.Context, viewer ViewerRef, bundleID string, req AddSkillsToBundleRequest) (SkillBundleDTO, error) {
	bundle, err := s.skillBundles.Get(ctx, strings.TrimSpace(bundleID))
	if err != nil {
		return SkillBundleDTO{}, mapSkillPackageStoreError(err)
	}
	skillIDs := requestedSkillIDs(req.SkillIDs)
	if len(skillIDs) == 0 {
		return SkillBundleDTO{}, fmt.Errorf("%w: skill_ids is required", ErrInvalidRequest)
	}
	if err := s.ensureSkillsExist(ctx, skillIDs); err != nil {
		return SkillBundleDTO{}, err
	}
	original := bundle.Clone()
	bundle.SkillIDs = appendUniqueSkillIDs(bundle.SkillIDs, skillIDs...)
	bundle.UpdatedAt = time.Now().UTC()
	if err := s.skillBundles.Update(ctx, bundle); err != nil {
		return SkillBundleDTO{}, errors.Join(mapSkillPackageStoreError(err), s.restoreSkillBundle(context.WithoutCancel(ctx), original))
	}
	if err := s.recordAuditWithMetadata(ctx, viewer, "resource.skill_bundle.skills.add", bundle.ID, "added skills to bundle", map[string]string{
		"skill_count": fmt.Sprint(len(skillIDs)),
	}); err != nil {
		return SkillBundleDTO{}, errors.Join(err, s.restoreSkillBundle(context.WithoutCancel(ctx), original))
	}
	return mapSkillBundle(ctx, s.skills, s.skillStore, bundle)
}

func (s *CommandService) ImportSkillIntoBundle(ctx context.Context, viewer ViewerRef, bundleID string, reader io.ReaderAt, size int64) (SkillBundleDTO, error) {
	if s == nil || s.skillBundles == nil {
		return SkillBundleDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return SkillBundleDTO{}, err
	}
	if s.skills == nil || s.skillStore == nil {
		return SkillBundleDTO{}, ErrServiceDependency
	}
	s.skillReferences.Lock()
	defer s.skillReferences.Unlock()
	if _, err := s.skillBundles.Get(ctx, strings.TrimSpace(bundleID)); err != nil {
		return SkillBundleDTO{}, mapSkillPackageStoreError(err)
	}
	imported, err := s.importSkillPackageLocked(ctx, viewer, reader, size)
	if err != nil {
		return SkillBundleDTO{}, err
	}
	return s.addSkillsToBundleLocked(ctx, viewer, bundleID, AddSkillsToBundleRequest{SkillIDs: []string{imported.SkillID}})
}

func (s *CommandService) RemoveSkillFromBundle(ctx context.Context, viewer ViewerRef, bundleID, skillID string) (SkillBundleDTO, error) {
	if s == nil || s.skills == nil || s.skillBundles == nil {
		return SkillBundleDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return SkillBundleDTO{}, err
	}
	s.skillReferences.Lock()
	defer s.skillReferences.Unlock()
	bundle, err := s.skillBundles.Get(ctx, strings.TrimSpace(bundleID))
	if err != nil {
		return SkillBundleDTO{}, mapSkillPackageStoreError(err)
	}
	next, removed := removeBundleSkillID(bundle.SkillIDs, agentcore.SkillID(strings.TrimSpace(skillID)))
	if !removed {
		return SkillBundleDTO{}, fmt.Errorf("%w: skill %s is not in bundle %s", ErrNotFound, skillID, bundle.ID)
	}
	original := bundle.Clone()
	bundle.SkillIDs = next
	bundle.UpdatedAt = time.Now().UTC()
	if err := s.skillBundles.Update(ctx, bundle); err != nil {
		return SkillBundleDTO{}, errors.Join(mapSkillPackageStoreError(err), s.restoreSkillBundle(context.WithoutCancel(ctx), original))
	}
	if err := s.recordAuditWithMetadata(ctx, viewer, "resource.skill_bundle.skills.remove", bundle.ID, "removed skill from bundle", map[string]string{
		"skill_id": strings.TrimSpace(skillID),
	}); err != nil {
		return SkillBundleDTO{}, errors.Join(err, s.restoreSkillBundle(context.WithoutCancel(ctx), original))
	}
	return mapSkillBundle(ctx, s.skills, s.skillStore, bundle)
}

func (s *CommandService) DeleteSkillBundle(ctx context.Context, viewer ViewerRef, bundleID string, req DeleteSkillBundleRequest) error {
	if s == nil || s.skills == nil || s.skillBundles == nil {
		return ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return err
	}
	s.skillReferences.Lock()
	defer s.skillReferences.Unlock()
	bundle, err := s.skillBundles.Get(ctx, strings.TrimSpace(bundleID))
	if err != nil {
		return mapSkillPackageStoreError(err)
	}
	var definitions []skillres.SkillDefinition
	var stagedPackages *skillres.StagedPackageDeletion
	if req.DeleteSkills {
		if err := s.preflightSkillDeletion(ctx, bundle.SkillIDs, bundle.ID); err != nil {
			return err
		}
		definitions, err = s.skillDefinitions(ctx, bundle.SkillIDs)
		if err != nil {
			return err
		}
		if s.skillStore != nil {
			stagedPackages, err = s.skillStore.StagePackageDeletions(ctx, bundle.SkillIDs)
			if err != nil {
				return mapSkillPackageStoreError(err)
			}
		}
		for _, definition := range definitions {
			if err := s.skills.Delete(ctx, definition.ID); err != nil {
				return errors.Join(mapSkillPackageStoreError(err), s.rollbackCascadeDeletion(ctx, bundle, definitions, stagedPackages, false))
			}
		}
	}
	if err := s.skillBundles.Delete(ctx, bundle.ID); err != nil {
		if req.DeleteSkills {
			return errors.Join(mapSkillPackageStoreError(err), s.rollbackCascadeDeletion(ctx, bundle, definitions, stagedPackages, true))
		}
		return errors.Join(mapSkillPackageStoreError(err), s.restoreSkillBundle(context.WithoutCancel(ctx), bundle))
	}
	if err := s.recordAuditWithMetadata(ctx, viewer, "resource.skill_bundle.delete", bundle.ID, "deleted skill bundle", map[string]string{
		"delete_skills": fmt.Sprint(req.DeleteSkills),
		"skill_count":   fmt.Sprint(len(bundle.SkillIDs)),
	}); err != nil {
		rollbackErr := s.restoreSkillBundle(context.WithoutCancel(ctx), bundle)
		if req.DeleteSkills {
			rollbackErr = s.rollbackCascadeDeletion(ctx, bundle, definitions, stagedPackages, true)
		}
		return errors.Join(err, rollbackErr)
	}
	if stagedPackages != nil {
		if err := stagedPackages.Commit(context.WithoutCancel(ctx)); err != nil {
			return errors.Join(err, s.rollbackCascadeDeletion(ctx, bundle, definitions, stagedPackages, true))
		}
	}
	return nil
}

func (s *CommandService) rollbackCascadeDeletion(
	ctx context.Context,
	bundle skillres.SkillBundle,
	definitions []skillres.SkillDefinition,
	stagedPackages *skillres.StagedPackageDeletion,
	restoreBundle bool,
) error {
	rollbackCtx := context.WithoutCancel(ctx)
	var rollbackErr error
	if stagedPackages != nil {
		rollbackErr = errors.Join(rollbackErr, stagedPackages.Rollback(rollbackCtx))
	}
	rollbackErr = errors.Join(rollbackErr, s.restoreSkillDefinitions(rollbackCtx, definitions))
	if restoreBundle {
		rollbackErr = errors.Join(rollbackErr, s.restoreSkillBundle(rollbackCtx, bundle))
	}
	return rollbackErr
}

func (s *CommandService) skillDefinitions(ctx context.Context, skillIDs []agentcore.SkillID) ([]skillres.SkillDefinition, error) {
	definitions := make([]skillres.SkillDefinition, 0, len(skillIDs))
	for _, skillID := range skillIDs {
		definition, err := s.skills.Get(ctx, skillID)
		if err != nil {
			return nil, mapSkillPackageStoreError(err)
		}
		definitions = append(definitions, definition)
	}
	return definitions, nil
}

func (s *CommandService) restoreSkillDefinitions(ctx context.Context, definitions []skillres.SkillDefinition) error {
	var restoreErr error
	for _, definition := range definitions {
		if _, err := s.skills.Get(ctx, definition.ID); err == nil {
			continue
		} else if !errors.Is(err, skillres.ErrNotFound) {
			restoreErr = errors.Join(restoreErr, err)
			continue
		}
		if err := s.skills.Register(ctx, definition); err != nil {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("restore skill %s: %w", definition.ID, err))
		}
	}
	return restoreErr
}

func (s *CommandService) restoreSkillBundle(ctx context.Context, bundle skillres.SkillBundle) error {
	if _, err := s.skillBundles.Get(ctx, bundle.ID); err == nil {
		if err := s.skillBundles.Update(ctx, bundle); err != nil {
			return fmt.Errorf("restore skill bundle %s: %w", bundle.ID, err)
		}
		return nil
	} else if !errors.Is(err, skillres.ErrNotFound) {
		return err
	}
	if err := s.skillBundles.Create(ctx, bundle); err != nil {
		return fmt.Errorf("restore skill bundle %s: %w", bundle.ID, err)
	}
	return nil
}

func (s *CommandService) removeSkillBundleForRollback(ctx context.Context, bundleID string) error {
	if err := s.skillBundles.Delete(ctx, bundleID); err != nil {
		if _, getErr := s.skillBundles.Get(ctx, bundleID); errors.Is(getErr, skillres.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("rollback skill bundle creation %s: %w", bundleID, err)
	}
	return nil
}

func (s *CommandService) ensureSkillsExist(ctx context.Context, skillIDs []agentcore.SkillID) error {
	for _, skillID := range skillIDs {
		if _, err := s.skills.Get(ctx, skillID); err != nil {
			return mapSkillPackageStoreError(err)
		}
	}
	return nil
}

func (s *CommandService) preflightSkillDeletion(ctx context.Context, skillIDs []agentcore.SkillID, ignoredBundleID string) error {
	if err := s.ensureSkillsExist(ctx, skillIDs); err != nil {
		return err
	}
	if len(skillIDs) == 0 {
		return nil
	}
	if s.skillBundles == nil || s.profiles == nil {
		return ErrServiceDependency
	}
	wanted := skillIDSet(skillIDs)
	bundles, err := s.skillBundles.List(ctx)
	if err != nil {
		return mapSkillPackageStoreError(err)
	}
	for _, bundle := range bundles {
		if strings.EqualFold(bundle.ID, ignoredBundleID) {
			continue
		}
		for _, skillID := range bundle.SkillIDs {
			if _, ok := wanted[strings.ToLower(string(skillID))]; ok {
				return fmt.Errorf("%w: skill %s is also referenced by bundle %s", ErrConflict, skillID, bundle.Name)
			}
		}
	}

	page, err := s.profiles.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{
		Placement:     agentcore.TalentPlacementFilterAll,
		IncludeAI:     true,
		IncludeHumans: true,
	})
	if err != nil {
		return err
	}
	blockers := make([]string, 0)
	seenProfiles := map[agentcore.AgentProfileID]struct{}{}
	for _, entry := range page.Entries {
		refs := entry.SkillRefs
		if !entry.AgentProfileID.IsZero() {
			if _, seen := seenProfiles[entry.AgentProfileID]; seen {
				continue
			}
			seenProfiles[entry.AgentProfileID] = struct{}{}
			profile, err := s.profiles.GetProfile(ctx, entry.AgentProfileID)
			if err != nil {
				return err
			}
			refs = profile.SkillRefs
		}
		for _, ref := range refs {
			if _, ok := wanted[strings.ToLower(string(ref.SkillID))]; !ok {
				continue
			}
			name := strings.TrimSpace(entry.DisplayName)
			if name == "" {
				name = resourceAgentID(entry)
			}
			blockers = append(blockers, fmt.Sprintf("%s (%s)", name, ref.SkillID))
		}
	}
	if len(blockers) > 0 {
		sort.Strings(blockers)
		return fmt.Errorf("%w: skills are bound to Agents: %s", ErrConflict, strings.Join(blockers, ", "))
	}
	return nil
}

func (s *CommandService) deleteSkillUnchecked(ctx context.Context, viewer ViewerRef, skillID agentcore.SkillID) error {
	definition, err := s.skills.Get(ctx, skillID)
	if err != nil {
		return mapSkillPackageStoreError(err)
	}
	var stagedPackage *skillres.StagedPackageDeletion
	if s.skillStore != nil {
		stagedPackage, err = s.skillStore.StagePackageDeletions(ctx, []agentcore.SkillID{skillID})
		if err != nil {
			return mapSkillPackageStoreError(err)
		}
	}
	if err := s.skills.Delete(ctx, skillID); err != nil {
		return errors.Join(mapSkillPackageStoreError(err), s.rollbackSkillDeletion(ctx, definition, stagedPackage))
	}
	if err := s.recordAudit(ctx, viewer, "resource.skill.delete", string(skillID), "deleted skill"); err != nil {
		return errors.Join(err, s.rollbackSkillDeletion(ctx, definition, stagedPackage))
	}
	if stagedPackage != nil {
		if err := stagedPackage.Commit(context.WithoutCancel(ctx)); err != nil {
			return errors.Join(err, s.rollbackSkillDeletion(ctx, definition, stagedPackage))
		}
	}
	return nil
}

func (s *CommandService) rollbackSkillDeletion(ctx context.Context, definition skillres.SkillDefinition, stagedPackage *skillres.StagedPackageDeletion) error {
	rollbackCtx := context.WithoutCancel(ctx)
	var rollbackErr error
	if stagedPackage != nil {
		rollbackErr = errors.Join(rollbackErr, stagedPackage.Rollback(rollbackCtx))
	}
	return errors.Join(rollbackErr, s.restoreSkillDefinitions(rollbackCtx, []skillres.SkillDefinition{definition}))
}

func requestedSkillIDs(values []string) []agentcore.SkillID {
	ids := make([]agentcore.SkillID, 0, len(values))
	for _, value := range values {
		ids = append(ids, agentcore.SkillID(value))
	}
	return appendUniqueSkillIDs(nil, ids...)
}

func appendUniqueSkillIDs(existing []agentcore.SkillID, values ...agentcore.SkillID) []agentcore.SkillID {
	out := append([]agentcore.SkillID(nil), existing...)
	seen := skillIDSet(out)
	for _, value := range values {
		id := strings.TrimSpace(string(value))
		if id == "" {
			continue
		}
		key := strings.ToLower(id)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, agentcore.SkillID(id))
	}
	return out
}

func removeBundleSkillID(values []agentcore.SkillID, target agentcore.SkillID) ([]agentcore.SkillID, bool) {
	out := make([]agentcore.SkillID, 0, len(values))
	removed := false
	for _, value := range values {
		if strings.EqualFold(string(value), string(target)) {
			removed = true
			continue
		}
		out = append(out, value)
	}
	return out, removed
}

func skillIDSet(values []agentcore.SkillID) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		if id := strings.TrimSpace(string(value)); id != "" {
			out[strings.ToLower(id)] = struct{}{}
		}
	}
	return out
}
