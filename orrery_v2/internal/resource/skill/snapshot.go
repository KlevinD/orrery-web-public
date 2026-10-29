package skill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// PackageSnapshot binds every packaged instruction and resource to its bytes.
// The files live in the existing package store, addressed by the manifest digest.
type PackageSnapshot struct {
	Digest string         `json:"digest"`
	Files  []SnapshotFile `json:"files"`
}

type SnapshotFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// FreezeDefinition creates an immutable executable package without changing the
// editable package directory. An existing snapshot supplies the inherited files.
func (s *PackageStore) FreezeDefinition(ctx context.Context, definition SkillDefinition) (SkillDefinition, error) {
	if err := ctx.Err(); err != nil {
		return SkillDefinition{}, err
	}
	if err := validateDefinition(definition); err != nil {
		return SkillDefinition{}, err
	}
	packageDir, err := s.SkillDirectory(definition.ID)
	if err != nil {
		return SkillDefinition{}, err
	}
	if err := os.MkdirAll(filepath.Dir(packageDir), 0o700); err != nil {
		return SkillDefinition{}, err
	}
	files := map[string][]byte{}
	if definition.PackageSnapshot != nil {
		if err := s.validateSnapshot(definition.PackageSnapshot); err != nil {
			return SkillDefinition{}, err
		}
		for _, file := range definition.PackageSnapshot.Files {
			raw, err := s.ReadSnapshotFile(ctx, definition, file.Path)
			if err != nil {
				return SkillDefinition{}, err
			}
			files[file.Path] = raw
		}
	} else {
		dir, err := s.SkillDirectory(definition.ID)
		if err != nil {
			return SkillDefinition{}, err
		}
		if _, err := os.Stat(dir); err == nil {
			err = filepath.WalkDir(dir, func(name string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				if entry.Type()&os.ModeSymlink != 0 {
					return fmt.Errorf("%w: snapshot does not accept symlinks", ErrHydration)
				}
				if entry.IsDir() {
					return nil
				}
				info, err := entry.Info()
				if err != nil {
					return err
				}
				if !info.Mode().IsRegular() {
					return fmt.Errorf("%w: snapshot requires regular files", ErrHydration)
				}
				rel, err := filepath.Rel(dir, name)
				if err != nil {
					return err
				}
				rel = filepath.ToSlash(rel)
				if _, err := safePackagePath(rel); err != nil {
					return err
				}
				raw, err := readLimitedSnapshotFile(name, s.limits.MaxFileBytes)
				if err != nil {
					return err
				}
				files[rel] = raw
				return s.checkSnapshotLimits(files)
			})
			if err != nil {
				return SkillDefinition{}, err
			}
		} else if !os.IsNotExist(err) {
			return SkillDefinition{}, err
		}
	}
	definition = definition.Clone()
	seen := map[string]bool{}
	for i := range definition.PromptAssets {
		asset := &definition.PromptAssets[i]
		name := strings.TrimSpace(asset.Path)
		if name == "" {
			name = "SKILL.md"
		}
		clean, err := safePackagePath(name)
		if err != nil {
			return SkillDefinition{}, err
		}
		if seen[clean] {
			return SkillDefinition{}, fmt.Errorf("%w: duplicate prompt asset %s", ErrHydration, clean)
		}
		seen[clean] = true
		asset.Path = clean
		if asset.Content != "" {
			files[clean] = []byte(asset.Content)
		} else if raw, ok := files[clean]; ok {
			asset.Content = string(raw)
		} else if asset.ContentRef != "" {
			return SkillDefinition{}, fmt.Errorf("%w: prompt asset %s has no packaged content", ErrHydration, clean)
		} else {
			files[clean] = nil
		}
	}
	// Reconcile the embedded frontmatter with the candidate's definition. This
	// keeps a cloned baseline's old version tag out of the new runtime package.
	if !seen["SKILL.md"] {
		definition.PromptAssets = append(definition.PromptAssets, PromptAsset{Path: "SKILL.md", Content: string(files["SKILL.md"]), Kind: PromptAssetMarkdown})
	}
	markdown, err := frozenMarkdown(definition)
	if err != nil {
		return SkillDefinition{}, err
	}
	files["SKILL.md"] = []byte(markdown)
	for i := range definition.PromptAssets {
		if definition.PromptAssets[i].Path == "SKILL.md" {
			definition.PromptAssets[i].Content = markdown
		}
	}
	if err := s.checkSnapshotLimits(files); err != nil {
		return SkillDefinition{}, err
	}
	snapshot := snapshotForFiles(files)
	if err := s.validateSnapshot(snapshot); err != nil {
		return SkillDefinition{}, err
	}
	if err := s.writeSnapshot(ctx, snapshot, files); err != nil {
		return SkillDefinition{}, err
	}
	definition.PackageSnapshot = snapshot
	return definition, nil
}

