package responsibility

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/clock"
)

type ResponsibilitySummaryDTO struct {
	Total            int `json:"total"`
	Now              int `json:"now"`
	Next             int `json:"next"`
	Waiting          int `json:"waiting"`
	Review           int `json:"review"`
	Risk             int `json:"risk"`
	Paused           int `json:"paused"`
	Overdue          int `json:"overdue"`
	SilentStalls     int `json:"silent_stalls"`
	RepeatedFailures int `json:"repeated_failures"`
}

type ResponsibilityMetricsDTO struct {
	AssignmentToAcceptSeconds float64 `json:"assignment_to_accept_seconds"`
	AutonomousClosureRate     float64 `json:"autonomous_closure_rate"`
	SilentStallRate           float64 `json:"silent_stall_rate"`
	AverageBlockedSeconds     float64 `json:"average_blocked_seconds"`
	FirstPassAcceptanceRate   float64 `json:"first_pass_acceptance_rate"`
	EvidenceCompleteness      float64 `json:"evidence_completeness"`
	DuplicateSideEffectRate   float64 `json:"duplicate_side_effect_rate"`
}

type ResponsibilityInboxItemDTO struct {
	ResponsibilityID         string                  `json:"responsibility_id"`
	TenantID                 string                  `json:"tenant_id"`
	WorkKind                 WorkKind                `json:"work_kind"`
	WorkID                   string                  `json:"work_id"`
	MissionID                string                  `json:"mission_id,omitempty"`
	SourceKind               string                  `json:"source_kind,omitempty"`
	SourceID                 string                  `json:"source_id,omitempty"`
	SourceStepID             string                  `json:"source_step_id,omitempty"`
	Title                    string                  `json:"title"`
	Goal                     string                  `json:"goal"`
	AccountableAgent         string                  `json:"accountable_agent"`
	State                    AssignmentState         `json:"state"`
	Lane                     string                  `json:"lane"`
	Health                   string                  `json:"health"`
	RiskReasons              []string                `json:"risk_reasons"`
	NextAction               string                  `json:"next_action"`
	NextActor                string                  `json:"next_actor"`
	Priority                 int                     `json:"priority"`
	AcceptBy                 time.Time               `json:"accept_by"`
	AssignedAt               time.Time               `json:"assigned_at"`
	AcceptedAt               time.Time               `json:"accepted_at,omitempty"`
	DueAt                    time.Time               `json:"due_at,omitempty"`
	Paused                   bool                    `json:"paused"`
	PauseReason              string                  `json:"pause_reason,omitempty"`
	AttemptCount             int                     `json:"attempt_count"`
	BlockedSeconds           float64                 `json:"blocked_seconds"`
	EvidenceCompleteness     float64                 `json:"evidence_completeness"`
	RuntimeOutcomeDeliveries int                     `json:"runtime_outcome_deliveries"`
	DuplicateRuntimeOutcomes int                     `json:"duplicate_runtime_outcomes"`
	OpenBlockers             []BlockerDTO            `json:"open_blockers"`
	OpenAlerts               []SupervisionAlertDTO   `json:"open_alerts"`
	Policy                   ResponsibilityPolicyDTO `json:"policy"`
	Version                  int64                   `json:"version"`
	UpdatedAt                time.Time               `json:"updated_at"`
}

type ResponsibilityPolicyDTO struct {
	PolicyID                string              `json:"policy_id"`
	AgentProfileID          string              `json:"agent_profile_id"`
	WorkKind                WorkKind            `json:"work_kind"`
	AutonomyLevel           AutonomyLevel       `json:"autonomy_level"`
	MaxParallel             int                 `json:"max_parallel"`
	AcceptTimeoutSeconds    int64               `json:"accept_timeout_seconds"`
	HeartbeatTimeoutSeconds int64               `json:"heartbeat_timeout_seconds"`
	MaxAttempts             int                 `json:"max_attempts"`
	RetryBackoffSeconds     []int64             `json:"retry_backoff_seconds"`
	MaxCostPerTask          float64             `json:"max_cost_per_task"`
	MaxTokenBudget          int64               `json:"max_token_budget"`
	ApprovalPolicy          ApprovalPolicy      `json:"approval_policy"`
	AllowedRiskLevels       []string            `json:"allowed_risk_levels"`
	ToolScopes              []string            `json:"tool_scopes"`
	DataScopes              []string            `json:"data_scopes"`
	EscalationChain         []EscalationStepDTO `json:"escalation_chain"`
	RecheckIntervalSeconds  int64               `json:"recheck_interval_seconds"`
	DeadlineWarningSeconds  int64               `json:"deadline_warning_seconds"`
	Enabled                 bool                `json:"enabled"`
	Version                 int64               `json:"version"`
	UpdatedAt               time.Time           `json:"updated_at,omitempty"`
}

type EscalationStepDTO struct {
	SubjectKind  string `json:"subject_kind"`
	SubjectID    string `json:"subject_id"`
	AfterSeconds int64  `json:"after_seconds"`
}

