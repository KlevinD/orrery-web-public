package task

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/mission/storage"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/uow"
)

type DeliveryService struct {
	uow         *uow.UnitOfWork
	repository  Repository
	events      EventSink
	clock       clock.Clock
	idGenerator idgen.ErrorGenerator
	assignees   MissionAssignmentResolver
}

type DeliveryServiceOptions struct {
	Clock            clock.Clock
	IDGenerator      idgen.ErrorGenerator
	AssigneeResolver MissionAssignmentResolver
}

type SubmitOptions struct {
	IdempotencyKey  string
	ExpectedVersion int64
}

func NewDeliveryService(unit *uow.UnitOfWork, repository Repository, events EventSink, opts DeliveryServiceOptions) *DeliveryService {
	return &DeliveryService{
		uow:         unit,
		repository:  repository,
		events:      events,
		clock:       opts.Clock,
		idGenerator: opts.IDGenerator,
		assignees:   opts.AssigneeResolver,
	}
}

func (s *DeliveryService) AcceptTask(ctx context.Context, taskID TaskID, agent agentcore.AgentInstanceRef) error {
	return s.transitionAssignedTask(ctx, taskID, agent, TaskStatusAssigned, TaskStatusAccepted, TaskEventTaskAccepted, "task accepted")
}

func (s *DeliveryService) StartTask(ctx context.Context, taskID TaskID, agent agentcore.AgentInstanceRef) error {
	return s.transitionAssignedTask(ctx, taskID, agent, TaskStatusAccepted, TaskStatusInProgress, TaskEventTaskStarted, "task started")
}

func (s *DeliveryService) SubmitAgentResult(ctx context.Context, taskID TaskID, result agentcore.AgentResult, opts SubmitOptions) (TaskSubmission, error) {
	var saved TaskSubmission
	err := s.uow.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, storage.TableTaskRecords.LogicalName, taskID.String()); err != nil {
			return err
		}
		task, err := s.repository.GetTask(ctx, tx, taskID)
		if err != nil {
			return err
		}
		key := SubmissionIdempotencyKey(result.RunID, opts.IdempotencyKey)
		if key == "" {
			return validationError(CodeMissingRequiredField, "idempotency_key", "submission idempotency key or run id is required")
		}
		if existing, ok, err := s.repository.FindSubmissionByIdempotencyKey(ctx, tx, taskID, key); err != nil {
			return err
		} else if ok {
			saved = existing
			return nil
		}
		if opts.ExpectedVersion != 0 && task.Version != opts.ExpectedVersion {
			return NewConflictError("task_version_conflict", "version", "task version changed")
		}
		if task.Status.IsTerminal() {
			return NewConflictError("task_terminal", "status", "terminal tasks cannot accept submissions")
		}
		if task.Status != TaskStatusInProgress && task.Status != TaskStatusBlocked {
			return NewConflictError("task_not_in_progress", "status", "task must be in progress before submission")
		}
		if err := ensureResultMatchesTask(task.Contract, result); err != nil {
			return err
		}
		if errs := ValidateAgentResult(task.Contract, result); len(errs) > 0 {
			return ResultValidationError{Errors: errs}
		}
		now := s.now()
		submission, deliverables, evidence, err := s.buildSubmission(task, result, key, now)
		if err != nil {
			return err
		}
		var updated TaskRecord
		if result.Blocker != nil {
			updated, err = task.Transition(TaskStatusBlocked, TransitionOptions{
				ExpectedVersion: task.Version, Reason: strings.TrimSpace(result.Blocker.Summary), Now: now,
			})
		} else {
			var submitted TaskRecord
			submitted, err = task.Transition(TaskStatusSubmitted, TransitionOptions{ExpectedVersion: task.Version, Reason: "agent result submitted", Now: now})
			if err == nil {
				finalStatus := TaskStatusApproved
				reason := "submission accepted without qa"
				if submitted.Contract.QACheckRequired {
					finalStatus = TaskStatusInReview
					reason = "submission ready for qa"
				}
				updated, err = submitted.Transition(finalStatus, TransitionOptions{ExpectedVersion: submitted.Version, Reason: reason, Now: now})
			}
		}
		if err != nil {
			return err
		}
		if err := s.repository.SaveSubmission(ctx, tx, submission, deliverables, evidence); err != nil {
			return err
		}
		if err := s.repository.SaveTask(ctx, tx, updated); err != nil {
			return err
		}
		if err := s.appendSubmissionEvents(ctx, tx, updated, submission, deliverables, evidence, now); err != nil {
			return err
		}
		saved = submission
		return nil
	})
	return saved, err
}

