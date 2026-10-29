package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	discussionsession "orrery_v2/internal/app/discussionsession"
	memoryadmin "orrery_v2/internal/app/memoryadmin"
	memorydistill "orrery_v2/internal/app/memorydistill"
	todoapp "orrery_v2/internal/app/todo"
	"orrery_v2/internal/platform/clock"
)

type DiscussionPort interface {
	SendMessage(context.Context, discussionsession.ViewerRef, discussionsession.SendMessageCommand) (discussionsession.ConversationDTO, error)
}

type AgentTaskPort interface {
	StartAgentTask(context.Context, AgentTaskRequest) (AgentTaskReceipt, error)
}

type AgentTaskRequest struct {
	TenantID           string
	MissionID          string
	RunID              string
	AgentTaskID        string
	ScheduledTaskID    string
	Title              string
	Description        string
	AgentID            string
	ApprovalAssigneeID string
	TaskGoal           string
	Prompt             string
	PrincipalID        string
	CreatorProfileID   string
	CreatorKind        ViewerKind
	CreatorName        string
	Permissions        []string
	MaxAttempts        int
	RetryDelay         string
	ResultContract     AgentResultContract
	Execution          ExecutionPolicy
	Delivery           DeliveryPolicy
	ExecutionEnvelope  *agentcore.ExecutionEnvelope
}

type AgentTaskReceipt struct {
	CommandID        string
	AgentID          string
	MissionID        string
	TaskID           string
	ResponsibilityID string
}

type MemoryDistillationPort interface {
	DistillExperience(context.Context, memorydistill.ViewerRef, memorydistill.DistillExperienceRequest) (memorydistill.DistillExperienceResponse, error)
	DistillWorld(context.Context, memorydistill.ViewerRef, memorydistill.DistillWorldRequest) (memorydistill.DistillWorldResponse, error)
}

type MemoryLifecyclePort interface {
	SweepRetention(context.Context, memoryadmin.ViewerRef, memoryadmin.MemoryRetentionSweepRequest) (memoryadmin.MemoryRetentionSweepResultDTO, error)
}

type TodoAutomationPort interface {
	RunScheduledAutomationScan(context.Context, todoapp.TodoAutomationScanRequest) (todoapp.TodoAutomationScanResult, error)
}

type ResponsibilityReconcileRequest struct {
	ScheduledTaskID string
	ScheduledRunID  string
	TriggeredAt     time.Time
}

type ResponsibilityReconcilePort interface {
	ReconcileResponsibilities(context.Context, ResponsibilityReconcileRequest) (ResponsibilityReconcileResult, error)
}

type OntologyStateRefreshRequest struct {
	ScheduledTaskID string
	ScheduledRunID  string
	TriggeredAt     time.Time
}

type OntologyStateRefreshResult struct {
	Fingerprint       string
	SourceCount       int
	ChangedCount      int
	SnapshotOnlyCount int
	NoopCount         int
	StaleCount        int
}

type OntologyStateRefreshPort interface {
	RefreshOntologyState(context.Context, OntologyStateRefreshRequest) (OntologyStateRefreshResult, error)
}

type ResponsibilityEvaluationRequest struct {
	TenantID           string
	ScheduledTaskID    string
	ScheduledRunID     string
	AgentProfileID     string
	PortfolioBindingID string
	PortfolioVersionID string
	ApprovalAssigneeID string
	TriggeredAt        time.Time
}

type ResponsibilityAction struct {
	Title          string
	Description    string
	TaskGoal       string
	Prompt         string
	SourceRef      string
	ResultContract AgentResultContract
}

type ResponsibilityEvaluationDecision struct {
	Result           ResponsibilityEvaluationResult
	Action           *ResponsibilityAction
	MissionID        string
	TaskID           string
	ResponsibilityID string
}

type ResponsibilityEvaluationPort interface {
	EvaluateResponsibility(context.Context, ResponsibilityEvaluationRequest) (ResponsibilityEvaluationDecision, error)
}

type ResponsibilityEvaluationBatchRequest struct {
	TenantID        string
	PeopleTenantID  string
	EvaluationID    string
	ScheduledTaskID string
	ScheduledRunID  string
	TriggeredAt     time.Time
}

type ResponsibilityEvaluationBatchTarget struct {
	TenantID           string `json:"tenant_id"`
	AgentProfileID     string `json:"agent_profile_id"`
	PortfolioID        string `json:"portfolio_id"`
	PortfolioBindingID string `json:"portfolio_binding_id"`
	PortfolioVersionID string `json:"portfolio_version_id"`
	ApprovalAssigneeID string `json:"approval_assignee_id,omitempty"`
}

type ResponsibilityEvaluationBatchManifest struct {
	TenantID        string                                `json:"-"`
	ScheduledTaskID string                                `json:"scheduled_task_id"`
	ScheduledRunID  string                                `json:"scheduled_run_id"`
	ScopeID         string                                `json:"scope_id"`
	Attempt         int                                   `json:"attempt"`
	Targets         []ResponsibilityEvaluationBatchTarget `json:"targets"`
	CreatedAt       time.Time                             `json:"created_at"`
}

type ResponsibilityEvaluationBatchPort interface {
	DiscoverResponsibilityEvaluations(context.Context, ResponsibilityEvaluationBatchRequest) ([]ResponsibilityEvaluationBatchTarget, error)
	EvaluateResponsibilityBatch(context.Context, ResponsibilityEvaluationBatchRequest, []ResponsibilityEvaluationBatchTarget) ([]ResponsibilityEvaluationBatchDecision, error)
}

type ResponsibilityEvaluationBatchDecision struct {
	Target      ResponsibilityEvaluationBatchTarget
	Decision    ResponsibilityEvaluationDecision
	Err         error
	StartedAt   time.Time
	CompletedAt time.Time
}

type ToolCallPort interface {
	CallTool(context.Context, ToolCallRequest) (ToolCallResult, error)
}

type ToolCallRequest struct {
	TenantID          string
	RunID             string
	ToolID            string
	PrincipalID       string
	ProfileID         string
	Permissions       []string
	Arguments         map[string]any
	IdempotencyKey    string
	ExecutionEnvelope *agentcore.ExecutionEnvelope
}

type ToolCallResult struct {
	Output map[string]any
}

type CreatorPermissionResolver interface {
	ResolveCreatorPermissions(context.Context, CreatorRef) (CreatorPermissions, error)
}

type CreatorRef struct {
	ViewerID    string
	ViewerKind  ViewerKind
	DisplayName string
	ProfileID   string
}

type CreatorPermissions struct {
	PermissionScope []string
	AgentID         string
	OrganizationIDs []string
	DepartmentIDs   []string
	ProjectIDs      []string
	MissionIDs      []string
	DiscussionIDs   []string
}

type DispatcherOptions struct {
	Clock                    clock.Clock
	Repository               *Repository
	Discussion               DiscussionPort
	AgentTasks               AgentTaskPort
	MemoryDistillation       MemoryDistillationPort
	MemoryLifecycle          MemoryLifecyclePort
	TodoAutomation           TodoAutomationPort
	ResponsibilityEvaluation ResponsibilityEvaluationPort
	Responsibilities         ResponsibilityReconcilePort
	OntologyStateRefresh     OntologyStateRefreshPort
	ToolCalls                ToolCallPort
	CreatorPermissions       CreatorPermissionResolver
}

type Dispatcher struct {
	clock                    clock.Clock
	repo                     *Repository
	discussion               DiscussionPort
	agentTasks               AgentTaskPort
	memoryDistillation       MemoryDistillationPort
	memoryLifecycle          MemoryLifecyclePort
	todoAutomation           TodoAutomationPort
	responsibilityEvaluation ResponsibilityEvaluationPort
	responsibilities         ResponsibilityReconcilePort
	ontologyStateRefresh     OntologyStateRefreshPort
	toolCalls                ToolCallPort
	creatorPermissions       CreatorPermissionResolver
}

const (
	todoAutomationEmptyRunRetention      = 7 * 24 * time.Hour
	todoAutomationDiagnosticRunRetention = 90 * 24 * time.Hour
)

type RunDispatcher interface {
	DispatchRun(context.Context, ScheduledTask, ScheduledRun) (ScheduledRun, error)
}

