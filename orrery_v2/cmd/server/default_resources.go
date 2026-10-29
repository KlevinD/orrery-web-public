package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/config"
	skillres "orrery_v2/internal/resource/skill"
	skillstd "orrery_v2/internal/resource/skill/std"
	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func ensureDefaultResourceCatalog(ctx context.Context, tools toolres.Registry, skills skillres.Registry, packageStore ...*skillres.PackageStore) error {
	return ensureDefaultResourceCatalogWithConfig(ctx, tools, skills, config.Config{}, packageStore...)
}

func ensureDefaultResourceCatalogWithConfig(ctx context.Context, tools toolres.Registry, skills skillres.Registry, cfg config.Config, packageStore ...*skillres.PackageStore) error {
	if err := ensureDefaultToolCatalog(ctx, tools, cfg); err != nil {
		return err
	}
	var store *skillres.PackageStore
	if len(packageStore) > 0 {
		store = packageStore[0]
	}
	return ensureDefaultSkillCatalog(ctx, skills, store)
}

func ensureDefaultToolCatalog(ctx context.Context, registry toolres.Registry, configs ...config.Config) error {
	for _, definition := range defaultSystemToolDefinitions(configs...) {
		if err := upsertDefaultTool(ctx, registry, definition); err != nil {
			return err
		}
	}
	return nil
}

func ensureDefaultSkillCatalog(ctx context.Context, registry skillres.Registry, packageStore *skillres.PackageStore) error {
	if err := refreshPackageSkillRisks(ctx, registry, packageStore); err != nil {
		return err
	}
	for _, definition := range defaultStandardSkillDefinitions() {
		if err := upsertDefaultSkill(ctx, registry, packageStore, definition); err != nil {
			return err
		}
	}
	return nil
}

func refreshPackageSkillRisks(ctx context.Context, registry skillres.Registry, packageStore *skillres.PackageStore) error {
	if registry == nil || packageStore == nil {
		return nil
	}
	definitions, err := registry.List(ctx)
	if err != nil {
		return err
	}
	for _, definition := range definitions {
		hydrated, err := packageStore.HydratePackage(ctx, definition.ID)
		if err != nil {
			if errors.Is(err, skillres.ErrNotFound) || errors.Is(err, skillres.ErrHydration) {
				continue
			}
			return err
		}
		hydrated.Enabled = definition.Enabled
		hydrated.CreatedAt = definition.CreatedAt
		hydrated.UpdatedAt = definition.UpdatedAt
		if hydrated.RiskLevel == definition.RiskLevel &&
			hydrated.Name == definition.Name &&
			hydrated.Version == definition.Version &&
			hydrated.Description == definition.Description &&
			stringSlicesEqual(hydrated.DomainTags, definition.DomainTags) &&
			stringSlicesEqual(hydrated.TaskTypeTags, definition.TaskTypeTags) {
			continue
		}
		if err := skillres.ApplyLegacyManagedDefinition(ctx, registry, hydrated, skillres.ManagedUpdateSourceDefaultCatalog); err != nil {
			return err
		}
	}
	return nil
}

func upsertDefaultTool(ctx context.Context, registry toolres.Registry, definition toolres.ToolDefinition) error {
	if registry == nil {
		return nil
	}
	existing, err := registry.Get(ctx, definition.ID)
	if err != nil {
		if errors.Is(err, toolres.ErrNotFound) {
			deleted, deletedErr := registry.IsDeleted(ctx, definition.ID)
			if deletedErr != nil {
				return deletedErr
			}
			if deleted {
				return nil
			}
			return registry.Register(ctx, definition)
		}
		return err
	}
	definition.CreatedAt = firstResourceTime(existing.CreatedAt, definition.CreatedAt)
	return registry.UpdateDefinition(ctx, definition)
}

