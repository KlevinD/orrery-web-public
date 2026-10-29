package resourceadmin

import (
	"context"

	agentcore "orrery_v2/internal/agent/core"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

func (s *QueryService) buildResourceRelationshipDetails(ctx context.Context, items []resourceListItem, skills []skillres.SkillDefinition, includeUsage, includeBindings bool) ([]ResourceUsageDTO, []ResourceBindingDTO, error) {
	resourceTypes := make(map[string]string, len(items))
	orderedIDs := make([]string, 0, len(items))
	for _, item := range items {
		if item.tool.ID != "" {
			resourceID := string(item.tool.ID)
			resourceType := "tool"
			if item.tool.Kind == toolres.KindMCPServer {
				resourceType = "mcp_server"
			}
			resourceTypes[resourceID] = resourceType
			orderedIDs = append(orderedIDs, resourceID)
			continue
		}
		resourceID := string(item.skill.ID)
		resourceTypes[resourceID] = "skill"
		orderedIDs = append(orderedIDs, resourceID)
	}

	usageByID := make(map[string]*ResourceUsageDTO, len(resourceTypes))
	for resourceID, resourceType := range resourceTypes {
		usageByID[resourceID] = &ResourceUsageDTO{ResourceID: resourceID, ResourceType: resourceType}
	}
	bindings, err := s.collectAgentResourceRelationships(ctx, resourceTypes, usageByID, includeUsage, includeBindings)
	if err != nil {
		return nil, nil, err
	}
	bindings = append(bindings, collectSkillToolRelationships(skills, resourceTypes, usageByID, includeUsage, includeBindings)...)
	if !includeUsage {
		return nil, bindings, nil
	}
	return orderedResourceUsage(orderedIDs, usageByID), bindings, nil
}

func (s *QueryService) collectAgentResourceRelationships(ctx context.Context, resourceTypes map[string]string, usageByID map[string]*ResourceUsageDTO, includeUsage, includeBindings bool) ([]ResourceBindingDTO, error) {
	if s.agents == nil {
		return nil, nil
	}
	page, err := s.agents.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{IncludeAI: true, IncludeHumans: true})
	if err != nil {
		return nil, err
	}
	var bindings []ResourceBindingDTO
	for _, entry := range page.Entries {
		agentID := resourceAgentID(entry)
		profileAuthorizations := s.profileMCPAuthorizationsForEntry(ctx, entry)
		for _, ref := range entry.ToolRefs {
			resourceID := string(ref.ToolID)
			resourceType, ok := resourceTypes[resourceID]
			if !ok {
				continue
			}
			if includeUsage {
				usageByID[resourceID].UsedByAgentIDs = appendUnique(usageByID[resourceID].UsedByAgentIDs, agentID)
			}
			if includeBindings {
				authorization, hasAuthorization := findMCPAuthorization(profileAuthorizations, ref.ToolID)
				authRequired, defaultAKConfigured, authHeaderName := s.mcpBindingMetadata(ctx, ref.ToolID)
				if authorization.HeaderName != "" {
					authHeaderName = authorization.HeaderName
				}
				bindings = append(bindings, ResourceBindingDTO{
					ResourceID:          resourceID,
					ResourceType:        resourceType,
					BindingType:         "agent_tool_ref",
					BoundToType:         "agent",
					BoundToID:           agentID,
					BoundToDisplayName:  entry.DisplayName,
					Alias:               ref.Alias,
					VersionConstraint:   ref.VersionConstraint,
					PermissionScope:     ref.PermissionScope,
					BindingPolicy:       ref.BindingPolicy,
					MCPAuthRequired:     authRequired,
					DefaultAKConfigured: defaultAKConfigured,
					AgentAKConfigured:   hasAuthorization,
					AuthHeaderName:      authHeaderName,
					AgentAKMasked:       maskCredentialValue(authorization.CredentialValue),
				})
			}
		}
		for _, ref := range entry.SkillRefs {
			resourceID := string(ref.SkillID)
			resourceType, ok := resourceTypes[resourceID]
			if !ok {
				continue
			}
			if includeUsage {
				usageByID[resourceID].UsedByAgentIDs = appendUnique(usageByID[resourceID].UsedByAgentIDs, agentID)
			}
			if includeBindings {
				bindings = append(bindings, ResourceBindingDTO{
					ResourceID:         resourceID,
					ResourceType:       resourceType,
					BindingType:        "agent_skill_ref",
					BoundToType:        "agent",
					BoundToID:          agentID,
					BoundToDisplayName: entry.DisplayName,
					Alias:              ref.Alias,
					VersionConstraint:  ref.VersionConstraint,
				})
			}
		}
	}
	return bindings, nil
}

func (s *QueryService) mcpBindingMetadata(ctx context.Context, toolID agentcore.ToolID) (bool, bool, string) {
	if s == nil || s.tools == nil {
		return false, false, ""
	}
	definition, err := s.tools.Get(ctx, toolID)
	if err != nil || definition.Kind != toolres.KindMCPServer || definition.MCP == nil {
		return false, false, ""
	}
	headerName := firstNonEmpty(definition.MCP.Auth.HeaderName, "Authorization")
	authRequired := definition.MCP.Auth.Required || definition.MCP.Availability.Status == toolres.MCPAvailabilityRequiresAuth
	return authRequired, mcpDefaultAKConfigured(definition, headerName), headerName
}

func (s *QueryService) profileMCPAuthorizationsForEntry(ctx context.Context, entry agentcore.AgentDirectoryEntry) []agentcore.MCPAuthorization {
	if s == nil || s.agents == nil || entry.AgentProfileID.IsZero() {
		return nil
	}
	lookup, ok := s.agents.(AgentProfileLookupPort)
	if !ok {
		return nil
	}
	profile, err := lookup.GetProfile(ctx, entry.AgentProfileID)
	if err != nil || profile.ID.IsZero() {
		return nil
	}
	return profile.MCPAuthorizations
}

func collectSkillToolRelationships(skills []skillres.SkillDefinition, resourceTypes map[string]string, usageByID map[string]*ResourceUsageDTO, includeUsage, includeBindings bool) []ResourceBindingDTO {
	var bindings []ResourceBindingDTO
	for _, skill := range skills {
		skillID := string(skill.ID)
		for _, ref := range skill.RequiredTools {
			resourceID := string(ref.ToolID)
			resourceType, ok := resourceTypes[resourceID]
			if !ok {
				continue
			}
			if includeUsage {
				usageByID[resourceID].RequiredBySkillIDs = appendUnique(usageByID[resourceID].RequiredBySkillIDs, skillID)
			}
			if includeBindings {
				bindings = append(bindings, ResourceBindingDTO{
					ResourceID:        resourceID,
					ResourceType:      resourceType,
					BindingType:       "skill_required_tool",
					BoundToType:       "skill",
					BoundToID:         skillID,
					Alias:             ref.Alias,
					VersionConstraint: ref.VersionConstraint,
				})
			}
		}
	}
	return bindings
}

func orderedResourceUsage(orderedIDs []string, usageByID map[string]*ResourceUsageDTO) []ResourceUsageDTO {
	usage := make([]ResourceUsageDTO, 0, len(orderedIDs))
	for _, resourceID := range orderedIDs {
		item := usageByID[resourceID]
		item.UsageCount = len(item.UsedByAgentIDs) + len(item.RequiredBySkillIDs)
		usage = append(usage, *item)
	}
	return usage
}
