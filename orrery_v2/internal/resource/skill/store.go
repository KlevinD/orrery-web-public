package skill

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	agentcore "orrery_v2/internal/agent/core"
)

const (
	DefaultMaxPackageFiles      = 256
	DefaultMaxPackageTotalBytes = 128 << 20
	DefaultMaxPackageFileBytes  = 64 << 20
	DefaultMaxPreviewBytes      = 1 << 20
)

type PackageLimits struct {
	MaxFiles        int
	MaxTotalBytes   int64
	MaxFileBytes    int64
	MaxPreviewBytes int64
}

type PackageStore struct {
	root   string
	limits PackageLimits
}

func NewPackageStore(root string) *PackageStore {
	return NewPackageStoreWithLimits(root, PackageLimits{})
}

func NewPackageStoreWithLimits(root string, limits PackageLimits) *PackageStore {
	return &PackageStore{root: root, limits: normalizePackageLimits(limits)}
}

func (s *PackageStore) SkillDirectory(id agentcore.SkillID) (string, error) {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return "", fmt.Errorf("%w: skill package root is required", ErrHydration)
	}
	dirName := skillDirectoryName(id)
	if dirName == "" {
		return "", fmt.Errorf("%w: skill_id is required", ErrInvalidDefinition)
	}
	root, err := filepath.Abs(s.root)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, dirName), nil
}

func (s *PackageStore) HydratePackage(ctx context.Context, id agentcore.SkillID) (SkillDefinition, error) {
	if err := ctx.Err(); err != nil {
		return SkillDefinition{}, err
	}
	dir, err := s.SkillDirectory(id)
	if err != nil {
		return SkillDefinition{}, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return SkillDefinition{}, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return SkillDefinition{}, err
	}
	if !info.IsDir() {
		return SkillDefinition{}, fmt.Errorf("%w: %s package directory is not a directory", ErrHydration, id)
	}
	return hydrateFromDirectory(ctx, dir, string(id))
}

func (s *PackageStore) ImportPackage(ctx context.Context, reader io.ReaderAt, size int64) (SkillDefinition, error) {
	if err := ctx.Err(); err != nil {
		return SkillDefinition{}, err
	}
	if err := s.ensureRoot(); err != nil {
		return SkillDefinition{}, err
	}
	pkg, err := readDirectoryPackage(ctx, reader, size, s.limits)
	if err != nil {
		return SkillDefinition{}, err
	}
	tempDir, err := os.MkdirTemp(s.root, ".import-*")
	if err != nil {
		return SkillDefinition{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tempDir)
		}
	}()
	for relPath, content := range pkg.files {
		target, err := safeJoin(tempDir, relPath)
		if err != nil {
			return SkillDefinition{}, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return SkillDefinition{}, err
		}
		if err := os.WriteFile(target, content, 0o600); err != nil {
			return SkillDefinition{}, err
		}
	}
	definition, err := hydrateFromDirectory(ctx, tempDir, pkg.idFallbackName)
	if err != nil {
		return SkillDefinition{}, err
	}
	finalDir, err := s.SkillDirectory(definition.ID)
	if err != nil {
		return SkillDefinition{}, err
	}
	if err := replaceDirectory(tempDir, finalDir); err != nil {
		return SkillDefinition{}, err
	}
	cleanup = false
	return HydrateFromDirectory(ctx, finalDir)
}

func (s *PackageStore) CreatePackage(ctx context.Context, rawSkillMarkdown string) (SkillDefinition, error) {
	if err := ctx.Err(); err != nil {
		return SkillDefinition{}, err
	}
	if err := s.ensureRoot(); err != nil {
		return SkillDefinition{}, err
	}
	tempDir, err := os.MkdirTemp(s.root, ".create-*")
	if err != nil {
		return SkillDefinition{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tempDir)
		}
	}()
	if err := os.WriteFile(filepath.Join(tempDir, "SKILL.md"), []byte(rawSkillMarkdown), 0o600); err != nil {
		return SkillDefinition{}, err
	}
	definition, err := HydrateFromDirectory(ctx, tempDir)
	if err != nil {
		return SkillDefinition{}, err
	}
	finalDir, err := s.SkillDirectory(definition.ID)
	if err != nil {
		return SkillDefinition{}, err
	}
	if _, err := os.Stat(finalDir); err == nil {
		return SkillDefinition{}, fmt.Errorf("%w: skill package %s already exists", ErrAlreadyExists, definition.ID)
	} else if !os.IsNotExist(err) {
		return SkillDefinition{}, err
	}
	if err := os.Rename(tempDir, finalDir); err != nil {
		return SkillDefinition{}, err
	}
	cleanup = false
	return HydrateFromDirectory(ctx, finalDir)
}