// Preserve provider-specific frontmatter while updating the fields represented
// by the registry definition. Losing an unknown key would change the baseline.
func frozenMarkdown(definition SkillDefinition) (string, error) {
	generated := skillMarkdownFromDefinition(definition)
	generatedHeader, generatedBody, _ := snapshotMarkdownParts(generated)
	metadata := map[string]any{}
	for _, asset := range definition.PromptAssets {
		if asset.Path != "SKILL.md" {
			continue
		}
		header, _, present := snapshotMarkdownParts(asset.Content)
		normalized := strings.ReplaceAll(strings.TrimPrefix(asset.Content, "\ufeff"), "\r\n", "\n")
		if strings.HasPrefix(normalized, "---\n") && !present {
			return "", fmt.Errorf("%w: unterminated snapshot frontmatter", ErrHydration)
		}
		if present {
			if err := yaml.Unmarshal([]byte(header), &metadata); err != nil {
				return "", fmt.Errorf("%w: invalid snapshot frontmatter: %v", ErrHydration, err)
			}
			if metadata == nil {
				metadata = map[string]any{}
			}
		}
		break
	}
	known := map[string]any{}
	if err := yaml.Unmarshal([]byte(generatedHeader), &known); err != nil {
		return "", err
	}
	for _, key := range []string{"skill_id", "name", "description", "version", "domain_tags", "task_type_tags", "required_tools", "evidence_refs"} {
		delete(metadata, key)
	}
	for key, value := range known {
		metadata[key] = value
	}
	header, err := yaml.Marshal(metadata)
	if err != nil {
		return "", err
	}
	return "---\n" + string(header) + "---\n" + generatedBody, nil
}

func snapshotMarkdownParts(raw string) (string, string, bool) {
	raw = strings.ReplaceAll(strings.TrimPrefix(raw, "\ufeff"), "\r\n", "\n")
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return "", raw, false
	}
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			return strings.Join(lines[1:i], "\n"), strings.Join(lines[i+1:], "\n"), true
		}
	}
	return "", raw, false
}

func snapshotForFiles(files map[string][]byte) *PackageSnapshot {
	snapshot := &PackageSnapshot{Files: make([]SnapshotFile, 0, len(files))}
	for name, raw := range files {
		sum := sha256.Sum256(raw)
		snapshot.Files = append(snapshot.Files, SnapshotFile{Path: name, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(raw))})
	}
	sort.Slice(snapshot.Files, func(i, j int) bool { return snapshot.Files[i].Path < snapshot.Files[j].Path })
	snapshot.Digest = snapshotDigest(snapshot.Files)
	return snapshot
}

