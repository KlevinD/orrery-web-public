package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
)

type RunStatus string

const (
	RunStatusQueued    RunStatus = "queued"
	RunStatusRunning   RunStatus = "running"
	RunStatusCompleted RunStatus = "completed"
	RunStatusFailed    RunStatus = "failed"
	RunStatusBlocked   RunStatus = "blocked"
)

func (s RunStatus) String() string { return string(s) }

type AgentRun struct {
	RunID           agentcore.AgentRunID
	AgentID         agentcore.AgentID
	AgentProfileID  agentcore.AgentProfileID
	AgentInstanceID agentcore.AgentInstanceID
	RuntimeID       string
	MissionID       string
	TaskID          string
	Status          RunStatus
	ClaimedBy       string
	LeaseExpiresAt  time.Time
	HeartbeatAt     time.Time
	Attempt         int
	Task            agentcore.TaskInput
	Request         agentcore.RuntimeRequest
	Response        agentcore.RuntimeResponse
	LastError       string
}

type RunStore interface {
	Enqueue(ctx context.Context, run AgentRun) error
	Claim(ctx context.Context, workerID string) (AgentRun, bool, error)
	Complete(ctx context.Context, runID agentcore.AgentRunID, response agentcore.RuntimeResponse) error
	Fail(ctx context.Context, runID agentcore.AgentRunID, message string) error
	Block(ctx context.Context, runID agentcore.AgentRunID, message string, response agentcore.RuntimeResponse) error
	Heartbeat(ctx context.Context, runID agentcore.AgentRunID, workerID string) error
}

type RuntimeLifecyclePublisher interface {
	PublishRuntimeLifecycle(ctx context.Context, event RuntimeLifecycleEvent) error
	PublishRuntimeFailure(ctx context.Context, event RuntimeFailureEvent) error
}

type RuntimeLifecycleKind string

const (
	RuntimeLifecycleStarted   RuntimeLifecycleKind = "started"
	RuntimeLifecycleCompleted RuntimeLifecycleKind = "completed"
	RuntimeLifecycleFailed    RuntimeLifecycleKind = "failed"
	RuntimeLifecycleTimedOut  RuntimeLifecycleKind = "timed_out"
	RuntimeLifecycleCancelled RuntimeLifecycleKind = "cancelled"
	RuntimeLifecycleBlocked   RuntimeLifecycleKind = "blocked"
)

type RuntimeLifecycleEvent struct {
	Kind                  RuntimeLifecycleKind
	TenantID              string
	MissionID             missioncore.MissionID
	TaskID                missioncore.TaskID
	AgentID               agentcore.AgentID
	RunID                 agentcore.AgentRunID
	AgentTaskID           string
	AgentVersionID        string
	ScheduledTaskID       string
	ScheduledRunID        string
	RuntimeKind           string
	QueueID               string
	Status                string
	WorkerID              string
	Message               string
	LastHeartbeatAt       time.Time
	LeaseExpiresAt        time.Time
	Usage                 map[string]agentcore.TokenUsage
	ContextSnapshotID     string
	ContextSummary        string
	InputRefs             []string
	MemoryRefs            []RuntimeMemoryRef
	KnowledgeRefs         []RuntimeKnowledgeRef
	ResponsibilityContext *agentcore.ResponsibilityContext
	TraceID               string
	TraceRef              string
	Artifacts             []agentcore.ArtifactRef
	Evidence              []RuntimeEvidence
	DurationMs            int64
	StartedAt             time.Time
	CompletedAt           time.Time
}

type RuntimeMemoryRef struct {
	ID          string
	MemoryKind  string
	SummaryRef  string
	Sensitivity string
}

type RuntimeKnowledgeRef struct {
	Kind string
	ID   string
}

type RuntimeEvidence struct {
	ID         string
	Kind       string
	Summary    string
	SourceRef  string
	ContentRef string
	Checksum   string
	Status     string
}