func NewDispatcher(opts DispatcherOptions) *Dispatcher {
	dispatchClock := opts.Clock
	if dispatchClock == nil {
		dispatchClock = clock.Real{}
	}
	return &Dispatcher{
		clock:                    dispatchClock,
		repo:                     opts.Repository,
		discussion:               opts.Discussion,
		agentTasks:               opts.AgentTasks,
		memoryDistillation:       opts.MemoryDistillation,
		memoryLifecycle:          opts.MemoryLifecycle,
		todoAutomation:           opts.TodoAutomation,
		responsibilityEvaluation: opts.ResponsibilityEvaluation,
		responsibilities:         opts.Responsibilities,
		ontologyStateRefresh:     opts.OntologyStateRefresh,
		toolCalls:                opts.ToolCalls,
		creatorPermissions:       opts.CreatorPermissions,
	}
}

func (d *Dispatcher) DispatchRun(ctx context.Context, task ScheduledTask, run ScheduledRun) (ScheduledRun, error) {
	if d == nil || d.repo == nil || d.clock == nil {
		return ScheduledRun{}, ErrServiceDependency
	}
	if err := ctx.Err(); err != nil {
		return ScheduledRun{}, err
	}
	if strings.TrimSpace(run.ScheduledRunID) == "" || strings.TrimSpace(run.ScheduledTaskID) == "" {
		return ScheduledRun{}, ErrInvalidRequest
	}
	incomingAttempt := max(1, run.Attempt)
	claimed := false
	dispatchTask := task
	dispatchMode := task.Target.Mode
	run, err := d.repo.MutateRun(ctx, run.ScheduledRunID, func(current *ScheduledRun) error {
		if current.ScheduledTaskID != run.ScheduledTaskID || current.ScheduledTaskID != task.ScheduledTaskID {
			return ErrRevisionConflict
		}
		shouldClaim := false
		switch current.Status {
		case RunStatusRunning:
			if incomingAttempt > current.Attempt {
				shouldClaim = true
			} else if scheduledRunDispatchMode(task, *current) == TargetModeAgentTask &&
				strings.TrimSpace(current.CommandID) == "" &&
				strings.TrimSpace(current.MissionID) == "" &&
				strings.TrimSpace(current.TaskID) == "" &&
				strings.TrimSpace(current.ResponsibilityID) == "" {
				shouldClaim = true
			} else {
				return nil
			}
		case RunStatusFailed:
			if incomingAttempt > current.Attempt && scheduledRunIsResponsibilityEvaluationBatch(task, *current) {
				shouldClaim = true
			} else {
				return nil
			}
		case RunStatusCompleted, RunStatusSkipped:
			return nil
		case RunStatusQueued:
			shouldClaim = true
		default:
			return ErrRevisionConflict
		}
		if !shouldClaim {
			return nil
		}
		var configErr error
		dispatchTask, dispatchMode, configErr = scheduledRunDispatchConfiguration(task, *current)
		if configErr != nil {
			return configErr
		}
		claimed = true
		current.Status = RunStatusRunning
		if current.ConfigurationSnapshotVersion <= 0 {
			current.TargetMode = dispatchMode
		}
		current.FailureReason = ""
		current.CompletedAt = time.Time{}
		current.Attempt = max(current.Attempt, incomingAttempt)
		if current.StartedAt.IsZero() {
			current.StartedAt = d.now()
		}
		return nil
	})
	if err != nil {
		return ScheduledRun{}, err
	}
	if !claimed {
		return run, nil
	}

	err = nil
	switch dispatchMode {
	case TargetModeDiscussionWorkflow:
		run, err = d.dispatchDiscussion(ctx, dispatchTask, run)
	case TargetModeAgentTask:
		run, err = d.dispatchAgentTask(ctx, dispatchTask, run)
	case TargetModeToolCall:
		run, err = d.dispatchToolCall(ctx, dispatchTask, run)
	case TargetModeMemoryExperienceDistillation:
		run, err = d.dispatchMemoryExperienceDistillation(ctx, dispatchTask, run)
	case TargetModeMemoryWorldConsolidation:
		run, err = d.dispatchMemoryWorldConsolidation(ctx, dispatchTask, run)
	case TargetModeMemoryLifecycleSweep:
		run, err = d.dispatchMemoryLifecycleSweep(ctx, dispatchTask, run)
	case TargetModeTodoAutomationScan:
		run, err = d.dispatchTodoAutomationScan(ctx, run)
	case TargetModeResponsibilityEvaluation:
		if normalizeViewerKind(dispatchTask.CreatedByKind) != ViewerKindSystem {
			err = fmt.Errorf("%w: responsibility evaluation is system-managed", ErrInvalidTarget)
			break
		}
		run, err = d.dispatchResponsibilityEvaluation(ctx, dispatchTask, run)
	case TargetModeResponsibilityReconcile:
		if normalizeViewerKind(dispatchTask.CreatedByKind) != ViewerKindSystem {
			err = fmt.Errorf("%w: responsibility reconciliation is system-managed", ErrInvalidTarget)
			break
		}
		run, err = d.dispatchResponsibilityReconcile(ctx, run)
	case TargetModeOntologyStateRefresh:
		if normalizeViewerKind(dispatchTask.CreatedByKind) != ViewerKindSystem {
			err = fmt.Errorf("%w: ontology state refresh is system-managed", ErrInvalidTarget)
			break
		}
		run, err = d.dispatchOntologyStateRefresh(ctx, run)
	default:
		err = fmt.Errorf("%w: unsupported target mode %q", ErrInvalidTarget, dispatchMode)
	}
	if err != nil {
		failed := d.completeRun(run, RunStatusFailed, err.Error())
		persisted, saveErr := d.persistRunSnapshot(ctx, failed)
		if saveErr != nil {
			return failed, saveErr
		}
		failed = persisted
		if dispatchMode == TargetModeTodoAutomationScan {
			if cleanupErr := d.cleanupTodoAutomationRuns(ctx); cleanupErr != nil {
				return failed, RunPersistenceError{Run: failed, Err: cleanupErr}
			}
		}
		return failed, err
	}
	if dispatchMode == TargetModeAgentTask {
		persisted, err := d.persistRunSnapshot(ctx, run)
		if err != nil {
			return run, RunPersistenceError{Run: run, Err: err}
		}
		return persisted, nil
	}
	completed := d.completeRun(run, RunStatusCompleted, "")
	if dispatchMode == TargetModeTodoAutomationScan && completed.TodoAutomationScan != nil && todoAutomationScanIsEmpty(*completed.TodoAutomationScan) {
		completed.AggregatedOccurrences = 1
		completed.LastOccurrenceAt = completed.CompletedAt
		aggregatedInto, aggregated, aggregateErr := d.repo.CoalesceEmptyTodoAutomationRun(ctx, completed, 24*time.Hour)
		if aggregateErr != nil {
			return completed, RunPersistenceError{Run: completed, Err: aggregateErr}
		}
		if aggregated {
			completed.AggregatedIntoRunID = aggregatedInto
		}
		if cleanupErr := d.cleanupTodoAutomationRuns(ctx); cleanupErr != nil {
			return completed, RunPersistenceError{Run: completed, Err: cleanupErr}
		}
		return completed, nil
	}
	persisted, err := d.persistRunSnapshot(ctx, completed)
	if err != nil {
		return completed, RunPersistenceError{Run: completed, Err: err}
	}
	completed = persisted
	if dispatchMode == TargetModeTodoAutomationScan {
		if cleanupErr := d.cleanupTodoAutomationRuns(ctx); cleanupErr != nil {
			return completed, RunPersistenceError{Run: completed, Err: cleanupErr}
		}
	}
	return completed, nil
}

func scheduledRunDispatchMode(task ScheduledTask, run ScheduledRun) TargetMode {
	if run.ConfigurationSnapshotVersion > 0 {
		return run.TargetMode
	}
	return task.Target.Mode
}

func scheduledRunIsResponsibilityEvaluationBatch(task ScheduledTask, run ScheduledRun) bool {
	dispatchTask, mode, err := scheduledRunDispatchConfiguration(task, run)
	return err == nil && mode == TargetModeResponsibilityEvaluation &&
		normalizeTarget(dispatchTask.Target).SourceScopeKind == ResponsibilityEvaluationPeoplePortfoliosScope
}

