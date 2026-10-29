package agentkernel

import (
	"context"
	"errors"
	"orrery_v2/internal/agent/llm"
	"strings"
	"sync"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/providers/kernelcompat"
	"orrery_v2/internal/agent/runtime/providers/profilemodel"
)

type Engine struct {
	config    Config
	callbacks *callbackExecutor
}

func NewEngine(config Config) (*Engine, error) {
	config, err := config.normalized()
	if err != nil {
		return nil, err
	}
	return &Engine{config: config, callbacks: &callbackExecutor{slots: make(chan struct{}, config.MaxConcurrentCallbacks)}}, nil
}

// OpenSession resolves only a host-created workspace. Model/config overrides in
// SessionConfig cannot expand the request previously authorized by the host.
func (e *Engine) OpenSession(ctx context.Context, spec agentcore.SessionSpec, config agentcore.SessionConfig) (kernelcompat.Runner, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if spec.Mode != agentcore.SessionOpenFresh && (spec.Mode != agentcore.SessionOpenResume || e.config.Checkpoints == nil || strings.TrimSpace(spec.BackendSessionID) == "") {
		return nil, ErrInput
	}
	binding, err := e.config.Workspaces.Resolve(config.WorkspaceRef)
	if err != nil {
		return nil, err
	}
	req := binding.Request
	req.ToolBindings = appendAuthorizedPlanTools(req)
	if responsibility := req.ContextPack.ResponsibilityContext; responsibility != nil && responsibility.RequireExplicitToolScopes {
		if !e.SupportsExplicitToolScopeFence() || !explicitTaskScopePresent(responsibility) {
			return nil, ErrToolDenied
		}
	}
	if len(req.ToolBindings) > 0 && (e.config.Tools == nil || e.config.AuthorizeTool == nil || e.config.ToolJournal == nil) {
		return nil, ErrConfiguration
	}
	tools, infos, err := prepareTools(req.ToolBindings)
	if err != nil {
		return nil, err
	}
	messages, err := initialMessages(req)
	if err != nil {
		return nil, err
	}
	inputLimit := e.config.MaxContextBytes
	if e.config.AutoCompact {
		inputLimit *= 2
	}
	if _, err := cloneBounded(messages, inputLimit); err != nil {
		return nil, err
	}
	if req.RuntimeOptions.ProviderSpecific["agentcore_task_mode"] == "long" && (req.TaskID == "" || e.config.Checkpoints == nil) {
		return nil, ErrInput
	}
	var checkpoint *Checkpoint
	cacheKey := ""
	if e.config.Checkpoints != nil || e.config.PromptCache {
		scope, err := checkpointScope(req)
		if err != nil {
			return nil, err
		}
		id := newCheckpointID()
		if automaticTask(req) && spec.Mode == agentcore.SessionOpenFresh {
			id = taskCheckpointID(req)
		}
		if e.config.Checkpoints != nil {
			owner := newCheckpointID()
			if spec.Mode == agentcore.SessionOpenResume {
				writeCtx, cancel := context.WithTimeout(ctx, e.config.JournalTimeout)
				claimed, claimErr := e.config.Checkpoints.Claim(writeCtx, req.TenantID, req.AgentID, spec.BackendSessionID, scope, owner, req.RunID)
				cancel()
				if claimErr != nil {
					return nil, claimErr
				}
				checkpoint = &claimed
				messages, err = cloneBounded(claimed.Messages, inputLimit)
				if err != nil {
					return nil, err
				}
				if claimed.Phase == CheckpointCompleted {
					message, err := freshLastUserMessage(req)
					if err != nil {
						return nil, err
					}
					messages = append(messages, message)
				} else if requestPrompt(req) != claimed.FirstPrompt {
					return nil, ErrInput
				}
				id = claimed.ID
			} else {
				checkpoint = &Checkpoint{ID: id, TenantID: req.TenantID, AgentID: req.AgentID, RunID: req.RunID, MissionID: req.MissionID, TaskID: req.TaskID, ScopeHash: scope, Owner: owner, Phase: CheckpointReady, Messages: messages, FirstPrompt: requestPrompt(req)}
				if automaticTask(req) {
					checkpoint.InitialMemoryEvidence = taskMemoryEvidence(req)
				}
			}
		}
		if e.config.PromptCache {
			digest, _ := digest([]string{scope, id})
			cacheKey = "orrery-" + digest[:56]
		}
	}
	modelCtx, stopModel := context.WithTimeout(ctx, e.config.ModelTimeout)
	defer stopModel()
	chat, err := invokeCallback(modelCtx, e.callbacks, func() (llm.ChatModel, error) {
		return e.config.Models.ModelForRequest(modelCtx, profilemodel.ModelRequest{
			Model:           req.RuntimeOptions.Model,
			LLMProfileID:    req.RuntimeOptions.ProviderSpecific["llm_profile_id"],
			ReasoningEffort: req.RuntimeOptions.ProviderSpecific["reasoning_effort"],
			PromptCacheKey:  cacheKey,
			ManagedContext:  true,
			StreamResponses: true,
		})
	}, nil)
	if err != nil {
		if modelCtx.Err() != nil {
			return nil, modelCtx.Err()
		}
		if errors.Is(err, ErrCapacity) {
			return nil, ErrCapacity
		}
		return nil, ErrModel
	}
	if chat == nil {
		return nil, ErrConfiguration
	}
	if checkpoint != nil {
		identity := ""
		if identified, ok := chat.(interface{ ModelIdentity() string }); ok {
			identity = identified.ModelIdentity()
		}
		if spec.Mode == agentcore.SessionOpenResume && identity != checkpoint.ModelIdentity {
			return nil, ErrCheckpoint
		}
		checkpoint.ModelIdentity = identity
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resumed, replayCompleted := spec.Mode == agentcore.SessionOpenResume, false
	automaticOpen := automaticTask(req) && spec.Mode == agentcore.SessionOpenFresh
	if automaticOpen {
		store, ok := e.config.Checkpoints.(taskCheckpointStore)
		if !ok {
			return nil, ErrConfiguration
		}
		writeCtx, stop := context.WithTimeout(ctx, e.config.JournalTimeout)
		opened, existing, err := store.OpenTask(writeCtx, *checkpoint)
		stop()
		if err != nil {
			return nil, err
		}
		checkpoint, resumed = &opened, existing
		messages, err = cloneBounded(opened.Messages, inputLimit)
		if err != nil {
			return nil, err
		}
		replayCompleted = existing && opened.Phase == CheckpointCompleted
	}
	lifetime, cancel := context.WithCancel(context.Background())
	runner := &Runner{config: e.config, callbacks: e.callbacks, request: req, model: chat, tools: tools, infos: infos,
		messages: messages, firstPrompt: requestPrompt(req),
		first: true, ctx: lifetime, cancel: cancel, checkpoint: checkpoint, replayCompleted: replayCompleted}
	if checkpoint != nil && checkpoint.CompactionProgress != nil {
		if !e.config.AutoCompact || checkpoint.CompactionProgress.PolicyHash != runner.compactionPolicy() {
			cancel()
			return nil, ErrCheckpoint
		}
		runner.compactionProgress = cloneCompactionProgress(checkpoint.CompactionProgress)
	}
	if checkpoint != nil {
		runner.backendID = checkpoint.ID
		if !resumed && !automaticOpen {
			writeCtx, stop := context.WithTimeout(ctx, e.config.JournalTimeout)
			created, err := e.config.Checkpoints.Create(writeCtx, *checkpoint)
			stop()
			if err != nil {
				cancel()
				return nil, err
			}
			runner.checkpoint = &created
		} else if resumed {
			runner.compactions = checkpoint.Compactions
			runner.usage = checkpoint.Usage
			runner.observation = checkpoint.Observation
			runner.blockedTaskReviewed = checkpoint.BlockedTaskReviewed || checkpoint.BlockedResultReviewRunID == req.RunID
			runner.turn = checkpoint.Turn
			if checkpoint.Phase == CheckpointReady || checkpoint.ApprovalWait != nil {
				runner.deadline = checkpoint.Deadline
				runner.continuing = true
				runner.modelCalls = checkpoint.ModelCalls
				runner.windowRecoveries = checkpoint.WindowRecoveries
				runner.toolCalls = checkpoint.ToolCalls
				if runner.turn > 0 {
					runner.turn--
				}
			}
		}
	}
	return runner, nil
}

type Runner struct {
	config             Config
	callbacks          *callbackExecutor
	request            agentcore.RuntimeRequest
	model              llm.ChatModel
	tools              map[string]boundTool
	infos              []*llm.Tool
	ctx                context.Context
	cancel             context.CancelFunc
	mu                 sync.Mutex
	closed             bool
	failed             bool
	active             bool
	first              bool
	firstPrompt        string
	turn               uint64
	messages           []*llm.Message
	checkpoint         *Checkpoint
	backendID          string
	deadline           time.Time
	modelCalls         int
	toolCalls          int
	compactions        int
	windowRecoveries   int
	usage              KernelUsage
	observation        ContextObservation
	compactionProgress *CompactionProgress
	continuing         bool
	replayCompleted    bool
	canResumeTask      bool
	approvalMu         sync.Mutex
	pendingApproval    *toolApproval

	blockedTaskReviewed bool
}

func (e *Engine) SupportsResume() bool            { return e.config.Checkpoints != nil }
func (e *Engine) SupportsPermissionAnswers() bool { return len(e.config.ApprovalToolIDs) > 0 }
func (e *Engine) SupportsExplicitToolScopeFence() bool {
	return e.config.ExplicitToolScopeFence && e.config.AuthorizeTool != nil && e.config.Tools != nil && e.config.ToolJournal != nil
}
func (r *Runner) BackendSessionID() string {
	return r.backendID
}

// The host request, resolved through the workspace broker, owns this budget.
// Model input and session transport configuration cannot extend it.
func (r *Runner) TurnTimeout() time.Duration {
	if !r.deadline.IsZero() {
		return max(time.Millisecond, time.Until(r.deadline))
	}
	if !r.request.Deadline.IsZero() {
		return max(time.Millisecond, time.Until(r.request.Deadline))
	}
	if seconds, err := time.ParseDuration(r.request.RuntimeOptions.ProviderSpecific["timeout_seconds"] + "s"); err == nil && seconds > 0 {
		return seconds
	}
	if r.request.TaskID != "" && r.request.RuntimeOptions.ProviderSpecific["agentcore_task_mode"] == "long" {
		return r.config.LongTaskTimeout
	}
	return 0
}

func (r *Runner) Run(ctx context.Context, input agentcore.SendCommand, emit kernelcompat.Emit) (result agentcore.TurnResultEvent, runErr error) {
	defer func() { runErr = publicExecutionError(runErr) }()
	if ctx == nil {
		ctx = context.Background()
	}
	if emit == nil {
		return result, ErrInput
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	r.mu.Lock()
	if r.closed || r.failed {
		r.mu.Unlock()
		return result, ErrClosed
	}
	if r.active {
		r.mu.Unlock()
		return result, ErrBusy
	}
	r.active = true
	defer r.observeDependency(ctx, "runner_returned", nil, nil, nil, "")
	r.canResumeTask = false
	if r.replayCompleted {
		r.mu.Unlock()
		defer func() { r.mu.Lock(); r.active = false; r.mu.Unlock() }()
		return r.replayTaskResult(ctx, input, emit)
	}
	r.turn++
	if !r.continuing {
		r.modelCalls, r.toolCalls = 0, 0
		r.windowRecoveries = 0
		r.blockedTaskReviewed = false
		if r.turn > 1 && r.checkpoint != nil {
			r.checkpoint.BlockedResultReviewRunID = ""
		}
	}
	r.continuing = false
	first, turn := r.first, r.turn
	inputLimit := r.config.MaxContextBytes
	if r.config.AutoCompact {
		inputLimit *= 2
	}
	messages, err := cloneBounded(r.messages, inputLimit)
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.active = false
		// A panic also leaves the session unusable. The compatibility adapter
		// owns panic recovery and emits the authoritative failure event.
		if runErr != nil || result.Outcome.Kind == "" {
			r.canResumeTask = r.taskCanRecover(runErr, ctx)
			r.failed = true
			return
		}
		if r.closed {
			if runErr == nil {
				runErr = context.Canceled
				result = agentcore.TurnResultEvent{}
			}
			return
		}
		r.messages, r.first = messages, false
		if automaticTask(r.request) {
			r.replayCompleted = true
		}
	}()
	if err != nil {
		return result, err
	}
	text, err := inputText(input)
	if err != nil {
		return result, err
	}
	if first && r.firstPrompt != "" {
		if _, err := inputParts(input.Content); err != nil {
			return result, err
		}
		if strings.TrimSpace(text) != r.firstPrompt || !sameInputImages(input.Content, r.request.InputMedia) {
			return result, ErrInput
		}
	} else {
		message, err := inputMessage(input.Content)
		if err != nil {
			return result, err
		}
		messages = append(messages, message)
	}
	r.firstPrompt = strings.TrimSpace(text)
	execution, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(r.ctx, cancel)
	defer stop()
	defer cancel()
	if timeout := r.TurnTimeout(); timeout > 0 && r.deadline.IsZero() {
		r.deadline = time.Now().Add(timeout)
	}
	// A persisted or transport budget cannot extend the host's absolute limit.
	if hostDeadline := r.request.Deadline; !hostDeadline.IsZero() && (r.deadline.IsZero() || hostDeadline.Before(r.deadline)) {
		r.deadline = hostDeadline
	}
	if !r.deadline.IsZero() {
		bounded, stopDeadline := context.WithDeadline(execution, r.deadline)
		defer stopDeadline()
		execution = bounded
	}
	if r.checkpoint != nil && !automaticTask(r.request) {
		r.checkpoint.FirstPrompt = r.firstPrompt
	}
	if r.checkpoint != nil && r.checkpoint.ApprovalWait != nil {
		messages, err = r.resumeApprovalWait(execution, messages, emit)
		if err != nil {
			return result, err
		}
	} else {
		if err := r.saveCheckpoint(execution, CheckpointReady, messages); err != nil {
			return result, err
		}
	}
	result, messages, runErr = r.loop(execution, messages, turn, emit)
	if runErr == nil {
		r.deadline = time.Time{}
	}
	return result, runErr
}

// Close may run concurrently with Run. Cancellation reaches both model streams
// and tools; the host's compatibility layer separately bounds shutdown waiting.
func (r *Runner) Close(context.Context) error {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	r.cancel()
	return nil
}
