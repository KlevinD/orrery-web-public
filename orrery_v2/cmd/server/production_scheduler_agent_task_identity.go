package main

import (
	"context"
	"fmt"
	"strings"

	"orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
)

func (p productionSchedulerAgentTaskPort) bindScheduledAgentTaskIdentity(ctx context.Context, req schedulerapp.AgentTaskRequest, missionID string) error {
	if p.runs == nil {
		return nil
	}
	taskID := scheduledMissionTaskID(req.TenantID, req.RunID)
	responsibilityID := responsibility.AssignmentID(req.TenantID, responsibility.WorkKindMissionTask, taskID)
	_, err := p.runs.MutateRun(ctx, req.RunID, func(run *schedulerapp.ScheduledRun) error {
		// Responsibility evaluation materializes work through the same port but
		// records its own evaluation outcome rather than an Agent occurrence.
		if run.TargetMode == schedulerapp.TargetModeResponsibilityEvaluation && run.ScheduledTaskID == req.ScheduledTaskID && run.TenantID == req.TenantID {
			return nil
		}
		if run.ScheduledTaskID != req.ScheduledTaskID || run.TargetMode != schedulerapp.TargetModeAgentTask ||
			(strings.TrimSpace(run.TenantID) != "" && run.TenantID != req.TenantID) ||
			run.Status == schedulerapp.RunStatusCompleted || run.Status == schedulerapp.RunStatusSkipped {
			return schedulerapp.ErrRevisionConflict
		}
		fields := [][2]string{{run.MissionID, missionID}, {run.TaskID, taskID}, {run.AgentID, req.AgentID}, {run.ResponsibilityID, responsibilityID}}
		for _, field := range fields {
			if strings.TrimSpace(field[0]) != "" && field[0] != field[1] {
				return fmt.Errorf("%w: scheduled occurrence already has another identity", responsibility.ErrConflict)
			}
		}
		if strings.TrimSpace(run.RuntimeRunID) != "" && (run.MissionID == "" || run.TaskID == "" || run.ResponsibilityID == "") {
			return fmt.Errorf("%w: running occurrence is missing its original identity", responsibility.ErrConflict)
		}
		run.MissionID, run.TaskID, run.AgentID, run.ResponsibilityID = missionID, taskID, req.AgentID, responsibilityID
		return nil
	})
	return err
}