func scheduledRunDispatchConfiguration(task ScheduledTask, run ScheduledRun) (ScheduledTask, TargetMode, error) {
	dispatchTask, err := ResolveScheduledRunConfiguration(task, run)
	if err != nil {
		return ScheduledTask{}, "", err
	}
	return dispatchTask, dispatchTask.Target.Mode, nil
}

// ResolveScheduledRunConfiguration returns the immutable task configuration for
// a versioned occurrence. Legacy runs intentionally retain their mutable-task
// fallback.
func ResolveScheduledRunConfiguration(task ScheduledTask, run ScheduledRun) (ScheduledTask, error) {
	if run.ConfigurationSnapshotVersion < 0 || run.ConfigurationSnapshotVersion > scheduledRunConfigurationSnapshotVersion {
		return ScheduledTask{}, fmt.Errorf("%w: unsupported scheduled run configuration snapshot version %d", ErrInvalidRequest, run.ConfigurationSnapshotVersion)
	}
	if run.ConfigurationSnapshotVersion == 0 {
		return task, nil
	}
	if run.ConfigurationSnapshot == nil {
		return ScheduledTask{}, fmt.Errorf("%w: scheduled run configuration snapshot is missing", ErrInvalidRequest)
	}
	snapshot, err := cloneScheduledTaskConfiguration(*run.ConfigurationSnapshot)
	if err != nil {
		return ScheduledTask{}, fmt.Errorf("%w: clone scheduled run configuration snapshot: %v", ErrInvalidRequest, err)
	}
	taskID := strings.TrimSpace(task.ScheduledTaskID)
	runTaskID := strings.TrimSpace(run.ScheduledTaskID)
	snapshotTaskID := strings.TrimSpace(snapshot.ScheduledTaskID)
	if taskID == "" || runTaskID == "" || snapshotTaskID == "" || taskID != runTaskID || snapshotTaskID != runTaskID {
		return ScheduledTask{}, fmt.Errorf("%w: scheduled run configuration task identity is missing or inconsistent", ErrRevisionConflict)
	}
	runTenantID := strings.TrimSpace(run.TenantID)
	snapshotTenantID := strings.TrimSpace(snapshot.TenantID)
	if runTenantID == "" || snapshotTenantID == "" {
		return ScheduledTask{}, fmt.Errorf("%w: scheduled run configuration tenant is missing", ErrInvalidRequest)
	}
	if normalizeTenantID(task.TenantID) != runTenantID || snapshotTenantID != runTenantID {
		return ScheduledTask{}, fmt.Errorf("%w: scheduled run configuration tenant is inconsistent", ErrRevisionConflict)
	}
	snapshot.Title = strings.TrimSpace(snapshot.Title)
	if snapshot.Title == "" || strings.TrimSpace(run.ScheduledTaskTitle) == "" || strings.TrimSpace(run.ScheduledTaskTitle) != snapshot.Title {
		return ScheduledTask{}, fmt.Errorf("%w: scheduled run configuration title is missing or inconsistent", ErrInvalidRequest)
	}
	if strings.TrimSpace(string(snapshot.Target.Mode)) == "" || snapshot.Target.Mode != run.TargetMode {
		return ScheduledTask{}, fmt.Errorf("%w: scheduled run configuration target mode is missing or inconsistent", ErrRevisionConflict)
	}
	if snapshot.Target.Mode == TargetModeAgentTask && strings.TrimSpace(run.AgentID) != strings.TrimSpace(snapshot.Target.AgentID) {
		return ScheduledTask{}, fmt.Errorf("%w: scheduled run Agent identity is inconsistent", ErrRevisionConflict)
	}
	if !agentResultContractsEqual(snapshot.Target.ResultContract, run.ResultContract) || snapshot.Delivery != run.Delivery {
		return ScheduledTask{}, fmt.Errorf("%w: scheduled run result or delivery configuration is inconsistent", ErrRevisionConflict)
	}
	if err := validatePolicy(snapshot.Policy); err != nil {
		return ScheduledTask{}, err
	}
	if err := validateExecutionPolicy(snapshot.Execution); err != nil {
		return ScheduledTask{}, err
	}
	if err := validateTarget(snapshot.Target); err != nil {
		return ScheduledTask{}, err
	}
	if err := validateDeliveryPolicy(snapshot.Delivery); err != nil {
		return ScheduledTask{}, err
	}
	if err := validateDeliveryContentArtifact(snapshot.Delivery, snapshot.Target); err != nil {
		return ScheduledTask{}, err
	}
	return snapshot, nil
}

func agentResultContractsEqual(left, right AgentResultContract) bool {
	if len(left.Artifacts) != len(right.Artifacts) {
		return false
	}
	for index := range left.Artifacts {
		if left.Artifacts[index] != right.Artifacts[index] {
			return false
		}
	}
	return true
}

func (d *Dispatcher) persistRunSnapshot(ctx context.Context, snapshot ScheduledRun) (ScheduledRun, error) {
	persistCtx, cancel := responsibilityEvaluationBatchPersistenceContext(ctx, snapshot.ResponsibilityEvaluationBatch != nil)
	defer cancel()
	return d.repo.MutateRun(persistCtx, snapshot.ScheduledRunID, func(current *ScheduledRun) error {
		if snapshot.TargetMode == TargetModeAgentTask && snapshot.Status == RunStatusRunning && current.RuntimeRunID != "" {
			// The responsibility worker can finish before dispatch returns. Its
			// durable result must win over the older dispatch snapshot.
			if current.MissionID != snapshot.MissionID || current.TaskID != snapshot.TaskID ||
				current.ResponsibilityID != snapshot.ResponsibilityID || current.AgentID != snapshot.AgentID {
				return ErrRevisionConflict
			}
			if current.Status == RunStatusCompleted || current.Status == RunStatusFailed || current.Status == RunStatusSkipped {
				return nil
			}
			if !current.StartedAt.IsZero() {
				snapshot.StartedAt = current.StartedAt
			}
		}
		if snapshot.ResponsibilityEvaluationBatch != nil && current.Attempt != snapshot.Attempt {
			return ErrRevisionConflict
		}
		current.Status = snapshot.Status
		current.TargetMode = snapshot.TargetMode
		current.CommandID = snapshot.CommandID
		current.ConversationID = snapshot.ConversationID
		current.AgentID = snapshot.AgentID
		current.MissionID = snapshot.MissionID
		current.TaskID = snapshot.TaskID
		current.ResponsibilityID = snapshot.ResponsibilityID
		if snapshot.ExecutionEnvelope != nil {
			current.ExecutionEnvelope = agentcore.CloneExecutionEnvelope(snapshot.ExecutionEnvelope)
		}
		current.Result = snapshot.Result
		current.Attempt = max(current.Attempt, snapshot.Attempt)
		current.StartedAt = snapshot.StartedAt
		current.CompletedAt = snapshot.CompletedAt
		current.FailureReason = snapshot.FailureReason
		current.TodoAutomationScan = snapshot.TodoAutomationScan
		current.ResponsibilityEvaluation = snapshot.ResponsibilityEvaluation
		current.ResponsibilityEvaluationBatch = snapshot.ResponsibilityEvaluationBatch
		current.ResponsibilityReconcile = snapshot.ResponsibilityReconcile
		current.AggregatedOccurrences = snapshot.AggregatedOccurrences
		current.LastOccurrenceAt = snapshot.LastOccurrenceAt
		current.AggregatedIntoRunID = snapshot.AggregatedIntoRunID
		return nil
	})
}

func responsibilityEvaluationBatchPersistenceContext(ctx context.Context, batch bool) (context.Context, context.CancelFunc) {
	if batch {
		return context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	}
	return ctx, func() {}
}

