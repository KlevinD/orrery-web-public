package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	missionprojection "orrery_v2/internal/mission/projection"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/execution"
	"orrery_v2/internal/platform/logging"
	"orrery_v2/internal/platform/uow"
)

type productionRuntimeLifecycleRepairCatalog interface {
	ListMissionCatalog(context.Context, string, int) ([]missionprojection.ReadModel, bool, error)
}

type productionRuntimeLifecycleRepairTasks interface {
	LatestSubmission(context.Context, uow.Tx, missiontask.TaskID) (missiontask.TaskSubmission, bool, error)
}

type productionRuntimeLifecycleRepairPublisher interface {
	PublishRuntimeLifecycle(context.Context, runtimedaemon.RuntimeLifecycleEvent) error
	PublishRuntimeFailure(context.Context, runtimedaemon.RuntimeFailureEvent) error
}

type productionRuntimeLifecycleRepairExecution interface {
	GetStatus(context.Context, string) (execution.JobStatus, bool, error)
}

type productionRuntimeLifecycleRepair struct {
	catalog        productionRuntimeLifecycleRepairCatalog
	tasks          productionRuntimeLifecycleRepairTasks
	unit           *uow.UnitOfWork
	publisher      productionRuntimeLifecycleRepairPublisher
	execution      productionRuntimeLifecycleRepairExecution
	notifier       agentcore.Notifier
	responsibility responsibilityRunLifecycleTracker
	logger         logging.Logger

	mu     sync.Mutex
	cursor string
}

func (r *productionRuntimeLifecycleRepair) Reconcile(ctx context.Context, limit int) (int, error) {
	if r == nil || r.catalog == nil || r.tasks == nil || r.unit == nil || r.publisher == nil {
		return 0, missioncontrol.ErrServiceDependency
	}
	if limit <= 0 {
		limit = 100
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	models, hasMore, err := r.catalog.ListMissionCatalog(ctx, r.cursor, limit)
	if err != nil {
		return 0, err
	}
	if hasMore && len(models) == 0 {
		return 0, missioncontrol.ErrServiceDependency
	}
	nextCursor := r.cursor
	repaired := 0
	for _, model := range models {
		tenantID := strings.TrimSpace(model.TenantID)
		if tenantID == "" {
			tenantID = strings.TrimSpace(model.Mission.TenantID)
		}
		if tenantID == "" {
			tenantID = "global"
		}
		nextCursor = tenantID + "/" + model.Mission.MissionID.String()
		count, repairErr := r.repairModel(ctx, tenantID, model)
		if repairErr != nil {
			return repaired, repairErr
		}
		repaired += count
	}
	if hasMore {
		r.cursor = nextCursor
	} else {
		r.cursor = ""
	}
	return repaired, nil
}

func (r *productionRuntimeLifecycleRepair) repairModel(ctx context.Context, tenantID string, model missionprojection.ReadModel) (int, error) {
	if model.Mission.MissionID.IsZero() {
		return 0, missioncontrol.ErrInvalidRequest
	}
	if model.Mission.Status != missionprojection.MissionStatusActive {
		return 0, nil
	}
	repaired := 0
	for sessionIndex, session := range model.RuntimeSessions {
		if (session.Status != missionprojection.RuntimeSessionQueued && session.Status != missionprojection.RuntimeSessionRunning) || session.RunID == "" || session.TaskID.IsZero() || session.AgentID.IsZero() {
			continue
		}
		if runtimeSessionSuperseded(model.RuntimeSessions, sessionIndex) {
			continue
		}
		var submission missiontask.TaskSubmission
		var found bool
		err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
			var readErr error
			submission, found, readErr = r.tasks.LatestSubmission(ctx, tx, missiontask.TaskID(session.TaskID))
			return readErr
		})
		if err != nil {
			return repaired, runtimeLifecycleRepairError(model, session, err)
		}
		if !found || submission.RunID != session.RunID || submission.AgentID != session.AgentID {
			if !runtimeLifecycleRepairableTask(model, session.TaskID) {
				continue
			}
			queueRepaired, queueErr := r.repairTerminalQueueState(ctx, tenantID, model, session)
			if queueErr != nil {
				return repaired, runtimeLifecycleRepairError(model, session, queueErr)
			}
			if queueRepaired {
				repaired++
			}
			continue
		}
		message := strings.TrimSpace(submission.Summary.Text)
		if message == "" {
			message = "agent run completion recovered from durable task submission"
		}
		if r.notifier != nil {
			input := agentcore.AgentInput{
				TenantID: tenantID, RunID: submission.RunID,
				Task: agentcore.TaskInput{MissionID: model.Mission.MissionID.String(), ID: session.TaskID.String()},
				ContextPack: agentcore.ContextPack{
					ConversationID:    model.Discussion.ConversationID,
					DiscussionGroupID: agentcore.DiscussionGroupID(model.Discussion.DiscussionGroupID),
				},
			}
			result := agentcore.AgentResult{
				MissionID: model.Mission.MissionID, TaskID: session.TaskID, AgentID: submission.AgentID, RunID: submission.RunID,
				Summary: submission.Summary,
			}
			if err := r.notifier.Notify(ctx, input, result); err != nil {
				return repaired, runtimeLifecycleRepairError(model, session, err)
			}
		}
		if r.responsibility != nil {
			if err := r.responsibility.Completed(ctx, tenantID, session.TaskID.String(), session.RunID.String()); err != nil {
				return repaired, runtimeLifecycleRepairError(model, session, err)
			}
		}
		if err := r.publisher.PublishRuntimeLifecycle(ctx, runtimedaemon.RuntimeLifecycleEvent{
			Kind: runtimedaemon.RuntimeLifecycleCompleted, TenantID: tenantID,
			MissionID: model.Mission.MissionID, TaskID: missioncore.TaskID(session.TaskID), AgentID: session.AgentID, RunID: session.RunID,
			RuntimeKind: session.RuntimeKind, QueueID: session.QueueID, Status: runtimedaemon.RunStatusCompleted.String(), WorkerID: session.WorkerID, Message: message,
		}); err != nil {
			return repaired, runtimeLifecycleRepairError(model, session, err)
		}
		repaired++
	}
	return repaired, nil
}

