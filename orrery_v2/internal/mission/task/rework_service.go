package task

import (
	"context"
	"fmt"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/mission/storage"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
)

type ReworkService struct {
	uow        *uow.UnitOfWork
	repository Repository
	events     EventSink
	clock      clock.Clock
}

type ReworkServiceOptions struct {
	Clock clock.Clock
}

func NewReworkService(unit *uow.UnitOfWork, repository Repository, events EventSink, opts ReworkServiceOptions) *ReworkService {
	return &ReworkService{uow: unit, repository: repository, events: events, clock: opts.Clock}
}

func (s *ReworkService) ReassignOpenRework(ctx context.Context, taskID TaskID, requestedBy agentcore.AgentID) (ReworkRequest, error) {
	var saved ReworkRequest
	err := s.uow.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, storage.TableTaskRecords.LogicalName, taskID.String()); err != nil {
			return err
		}
		task, err := s.repository.GetTask(ctx, tx, taskID)
		if err != nil {
			return err
		}
		request, ok, err := s.repository.FindOpenReworkRequest(ctx, tx, taskID)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: open rework request for task %s", ErrNotFound, taskID)
		}
		if task.Status == TaskStatusAssigned && request.Status == ReworkStatusAssigned {
			saved = request
			return nil
		}
		if task.Status != TaskStatusReworkRequested {
			return NewConflictError("task_not_rework_requested", "status", "task must be rework_requested before reassignment")
		}
		request.Status = ReworkStatusAssigned
		updated, err := task.Transition(TaskStatusAssigned, TransitionOptions{ExpectedVersion: task.Version, Reason: "rework reassigned", Now: s.now()})
		if err != nil {
			return err
		}
		if err := s.repository.SaveReworkRequest(ctx, tx, request); err != nil {
			return err
		}
		if err := s.repository.SaveTask(ctx, tx, updated); err != nil {
			return err
		}
		if err := s.appendEvent(ctx, tx, updated, TaskEventReworkAssigned, requestedBy, request); err != nil {
			return err
		}
		saved = request
		return nil
	})
	return saved, err
}

func (s *ReworkService) ResolveRework(ctx context.Context, taskID TaskID, resolvedBy agentcore.AgentID) (ReworkRequest, error) {
	var saved ReworkRequest
	err := s.uow.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, storage.TableTaskRecords.LogicalName, taskID.String()); err != nil {
			return err
		}
		task, err := s.repository.GetTask(ctx, tx, taskID)
		if err != nil {
			return err
		}
		if task.Status != TaskStatusAccepted && task.Status != TaskStatusInProgress && task.Status != TaskStatusSubmitted && task.Status != TaskStatusInReview && task.Status != TaskStatusApproved {
			return NewConflictError("task_not_reworked", "status", "task has not re-entered execution")
		}
		request, ok, err := s.repository.FindOpenReworkRequest(ctx, tx, taskID)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if !isActiveReworkStatus(request.Status) {
			return nil
		}
		request.Status = ReworkStatusResolved
		if err := s.repository.SaveReworkRequest(ctx, tx, request); err != nil {
			return err
		}
		if err := s.appendEvent(ctx, tx, task, TaskEventReworkResolved, resolvedBy, request); err != nil {
			return err
		}
		saved = request
		return nil
	})
	return saved, err
}

func (s *ReworkService) appendEvent(ctx context.Context, tx uow.Tx, task TaskRecord, eventType TaskEventType, actor agentcore.AgentID, payload any) error {
	if s.events == nil {
		return nil
	}
	return s.events.AppendTaskEvent(ctx, tx, TaskEvent{
		Type:           eventType,
		TaskID:         task.Contract.ID,
		MissionID:      task.Contract.MissionID.String(),
		ActorID:        actor,
		ActorProfileID: agentcore.AgentProfileID(actor),
		Payload:        payload,
		OccurredAt:     s.now(),
	})
}

func (s *ReworkService) now() time.Time {
	if s.clock == nil {
		return time.Now().UTC()
	}
	return s.clock.Now().UTC()
}