func (d *Dispatcher) dispatchResponsibilityEvaluation(ctx context.Context, task ScheduledTask, run ScheduledRun) (ScheduledRun, error) {
	if d.responsibilityEvaluation == nil {
		return run, ErrServiceDependency
	}
	target := normalizeTarget(task.Target)
	if target.SourceScopeKind == ResponsibilityEvaluationPeoplePortfoliosScope {
		return d.dispatchResponsibilityEvaluationBatch(ctx, task, run, target)
	}
	decision, err := d.responsibilityEvaluation.EvaluateResponsibility(ctx, ResponsibilityEvaluationRequest{
		TenantID: task.TenantID, ScheduledTaskID: run.ScheduledTaskID, ScheduledRunID: run.ScheduledRunID,
		AgentProfileID: target.AgentID, PortfolioBindingID: target.SourceScopeID, ApprovalAssigneeID: target.ApprovalAssigneeID, TriggeredAt: run.StartedAt,
	})
	run.ResponsibilityEvaluation = &decision.Result
	run.AgentID = target.AgentID
	if err != nil {
		return run, err
	}
	if err := validateResponsibilityEvaluationDecision(target, decision); err != nil {
		return run, err
	}
	run.ResponsibilityEvaluation.LinkedExistingMission = false
	run.ResponsibilityEvaluation.MissionTaskMaterialized = false
	if !decision.Result.Actionable {
		run.CommandID = fmt.Sprintf("responsibility-evaluation:%s:no-action", strings.TrimSpace(run.ScheduledRunID))
		return run, nil
	}
	if decision.Action == nil {
		run.MissionID = strings.TrimSpace(decision.MissionID)
		run.TaskID = strings.TrimSpace(decision.TaskID)
		run.ResponsibilityID = strings.TrimSpace(decision.ResponsibilityID)
		run.CommandID = run.ResponsibilityID
		run.ResponsibilityEvaluation.LinkedExistingMission = true
		return run, nil
	}
	actionTask := task
	actionTask.Title = firstNonBlank(decision.Action.Title, task.Title)
	actionTask.Description = firstNonBlank(decision.Action.Description, task.Description)
	actionTask.Target = Target{
		Mode: TargetModeAgentTask, AgentID: target.AgentID, TaskGoal: strings.TrimSpace(decision.Action.TaskGoal),
		Prompt: strings.TrimSpace(decision.Action.Prompt), ResultContract: decision.Action.ResultContract,
	}
	run, err = d.dispatchAgentTask(ctx, actionTask, run)
	if err != nil {
		return run, err
	}
	if strings.TrimSpace(run.MissionID) == "" || strings.TrimSpace(run.TaskID) == "" || strings.TrimSpace(run.ResponsibilityID) == "" {
		return run, ErrServiceDependency
	}
	run.ResponsibilityEvaluation.MissionTaskMaterialized = true
	return run, nil
}

func (d *Dispatcher) dispatchResponsibilityEvaluationBatch(
	ctx context.Context,
	task ScheduledTask,
	run ScheduledRun,
	target Target,
) (ScheduledRun, error) {
	batch, ok := d.responsibilityEvaluation.(ResponsibilityEvaluationBatchPort)
	if !ok {
		return run, ErrServiceDependency
	}
	request := ResponsibilityEvaluationBatchRequest{
		TenantID: task.TenantID, PeopleTenantID: target.SourceScopeID, ScheduledTaskID: run.ScheduledTaskID,
		ScheduledRunID: run.ScheduledRunID, TriggeredAt: run.StartedAt,
	}
	summary := &ResponsibilityEvaluationBatchResult{ScopeID: target.SourceScopeID}
	run.ResponsibilityEvaluation = nil
	run.ResponsibilityEvaluationBatch = summary
	run.AgentID, run.MissionID, run.TaskID, run.ResponsibilityID = "", "", "", ""
	run.CommandID = "responsibility-evaluation-batch:" + strings.TrimSpace(run.ScheduledRunID)

	manifest, err := d.repo.GetResponsibilityEvaluationBatchManifest(ctx, run.ScheduledRunID)
	if errors.Is(err, ErrNotFound) {
		targets, discoverErr := batch.DiscoverResponsibilityEvaluations(ctx, request)
		if discoverErr != nil {
			return run, discoverErr
		}
		targets, discoverErr = canonicalResponsibilityEvaluationBatchTargets(task.TenantID, targets)
		if discoverErr != nil {
			return run, discoverErr
		}
		manifestCtx, cancelManifest := responsibilityEvaluationBatchPersistenceContext(ctx, true)
		manifest, err = d.repo.EnsureResponsibilityEvaluationBatchManifest(manifestCtx, ResponsibilityEvaluationBatchManifest{
			TenantID: task.TenantID, ScheduledTaskID: run.ScheduledTaskID, ScheduledRunID: run.ScheduledRunID,
			ScopeID: target.SourceScopeID, Attempt: max(1, run.Attempt), Targets: targets, CreatedAt: d.now(),
		})
		cancelManifest()
	}
	if err != nil {
		return run, err
	}
	if manifest.TenantID != strings.TrimSpace(task.TenantID) || manifest.ScheduledTaskID != strings.TrimSpace(run.ScheduledTaskID) ||
		manifest.ScheduledRunID != strings.TrimSpace(run.ScheduledRunID) || manifest.ScopeID != strings.TrimSpace(target.SourceScopeID) {
		return run, fmt.Errorf("%w: responsibility evaluation batch manifest identity changed", ErrRevisionConflict)
	}
	targets, err := canonicalResponsibilityEvaluationBatchTargets(task.TenantID, manifest.Targets)
	if err != nil {
		return run, err
	}
	summary.DiscoveredCount = len(targets)
	pendingTargets := make([]ResponsibilityEvaluationBatchTarget, 0, len(targets))
	completedItems := make(map[string]ResponsibilityEvaluationItem, len(targets))
	for _, itemTarget := range targets {
		itemID := ResponsibilityEvaluationItemID(run.ScheduledRunID, itemTarget.PortfolioBindingID, itemTarget.PortfolioVersionID)
		if existing, getErr := d.repo.GetResponsibilityEvaluationItem(ctx, itemID); getErr == nil && existing.Status == ResponsibilityEvaluationItemCompleted {
			completedItems[itemID] = existing
			continue
		} else if getErr != nil && !errors.Is(getErr, ErrNotFound) {
			return run, getErr
		}
		pendingTargets = append(pendingTargets, itemTarget)
	}
	decisions, err := batch.EvaluateResponsibilityBatch(ctx, request, pendingTargets)
	if err != nil {
		return run, err
	}
	if len(decisions) != len(pendingTargets) {
		return run, fmt.Errorf("%w: responsibility evaluation batch returned %d decisions for %d targets", ErrServiceDependency, len(decisions), len(pendingTargets))
	}
	decisionByItemID := make(map[string]ResponsibilityEvaluationBatchDecision, len(decisions))
	for _, batchDecision := range decisions {
		itemID := ResponsibilityEvaluationItemID(run.ScheduledRunID, batchDecision.Target.PortfolioBindingID, batchDecision.Target.PortfolioVersionID)
		if _, duplicate := decisionByItemID[itemID]; duplicate {
			return run, fmt.Errorf("%w: responsibility evaluation batch returned a duplicate target", ErrServiceDependency)
		}
		decisionByItemID[itemID] = batchDecision
	}
	persistCtx, cancel := responsibilityEvaluationBatchPersistenceContext(ctx, true)
	defer cancel()
	for _, itemTarget := range targets {
		itemID := ResponsibilityEvaluationItemID(run.ScheduledRunID, itemTarget.PortfolioBindingID, itemTarget.PortfolioVersionID)
		if existing, ok := completedItems[itemID]; ok {
			summary.SkippedCount++
			aggregateResponsibilityEvaluationItem(summary, existing)
			continue
		}
		batchDecision, ok := decisionByItemID[itemID]
		if !ok || batchDecision.Target.TenantID != itemTarget.TenantID || batchDecision.Target.AgentProfileID != itemTarget.AgentProfileID ||
			batchDecision.Target.PortfolioID != itemTarget.PortfolioID || batchDecision.Target.PortfolioBindingID != itemTarget.PortfolioBindingID ||
			batchDecision.Target.PortfolioVersionID != itemTarget.PortfolioVersionID {
			return run, fmt.Errorf("%w: responsibility evaluation batch target identity changed", ErrServiceDependency)
		}
		startedAt := batchDecision.StartedAt
		if startedAt.IsZero() {
			startedAt = d.now()
		}
		completedAt := batchDecision.CompletedAt
		if completedAt.IsZero() {
			completedAt = d.now()
		}
		decision, evaluateErr := batchDecision.Decision, batchDecision.Err
		item := ResponsibilityEvaluationItem{
			ItemID: itemID, TenantID: task.TenantID, ScheduledTaskID: run.ScheduledTaskID, ScheduledRunID: run.ScheduledRunID,
			PortfolioID: itemTarget.PortfolioID, PortfolioBindingID: itemTarget.PortfolioBindingID,
			PortfolioVersionID: itemTarget.PortfolioVersionID, AgentProfileID: itemTarget.AgentProfileID,
			Attempt: max(1, run.Attempt), StartedAt: startedAt, CompletedAt: completedAt, CreatedAt: startedAt, UpdatedAt: completedAt,
		}
		result := decision.Result
		if strings.TrimSpace(result.PortfolioID) == "" {
			result.PortfolioID = itemTarget.PortfolioID
		}
		if strings.TrimSpace(result.PortfolioBindingID) == "" {
			result.PortfolioBindingID = itemTarget.PortfolioBindingID
		}
		if strings.TrimSpace(result.PortfolioVersionID) == "" {
			result.PortfolioVersionID = itemTarget.PortfolioVersionID
		}
		if strings.TrimSpace(result.AgentProfileID) == "" {
			result.AgentProfileID = itemTarget.AgentProfileID
		}
		item.Evaluation = &result
		singularTarget := Target{
			Mode: TargetModeResponsibilityEvaluation, AgentID: itemTarget.AgentProfileID,
			ApprovalAssigneeID: itemTarget.ApprovalAssigneeID,
			SourceScopeKind:    ResponsibilityEvaluationPeoplePortfolioScope, SourceScopeID: itemTarget.PortfolioBindingID,
		}
		if evaluateErr == nil {
			evaluateErr = validateResponsibilityEvaluationDecision(singularTarget, decision)
		}
		if evaluateErr == nil && (strings.TrimSpace(result.PortfolioID) != strings.TrimSpace(itemTarget.PortfolioID) ||
			strings.TrimSpace(result.PortfolioVersionID) != strings.TrimSpace(itemTarget.PortfolioVersionID)) {
			evaluateErr = fmt.Errorf("%w: responsibility evaluation portfolio version changed", ErrRevisionConflict)
		}
		if evaluateErr == nil && decision.Action != nil {
			evaluateErr = fmt.Errorf("%w: batch responsibility evaluation cannot materialize Agent tasks", ErrInvalidTarget)
		}
		if evaluateErr != nil {
			item.Status = ResponsibilityEvaluationItemFailed
			item.FailureCode = "evaluation_failed"
			item.FailureMessage = responsibilityEvaluationFailureMessage(evaluateErr)
		} else {
			item.Status = ResponsibilityEvaluationItemCompleted
			item.Evaluation.LinkedExistingMission = false
			item.Evaluation.MissionTaskMaterialized = false
			if !decision.Result.Actionable {
				item.CommandID = "responsibility-evaluation:" + item.ItemID + ":no-action"
			} else {
				item.MissionID = strings.TrimSpace(decision.MissionID)
				item.TaskID = strings.TrimSpace(decision.TaskID)
				item.ResponsibilityID = strings.TrimSpace(decision.ResponsibilityID)
				item.CommandID = item.ResponsibilityID
				item.Evaluation.LinkedExistingMission = true
			}
		}
		if err := d.repo.SaveResponsibilityEvaluationItem(persistCtx, item); err != nil {
			return run, err
		}
		if item.Status == ResponsibilityEvaluationItemFailed {
			summary.FailedCount++
		} else {
			aggregateResponsibilityEvaluationItem(summary, item)
		}
	}
	if summary.FailedCount > 0 {
		return run, fmt.Errorf("responsibility evaluation batch has %d failed item(s)", summary.FailedCount)
	}
	return run, nil
}