func (s *DeliveryService) transitionAssignedTask(ctx context.Context, taskID TaskID, agent agentcore.AgentInstanceRef, required TaskStatus, next TaskStatus, eventType TaskEventType, reason string) error {
	return s.uow.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, storage.TableTaskRecords.LogicalName, taskID.String()); err != nil {
			return err
		}
		task, err := s.repository.GetTask(ctx, tx, taskID)
		if err != nil {
			return err
		}
		if task.Status != required {
			return NewConflictError("task_status_mismatch", "status", fmt.Sprintf("task must be %s", required))
		}
		if next == TaskStatusInProgress && task.Contract.QAPlanRequired && !task.Contract.QAPlanCompleted {
			return NewConflictError("qa_plan_required", "qa_plan", "qa plan must be completed before task start")
		}
		expected, err := ResolveTaskAssignee(ctx, task.Contract.Assignee, s.assignees)
		if err != nil {
			return err
		}
		if !expected.MatchesInstance(agent) {
			return NewConflictError("task_assignee_mismatch", "assignee", "agent is not the task assignee")
		}
		updated, err := task.Transition(next, TransitionOptions{ExpectedVersion: task.Version, Reason: reason, Now: s.now()})
		if err != nil {
			return err
		}
		if err := s.repository.SaveTask(ctx, tx, updated); err != nil {
			return err
		}
		return s.appendDeliveryEvent(ctx, tx, updated, eventType, agentcore.AgentID(agent.ID), expected.ProfileRef.ID, map[string]any{"status": string(next)})
	})
}

func (s *DeliveryService) buildSubmission(task TaskRecord, result agentcore.AgentResult, key string, now time.Time) (TaskSubmission, []DeliverableSubmission, []EvidenceSubmission, error) {
	submissionID, err := s.newID("submission")
	if err != nil {
		return TaskSubmission{}, nil, nil, err
	}
	submission := TaskSubmission{
		ID:                  TaskSubmissionID(submissionID),
		TaskID:              task.Contract.ID,
		AgentID:             result.AgentID,
		RunID:               result.RunID,
		AgentResultRef:      result.ID,
		InternalTraceRef:    result.InternalTraceRef,
		RuntimeAttemptID:    result.RuntimeAttemptID,
		Summary:             result.Summary,
		UserReport:          result.UserReport,
		Blocker:             cloneAgentResultBlocker(result.Blocker),
		SubmittedAt:         now,
		IdempotencyKey:      key,
		TaskVersionAccepted: task.Version,
	}
	deliverables := make([]DeliverableSubmission, 0, len(result.Deliverables))
	for _, deliverable := range result.Deliverables {
		id, err := s.newID("deliverable submission")
		if err != nil {
			return TaskSubmission{}, nil, nil, err
		}
		submission.DeliverableRefs = append(submission.DeliverableRefs, deliverable.ID)
		submission.DeliverableRecordRefs = append(submission.DeliverableRecordRefs, DeliverableSubmissionID(id))
		deliverables = append(deliverables, DeliverableSubmission{
			ID:           DeliverableSubmissionID(id),
			SubmissionID: submission.ID,
			TaskID:       task.Contract.ID,
			AgentID:      result.AgentID,
			Deliverable:  deliverable,
			SubmittedAt:  now,
		})
	}
	evidence := make([]EvidenceSubmission, 0, len(result.Evidence))
	for _, item := range result.Evidence {
		id, err := s.newID("evidence submission")
		if err != nil {
			return TaskSubmission{}, nil, nil, err
		}
		submission.EvidenceRefs = append(submission.EvidenceRefs, item.ID)
		submission.EvidenceRecordRefs = append(submission.EvidenceRecordRefs, EvidenceSubmissionID(id))
		evidence = append(evidence, EvidenceSubmission{
			ID:           EvidenceSubmissionID(id),
			SubmissionID: submission.ID,
			TaskID:       task.Contract.ID,
			AgentID:      result.AgentID,
			Evidence:     item,
			SubmittedAt:  now,
		})
	}
	return submission, deliverables, evidence, nil
}

func cloneAgentResultBlocker(blocker *agentcore.Blocker) *agentcore.Blocker {
	if blocker == nil {
		return nil
	}
	return &agentcore.Blocker{Summary: strings.TrimSpace(blocker.Summary), Report: blocker.Report}
}