type RuntimeFailureEvent struct {
	Kind            RuntimeLifecycleKind
	TenantID        string
	MissionID       missioncore.MissionID
	TaskID          missioncore.TaskID
	AgentID         agentcore.AgentID
	RunID           agentcore.AgentRunID
	AgentTaskID     string
	ScheduledTaskID string
	ScheduledRunID  string
	RuntimeKind     string
	QueueID         string
	WorkerID        string
	Code            string
	Message         string
	TraceRef        string
	Metadata        map[string]string
}

type WorkerOptions struct {
	WorkerID          string
	Store             RunStore
	Agent             agentcore.Agent
	Runtime           agentcore.SessionRuntime
	Lifecycle         RuntimeLifecyclePublisher
	HeartbeatInterval time.Duration
	HardTimeout       time.Duration
	Concurrency       *Limiter
}

type Worker struct {
	workerID          string
	store             RunStore
	agent             agentcore.Agent
	runtime           agentcore.SessionRuntime
	lifecycle         RuntimeLifecyclePublisher
	heartbeatInterval time.Duration
	hardTimeout       time.Duration
	concurrency       *Limiter
}

func NewWorker(opts WorkerOptions) *Worker {
	interval := opts.HeartbeatInterval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	return &Worker{workerID: opts.WorkerID, store: opts.Store, agent: opts.Agent, runtime: opts.Runtime, lifecycle: opts.Lifecycle, heartbeatInterval: interval, hardTimeout: opts.HardTimeout, concurrency: opts.Concurrency}
}

func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	if w.store == nil {
		return false, fmt.Errorf("run store is not configured")
	}
	if w.agent == nil && w.runtime == nil {
		return false, fmt.Errorf("base agent is not configured")
	}
	if w.concurrency != nil && !w.concurrency.TryAcquire() {
		return false, nil
	}
	if w.concurrency != nil {
		defer w.concurrency.Release()
	}
	workerID := w.workerID
	if workerID == "" {
		workerID = "agent-runtime-worker"
	}
	run, ok, err := w.store.Claim(ctx, workerID)
	if err != nil || !ok {
		return ok, err
	}
	if err := w.publishStarted(ctx, run, workerID); err != nil {
		return true, errors.Join(err, w.failRun(ctx, run, workerID, RuntimeLifecycleFailed, "runtime_start_publish_failed", err.Error(), agentcore.RuntimeResponse{}, nil))
	}
	runCtx := ctx
	cancel := func() {}
	if w.hardTimeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, w.hardTimeout)
	}
	defer cancel()
	heartbeatCtx, stopHeartbeatCtx := context.WithCancel(runCtx)
	waitHeartbeat := w.startHeartbeat(heartbeatCtx, run.RunID, workerID)
	defer func() {
		stopHeartbeatCtx()
		waitHeartbeat()
	}()

	response, err := w.runManagedAgent(runCtx, run)
	if err != nil {
		metadata := runtimeErrorMetadata(err)
		kind := runtimeErrorLifecycleKind(err, RuntimeLifecycleFailed)
		recordErr := w.failRun(ctx, run, workerID, kind, runtimeErrorCode(err, "agent_runtime_failed"), err.Error(), runtimeErrorResponse(run, err), metadata)
		if isRuntimePipelineTerminal(err) {
			return true, recordErr
		}
		return true, errors.Join(err, recordErr)
	}
	if response.Status != "" && response.Status != agentcore.RuntimeStatusCompleted {
		message := runtimeFailureMessage(response)
		return true, w.failRun(ctx, run, workerID, terminalRuntimeLifecycleKind(response.Status), runtimeFailureCode(response), message, response, cloneRuntimeMetadata(response.ProviderMetadata))
	}
	if err := w.store.Complete(ctx, run.RunID, response); err != nil {
		return true, err
	}
	return true, w.publishCompleted(ctx, run, workerID, response)
}

