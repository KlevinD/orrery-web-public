package missioncontrol

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/mission/projection"
	"orrery_v2/internal/mission/storage"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
)

type ImportHistoricalTaskAssignmentCommand struct {
	IdempotencyKey      string
	MissionID           missioncore.MissionID
	TaskID              missioncore.TaskID
	ScheduledTaskID     string
	ScheduledRunID      string
	AssigneeID          string
	ResponsibilityState string
	Reason              string
	OccurredAt          time.Time
}

func (s *LifecycleService) ImportHistoricalTaskAssignment(ctx context.Context, viewer ViewerRef, cmd ImportHistoricalTaskAssignmentCommand) (CommandReceiptDTO, error) {
	if s == nil || s.uow == nil || s.repo == nil || s.taskRepo == nil || s.responsibility == nil {
		return CommandReceiptDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return CommandReceiptDTO{}, err
	}
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	cmd.ScheduledTaskID = strings.TrimSpace(cmd.ScheduledTaskID)
	cmd.ScheduledRunID = strings.TrimSpace(cmd.ScheduledRunID)
	cmd.AssigneeID = strings.TrimSpace(cmd.AssigneeID)
	cmd.ResponsibilityState = strings.TrimSpace(cmd.ResponsibilityState)
	cmd.Reason = strings.TrimSpace(cmd.Reason)
	if viewer.ViewerKind != ViewerKindSystem || cmd.IdempotencyKey == "" || cmd.MissionID.IsZero() || cmd.TaskID.IsZero() || cmd.ScheduledTaskID == "" || cmd.ScheduledRunID == "" || cmd.AssigneeID == "" || cmd.OccurredAt.IsZero() || !validHistoricalResponsibilityState(cmd.ResponsibilityState) {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	historicalResponsibilities, ok := s.responsibility.(TaskResponsibilityHistoricalImportPort)
	if !ok {
		return CommandReceiptDTO{}, ErrServiceDependency
	}
	if err := s.authorizeMissionAction(ctx, viewer, cmd.MissionID, "mission.edit"); err != nil {
		return CommandReceiptDTO{}, err
	}
	model, err := loadMissionProjection(ctx, s.repo, viewer.Tenant(), cmd.MissionID)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	if strings.TrimSpace(model.Mission.OriginKind) != "scheduler" || strings.TrimSpace(model.Mission.OriginID) != cmd.ScheduledTaskID {
		return CommandReceiptDTO{}, ErrProjectionNotFound
	}
	assignee, err := s.resolveHistoricalTaskAssignee(ctx, model, cmd.AssigneeID)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	fingerprint, err := missionTaskCommandFingerprint(struct {
		TaskID              string
		ScheduledTaskID     string
		ScheduledRunID      string
		AssigneeID          string
		ResponsibilityState string
		Reason              string
	}{
		TaskID: cmd.TaskID.String(), ScheduledTaskID: cmd.ScheduledTaskID, ScheduledRunID: cmd.ScheduledRunID,
		AssigneeID: assignee.ProfileID.String(), ResponsibilityState: cmd.ResponsibilityState, Reason: cmd.Reason,
	})
	if err != nil {
		return CommandReceiptDTO{}, err
	}

	var updated missiontask.TaskRecord
	var replaying bool
	err = s.uow.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, storage.TableTaskRecords.LogicalName, missiontask.TaskID(cmd.TaskID).String()); err != nil {
			return err
		}
		record, err := s.taskRepo.GetTask(ctx, tx, missiontask.TaskID(cmd.TaskID))
		if err != nil {
			return err
		}
		if record.Contract.MissionID != cmd.MissionID || strings.TrimSpace(record.Contract.SourceKind) != "scheduler_agent_task" || strings.TrimSpace(record.Contract.SourceID) != cmd.ScheduledTaskID || strings.TrimSpace(record.Contract.SourceStepID) != cmd.ScheduledRunID {
			return fmt.Errorf("%w: historical assignment source does not match the scheduler Task", ErrConflict)
		}
		if record.AssignmentIdempotencyKey == cmd.IdempotencyKey {
			if record.AssignmentRequestFingerprint != "" && record.AssignmentRequestFingerprint != fingerprint {
				return fmt.Errorf("%w: historical assignment idempotency key was reused with a different request", ErrInvalidRequest)
			}
			updated = record
			replaying = true
			return nil
		}
		if err := validateTaskExecutionEnvelopeAssignee(record.Contract.ExecutionEnvelope, assignee); err != nil {
			return fmt.Errorf("%w: historical assignment does not match the task execution envelope", err)
		}
		applyTaskQAPolicy(&record.Contract, taskQAPolicyForAssigneeAndSource(assignee, record.Contract.SourceKind))
		assigned, err := record.Reassign(missiontask.TaskAssigneeRef{AgentProfileRef: &agentcore.AgentProfileRef{ID: assignee.ProfileID}}, missiontask.TransitionOptions{
			ExpectedVersion: record.Version, Reason: cmd.Reason, Now: cmd.OccurredAt,
		})
		if err != nil {
			return err
		}
		assigned.AssignmentIdempotencyKey = cmd.IdempotencyKey
		assigned.AssignmentRequestFingerprint = fingerprint
		if err := s.taskRepo.SaveTask(ctx, tx, assigned); err != nil {
			return err
		}
		if err := historicalResponsibilities.RecordHistoricalTaskAssignment(ctx, tx, HistoricalTaskResponsibilityAssignmentSignal{
			TenantID: viewer.Tenant(), MissionID: cmd.MissionID.String(), TaskID: cmd.TaskID.String(),
			SourceKind: assigned.Contract.SourceKind, SourceID: assigned.Contract.SourceID, SourceStepID: assigned.Contract.SourceStepID,
			AgentProfileID: assignee.ProfileID.String(), State: cmd.ResponsibilityState,
			Priority: responsibilityPriority(assigned.Contract.Priority), DueAt: assigned.Contract.DuePolicy.DueAt,
			ActorID: viewer.ViewerID, Reason: cmd.Reason, OccurredAt: cmd.OccurredAt,
		}); err != nil {
			return err
		}
		updated = assigned
		return nil
	})
	if err != nil {
		return CommandReceiptDTO{}, err
	}

	projectionNow := s.now()
	if !replaying || !taskProjectionAlreadyApplied(model, cmd.TaskID, projection.TaskStatusAssigned, assignee.StableAgentID, taskProjectionAssign) {
		model, err = s.persistTaskProjection(ctx, viewer, cmd.MissionID, model, updated, projection.TaskStatusAssigned, assignee, projectionNow, taskProjectionAssign)
		if err != nil {
			return CommandReceiptDTO{}, err
		}
	}
	if !assignee.HistoricalOnly {
		if err := s.addTaskAssigneeToDiscussion(ctx, model, assignee, projectionNow); err != nil {
			return CommandReceiptDTO{}, err
		}
		if err := s.reconcileTaskAssigneeRelations(ctx, viewer, model, cmd.MissionID, assignee); err != nil {
			return CommandReceiptDTO{}, err
		}
	}
	return CommandReceiptDTO{
		CommandID: cmd.IdempotencyKey, Status: "accepted", MissionID: cmd.MissionID.String(), TaskID: cmd.TaskID.String(),
		Version: int(updated.Version), AcceptedAt: updated.StatusChangedAt,
	}, nil
}

func (s *LifecycleService) resolveHistoricalTaskAssignee(ctx context.Context, model projection.ReadModel, assigneeID string) (taskAssigneeBinding, error) {
	assignee, err := s.resolveTaskAssignee(ctx, model, assigneeID)
	if err == nil {
		return assignee, nil
	}
	if !errors.Is(err, ErrInvalidRequest) {
		return taskAssigneeBinding{}, err
	}
	profileID := agentcore.AgentProfileID(strings.TrimSpace(assigneeID))
	if profileID.IsZero() {
		return taskAssigneeBinding{}, ErrInvalidRequest
	}
	return taskAssigneeBinding{
		ProfileID: profileID, InstanceID: agentcore.AgentInstanceID(profileID), StableAgentID: agentcore.AgentID(profileID), HistoricalOnly: true,
	}, nil
}

func validHistoricalResponsibilityState(state string) bool {
	switch state {
	case "submitted", "fulfilled", "terminated":
		return true
	default:
		return false
	}
}
