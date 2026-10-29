package responsibility

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidContract   = errors.New("responsibility contract is invalid")
	ErrInvalidTransition = errors.New("responsibility state transition is invalid")
	ErrCompletionBlocked = errors.New("responsibility completion gate is not satisfied")
	ErrAutoplanCapacity  = errors.New("responsibility autonomous-plan capacity is exhausted")
	ErrDispatchRejected  = errors.New("responsibility dispatch was rejected")
	ErrRuntimeTerminal   = errors.New("responsibility runtime is already terminal")
)

type WorkKind string

const (
	WorkKindMissionTask   WorkKind = "mission_task"
	WorkKindTodo          WorkKind = "todo"
	WorkKindScheduledDuty WorkKind = "scheduled_duty"
	WorkKindMention       WorkKind = "mention"
)

type AssignmentState string

const (
	AssignmentStateOffered    AssignmentState = "offered"
	AssignmentStateAccepted   AssignmentState = "accepted"
	AssignmentStateActive     AssignmentState = "active"
	AssignmentStateWaiting    AssignmentState = "waiting"
	AssignmentStateSubmitted  AssignmentState = "submitted"
	AssignmentStateFulfilled  AssignmentState = "fulfilled"
	AssignmentStateDeclined   AssignmentState = "declined"
	AssignmentStateEscalated  AssignmentState = "escalated"
	AssignmentStateReassigned AssignmentState = "reassigned"
	AssignmentStateTerminated AssignmentState = "terminated"
)

type ActivationStatus string

const (
	ActivationStatusPending    ActivationStatus = "pending"
	ActivationStatusClaimed    ActivationStatus = "claimed"
	ActivationStatusDispatched ActivationStatus = "dispatched"
	ActivationStatusCompleted  ActivationStatus = "completed"
	ActivationStatusSuperseded ActivationStatus = "superseded"
)

type ActivationCause string

const (
	ActivationCauseAssigned           ActivationCause = "assigned"
	ActivationCauseDependencyResolved ActivationCause = "dependency_resolved"
	ActivationCauseRetryDue           ActivationCause = "retry_due"
	ActivationCauseBlockerResolved    ActivationCause = "blocker_resolved"
	ActivationCauseReviewRejected     ActivationCause = "review_rejected"
	ActivationCauseRecheckDue         ActivationCause = "recheck_due"
	ActivationCauseExternalSignal     ActivationCause = "external_signal"
	ActivationCauseManualRetry        ActivationCause = "manual_retry"
	ActivationCauseReassigned         ActivationCause = "reassigned"
)

type AttemptOutcome string

const (
	AttemptOutcomeCompleted AttemptOutcome = "completed"
	AttemptOutcomeBlocked   AttemptOutcome = "blocked"
	AttemptOutcomeFailed    AttemptOutcome = "failed"
	AttemptOutcomeTimedOut  AttemptOutcome = "timed_out"
	AttemptOutcomeCancelled AttemptOutcome = "cancelled"
)

type BlockerKind string

const (
	BlockerKindInput          BlockerKind = "input"
	BlockerKindPermission     BlockerKind = "permission"
	BlockerKindDependency     BlockerKind = "dependency"
	BlockerKindApproval       BlockerKind = "approval"
	BlockerKindCapacity       BlockerKind = "capacity"
	BlockerKindBudget         BlockerKind = "budget"
	BlockerKindRisk           BlockerKind = "risk"
	BlockerKindExternalSystem BlockerKind = "external_system"
)

type BlockerStatus string

const (
	BlockerStatusOpen       BlockerStatus = "open"
	BlockerStatusResolved   BlockerStatus = "resolved"
	BlockerStatusSuperseded BlockerStatus = "superseded"
)

type AutonomyLevel string

const (
	AutonomyAdvise            AutonomyLevel = "advise"
	AutonomyExecuteReversible AutonomyLevel = "execute_reversible"
	AutonomyExecuteScoped     AutonomyLevel = "execute_scoped"
	AutonomyContinuous        AutonomyLevel = "continuous"
)

