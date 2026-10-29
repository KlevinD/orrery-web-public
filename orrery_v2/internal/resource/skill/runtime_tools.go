package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

type RuntimeToolServiceOptions struct {
	Registry Registry
	Store    *PackageStore
}

type RuntimeToolService struct {
	registry Registry
	store    *PackageStore
}

type RuntimeSkillRequest struct {
	SkillBindings []agentcore.RuntimeSkillBinding
	SkillID       string
	Path          string
	Query         string
}

type RuntimeToolResult struct {
	Output map[string]any
}

type RuntimeSkillSummary struct {
	SkillID       string `json:"skill_id"`
	Alias         string `json:"alias,omitempty"`
	Version       string `json:"version,omitempty"`
	Name          string `json:"name,omitempty"`
	Summary       string `json:"summary,omitempty"`
	PackageDigest string `json:"package_digest,omitempty"`
}

type RuntimeSkillListResult struct {
	Skills []RuntimeSkillSummary `json:"skills"`
	Total  int                   `json:"total"`
}

type RuntimeSkillLoadResult struct {
	Skill   RuntimeSkillSummary `json:"skill"`
	SkillID string              `json:"skill_id"`
	Content string              `json:"content"`
	Source  string              `json:"source,omitempty"`
}

type RuntimeSkillResource struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Size        int64  `json:"size"`
	MediaType   string `json:"media_type,omitempty"`
	PreviewKind string `json:"preview_kind"`
}

type RuntimeSkillResourceListResult struct {
	SkillID   string                 `json:"skill_id"`
	Resources []RuntimeSkillResource `json:"resources"`
	Total     int                    `json:"total"`
}

