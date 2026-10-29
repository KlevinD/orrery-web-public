package resourceadmin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
	agentcore "orrery_v2/internal/agent/core"
	skillres "orrery_v2/internal/resource/skill"
)

func (s *CommandService) ImportSkillPackage(ctx context.Context, viewer ViewerRef, reader io.ReaderAt, size int64) (SkillDTO, error) {
	if s == nil || s.skills == nil || s.skillStore == nil {
		return SkillDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return SkillDTO{}, err
	}
	s.skillReferences.Lock()
	defer s.skillReferences.Unlock()
	return s.importSkillPackageLocked(ctx, viewer, reader, size)
}

func (s *CommandService) importSkillPackageLocked(ctx context.Context, viewer ViewerRef, reader io.ReaderAt, size int64) (SkillDTO, error) {
	definition, err := s.skillStore.ImportPackage(ctx, reader, size)
	if err != nil {
		return SkillDTO{}, mapSkillPackageStoreError(err)
	}
	if err := registerOrUpdateSkill(ctx, s.skills, definition); err != nil {
		return SkillDTO{}, err
	}
	if err := s.recordAudit(ctx, viewer, "resource.skill.import", string(definition.ID), "imported skill package"); err != nil {
		return SkillDTO{}, err
	}
	return s.mapSkillWithStore(ctx, definition)
}

func (s *CommandService) CreateSkill(ctx context.Context, viewer ViewerRef, req CreateSkillRequest) (SkillDetailDTO, error) {
	if s == nil || s.skills == nil || s.skillStore == nil {
		return SkillDetailDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return SkillDetailDTO{}, err
	}
	raw, err := buildSkillMarkdownFromCreateRequest(req)
	if err != nil {
		return SkillDetailDTO{}, err
	}
	definition, err := s.skillStore.CreatePackage(ctx, raw)
	if err != nil {
		return SkillDetailDTO{}, mapSkillPackageStoreError(err)
	}
	if err := registerOrUpdateSkill(ctx, s.skills, definition); err != nil {
		return SkillDetailDTO{}, err
	}
	if err := s.recordAudit(ctx, viewer, "resource.skill.create", string(definition.ID), "created skill package"); err != nil {
		return SkillDetailDTO{}, err
	}
	query := &QueryService{skills: s.skills, skillStore: s.skillStore}
	return query.GetSkillDetail(ctx, ViewerRef{ViewerID: viewer.ViewerID, PermissionScope: []string{PermissionResourceRead}}, string(definition.ID))
}

func (s *CommandService) DisableSkill(ctx context.Context, viewer ViewerRef, skillID string) error {
	if s == nil || s.skills == nil {
		return ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return err
	}
	if err := s.skills.Disable(ctx, agentcore.SkillID(skillID)); err != nil {
		return mapSkillPackageStoreError(err)
	}
	return s.recordAudit(ctx, viewer, "resource.skill.disable", skillID, "disabled skill")
}

func (s *CommandService) DeleteSkill(ctx context.Context, viewer ViewerRef, skillID string) error {
	if s == nil || s.skills == nil {
		return ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return err
	}
	s.skillReferences.Lock()
	defer s.skillReferences.Unlock()
	if _, err := s.skills.Get(ctx, agentcore.SkillID(skillID)); err != nil {
		return mapSkillPackageStoreError(err)
	}
	if err := s.preflightSkillDeletion(ctx, []agentcore.SkillID{agentcore.SkillID(skillID)}, ""); err != nil {
		return err
	}
	return s.deleteSkillUnchecked(ctx, viewer, agentcore.SkillID(skillID))
}

func (s *CommandService) ExportSkillPackage(ctx context.Context, viewer ViewerRef, skillID string) (SkillPackageDTO, error) {
	if s == nil || s.skills == nil || s.skillStore == nil {
		return SkillPackageDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceRead); err != nil {
		return SkillPackageDTO{}, err
	}
	if _, err := s.getPackageSkillDefinition(ctx, skillID); err != nil {
		return SkillPackageDTO{}, err
	}
	var buf bytes.Buffer
	if err := s.skillStore.ExportPackage(ctx, agentcore.SkillID(skillID), &buf); err != nil {
		return SkillPackageDTO{}, mapSkillPackageStoreError(err)
	}
	if err := s.recordAudit(ctx, viewer, "resource.skill.export", skillID, "exported skill package"); err != nil {
		return SkillPackageDTO{}, err
	}
	return SkillPackageDTO{
		FileName:    skillID + ".skill.zip",
		ContentType: "application/zip",
		Bytes:       buf.Bytes(),
	}, nil
}