// Failure reporting must survive the execution timeout. Return both failures:
// a failed state write must not prevent publication, or silently look complete.
func (w *Worker) failRun(ctx context.Context, run AgentRun, workerID string, kind RuntimeLifecycleKind, code, message string, response agentcore.RuntimeResponse, metadata map[string]string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var stateErr error
	if kind == RuntimeLifecycleBlocked {
		stateErr = w.store.Block(ctx, run.RunID, message, response)
	} else {
		stateErr = w.store.Fail(ctx, run.RunID, message)
	}
	if stateErr != nil {
		stateErr = fmt.Errorf("persist failed agent run %s: %w", run.RunID, stateErr)
	}
	publishErr := w.publishFailure(ctx, run, workerID, kind, code, message, metadata)
	if publishErr != nil {
		publishErr = fmt.Errorf("publish agent failure %s: %w", run.RunID, publishErr)
	}
	return errors.Join(stateErr, publishErr)
}

func (w *Worker) publishStarted(ctx context.Context, run AgentRun, workerID string) error {
	if w == nil || w.lifecycle == nil || !runHasMissionRuntimeIdentity(run) {
		return nil
	}
	return w.lifecycle.PublishRuntimeLifecycle(ctx, lifecycleEventFromRun(run, workerID, RuntimeLifecycleStarted, RunStatusRunning.String(), "agent run started"))
}

func (w *Worker) publishCompleted(ctx context.Context, run AgentRun, workerID string, response agentcore.RuntimeResponse) error {
	if w == nil || w.lifecycle == nil || !runHasMissionRuntimeIdentity(run) {
		return nil
	}
	event := lifecycleEventFromRun(run, workerID, RuntimeLifecycleCompleted, RunStatusCompleted.String(), "agent run completed")
	event.Usage = cloneRuntimeUsage(response.Usage)
	return w.lifecycle.PublishRuntimeLifecycle(ctx, event)
}

func (w *Worker) publishFailure(ctx context.Context, run AgentRun, workerID string, kind RuntimeLifecycleKind, code, message string, metadata map[string]string) error {
	if w == nil || w.lifecycle == nil || !runHasMissionRuntimeIdentity(run) {
		return nil
	}
	return w.lifecycle.PublishRuntimeFailure(ctx, RuntimeFailureEvent{
		Kind:        kind,
		TenantID:    run.Request.TenantID,
		MissionID:   missioncore.MissionID(run.MissionID),
		TaskID:      missioncore.TaskID(run.TaskID),
		AgentID:     run.AgentID,
		RunID:       run.RunID,
		RuntimeKind: run.RuntimeID,
		QueueID:     runQueueID(run),
		WorkerID:    workerID,
		Code:        code,
		Message:     message,
		Metadata:    cloneRuntimeMetadata(metadata),
	})
}

func lifecycleEventFromRun(run AgentRun, workerID string, kind RuntimeLifecycleKind, status, message string) RuntimeLifecycleEvent {
	return RuntimeLifecycleEvent{
		Kind:            kind,
		TenantID:        run.Request.TenantID,
		MissionID:       missioncore.MissionID(run.MissionID),
		TaskID:          missioncore.TaskID(run.TaskID),
		AgentID:         run.AgentID,
		RunID:           run.RunID,
		RuntimeKind:     run.RuntimeID,
		QueueID:         runQueueID(run),
		Status:          status,
		WorkerID:        workerID,
		Message:         message,
		LastHeartbeatAt: run.HeartbeatAt,
		LeaseExpiresAt:  run.LeaseExpiresAt,
	}
}

func terminalRuntimeLifecycleKind(status agentcore.RuntimeStatus) RuntimeLifecycleKind {
	switch status {
	case agentcore.RuntimeStatusTimeout:
		return RuntimeLifecycleTimedOut
	case agentcore.RuntimeStatusCancelled:
		return RuntimeLifecycleCancelled
	case agentcore.RuntimeStatusBlocked:
		return RuntimeLifecycleBlocked
	default:
		return RuntimeLifecycleFailed
	}
}