type BlockerDTO struct {
	BlockerID       string        `json:"blocker_id"`
	DedupeKey       string        `json:"dedupe_key"`
	Kind            BlockerKind   `json:"kind"`
	Needed          string        `json:"needed"`
	RequestedFrom   string        `json:"requested_from"`
	RecheckAt       time.Time     `json:"recheck_at"`
	EscalationLevel int           `json:"escalation_level"`
	Status          BlockerStatus `json:"status"`
	Resolution      string        `json:"resolution,omitempty"`
	Version         int64         `json:"version"`
	CreatedAt       time.Time     `json:"created_at"`
	ResolvedAt      time.Time     `json:"resolved_at,omitempty"`
}

type SupervisionAlertDTO struct {
	AlertID          string                 `json:"alert_id"`
	Kind             SupervisionAlertKind   `json:"kind"`
	Severity         string                 `json:"severity"`
	RequestedFrom    string                 `json:"requested_from"`
	DueAt            time.Time              `json:"due_at"`
	NextEscalationAt time.Time              `json:"next_escalation_at,omitempty"`
	EscalationLevel  int                    `json:"escalation_level"`
	Status           SupervisionAlertStatus `json:"status"`
	Resolution       string                 `json:"resolution,omitempty"`
	Version          int64                  `json:"version"`
	CreatedAt        time.Time              `json:"created_at"`
	ResolvedAt       time.Time              `json:"resolved_at,omitempty"`
}

type AttemptDTO struct {
	AttemptID    string         `json:"attempt_id"`
	ActivationID string         `json:"activation_id"`
	RunID        string         `json:"run_id"`
	RuntimeKind  string         `json:"runtime_kind"`
	Attempt      int            `json:"attempt"`
	Outcome      AttemptOutcome `json:"outcome,omitempty"`
	UsageTokens  int64          `json:"usage_tokens"`
	StartedAt    time.Time      `json:"started_at"`
	HeartbeatAt  time.Time      `json:"heartbeat_at,omitempty"`
	CompletedAt  time.Time      `json:"completed_at,omitempty"`
	Error        string         `json:"error,omitempty"`
}

type ActivationDTO struct {
	ActivationID string           `json:"activation_id"`
	Cause        ActivationCause  `json:"cause"`
	Status       ActivationStatus `json:"status"`
	NotBefore    time.Time        `json:"not_before"`
	RunID        string           `json:"run_id,omitempty"`
	LastError    string           `json:"last_error,omitempty"`
	Version      int64            `json:"assignment_version"`
}