func canonicalResponsibilityEvaluationBatchTargets(tenantID string, targets []ResponsibilityEvaluationBatchTarget) ([]ResponsibilityEvaluationBatchTarget, error) {
	tenantID = strings.TrimSpace(tenantID)
	canonical := append([]ResponsibilityEvaluationBatchTarget(nil), targets...)
	for index := range canonical {
		item := &canonical[index]
		item.TenantID = strings.TrimSpace(item.TenantID)
		item.AgentProfileID = strings.TrimSpace(item.AgentProfileID)
		item.PortfolioID = strings.TrimSpace(item.PortfolioID)
		item.PortfolioBindingID = strings.TrimSpace(item.PortfolioBindingID)
		item.PortfolioVersionID = strings.TrimSpace(item.PortfolioVersionID)
		item.ApprovalAssigneeID = strings.TrimSpace(item.ApprovalAssigneeID)
		if tenantID == "" || item.TenantID != tenantID || item.AgentProfileID == "" || item.PortfolioID == "" ||
			item.PortfolioBindingID == "" || item.PortfolioVersionID == "" {
			return nil, fmt.Errorf("%w: responsibility evaluation batch target is incomplete", ErrInvalidTarget)
		}
	}
	sort.Slice(canonical, func(i, j int) bool {
		if canonical[i].PortfolioBindingID == canonical[j].PortfolioBindingID {
			return canonical[i].PortfolioVersionID < canonical[j].PortfolioVersionID
		}
		return canonical[i].PortfolioBindingID < canonical[j].PortfolioBindingID
	})
	seen := make(map[string]struct{}, len(canonical))
	for _, item := range canonical {
		itemID := ResponsibilityEvaluationItemID("manifest", item.PortfolioBindingID, item.PortfolioVersionID)
		if _, duplicate := seen[itemID]; duplicate {
			return nil, fmt.Errorf("%w: responsibility evaluation batch contains a duplicate target", ErrInvalidTarget)
		}
		seen[itemID] = struct{}{}
	}
	return canonical, nil
}

func aggregateResponsibilityEvaluationItem(summary *ResponsibilityEvaluationBatchResult, item ResponsibilityEvaluationItem) {
	if summary == nil || item.Status != ResponsibilityEvaluationItemCompleted || item.Evaluation == nil {
		return
	}
	result := item.Evaluation
	summary.EvaluatedCount++
	summary.CandidateCount += result.CandidateCount
	summary.ActionableCount += result.ActionableCount
	summary.WaitingCount += result.WaitingCount
	summary.ReviewCount += result.ReviewCount
	summary.EscalatedCount += result.EscalatedCount
	summary.PausedCount += result.PausedCount
	summary.InvalidCount += result.InvalidCount
	if result.MissionTaskMaterialized {
		summary.MaterializedCount++
	} else if result.LinkedExistingMission {
		summary.LinkedExistingMissionCount++
	} else {
		summary.NoActionCount++
	}
}

func responsibilityEvaluationFailureMessage(err error) string {
	message := strings.Join(strings.Fields(strings.TrimSpace(err.Error())), " ")
	const maxLength = 500
	if len(message) > maxLength {
		message = message[:maxLength]
	}
	return message
}