func (s *PackageStore) UpsertDefinitionPackage(ctx context.Context, definition SkillDefinition) (SkillDefinition, error) {
	if err := ctx.Err(); err != nil {
		return SkillDefinition{}, err
	}
	if err := validateDefinition(definition); err != nil {
		return SkillDefinition{}, err
	}
	if err := s.ensureRoot(); err != nil {
		return SkillDefinition{}, err
	}
	tempDir, err := os.MkdirTemp(s.root, ".definition-*")
	if err != nil {
		return SkillDefinition{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tempDir)
		}
	}()
	if err := os.WriteFile(filepath.Join(tempDir, "SKILL.md"), []byte(skillMarkdownFromDefinition(definition)), 0o600); err != nil {
		return SkillDefinition{}, err
	}
	hydrated, err := HydrateFromDirectory(ctx, tempDir)
	if err != nil {
		return SkillDefinition{}, err
	}
	finalDir, err := s.SkillDirectory(definition.ID)
	if err != nil {
		return SkillDefinition{}, err
	}
	if err := replaceDirectory(tempDir, finalDir); err != nil {
		return SkillDefinition{}, err
	}
	cleanup = false
	hydrated, err = HydrateFromDirectory(ctx, finalDir)
	if err != nil {
		return SkillDefinition{}, err
	}
	hydrated.CreatedAt = definition.CreatedAt
	hydrated.UpdatedAt = definition.UpdatedAt
	hydrated.SourceRef = definition.SourceRef
	return hydrated, nil
}

func (s *PackageStore) ExportPackage(ctx context.Context, id agentcore.SkillID, writer io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := s.SkillDirectory(id)
	if err != nil {
		return err
	}
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s package directory is not a directory", ErrHydration, id)
	}
	files, err := collectPackageFiles(ctx, dir)
	if err != nil {
		return err
	}
	zipWriter := zip.NewWriter(writer)
	top := skillDirectoryName(id)
	for _, relPath := range files {
		if err := ctx.Err(); err != nil {
			_ = zipWriter.Close()
			return err
		}
		entryName := top + "/" + relPath
		entry, err := zipWriter.Create(entryName)
		if err != nil {
			_ = zipWriter.Close()
			return err
		}
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(relPath)))
		if err != nil {
			_ = zipWriter.Close()
			return err
		}
		if _, err := entry.Write(raw); err != nil {
			_ = zipWriter.Close()
			return err
		}
	}
	return zipWriter.Close()
}

