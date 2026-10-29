package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
)

func (r productionSchedulerRunReconciler) expireIdleScheduledOccurrence(ctx context.Context, run schedulerapp.ScheduledRun, assignment responsibility.AssignmentRecord) (bool, error) {
	if run.CreatedAt.IsZero() || assignment.Paused ||
		(assignment.State != responsibility.AssignmentStateOffered && assignment.State != responsibility.AssignmentStateActive) {
		return false, nil
	}
	// Only the responsibility created for this exact occurrence may be expired.
	if assignment.SourceKind != schedulerAgentTaskRunSource || assignment.SourceID != run.ScheduledTaskID || assignment.SourceStepID != run.ScheduledRunID {
		return false, nil
	}
	var task schedulerapp.ScheduledTask
	if run.ConfigurationSnapshot != nil {
		task = *run.ConfigurationSnapshot
	} else {
		var err error
		task, err = r.repo.GetTask(ctx, run.ScheduledTaskID)
		if err != nil {
			return false, err
		}
	}
	if task.Schedule.Type != schedulerapp.ScheduleTypeCron || task.Policy.Concurrency != schedulerapp.ConcurrencySkipIfRunning {
		return false, nil
	}
	next, err := schedulerapp.NextOccurrence(task.Schedule, run.CreatedAt)
	if err != nil || next.IsZero() || next.After(r.now()) {
		return false, err
	}
	attempts, err := r.responsibilities.ListAttempts(ctx, assignment.ID)
	if err != nil {
		return false, err
	}
	for _, attempt := range attempts {
		if attempt.Outcome == "" {
			return false, nil
		}
	}
	reason := "scheduled occurrence expired without an active runtime before the next scheduled occurrence"
	if len(attempts) > 0 {
		latest := attempts[len(attempts)-1]
		// Successful runtime completion may still need business result recovery.
		if latest.Outcome == responsibility.AttemptOutcomeCompleted {
			return false, nil
		}
		if cause := strings.TrimSpace(latest.Error); cause != "" {
			reason += "; last runtime outcome: " + string(latest.Outcome) + "; " + cause
		}
	}
	_, err = r.responsibilities.ExpireIdleScheduledAssignment(ctx, responsibility.AssignmentControl{
		TenantID: assignment.TenantID, AssignmentID: assignment.ID, ActorID: "system:scheduler-reconciler",
		ExpectedVersion: assignment.Version, Reason: reason,
		IdempotencyKey: fmt.Sprintf("expire-idle-scheduled-occurrence:%s:%d", run.ScheduledRunID, assignment.Version),
	}, r.now())
	if errors.Is(err, responsibility.ErrConflict) {
		// A concurrent dispatch or control change wins; re-read on the next sweep.
		return false, nil
	}
	if err != nil {
		return false, err
	}
	tracker := productionSchedulerRunTracker{repo: r.repo, clock: r.clock}
	return true, tracker.AttemptFailed(ctx, run.ScheduledRunID, max(1, run.Attempt), true, errors.New(reason))
}