func validateResponsibilityEvaluationDecision(target Target, decision ResponsibilityEvaluationDecision) error {
	result := decision.Result
	if !result.Evaluated || strings.TrimSpace(result.Reason) == "" || strings.TrimSpace(result.PortfolioID) == "" ||
		strings.TrimSpace(result.PortfolioBindingID) == "" || strings.TrimSpace(result.PortfolioVersionID) == "" ||
		strings.TrimSpace(result.SourceRevision) == "" || strings.TrimSpace(result.SourceDigest) == "" ||
		strings.TrimSpace(result.AgentProfileID) == "" || strings.TrimSpace(result.DecisionSourceRef) == "" ||
		result.CandidateCount < 0 || result.ActionableCount < 0 || result.WaitingCount < 0 || result.ReviewCount < 0 ||
		result.EscalatedCount < 0 || result.PausedCount < 0 || result.InvalidCount < 0 ||
		result.ActionableCount+result.WaitingCount+result.ReviewCount+result.EscalatedCount+result.PausedCount+result.InvalidCount > result.CandidateCount {
		return fmt.Errorf("%w: responsibility evaluation result is incomplete", ErrInvalidTarget)
	}
	if strings.TrimSpace(result.AgentProfileID) != strings.TrimSpace(target.AgentID) {
		return fmt.Errorf("%w: responsibility evaluation Agent does not match the scheduled target", ErrInvalidTarget)
	}
	if strings.TrimSpace(result.PortfolioBindingID) != strings.TrimSpace(target.SourceScopeID) {
		return fmt.Errorf("%w: responsibility evaluation portfolio does not match the scheduled target", ErrInvalidTarget)
	}
	missionID := strings.TrimSpace(decision.MissionID)
	taskID := strings.TrimSpace(decision.TaskID)
	responsibilityID := strings.TrimSpace(decision.ResponsibilityID)
	if !result.Actionable {
		if result.DecisionKind != ResponsibilityEvaluationNoAction || decision.Action != nil || missionID != "" || taskID != "" || responsibilityID != "" ||
			strings.TrimSpace(result.SelectedMissionID) != "" || strings.TrimSpace(result.SelectedTaskID) != "" || strings.TrimSpace(result.SelectedResponsibilityID) != "" {
			return fmt.Errorf("%w: a non-actionable responsibility evaluation cannot materialize work", ErrInvalidTarget)
		}
		return nil
	}
	if result.ActionableCount == 0 {
		return fmt.Errorf("%w: actionable responsibility evaluation has no actionable candidates", ErrInvalidTarget)
	}
	if decision.Action != nil {
		if result.DecisionKind != ResponsibilityEvaluationMaterializeAction || missionID != "" || taskID != "" || responsibilityID != "" ||
			strings.TrimSpace(decision.Action.TaskGoal) == "" || strings.TrimSpace(decision.Action.SourceRef) == "" ||
			strings.TrimSpace(decision.Action.SourceRef) != strings.TrimSpace(result.DecisionSourceRef) {
			return fmt.Errorf("%w: responsibility action contract is incomplete", ErrInvalidTarget)
		}
		return validateAgentResultContract(decision.Action.ResultContract)
	}
	if result.DecisionKind != ResponsibilityEvaluationLinkExistingMission || missionID == "" || taskID == "" || responsibilityID == "" ||
		strings.TrimSpace(result.SelectedMissionID) != missionID || strings.TrimSpace(result.SelectedTaskID) != taskID ||
		strings.TrimSpace(result.SelectedResponsibilityID) != responsibilityID || strings.TrimSpace(result.SelectedAssignmentState) == "" ||
		result.SelectedAssignmentVersion < 1 {
		return fmt.Errorf("%w: actionable responsibility evaluation must link existing work or provide an action contract", ErrInvalidTarget)
	}
	return nil
}

func (d *Dispatcher) dispatchResponsibilityReconcile(ctx context.Context, run ScheduledRun) (ScheduledRun, error) {
	if d.responsibilities == nil {
		return run, ErrServiceDependency
	}
	result, err := d.responsibilities.ReconcileResponsibilities(ctx, ResponsibilityReconcileRequest{
		ScheduledTaskID: run.ScheduledTaskID, ScheduledRunID: run.ScheduledRunID, TriggeredAt: run.StartedAt,
	})
	run.ResponsibilityReconcile = &result
	run.CommandID = fmt.Sprintf("responsibility:%s:candidates=%d:dispatched=%d:failed=%d", strings.TrimSpace(run.ScheduledRunID), result.Candidates, result.Dispatched, result.Failed)
	return run, err
}

func (d *Dispatcher) dispatchOntologyStateRefresh(ctx context.Context, run ScheduledRun) (ScheduledRun, error) {
	if d.ontologyStateRefresh == nil {
		return run, ErrServiceDependency
	}
	result, err := d.ontologyStateRefresh.RefreshOntologyState(ctx, OntologyStateRefreshRequest{
		ScheduledTaskID: run.ScheduledTaskID,
		ScheduledRunID:  run.ScheduledRunID,
		TriggeredAt:     run.StartedAt,
	})
	run.CommandID = "ontology-state-refresh:" + strings.TrimSpace(result.Fingerprint)
	run.Result.Summary = fmt.Sprintf(
		"Ontology state refresh: source=%d changed=%d snapshot_only=%d noop=%d stale=%d",
		result.SourceCount,
		result.ChangedCount,
		result.SnapshotOnlyCount,
		result.NoopCount,
		result.StaleCount,
	)
	return run, err
}

func (d *Dispatcher) cleanupTodoAutomationRuns(ctx context.Context) error {
	_, err := d.repo.CleanupTodoAutomationRuns(ctx, d.now(), todoAutomationEmptyRunRetention, todoAutomationDiagnosticRunRetention)
	return err
}

func (d *Dispatcher) dispatchTodoAutomationScan(ctx context.Context, run ScheduledRun) (ScheduledRun, error) {
	if d.todoAutomation == nil {
		return run, ErrServiceDependency
	}
	result, err := d.todoAutomation.RunScheduledAutomationScan(ctx, todoapp.TodoAutomationScanRequest{
		ScheduledTaskID: run.ScheduledTaskID, ScheduledRunID: run.ScheduledRunID, TriggeredAt: run.StartedAt,
	})
	run.TodoAutomationScan = &result
	run.CommandID = fmt.Sprintf("todo-automation:%s:eligible=%d:planned=%d:failed=%d", strings.TrimSpace(run.ScheduledRunID), result.EligibleCount, result.PlannedCount, result.FailedCount)
	return run, err
}

func todoAutomationScanIsEmpty(result todoapp.TodoAutomationScanResult) bool {
	return result.ScannedCount == 0 &&
		result.EligibleCount == 0 &&
		result.ClaimedCount == 0 &&
		result.DiscussionSyncCount == 0 &&
		result.PlannedCount == 0 &&
		result.RecoveredCount == 0 &&
		result.SkippedCount == 0 &&
		result.FailedCount == 0
}

func (d *Dispatcher) dispatchDiscussion(ctx context.Context, task ScheduledTask, run ScheduledRun) (ScheduledRun, error) {
	if d.discussion == nil {
		return run, ErrServiceDependency
	}
	target := normalizeTarget(task.Target)
	viewer, err := discussionViewerForTask(task)
	if err != nil {
		return run, err
	}
	conversation, err := d.discussion.SendMessage(ctx, viewer, discussionsession.SendMessageCommand{
		ConversationID:       target.ConversationID,
		Content:              scheduledDiscussionContent(task, run),
		InitialMemberIDs:     scheduledDiscussionInitialMemberIDs(target),
		AllowMemberAdditions: target.AllowMemberAdditions,
		Mentions:             scheduledDiscussionLeadMention(target),
		IdempotencyKey:       scheduledRunIdempotency(run.ScheduledRunID),
	})
	if err != nil {
		return run, err
	}
	run.ConversationID = firstNonBlank(conversation.ConversationID, target.ConversationID)
	return run, nil
}

func (d *Dispatcher) dispatchAgentTask(ctx context.Context, task ScheduledTask, run ScheduledRun) (ScheduledRun, error) {
	if d.agentTasks == nil {
		return run, ErrServiceDependency
	}
	creator, err := d.resolveCreatorPermissions(ctx, task)
	if err != nil {
		return run, err
	}
	target := normalizeTarget(task.Target)
	executionEnvelope, err := scheduledExecutionEnvelope(task, run, creator, target)
	if err != nil {
		return run, err
	}
	receipt, err := d.agentTasks.StartAgentTask(ctx, AgentTaskRequest{
		TenantID:           task.TenantID,
		MissionID:          task.MissionID,
		RunID:              run.ScheduledRunID,
		AgentTaskID:        firstNonBlank(task.AgentTaskID, AgentTaskIDForScheduledTask(task.ScheduledTaskID)),
		ScheduledTaskID:    task.ScheduledTaskID,
		Title:              task.Title,
		Description:        task.Description,
		AgentID:            target.AgentID,
		ApprovalAssigneeID: target.ApprovalAssigneeID,
		TaskGoal:           target.TaskGoal,
		Prompt:             firstNonBlank(target.Prompt, target.TaskGoal),
		PrincipalID:        strings.TrimSpace(task.CreatedBy),
		CreatorProfileID:   strings.TrimSpace(task.CreatorProfileID),
		CreatorKind:        normalizeViewerKind(task.CreatedByKind),
		CreatorName:        strings.TrimSpace(task.CreatedByName),
		Permissions:        append([]string(nil), creator.PermissionScope...),
		MaxAttempts:        max(1, task.Policy.MaxRetries+1),
		RetryDelay:         task.Policy.RetryDelay,
		ResultContract:     cloneAgentResultContract(target.ResultContract),
		Execution:          task.Execution,
		Delivery:           task.Delivery,
		ExecutionEnvelope:  agentcore.CloneExecutionEnvelope(executionEnvelope),
	})
	if err != nil {
		return run, err
	}
	receiptAgentID := strings.TrimSpace(receipt.AgentID)
	if receiptAgentID != "" && receiptAgentID != target.AgentID {
		return run, fmt.Errorf("%w: dispatched Agent %q does not match scheduled Agent %q", ErrRevisionConflict, receiptAgentID, target.AgentID)
	}
	run.CommandID = firstNonBlank(receipt.CommandID, run.ScheduledRunID)
	run.AgentID = target.AgentID
	run.MissionID = strings.TrimSpace(receipt.MissionID)
	run.TaskID = strings.TrimSpace(receipt.TaskID)
	run.ResponsibilityID = strings.TrimSpace(receipt.ResponsibilityID)
	run.ExecutionEnvelope = executionEnvelope
	return run, nil
}

