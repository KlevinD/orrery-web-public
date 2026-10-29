package task

import (
	"context"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/mission/storage"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
)

type HistoricalTaskOutcome string

const (
	HistoricalTaskOutcomeCompleted HistoricalTaskOutcome = "completed"
	HistoricalTaskOutcomeFailed    HistoricalTaskOutcome = "failed"
	HistoricalTaskOutcomeSkipped   HistoricalTaskOutcome = "skipped"
)

type HistoricalTaskOutcomeInput struct {
	TaskID            TaskID
	MissionID         missioncore.MissionID
	AgentProfileID    agentcore.AgentProfileID
	Outcome           HistoricalTaskOutcome
	Result            agentcore.AgentResult
	EvidenceValidated bool
	FailureReason     string
	IdempotencyKey    string
	OccurredAt        time.Time
}

type HistoricalTaskOutcomeResult struct {
	Status     TaskStatus
	Submission TaskSubmission
}

func (s *DeliveryService) ImportHistoricalOutcome(ctx context.Context, input HistoricalTaskOutcomeInput) (HistoricalTaskOutcomeResult, error) {
	if s == nil || s.uow == nil || s.repository == nil {
		return HistoricalTaskOutcomeResult{}, validationError(CodeMissingRequiredField, "service", "historical task import dependencies are required")
	}
	input.FailureReason = strings.TrimSpace(input.FailureReason)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	wantedStatus, err := historicalTaskStatus(input)
	if err != nil {
		return HistoricalTaskOutcomeResult{}, err
	}
	if input.TaskID.IsZero() || input.MissionID.IsZero() || input.AgentProfileID.IsZero() || input.OccurredAt.IsZero() {
		return HistoricalTaskOutcomeResult{}, validationError(CodeMissingRequiredField, "historical_outcome", "task, Mission, Agent, and occurrence time are required")
	}
	if input.Outcome == HistoricalTaskOutcomeCompleted && input.IdempotencyKey == "" {
		return HistoricalTaskOutcomeResult{}, validationError(CodeMissingRequiredField, "idempotency_key", "completed historical outcome requires an idempotency key")
	}
	normalizedResult := input.Result
	if input.Outcome == HistoricalTaskOutcomeCompleted {
		normalizedResult, err = historicalResultForTask(input.Result, input.MissionID, input.TaskID, agentcore.AgentID(input.AgentProfileID))
		if err != nil {
			return HistoricalTaskOutcomeResult{}, err
		}
	}
	var out HistoricalTaskOutcomeResult
	err = s.uow.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, storage.TableTaskRecords.LogicalName, input.TaskID.String()); err != nil {
			return err
		}
		record, err := s.repository.GetTask(ctx, tx, input.TaskID)
		if err != nil {
			return err
		}
		if record.Contract.MissionID != input.MissionID || record.Contract.Assignee.AgentProfileRef == nil || record.Contract.Assignee.AgentProfileRef.ID != input.AgentProfileID {
			return NewConflictError("historical_task_identity_conflict", "task", "historical outcome does not match Task Mission and assignee")
		}
		if record.Status == wantedStatus {
			out.Status = record.Status
			if input.Outcome == HistoricalTaskOutcomeCompleted {
				if submission, ok, err := s.repository.FindSubmissionByIdempotencyKey(ctx, tx, input.TaskID, input.IdempotencyKey); err != nil {
					return err
				} else if ok {
					out.Submission = submission
				}
			}
			return nil
		}
		if record.Status != TaskStatusAssigned {
			return NewConflictError("historical_task_state_conflict", "status", "Task has a different historical outcome")
		}
		actor := agentcore.AgentID(input.AgentProfileID)
		if input.Outcome == HistoricalTaskOutcomeSkipped {
			record, err = transitionHistoricalTask(record, TaskStatusCancelled, input.FailureReason, input.OccurredAt)
			if err != nil {
				return err
			}
			if err := s.appendEventAt(ctx, tx, record, TaskEventTaskCancelled, actor, input.AgentProfileID, map[string]any{"reason": input.FailureReason}, input.OccurredAt); err != nil {
				return err
			}
			if err := s.repository.SaveTask(ctx, tx, record); err != nil {
				return err
			}
			out.Status = record.Status
			return nil
		}
		record, err = transitionHistoricalTask(record, TaskStatusAccepted, "historical assignment accepted", input.OccurredAt)
		if err != nil {
			return err
		}
		if err := s.appendEventAt(ctx, tx, record, TaskEventTaskAccepted, actor, input.AgentProfileID, map[string]any{"status": "accepted"}, input.OccurredAt); err != nil {
			return err
		}
		record, err = transitionHistoricalTask(record, TaskStatusInProgress, "historical execution started", input.OccurredAt)
		if err != nil {
			return err
		}
		if err := s.appendEventAt(ctx, tx, record, TaskEventTaskStarted, actor, input.AgentProfileID, map[string]any{"status": "in_progress"}, input.OccurredAt); err != nil {
			return err
		}
		switch input.Outcome {
		case HistoricalTaskOutcomeCompleted:
			result := normalizedResult
			if input.EvidenceValidated {
				if errs := ValidateAgentResult(record.Contract, result); len(errs) > 0 {
					return ResultValidationError{Errors: errs}
				}
			} else {
				result.Deliverables = nil
				result.Evidence = nil
			}
			if !result.Summary.IsZero() {
				submission, deliverables, evidence, err := s.buildSubmission(record, result, input.IdempotencyKey, input.OccurredAt)
				if err != nil {
					return err
				}
				if err := s.repository.SaveSubmission(ctx, tx, submission, deliverables, evidence); err != nil {
					return err
				}
				if err := s.appendEventAt(ctx, tx, record, TaskEventAgentResultSubmitted, actor, input.AgentProfileID, submission, input.OccurredAt); err != nil {
					return err
				}
				for _, deliverable := range deliverables {
					if err := s.appendEventAt(ctx, tx, record, TaskEventDeliverableSubmitted, actor, input.AgentProfileID, deliverable, input.OccurredAt); err != nil {
						return err
					}
				}
				for _, item := range evidence {
					if err := s.appendEventAt(ctx, tx, record, TaskEventEvidenceSubmitted, actor, input.AgentProfileID, item, input.OccurredAt); err != nil {
						return err
					}
				}
				out.Submission = submission
			} else {
				if err := s.appendEventAt(ctx, tx, record, TaskEventTaskSubmitted, actor, input.AgentProfileID, map[string]any{"reason": "historical run completed without persisted summary"}, input.OccurredAt); err != nil {
					return err
				}
			}
			record, err = transitionHistoricalTask(record, TaskStatusSubmitted, "historical scheduled run completed", input.OccurredAt)
			if err != nil {
				return err
			}
			if input.EvidenceValidated {
				record, err = transitionHistoricalTask(record, TaskStatusApproved, "historical evidence revalidated", input.OccurredAt)
				if err != nil {
					return err
				}
				skip := QASkipRecord{
					ID: QASkipID("qa-skip-historical-" + input.IdempotencyKey), TaskID: input.TaskID,
					SubmissionID: out.Submission.ID, AgentID: actor, Reason: "historical evidence revalidated", SkippedAt: input.OccurredAt,
				}
				if err := s.appendEventAt(ctx, tx, record, TaskEventQASkipped, actor, input.AgentProfileID, skip, input.OccurredAt); err != nil {
					return err
				}
			}
		case HistoricalTaskOutcomeFailed:
			record, err = transitionHistoricalTask(record, TaskStatusBlocked, input.FailureReason, input.OccurredAt)
			if err != nil {
				return err
			}
			if err := s.appendEventAt(ctx, tx, record, TaskEventTaskBlocked, actor, input.AgentProfileID, map[string]any{"reason": input.FailureReason}, input.OccurredAt); err != nil {
				return err
			}
		}
		if err := s.repository.SaveTask(ctx, tx, record); err != nil {
			return err
		}
		out.Status = record.Status
		return nil
	})
	return out, err
}

