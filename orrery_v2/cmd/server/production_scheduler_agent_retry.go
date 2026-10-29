package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
)

type scheduledAgentAttemptSource interface {
	GetAssignment(context.Context, string) (responsibility.AssignmentRecord, error)
	ListAttempts(context.Context, string) ([]responsibility.AttemptRecord, error)
}

type schedulerAgentRunStartTracker interface {
	StartAgentRun(context.Context, schedulerapp.ScheduledRun, int) error
}

func (w productionAgentRunWorker) startScheduledAgentRun(ctx context.Context, run runtimedaemon.AgentRun, attempt int) error {
	if tracker, ok := w.scheduler.(schedulerAgentRunStartTracker); ok {
		expected, err := scheduledRunIdentityFromAgentRun(run)
		if err != nil {
			return err
		}
		return tracker.StartAgentRun(ctx, expected, attempt)
	}
	return w.scheduler.Started(ctx, scheduledRunIDFromRun(run), attempt)
}

// A scheduled occurrence survives responsibility retries. Bind its latest
// execution before running or recovering a committed result; completion still
// requires an exact runtime identity match and cannot replace another result.
func (t productionSchedulerRunTracker) StartAgentRun(ctx context.Context, expected schedulerapp.ScheduledRun, queueAttempt int) error {
	if t.repo == nil || t.responsibilities == nil {
		return schedulerapp.ErrServiceDependency
	}
	if err := validateScheduledAgentRunCompletionIdentity(expected, expected); err != nil {
		return err
	}
	assignment, err := t.responsibilities.GetAssignment(ctx, expected.ResponsibilityID)
	if err != nil {
		return err
	}
	if assignment.ID != expected.ResponsibilityID || assignment.TenantID != expected.TenantID ||
		assignment.MissionID != expected.MissionID || assignment.WorkID != expected.TaskID ||
		assignment.AccountableAgent != expected.AgentID || assignment.SourceKind != schedulerAgentTaskRunSource ||
		assignment.SourceID != expected.ScheduledTaskID || assignment.SourceStepID != expected.ScheduledRunID {
		return fmt.Errorf("%w: scheduled retry responsibility identity changed", responsibility.ErrConflict)
	}
	attempts, err := t.responsibilities.ListAttempts(ctx, assignment.ID)
	if err != nil {
		return err
	}
	if len(attempts) == 0 {
		return fmt.Errorf("%w: scheduled retry has no recorded attempt", responsibility.ErrConflict)
	}
	latest := attempts[len(attempts)-1]
	if latest.AgentRunID != strings.TrimSpace(expected.RuntimeRunID) || latest.TenantID != expected.TenantID ||
		latest.ResponsibilityID != assignment.ID || latest.AgentProfileID != expected.AgentID || latest.Attempt < 1 {
		return fmt.Errorf("%w: scheduled retry is not the latest recorded attempt", responsibility.ErrConflict)
	}
	_, err = t.repo.MutateRun(ctx, expected.ScheduledRunID, func(run *schedulerapp.ScheduledRun) error {
		if err := validateScheduledAgentRunCompletionIdentity(*run, expected); err != nil {
			return err
		}
		if strings.TrimSpace(run.RuntimeRunID) == strings.TrimSpace(expected.RuntimeRunID) {
			// Replaying a queue delivery must preserve the durable result and trace.
			return nil
		}
		if assignment.State != responsibility.AssignmentStateActive || latest.Outcome != "" ||
			run.Status == schedulerapp.RunStatusCompleted || run.Status == schedulerapp.RunStatusSkipped {
			return fmt.Errorf("%w: scheduled retry is not active", responsibility.ErrConflict)
		}
		if previousID := strings.TrimSpace(run.RuntimeRunID); previousID != "" {
			previousFound := false
			for _, previous := range attempts {
				if previous.AgentRunID == previousID && previous.Attempt < latest.Attempt &&
					previous.TenantID == expected.TenantID && previous.ResponsibilityID == assignment.ID &&
					previous.AgentProfileID == expected.AgentID && previous.Outcome != "" {
					previousFound = true
					break
				}
			}
			if !previousFound {
				return fmt.Errorf("%w: scheduled retry would replace an unrelated or live execution", responsibility.ErrConflict)
			}
		} else if scheduledRunHasResult(run.Result) {
			return fmt.Errorf("%w: scheduled retry cannot identify the previous result execution", responsibility.ErrConflict)
		}
		run.RuntimeRunID = strings.TrimSpace(expected.RuntimeRunID)
		run.Attempt = max(run.Attempt, latest.Attempt, queueAttempt)
		run.Status = schedulerapp.RunStatusRunning
		run.StartedAt = latest.StartedAt
		run.CompletedAt = time.Time{}
		run.FailureReason = ""
		run.Result = schedulerapp.RunResult{}
		applyScheduledRunTrace(run, "", "")
		run.DeliveryStatus, run.DeliveryError, run.ExternalMessageID = "", "", ""
		return nil
	})
	return err
}
