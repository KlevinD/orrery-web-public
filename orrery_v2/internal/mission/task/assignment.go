package task

import (
	"fmt"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
)

type TaskAssignment struct {
	TaskID               TaskID
	MissionID            missioncore.MissionID
	AssigneeAgentID      agentcore.AgentInstanceID
	MissionAssignmentRef MissionAssignmentRef
	AssignedBy           agentcore.AgentID
	AssignedAt           time.Time
	Status               TaskStatus
}

type TaskAssignmentOptions struct {
	AssignedBy agentcore.AgentID
	AssignedAt time.Time
}

func NewTaskAssignment(contract TaskContract, opts TaskAssignmentOptions) (TaskAssignment, error) {
	if !contract.Assignee.IsAgentAssignable() {
		return TaskAssignment{}, fmt.Errorf("task assignee must be an agent instance or mission assignment ref")
	}
	assignedAt := opts.AssignedAt
	if assignedAt.IsZero() {
		assignedAt = time.Now().UTC()
	}
	return TaskAssignment{
		TaskID:               contract.ID,
		MissionID:            contract.MissionID,
		AssigneeAgentID:      assigneeAgentInstanceID(contract.Assignee),
		MissionAssignmentRef: assigneeMissionAssignmentRef(contract.Assignee),
		AssignedBy:           opts.AssignedBy,
		AssignedAt:           assignedAt.UTC(),
		Status:               TaskStatusAssigned,
	}, nil
}

func assigneeAgentInstanceID(ref TaskAssigneeRef) agentcore.AgentInstanceID {
	if ref.AgentProfileRef == nil {
		return ""
	}
	return agentcore.AgentInstanceID(ref.AgentProfileRef.ID)
}

func assigneeMissionAssignmentRef(ref TaskAssigneeRef) MissionAssignmentRef {
	if ref.MissionAssignmentRef == nil {
		return MissionAssignmentRef{}
	}
	return *ref.MissionAssignmentRef
}