func skillMarkdownFromDefinition(definition SkillDefinition) string {
	meta := struct {
		SkillID       string   `yaml:"skill_id"`
		Name          string   `yaml:"name"`
		Description   string   `yaml:"description,omitempty"`
		Version       string   `yaml:"version"`
		DomainTags    []string `yaml:"domain_tags,omitempty"`
		TaskTypeTags  []string `yaml:"task_type_tags,omitempty"`
		RequiredTools []struct {
			ToolID            string `yaml:"tool_id"`
			VersionConstraint string `yaml:"version_constraint,omitempty"`
			Alias             string `yaml:"alias,omitempty"`
			PermissionScope   string `yaml:"permission_scope,omitempty"`
			BindingPolicy     string `yaml:"binding_policy,omitempty"`
		} `yaml:"required_tools,omitempty"`
		EvidenceRefs []string `yaml:"evidence_refs,omitempty"`
	}{
		SkillID:      string(definition.ID),
		Name:         strings.TrimSpace(definition.Name),
		Description:  strings.TrimSpace(definition.Description),
		Version:      strings.TrimSpace(definition.Version),
		DomainTags:   cleanPackageStringList(definition.DomainTags),
		TaskTypeTags: cleanPackageStringList(definition.TaskTypeTags),
	}
	for _, ref := range definition.RequiredTools {
		if strings.TrimSpace(string(ref.ToolID)) == "" {
			continue
		}
		meta.RequiredTools = append(meta.RequiredTools, struct {
			ToolID            string `yaml:"tool_id"`
			VersionConstraint string `yaml:"version_constraint,omitempty"`
			Alias             string `yaml:"alias,omitempty"`
			PermissionScope   string `yaml:"permission_scope,omitempty"`
			BindingPolicy     string `yaml:"binding_policy,omitempty"`
		}{
			ToolID:            string(ref.ToolID),
			VersionConstraint: strings.TrimSpace(ref.VersionConstraint),
			Alias:             strings.TrimSpace(ref.Alias),
			PermissionScope:   strings.TrimSpace(ref.PermissionScope),
			BindingPolicy:     strings.TrimSpace(ref.BindingPolicy),
		})
	}
	for _, ref := range definition.EvidenceRefs {
		if strings.TrimSpace(ref.ID) != "" {
			meta.EvidenceRefs = append(meta.EvidenceRefs, strings.TrimSpace(ref.ID))
		}
	}
	var b strings.Builder
	encoder := yaml.NewEncoder(&b)
	encoder.SetIndent(2)
	if err := encoder.Encode(meta); err != nil {
		return fallbackSkillMarkdown(definition)
	}
	if err := encoder.Close(); err != nil {
		return fallbackSkillMarkdown(definition)
	}
	body := strings.TrimSpace(definitionPackageMarkdownBody(definition))
	if body == "" {
		body = "# " + strings.TrimSpace(definition.Name) + "\n\n" + strings.TrimSpace(definition.Description)
	}
	return "---\n" + b.String() + "---\n\n" + body + "\n"
}

func definitionPackageMarkdownBody(definition SkillDefinition) string {
	for _, asset := range definition.PromptAssets {
		if strings.TrimSpace(asset.Path) != "" && !strings.EqualFold(strings.TrimSpace(asset.Path), "SKILL.md") {
			continue
		}
		if strings.TrimSpace(asset.Content) == "" {
			continue
		}
		return stripSkillMarkdownFrontmatter(asset.Content)
	}
	return ""
}

func stripSkillMarkdownFrontmatter(content string) string {
	trimmed := strings.TrimPrefix(content, "\ufeff")
	if !strings.HasPrefix(trimmed, "---\n") && !strings.HasPrefix(trimmed, "---\r\n") {
		return content
	}
	lines := strings.Split(strings.ReplaceAll(trimmed, "\r\n", "\n"), "\n")
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			return strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
		}
	}
	return content
}

func cleanPackageStringList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func fallbackSkillMarkdown(definition SkillDefinition) string {
	return "---\nskill_id: " + string(definition.ID) + "\nname: " + definition.Name + "\nversion: " + definition.Version + "\n---\n\n" + definitionPackageMarkdownBody(definition) + "\n"
}

func (s *PackageStore) ReadFile(ctx context.Context, id agentcore.SkillID, relPath string) (FileNode, []byte, error) {
	if err := ctx.Err(); err != nil {
		return FileNode{}, nil, err
	}
	normalized, err := validateResourceFilePath(relPath)
	if err != nil {
		return FileNode{}, nil, err
	}
	filePath, err := s.safeSkillPath(id, relPath)
	if err != nil {
		return FileNode{}, nil, err
	}
	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return FileNode{}, nil, fmt.Errorf("%w: %s", ErrNotFound, relPath)
		}
		return FileNode{}, nil, err
	}
	if info.IsDir() {
		return FileNode{}, nil, fmt.Errorf("%w: %s is a directory", ErrHydration, relPath)
	}
	node := fileNodeForPath(normalized, info.Size(), s.limits.MaxPreviewBytes)
	switch node.PreviewKind {
	case PreviewKindMarkdown, PreviewKindText, PreviewKindJSON:
	default:
		return node, nil, nil
	}
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return FileNode{}, nil, err
	}
	return node, raw, nil
}