type ResponsibilityAssignment struct {
	ID               string
	TenantID         string
	WorkKind         WorkKind
	WorkID           string
	ActorMemberID    string
	AccountableAgent string
	State            AssignmentState
	Priority         int
	AcceptBy         time.Time
	DueAt            time.Time
	PolicyRef        string
	Version          int64
}

func (assignment ResponsibilityAssignment) Validate() error {
	if blank(assignment.ID) || blank(assignment.TenantID) || blank(assignment.WorkID) || blank(assignment.AccountableAgent) {
		return fmt.Errorf("%w: assignment identity, tenant, work, and accountable Agent are required", ErrInvalidContract)
	}
	if !assignment.WorkKind.valid() || !assignment.State.valid() {
		return fmt.Errorf("%w: unsupported work kind or assignment state", ErrInvalidContract)
	}
	if assignment.Version < 1 {
		return fmt.Errorf("%w: assignment version must be positive", ErrInvalidContract)
	}
	if assignment.Priority < 0 || assignment.AcceptBy.IsZero() {
		return fmt.Errorf("%w: assignment priority cannot be negative and acceptance deadline is required", ErrInvalidContract)
	}
	if !assignment.AcceptBy.IsZero() && !assignment.DueAt.IsZero() && assignment.DueAt.Before(assignment.AcceptBy) {
		return fmt.Errorf("%w: due time precedes acceptance deadline", ErrInvalidContract)
	}
	return nil
}

type ResponsibilityActivation struct {
	ID                string
	TenantID          string
	ResponsibilityID  string
	Cause             ActivationCause
	NotBefore         time.Time
	DedupeKey         string
	AssignmentVersion int64
	Status            ActivationStatus
}

func (activation ResponsibilityActivation) Validate() error {
	if blank(activation.ID) || blank(activation.TenantID) || blank(activation.ResponsibilityID) || blank(activation.DedupeKey) {
		return fmt.Errorf("%w: activation identity, tenant, responsibility, and dedupe key are required", ErrInvalidContract)
	}
	if !activation.Cause.valid() || !activation.Status.valid() {
		return fmt.Errorf("%w: unsupported activation cause or status", ErrInvalidContract)
	}
	if activation.AssignmentVersion < 1 {
		return fmt.Errorf("%w: activation assignment version must be positive", ErrInvalidContract)
	}
	if activation.DedupeKey != ActivationDedupeKey(activation.TenantID, activation.ResponsibilityID, activation.Cause, activation.AssignmentVersion) {
		return fmt.Errorf("%w: activation dedupe key does not match its tenant, responsibility, cause, and assignment version", ErrInvalidContract)
	}
	return nil
}

func ActivationDedupeKey(tenantID string, responsibilityID string, cause ActivationCause, assignmentVersion int64) string {
	payload, _ := json.Marshal(struct {
		TenantID          string          `json:"tenant_id"`
		ResponsibilityID  string          `json:"responsibility_id"`
		Cause             ActivationCause `json:"cause"`
		AssignmentVersion int64           `json:"assignment_version"`
	}{
		TenantID:          strings.TrimSpace(tenantID),
		ResponsibilityID:  strings.TrimSpace(responsibilityID),
		Cause:             cause,
		AssignmentVersion: assignmentVersion,
	})
	return fmt.Sprintf("responsibility_activation:%x", sha256.Sum256(payload))
}

type ResponsibilityAttempt struct {
	ID               string
	TenantID         string
	ResponsibilityID string
	AgentRunID       string
	ClaimToken       string
	Attempt          int
	StartedAt        time.Time
	HeartbeatAt      time.Time
	Outcome          AttemptOutcome
}

func (attempt ResponsibilityAttempt) Validate() error {
	if blank(attempt.ID) || blank(attempt.TenantID) || blank(attempt.ResponsibilityID) || blank(attempt.AgentRunID) || blank(attempt.ClaimToken) {
		return fmt.Errorf("%w: attempt identity, tenant, responsibility, run, and claim token are required", ErrInvalidContract)
	}
	if attempt.Attempt < 1 || attempt.StartedAt.IsZero() {
		return fmt.Errorf("%w: attempt number and start time are required", ErrInvalidContract)
	}
	if !attempt.HeartbeatAt.IsZero() && attempt.HeartbeatAt.Before(attempt.StartedAt) {
		return fmt.Errorf("%w: heartbeat precedes attempt start", ErrInvalidContract)
	}
	if attempt.Outcome != "" && !attempt.Outcome.valid() {
		return fmt.Errorf("%w: unsupported attempt outcome", ErrInvalidContract)
	}
	return nil
}

