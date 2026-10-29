package main

import (
	"context"

	agentassembly "orrery_v2/internal/agent/assembly"
	agentcore "orrery_v2/internal/agent/core"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

type productionAgentResourcePort struct {
	profiles *agentcore.PlatformProfileRepository
	tools    toolres.Registry
	skills   skillres.Registry
}

func (p productionAgentResourcePort) GetAgentResources(ctx context.Context, agentIDs []string) (map[string][]missioncontrol.ResourceRefDTO, error) {
	if p.profiles == nil {
		return nil, missioncontrol.ErrServiceDependency
	}
	binder := agentassembly.NewEffectiveResourceBinder(agentassembly.EffectiveResourceBinderOptions{
		ToolResolver:  toolres.NewResolver(p.tools, toolres.ResolverOptions{}),
		SkillResolver: skillres.NewResolver(p.skills, skillres.ResolverOptions{}),
	})
	out := map[string][]missioncontrol.ResourceRefDTO{}
	for _, agentID := range agentIDs {
		profile, err := p.profiles.GetProfile(ctx, agentcore.AgentProfileID(agentID))
		if err != nil {
			continue
		}
		binding, err := binder.Bind(ctx, agentassembly.EffectiveResourceBindRequest{
			Profile: profile,
			AgentID: agentcore.AgentID(agentID),
		})
		if err != nil {
			continue
		}
		out[agentID] = p.resourceRefs(ctx, binding)
	}
	return out, nil
}

func (p productionAgentResourcePort) resourceRefs(ctx context.Context, binding agentassembly.EffectiveResourceBinding) []missioncontrol.ResourceRefDTO {
	refs := make([]missioncontrol.ResourceRefDTO, 0, len(binding.Skills)+len(binding.Tools))
	for _, skill := range binding.Skills {
		refs = append(refs, missioncontrol.ResourceRefDTO{
			ResourceType: "skill",
			ResourceID:   skill.SkillID,
			Alias:        firstNonBlank(skill.Alias, skill.Name),
			Source:       "profile",
		})
	}
	for _, tool := range binding.Tools {
		refs = append(refs, missioncontrol.ResourceRefDTO{
			ResourceType: p.resourceType(ctx, tool.ToolID),
			ResourceID:   string(tool.ToolID),
			Alias:        firstNonBlank(tool.Alias, tool.Name),
			Source:       "effective_tool",
		})
	}
	return refs
}

func (p productionAgentResourcePort) resourceType(ctx context.Context, toolID agentcore.ToolID) string {
	if p.tools == nil {
		return "tool"
	}
	definition, err := p.tools.Get(ctx, toolID)
	if err != nil {
		return "tool"
	}
	if definition.Kind == toolres.KindMCPServer {
		return "mcp_server"
	}
	return "tool"
}