func (d *Dispatcher) dispatchToolCall(ctx context.Context, task ScheduledTask, run ScheduledRun) (ScheduledRun, error) {
	if d.toolCalls == nil {
		return run, ErrServiceDependency
	}
	creator, err := d.resolveCreatorPermissions(ctx, task)
	if err != nil {
		return run, err
	}
	target := normalizeTarget(task.Target)
	executionEnvelope, err := scheduledExecutionEnvelope(task, run, creator, target)
	if err != nil {
		return run, err
	}
	result, err := d.toolCalls.CallTool(ctx, ToolCallRequest{
		TenantID:          normalizeTenantID(task.TenantID),
		RunID:             strings.TrimSpace(run.ScheduledRunID),
		ToolID:            target.ToolID,
		PrincipalID:       strings.TrimSpace(task.CreatedBy),
		ProfileID:         strings.TrimSpace(task.CreatorProfileID),
		Permissions:       append([]string(nil), creator.PermissionScope...),
		Arguments:         cloneAnyMap(target.ToolArguments),
		IdempotencyKey:    scheduledRunIdempotency(run.ScheduledRunID),
		ExecutionEnvelope: executionEnvelope,
	})
	if err != nil {
		return run, err
	}
	if id := firstOutputID(result.Output); id != "" {
		run.CommandID = "tool:" + id
	} else {
		run.CommandID = "tool:" + target.ToolID
	}
	run.ExecutionEnvelope = executionEnvelope
	return run, nil
}

func scheduledExecutionEnvelope(task ScheduledTask, run ScheduledRun, creator CreatorPermissions, target Target) (*agentcore.ExecutionEnvelope, error) {
	at := run.StartedAt.UTC()
	if at.IsZero() {
		at = time.Now().UTC()
	}
	principalID := firstNonBlank(task.CreatedBy, task.CreatorProfileID, target.AgentID)
	agentID := firstNonBlank(target.AgentID, task.CreatorProfileID, task.CreatedBy)
	profileID := firstNonBlank(target.AgentID, task.CreatorProfileID, task.CreatedBy)
	if existing := agentcore.CloneExecutionEnvelope(run.ExecutionEnvelope); existing != nil {
		if err := existing.Verify(); err != nil {
			return nil, fmt.Errorf("verify persisted scheduled execution envelope: %w", err)
		}
		if strings.TrimSpace(existing.TenantID) != normalizeTenantID(task.TenantID) ||
			strings.TrimSpace(existing.RunID) != strings.TrimSpace(run.ScheduledRunID) ||
			strings.TrimSpace(existing.ScheduledTaskID) != strings.TrimSpace(task.ScheduledTaskID) ||
			strings.TrimSpace(existing.ScheduledRunID) != strings.TrimSpace(run.ScheduledRunID) ||
			strings.TrimSpace(existing.AgentID) != strings.TrimSpace(agentID) ||
			strings.TrimSpace(existing.AgentProfileID) != strings.TrimSpace(profileID) {
			return nil, fmt.Errorf("persisted scheduled execution envelope identity changed")
		}
		if missionID := strings.TrimSpace(task.MissionID); missionID != "" && strings.TrimSpace(existing.MissionID) != "" && strings.TrimSpace(existing.MissionID) != missionID {
			return nil, fmt.Errorf("persisted scheduled execution envelope Mission identity changed")
		}
		return existing, nil
	}
	principalKind := agentcore.ExecutionPrincipalSystem
	switch normalizeViewerKind(task.CreatedByKind) {
	case ViewerKindUser, ViewerKindHuman:
		principalKind = agentcore.ExecutionPrincipalUser
	case ViewerKindAgent:
		principalKind = agentcore.ExecutionPrincipalAgent
	}
	envelope := &agentcore.ExecutionEnvelope{
		TenantID:         normalizeTenantID(task.TenantID),
		RunID:            strings.TrimSpace(run.ScheduledRunID),
		ScheduledTaskID:  strings.TrimSpace(task.ScheduledTaskID),
		ScheduledRunID:   strings.TrimSpace(run.ScheduledRunID),
		MissionID:        strings.TrimSpace(task.MissionID),
		AgentID:          agentID,
		AgentProfileID:   profileID,
		PrincipalID:      principalID,
		PrincipalKind:    principalKind,
		PermissionScopes: append([]string(nil), creator.PermissionScope...),
		AllowedToolIDs:   append([]string(nil), task.Execution.AllowedToolIDs...),
	}
	if len(creator.OrganizationIDs) > 0 {
		envelope.OrganizationID = strings.TrimSpace(creator.OrganizationIDs[0])
	}
	if err := envelope.SealAt(at); err != nil {
		return nil, err
	}
	return envelope, nil
}

func (d *Dispatcher) dispatchMemoryExperienceDistillation(ctx context.Context, task ScheduledTask, run ScheduledRun) (ScheduledRun, error) {
	if d.memoryDistillation == nil {
		return run, ErrServiceDependency
	}
	target := normalizeTarget(task.Target)
	creator, err := d.resolveCreatorPermissions(ctx, task)
	if err != nil {
		return run, err
	}
	response, err := d.memoryDistillation.DistillExperience(ctx, memoryDistillationViewer(task, target, creator, run.ScheduledRunID), memorydistill.DistillExperienceRequest{
		TargetScope: memorydistill.MemoryScopeDTO{Kind: target.TargetScopeKind, ID: target.TargetScopeID},
		SourceScope: memorydistill.MemoryScopeDTO{Kind: target.SourceScopeKind, ID: target.SourceScopeID},
		Topic:       target.MemoryTopic,
		Limit:       target.MemoryRecordLimit,
	})
	if err != nil {
		if errors.Is(err, memorydistill.ErrNoCandidates) {
			return run, nil
		}
		return run, err
	}
	if len(response.Candidates) > 0 {
		run.CommandID = response.Candidates[0].CandidateID
	}
	return run, nil
}

func firstOutputID(output map[string]any) string {
	for _, key := range []string{"command_id", "todo_id", "idea_id", "id"} {
		if value, ok := output[key]; ok {
			text := strings.TrimSpace(fmt.Sprint(value))
			if text != "" {
				return text
			}
		}
	}
	return ""
}

func cloneAnyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func (d *Dispatcher) dispatchMemoryWorldConsolidation(ctx context.Context, task ScheduledTask, run ScheduledRun) (ScheduledRun, error) {
	if d.memoryDistillation == nil {
		return run, ErrServiceDependency
	}
	target := normalizeTarget(task.Target)
	creator, err := d.resolveCreatorPermissions(ctx, task)
	if err != nil {
		return run, err
	}
	response, err := d.memoryDistillation.DistillWorld(ctx, memoryDistillationViewer(task, target, creator, run.ScheduledRunID), memorydistill.DistillWorldRequest{
		TargetScope: memorydistill.MemoryScopeDTO{Kind: target.TargetScopeKind, ID: target.TargetScopeID},
		SourceScope: memorydistill.MemoryScopeDTO{Kind: target.SourceScopeKind, ID: target.SourceScopeID},
		Topic:       target.MemoryTopic,
		Limit:       target.MemoryRecordLimit,
	})
	if err != nil {
		if errors.Is(err, memorydistill.ErrNoCandidates) {
			return run, nil
		}
		return run, err
	}
	if len(response.Candidates) > 0 {
		run.CommandID = response.Candidates[0].CandidateID
	}
	return run, nil
}