func (s *DeliveryService) appendSubmissionEvents(ctx context.Context, tx uow.Tx, task TaskRecord, submission TaskSubmission, deliverables []DeliverableSubmission, evidence []EvidenceSubmission, now time.Time) error {
	submitterProfileID := agentcore.AgentProfileID(submission.AgentID)
	if err := s.appendEventAt(ctx, tx, task, TaskEventAgentResultSubmitted, submission.AgentID, submitterProfileID, submission, now); err != nil {
		return err
	}
	for _, deliverable := range deliverables {
		if err := s.appendEventAt(ctx, tx, task, TaskEventDeliverableSubmitted, submission.AgentID, submitterProfileID, deliverable, now); err != nil {
			return err
		}
	}
	for _, item := range evidence {
		if err := s.appendEventAt(ctx, tx, task, TaskEventEvidenceSubmitted, submission.AgentID, submitterProfileID, item, now); err != nil {
			return err
		}
	}
	if submission.Blocker != nil {
		return s.appendEventAt(ctx, tx, task, TaskEventTaskBlocked, submission.AgentID, submitterProfileID, map[string]any{
			"reason": strings.TrimSpace(submission.Blocker.Summary),
		}, now)
	}
	if !task.Contract.QACheckRequired {
		skip := QASkipRecord{
			ID:           QASkipID("qa-skip-" + string(submission.ID)),
			TaskID:       task.Contract.ID,
			SubmissionID: submission.ID,
			AgentID:      submission.AgentID,
			Reason:       "qa check disabled",
			SkippedAt:    now,
		}
		return s.appendEventAt(ctx, tx, task, TaskEventQASkipped, submission.AgentID, submitterProfileID, skip, now)
	}
	// A submission that enters review must create the durable QA gate in the
	// same transaction.  Without this event the mission projection has no
	// pending QA request, even though the task is in_review.
	return s.appendEventAt(ctx, tx, task, TaskEventQARequested, submission.AgentID, submitterProfileID, map[string]any{
		"status":  "requested",
		"qa_ref":  QARequestRef(task.Contract.ID),
		"message": "QA review requested",
	}, now)
}

func (s *DeliveryService) appendDeliveryEvent(ctx context.Context, tx uow.Tx, task TaskRecord, eventType TaskEventType, actor agentcore.AgentID, actorProfileID agentcore.AgentProfileID, payload any) error {
	return s.appendEventAt(ctx, tx, task, eventType, actor, actorProfileID, payload, s.now())
}

func (s *DeliveryService) appendEventAt(ctx context.Context, tx uow.Tx, task TaskRecord, eventType TaskEventType, actor agentcore.AgentID, actorProfileID agentcore.AgentProfileID, payload any, occurredAt time.Time) error {
	if s.events == nil {
		return nil
	}
	return s.events.AppendTaskEvent(ctx, tx, TaskEvent{
		Type:           eventType,
		TaskID:         task.Contract.ID,
		MissionID:      task.Contract.MissionID.String(),
		ActorID:        actor,
		ActorProfileID: actorProfileID,
		Payload:        payload,
		OccurredAt:     occurredAt,
	})
}

func ensureResultMatchesTask(contract TaskContract, result agentcore.AgentResult) error {
	if result.TaskID != "" && result.TaskID != contract.ID {
		return NewConflictError("result_task_mismatch", "task_id", "result task id does not match task")
	}
	if !contract.MissionID.IsZero() && !result.MissionID.IsZero() && result.MissionID != contract.MissionID {
		return NewConflictError("result_mission_mismatch", "mission_id", "result mission id does not match task")
	}
	return nil
}

func (s *DeliveryService) now() time.Time {
	if s.clock == nil {
		return time.Now().UTC()
	}
	return s.clock.Now().UTC()
}

func (s *DeliveryService) newID(kind string) (string, error) {
	if s.idGenerator == nil {
		return "", fmt.Errorf("%s id generator is not configured", kind)
	}
	id, err := s.idGenerator.NewIDContext()
	if err != nil {
		return "", fmt.Errorf("generate %s id: %w", kind, err)
	}
	if id == "" {
		return "", fmt.Errorf("generated %s id is empty", kind)
	}
	return id, nil
}

type ResultValidationError struct {
	Errors []ValidationError
}

func (e ResultValidationError) Error() string {
	return "agent result failed task policy validation"
}