func runtimeFailureCode(response agentcore.RuntimeResponse) string {
	if response.Error.Code != "" {
		return response.Error.Code
	}
	switch response.Status {
	case agentcore.RuntimeStatusTimeout:
		return "agent_runtime_timeout"
	case agentcore.RuntimeStatusCancelled:
		return "agent_runtime_cancelled"
	case agentcore.RuntimeStatusBlocked:
		return "agent_runtime_blocked"
	default:
		return "agent_runtime_failed"
	}
}

func runtimeFailureMessage(response agentcore.RuntimeResponse) string {
	if response.Error.Message != "" {
		return response.Error.Message
	}
	if response.Status != "" {
		return string(response.Status)
	}
	return "agent runtime failed"
}

func runtimeErrorLifecycleKind(err error, fallback RuntimeLifecycleKind) RuntimeLifecycleKind {
	var runtimeErr agentcore.RuntimePipelineError
	if !errors.As(err, &runtimeErr) {
		return fallback
	}
	return terminalRuntimeLifecycleKind(runtimeErr.Status)
}

func runtimeErrorCode(err error, fallback string) string {
	var runtimeErr agentcore.RuntimePipelineError
	if !errors.As(err, &runtimeErr) || runtimeErr.Status == "" {
		return fallback
	}
	switch runtimeErr.Status {
	case agentcore.RuntimeStatusTimeout:
		return "agent_runtime_timeout"
	case agentcore.RuntimeStatusCancelled:
		return "agent_runtime_cancelled"
	case agentcore.RuntimeStatusBlocked:
		return "agent_runtime_blocked"
	default:
		return fallback
	}
}

func runtimeErrorMetadata(err error) map[string]string {
	var runtimeErr agentcore.RuntimePipelineError
	if errors.As(err, &runtimeErr) {
		return cloneRuntimeMetadata(runtimeErr.ProviderMetadata)
	}
	return nil
}

func runtimeErrorResponse(run AgentRun, err error) agentcore.RuntimeResponse {
	var runtimeErr agentcore.RuntimePipelineError
	if errors.As(err, &runtimeErr) {
		return agentcore.RuntimeResponse{
			RunID:            run.RunID,
			Status:           runtimeErr.Status,
			Error:            agentcore.RuntimeFailure{Code: runtimeErrorCode(err, "agent_runtime_failed"), Message: runtimeErr.Error(), Retryable: runtimeErr.Status == agentcore.RuntimeStatusBlocked || runtimeErr.Status == agentcore.RuntimeStatusTimeout},
			ProviderMetadata: cloneRuntimeMetadata(runtimeErr.ProviderMetadata),
		}
	}
	return agentcore.RuntimeResponse{RunID: run.RunID, Status: agentcore.RuntimeStatusFailed, Error: agentcore.RuntimeFailure{Code: "agent_runtime_failed", Message: err.Error()}}
}

func isRuntimePipelineTerminal(err error) bool {
	var runtimeErr agentcore.RuntimePipelineError
	return errors.As(err, &runtimeErr) && runtimeErr.Status.IsTerminal()
}

func cloneRuntimeMetadata(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for key, value := range src {
		if value != "" {
			out[key] = value
		}
	}
	return out
}

