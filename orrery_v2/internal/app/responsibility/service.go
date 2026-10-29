package responsibility

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/uow"
)

type TaskRepositoryPort interface {
	GetTask(ctx context.Context, tx uow.Tx, id missiontask.TaskID) (missiontask.TaskRecord, error)
}

type TaskEvidenceRepositoryPort interface {
	LatestSubmission(ctx context.Context, tx uow.Tx, taskID missiontask.TaskID) (missiontask.TaskSubmission, bool, error)
	ListEvidenceSubmissions(ctx context.Context, tx uow.Tx, submissionID missiontask.TaskSubmissionID) ([]missiontask.EvidenceSubmission, error)
}

type ServiceOptions struct {
	Repository  *Repository
	UnitOfWork  *uow.UnitOfWork
	Tasks       TaskRepositoryPort
	Clock       clock.Clock
	MaxAttempts int
	RetryDelays []time.Duration
}

type Service struct {
	repository      *Repository
	unit            *uow.UnitOfWork
	tasks           TaskRepositoryPort
	clock           clock.Clock
	maxAttempts     int
	retryDelays     []time.Duration
	retryConfigured bool
}

type TaskReviewSignal struct {
	TenantID string
	TaskID   string
	Approved bool
}

type RuntimeOutcomeInput struct {
	TenantID    string
	TaskID      string
	RunID       string
	Outcome     AttemptOutcome
	Error       error
	TraceRef    string
	UsageTokens int64
}

type ExternalSignal struct {
	TenantID         string
	ActorID          string
	IdempotencyKey   string
	ResponsibilityID string
	BlockerKind      BlockerKind
	RequestedFrom    string
	SourceRef        string
	Summary          string
}

func NewService(opts ServiceOptions) *Service {
	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	retryDelays := append([]time.Duration(nil), opts.RetryDelays...)
	if len(retryDelays) == 0 {
		retryDelays = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute}
	}
	return &Service{
		repository: opts.Repository, unit: opts.UnitOfWork, tasks: opts.Tasks, clock: opts.Clock,
		maxAttempts: maxAttempts, retryDelays: retryDelays, retryConfigured: len(opts.RetryDelays) > 0,
	}
}

func (s *Service) AssignMissionTaskTx(ctx context.Context, tx uow.Tx, input MissionTaskAssignmentInput) (AssignmentRecord, ActivationRecord, error) {
	if s == nil || s.repository == nil {
		return AssignmentRecord{}, ActivationRecord{}, ErrInvalidContract
	}
	return s.repository.AssignMissionTaskTx(ctx, tx, input, s.now())
}

func (s *Service) ImportHistoricalMissionTaskTx(ctx context.Context, tx uow.Tx, input HistoricalMissionTaskAssignmentInput, occurredAt time.Time) (AssignmentRecord, error) {
	if s == nil || s.repository == nil {
		return AssignmentRecord{}, ErrInvalidContract
	}
	return s.repository.ImportHistoricalMissionTaskTx(ctx, tx, input, occurredAt)
}