func upsertDefaultSkill(ctx context.Context, registry skillres.Registry, packageStore *skillres.PackageStore, definition skillres.SkillDefinition) error {
	if registry == nil {
		return nil
	}
	existing, err := registry.Get(ctx, definition.ID)
	if err != nil {
		if errors.Is(err, skillres.ErrNotFound) {
			deleted, deletedErr := registry.IsDeleted(ctx, definition.ID)
			if deletedErr != nil {
				return deletedErr
			}
			if deleted {
				return nil
			}
			if packageStore != nil {
				seeded, err := ensureDefaultSkillPackage(ctx, packageStore, definition)
				if err != nil {
					return err
				}
				definition = seeded
			}
			return registry.Register(ctx, definition)
		}
		return err
	}
	definition.CreatedAt = firstResourceTime(existing.CreatedAt, definition.CreatedAt)
	if packageStore != nil {
		seeded, err := ensureDefaultSkillPackage(ctx, packageStore, definition)
		if err != nil {
			return err
		}
		seeded.CreatedAt = firstResourceTime(existing.CreatedAt, seeded.CreatedAt)
		definition = seeded
	}
	return skillres.ApplyLegacyManagedDefinition(ctx, registry, definition, skillres.ManagedUpdateSourceDefaultCatalog)
}

func ensureDefaultSkillPackage(ctx context.Context, store *skillres.PackageStore, definition skillres.SkillDefinition) (skillres.SkillDefinition, error) {
	if store == nil {
		return definition, nil
	}
	if err := ctx.Err(); err != nil {
		return skillres.SkillDefinition{}, err
	}
	dir, err := store.SkillDirectory(definition.ID)
	if err != nil {
		return skillres.SkillDefinition{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return skillres.SkillDefinition{}, err
	}
	body := defaultSkillMarkdown(definition)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o600); err != nil {
		return skillres.SkillDefinition{}, err
	}
	seeded, err := skillres.HydrateFromDirectory(ctx, dir)
	if err != nil {
		return skillres.SkillDefinition{}, err
	}
	seeded.CreatedAt = definition.CreatedAt
	seeded.UpdatedAt = definition.UpdatedAt
	seeded.SourceRef = definition.SourceRef
	return seeded, nil
}

func defaultSkillMarkdown(definition skillres.SkillDefinition) string {
	var b strings.Builder
	b.WriteString("---\n")
	writeYAMLScalar(&b, "name", strings.TrimPrefix(string(definition.ID), "skill."))
	writeYAMLScalar(&b, "description", definition.Description)
	writeYAMLScalar(&b, "version", definition.Version)
	writeYAMLStringList(&b, "domain_tags", definition.DomainTags)
	writeYAMLStringList(&b, "task_type_tags", definition.TaskTypeTags)
	if len(definition.RequiredTools) > 0 {
		b.WriteString("required_tools:\n")
		for _, tool := range definition.RequiredTools {
			if strings.TrimSpace(string(tool.ToolID)) == "" {
				continue
			}
			b.WriteString("  - ")
			writeYAMLInlineScalar(&b, "tool_id", string(tool.ToolID))
			if strings.TrimSpace(tool.VersionConstraint) != "" {
				b.WriteString("\n    ")
				writeYAMLInlineScalar(&b, "version_constraint", tool.VersionConstraint)
			}
			if strings.TrimSpace(tool.Alias) != "" {
				b.WriteString("\n    ")
				writeYAMLInlineScalar(&b, "alias", tool.Alias)
			}
			if strings.TrimSpace(tool.PermissionScope) != "" {
				b.WriteString("\n    ")
				writeYAMLInlineScalar(&b, "permission_scope", tool.PermissionScope)
			}
			if strings.TrimSpace(tool.BindingPolicy) != "" {
				b.WriteString("\n    ")
				writeYAMLInlineScalar(&b, "binding_policy", tool.BindingPolicy)
			}
			b.WriteString("\n")
		}
	}
	evidenceRefs := make([]string, 0, len(definition.EvidenceRefs))
	for _, ref := range definition.EvidenceRefs {
		if strings.TrimSpace(ref.ID) != "" {
			evidenceRefs = append(evidenceRefs, ref.ID)
		}
	}
	writeYAMLStringList(&b, "evidence_refs", evidenceRefs)
	b.WriteString("---\n\n")
	b.WriteString("# ")
	b.WriteString(strings.TrimPrefix(string(definition.ID), "skill."))
	b.WriteString("\n\n")
	if strings.TrimSpace(definition.Description) != "" {
		b.WriteString(definition.Description)
		b.WriteString("\n")
	}
	return b.String()
}

