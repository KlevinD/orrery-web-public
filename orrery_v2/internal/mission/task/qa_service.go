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

type QAService struct {
	uow         *uow.UnitOfWork
	repository  Repository
	events      EventSink
	clock       clock.Clock
	idGenerator idgen.ErrorGenerator
	contract    QAContract
	policy      QAPolicy
}

type QAServiceOptions struct {
	Clock       clock.Clock
	IDGenerator idgen.ErrorGenerator
	Contract    QAContract
	Policy      QAPolicy
}

type QASubmitOptions struct {
	IdempotencyKey string
}

type QAPlanSubmitOptions struct {
	IdempotencyKey string
}

// QARequestRef returns the stable projection identity for a task's pending QA
// request.  The request represents the task review gate (rather than a
// particular reviewer response), so it must remain stable across retries and
// projection replays.
func QARequestRef(taskID TaskID) string {
	return "qa-request-" + strings.TrimSpace(taskID.String())
}

func NewQAService(unit *uow.UnitOfWork, repository Repository, events EventSink, opts QAServiceOptions) *QAService {
	return &QAService{uow: unit, repository: repository, events: events, clock: opts.Clock, idGenerator: opts.IDGenerator, contract: opts.Contract, policy: opts.Policy}
}

func (s *QAService) RequestQA(ctx context.Context, taskID TaskID, requestedBy agentcore.AgentID) error {
	return s.uow.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, storage.TableTaskRecords.LogicalName, taskID.String()); err != nil {
			return err
		}
		task, err := s.repository.GetTask(ctx, tx, taskID)
		if err != nil {
			return err
		}
		if task.Status != TaskStatusInReview {
			return NewConflictError("task_not_in_review", "status", "task must be in review before QA can be requested")
		}
		return s.appendEvent(ctx, tx, task, TaskEventQARequested, requestedBy, map[string]any{
			"status":  "requested",
			"qa_ref":  QARequestRef(taskID),
			"message": "QA review requested",
		})
	})
}

func (s *QAService) SubmitQAPlan(ctx context.Context, taskID TaskID, plan QAPlanRecord, opts QAPlanSubmitOptions) (QAPlanRecord, error) {
	var saved QAPlanRecord
	err := s.uow.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, storage.TableTaskRecords.LogicalName, taskID.String()); err != nil {
			return err
		}
		task, err := s.repository.GetTask(ctx, tx, taskID)
		if err != nil {
			return err
		}
		key := strings.TrimSpace(opts.IdempotencyKey)
		if key == "" {
			return validationError(CodeMissingRequiredField, "idempotency_key", "QA plan idempotency key is required")
		}
		if task.Contract.QAPlanCompleted {
			if task.Contract.QAPlanIdempotencyKey != key {
				return NewConflictError("qa_plan_already_completed", "idempotency_key", "task already has a different completed QA plan")
			}
			if plan.ID != "" && plan.ID != task.Contract.QAPlanRef {
				return NewConflictError("qa_plan_replay_mismatch", "qa_plan_id", "QA plan replay does not match the completed plan")
			}
			if plan.ArtifactRef.ID != "" && plan.ArtifactRef.ID != task.Contract.QAPlanArtifactRef.ID {
				return NewConflictError("qa_plan_replay_mismatch", "artifact_ref", "QA plan replay does not match the completed artifact")
			}
			plan.ID = task.Contract.QAPlanRef
			plan.TaskID = taskID
			plan.ArtifactRef = task.Contract.QAPlanArtifactRef
			plan.IdempotencyKey = key
			if plan.CompletedAt.IsZero() {
				plan.CompletedAt = task.StatusChangedAt
			}
			saved = plan
			return nil
		}
		if task.Status.IsTerminal() {
			return NewConflictError("task_terminal", "status", "terminal tasks cannot accept QA plans")
		}
		if !task.Contract.QAPlanRequired {
			return NewConflictError("qa_plan_not_required", "qa_plan", "task does not require a QA plan")
		}
		plan, err = s.prepareQAPlan(plan, taskID, key)
		if err != nil {
			return err
		}
		updated := task
		updated.Contract.QAPlanCompleted = true
		updated.Contract.QAPlanRef = plan.ID
		updated.Contract.QAPlanArtifactRef = plan.ArtifactRef
		updated.Contract.QAPlanIdempotencyKey = key
		updated.Version++
		updated.LastTransitionReason = "QA plan completed"
		updated.StatusChangedAt = plan.CompletedAt
		if err := s.repository.SaveTask(ctx, tx, updated); err != nil {
			return err
		}
		if err := s.appendEventAt(ctx, tx, updated, TaskEventQAPlanCompleted, plan.CreatedBy, plan, plan.CompletedAt); err != nil {
			return err
		}
		saved = plan
		return nil
	})
	return saved, err
}