type ResponsibilityBlocker struct {
	ID               string
	TenantID         string
	ResponsibilityID string
	DedupeKey        string
	Kind             BlockerKind
	Needed           string
	RequestedFrom    string
	RecheckAt        time.Time
	EscalationLevel  int
	Status           BlockerStatus
}

func (blocker ResponsibilityBlocker) Validate() error {
	if blank(blocker.ID) || blank(blocker.TenantID) || blank(blocker.ResponsibilityID) || blank(blocker.DedupeKey) || blank(blocker.Needed) || blank(blocker.RequestedFrom) {
		return fmt.Errorf("%w: blocker identity, tenant, responsibility, dedupe key, required input, and waiting party are required", ErrInvalidContract)
	}
	if !blocker.Kind.valid() || !blocker.Status.valid() {
		return fmt.Errorf("%w: unsupported blocker kind or status", ErrInvalidContract)
	}
	if blocker.Status == BlockerStatusOpen && blocker.RecheckAt.IsZero() {
		return fmt.Errorf("%w: open blocker requires a recheck time", ErrInvalidContract)
	}
	if blocker.EscalationLevel < 0 {
		return fmt.Errorf("%w: escalation level cannot be negative", ErrInvalidContract)
	}
	return nil
}

type CompletionSubmission struct {
	Summary             string
	DeliverableRefs     []string
	EvidenceRefs        []string
	RequiredEvidence    int
	QARequired          bool
	QAApproved          bool
	HumanReviewRequired bool
	HumanApproved       bool
}

func (submission CompletionSubmission) ValidateForFulfillment() error {
	if blank(submission.Summary) || len(nonBlank(submission.DeliverableRefs)) == 0 {
		return fmt.Errorf("%w: result summary and at least one deliverable are required", ErrCompletionBlocked)
	}
	if submission.RequiredEvidence < 1 || len(nonBlank(submission.EvidenceRefs)) < submission.RequiredEvidence {
		return fmt.Errorf("%w: required evidence is incomplete", ErrCompletionBlocked)
	}
	if submission.QARequired && !submission.QAApproved {
		return fmt.Errorf("%w: QA approval is required", ErrCompletionBlocked)
	}
	if submission.HumanReviewRequired && !submission.HumanApproved {
		return fmt.Errorf("%w: human approval is required", ErrCompletionBlocked)
	}
	return nil
}

type TraceLinks struct {
	TenantID       string
	CorrelationID  string
	MissionID      string
	MissionTaskID  string
	RuntimeRunID   string
	ScheduledRunID string
}

func (links TraceLinks) Validate() error {
	if blank(links.TenantID) || blank(links.CorrelationID) {
		return fmt.Errorf("%w: tenant and correlation ID are required", ErrInvalidContract)
	}
	if blank(links.MissionTaskID) && blank(links.RuntimeRunID) && blank(links.ScheduledRunID) {
		return fmt.Errorf("%w: at least one execution link is required", ErrInvalidContract)
	}
	if !blank(links.MissionTaskID) && blank(links.MissionID) {
		return fmt.Errorf("%w: Mission Task link requires Mission ID", ErrInvalidContract)
	}
	return nil
}

func ValidateAssignmentTransition(from AssignmentState, to AssignmentState) error {
	if !from.valid() || !to.valid() {
		return fmt.Errorf("%w: unsupported assignment state", ErrInvalidTransition)
	}
	if assignmentTransitions[from][to] {
		return nil
	}
	return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, from, to)
}

func ValidateActivationTransition(from ActivationStatus, to ActivationStatus) error {
	if !from.valid() || !to.valid() {
		return fmt.Errorf("%w: unsupported activation status", ErrInvalidTransition)
	}
	if activationTransitions[from][to] {
		return nil
	}
	return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, from, to)
}