func cloneRuntimeUsage(src map[string]agentcore.TokenUsage) map[string]agentcore.TokenUsage {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]agentcore.TokenUsage, len(src))
	for model, usage := range src {
		if model == "" || isZeroTokenUsage(usage) {
			continue
		}
		out[model] = usage
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isZeroTokenUsage(usage agentcore.TokenUsage) bool {
	return usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.CacheReadTokens == 0 && usage.CacheWriteTokens == 0 && usage.CachedInputTokens == 0 && usage.ReasoningTokens == 0 && !usage.CachedInputTokensKnown && !usage.ReasoningTokensKnown
}

func runHasMissionRuntimeIdentity(run AgentRun) bool {
	return run.MissionID != "" && run.TaskID != "" && !run.AgentID.IsZero() && run.RunID != ""
}

func runQueueID(run AgentRun) string {
	if value := run.Request.RuntimeOptions.ProviderSpecific["queue_id"]; value != "" {
		return value
	}
	if run.MissionID != "" {
		return "mission:" + run.MissionID
	}
	return ""
}

func (w *Worker) runManagedAgent(ctx context.Context, run AgentRun) (agentcore.RuntimeResponse, error) {
	agent := w.agent
	if agent == nil {
		var err error
		agent, err = newWorkerBaseAgent(run, w.runtime)
		if err != nil {
			return agentcore.RuntimeResponse{}, err
		}
	}
	base := agent.Base()
	if base == nil {
		return agentcore.RuntimeResponse{}, fmt.Errorf("base agent is not configured")
	}
	result, err := base.Handle(ctx, AgentInputFromRun(run))
	if err != nil {
		return agentcore.RuntimeResponse{}, err
	}
	return runtimeResponseFromAgentResult(run, result), nil
}

func newWorkerBaseAgent(run AgentRun, runtime agentcore.SessionRuntime) (agentcore.Agent, error) {
	agentID := firstRunValue(run.AgentID.String(), run.Request.AgentID.String(), "agent-runtime-worker")
	instanceID := run.AgentInstanceID
	if instanceID.IsZero() {
		return nil, fmt.Errorf("agent instance id is required")
	}
	profileID := run.AgentProfileID
	if profileID.IsZero() {
		return nil, fmt.Errorf("agent profile id is required")
	}
	runtimeKind := agentcore.RuntimeKind(run.RuntimeID)
	if !runtimeKind.IsValid() {
		runtimeKind = ""
	}
	return agentcore.NewBaseAgent(agentcore.BaseAgentConfig{
		Identity: agentcore.AgentIdentity{
			AgentID:        agentcore.AgentID(agentID),
			AgentProfileID: profileID,
			InstanceRef:    agentcore.AgentInstanceRef{ID: instanceID},
		},
		Profile: agentcore.AgentProfile{
			ID:          profileID,
			RealName:    "周执行",
			DisplayName: "周执行",
			AgentKind:   agentcore.AgentKindAI,
			Persona: agentcore.AgentPersona{
				ResponsibilityBoundary: "负责执行队列任务，并通过 BaseAgent 记录运行、审计和结果提交流程。",
			},
			BaseAgent:   agentcore.DefaultBaseAgentSpec("周执行", "Queued runtime executor"),
			RuntimeSpec: agentcore.RuntimeSpec{Kind: runtimeKind, ConfigRef: run.Request.RuntimeOptions.ProviderSpecific["config_ref"], Config: run.Request.RuntimeOptions.ProviderConfig},
		},
		Runtime:   runtime,
		Delivery:  noopWorkerCommitter{},
		Trace:     agentcore.NoopTraceRecorder{},
		Notifier:  agentcore.NoopNotifier{},
		Validator: agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error { return nil }),
		Summary:   emptyWorkerSummaryGenerator{},
	}), nil
}

