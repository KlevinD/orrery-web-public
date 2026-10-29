package missioncontrol

import (
	"context"

	"orrery_v2/internal/mission/projection"
)

type AgentResourcePort interface {
	GetAgentResources(ctx context.Context, agentIDs []string) (map[string][]ResourceRefDTO, error)
}

func applyAgentResources(ctx context.Context, view *MissionOpsViewDTO, port AgentResourcePort) error {
	if view == nil || port == nil || len(view.AgentLanes) == 0 {
		return nil
	}
	agentIDs := make([]string, 0, len(view.AgentLanes))
	seen := map[string]struct{}{}
	for _, lane := range view.AgentLanes {
		if lane.AgentID == "" {
			continue
		}
		if _, ok := seen[lane.AgentID]; ok {
			continue
		}
		seen[lane.AgentID] = struct{}{}
		agentIDs = append(agentIDs, lane.AgentID)
	}
	if len(agentIDs) == 0 {
		return nil
	}
	resources, err := port.GetAgentResources(ctx, agentIDs)
	if err != nil {
		return err
	}
	for i := range view.AgentLanes {
		if refs := resources[view.AgentLanes[i].AgentID]; len(refs) > 0 {
			view.AgentLanes[i].ResourceBindings = cloneResourceRefs(refs)
		}
	}
	return nil
}

func cloneResourceRefs(refs []ResourceRefDTO) []ResourceRefDTO {
	if len(refs) == 0 {
		return nil
	}
	out := make([]ResourceRefDTO, 0, len(refs))
	for _, ref := range refs {
		if ref.ResourceID == "" || ref.ResourceType == "" {
			continue
		}
		out = append(out, ref)
	}
	return out
}

func stableAgentIDsFromModel(model projection.ReadModel) []string {
	ids := make([]string, 0, len(model.Team.Members))
	seen := map[string]struct{}{}
	for _, member := range model.Team.Members {
		agentID := member.AgentID.String()
		if agentID == "" {
			agentID = firstString(member.AgentProfileID.String(), member.AgentInstanceID.String())
		}
		if agentID == "" {
			continue
		}
		if _, ok := seen[agentID]; ok {
			continue
		}
		seen[agentID] = struct{}{}
		ids = append(ids, agentID)
	}
	return ids
}