func (s *QAService) SubmitQAResult(ctx context.Context, taskID TaskID, result QAResultRecord, opts QASubmitOptions) (QAResultRecord, error) {
	var saved QAResultRecord
	err := s.uow.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, storage.TableTaskRecords.LogicalName, taskID.String()); err != nil {
			return err
		}
		task, err := s.repository.GetTask(ctx, tx, taskID)
		if err != nil {
			return err
		}
		key := strings.TrimSpace(opts.IdempotencyKey)
		if key == "" {
			return validationError(CodeMissingRequiredField, "idempotency_key", "qa result idempotency key is required")
		}
		if existing, ok, err := s.repository.FindQAResultByIdempotencyKey(ctx, tx, taskID, key); err != nil {
			return err
		} else if ok {
			saved = existing
			return nil
		}
		if task.Status.IsTerminal() {
			return NewConflictError("task_terminal", "status", "terminal tasks cannot accept QA results")
		}
		if task.Status != TaskStatusInReview {
			return NewConflictError("task_not_in_review", "status", "task must be in review before QA result")
		}
		latest, ok, err := s.repository.LatestSubmission(ctx, tx, taskID)
		if err != nil {
			return err
		}
		if !ok {
			return NewConflictError("submission_missing", "submission_id", "QA requires a task submission")
		}
		if result.SubmissionID != "" && result.SubmissionID != string(latest.ID) {
			return NewConflictError("stale_submission", "submission_id", "QA result targets an older submission")
		}
		result = s.prepareResult(result, taskID, latest, key)
		contract := s.effectiveContract(task.Contract)
		if err := ensureRequiredReviewer(contract, result.CheckedBy); err != nil {
			return err
		}
		evidence, err := s.repository.ListEvidenceSubmissions(ctx, tx, latest.ID)
		if err != nil {
			return err
		}
		result = CheckQA(contract, s.policy, QACheckInput{
			TaskID:       taskID,
			SubmissionID: string(latest.ID),
			CheckedBy:    result.CheckedBy,
			Evidence:     evidenceItems(evidence),
		}, result)
		switch result.Status {
		case QAStatusApproved:
			if err := s.approve(ctx, tx, task, &result); err != nil {
				return err
			}
		case QAStatusRejected, QAStatusNeedsRework:
			if err := s.reject(ctx, tx, task, &result); err != nil {
				return err
			}
		case QAStatusBlocked:
			return NewConflictError("qa_blocked", "status", strings.Join(result.RejectionReasons, "; "))
		default:
			return NewConflictError("qa_status_unsupported", "status", fmt.Sprintf("unsupported QA status %s", result.Status))
		}
		if err := s.repository.SaveQAResult(ctx, tx, result); err != nil {
			return err
		}
		saved = result
		return nil
	})
	return saved, err
}

func (s *QAService) prepareQAPlan(plan QAPlanRecord, taskID TaskID, key string) (QAPlanRecord, error) {
	if plan.TaskID == "" {
		plan.TaskID = taskID
	}
	if plan.TaskID != taskID {
		return QAPlanRecord{}, NewConflictError("qa_plan_task_mismatch", "task_id", "QA plan task id does not match task")
	}
	if plan.ID == "" {
		id, err := s.newID("qa plan")
		if err != nil {
			return QAPlanRecord{}, err
		}
		plan.ID = QAPlanID(id)
	}
	if plan.CreatedBy == "" {
		return QAPlanRecord{}, validationError(CodeMissingRequiredField, "created_by", "QA plan creator is required")
	}
	if plan.ArtifactRef.ID == "" {
		return QAPlanRecord{}, validationError(CodeMissingRequiredField, "artifact_ref", "QA plan artifact ref is required")
	}
	if plan.CompletedAt.IsZero() {
		plan.CompletedAt = s.now()
	}
	plan.IdempotencyKey = key
	return plan, nil
}

func ensureRequiredReviewer(contract QAContract, checkedBy agentcore.AgentID) error {
	if contract.RequiredReviewerProfileRef.IsZero() {
		return nil
	}
	if agentcore.AgentProfileID(checkedBy) != contract.RequiredReviewerProfileRef.ID {
		return NewConflictError("qa_reviewer_mismatch", "checked_by", "QA result must be submitted by the required reviewer")
	}
	return nil
}

func (s *QAService) effectiveContract(contract TaskContract) QAContract {
	if len(s.contract.AcceptanceCriteria) > 0 || len(s.contract.EvidenceRequirements) > 0 {
		return s.contract
	}
	return QAContract{
		AcceptanceCriteria:   contract.AcceptanceCriteria,
		EvidenceRequirements: contract.EvidenceRequirements,
	}
}

func evidenceItems(submissions []EvidenceSubmission) []agentcore.Evidence {
	items := make([]agentcore.Evidence, 0, len(submissions))
	for _, submission := range submissions {
		items = append(items, submission.Evidence)
	}
	return items
}