func (d *Dispatcher) dispatchMemoryLifecycleSweep(ctx context.Context, task ScheduledTask, run ScheduledRun) (ScheduledRun, error) {
	if d.memoryLifecycle == nil {
		return run, ErrServiceDependency
	}
	target := normalizeTarget(task.Target)
	creator, err := d.resolveCreatorPermissions(ctx, task)
	if err != nil {
		return run, err
	}
	response, err := d.memoryLifecycle.SweepRetention(ctx, memoryLifecycleViewer(task, target, creator, run.ScheduledRunID), memoryadmin.MemoryRetentionSweepRequest{
		Limit:  target.MemoryRecordLimit,
		Reason: "scheduled lifecycle sweep " + run.ScheduledRunID,
	})
	if err != nil {
		return run, err
	}
	run.CommandID = fmt.Sprintf("archived:%d", response.ArchivedCount)
	return run, nil
}

func (d *Dispatcher) completeRun(run ScheduledRun, status RunStatus, failureReason string) ScheduledRun {
	run.Status = status
	run.FailureReason = failureReason
	run.CompletedAt = d.now()
	return run
}

func (d *Dispatcher) now() time.Time {
	return d.clock.Now().UTC()
}

func (d *Dispatcher) resolveCreatorPermissions(ctx context.Context, task ScheduledTask) (CreatorPermissions, error) {
	if d.creatorPermissions == nil {
		return CreatorPermissions{}, ErrServiceDependency
	}
	ref, err := creatorRefForTask(task)
	if err != nil {
		return CreatorPermissions{}, err
	}
	return d.creatorPermissions.ResolveCreatorPermissions(ctx, ref)
}

func creatorRefForTask(task ScheduledTask) (CreatorRef, error) {
	ref := CreatorRef{
		ViewerID:    strings.TrimSpace(task.CreatedBy),
		ViewerKind:  normalizeViewerKind(task.CreatedByKind),
		DisplayName: strings.TrimSpace(task.CreatedByName),
		ProfileID:   strings.TrimSpace(task.CreatorProfileID),
	}
	if ref.ViewerID == "" || ref.ProfileID == "" {
		return CreatorRef{}, ErrInvalidRequest
	}
	return ref, nil
}

func discussionViewerForTask(task ScheduledTask) (discussionsession.ViewerRef, error) {
	ref, err := creatorRefForTask(task)
	if err != nil {
		return discussionsession.ViewerRef{}, err
	}
	kind := discussionViewerKind(ref.ViewerKind)
	viewerID := ref.ViewerID
	agentProfileID := ""
	if kind == discussionsession.ViewerKindUser || kind == discussionsession.ViewerKindHuman {
		viewerID = ref.ProfileID
		agentProfileID = ref.ProfileID
	}
	return discussionsession.ViewerRef{
		ViewerID:       viewerID,
		AgentProfileID: agentProfileID,
		ViewerKind:     kind,
		DisplayName:    firstNonBlank(ref.DisplayName, ref.ViewerID),
	}, nil
}

func discussionViewerKind(kind ViewerKind) discussionsession.ViewerKind {
	switch normalizeViewerKind(kind) {
	case ViewerKindAgent:
		return discussionsession.ViewerKindAgent
	case ViewerKindHuman:
		return discussionsession.ViewerKindHuman
	case ViewerKindSystem:
		return discussionsession.ViewerKindSystem
	default:
		return discussionsession.ViewerKindUser
	}
}

func memoryDistillationViewer(task ScheduledTask, target Target, creator CreatorPermissions, runID string) memorydistill.ViewerRef {
	viewer := memorydistill.ViewerRef{
		ViewerID:        strings.TrimSpace(task.CreatedBy),
		ViewerKind:      string(normalizeViewerKind(task.CreatedByKind)),
		AgentID:         strings.TrimSpace(creator.AgentID),
		OrganizationIDs: append([]string(nil), creator.OrganizationIDs...),
		DepartmentIDs:   append([]string(nil), creator.DepartmentIDs...),
		ProjectIDs:      append([]string(nil), creator.ProjectIDs...),
		MissionIDs:      append([]string(nil), creator.MissionIDs...),
		DiscussionIDs:   append([]string(nil), creator.DiscussionIDs...),
		PermissionScope: append([]string(nil), creator.PermissionScope...),
		RequestID:       scheduledRunIdempotency(runID),
		IdempotencyKey:  scheduledRunIdempotency(runID),
	}
	addMemoryViewerScope(&viewer, target.SourceScopeKind, target.SourceScopeID)
	addMemoryViewerScope(&viewer, target.TargetScopeKind, target.TargetScopeID)
	return viewer
}

func memoryLifecycleViewer(task ScheduledTask, target Target, creator CreatorPermissions, runID string) memoryadmin.ViewerRef {
	viewer := memoryadmin.ViewerRef{
		ViewerID:        strings.TrimSpace(task.CreatedBy),
		ViewerKind:      string(normalizeViewerKind(task.CreatedByKind)),
		AgentID:         strings.TrimSpace(creator.AgentID),
		OrganizationIDs: append([]string(nil), creator.OrganizationIDs...),
		DepartmentIDs:   append([]string(nil), creator.DepartmentIDs...),
		ProjectIDs:      append([]string(nil), creator.ProjectIDs...),
		MissionIDs:      append([]string(nil), creator.MissionIDs...),
		DiscussionIDs:   append([]string(nil), creator.DiscussionIDs...),
		PermissionScope: append([]string(nil), creator.PermissionScope...),
		RequestID:       scheduledRunIdempotency(runID),
		IdempotencyKey:  scheduledRunIdempotency(runID),
	}
	addMemoryAdminViewerScope(&viewer, target.TargetScopeKind, target.TargetScopeID)
	return viewer
}

func addMemoryViewerScope(viewer *memorydistill.ViewerRef, kind string, id string) {
	id = strings.TrimSpace(id)
	if viewer == nil || id == "" {
		return
	}
	switch strings.TrimSpace(kind) {
	case "organization":
		viewer.OrganizationIDs = append(viewer.OrganizationIDs, id)
	case "department":
		viewer.DepartmentIDs = append(viewer.DepartmentIDs, id)
	case "project":
		viewer.ProjectIDs = append(viewer.ProjectIDs, id)
	case "mission":
		viewer.MissionIDs = append(viewer.MissionIDs, id)
	case "discussion":
		viewer.DiscussionIDs = append(viewer.DiscussionIDs, id)
	case "agent":
		viewer.AgentID = id
	}
}

func addMemoryAdminViewerScope(viewer *memoryadmin.ViewerRef, kind string, id string) {
	id = strings.TrimSpace(id)
	if viewer == nil || id == "" {
		return
	}
	switch strings.TrimSpace(kind) {
	case "organization":
		viewer.OrganizationIDs = append(viewer.OrganizationIDs, id)
	case "department":
		viewer.DepartmentIDs = append(viewer.DepartmentIDs, id)
	case "project":
		viewer.ProjectIDs = append(viewer.ProjectIDs, id)
	case "mission":
		viewer.MissionIDs = append(viewer.MissionIDs, id)
	case "discussion":
		viewer.DiscussionIDs = append(viewer.DiscussionIDs, id)
	case "agent":
		viewer.AgentID = id
	}
}

func scheduledDiscussionContent(task ScheduledTask, run ScheduledRun) string {
	message := strings.TrimSpace(task.Target.Message)
	title := strings.TrimSpace(task.Title)
	if title == "" {
		return message
	}
	return "Scheduled task: " + title + "\nRun: " + run.ScheduledRunID + "\n\n" + message
}

func scheduledRunIdempotency(runID string) string {
	return "scheduled-run:" + strings.TrimSpace(runID)
}

func scheduledDiscussionInitialMemberIDs(target Target) []string {
	if strings.TrimSpace(target.ConversationID) != "" {
		return nil
	}
	return cleanStringList(append([]string{target.LeadAgentID}, target.InitialMemberIDs...))
}

func scheduledDiscussionLeadMention(target Target) []discussionsession.MessageMentionDTO {
	leadAgentID := strings.TrimSpace(target.LeadAgentID)
	if leadAgentID == "" {
		return nil
	}
	return []discussionsession.MessageMentionDTO{{TargetKind: "agent", TargetID: leadAgentID}}
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