func AgentInputFromRun(run AgentRun) agentcore.AgentInput {
	request := run.Request
	runID := run.RunID
	if runID == "" {
		runID = request.RunID
	}
	agentID := agentcore.AgentID(firstRunValue(run.AgentID.String(), request.AgentID.String(), "agent-runtime-worker"))
	missionID := firstRunValue(run.MissionID, request.MissionID, string(request.ContextPack.MissionID), "mission-"+runID.String())
	taskID := firstRunValue(run.TaskID, request.TaskID, string(request.ContextPack.TaskID), "task-"+runID.String())
	goal := firstRunValue(request.ContextPack.WorkerPersonalGoal, request.ContextPack.MissionFinalGoal, "Execute queued agent run "+runID.String())
	pack := request.ContextPack
	if pack.ID == "" {
		pack.ID = agentcore.ContextPackID("ctx-" + runID.String())
	}
	if pack.AgentID == "" {
		pack.AgentID = agentID
	}
	if pack.MissionID == "" {
		pack.MissionID = missionIDFromString(missionID)
	}
	if pack.TaskID == "" {
		pack.TaskID = taskIDFromString(taskID)
	}
	if pack.MissionFinalGoal == "" {
		pack.MissionFinalGoal = goal
	}
	if pack.DiscussionGroupGoal == "" {
		pack.DiscussionGroupGoal = goal
	}
	if pack.WorkerPersonalGoal == "" {
		pack.WorkerPersonalGoal = goal
	}
	if pack.TaskContractRef.ID == "" {
		pack.TaskContractRef = agentcore.TaskContractRef{ID: taskID}
	}
	if len(pack.SourceRefs) == 0 {
		pack.SourceRefs = []agentcore.ContextSourceRef{{
			Kind:      agentcore.ContextSourceKindTaskContract,
			Ref:       agentcore.ContextSourceTypedRef{TaskContractRef: &agentcore.TaskContractRef{ID: taskID}},
			Priority:  agentcore.SourcePriorityContract,
			Immutable: true,
		}}
	}
	if pack.OverridePolicy == "" {
		pack.OverridePolicy = agentcore.OverridePolicyPreserveProtected
	}
	task := run.Task
	if task.ID == "" {
		task.ID = taskID
	}
	if task.MissionID == "" {
		task.MissionID = missionID
	}
	if task.Goal == "" {
		task.Goal = goal
	}
	if task.WorkerPersonalGoal == "" {
		task.WorkerPersonalGoal = pack.WorkerPersonalGoal
	}
	return agentcore.AgentInput{
		RunID:              runID,
		TenantID:           request.TenantID,
		ExecutionEnvelope:  agentcore.CloneExecutionEnvelope(request.ExecutionEnvelope),
		ExecutionPrincipal: request.ExecutionPrincipal,
		ContextPack:        pack,
		Task:               task,
		RuntimeOptions:     request.RuntimeOptions,
	}
}

func runtimeResponseFromAgentResult(run AgentRun, result agentcore.AgentResult) agentcore.RuntimeResponse {
	status := agentcore.RuntimeStatusCompleted
	if result.Blocker != nil {
		status = agentcore.RuntimeStatusBlocked
	}
	var messages []agentcore.RuntimeMessage
	if result.Summary.Text != "" {
		messages = append(messages, agentcore.RuntimeMessage{Type: agentcore.RuntimeMessageText, Content: result.Summary.Text})
	}
	return agentcore.RuntimeResponse{
		RunID:       run.RunID,
		Status:      status,
		Messages:    messages,
		CompletedAt: result.CreatedAt,
	}
}

type noopWorkerCommitter struct{}

func (noopWorkerCommitter) SubmitAgentResult(context.Context, agentcore.AgentResult, agentcore.CommitOptions) error {
	return nil
}

type emptyWorkerSummaryGenerator struct{}

func (emptyWorkerSummaryGenerator) GenerateSummary(input agentcore.AgentInput, response agentcore.RuntimeResponse, result agentcore.AgentResult) (agentcore.Summary, error) {
	summary, err := agentcore.DefaultSummaryGenerator{}.GenerateSummary(input, response, result)
	if err == nil {
		return summary, nil
	}
	return agentcore.Summary{Text: "Agent runtime completed."}, nil
}

func firstRunValue(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func missionIDFromString(value string) missioncore.MissionID { return missioncore.MissionID(value) }

func taskIDFromString(value string) missioncore.TaskID { return missioncore.TaskID(value) }

func (w *Worker) startHeartbeat(ctx context.Context, runID agentcore.AgentRunID, workerID string) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(w.heartbeatInterval)
		defer ticker.Stop()
		defer close(done)
		_ = w.store.Heartbeat(ctx, runID, workerID)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = w.store.Heartbeat(ctx, runID, workerID)
			}
		}
	}()
	return func() {
		<-done
	}
}
