package team

import (
	"context"
	"fmt"

	agentcore "orrery_v2/internal/agent/core"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/uow"
)

type TaskAssignmentResolver struct {
	unit *uow.UnitOfWork
	repo *PlatformTeamRepository
}

func NewTaskAssignmentResolver(unit *uow.UnitOfWork, repo *PlatformTeamRepository) *TaskAssignmentResolver {
	if repo == nil {
		repo = NewPlatformTeamRepository()
	}
	return &TaskAssignmentResolver{unit: unit, repo: repo}
}

func (r *TaskAssignmentResolver) ResolveMissionAssignment(ctx context.Context, ref missiontask.MissionAssignmentRef) (missiontask.TaskAgentRef, error) {
	if r == nil || r.unit == nil || r.repo == nil {
		return missiontask.TaskAgentRef{}, fmt.Errorf("mission assignment resolver is not configured")
	}
	var out missiontask.TaskAgentRef
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		assignment, err := r.repo.GetAssignment(ctx, tx, MissionAssignmentID(ref.ID))
		if err != nil {
			return err
		}
		if assignment.Status != AssignmentStatusActive {
			return fmt.Errorf("%w: %s", ErrPolicyDenied, CodeAssignmentNotActive)
		}
		out = missiontask.TaskAgentRef{
			ProfileRef:  agentcore.AgentProfileRef{ID: assignment.AgentProfileID},
			InstanceRef: agentcore.AgentInstanceRef{ID: assignment.AgentInstanceID},
		}
		return nil
	})
	return out, err
}