func (s *PackageStore) WriteTextFile(ctx context.Context, id agentcore.SkillID, relPath, content string) (FileNode, []byte, error) {
	if err := ctx.Err(); err != nil {
		return FileNode{}, nil, err
	}
	normalized, err := validateResourceFilePath(relPath)
	if err != nil {
		return FileNode{}, nil, err
	}
	filePath, err := s.safeSkillPath(id, relPath)
	if err != nil {
		return FileNode{}, nil, err
	}
	kind := previewKindForPath(normalized, int64(len(content)), s.limits.MaxPreviewBytes)
	if kind != PreviewKindMarkdown && kind != PreviewKindText && kind != PreviewKindJSON {
		return FileNode{}, nil, fmt.Errorf("%w: %s is not editable text", ErrHydration, relPath)
	}
	if err := writeFileAtomic(filePath, []byte(content)); err != nil {
		return FileNode{}, nil, err
	}
	return s.ReadFile(ctx, id, relPath)
}

func (s *PackageStore) WriteFile(ctx context.Context, id agentcore.SkillID, relPath string, content []byte) (FileNode, []byte, error) {
	if err := ctx.Err(); err != nil {
		return FileNode{}, nil, err
	}
	normalized, err := validateResourceFilePath(relPath)
	if err != nil {
		return FileNode{}, nil, err
	}
	if int64(len(content)) > s.limits.MaxFileBytes {
		return FileNode{}, nil, fmt.Errorf("%w: package file %q exceeds %d bytes", ErrHydration, normalized, s.limits.MaxFileBytes)
	}
	filePath, err := s.safeSkillPath(id, normalized)
	if err != nil {
		return FileNode{}, nil, err
	}
	if err := writeFileAtomic(filePath, content); err != nil {
		return FileNode{}, nil, err
	}
	return s.ReadFile(ctx, id, normalized)
}

func (s *PackageStore) DownloadFile(ctx context.Context, id agentcore.SkillID, relPath string) (FileNode, []byte, error) {
	if err := ctx.Err(); err != nil {
		return FileNode{}, nil, err
	}
	normalized, err := validateResourceFilePath(relPath)
	if err != nil {
		return FileNode{}, nil, err
	}
	filePath, err := s.safeSkillPath(id, normalized)
	if err != nil {
		return FileNode{}, nil, err
	}
	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return FileNode{}, nil, fmt.Errorf("%w: %s", ErrNotFound, normalized)
		}
		return FileNode{}, nil, err
	}
	if info.IsDir() {
		return FileNode{}, nil, fmt.Errorf("%w: %s is a directory", ErrHydration, normalized)
	}
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return FileNode{}, nil, err
	}
	return fileNodeForPath(normalized, info.Size(), s.limits.MaxPreviewBytes), raw, nil
}

func (s *PackageStore) DeleteFile(ctx context.Context, id agentcore.SkillID, relPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized, err := validateResourceFilePath(relPath)
	if err != nil {
		return err
	}
	filePath, err := s.safeSkillPath(id, normalized)
	if err != nil {
		return err
	}
	if err := os.Remove(filePath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrNotFound, normalized)
		}
		return err
	}
	dir, err := s.SkillDirectory(id)
	if err != nil {
		return err
	}
	return pruneEmptyParents(filepath.Dir(filePath), dir)
}

func (s *PackageStore) CreateDirectory(ctx context.Context, id agentcore.SkillID, relPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized, err := validateResourceFilePath(relPath)
	if err != nil {
		return err
	}
	dirPath, err := s.safeSkillPath(id, normalized)
	if err != nil {
		return err
	}
	if info, err := os.Stat(dirPath); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%w: %s is a file", ErrHydration, normalized)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.MkdirAll(dirPath, 0o755)
}