var assignmentTransitions = map[AssignmentState]map[AssignmentState]bool{
	AssignmentStateOffered: {
		AssignmentStateAccepted:   true,
		AssignmentStateDeclined:   true,
		AssignmentStateReassigned: true,
		AssignmentStateTerminated: true,
	},
	AssignmentStateAccepted: {
		AssignmentStateActive:     true,
		AssignmentStateReassigned: true,
		AssignmentStateTerminated: true,
	},
	AssignmentStateActive: {
		AssignmentStateWaiting:    true,
		AssignmentStateSubmitted:  true,
		AssignmentStateEscalated:  true,
		AssignmentStateReassigned: true,
		AssignmentStateTerminated: true,
	},
	AssignmentStateWaiting: {
		AssignmentStateActive:     true,
		AssignmentStateEscalated:  true,
		AssignmentStateReassigned: true,
		AssignmentStateTerminated: true,
	},
	AssignmentStateSubmitted: {
		AssignmentStateActive:     true,
		AssignmentStateFulfilled:  true,
		AssignmentStateEscalated:  true,
		AssignmentStateTerminated: true,
	},
	AssignmentStateEscalated: {
		AssignmentStateActive:     true,
		AssignmentStateReassigned: true,
		AssignmentStateTerminated: true,
	},
}

var activationTransitions = map[ActivationStatus]map[ActivationStatus]bool{
	ActivationStatusPending: {
		ActivationStatusClaimed:    true,
		ActivationStatusSuperseded: true,
	},
	ActivationStatusClaimed: {
		ActivationStatusDispatched: true,
		ActivationStatusSuperseded: true,
	},
	ActivationStatusDispatched: {
		ActivationStatusCompleted:  true,
		ActivationStatusSuperseded: true,
	},
}

func (kind WorkKind) valid() bool {
	switch kind {
	case WorkKindMissionTask, WorkKindTodo, WorkKindScheduledDuty, WorkKindMention:
		return true
	default:
		return false
	}
}

func (state AssignmentState) valid() bool {
	switch state {
	case AssignmentStateOffered, AssignmentStateAccepted, AssignmentStateActive, AssignmentStateWaiting,
		AssignmentStateSubmitted, AssignmentStateFulfilled, AssignmentStateDeclined, AssignmentStateEscalated,
		AssignmentStateReassigned, AssignmentStateTerminated:
		return true
	default:
		return false
	}
}

func (status ActivationStatus) valid() bool {
	switch status {
	case ActivationStatusPending, ActivationStatusClaimed, ActivationStatusDispatched, ActivationStatusCompleted, ActivationStatusSuperseded:
		return true
	default:
		return false
	}
}

func (cause ActivationCause) valid() bool {
	switch cause {
	case ActivationCauseAssigned, ActivationCauseDependencyResolved, ActivationCauseRetryDue, ActivationCauseBlockerResolved,
		ActivationCauseReviewRejected, ActivationCauseRecheckDue, ActivationCauseExternalSignal,
		ActivationCauseManualRetry, ActivationCauseReassigned:
		return true
	default:
		return false
	}
}

func (outcome AttemptOutcome) valid() bool {
	switch outcome {
	case AttemptOutcomeCompleted, AttemptOutcomeBlocked, AttemptOutcomeFailed, AttemptOutcomeTimedOut, AttemptOutcomeCancelled:
		return true
	default:
		return false
	}
}

func (kind BlockerKind) valid() bool {
	switch kind {
	case BlockerKindInput, BlockerKindPermission, BlockerKindDependency, BlockerKindApproval, BlockerKindCapacity, BlockerKindBudget, BlockerKindRisk, BlockerKindExternalSystem:
		return true
	default:
		return false
	}
}

func (status BlockerStatus) valid() bool {
	switch status {
	case BlockerStatusOpen, BlockerStatusResolved, BlockerStatusSuperseded:
		return true
	default:
		return false
	}
}

func blank(value string) bool { return strings.TrimSpace(value) == "" }

func nonBlank(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if clean := strings.TrimSpace(value); clean != "" {
			out = append(out, clean)
		}
	}
	return out
}