func (s *Service) ObserveTaskReview(ctx context.Context, signal TaskReviewSignal) error {
	if s == nil || s.repository == nil || strings.TrimSpace(signal.TenantID) == "" || strings.TrimSpace(signal.TaskID) == "" {
		return ErrInvalidContract
	}
	assignment, err := s.repository.GetAssignmentForWork(ctx, signal.TenantID, WorkKindMissionTask, signal.TaskID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	task, err := s.Task(ctx, assignment.WorkID)
	if err != nil {
		return err
	}
	if signal.Approved {
		if task.Status != missiontask.TaskStatusApproved {
			return fmt.Errorf("%w: approval signal does not match task status %s", ErrConflict, task.Status)
		}
	} else if task.Status != missiontask.TaskStatusRejected && task.Status != missiontask.TaskStatusReworkRequested {
		return fmt.Errorf("%w: rejection signal does not match task status %s", ErrConflict, task.Status)
	}
	return s.synchronizeTaskState(ctx, assignment)
}

func (s *Service) SynchronizeTask(ctx context.Context, tenantID, taskID string) error {
	if s == nil || s.repository == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(taskID) == "" {
		return ErrInvalidContract
	}
	assignment, err := s.repository.GetAssignmentForWork(ctx, tenantID, WorkKindMissionTask, taskID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.synchronizeTaskState(ctx, assignment)
}

func (s *Service) EscalateAssignment(ctx context.Context, assignment AssignmentRecord) error {
	if s == nil || s.repository == nil || assignment.ID == "" {
		return ErrInvalidContract
	}
	current, err := s.repository.GetAssignment(ctx, assignment.ID)
	if err != nil {
		return err
	}
	for current.State != AssignmentStateEscalated {
		var next AssignmentState
		switch current.State {
		case AssignmentStateOffered:
			next = AssignmentStateAccepted
		case AssignmentStateAccepted, AssignmentStateWaiting:
			next = AssignmentStateActive
		case AssignmentStateActive, AssignmentStateSubmitted:
			next = AssignmentStateEscalated
		default:
			return fmt.Errorf("%w: cannot escalate assignment from %s", ErrInvalidTransition, current.State)
		}
		current, err = s.repository.TransitionAssignment(ctx, current.ID, next, s.now())
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) RecoverAssignment(ctx context.Context, assignment AssignmentRecord) (bool, error) {
	if s == nil || s.repository == nil || assignment.ID == "" || assignment.TenantID == "" || assignment.WorkID == "" {
		return false, ErrInvalidContract
	}
	advanced := false
	attempts, err := s.repository.ListAttempts(ctx, assignment.ID)
	if err != nil {
		return false, err
	}
	// A later attempt owns current progress. Replaying an older failure can
	// invalidate a newer live run or reopen a blocker after successful recovery.
	if len(attempts) > 0 {
		progressed, err := s.ensureFailedAttemptProgress(ctx, attempts[len(attempts)-1])
		if err != nil {
			return false, err
		}
		advanced = advanced || progressed
	}
	task, err := s.Task(ctx, assignment.WorkID)
	if err != nil {
		return false, err
	}
	switch task.Status {
	case missiontask.TaskStatusSubmitted, missiontask.TaskStatusInReview, missiontask.TaskStatusApproved:
		for index := len(attempts) - 1; index >= 0; index-- {
			if attempts[index].Outcome != "" {
				break
			}
			if _, err := s.repository.CompleteAttempt(ctx, attempts[index].ID, AttemptOutcomeCompleted, nil, s.now()); err != nil {
				return false, err
			}
			advanced = true
			break
		}
	case missiontask.TaskStatusRejected, missiontask.TaskStatusReworkRequested:
		advanced = true
	case missiontask.TaskStatusBlocked:
		if err := s.synchronizeTaskState(ctx, assignment); err != nil {
			return false, err
		}
		current, err := s.repository.GetAssignment(ctx, assignment.ID)
		if err != nil {
			return false, err
		}
		return current.Version != assignment.Version || current.State != assignment.State, nil
	default:
		if len(attempts) > 0 && attempts[len(attempts)-1].Outcome == AttemptOutcomeCompleted {
			if err := s.EscalateAssignment(ctx, assignment); err != nil {
				return false, err
			}
			return true, nil
		}
		return false, nil
	}
	if err := s.synchronizeTaskState(ctx, assignment); err != nil {
		return false, err
	}
	return advanced, nil
}

func (s *Service) RecordRuntimeOutcome(ctx context.Context, input RuntimeOutcomeInput) error {
	if s == nil || s.repository == nil || strings.TrimSpace(input.TenantID) == "" || strings.TrimSpace(input.TaskID) == "" || strings.TrimSpace(input.RunID) == "" || !input.Outcome.valid() || input.UsageTokens < 0 {
		return ErrInvalidContract
	}
	attempt, applied, err := s.repository.PersistRuntimeOutcome(ctx, RuntimeOutcomeRecord{
		TenantID: input.TenantID, TaskID: input.TaskID, RunID: input.RunID, Outcome: input.Outcome, Error: errorText(input.Error), TraceRef: strings.TrimSpace(input.TraceRef), UsageTokens: input.UsageTokens,
	}, s.now())
	if err != nil {
		return err
	}
	if !applied {
		return nil
	}
	assignment, err := s.repository.GetAssignment(ctx, attempt.ResponsibilityID)
	if err != nil {
		return err
	}
	if assignment.TenantID != strings.TrimSpace(input.TenantID) || assignment.WorkID != strings.TrimSpace(input.TaskID) {
		return ErrConflict
	}
	if input.Outcome == AttemptOutcomeCompleted {
		return s.synchronizeTaskState(ctx, assignment)
	}
	_, err = s.ensureFailedAttemptProgress(ctx, attempt)
	return err
}

func (s *Service) ObserveExternalSignal(ctx context.Context, signal ExternalSignal) (int, error) {
	if s == nil || s.repository == nil {
		return 0, ErrInvalidContract
	}
	return s.repository.ResolveBlockersForExternalSignal(ctx, signal, s.now())
}

func (s *Service) ensureFailedAttemptProgress(ctx context.Context, attempt AttemptRecord) (bool, error) {
	if attempt.Outcome == "" || attempt.Outcome == AttemptOutcomeCompleted {
		return false, nil
	}
	assignment, err := s.repository.GetAssignment(ctx, attempt.ResponsibilityID)
	if err != nil {
		return false, err
	}
	policy, err := s.repository.ResolvePolicy(ctx, assignment)
	if err != nil {
		return false, err
	}
	if attempt.Outcome == AttemptOutcomeBlocked {
		needed := strings.TrimSpace(attempt.Error)
		if needed == "" {
			needed = "runtime reported a blocker that requires external input"
		}
		blocker, err := s.repository.openBlocker(ctx, OpenBlockerInput{
			TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, Kind: blockerKindFromMessage(needed),
			Needed: needed, RequestedFrom: "mission_owner:" + assignment.MissionID,
			RecheckAt: s.now().Add(policy.RecheckInterval), DedupeKey: "runtime-blocked-" + attempt.ID,
		}, s.now(), true)
		return err == nil && blocker.Status == BlockerStatusOpen, err
	}
	maxAttempts := policy.MaxAttempts
	if s.maxAttempts > 0 && s.maxAttempts < maxAttempts {
		maxAttempts = s.maxAttempts
	}
	delay := s.policyRetryDelay(policy, attempt.Attempt)
	return s.repository.EnsureRetryForAttempt(ctx, attempt.ID, maxAttempts, attempt.CompletedAt.Add(delay), s.now())
}

func blockerKindFromMessage(message string) BlockerKind {
	message = strings.ToLower(message)
	switch {
	case strings.Contains(message, "permission"), strings.Contains(message, "authorization"), strings.Contains(message, "credential"):
		return BlockerKindPermission
	case strings.Contains(message, "approval"):
		return BlockerKindApproval
	case strings.Contains(message, "dependency"):
		return BlockerKindDependency
	case strings.Contains(message, "capacity"):
		return BlockerKindCapacity
	case strings.Contains(message, "external"), strings.Contains(message, "network"):
		return BlockerKindExternalSystem
	default:
		return BlockerKindInput
	}
}

func (s *Service) policyRetryDelay(policy ResponsibilityPolicyRecord, attempt int) time.Duration {
	if s.retryConfigured {
		return s.retryDelay(attempt)
	}
	if len(policy.RetryBackoff) == 0 {
		return s.retryDelay(attempt)
	}
	index := attempt - 1
	if index < 0 {
		index = 0
	}
	if index >= len(policy.RetryBackoff) {
		index = len(policy.RetryBackoff) - 1
	}
	return policy.RetryBackoff[index]
}

func (s *Service) Task(ctx context.Context, taskID string) (missiontask.TaskRecord, error) {
	if s == nil || s.unit == nil || s.tasks == nil || strings.TrimSpace(taskID) == "" {
		return missiontask.TaskRecord{}, ErrInvalidContract
	}
	var record missiontask.TaskRecord
	err := s.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		var err error
		record, err = s.tasks.GetTask(ctx, tx, missiontask.TaskID(strings.TrimSpace(taskID)))
		return err
	})
	return record, err
}

func (s *Service) EvidenceCompleteness(ctx context.Context, task missiontask.TaskRecord) (float64, error) {
	if s == nil || s.unit == nil || s.tasks == nil || task.Contract.ID.IsZero() {
		return 0, ErrInvalidContract
	}
	requiredEvidence := make([]missiontask.EvidenceRequirementID, 0)
	for _, requirement := range task.Contract.EvidenceRequirements {
		if requirement.Required {
			requiredEvidence = append(requiredEvidence, requirement.ID)
		}
	}
	total := len(requiredEvidence) + len(task.Contract.AcceptanceCriteria)
	if total == 0 {
		return 1, nil
	}
	repository, ok := s.tasks.(TaskEvidenceRepositoryPort)
	if !ok {
		return 0, ErrInvalidContract
	}
	complete := 0
	err := s.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		submission, found, err := repository.LatestSubmission(ctx, tx, task.Contract.ID)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		evidence, err := repository.ListEvidenceSubmissions(ctx, tx, submission.ID)
		if err != nil {
			return err
		}
		requirements := map[string]bool{}
		criteria := map[string]bool{}
		for _, submission := range evidence {
			if !submission.Evidence.HasTraceableSource() {
				continue
			}
			if id := strings.TrimSpace(submission.Evidence.EvidenceRequirementID); id != "" {
				requirements[id] = true
			}
			if id := strings.TrimSpace(submission.Evidence.AcceptanceCriterionID); id != "" {
				criteria[id] = true
			}
		}
		for _, id := range requiredEvidence {
			if requirements[string(id)] {
				complete++
			}
		}
		for _, criterion := range task.Contract.AcceptanceCriteria {
			if criteria[string(criterion.ID)] {
				complete++
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return float64(complete) / float64(total), nil
}

func (s *Service) DependenciesSatisfied(ctx context.Context, task missiontask.TaskRecord) (bool, error) {
	for _, dependency := range task.Contract.Dependencies {
		record, err := s.Task(ctx, dependency.TaskID.String())
		if err != nil {
			if errors.Is(err, missiontask.ErrNotFound) {
				return false, nil
			}
			return false, err
		}
		if record.Contract.MissionID != task.Contract.MissionID || record.Status != missiontask.TaskStatusApproved {
			return false, nil
		}
	}
	return true, nil
}

func (s *Service) synchronizeTaskState(ctx context.Context, assignment AssignmentRecord) error {
	task, err := s.Task(ctx, assignment.WorkID)
	if err != nil {
		return err
	}
	switch task.Status {
	case missiontask.TaskStatusApproved:
		return s.advanceToFulfilled(ctx, assignment)
	case missiontask.TaskStatusSubmitted, missiontask.TaskStatusInReview:
		return s.advanceToSubmitted(ctx, assignment)
	case missiontask.TaskStatusRejected, missiontask.TaskStatusReworkRequested:
		_, err := s.repository.Reactivate(ctx, assignment.ID, ActivationCauseReviewRejected, s.now(), s.now())
		return err
	case missiontask.TaskStatusBlocked:
		if assignmentTerminal(assignment.State) {
			return nil
		}
		policy, err := s.repository.ResolvePolicy(ctx, assignment)
		if err != nil {
			return err
		}
		needed := strings.TrimSpace(task.LastTransitionReason)
		if needed == "" {
			needed = "mission task reported a blocker that requires external input"
		}
		_, err = s.repository.OpenBlocker(ctx, OpenBlockerInput{
			TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, Kind: blockerKindFromMessage(needed),
			Needed: needed, RequestedFrom: "mission_owner:" + assignment.MissionID,
			RecheckAt: s.now().Add(policy.RecheckInterval),
			DedupeKey: fmt.Sprintf("task-blocked-%s-v%d", assignment.WorkID, task.Version),
		}, s.now())
		return err
	default:
		// A completed Runtime is only an Attempt result. The responsibility
		// stays active until Delivery and QA move the task through their gates.
		return nil
	}
}

func (s *Service) advanceToSubmitted(ctx context.Context, assignment AssignmentRecord) error {
	current := assignment
	var err error
	for current.State != AssignmentStateSubmitted {
		next, ok := nextAssignmentState(current.State, AssignmentStateSubmitted)
		if !ok {
			return fmt.Errorf("%w: cannot advance %s to submitted", ErrInvalidTransition, current.State)
		}
		current, err = s.repository.TransitionAssignment(ctx, current.ID, next, s.now())
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) advanceToFulfilled(ctx context.Context, assignment AssignmentRecord) error {
	current := assignment
	var err error
	for current.State != AssignmentStateFulfilled {
		next, ok := nextAssignmentState(current.State, AssignmentStateFulfilled)
		if !ok {
			return fmt.Errorf("%w: cannot advance %s to fulfilled", ErrInvalidTransition, current.State)
		}
		current, err = s.repository.TransitionAssignment(ctx, current.ID, next, s.now())
		if err != nil {
			return err
		}
	}
	return nil
}

func nextAssignmentState(current AssignmentState, target AssignmentState) (AssignmentState, bool) {
	if current == target {
		return target, true
	}
	switch current {
	case AssignmentStateOffered:
		return AssignmentStateAccepted, true
	case AssignmentStateAccepted, AssignmentStateWaiting, AssignmentStateEscalated:
		return AssignmentStateActive, true
	case AssignmentStateActive:
		return AssignmentStateSubmitted, true
	case AssignmentStateSubmitted:
		if target == AssignmentStateFulfilled {
			return AssignmentStateFulfilled, true
		}
	}
	return "", false
}

func (s *Service) retryDelay(attempt int) time.Duration {
	if len(s.retryDelays) == 0 {
		return 0
	}
	index := attempt - 1
	if index < 0 {
		index = 0
	}
	if index >= len(s.retryDelays) {
		index = len(s.retryDelays) - 1
	}
	if s.retryDelays[index] < 0 {
		return 0
	}
	return s.retryDelays[index]
}

func (s *Service) now() time.Time {
	if s != nil && s.clock != nil {
		return s.clock.Now().UTC()
	}
	return time.Now().UTC()
}