func (s *CommandService) SaveSkillMarkdown(ctx context.Context, viewer ViewerRef, skillID, raw string) (SkillDetailDTO, error) {
	if s == nil || s.skills == nil || s.skillStore == nil {
		return SkillDetailDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return SkillDetailDTO{}, err
	}
	oldDefinition, err := s.getPackageSkillDefinition(ctx, skillID)
	if err != nil {
		return SkillDetailDTO{}, err
	}
	oldID := agentcore.SkillID(skillID)
	definition, err := s.skillStore.WriteSkillMarkdown(ctx, oldID, raw)
	if err != nil {
		return SkillDetailDTO{}, mapSkillPackageStoreError(err)
	}
	definition.Enabled = oldDefinition.Enabled
	definition.CreatedAt = oldDefinition.CreatedAt
	if definition.ID != oldID {
		_ = s.skills.Delete(ctx, oldID)
	}
	if err := registerOrUpdateSkill(ctx, s.skills, definition); err != nil {
		return SkillDetailDTO{}, err
	}
	if err := s.recordAudit(ctx, viewer, "resource.skill.update_skill_md", string(definition.ID), "updated skill markdown"); err != nil {
		return SkillDetailDTO{}, err
	}
	query := &QueryService{skills: s.skills, skillStore: s.skillStore}
	return query.GetSkillDetail(ctx, ViewerRef{ViewerID: viewer.ViewerID, PermissionScope: []string{PermissionResourceRead}}, string(definition.ID))
}

func (s *CommandService) GetSkillFile(ctx context.Context, viewer ViewerRef, req SkillFileRequest) (SkillFileDTO, error) {
	if s == nil || s.skills == nil || s.skillStore == nil {
		return SkillFileDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceRead); err != nil {
		return SkillFileDTO{}, err
	}
	if _, err := s.getPackageSkillDefinition(ctx, req.SkillID); err != nil {
		return SkillFileDTO{}, err
	}
	node, raw, err := s.skillStore.ReadFile(ctx, agentcore.SkillID(req.SkillID), req.Path)
	if err != nil {
		return SkillFileDTO{}, mapSkillPackageStoreError(err)
	}
	return mapSkillFile(req.SkillID, node, raw), nil
}

func (s *CommandService) SaveSkillFile(ctx context.Context, viewer ViewerRef, req SkillFileWriteRequest) (SkillFileDTO, error) {
	if s == nil || s.skills == nil || s.skillStore == nil {
		return SkillFileDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return SkillFileDTO{}, err
	}
	if _, err := s.getPackageSkillDefinition(ctx, req.SkillID); err != nil {
		return SkillFileDTO{}, err
	}
	node, raw, err := s.skillStore.WriteTextFile(ctx, agentcore.SkillID(req.SkillID), req.Path, req.Content)
	if err != nil {
		return SkillFileDTO{}, mapSkillPackageStoreError(err)
	}
	if _, err := s.refreshSkillDefinitionFromStore(ctx, req.SkillID); err != nil {
		return SkillFileDTO{}, err
	}
	if err := s.recordAudit(ctx, viewer, "resource.skill.file.update", req.SkillID, "updated skill file"); err != nil {
		return SkillFileDTO{}, err
	}
	return mapSkillFile(req.SkillID, node, raw), nil
}

func (s *CommandService) UploadSkillFile(ctx context.Context, viewer ViewerRef, req SkillFileUploadRequest) (SkillFileDTO, error) {
	if s == nil || s.skills == nil || s.skillStore == nil {
		return SkillFileDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return SkillFileDTO{}, err
	}
	if _, err := s.getPackageSkillDefinition(ctx, req.SkillID); err != nil {
		return SkillFileDTO{}, err
	}
	path := req.Path
	if path == "" {
		path = req.Name
	}
	node, raw, err := s.skillStore.WriteFile(ctx, agentcore.SkillID(req.SkillID), path, req.Content)
	if err != nil {
		return SkillFileDTO{}, mapSkillPackageStoreError(err)
	}
	if _, err := s.refreshSkillDefinitionFromStore(ctx, req.SkillID); err != nil {
		return SkillFileDTO{}, err
	}
	if err := s.recordAudit(ctx, viewer, "resource.skill.file.upload", req.SkillID, "uploaded skill file"); err != nil {
		return SkillFileDTO{}, err
	}
	return mapSkillFile(req.SkillID, node, raw), nil
}

func (s *CommandService) DownloadSkillFile(ctx context.Context, viewer ViewerRef, req SkillFileRequest) (SkillFileDownloadDTO, error) {
	if s == nil || s.skills == nil || s.skillStore == nil {
		return SkillFileDownloadDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceRead); err != nil {
		return SkillFileDownloadDTO{}, err
	}
	if _, err := s.getPackageSkillDefinition(ctx, req.SkillID); err != nil {
		return SkillFileDownloadDTO{}, err
	}
	node, raw, err := s.skillStore.DownloadFile(ctx, agentcore.SkillID(req.SkillID), req.Path)
	if err != nil {
		return SkillFileDownloadDTO{}, mapSkillPackageStoreError(err)
	}
	if err := s.recordAudit(ctx, viewer, "resource.skill.file.download", req.SkillID, "downloaded skill file"); err != nil {
		return SkillFileDownloadDTO{}, err
	}
	return SkillFileDownloadDTO{FileName: node.Name, ContentType: node.MediaType, Bytes: raw}, nil
}

