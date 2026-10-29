package skill

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
	agentcore "orrery_v2/internal/agent/core"
)

var slugUnsafe = regexp.MustCompile(`[^a-z0-9_.-]+`)
var bundledArchiveRootPattern = regexp.MustCompile(`(?i)^skills-[^.]+\.([a-z0-9_.-]+)-master-[a-f0-9]{12,}$`)

type frontmatter struct {
	SkillID       string   `yaml:"skill_id"`
	Name          string   `yaml:"name"`
	Description   string   `yaml:"description"`
	Version       string   `yaml:"version"`
	DomainTags    []string `yaml:"domain_tags"`
	TaskTypeTags  []string `yaml:"task_type_tags"`
	RequiredTools []struct {
		ToolID            string `yaml:"tool_id"`
		VersionConstraint string `yaml:"version_constraint"`
		Alias             string `yaml:"alias"`
		PermissionScope   string `yaml:"permission_scope"`
		BindingPolicy     string `yaml:"binding_policy"`
	} `yaml:"required_tools"`
	EvidenceRefs []string `yaml:"evidence_refs"`
}

func HydrateFromDirectory(ctx context.Context, dir string) (SkillDefinition, error) {
	return hydrateFromDirectory(ctx, dir, "")
}

func hydrateFromDirectory(ctx context.Context, dir, idFallbackName string) (SkillDefinition, error) {
	if err := ctx.Err(); err != nil {
		return SkillDefinition{}, err
	}
	path := filepath.Join(dir, "SKILL.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		return SkillDefinition{}, fmt.Errorf("%w: read %s: %v", ErrHydration, path, err)
	}
	meta, err := parseFrontmatter(string(raw))
	if err != nil {
		return SkillDefinition{}, err
	}
	name := strings.TrimSpace(meta.Name)
	if name == "" {
		name = filepath.Base(dir)
	}
	version := strings.TrimSpace(meta.Version)
	if version == "" {
		version = "1.0.0"
	}
	id := strings.TrimSpace(meta.SkillID)
	if id == "" {
		idSlug := slug(name)
		if idSlug == "" {
			idSlug = slug(skillIDFallbackName(idFallbackName))
		}
		if idSlug == "" {
			idSlug = slug(filepath.Base(dir))
		}
		id = "skill." + idSlug
	}
	riskLevel, err := ClassifyRiskFromDirectory(ctx, dir)
	if err != nil {
		return SkillDefinition{}, fmt.Errorf("%w: classify package risk: %v", ErrHydration, err)
	}
	definition := SkillDefinition{
		ID:           agentcore.SkillID(id),
		Name:         name,
		Version:      version,
		Description:  strings.TrimSpace(meta.Description),
		DomainTags:   append([]string(nil), meta.DomainTags...),
		TaskTypeTags: append([]string(nil), meta.TaskTypeTags...),
		RiskLevel:    riskLevel,
		PromptAssets: []PromptAsset{{
			Path:       "SKILL.md",
			Content:    string(raw),
			ContentRef: "file://" + filepath.ToSlash(path),
			Kind:       PromptAssetMarkdown,
		}},
		Enabled:   true,
		SourceRef: "file://" + filepath.ToSlash(dir),
	}
	for _, ref := range meta.RequiredTools {
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
	for _, ref := range meta.EvidenceRefs {
		if strings.TrimSpace(ref) != "" {
			definition.EvidenceRefs = append(definition.EvidenceRefs, agentcore.EvidenceRef{ID: ref})
		}
	}
	if err := validateDefinition(definition); err != nil {
		return SkillDefinition{}, err
	}
	return definition, nil
}

func LoadDirectorySeeds(ctx context.Context, registry Registry, root string) (int, error) {
	loaded := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Name() != "SKILL.md" {
			return nil
		}
		definition, err := HydrateFromDirectory(ctx, filepath.Dir(path))
		if err != nil {
			return err
		}
		if err := registry.Register(ctx, definition); err != nil {
			return err
		}
		loaded++
		return nil
	})
	if err != nil {
		return loaded, fmt.Errorf("%w: load directory seeds: %v", ErrHydration, err)
	}
	return loaded, nil
}

func parseFrontmatter(content string) (frontmatter, error) {
	trimmed := strings.TrimPrefix(content, "\ufeff")
	if !strings.HasPrefix(trimmed, "---\n") && !strings.HasPrefix(trimmed, "---\r\n") {
		return frontmatter{}, nil
	}
	lines := strings.Split(strings.ReplaceAll(trimmed, "\r\n", "\n"), "\n")
	var yamlLines []string
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			var meta frontmatter
			if err := yaml.Unmarshal([]byte(strings.Join(yamlLines, "\n")), &meta); err != nil {
				return frontmatter{}, fmt.Errorf("%w: parse SKILL.md frontmatter: %v", ErrHydration, err)
			}
			return meta, nil
		}
		yamlLines = append(yamlLines, lines[i])
	}
	return frontmatter{}, fmt.Errorf("%w: unterminated SKILL.md frontmatter", ErrHydration)
}

func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = slugUnsafe.ReplaceAllString(value, "-")
	return strings.Trim(value, "-")
}

func skillIDFallbackName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "skill.")
	if match := bundledArchiveRootPattern.FindStringSubmatch(value); len(match) == 2 {
		return match[1]
	}
	return value
}