func (s *PackageStore) DeletePackage(ctx context.Context, id agentcore.SkillID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := s.SkillDirectory(id)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return nil
}

func (s *PackageStore) WriteSkillMarkdown(ctx context.Context, oldID agentcore.SkillID, raw string) (SkillDefinition, error) {
	if err := ctx.Err(); err != nil {
		return SkillDefinition{}, err
	}
	dir, err := s.SkillDirectory(oldID)
	if err != nil {
		return SkillDefinition{}, err
	}
	tempDir, err := os.MkdirTemp(s.root, ".skill-md-*")
	if err != nil {
		return SkillDefinition{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tempDir)
		}
	}()
	if err := copyDirectory(ctx, dir, tempDir); err != nil {
		return SkillDefinition{}, err
	}
	if err := os.WriteFile(filepath.Join(tempDir, "SKILL.md"), []byte(raw), 0o600); err != nil {
		return SkillDefinition{}, err
	}
	definition, err := hydrateFromDirectory(ctx, tempDir, string(oldID))
	if err != nil {
		return SkillDefinition{}, err
	}
	finalDir, err := s.SkillDirectory(definition.ID)
	if err != nil {
		return SkillDefinition{}, err
	}
	if definition.ID != oldID {
		if _, err := os.Stat(finalDir); err == nil {
			return SkillDefinition{}, fmt.Errorf("%w: skill package %s already exists", ErrAlreadyExists, definition.ID)
		} else if !os.IsNotExist(err) {
			return SkillDefinition{}, err
		}
	}
	if err := replaceDirectory(tempDir, finalDir); err != nil {
		return SkillDefinition{}, err
	}
	cleanup = false
	if definition.ID != oldID {
		oldDir, err := s.SkillDirectory(oldID)
		if err != nil {
			return SkillDefinition{}, err
		}
		_ = os.RemoveAll(oldDir)
	}
	return hydrateFromDirectory(ctx, finalDir, string(definition.ID))
}

func (s *PackageStore) safeSkillPath(id agentcore.SkillID, relPath string) (string, error) {
	dir, err := s.SkillDirectory(id)
	if err != nil {
		return "", err
	}
	return safeJoin(dir, relPath)
}

func validateResourceFilePath(relPath string) (string, error) {
	normalized, err := safePackagePath(relPath)
	if err != nil {
		return "", err
	}
	if normalized == "SKILL.md" {
		return "", fmt.Errorf("%w: SKILL.md must be edited through the skill markdown endpoint", ErrHydration)
	}
	return normalized, nil
}

func fileNodeForPath(relPath string, size, maxPreviewBytes int64) FileNode {
	return FileNode{
		Path:        relPath,
		Name:        path.Base(relPath),
		Type:        "file",
		Size:        size,
		MediaType:   mediaTypeForPath(relPath),
		PreviewKind: previewKindForPath(relPath, size, maxPreviewBytes),
	}
}

func writeFileAtomic(filePath string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(filePath), ".write-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempName)
		return err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempName)
		return err
	}
	if err := os.Rename(tempName, filePath); err != nil {
		_ = os.Remove(tempName)
		return err
	}
	return nil
}

func pruneEmptyParents(currentDir, stopDir string) error {
	currentAbs, err := filepath.Abs(currentDir)
	if err != nil {
		return err
	}
	stopAbs, err := filepath.Abs(stopDir)
	if err != nil {
		return err
	}
	for currentAbs != stopAbs && strings.HasPrefix(currentAbs, stopAbs+string(os.PathSeparator)) {
		if err := os.Remove(currentAbs); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			if isDirectoryNotEmpty(err) {
				return nil
			}
			return err
		}
		currentAbs = filepath.Dir(currentAbs)
	}
	return nil
}

func isDirectoryNotEmpty(err error) bool {
	return errors.Is(err, os.ErrExist) || strings.Contains(strings.ToLower(err.Error()), "directory not empty") || strings.Contains(strings.ToLower(err.Error()), "not empty")
}