func (s *CommandService) DeleteSkillFile(ctx context.Context, viewer ViewerRef, req SkillFileRequest) error {
	if s == nil || s.skills == nil || s.skillStore == nil {
		return ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return err
	}
	if _, err := s.getPackageSkillDefinition(ctx, req.SkillID); err != nil {
		return err
	}
	if err := s.skillStore.DeleteFile(ctx, agentcore.SkillID(req.SkillID), req.Path); err != nil {
		return mapSkillPackageStoreError(err)
	}
	if _, err := s.refreshSkillDefinitionFromStore(ctx, req.SkillID); err != nil {
		return err
	}
	return s.recordAudit(ctx, viewer, "resource.skill.file.delete", req.SkillID, "deleted skill file")
}

func (s *CommandService) CreateSkillFolder(ctx context.Context, viewer ViewerRef, req SkillFileRequest) error {
	if s == nil || s.skills == nil || s.skillStore == nil {
		return ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return err
	}
	if _, err := s.getPackageSkillDefinition(ctx, req.SkillID); err != nil {
		return err
	}
	if err := s.skillStore.CreateDirectory(ctx, agentcore.SkillID(req.SkillID), req.Path); err != nil {
		return mapSkillPackageStoreError(err)
	}
	return s.recordAudit(ctx, viewer, "resource.skill.folder.create", req.SkillID, "created skill folder")
}

func registerOrUpdateSkill(ctx context.Context, registry skillres.Registry, definition skillres.SkillDefinition) error {
	if _, err := registry.Get(ctx, definition.ID); err == nil {
		if err := skillres.ApplyLegacyManagedDefinition(ctx, registry, definition, skillres.ManagedUpdateSourceResourceAdmin); err != nil {
			return err
		}
		return nil
	} else if !errors.Is(err, skillres.ErrNotFound) {
		return err
	}
	return registry.Register(ctx, definition)
}

func (s *CommandService) mapSkillWithStore(ctx context.Context, definition skillres.SkillDefinition) (SkillDTO, error) {
	if s == nil || s.skillStore == nil {
		return mapSkill(definition), nil
	}
	tree, err := s.skillStore.BuildFileTree(ctx, definition.ID)
	if err != nil {
		return SkillDTO{}, mapSkillPackageStoreError(err)
	}
	return mapSkillWithTree(definition, tree), nil
}

func (s *CommandService) refreshSkillDefinitionFromStore(ctx context.Context, skillID string) (skillres.SkillDefinition, error) {
	if s == nil || s.skills == nil || s.skillStore == nil {
		return skillres.SkillDefinition{}, ErrServiceDependency
	}
	existing, err := s.skills.Get(ctx, agentcore.SkillID(skillID))
	if err != nil {
		return skillres.SkillDefinition{}, err
	}
	definition, err := s.skillStore.HydratePackage(ctx, agentcore.SkillID(skillID))
	if err != nil {
		return skillres.SkillDefinition{}, mapSkillPackageStoreError(err)
	}
	definition.Enabled = existing.Enabled
	definition.CreatedAt = existing.CreatedAt
	definition.UpdatedAt = existing.UpdatedAt
	if err := registerOrUpdateSkill(ctx, s.skills, definition); err != nil {
		return skillres.SkillDefinition{}, err
	}
	return definition, nil
}

func (s *CommandService) getPackageSkillDefinition(ctx context.Context, skillID string) (skillres.SkillDefinition, error) {
	definition, err := s.skills.Get(ctx, agentcore.SkillID(skillID))
	if err != nil {
		if errors.Is(err, skillres.ErrNotFound) {
			return skillres.SkillDefinition{}, ErrNotFound
		}
		return skillres.SkillDefinition{}, err
	}
	if _, err := s.skillStore.BuildFileTree(ctx, definition.ID); err != nil {
		return skillres.SkillDefinition{}, mapSkillPackageStoreError(err)
	}
	return definition, nil
}

func mapSkillPackageStoreError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, skillres.ErrConflict) {
		return fmt.Errorf("%w: %v", ErrConflict, err)
	}
	if errors.Is(err, skillres.ErrNotFound) {
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	if errors.Is(err, skillres.ErrHydration) || errors.Is(err, skillres.ErrInvalidDefinition) || errors.Is(err, skillres.ErrAlreadyExists) || errors.Is(err, skillres.ErrVersionMismatch) {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return err
}

func buildSkillMarkdownFromCreateRequest(req CreateSkillRequest) (string, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return "", fmt.Errorf("%w: name is required", ErrInvalidRequest)
	}
	version := strings.TrimSpace(req.Version)
	if version == "" {
		version = "1.0.0"
	}
	meta := struct {
		Name         string   `yaml:"name"`
		Description  string   `yaml:"description,omitempty"`
		Version      string   `yaml:"version"`
		TaskTypeTags []string `yaml:"task_type_tags,omitempty"`
	}{
		Name:         name,
		Description:  strings.TrimSpace(req.Description),
		Version:      version,
		TaskTypeTags: cleanStringList(req.TaskTypeTags),
	}
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	err := encoder.Encode(meta)
	if err != nil {
		return "", err
	}
	if err := encoder.Close(); err != nil {
		return "", err
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		prompt = "# " + name + "\n\n"
	}
	return "---\n" + buf.String() + "---\n" + prompt + "\n", nil
}

func cleanStringList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