func snapshotDigest(files []SnapshotFile) string {
	raw, _ := json.Marshal(files)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (s *PackageStore) checkSnapshotLimits(files map[string][]byte) error {
	if len(files) > s.limits.MaxFiles {
		return fmt.Errorf("%w: snapshot file count exceeds limit", ErrHydration)
	}
	var total int64
	for _, raw := range files {
		size := int64(len(raw))
		if size > s.limits.MaxFileBytes {
			return fmt.Errorf("%w: snapshot file exceeds limit", ErrHydration)
		}
		total += size
		if total > s.limits.MaxTotalBytes {
			return fmt.Errorf("%w: snapshot exceeds total size limit", ErrHydration)
		}
	}
	return nil
}

func (s *PackageStore) validateSnapshot(snapshot *PackageSnapshot) error {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return fmt.Errorf("%w: package store is not configured", ErrHydration)
	}
	if snapshot == nil || len(snapshot.Files) == 0 || len(snapshot.Files) > s.limits.MaxFiles || len(snapshot.Digest) != 64 {
		return fmt.Errorf("%w: invalid package snapshot", ErrHydration)
	}
	var total int64
	var previous string
	seen := map[string]bool{}
	foundMarkdown := false
	for _, file := range snapshot.Files {
		clean, err := safePackagePath(file.Path)
		if err != nil || clean != file.Path || file.Path <= previous || len(file.SHA256) != 64 || file.Size < 0 || file.Size > s.limits.MaxFileBytes {
			return fmt.Errorf("%w: invalid snapshot file", ErrHydration)
		}
		key := strings.ToLower(file.Path)
		if seen[key] {
			return fmt.Errorf("%w: case-conflicting snapshot paths", ErrHydration)
		}
		seen[key] = true
		if _, err := hex.DecodeString(file.SHA256); err != nil {
			return fmt.Errorf("%w: invalid snapshot file digest", ErrHydration)
		}
		total += file.Size
		if total > s.limits.MaxTotalBytes {
			return fmt.Errorf("%w: snapshot exceeds total size limit", ErrHydration)
		}
		previous = file.Path
		foundMarkdown = foundMarkdown || file.Path == "SKILL.md"
	}
	if !foundMarkdown || snapshotDigest(snapshot.Files) != snapshot.Digest {
		return fmt.Errorf("%w: snapshot manifest digest mismatch", ErrHydration)
	}
	return nil
}

func (s *PackageStore) snapshotDirectory(snapshot *PackageSnapshot) (string, error) {
	if err := s.validateSnapshot(snapshot); err != nil {
		return "", err
	}
	root, err := filepath.Abs(s.root)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ".versions", snapshot.Digest), nil
}

// ReadSnapshotFile returns only bytes declared in, and verified against, the
// frozen manifest. Missing or altered files never fall back to editable files.
func (s *PackageStore) ReadSnapshotFile(ctx context.Context, definition SkillDefinition, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir, err := s.snapshotDirectory(definition.PackageSnapshot)
	if err != nil {
		return nil, err
	}
	clean, err := safePackagePath(name)
	if err != nil {
		return nil, err
	}
	for _, file := range definition.PackageSnapshot.Files {
		if file.Path != clean {
			continue
		}
		filePath, err := safeJoin(filepath.Join(dir, "files"), clean)
		if err != nil {
			return nil, err
		}
		raw, err := readLimitedSnapshotFile(filePath, s.limits.MaxFileBytes)
		if err != nil {
			return nil, fmt.Errorf("%w: snapshot file %s unavailable: %v", ErrHydration, clean, err)
		}
		sum := sha256.Sum256(raw)
		if int64(len(raw)) != file.Size || hex.EncodeToString(sum[:]) != file.SHA256 {
			return nil, fmt.Errorf("%w: snapshot file %s digest mismatch", ErrHydration, clean)
		}
		return raw, nil
	}
	return nil, fmt.Errorf("%w: snapshot resource %s", ErrNotFound, clean)
}

func readLimitedSnapshotFile(name string, limit int64) ([]byte, error) {
	info, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("%w: invalid snapshot file", ErrHydration)
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%w: snapshot file exceeds limit", ErrHydration)
	}
	return raw, nil
}

func (s *PackageStore) writeSnapshot(ctx context.Context, snapshot *PackageSnapshot, files map[string][]byte) error {
	dir, err := s.snapshotDirectory(snapshot)
	if err != nil {
		return err
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	temp, err := os.MkdirTemp(parent, ".snapshot-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	for _, file := range snapshot.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		name, err := safeJoin(filepath.Join(temp, "files"), file.Path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(name, files[file.Path], 0o400); err != nil {
			return err
		}
	}
	if err := os.Rename(temp, dir); err == nil {
		return nil
	}
	// Concurrent writers can publish the same content. Accept only a complete,
	// verified existing snapshot, never replace it or repair it during a read.
	definition := SkillDefinition{PackageSnapshot: snapshot}
	for _, file := range snapshot.Files {
		if _, err := s.ReadSnapshotFile(ctx, definition, file.Path); err != nil {
			return err
		}
	}
	return nil
}