func copyDirectory(ctx context.Context, sourceDir, targetDir string) error {
	return filepath.WalkDir(sourceDir, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relPath, err := filepath.Rel(sourceDir, filePath)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}
		targetPath := filepath.Join(targetDir, relPath)
		if entry.IsDir() {
			return os.MkdirAll(targetPath, 0o755)
		}
		raw, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		return os.WriteFile(targetPath, raw, 0o600)
	})
}

func (s *PackageStore) ensureRoot() error {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return fmt.Errorf("%w: skill package root is required", ErrHydration)
	}
	abs, err := filepath.Abs(s.root)
	if err != nil {
		return err
	}
	s.root = abs
	return os.MkdirAll(abs, 0o755)
}

type directoryPackage struct {
	files          map[string][]byte
	idFallbackName string
}

func readDirectoryPackage(ctx context.Context, reader io.ReaderAt, size int64, limits PackageLimits) (directoryPackage, error) {
	if err := ctx.Err(); err != nil {
		return directoryPackage{}, err
	}
	limits = normalizePackageLimits(limits)
	zipReader, err := zip.NewReader(reader, size)
	if err != nil {
		return directoryPackage{}, fmt.Errorf("%w: open skill package: %v", ErrHydration, err)
	}
	if len(zipReader.File) > limits.MaxFiles {
		return directoryPackage{}, fmt.Errorf("%w: package has %d files, max %d", ErrHydration, len(zipReader.File), limits.MaxFiles)
	}
	var entries []packageEntry
	var totalBytes int64
	hasLegacyManifest := false
	hasLegacyFiles := false
	for _, file := range zipReader.File {
		if err := ctx.Err(); err != nil {
			return directoryPackage{}, err
		}
		rawName := strings.TrimSuffix(file.Name, "/")
		if rawName == "" {
			continue
		}
		entryName, err := safePackagePath(rawName)
		if err != nil {
			return directoryPackage{}, err
		}
		if file.FileInfo().IsDir() {
			continue
		}
		if entryName == "skill.json" {
			hasLegacyManifest = true
		}
		if strings.HasPrefix(entryName, "files/") {
			hasLegacyFiles = true
		}
		if file.UncompressedSize64 > uint64(limits.MaxFileBytes) {
			return directoryPackage{}, fmt.Errorf("%w: package file %q exceeds %d bytes", ErrHydration, entryName, limits.MaxFileBytes)
		}
		if totalBytes+int64(file.UncompressedSize64) > limits.MaxTotalBytes {
			return directoryPackage{}, fmt.Errorf("%w: package exceeds %d bytes", ErrHydration, limits.MaxTotalBytes)
		}
		content, err := readZipEntry(file, limits.MaxFileBytes)
		if err != nil {
			return directoryPackage{}, err
		}
		totalBytes += int64(len(content))
		if totalBytes > limits.MaxTotalBytes {
			return directoryPackage{}, fmt.Errorf("%w: package exceeds %d bytes", ErrHydration, limits.MaxTotalBytes)
		}
		entries = append(entries, packageEntry{name: entryName, content: content})
	}
	if hasLegacyManifest && hasLegacyFiles {
		return directoryPackage{}, fmt.Errorf("%w: unsupported legacy skill package", ErrHydration)
	}
	prefix, err := packageContentPrefix(entries)
	if err != nil {
		return directoryPackage{}, err
	}
	out := map[string][]byte{}
	fallbackName := strings.TrimSuffix(prefix, "/")
	for _, entry := range entries {
		relPath := entry.name
		if prefix != "" {
			if !strings.HasPrefix(entry.name, prefix) {
				return directoryPackage{}, fmt.Errorf("%w: package mixes multiple roots", ErrHydration)
			}
			relPath = strings.TrimPrefix(entry.name, prefix)
		}
		if relPath == "" {
			continue
		}
		if _, err := safePackagePath(relPath); err != nil {
			return directoryPackage{}, err
		}
		out[relPath] = entry.content
	}
	if _, exists := out["SKILL.md"]; !exists {
		return directoryPackage{}, fmt.Errorf("%w: skill package must contain SKILL.md", ErrHydration)
	}
	return directoryPackage{files: out, idFallbackName: fallbackName}, nil
}