func runtimeSessionSuperseded(sessions []missionprojection.RuntimeSessionView, currentIndex int) bool {
	if currentIndex < 0 || currentIndex >= len(sessions) {
		return false
	}
	current := sessions[currentIndex]
	for candidateIndex, candidate := range sessions {
		if candidateIndex == currentIndex || candidate.TaskID != current.TaskID {
			continue
		}
		if candidate.QueuedAt.After(current.QueuedAt) ||
			(candidate.QueuedAt.Equal(current.QueuedAt) && candidate.UpdatedAt.After(current.UpdatedAt)) ||
			(candidate.QueuedAt.Equal(current.QueuedAt) && candidate.UpdatedAt.Equal(current.UpdatedAt) && candidateIndex > currentIndex) {
			return true
		}
	}
	return false
}

func runtimeLifecycleRepairableTask(model missionprojection.ReadModel, taskID missioncore.TaskID) bool {
	for _, task := range model.TaskBoard.Tasks {
		if task.TaskID != taskID {
			continue
		}
		switch task.Status {
		case missionprojection.TaskStatusAssigned, missionprojection.TaskStatusAccepted, missionprojection.TaskStatusStarted:
			return true
		default:
			return false
		}
	}
	return false
}

func runtimeLifecycleRepairError(model missionprojection.ReadModel, session missionprojection.RuntimeSessionView, err error) error {
	return fmt.Errorf("repair runtime lifecycle mission_id=%s task_id=%s run_id=%s: %w",
		model.Mission.MissionID.String(), session.TaskID.String(), session.RunID.String(), err)
}

func (r *productionRuntimeLifecycleRepair) repairTerminalQueueState(ctx context.Context, tenantID string, model missionprojection.ReadModel, session missionprojection.RuntimeSessionView) (bool, error) {
	if r.execution == nil {
		return false, nil
	}
	status, found, err := r.execution.GetStatus(ctx, session.RunID.String())
	if err != nil || !found {
		return false, err
	}
	kind := runtimedaemon.RuntimeLifecycleKind("")
	code := ""
	switch status.Status {
	case execution.StatusFailed, execution.StatusDeadLetter:
		kind = runtimedaemon.RuntimeLifecycleFailed
		code = "agent_queue_failed"
	case execution.StatusCancelled:
		kind = runtimedaemon.RuntimeLifecycleCancelled
		code = "agent_queue_cancelled"
	default:
		return false, nil
	}
	message := firstNonBlank(strings.TrimSpace(status.LastError), strings.TrimSpace(status.FailureReason), "agent execution queue reached a terminal state")
	if err := r.publisher.PublishRuntimeFailure(ctx, runtimedaemon.RuntimeFailureEvent{
		Kind: kind, TenantID: tenantID, MissionID: model.Mission.MissionID,
		TaskID: missioncore.TaskID(session.TaskID), AgentID: session.AgentID, RunID: session.RunID,
		RuntimeKind: session.RuntimeKind, QueueID: session.QueueID, WorkerID: status.WorkerID,
		Code: code, Message: message,
		Metadata: map[string]string{"queue_status": string(status.Status), "failure_reason": strings.TrimSpace(status.FailureReason)},
	}); err != nil {
		return false, err
	}
	if r.responsibility != nil {
		if err := r.responsibility.Failed(ctx, tenantID, session.TaskID.String(), session.RunID.String(), kind, errors.New(message)); err != nil && r.logger != nil {
			r.logger.Warn(ctx, "repair terminal queue responsibility outcome",
				logging.String("run_id", session.RunID.String()),
				logging.String("task_id", session.TaskID.String()),
				logging.Any("error", err),
			)
		}
	}
	return true, nil
}