type RuntimeSkillResourceLoadResult struct {
	SkillID     string `json:"skill_id"`
	Path        string `json:"path"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	MediaType   string `json:"media_type,omitempty"`
	PreviewKind string `json:"preview_kind"`
	Content     string `json:"content,omitempty"`
}

func NewRuntimeToolService(opts RuntimeToolServiceOptions) *RuntimeToolService {
	return &RuntimeToolService{registry: opts.Registry, store: opts.Store}
}

func (s *RuntimeToolService) ExecuteSkillTool(ctx context.Context, toolID string, req RuntimeSkillRequest) (RuntimeToolResult, error) {
	switch strings.TrimSpace(toolID) {
	case "list_skills":
		result, err := s.ListSkills(ctx, req)
		return runtimeToolResult(result, err)
	case "load_skill":
		result, err := s.LoadSkill(ctx, req)
		return runtimeToolResult(result, err)
	case "list_skill_resources":
		result, err := s.ListSkillResources(ctx, req)
		return runtimeToolResult(result, err)
	case "load_resource":
		result, err := s.LoadResource(ctx, req)
		return runtimeToolResult(result, err)
	default:
		return RuntimeToolResult{}, fmt.Errorf("%w: %s", ErrNotFound, toolID)
	}
}

func runtimeToolResult(value any, err error) (RuntimeToolResult, error) {
	if err != nil {
		return RuntimeToolResult{}, err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return RuntimeToolResult{}, err
	}
	var output map[string]any
	if err := json.Unmarshal(raw, &output); err != nil {
		return RuntimeToolResult{}, err
	}
	return RuntimeToolResult{Output: output}, nil
}

func (s *RuntimeToolService) ListSkills(ctx context.Context, req RuntimeSkillRequest) (RuntimeSkillListResult, error) {
	if err := ctx.Err(); err != nil {
		return RuntimeSkillListResult{}, err
	}
	bindings := s.boundSkillSummaries(ctx, req.SkillBindings)
	query := strings.ToLower(strings.TrimSpace(req.Query))
	out := make([]RuntimeSkillSummary, 0, len(bindings))
	for _, item := range bindings {
		if query != "" && !runtimeSkillMatches(item, query) {
			continue
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SkillID < out[j].SkillID })
	return RuntimeSkillListResult{Skills: out, Total: len(out)}, nil
}

func (s *RuntimeToolService) LoadSkill(ctx context.Context, req RuntimeSkillRequest) (RuntimeSkillLoadResult, error) {
	if err := ctx.Err(); err != nil {
		return RuntimeSkillLoadResult{}, err
	}
	summary, definition, err := s.resolveBoundSkill(ctx, req)
	if err != nil {
		return RuntimeSkillLoadResult{}, err
	}
	if s == nil || s.store == nil {
		return RuntimeSkillLoadResult{}, fmt.Errorf("%w: skill package store is not configured", ErrHydration)
	}
	if definition.PackageSnapshot != nil {
		raw, err := s.store.ReadSnapshotFile(ctx, definition, "SKILL.md")
		if err != nil {
			return RuntimeSkillLoadResult{}, err
		}
		return RuntimeSkillLoadResult{Skill: summary, SkillID: summary.SkillID, Content: string(raw), Source: definition.SourceRef}, nil
	}
	if err := s.validateLegacyPackage(ctx, definition); err != nil {
		return RuntimeSkillLoadResult{}, err
	}
	dir, err := s.store.SkillDirectory(agentcore.SkillID(summary.SkillID))
	if err != nil {
		return RuntimeSkillLoadResult{}, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return RuntimeSkillLoadResult{}, fmt.Errorf("%w: read SKILL.md: %v", ErrHydration, err)
	}
	return RuntimeSkillLoadResult{
		Skill:   summary,
		SkillID: summary.SkillID,
		Content: string(raw),
		Source:  definition.SourceRef,
	}, nil
}

func (s *RuntimeToolService) ListSkillResources(ctx context.Context, req RuntimeSkillRequest) (RuntimeSkillResourceListResult, error) {
	if err := ctx.Err(); err != nil {
		return RuntimeSkillResourceListResult{}, err
	}
	summary, definition, err := s.resolveBoundSkill(ctx, req)
	if err != nil {
		return RuntimeSkillResourceListResult{}, err
	}
	if s == nil || s.store == nil {
		return RuntimeSkillResourceListResult{}, fmt.Errorf("%w: skill package store is not configured", ErrHydration)
	}
	if definition.PackageSnapshot != nil {
		resources := make([]RuntimeSkillResource, 0, len(definition.PackageSnapshot.Files))
		for _, file := range definition.PackageSnapshot.Files {
			if _, err := s.store.ReadSnapshotFile(ctx, definition, file.Path); err != nil {
				return RuntimeSkillResourceListResult{}, err
			}
			if file.Path == "SKILL.md" {
				continue
			}
			node := fileNodeForPath(file.Path, file.Size, s.store.limits.MaxPreviewBytes)
			resources = append(resources, RuntimeSkillResource{Path: node.Path, Name: node.Name, Type: node.Type, Size: node.Size, MediaType: node.MediaType, PreviewKind: string(node.PreviewKind)})
		}
		return RuntimeSkillResourceListResult{SkillID: summary.SkillID, Resources: resources, Total: len(resources)}, nil
	}
	if err := s.validateLegacyPackage(ctx, definition); err != nil {
		return RuntimeSkillResourceListResult{}, err
	}
	tree, err := s.store.BuildFileTree(ctx, agentcore.SkillID(summary.SkillID))
	if err != nil {
		return RuntimeSkillResourceListResult{}, err
	}
	var resources []RuntimeSkillResource
	for _, root := range tree.Roots {
		resources = appendRuntimeSkillResources(resources, root.Children)
	}
	sort.Slice(resources, func(i, j int) bool { return resources[i].Path < resources[j].Path })
	return RuntimeSkillResourceListResult{SkillID: summary.SkillID, Resources: resources, Total: len(resources)}, nil
}

func (s *RuntimeToolService) LoadResource(ctx context.Context, req RuntimeSkillRequest) (RuntimeSkillResourceLoadResult, error) {
	if err := ctx.Err(); err != nil {
		return RuntimeSkillResourceLoadResult{}, err
	}
	summary, definition, err := s.resolveBoundSkill(ctx, req)
	if err != nil {
		return RuntimeSkillResourceLoadResult{}, err
	}
	if s == nil || s.store == nil {
		return RuntimeSkillResourceLoadResult{}, fmt.Errorf("%w: skill package store is not configured", ErrHydration)
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		return RuntimeSkillResourceLoadResult{}, fmt.Errorf("%w: resource path is required", ErrHydration)
	}
	if definition.PackageSnapshot != nil {
		normalized, err := validateResourceFilePath(path)
		if err != nil {
			return RuntimeSkillResourceLoadResult{}, err
		}
		raw, err := s.store.ReadSnapshotFile(ctx, definition, normalized)
		if err != nil {
			return RuntimeSkillResourceLoadResult{}, err
		}
		node := fileNodeForPath(normalized, int64(len(raw)), s.store.limits.MaxPreviewBytes)
		content := ""
		if node.PreviewKind == PreviewKindMarkdown || node.PreviewKind == PreviewKindText || node.PreviewKind == PreviewKindJSON {
			content = string(raw)
		}
		return RuntimeSkillResourceLoadResult{SkillID: summary.SkillID, Path: node.Path, Name: node.Name, Size: node.Size, MediaType: node.MediaType, PreviewKind: string(node.PreviewKind), Content: content}, nil
	}
	if err := s.validateLegacyPackage(ctx, definition); err != nil {
		return RuntimeSkillResourceLoadResult{}, err
	}
	node, raw, err := s.store.ReadFile(ctx, agentcore.SkillID(summary.SkillID), path)
	if err != nil {
		return RuntimeSkillResourceLoadResult{}, err
	}
	return RuntimeSkillResourceLoadResult{
		SkillID:     summary.SkillID,
		Path:        node.Path,
		Name:        node.Name,
		Size:        node.Size,
		MediaType:   node.MediaType,
		PreviewKind: string(node.PreviewKind),
		Content:     string(raw),
	}, nil
}

func (s *RuntimeToolService) resolveBoundSkill(ctx context.Context, req RuntimeSkillRequest) (RuntimeSkillSummary, SkillDefinition, error) {
	id := strings.TrimSpace(req.SkillID)
	if id == "" {
		return RuntimeSkillSummary{}, SkillDefinition{}, fmt.Errorf("%w: skill_id is required", ErrNotFound)
	}
	bindings := s.boundSkillSummaries(ctx, req.SkillBindings)
	var matches []RuntimeSkillSummary
	for _, summary := range bindings {
		if summary.SkillID == id {
			matches = []RuntimeSkillSummary{summary}
			break
		}
	}
	if len(matches) == 0 {
		for _, summary := range bindings {
			if runtimeSkillLookupMatches(summary, id) {
				matches = append(matches, summary)
			}
		}
	}
	if len(matches) > 1 {
		return RuntimeSkillSummary{}, SkillDefinition{}, fmt.Errorf("%w: skill name %s is ambiguous; use the bound skill_id from list_skills", ErrNotFound, id)
	}
	for _, summary := range matches {
		var binding agentcore.RuntimeSkillBinding
		for _, candidate := range req.SkillBindings {
			if strings.TrimSpace(candidate.SkillID) == summary.SkillID {
				binding = candidate
				break
			}
		}
		definition, err := s.boundDefinition(ctx, binding)
		if err != nil {
			return RuntimeSkillSummary{}, SkillDefinition{}, err
		}
		summary.Version = definition.Version
		if definition.PackageSnapshot != nil {
			summary.PackageDigest = definition.PackageSnapshot.Digest
		}
		if summary.Name == "" {
			summary.Name = definition.Name
		}
		if summary.Summary == "" {
			summary.Summary = definition.Description
		}
		if summary.Version == "" {
			summary.Version = definition.Version
		}
		return summary, definition, nil
	}
	return RuntimeSkillSummary{}, SkillDefinition{}, fmt.Errorf("%w: skill %s is not bound to this Agent", ErrNotFound, id)
}

// Pi can expose either the human skill name or the stable staging directory
// name when it asks Orrery to hydrate a skill. Resolve both forms only against
// the bindings already granted to this Agent.
func runtimeSkillLookupMatches(summary RuntimeSkillSummary, requested string) bool {
	if strings.EqualFold(summary.Alias, requested) {
		return true
	}
	name := strings.ToLower(requested)
	// stabilizePiRPCSkills historically exposed names such as
	// 000-web-research-<digest>. Keep those sessions loadable while new
	// sessions use the canonical leaf directory name.
	last := strings.LastIndexByte(name, '-')
	if len(name) > 4 && name[3] == '-' && allASCIIDigits(name[:3]) && last > 4 && len(name[last+1:]) == 16 && allASCIIHex(name[last+1:]) {
		name = name[4:last]
	}
	projected := summary.Alias
	if projected == "" {
		projected = summary.SkillID
	}
	return name == runtimeSkillLookupSlug(projected) ||
		name == runtimeSkillLookupSlug(summary.SkillID) ||
		(summary.Name != "" && name == runtimeSkillLookupSlug(summary.Name))
}

func runtimeSkillLookupSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	separator := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			separator = false
			continue
		}
		if b.Len() > 0 && !separator {
			b.WriteByte('-')
			separator = true
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		return "orrery-skill"
	}
	if len(name) > 64 {
		name = strings.Trim(name[:64], "-")
	}
	return name
}

func allASCIIDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func allASCIIHex(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

func (s *RuntimeToolService) boundSkillSummaries(ctx context.Context, bindings []agentcore.RuntimeSkillBinding) []RuntimeSkillSummary {
	out := make([]RuntimeSkillSummary, 0, len(bindings))
	seen := map[string]bool{}
	for _, binding := range bindings {
		id := strings.TrimSpace(binding.SkillID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		item := RuntimeSkillSummary{
			SkillID:       id,
			Alias:         strings.TrimSpace(binding.Alias),
			Version:       strings.TrimSpace(binding.Version),
			Summary:       strings.TrimSpace(binding.Summary),
			PackageDigest: binding.PackageDigest,
		}
		if definition, err := s.boundDefinition(ctx, binding); err == nil {
			item.Name = strings.TrimSpace(definition.Name)
			if item.Summary == "" {
				item.Summary = strings.TrimSpace(definition.Description)
			}
			if item.Version == "" {
				item.Version = strings.TrimSpace(definition.Version)
			}
			if definition.PackageSnapshot != nil {
				item.PackageDigest = definition.PackageSnapshot.Digest
			}
		}
		out = append(out, item)
	}
	return out
}

func (s *RuntimeToolService) boundDefinition(ctx context.Context, binding agentcore.RuntimeSkillBinding) (SkillDefinition, error) {
	definition, err := s.definition(ctx, agentcore.SkillID(binding.SkillID))
	if err != nil {
		return SkillDefinition{}, err
	}
	if version := strings.TrimSpace(binding.Version); version != "" {
		if registry, ok := s.registry.(VersionedRegistry); ok {
			definition, err = registry.GetVersion(ctx, agentcore.SkillID(binding.SkillID), version)
			if err != nil {
				return SkillDefinition{}, err
			}
		} else if definition.Version != version {
			return SkillDefinition{}, fmt.Errorf("%w: bound skill version unavailable", ErrVersionMismatch)
		}
	}
	if !definition.Enabled {
		return SkillDefinition{}, ErrDisabled
	}
	if binding.PackageDigest != "" && (definition.PackageSnapshot == nil || binding.PackageDigest != definition.PackageSnapshot.Digest) {
		return SkillDefinition{}, fmt.Errorf("%w: bound skill package changed", ErrVersionMismatch)
	}
	return definition, nil
}

// Legacy loading remains available only for the active definition and matching
// embedded markdown. Candidates and frozen versions never borrow its resources.
func (s *RuntimeToolService) validateLegacyPackage(ctx context.Context, definition SkillDefinition) error {
	active, err := s.definition(ctx, definition.ID)
	if err != nil {
		return err
	}
	if DefinitionHash(active) != DefinitionHash(definition) {
		return fmt.Errorf("%w: historical package needs an immutable snapshot", ErrHydration)
	}
	dir, err := s.store.SkillDirectory(definition.ID)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return fmt.Errorf("%w: legacy package unavailable", ErrHydration)
	}
	for _, asset := range definition.PromptAssets {
		if (asset.Path == "SKILL.md" || asset.Path == "") && asset.Content != "" {
			if asset.Content != string(raw) {
				return fmt.Errorf("%w: legacy package content changed", ErrHydration)
			}
			return nil
		}
	}
	return fmt.Errorf("%w: legacy package has no bound content", ErrHydration)
}

func (s *RuntimeToolService) definition(ctx context.Context, id agentcore.SkillID) (SkillDefinition, error) {
	if s == nil || s.registry == nil {
		return SkillDefinition{}, fmt.Errorf("%w: skill registry is not configured", ErrNotFound)
	}
	definition, err := s.registry.Get(ctx, id)
	if err != nil {
		return SkillDefinition{}, err
	}
	if !definition.Enabled {
		return SkillDefinition{}, fmt.Errorf("%w: %s", ErrDisabled, id)
	}
	return definition, nil
}

func runtimeSkillMatches(item RuntimeSkillSummary, query string) bool {
	haystack := strings.ToLower(strings.Join([]string{item.SkillID, item.Alias, item.Version, item.Name, item.Summary}, " "))
	return strings.Contains(haystack, query)
}

func appendRuntimeSkillResources(out []RuntimeSkillResource, nodes []FileNode) []RuntimeSkillResource {
	for _, node := range nodes {
		if node.Type == "file" {
			out = append(out, RuntimeSkillResource{
				Path:        node.Path,
				Name:        node.Name,
				Type:        node.Type,
				Size:        node.Size,
				MediaType:   node.MediaType,
				PreviewKind: string(node.PreviewKind),
			})
		}
		out = appendRuntimeSkillResources(out, node.Children)
	}
	return out
}

func IsRuntimeToolServiceReadError(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, ErrDisabled) || errors.Is(err, ErrHydration)
}