type packageEntry struct {
	name    string
	content []byte
}

func normalizePackageLimits(limits PackageLimits) PackageLimits {
	if limits.MaxFiles <= 0 {
		limits.MaxFiles = DefaultMaxPackageFiles
	}
	if limits.MaxTotalBytes <= 0 {
		limits.MaxTotalBytes = DefaultMaxPackageTotalBytes
	}
	if limits.MaxFileBytes <= 0 {
		limits.MaxFileBytes = DefaultMaxPackageFileBytes
	}
	if limits.MaxPreviewBytes <= 0 {
		limits.MaxPreviewBytes = DefaultMaxPreviewBytes
	}
	return limits
}

func safePackagePath(name string) (string, error) {
	if strings.Contains(name, "\\") || hasWindowsDrivePrefix(name) {
		return "", fmt.Errorf("%w: unsafe package path %q", ErrHydration, name)
	}
	clean := path.Clean(name)
	if clean == "." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") || path.IsAbs(clean) {
		return "", fmt.Errorf("%w: unsafe package path %q", ErrHydration, name)
	}
	if clean != name {
		return "", fmt.Errorf("%w: unsafe package path %q", ErrHydration, name)
	}
	return clean, nil
}

func safeJoin(root, relPath string) (string, error) {
	clean, err := safePackagePath(relPath)
	if err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target := filepath.Join(rootAbs, filepath.FromSlash(clean))
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if targetAbs != rootAbs && !strings.HasPrefix(targetAbs, rootAbs+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: unsafe package path %q", ErrHydration, relPath)
	}
	return targetAbs, nil
}

func readZipEntry(file *zip.File, maxBytes int64) ([]byte, error) {
	rc, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	limited := io.LimitReader(rc, maxBytes+1)
	content, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > maxBytes {
		return nil, fmt.Errorf("%w: package file %q exceeds %d bytes", ErrHydration, file.Name, maxBytes)
	}
	return content, nil
}

func packageContentPrefix(entries []packageEntry) (string, error) {
	hasRootSkill := false
	topLevels := map[string]bool{}
	for _, entry := range entries {
		if entry.name == "SKILL.md" {
			hasRootSkill = true
		}
		first := strings.SplitN(entry.name, "/", 2)[0]
		topLevels[first] = true
	}
	if hasRootSkill {
		return "", nil
	}
	var top string
	for name := range topLevels {
		if top != "" {
			return "", fmt.Errorf("%w: package must contain root SKILL.md or one skill folder", ErrHydration)
		}
		top = name
	}
	if top == "" {
		return "", fmt.Errorf("%w: skill package must contain SKILL.md", ErrHydration)
	}
	return top + "/", nil
}

func collectPackageFiles(ctx context.Context, root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relPath, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		relPath = filepath.ToSlash(relPath)
		if _, err := safePackagePath(relPath); err != nil {
			return err
		}
		files = append(files, relPath)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func replaceDirectory(sourceDir, targetDir string) error {
	if _, err := os.Stat(targetDir); err != nil {
		if os.IsNotExist(err) {
			return os.Rename(sourceDir, targetDir)
		}
		return err
	}
	backupDir := targetDir + ".bak-" + filepath.Base(sourceDir)
	_ = os.RemoveAll(backupDir)
	if err := os.Rename(targetDir, backupDir); err != nil {
		return err
	}
	if err := os.Rename(sourceDir, targetDir); err != nil {
		_ = os.Rename(backupDir, targetDir)
		return err
	}
	return os.RemoveAll(backupDir)
}

func skillDirectoryName(id agentcore.SkillID) string {
	value := strings.TrimSpace(string(id))
	value = strings.TrimPrefix(value, "skill.")
	return slug(value)
}

func hasWindowsDrivePrefix(value string) bool {
	if len(value) < 2 || value[1] != ':' {
		return false
	}
	first := value[0]
	return (first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z')
}
