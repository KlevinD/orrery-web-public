package task

import (
	"context"
	"fmt"

	agentcore "orrery_v2/internal/agent/core"
)

type MissionAssignmentResolver interface {
	ResolveMissionAssignment(ctx context.Context, ref MissionAssignmentRef) (TaskAgentRef, error)
}

type TaskAgentRef struct {
	ProfileRef  agentcore.AgentProfileRef
	InstanceRef agentcore.AgentInstanceRef
}

func (r TaskAgentRef) IsZero() bool {
	return r.ProfileRef.IsZero() && r.InstanceRef.IsZero()
}

func (r TaskAgentRef) MatchesInstance(ref agentcore.AgentInstanceRef) bool {
	return !r.InstanceRef.IsZero() && r.InstanceRef.ID == ref.ID
}

type StaticMissionAssignmentResolver map[MissionAssignmentID]TaskAgentRef

func (r StaticMissionAssignmentResolver) ResolveMissionAssignment(ctx context.Context, ref MissionAssignmentRef) (TaskAgentRef, error) {
	if agent, ok := r[ref.ID]; ok && !agent.IsZero() {
		return agent, nil
	}
	return TaskAgentRef{}, fmt.Errorf("%w: mission assignment %s", ErrNotFound, ref.ID)
}

func ResolveTaskAssignee(ctx context.Context, assignee TaskAssigneeRef, resolver MissionAssignmentResolver) (TaskAgentRef, error) {
	if assignee.AgentProfileRef != nil && !assignee.AgentProfileRef.IsZero() {
		return TaskAgentRef{
			ProfileRef:  *assignee.AgentProfileRef,
			InstanceRef: agentcore.AgentInstanceRef{ID: agentcore.AgentInstanceID(assignee.AgentProfileRef.ID)},
		}, nil
	}
	if assignee.MissionAssignmentRef != nil && assignee.MissionAssignmentRef.ID != "" {
		if resolver == nil {
			return TaskAgentRef{}, fmt.Errorf("mission assignment resolver is required")
		}
		return resolver.ResolveMissionAssignment(ctx, *assignee.MissionAssignmentRef)
	}
	return TaskAgentRef{}, fmt.Errorf("task assignee does not identify an agent")
}