func writeYAMLScalar(b *strings.Builder, key, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	writeYAMLInlineScalar(b, key, value)
	b.WriteString("\n")
}

func writeYAMLInlineScalar(b *strings.Builder, key, value string) {
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(yamlSingleQuote(value))
}

func writeYAMLStringList(b *strings.Builder, key string, values []string) {
	clean := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			clean = append(clean, strings.TrimSpace(value))
		}
	}
	if len(clean) == 0 {
		return
	}
	sort.Strings(clean)
	b.WriteString(key)
	b.WriteString(":\n")
	for _, value := range clean {
		b.WriteString("  - ")
		b.WriteString(yamlSingleQuote(value))
		b.WriteString("\n")
	}
}

func yamlSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func defaultSystemToolDefinitions(configs ...config.Config) []toolres.ToolDefinition {
	now := defaultResourceCatalogTime()
	definitions := toolstd.Definitions(now)
	definitions = append(definitions, toolstd.SystemMCPServerDefinition(now))
	definitions = append(definitions, defaultLegalMCPServerDefinition(now, configs...))
	definitions = append(definitions, defaultOrreryPublicMCPServerDefinition(now))
	definitions = append(definitions, defaultOrreryCustomerMCPServerDefinition(now))
	if definition, ok := defaultWebsiteMarketingMCPServerDefinition(now); ok {
		definitions = append(definitions, definition)
	}
	if definition, ok := defaultServerBrowserMCPServerDefinition(now); ok {
		definitions = append(definitions, definition)
	}
	definitions = append(definitions, defaultWebsiteKnowledgeCurationToolDefinitions(now)...)
	return definitions
}

func defaultStandardSkillDefinitions() []skillres.SkillDefinition {
	now := defaultResourceCatalogTime()
	webResearch := skillstd.WebResearchDefinition(now)
	definitionsByID := map[agentcore.SkillID]skillres.SkillDefinition{
		webResearch.ID: webResearch,
	}
	for _, fileName := range defaultAgentTemplateFiles() {
		template, err := readDefaultAgentTemplate(fileName)
		if err != nil {
			continue
		}
		for _, ref := range template.Skills {
			source := strings.TrimSpace(ref.SourceURL)
			id := defaultStandardSkillID(ref.CachedName)
			if id == "" {
				continue
			}
			skillID := agentcore.SkillID(id)
			definitionsByID[skillID] = skillres.SkillDefinition{
				ID:           skillID,
				Name:         id,
				Version:      "1.0.0",
				Description:  strings.TrimSpace(ref.CachedDescription),
				DomainTags:   []string{"system", "mission-control", "std"},
				TaskTypeTags: []string{strings.TrimSpace(template.Slug)},
				Enabled:      true,
				SourceRef:    source,
				CreatedAt:    now,
				UpdatedAt:    now,
			}
		}
	}
	out := make([]skillres.SkillDefinition, 0, len(definitionsByID))
	for _, definition := range definitionsByID {
		out = append(out, definition)
	}
	return out
}

func defaultStandardSkillID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "skill.") {
		return value
	}
	return "skill." + value
}

func defaultAgentTemplateFiles() []string {
	files, err := fs.Glob(defaultAgentTemplateFS, "agent_templates/*.json")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(files))
	for _, path := range files {
		out = append(out, strings.TrimPrefix(path, "agent_templates/"))
	}
	return out
}

func defaultResourceCatalogTime() time.Time {
	return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
}

func firstResourceTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