type AuditDTO struct {
	AuditID    string    `json:"audit_id"`
	EntityType string    `json:"entity_type"`
	EntityID   string    `json:"entity_id"`
	Action     string    `json:"action"`
	ActorID    string    `json:"actor_id,omitempty"`
	FromState  string    `json:"from_state,omitempty"`
	ToState    string    `json:"to_state,omitempty"`
	Details    string    `json:"details,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

type ResponsibilityInboxPageDTO struct {
	Items     []ResponsibilityInboxItemDTO `json:"items"`
	Summary   ResponsibilitySummaryDTO     `json:"summary"`
	Metrics   ResponsibilityMetricsDTO     `json:"metrics"`
	UpdatedAt time.Time                    `json:"updated_at"`
}

type ResponsibilityDetailDTO struct {
	ResponsibilityInboxItemDTO
	Attempts    []AttemptDTO          `json:"attempts"`
	Activations []ActivationDTO       `json:"activations"`
	Blockers    []BlockerDTO          `json:"blockers"`
	Alerts      []SupervisionAlertDTO `json:"alerts"`
	Audit       []AuditDTO            `json:"audit"`
}

type PolicyUpdate struct {
	TenantID                string
	ActorID                 string
	IdempotencyKey          string
	AgentProfileID          string
	WorkKind                WorkKind
	AutonomyLevel           AutonomyLevel
	MaxParallel             int
	AcceptTimeoutSeconds    int64
	HeartbeatTimeoutSeconds int64
	MaxAttempts             int
	RetryBackoffSeconds     []int64
	MaxCostPerTask          float64
	MaxTokenBudget          int64
	ApprovalPolicy          ApprovalPolicy
	AllowedRiskLevels       []string
	ToolScopes              []string
	DataScopes              []string
	EscalationChain         []EscalationStepDTO
	RecheckIntervalSeconds  int64
	DeadlineWarningSeconds  int64
	Enabled                 bool
	ExpectedVersion         int64
}

type OperationsServiceOptions struct {
	Repository       *Repository
	Service          *Service
	Profiles         AgentProfilePort
	RuntimeControl   RuntimeController
	TaskReassignment TaskReassignmentPort
	Clock            clock.Clock
}

type TaskReassignmentRequest struct {
	TenantID        string
	MissionID       string
	TaskID          string
	AgentProfileID  string
	ActorID         string
	Reason          string
	IdempotencyKey  string
	ExpectedVersion int64
}

type TaskReassignmentPort interface {
	ReassignTask(context.Context, TaskReassignmentRequest) error
}

type OperationsService struct {
	repository       *Repository
	service          *Service
	profiles         AgentProfilePort
	runtimeControl   RuntimeController
	taskReassignment TaskReassignmentPort
	clock            clock.Clock
}

func NewOperationsService(opts OperationsServiceOptions) *OperationsService {
	return &OperationsService{repository: opts.Repository, service: opts.Service, profiles: opts.Profiles, runtimeControl: opts.RuntimeControl, taskReassignment: opts.TaskReassignment, clock: opts.Clock}
}

func (s *OperationsService) ListInbox(ctx context.Context, tenantID, agentID string, states []AssignmentState, limit int) (ResponsibilityInboxPageDTO, error) {
	if s == nil || s.repository == nil || s.service == nil || strings.TrimSpace(tenantID) == "" {
		return ResponsibilityInboxPageDTO{}, ErrInvalidContract
	}
	assignments, err := s.repository.ListAssignments(ctx, AssignmentListQuery{TenantID: tenantID, AgentID: agentID, States: states, Limit: limit})
	if err != nil {
		return ResponsibilityInboxPageDTO{}, err
	}
	page := ResponsibilityInboxPageDTO{Items: make([]ResponsibilityInboxItemDTO, 0, len(assignments)), UpdatedAt: s.now()}
	for _, assignment := range assignments {
		item, err := s.inboxItem(ctx, assignment)
		if err != nil {
			return ResponsibilityInboxPageDTO{}, err
		}
		page.Items = append(page.Items, item)
		accumulateSummary(&page.Summary, item)
	}
	page.Metrics = calculateMetrics(page.Items)
	return page, nil
}

func (s *OperationsService) Get(ctx context.Context, tenantID, responsibilityID string) (ResponsibilityDetailDTO, error) {
	if s == nil || s.repository == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(responsibilityID) == "" {
		return ResponsibilityDetailDTO{}, ErrInvalidContract
	}
	assignment, err := s.repository.GetAssignment(ctx, responsibilityID)
	if err != nil {
		return ResponsibilityDetailDTO{}, err
	}
	if assignment.TenantID != strings.TrimSpace(tenantID) {
		return ResponsibilityDetailDTO{}, ErrNotFound
	}
	item, err := s.inboxItem(ctx, assignment)
	if err != nil {
		return ResponsibilityDetailDTO{}, err
	}
	attempts, err := s.repository.ListAttempts(ctx, assignment.ID)
	if err != nil {
		return ResponsibilityDetailDTO{}, err
	}
	activations, err := s.repository.ListActivations(ctx, tenantID, assignment.ID)
	if err != nil {
		return ResponsibilityDetailDTO{}, err
	}
	blockers, err := s.repository.ListBlockers(ctx, tenantID, assignment.ID, false)
	if err != nil {
		return ResponsibilityDetailDTO{}, err
	}
	alerts, err := s.repository.ListSupervisionAlerts(ctx, tenantID, assignment.ID, false)
	if err != nil {
		return ResponsibilityDetailDTO{}, err
	}
	audits, err := s.repository.ListAudits(ctx, tenantID, assignment.ID, 200)
	if err != nil {
		return ResponsibilityDetailDTO{}, err
	}
	detail := ResponsibilityDetailDTO{ResponsibilityInboxItemDTO: item, Attempts: mapAttempts(attempts), Activations: mapActivations(activations), Blockers: mapBlockers(blockers), Alerts: mapSupervisionAlerts(alerts), Audit: mapAudits(audits)}
	return detail, nil
}

func (s *OperationsService) GetPolicy(ctx context.Context, tenantID, agentID string, kind WorkKind) (ResponsibilityPolicyDTO, error) {
	policy, err := s.repository.GetPolicy(ctx, tenantID, agentID, kind)
	if errors.Is(err, ErrNotFound) {
		policy = DefaultPolicy(tenantID, agentID, kind)
		err = nil
	}
	return mapPolicy(policy), err
}

func (s *OperationsService) SavePolicy(ctx context.Context, update PolicyUpdate) (ResponsibilityPolicyDTO, error) {
	if s == nil || s.repository == nil || s.profiles == nil {
		return ResponsibilityPolicyDTO{}, ErrInvalidContract
	}
	profile, err := s.profiles.GetProfile(ctx, agentcore.AgentProfileID(strings.TrimSpace(update.AgentProfileID)))
	if err != nil {
		return ResponsibilityPolicyDTO{}, err
	}
	if profile.ID.String() != strings.TrimSpace(update.AgentProfileID) {
		return ResponsibilityPolicyDTO{}, ErrConflict
	}
	retry := make([]time.Duration, 0, len(update.RetryBackoffSeconds))
	for _, seconds := range update.RetryBackoffSeconds {
		retry = append(retry, time.Duration(seconds)*time.Second)
	}
	chain := make([]EscalationTarget, 0, len(update.EscalationChain))
	for _, target := range update.EscalationChain {
		chain = append(chain, EscalationTarget{SubjectKind: target.SubjectKind, SubjectID: target.SubjectID, After: time.Duration(target.AfterSeconds) * time.Second})
	}
	policy, err := s.repository.SavePolicy(ctx, ResponsibilityPolicyRecord{
		TenantID: update.TenantID, AgentProfileID: update.AgentProfileID, WorkKind: update.WorkKind,
		AutonomyLevel: update.AutonomyLevel, MaxParallel: update.MaxParallel,
		AcceptTimeout:    time.Duration(update.AcceptTimeoutSeconds) * time.Second,
		HeartbeatTimeout: time.Duration(update.HeartbeatTimeoutSeconds) * time.Second,
		MaxAttempts:      update.MaxAttempts, RetryBackoff: retry, MaxCostPerTask: update.MaxCostPerTask,
		MaxTokenBudget: update.MaxTokenBudget, ApprovalPolicy: update.ApprovalPolicy,
		AllowedRiskLevels: update.AllowedRiskLevels, ToolScopes: update.ToolScopes, DataScopes: update.DataScopes,
		EscalationChain: chain, RecheckInterval: time.Duration(update.RecheckIntervalSeconds) * time.Second,
		DeadlineWarningWindow: time.Duration(update.DeadlineWarningSeconds) * time.Second, Enabled: update.Enabled,
	}, update.ActorID, update.IdempotencyKey, update.ExpectedVersion, s.now())
	return mapPolicy(policy), err
}

func (s *OperationsService) Pause(ctx context.Context, command AssignmentControl) (ResponsibilityDetailDTO, error) {
	assignment, err := s.repository.PauseAssignment(ctx, command, s.now())
	if err != nil {
		return ResponsibilityDetailDTO{}, err
	}
	if err := s.cancelLiveAttempts(ctx, command.TenantID, command.AssignmentID, "responsibility paused: "+command.Reason); err != nil {
		return ResponsibilityDetailDTO{}, err
	}
	return s.Get(ctx, command.TenantID, assignment.ID)
}

func (s *OperationsService) Resume(ctx context.Context, command AssignmentControl) (ResponsibilityDetailDTO, error) {
	assignment, _, err := s.repository.ResumeAssignment(ctx, command, s.now())
	if err != nil {
		return ResponsibilityDetailDTO{}, err
	}
	return s.Get(ctx, command.TenantID, assignment.ID)
}

func (s *OperationsService) Retry(ctx context.Context, command AssignmentControl) (ResponsibilityDetailDTO, error) {
	assignment, _, err := s.repository.RetryAssignment(ctx, command, s.now())
	if err != nil {
		return ResponsibilityDetailDTO{}, err
	}
	return s.Get(ctx, command.TenantID, assignment.ID)
}

func (s *OperationsService) Terminate(ctx context.Context, command AssignmentControl) (ResponsibilityDetailDTO, error) {
	if strings.TrimSpace(command.Reason) == "" {
		return ResponsibilityDetailDTO{}, ErrInvalidContract
	}
	assignment, err := s.repository.TerminateAssignment(ctx, command, s.now())
	if err != nil {
		return ResponsibilityDetailDTO{}, err
	}
	if err := s.cancelLiveAttempts(ctx, command.TenantID, command.AssignmentID, "responsibility terminated: "+command.Reason); err != nil {
		return ResponsibilityDetailDTO{}, err
	}
	return s.Get(ctx, command.TenantID, assignment.ID)
}

func (s *OperationsService) Reassign(ctx context.Context, command AssignmentControl) (ResponsibilityDetailDTO, error) {
	if s.profiles == nil || s.taskReassignment == nil || strings.TrimSpace(command.AgentProfileID) == "" || strings.TrimSpace(command.Reason) == "" || command.ExpectedVersion < 1 {
		return ResponsibilityDetailDTO{}, ErrInvalidContract
	}
	assignment, err := s.repository.GetAssignment(ctx, strings.TrimSpace(command.AssignmentID))
	if err != nil {
		return ResponsibilityDetailDTO{}, err
	}
	if assignment.TenantID != strings.TrimSpace(command.TenantID) {
		return ResponsibilityDetailDTO{}, ErrNotFound
	}
	if assignment.WorkKind != WorkKindMissionTask || strings.TrimSpace(assignment.MissionID) == "" || strings.TrimSpace(assignment.WorkID) == "" || assignmentTerminal(assignment.State) {
		return ResponsibilityDetailDTO{}, ErrConflict
	}
	profile, err := s.profiles.GetProfile(ctx, agentcore.AgentProfileID(command.AgentProfileID))
	if err != nil {
		return ResponsibilityDetailDTO{}, err
	}
	if profile.ID.String() != strings.TrimSpace(command.AgentProfileID) || !profile.Enabled || !profile.RuntimeSpec.Kind.IsValid() {
		return ResponsibilityDetailDTO{}, ErrInvalidContract
	}
	if profile.ID.String() == assignment.AccountableAgent {
		return ResponsibilityDetailDTO{}, ErrConflict
	}
	if err := s.taskReassignment.ReassignTask(ctx, TaskReassignmentRequest{
		TenantID: assignment.TenantID, MissionID: assignment.MissionID, TaskID: assignment.WorkID,
		AgentProfileID: profile.ID.String(), ActorID: strings.TrimSpace(command.ActorID), Reason: strings.TrimSpace(command.Reason),
		IdempotencyKey: strings.TrimSpace(command.IdempotencyKey), ExpectedVersion: command.ExpectedVersion,
	}); err != nil {
		return ResponsibilityDetailDTO{}, err
	}
	if err := s.cancelLiveAttempts(ctx, command.TenantID, command.AssignmentID, "responsibility reassigned: "+command.Reason); err != nil {
		return ResponsibilityDetailDTO{}, err
	}
	return s.Get(ctx, command.TenantID, assignment.ID)
}

func (s *OperationsService) ResolveBlocker(ctx context.Context, tenantID, blockerID, actorID, resolution, idempotencyKey string, expectedVersion int64) (ResponsibilityDetailDTO, error) {
	current, err := s.repository.GetBlocker(ctx, tenantID, blockerID)
	if err != nil {
		return ResponsibilityDetailDTO{}, err
	}
	var blocker BlockerRecord
	if current.Status == BlockerStatusResolved {
		blocker, _, err = s.repository.ResolveBlocker(ctx, tenantID, blockerID, actorID, resolution, idempotencyKey, expectedVersion, ActivationCauseBlockerResolved, s.now())
	} else if current.Kind == BlockerKindApproval {
		assignment, loadErr := s.repository.GetAssignment(ctx, current.ResponsibilityID)
		if loadErr != nil {
			return ResponsibilityDetailDTO{}, loadErr
		}
		if assignment.TenantID != strings.TrimSpace(tenantID) {
			return ResponsibilityDetailDTO{}, ErrNotFound
		}
		policy, loadErr := s.repository.ResolvePolicy(ctx, assignment)
		if loadErr != nil {
			return ResponsibilityDetailDTO{}, loadErr
		}
		task, loadErr := s.service.Task(ctx, assignment.WorkID)
		if loadErr != nil {
			return ResponsibilityDetailDTO{}, loadErr
		}
		blocker, _, err = s.repository.ResolveApprovalBlocker(ctx, tenantID, blockerID, actorID, resolution, idempotencyKey, expectedVersion, ActivationCauseBlockerResolved, ApprovalGrant{
			AgentProfileID: assignment.AccountableAgent, PolicyID: policy.ID, PolicyVersion: policy.Version,
			ScopeDigest: approvalScopeDigest(task.Contract.ExecutionPermissionScopes),
		}, s.now())
	} else {
		blocker, _, err = s.repository.ResolveBlocker(ctx, tenantID, blockerID, actorID, resolution, idempotencyKey, expectedVersion, ActivationCauseBlockerResolved, s.now())
	}
	if err != nil {
		return ResponsibilityDetailDTO{}, err
	}
	return s.Get(ctx, tenantID, blocker.ResponsibilityID)
}

func (s *OperationsService) ObserveExternalSignal(ctx context.Context, signal ExternalSignal) (int, error) {
	return s.service.ObserveExternalSignal(ctx, signal)
}

func (s *OperationsService) cancelLiveAttempts(ctx context.Context, tenantID, responsibilityID, reason string) error {
	assignment, err := s.repository.GetAssignment(ctx, responsibilityID)
	if err != nil {
		return err
	}
	if assignment.TenantID != strings.TrimSpace(tenantID) {
		return ErrNotFound
	}
	attempts, err := s.repository.ListAttempts(ctx, responsibilityID)
	if err != nil {
		return err
	}
	for _, attempt := range attempts {
		if attempt.Outcome != "" {
			continue
		}
		if s.runtimeControl == nil {
			return fmt.Errorf("%w: runtime controller is required", ErrInvalidContract)
		}
		agentID := strings.TrimSpace(attempt.AgentProfileID)
		if agentID == "" {
			agentID = assignment.AccountableAgent
		}
		if err := s.runtimeControl.CancelResponsibility(ctx, RuntimeCancelRequest{TenantID: assignment.TenantID, MissionID: assignment.MissionID, TaskID: assignment.WorkID, RunID: attempt.AgentRunID, AgentID: agentID, Reason: reason}); err != nil {
			if errors.Is(err, ErrRuntimeTerminal) {
				continue
			}
			return err
		}
		if err := s.service.RecordRuntimeOutcome(ctx, RuntimeOutcomeInput{TenantID: assignment.TenantID, TaskID: assignment.WorkID, RunID: attempt.AgentRunID, Outcome: AttemptOutcomeCancelled, Error: errors.New(reason)}); err != nil {
			return err
		}
	}
	return nil
}

func (s *OperationsService) inboxItem(ctx context.Context, assignment AssignmentRecord) (ResponsibilityInboxItemDTO, error) {
	task, err := s.service.Task(ctx, assignment.WorkID)
	if err != nil {
		return ResponsibilityInboxItemDTO{}, err
	}
	policy, err := s.repository.ResolvePolicy(ctx, assignment)
	if err != nil {
		return ResponsibilityInboxItemDTO{}, err
	}
	allBlockers, err := s.repository.ListBlockers(ctx, assignment.TenantID, assignment.ID, false)
	if err != nil {
		return ResponsibilityInboxItemDTO{}, err
	}
	blockers := make([]BlockerRecord, 0, len(allBlockers))
	blockedSeconds := 0.0
	for _, blocker := range allBlockers {
		if blocker.Status == BlockerStatusOpen {
			blockers = append(blockers, blocker)
		}
		end := blocker.ResolvedAt
		if end.IsZero() {
			end = s.now()
		}
		if seconds := end.Sub(blocker.CreatedAt).Seconds(); seconds > 0 {
			blockedSeconds += seconds
		}
	}
	allAlerts, err := s.repository.ListSupervisionAlerts(ctx, assignment.TenantID, assignment.ID, false)
	if err != nil {
		return ResponsibilityInboxItemDTO{}, err
	}
	alerts := make([]SupervisionAlertRecord, 0, len(allAlerts))
	for _, alert := range allAlerts {
		if alert.Status == SupervisionAlertOpen {
			alerts = append(alerts, alert)
		}
	}
	attempts, err := s.repository.ListAttempts(ctx, assignment.ID)
	if err != nil {
		return ResponsibilityInboxItemDTO{}, err
	}
	activations, err := s.repository.ListActivations(ctx, assignment.TenantID, assignment.ID)
	if err != nil {
		return ResponsibilityInboxItemDTO{}, err
	}
	evidenceCompleteness, err := s.service.EvidenceCompleteness(ctx, task)
	if err != nil {
		return ResponsibilityInboxItemDTO{}, err
	}
	deliveries, duplicates, err := s.repository.RuntimeOutcomeDeliveryStats(ctx, assignment)
	if err != nil {
		return ResponsibilityInboxItemDTO{}, err
	}
	lane, nextAction, nextActor := responsibilityLane(assignment, blockers, attempts, activations)
	health, reasons := responsibilityHealth(assignment, blockers, alerts, attempts, activations, policy, s.now())
	sourceKind := firstContextValue(assignment.SourceKind, task.Contract.SourceKind)
	sourceID := firstContextValue(assignment.SourceID, task.Contract.SourceID)
	sourceStepID := firstContextValue(assignment.SourceStepID, task.Contract.SourceStepID)
	return ResponsibilityInboxItemDTO{
		ResponsibilityID: assignment.ID, TenantID: assignment.TenantID, WorkKind: assignment.WorkKind, WorkID: assignment.WorkID,
		MissionID: assignment.MissionID, SourceKind: sourceKind, SourceID: sourceID, SourceStepID: sourceStepID,
		Title: task.Contract.Title, Goal: task.Contract.Goal,
		AccountableAgent: assignment.AccountableAgent, State: assignment.State, Lane: lane, Health: health,
		RiskReasons: reasons, NextAction: nextAction, NextActor: nextActor, Priority: assignment.Priority,
		AcceptBy: assignment.AcceptBy, AssignedAt: assignment.CreatedAt, AcceptedAt: assignment.AcceptedAt, DueAt: assignment.DueAt, Paused: assignment.Paused, PauseReason: assignment.PauseReason,
		AttemptCount: len(attempts), BlockedSeconds: blockedSeconds, EvidenceCompleteness: evidenceCompleteness,
		RuntimeOutcomeDeliveries: deliveries, DuplicateRuntimeOutcomes: duplicates,
		OpenBlockers: mapBlockers(blockers), OpenAlerts: mapSupervisionAlerts(alerts), Policy: mapPolicy(policy), Version: assignment.Version, UpdatedAt: assignment.UpdatedAt,
	}, nil
}

func responsibilityLane(assignment AssignmentRecord, blockers []BlockerRecord, attempts []AttemptRecord, activations []ActivationRecord) (string, string, string) {
	if assignment.Paused {
		return "risk", "resume or terminate the paused responsibility", "manager"
	}
	if len(blockers) > 0 {
		return "waiting", blockers[0].Needed, blockers[0].RequestedFrom
	}
	if assignment.State == AssignmentStateSubmitted {
		return "review", "review submitted evidence and QA result", "reviewer"
	}
	if assignmentTerminal(assignment.State) || assignment.State == AssignmentStateEscalated {
		return "risk", "review the terminal or escalated outcome", "manager"
	}
	for _, attempt := range attempts {
		if attempt.Outcome == "" {
			return "now", "monitor the active runtime attempt", assignment.AccountableAgent
		}
	}
	for _, activation := range activations {
		if activation.Status == ActivationStatusPending || activation.Status == ActivationStatusClaimed {
			return "now", "claim and dispatch the pending activation", assignment.AccountableAgent
		}
	}
	return "next", "create the next activation or resolve the missing transition", "responsibility-coordinator"
}

func responsibilityHealth(assignment AssignmentRecord, blockers []BlockerRecord, alerts []SupervisionAlertRecord, attempts []AttemptRecord, activations []ActivationRecord, policy ResponsibilityPolicyRecord, now time.Time) (string, []string) {
	reasons := make([]string, 0)
	if assignment.Paused {
		reasons = append(reasons, "paused")
	}
	if !assignment.DueAt.IsZero() && assignment.DueAt.Before(now) && !assignmentTerminal(assignment.State) {
		reasons = append(reasons, "overdue")
	} else if !assignment.DueAt.IsZero() && policy.DeadlineWarningWindow > 0 && assignment.DueAt.Sub(now) <= policy.DeadlineWarningWindow && !assignmentTerminal(assignment.State) {
		reasons = append(reasons, "deadline_approaching")
	}
	failed := 0
	live := false
	for _, attempt := range attempts {
		if attempt.Outcome == "" {
			live = true
		} else if attempt.Outcome != AttemptOutcomeCompleted {
			failed++
		}
	}
	if failed >= 2 {
		reasons = append(reasons, "repeated_failure")
	}
	pending := false
	for _, activation := range activations {
		if activation.Status == ActivationStatusPending || activation.Status == ActivationStatusClaimed {
			pending = true
		}
	}
	if !assignmentTerminal(assignment.State) && assignment.State != AssignmentStateEscalated && !assignment.Paused && len(blockers) == 0 && !live && !pending {
		reasons = append(reasons, "silent_stall")
	}
	for _, blocker := range blockers {
		if blocker.RecheckAt.Before(now) {
			reasons = append(reasons, "blocker_recheck_overdue")
		}
	}
	for _, alert := range alerts {
		switch alert.Kind {
		case SupervisionAlertDeadlineWarning:
			if !containsString(reasons, "deadline_approaching") {
				reasons = append(reasons, "deadline_approaching")
			}
		case SupervisionAlertOverdue:
			if !containsString(reasons, "overdue") {
				reasons = append(reasons, "overdue")
			}
		}
	}
	if len(reasons) == 0 {
		return "healthy", nil
	}
	if containsString(reasons, "overdue") || containsString(reasons, "silent_stall") || containsString(reasons, "repeated_failure") || assignment.State == AssignmentStateEscalated {
		return "critical", reasons
	}
	return "at_risk", reasons
}

func accumulateSummary(summary *ResponsibilitySummaryDTO, item ResponsibilityInboxItemDTO) {
	if summary == nil {
		return
	}
	summary.Total++
	switch item.Lane {
	case "now":
		summary.Now++
	case "next":
		summary.Next++
	case "waiting":
		summary.Waiting++
	case "review":
		summary.Review++
	case "risk":
		summary.Risk++
	}
	if item.Paused {
		summary.Paused++
	}
	if containsString(item.RiskReasons, "overdue") {
		summary.Overdue++
	}
	if containsString(item.RiskReasons, "silent_stall") {
		summary.SilentStalls++
	}
	if containsString(item.RiskReasons, "repeated_failure") {
		summary.RepeatedFailures++
	}
}

func calculateMetrics(items []ResponsibilityInboxItemDTO) ResponsibilityMetricsDTO {
	if len(items) == 0 {
		return ResponsibilityMetricsDTO{}
	}
	fulfilled, stalled, firstPass, blocked := 0, 0, 0, 0
	accepted := 0
	var assignmentToAcceptSeconds float64
	var evidenceCompleteness float64
	runtimeDeliveries, duplicateOutcomes := 0, 0
	var blockedSeconds float64
	for _, item := range items {
		if item.State == AssignmentStateFulfilled {
			fulfilled++
		}
		if !item.AcceptedAt.IsZero() {
			accepted++
			if seconds := item.AcceptedAt.Sub(item.AssignedAt).Seconds(); seconds > 0 {
				assignmentToAcceptSeconds += seconds
			}
		}
		if containsString(item.RiskReasons, "silent_stall") {
			stalled++
		}
		if item.State == AssignmentStateFulfilled && item.AttemptCount == 1 {
			firstPass++
		}
		if item.BlockedSeconds > 0 {
			blocked++
			blockedSeconds += item.BlockedSeconds
		}
		evidenceCompleteness += item.EvidenceCompleteness
		runtimeDeliveries += item.RuntimeOutcomeDeliveries
		duplicateOutcomes += item.DuplicateRuntimeOutcomes
	}
	denominator := float64(len(items))
	avgBlocked := 0.0
	if blocked > 0 {
		avgBlocked = blockedSeconds / float64(blocked)
	}
	avgAccept := 0.0
	if accepted > 0 {
		avgAccept = assignmentToAcceptSeconds / float64(accepted)
	}
	firstPassRate := 0.0
	if fulfilled > 0 {
		firstPassRate = float64(firstPass) / float64(fulfilled)
	}
	duplicateRate := 0.0
	if runtimeDeliveries > 0 {
		duplicateRate = float64(duplicateOutcomes) / float64(runtimeDeliveries)
	}
	return ResponsibilityMetricsDTO{
		AssignmentToAcceptSeconds: avgAccept, AutonomousClosureRate: float64(fulfilled) / denominator, SilentStallRate: float64(stalled) / denominator,
		AverageBlockedSeconds: avgBlocked, FirstPassAcceptanceRate: firstPassRate,
		EvidenceCompleteness: evidenceCompleteness / denominator, DuplicateSideEffectRate: duplicateRate,
	}
}

func mapPolicy(policy ResponsibilityPolicyRecord) ResponsibilityPolicyDTO {
	retry := make([]int64, 0, len(policy.RetryBackoff))
	for _, delay := range policy.RetryBackoff {
		retry = append(retry, int64(delay/time.Second))
	}
	chain := make([]EscalationStepDTO, 0, len(policy.EscalationChain))
	for _, target := range policy.EscalationChain {
		chain = append(chain, EscalationStepDTO{SubjectKind: target.SubjectKind, SubjectID: target.SubjectID, AfterSeconds: int64(target.After / time.Second)})
	}
	return ResponsibilityPolicyDTO{PolicyID: policy.ID, AgentProfileID: policy.AgentProfileID, WorkKind: policy.WorkKind,
		AutonomyLevel: policy.AutonomyLevel, MaxParallel: policy.MaxParallel, AcceptTimeoutSeconds: int64(policy.AcceptTimeout / time.Second),
		HeartbeatTimeoutSeconds: int64(policy.HeartbeatTimeout / time.Second), MaxAttempts: policy.MaxAttempts,
		RetryBackoffSeconds: retry, MaxCostPerTask: policy.MaxCostPerTask, MaxTokenBudget: policy.MaxTokenBudget,
		ApprovalPolicy: policy.ApprovalPolicy, AllowedRiskLevels: append([]string(nil), policy.AllowedRiskLevels...),
		ToolScopes: append([]string(nil), policy.ToolScopes...), DataScopes: append([]string(nil), policy.DataScopes...),
		EscalationChain: chain, RecheckIntervalSeconds: int64(policy.RecheckInterval / time.Second),
		DeadlineWarningSeconds: int64(policy.DeadlineWarningWindow / time.Second), Enabled: policy.Enabled, Version: policy.Version, UpdatedAt: policy.UpdatedAt}
}

func mapBlockers(records []BlockerRecord) []BlockerDTO {
	out := make([]BlockerDTO, 0, len(records))
	for _, blocker := range records {
		out = append(out, BlockerDTO{BlockerID: blocker.ID, DedupeKey: blocker.DedupeKey, Kind: blocker.Kind, Needed: blocker.Needed, RequestedFrom: blocker.RequestedFrom, RecheckAt: blocker.RecheckAt, EscalationLevel: blocker.EscalationLevel, Status: blocker.Status, Resolution: blocker.Resolution, Version: blocker.Version, CreatedAt: blocker.CreatedAt, ResolvedAt: blocker.ResolvedAt})
	}
	return out
}

func mapSupervisionAlerts(records []SupervisionAlertRecord) []SupervisionAlertDTO {
	out := make([]SupervisionAlertDTO, 0, len(records))
	for _, alert := range records {
		out = append(out, SupervisionAlertDTO{AlertID: alert.ID, Kind: alert.Kind, Severity: alert.Severity,
			RequestedFrom: alert.RequestedFrom, DueAt: alert.DueAt, NextEscalationAt: alert.NextEscalationAt,
			EscalationLevel: alert.EscalationLevel, Status: alert.Status, Resolution: alert.Resolution,
			Version: alert.Version, CreatedAt: alert.CreatedAt, ResolvedAt: alert.ResolvedAt})
	}
	return out
}

func mapAttempts(records []AttemptRecord) []AttemptDTO {
	out := make([]AttemptDTO, 0, len(records))
	for _, attempt := range records {
		out = append(out, AttemptDTO{AttemptID: attempt.ID, ActivationID: attempt.ActivationID, RunID: attempt.AgentRunID, RuntimeKind: attempt.RuntimeKind, Attempt: attempt.Attempt, Outcome: attempt.Outcome, UsageTokens: attempt.UsageTokens, StartedAt: attempt.StartedAt, HeartbeatAt: attempt.HeartbeatAt, CompletedAt: attempt.CompletedAt, Error: attempt.Error})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Attempt > out[j].Attempt })
	return out
}

func mapActivations(records []ActivationRecord) []ActivationDTO {
	out := make([]ActivationDTO, 0, len(records))
	for _, activation := range records {
		out = append(out, ActivationDTO{ActivationID: activation.ID, Cause: activation.Cause, Status: activation.Status, NotBefore: activation.NotBefore, RunID: RunIDForActivation(activation.ID), LastError: activation.LastError, Version: activation.AssignmentVersion})
	}
	return out
}

func mapAudits(records []AuditRecord) []AuditDTO {
	out := make([]AuditDTO, 0, len(records))
	for _, audit := range records {
		out = append(out, AuditDTO{AuditID: audit.ID, EntityType: audit.EntityType, EntityID: audit.EntityID, Action: audit.Action, ActorID: audit.ActorID, FromState: audit.FromState, ToState: audit.ToState, Details: audit.Details, OccurredAt: audit.OccurredAt})
	}
	return out
}

func (s *OperationsService) now() time.Time {
	if s != nil && s.clock != nil {
		return s.clock.Now().UTC()
	}
	return time.Now().UTC()
}
