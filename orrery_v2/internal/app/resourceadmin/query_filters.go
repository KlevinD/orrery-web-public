package resourceadmin

import (
	"context"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

type resourceListItem struct {
	tool  toolres.ToolDefinition
	skill skillres.SkillDefinition
}

func pageWindow(total, rawOffset, rawLimit int) (int, int) {
	offset := rawOffset
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	limit := rawLimit
	if limit <= 0 {
		limit = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return offset, end
}

func requiresAgentDirectory(req ListResourcesRequest) bool {
	return strings.TrimSpace(req.BoundAgentID) != "" || req.IncludeUsage || req.IncludeBindings
}

func (s *QueryService) boundResourceIDsForAgent(ctx context.Context, agentID string) (map[string]struct{}, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, nil
	}
	if s.agents == nil {
		return map[string]struct{}{}, nil
	}
	page, err := s.agents.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{IncludeAI: true, IncludeHumans: true})
	if err != nil {
		return nil, err
	}
	ids := map[string]struct{}{}
	for _, entry := range page.Entries {
		if !resourceAgentMatches(entry, agentID) {
			continue
		}
		for _, ref := range entry.ToolRefs {
			ids[strings.ToLower(string(ref.ToolID))] = struct{}{}
		}
		for _, ref := range entry.SkillRefs {
			ids[strings.ToLower(string(ref.SkillID))] = struct{}{}
		}
	}
	return ids, nil
}

func matchesToolResourceFilter(req ListResourcesRequest, definition toolres.ToolDefinition, boundResourceIDs map[string]struct{}) bool {
	if !matchesResourceFilter(req, string(definition.Kind), string(definition.ID), definition.Name, definition.Description, definition.Tags) {
		return false
	}
	if !matchesResourceID(req.ResourceID, string(definition.ID)) {
		return false
	}
	if !matchesBoundResource(boundResourceIDs, string(definition.ID)) {
		return false
	}
	if strings.TrimSpace(req.Category) != "" && !hasStringFold(definition.Tags, req.Category) {
		return false
	}
	if strings.TrimSpace(req.RiskLevel) != "" && !strings.EqualFold(req.RiskLevel, string(definition.RiskLevel)) {
		return false
	}
	if req.Enabled != nil && definition.Enabled != *req.Enabled {
		return false
	}
	return true
}

func matchesSkillResourceFilter(req ListResourcesRequest, definition skillres.SkillDefinition, boundResourceIDs map[string]struct{}) bool {
	tags := append(append([]string(nil), definition.DomainTags...), definition.TaskTypeTags...)
	if !matchesResourceFilter(req, "skill", string(definition.ID), definition.Name, definition.Description, tags) {
		return false
	}
	if !matchesResourceID(req.ResourceID, string(definition.ID)) {
		return false
	}
	if !matchesBoundResource(boundResourceIDs, string(definition.ID)) {
		return false
	}
	if strings.TrimSpace(req.Category) != "" && !hasStringFold(tags, req.Category) {
		return false
	}
	if strings.TrimSpace(req.RiskLevel) != "" && !strings.EqualFold(req.RiskLevel, string(skillres.NormalizeRiskLevel(definition.RiskLevel))) {
		return false
	}
	if req.Enabled != nil && definition.Enabled != *req.Enabled {
		return false
	}
	if strings.TrimSpace(req.RequiredToolID) != "" && !skillRequiresTool(definition, req.RequiredToolID) {
		return false
	}
	return true
}

func matchesResourceID(requested, resourceID string) bool {
	return strings.TrimSpace(requested) == "" || strings.EqualFold(requested, resourceID)
}

func matchesBoundResource(boundResourceIDs map[string]struct{}, resourceID string) bool {
	if boundResourceIDs == nil {
		return true
	}
	_, ok := boundResourceIDs[strings.ToLower(resourceID)]
	return ok
}

func matchesResourceFilter(req ListResourcesRequest, kind, id, name, description string, tags []string) bool {
	if !matchesKindFilter(req.Kind, kind) {
		return false
	}
	search := strings.ToLower(strings.TrimSpace(req.Search))
	if search == "" {
		return true
	}
	values := []string{id, name, description}
	values = append(values, tags...)
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), search) {
			return true
		}
	}
	return false
}

func matchesKindFilter(requested, kind string) bool {
	requested = strings.TrimSpace(requested)
	if requested == "" || requested == kind {
		return true
	}
	if requested == "mcp" {
		return kind == string(toolres.KindMCPServer)
	}
	if requested == "tool" {
		return kind != "skill" && kind != string(toolres.KindMCPServer)
	}
	return false
}

func skillRequiresTool(definition skillres.SkillDefinition, toolID string) bool {
	for _, ref := range definition.RequiredTools {
		if strings.EqualFold(string(ref.ToolID), toolID) {
			return true
		}
	}
	return false
}

func hasStringFold(values []string, needle string) bool {
	for _, value := range values {
		if strings.EqualFold(value, strings.TrimSpace(needle)) {
			return true
		}
	}
	return false
}

func appendUnique(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