func historicalTaskStatus(input HistoricalTaskOutcomeInput) (TaskStatus, error) {
	switch input.Outcome {
	case HistoricalTaskOutcomeCompleted:
		if input.EvidenceValidated {
			return TaskStatusApproved, nil
		}
		return TaskStatusSubmitted, nil
	case HistoricalTaskOutcomeFailed:
		if strings.TrimSpace(input.FailureReason) == "" {
			return "", validationError(CodeMissingRequiredField, "failure_reason", "failed historical outcome requires its stored reason")
		}
		return TaskStatusBlocked, nil
	case HistoricalTaskOutcomeSkipped:
		if strings.TrimSpace(input.FailureReason) == "" {
			return "", validationError(CodeMissingRequiredField, "failure_reason", "skipped historical outcome requires its stored reason")
		}
		return TaskStatusCancelled, nil
	default:
		return "", validationError(CodeInvalidRef, "outcome", "unsupported historical task outcome")
	}
}

func transitionHistoricalTask(record TaskRecord, status TaskStatus, reason string, occurredAt time.Time) (TaskRecord, error) {
	return record.Transition(status, TransitionOptions{ExpectedVersion: record.Version, Reason: reason, Now: occurredAt})
}

func historicalResultForTask(result agentcore.AgentResult, missionID missioncore.MissionID, taskID TaskID, agentID agentcore.AgentID) (agentcore.AgentResult, error) {
	if (!result.MissionID.IsZero() && result.MissionID != missionID) ||
		(!result.TaskID.IsZero() && result.TaskID != missioncore.TaskID(taskID)) ||
		(result.AgentID != "" && result.AgentID != agentID) {
		return agentcore.AgentResult{}, NewConflictError("historical_result_identity_conflict", "result", "historical result identity does not match Task Mission and assignee")
	}
	result.MissionID = missionID
	result.TaskID = missioncore.TaskID(taskID)
	result.AgentID = agentID
	return result, nil
}