func (s *QAService) approve(ctx context.Context, tx uow.Tx, task TaskRecord, result *QAResultRecord) error {
	reportID, err := s.newID("acceptance report")
	if err != nil {
		return err
	}
	result.AcceptanceReportRef = reportID
	report := AcceptanceReport{
		ID:           AcceptanceReportID(reportID),
		TaskID:       task.Contract.ID,
		SubmissionID: result.SubmissionID,
		QAResultID:   result.ID,
		CreatedBy:    result.CheckedBy,
		Summary:      acceptanceReportSummary(*result),
		CreatedAt:    result.CheckedAt,
	}
	updated, err := task.Transition(TaskStatusApproved, TransitionOptions{ExpectedVersion: task.Version, Reason: "QA approved", Now: result.CheckedAt})
	if err != nil {
		return err
	}
	if err := s.repository.SaveAcceptanceReport(ctx, tx, report); err != nil {
		return err
	}
	if err := s.repository.SaveTask(ctx, tx, updated); err != nil {
		return err
	}
	if err := s.appendEventAt(ctx, tx, updated, TaskEventQAApproved, result.CheckedBy, *result, result.CheckedAt); err != nil {
		return err
	}
	return s.appendEventAt(ctx, tx, updated, TaskEventAcceptanceReportCreated, result.CheckedBy, report, result.CheckedAt)
}

func acceptanceReportSummary(result QAResultRecord) string {
	criteriaPassed := 0
	for _, criterion := range result.CheckedCriteria {
		if criterion.Status == CheckStatusPassed {
			criteriaPassed++
		}
	}
	evidencePassed := 0
	for _, evidence := range result.EvidenceResults {
		if evidence.Status == CheckStatusPassed {
			evidencePassed++
		}
	}
	return fmt.Sprintf(
		"QA approved submission %s: %d/%d acceptance criteria passed and %d/%d evidence requirements passed.",
		strings.TrimSpace(result.SubmissionID), criteriaPassed, len(result.CheckedCriteria), evidencePassed, len(result.EvidenceResults),
	)
}

func (s *QAService) reject(ctx context.Context, tx uow.Tx, task TaskRecord, result *QAResultRecord) error {
	request, ok, err := s.repository.FindOpenReworkRequest(ctx, tx, task.Contract.ID)
	if err != nil {
		return err
	}
	if !ok {
		id, err := s.newID("rework request")
		if err != nil {
			return err
		}
		request = NewReworkRequest(ReworkRequestOptions{
			ID:              ReworkRequestID(id),
			OriginalTaskID:  task.Contract.ID,
			TargetAssignee:  task.Contract.Assignee,
			Reason:          strings.Join(result.RejectionReasons, "; "),
			FailedCriteria:  failedCriteria(result.CheckedCriteria),
			MissingEvidence: result.MissingEvidence,
			RequestedBy:     result.CheckedBy,
			CreatedAt:       result.CheckedAt,
		})
		if err := s.repository.SaveReworkRequest(ctx, tx, request); err != nil {
			return err
		}
	}
	result.ReworkRequestRef = string(request.ID)
	rejected, err := task.Transition(TaskStatusRejected, TransitionOptions{ExpectedVersion: task.Version, Reason: "QA rejected", Now: result.CheckedAt})
	if err != nil {
		return err
	}
	rework, err := rejected.Transition(TaskStatusReworkRequested, TransitionOptions{ExpectedVersion: rejected.Version, Reason: "rework requested", Now: result.CheckedAt})
	if err != nil {
		return err
	}
	if err := s.repository.SaveTask(ctx, tx, rework); err != nil {
		return err
	}
	if err := s.appendEventAt(ctx, tx, rework, TaskEventQARejected, result.CheckedBy, *result, result.CheckedAt); err != nil {
		return err
	}
	return s.appendEventAt(ctx, tx, rework, TaskEventReworkRequested, result.CheckedBy, request, result.CheckedAt)
}

func (s *QAService) prepareResult(result QAResultRecord, taskID TaskID, submission TaskSubmission, key string) QAResultRecord {
	if result.TaskID == "" {
		result.TaskID = taskID
	}
	if result.SubmissionID == "" {
		result.SubmissionID = string(submission.ID)
	}
	if result.CheckedAt.IsZero() {
		result.CheckedAt = s.now()
	}
	if result.ID == "" {
		if id, err := s.newID("qa result"); err == nil {
			result.ID = QAResultID(id)
		}
	}
	result.IdempotencyKey = key
	return result
}

func failedCriteria(items []CheckedCriterion) []AcceptanceCriterionID {
	var ids []AcceptanceCriterionID
	for _, item := range items {
		if item.Status == CheckStatusFailed || item.Status == CheckStatusMissing {
			ids = append(ids, item.CriterionID)
		}
	}
	return ids
}

func (s *QAService) appendEvent(ctx context.Context, tx uow.Tx, task TaskRecord, eventType TaskEventType, actor agentcore.AgentID, payload any) error {
	return s.appendEventAt(ctx, tx, task, eventType, actor, payload, s.now())
}

func (s *QAService) appendEventAt(ctx context.Context, tx uow.Tx, task TaskRecord, eventType TaskEventType, actor agentcore.AgentID, payload any, occurredAt time.Time) error {
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
		OccurredAt:     occurredAt,
	})
}

func (s *QAService) now() time.Time {
	if s.clock == nil {
		return time.Now().UTC()
	}
	return s.clock.Now().UTC()
}

func (s *QAService) newID(kind string) (string, error) {
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
