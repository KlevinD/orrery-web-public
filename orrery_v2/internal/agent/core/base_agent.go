package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	MissionLifecycleAuthorityProviderKey    = "mission_lifecycle_authority"
	MissionLifecycleAuthorityRunWorker      = "agent_run_worker"
	CanonicalDiscussionWorkspaceProviderKey = "canonical_discussion_workspace"
	PipelineReplyOwnerProviderKey           = "pipeline_reply_owner"
	PipelineReplyOwnerCaller                = "caller"
	durableTraceFinishTimeout               = 10 * time.Second
)

type durableTraceContextKey struct{}

type BaseAgentConfig struct {
	Identity          AgentIdentity
	Profile           AgentProfile
	Bindings          AgentBindings
	Runtime           SessionRuntime
	Components        any
	Signals           SignalSink
	Delivery          ResultCommitter
	Clock             Clock
	Normalizer        ResultNormalizer
	Validator         ResultValidator
	Summary           SummaryGenerator
	Trace             TraceRecorder
	Notifier          Notifier
	DiaryFlush        DiaryFlushCoordinator
	DiaryFlushTimeout time.Duration
}

type BaseAgent struct {
	identity          AgentIdentity
	profile           AgentProfile
	bindings          AgentBindings
	runtime           SessionRuntime
	components        any
	signals           SignalSink
	delivery          ResultCommitter
	clock             Clock
	normalizer        ResultNormalizer
	validator         ResultValidator
	summary           SummaryGenerator
	trace             TraceRecorder
	notifier          Notifier
	diaryFlush        DiaryFlushCoordinator
	diaryFlushTimeout time.Duration
}

func NewBaseAgent(cfg BaseAgentConfig) *BaseAgent {
	diaryFlushTimeout := cfg.DiaryFlushTimeout
	if diaryFlushTimeout <= 0 {
		diaryFlushTimeout = 45 * time.Second
	}
	return &BaseAgent{
		identity:          cfg.Identity,
		profile:           cfg.Profile,
		bindings:          cfg.Bindings,
		runtime:           cfg.Runtime,
		components:        cfg.Components,
		signals:           cfg.Signals,
		delivery:          cfg.Delivery,
		clock:             cfg.Clock,
		normalizer:        defaultNormalizer(cfg.Normalizer),
		validator:         defaultValidator(cfg.Validator),
		summary:           defaultSummaryGenerator(cfg.Summary),
		trace:             defaultTraceRecorder(cfg.Trace),
		notifier:          defaultNotifier(cfg.Notifier),
		diaryFlush:        cfg.DiaryFlush,
		diaryFlushTimeout: diaryFlushTimeout,
	}
}

func NewProductionBaseAgent(cfg BaseAgentConfig) (*BaseAgent, error) {
	if cfg.Runtime == nil {
		return nil, PipelineDependencyError{Dependency: "runtime", Message: "canonical session runtime is required before production agent execution"}
	}
	if cfg.Delivery == nil {
		return nil, PipelineDependencyError{Dependency: "delivery", Message: "result committer is required before production agent execution"}
	}
	if cfg.Normalizer == nil {
		return nil, PipelineDependencyError{Dependency: "normalizer", Message: "result normalizer is required before production agent execution"}
	}
	if cfg.Validator == nil {
		return nil, PipelineDependencyError{Dependency: "validator", Message: "result validator is required before production agent execution"}
	}
	if cfg.Summary == nil {
		return nil, PipelineDependencyError{Dependency: "summary", Message: "summary generator is required before production agent execution"}
	}
	if cfg.Trace == nil {
		return nil, PipelineDependencyError{Dependency: "trace", Message: "trace recorder is required before production agent execution"}
	}
	if _, ok := cfg.Trace.(DurableTraceRecorder); !ok {
		return nil, PipelineDependencyError{Dependency: "trace", Message: "durable trace recorder is required before production agent execution"}
	}
	if cfg.Notifier == nil {
		return nil, PipelineDependencyError{Dependency: "notifier", Message: "notifier is required before production agent execution"}
	}
	return NewBaseAgent(cfg), nil
}

func (a *BaseAgent) ID() AgentInstanceID {
	return a.identity.InstanceRef.ID
}

func (a *BaseAgent) Base() *BaseAgent {
	return a
}

func (a *BaseAgent) Identity() AgentIdentity {
	return a.identity
}

func (a *BaseAgent) Profile() AgentProfile {
	return a.profile
}

func (a *BaseAgent) ManagedSpec() BaseAgentSpec {
	spec := a.profile.BaseAgent
	spec.Extensions = cloneRawMessageMap(spec.Extensions)
	return spec
}

func (a *BaseAgent) SystemPrompt() string {
	return a.profile.BaseAgent.SystemPrompt
}

func (a *BaseAgent) LLMAPIKeyRef() string {
	return a.profile.BaseAgent.LLMAPIKeyRef
}

func (a *BaseAgent) SkillScope() string {
	return a.profile.BaseAgent.SkillScope
}

func (a *BaseAgent) ToolScope() string {
	return a.profile.BaseAgent.ToolScope
}

func (a *BaseAgent) MCPScope() string {
	return a.profile.BaseAgent.MCPScope
}

func (a *BaseAgent) RuntimeSpec() RuntimeSpec {
	spec := a.profile.RuntimeSpec
	spec.Config = append([]byte(nil), spec.Config...)
	return spec
}

func (a *BaseAgent) Extensions() map[string]json.RawMessage {
	return cloneRawMessageMap(a.profile.BaseAgent.Extensions)
}

func (a *BaseAgent) Bindings() AgentBindings {
	return a.bindings
}

func (a *BaseAgent) Runtime() SessionRuntime {
	return a.runtime
}

// BuildRuntimeRequest prepares the provider request without starting a run.
// It is used by runtime prewarming paths that must exactly match normal
// execution options while avoiding model input.
func (a *BaseAgent) BuildRuntimeRequest(input AgentInput) RuntimeRequest {
	if a == nil {
		return RuntimeRequest{}
	}
	if input.RunID == "" {
		input.RunID = AgentRunID("run-" + a.ID().String())
	}
	input.TenantID = firstNonEmpty(strings.TrimSpace(input.TenantID), strings.TrimSpace(string(a.profile.OrgIdentity.OrganizationRef.ID)), "global")
	if !input.DisableBoundMemoryContext {
		input.ContextPack = a.applyBoundMemoryToContextPack(input.ContextPack)
	}
	return a.buildRuntimeRequest(input)
}

func (a *BaseAgent) Handle(ctx context.Context, input AgentInput) (out AgentResult, returnErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if input.RunID == "" {
		input.RunID = AgentRunID("run-" + a.ID().String())
	}
	input.TenantID = firstNonEmpty(strings.TrimSpace(input.TenantID), strings.TrimSpace(string(a.profile.OrgIdentity.OrganizationRef.ID)), "global")
	var traceSession DurableTraceSession
	var traceResponse RuntimeResponse
	resultCommitted := false
	if recorder, ok := a.trace.(DurableTraceRecorder); ok {
		var err error
		traceSession, err = recorder.BeginTrace(ctx, input)
		if err != nil {
			return AgentResult{}, err
		}
		if traceSession == nil {
			return AgentResult{}, fmt.Errorf("durable trace recorder returned a nil session")
		}
		ctx = context.WithValue(ctx, durableTraceContextKey{}, traceSession)
		if err := traceSession.RecordLifecycle(ctx, TraceLifecycleEvent{Stage: "TraceAccepted", At: a.now()}); err != nil {
			traceErr := TracePersistenceError{Stage: "trace_accepted", Err: err}
			finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), durableTraceFinishTimeout)
			defer cancel()
			_ = traceSession.Finish(finishCtx, TraceTerminal{
				Status:       RuntimeStatusFailed,
				ErrorCode:    "trace_persistence_failed",
				ErrorMessage: traceErr.Error(),
				CompletedAt:  a.now(),
			})
			return AgentResult{}, traceErr
		}
		defer func() {
			out.InternalTraceRef = traceSession.Ref()
			terminal := durableTraceTerminal(out, traceResponse, returnErr, a.now())
			finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), durableTraceFinishTimeout)
			defer cancel()
			if err := traceSession.Finish(finishCtx, terminal); err != nil {
				finishErr := fmt.Errorf("finalize durable trace: %w", err)
				if returnErr == nil {
					if resultCommitted {
						returnErr = NewPostCommitPipelineError("durable_trace_finalize", finishErr)
					} else {
						returnErr = finishErr
					}
				} else {
					returnErr = errors.Join(returnErr, finishErr)
				}
			}
		}()
	}
	if err := a.requireRuntimeDependencies(ctx, input); err != nil {
		return AgentResult{}, err
	}
	if err := a.emit(ctx, SignalBeforeTaskReceived, input, nil); err != nil {
		return AgentResult{}, err
	}
	if err := a.emit(ctx, SignalAfterTaskReceived, input, nil); err != nil {
		return AgentResult{}, err
	}
	if err := a.emit(ctx, SignalBeforeTaskAccepted, input, nil); err != nil {
		return AgentResult{}, err
	}
	if err := a.emit(ctx, SignalAfterTaskAccepted, input, nil); err != nil {
		return AgentResult{}, err
	}
	if err := a.emit(ctx, SignalBeforeContextBuild, input, nil); err != nil {
		return AgentResult{}, err
	}
	if !input.DisableBoundMemoryContext {
		input.ContextPack = a.applyBoundMemoryToContextPack(input.ContextPack)
	}
	if errs := ValidateContextPack(input.ContextPack); len(errs) > 0 {
		err := fmt.Errorf("context pack invalid: %v", errs)
		_ = a.emit(ctx, SignalAgentFailed, input, map[string]string{"error": err.Error()})
		return AgentResult{}, err
	}
	if err := a.validateRuntimeIdentity(input); err != nil {
		_ = a.emit(ctx, SignalAgentFailed, input, map[string]string{"error": err.Error()})
		return AgentResult{}, err
	}
	if err := a.emit(ctx, SignalAfterContextBuild, input, map[string]string{"context_pack_id": input.ContextPack.ID.String()}); err != nil {
		return AgentResult{}, err
	}
	sessionRuntime := a.runtime
	if sessionRuntime == nil {
		err := fmt.Errorf("runtime is not configured")
		_ = a.emit(ctx, SignalAgentFailed, input, map[string]string{"error": err.Error()})
		return AgentResult{}, err
	}
	request := a.buildRuntimeRequest(input)
	if request.RuntimeOptions.ProviderSpecific[RuntimeContextOverflowProviderKey] == "true" {
		return AgentResult{}, NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "required conversation context exceeds the configured input budget; shorten the current input or increase the configured context allowance", false)
	}
	if a.diaryFlush != nil {
		request = a.diaryFlush.PrepareRuntimeRequest(ctx, request)
	}
	if traceSession != nil {
		if err := traceSession.RecordRequest(ctx, request); err != nil {
			return AgentResult{}, TracePersistenceError{Stage: "runtime_request", Err: err}
		}
	}
	if err := a.emit(ctx, SignalBeforeRuntimeStart, input, nil); err != nil {
		return AgentResult{}, err
	}
	runtimeCtx := ctx
	if executionTrace, ok := traceSession.(RuntimeExecutionTraceSession); ok {
		runtimeCtx = WithRuntimeExecutionObserver(ctx, executionTrace.RecordExecution)
	}
	if dependencyTrace, ok := traceSession.(RuntimeDependencyTraceSession); ok {
		runtimeCtx = WithRuntimeDependencyObserver(runtimeCtx, dependencyTrace.RecordDependency)
	}
	response, err := a.runSessionRuntime(runtimeCtx, sessionRuntime, request, input)
	traceResponse = response
	if err != nil {
		a.flushTerminalDiaryAfterRuntimeError(ctx, sessionRuntime, request, response, input, err)
		_ = a.emit(ctx, SignalAgentFailed, input, map[string]string{"error": err.Error()})
		return AgentResult{}, err
	}
	finishSignalErr := a.emit(ctx, SignalBeforeRuntimeFinish, input, nil)
	a.flushTerminalDiary(ctx, sessionRuntime, request, response, input)
	if finishSignalErr != nil {
		return AgentResult{}, finishSignalErr
	}
	if err := a.emit(ctx, SignalAfterRuntimeFinish, input, map[string]string{"status": string(response.Status)}); err != nil {
		return AgentResult{}, err
	}
	if response.Status != "" && response.Status != RuntimeStatusCompleted {
		return a.handleRuntimeTerminal(ctx, input, response)
	}
	if err := a.emit(ctx, SignalBeforeResultNormalize, input, nil); err != nil {
		return AgentResult{}, err
	}
	normalizerInput := input
	normalizerInput.RuntimeToolBindings = cloneRuntimeToolBindings(request.ToolBindings)
	result, err := normalizeAgentResult(ctx, a.normalizer, normalizerInput, response, a.now())
	if err != nil {
		_ = a.emit(ctx, SignalAgentFailed, input, map[string]string{"error": err.Error()})
		return AgentResult{}, err
	}
	if traceSession != nil {
		result.InternalTraceRef = traceSession.Ref()
	} else {
		traceRef, err := a.trace.RecordTrace(ctx, input, response)
		if err != nil {
			_ = a.emit(ctx, SignalAgentFailed, input, map[string]string{"error": err.Error()})
			return AgentResult{}, err
		}
		if strings.TrimSpace(traceRef) != "" {
			result.InternalTraceRef = traceRef
		}
	}
	result.RuntimeAttemptID = ""
	if attempt, ok := traceSession.(RuntimeAttemptTraceSession); ok {
		result.RuntimeAttemptID = attempt.RuntimeAttemptID()
	}
	if result.Summary.IsZero() {
		summary, err := a.summary.GenerateSummary(input, response, result)
		if err != nil {
			_ = a.emit(ctx, SignalAgentFailed, input, map[string]string{"error": err.Error()})
			return AgentResult{}, err
		}
		result.Summary = summary
	}
	if err := a.emit(ctx, SignalAfterResultNormalize, input, map[string]string{"result_id": result.ID.String()}); err != nil {
		return AgentResult{}, err
	}
	if err := a.emit(ctx, SignalBeforeResultValidate, input, nil); err != nil {
		return AgentResult{}, err
	}
	if err := a.validator.Validate(input, result); err != nil {
		_ = a.emit(ctx, SignalAgentFailed, input, map[string]string{"error": err.Error()})
		return AgentResult{}, err
	}
	if err := a.emit(ctx, SignalAfterResultValidate, input, nil); err != nil {
		return AgentResult{}, err
	}
	if err := a.emit(ctx, SignalBeforeResultCommit, input, nil); err != nil {
		return AgentResult{}, err
	}
	if a.delivery == nil {
		err := PipelineDependencyError{Dependency: "delivery", Message: "result committer is required before commit"}
		_ = a.emit(ctx, SignalAgentFailed, input, map[string]string{"error": err.Error()})
		return AgentResult{}, err
	}
	if err := a.delivery.SubmitAgentResult(ctx, result, CommitOptions{
		IdempotencyKey:       input.RunID.String(),
		TenantID:             firstNonEmpty(strings.TrimSpace(input.TenantID), strings.TrimSpace(string(a.profile.OrgIdentity.OrganizationRef.ID)), "global"),
		ContextSourceRefs:    append([]ContextSourceRef(nil), input.ContextPack.SourceRefs...),
		ApprovedContinuation: approvedContinuationCommit(input.RuntimeOptions.ProviderSpecific),
	}); err != nil {
		var committedErr PostCommitPipelineError
		if errors.As(err, &committedErr) {
			resultCommitted = true
			return result, committedErr
		}
		_ = a.emit(ctx, SignalAgentFailed, input, map[string]string{"error": err.Error()})
		return AgentResult{}, err
	}
	resultCommitted = true
	if err := a.emit(ctx, SignalAfterResultCommit, input, map[string]string{"result_id": result.ID.String()}); err != nil {
		return result, NewPostCommitPipelineError("after_result_commit", err)
	}
	if strings.TrimSpace(input.RuntimeOptions.ProviderSpecific[PipelineReplyOwnerProviderKey]) != PipelineReplyOwnerCaller {
		if err := a.emit(ctx, SignalBeforeAgentReply, input, nil); err != nil {
			return result, NewPostCommitPipelineError("before_agent_reply", err)
		}
		if err := a.notifier.Notify(ctx, input, result); err != nil {
			return result, NewPostCommitPipelineError("notify", err)
		}
		if err := a.emit(ctx, SignalAfterAgentReply, input, nil); err != nil {
			return result, NewPostCommitPipelineError("after_agent_reply", err)
		}
	}
	return result, nil
}

func (a *BaseAgent) requireRuntimeDependencies(ctx context.Context, input AgentInput) error {
	if a.trace == nil {
		err := PipelineDependencyError{Dependency: "trace", Message: "trace recorder is required before agent runtime execution"}
		_ = a.emit(ctx, SignalAgentFailed, input, map[string]string{"error": err.Error(), "dependency": err.Dependency})
		return err
	}
	if a.notifier == nil {
		err := PipelineDependencyError{Dependency: "notifier", Message: "notifier is required before agent runtime execution"}
		_ = a.emit(ctx, SignalAgentFailed, input, map[string]string{"error": err.Error(), "dependency": err.Dependency})
		return err
	}
	return nil
}

func (a *BaseAgent) runSessionRuntime(ctx context.Context, runtime SessionRuntime, request RuntimeRequest, input AgentInput) (RuntimeResponse, error) {
	started := false
	traceSession := durableTraceSessionFromContext(ctx)
	return runtime.RunSession(ctx, request, func(eventCtx context.Context, envelope SessionEnvelope) error {
		eventCtx = durableTraceEventContext(ctx, eventCtx, traceSession)
		if traceSession != nil {
			if err := traceSession.RecordRuntime(eventCtx, envelope); err != nil {
				return TracePersistenceError{Stage: "runtime_event", Err: err}
			}
		}
		if envelope.Event.Kind == EventTurnStarted && !started {
			started = true
			return a.emit(eventCtx, SignalRuntimeStarted, input, map[string]string{
				"turn_generation": strconv.FormatUint(envelope.TurnGeneration, 10),
			})
		}
		if !runtimeSessionEventIsProgress(envelope.Event.Kind) {
			return nil
		}
		return a.emit(eventCtx, SignalRuntimeProgress, input, runtimeSessionProgressPayload(envelope))
	})
}

func runtimeSessionEventIsProgress(kind SessionEventKind) bool {
	switch kind {
	case EventThoughtDelta, EventToolCall, EventToolOutputDelta, EventToolResult, EventPlan, EventUsageDelta,
		EventProvisioning, EventNotice, EventSubagentUpdate, EventSubagentDetail,
		EventConfigChanged, EventCatalogUpdated, EventBackendBound, EventBackendSuspended, EventDetached:
		return true
	default:
		return false
	}
}

func runtimeSessionProgressPayload(envelope SessionEnvelope) map[string]string {
	payload := map[string]string{
		"event_kind":      string(envelope.Event.Kind),
		"sequence":        strconv.FormatUint(envelope.Sequence, 10),
		"turn_generation": strconv.FormatUint(envelope.TurnGeneration, 10),
	}
	switch envelope.Event.Kind {
	case EventToolCall:
		payload["tool"] = envelope.Event.ToolCall.Name
		payload["call_id"] = envelope.Event.ToolCall.ToolUseID
	case EventToolResult:
		payload["call_id"] = envelope.Event.ToolResult.ToolUseID
		if envelope.Event.ToolResult.IsError {
			payload["status"] = "failed"
		} else {
			payload["status"] = "completed"
		}
	case EventNotice:
		payload["level"] = string(envelope.Event.Notice.Level)
	case EventProvisioning:
		payload["status"] = string(envelope.Event.Provisioning.Phase)
	}
	return payload
}

func (a *BaseAgent) applyBoundMemoryToContextPack(pack ContextPack) ContextPack {
	memory := a.bindings.Memory
	if !memory.PersonalDigest.IsZero() && pack.PersonalMemoryDigestRef.IsZero() {
		pack.PersonalMemoryDigestRef = memory.PersonalDigest
		pack.PersonalMemoryDigestSummary = memory.PersonalDigest.SummaryRef
	}
	for _, digest := range memory.CollectiveDigests {
		if digest.IsZero() || hasMemoryDigestRef(pack.CollectiveMemoryDigestRefs, digest.ID) {
			continue
		}
		pack.CollectiveMemoryDigestRefs = append(pack.CollectiveMemoryDigestRefs, digest)
		pack.CollectiveMemoryDigestSummaries = append(pack.CollectiveMemoryDigestSummaries, MemoryDigestSummary{
			ID:         digest.ID,
			MemoryKind: digest.MemoryKind,
			Summary:    digest.SummaryRef,
		})
	}
	pack.SourceRefs = appendMemoryDigestSourceRefs(pack.SourceRefs, pack.PersonalMemoryDigestRef, pack.CollectiveMemoryDigestRefs)
	return pack
}

func appendMemoryDigestSourceRefs(refs []ContextSourceRef, personal MemoryDigestRef, collective []MemoryDigestRef) []ContextSourceRef {
	if !personal.IsZero() && !hasSource(refs, ContextSourceKindMemoryDigest, string(personal.ID)) {
		ref := personal
		refs = append(refs, ContextSourceRef{Kind: ContextSourceKindMemoryDigest, Ref: ContextSourceTypedRef{MemoryDigestRef: &ref}, Priority: SourcePriorityMemory})
	}
	for _, digest := range collective {
		if digest.IsZero() || hasSource(refs, ContextSourceKindMemoryDigest, string(digest.ID)) {
			continue
		}
		ref := digest
		refs = append(refs, ContextSourceRef{Kind: ContextSourceKindMemoryDigest, Ref: ContextSourceTypedRef{MemoryDigestRef: &ref}, Priority: SourcePriorityMemory})
	}
	return refs
}

func hasMemoryDigestRef(refs []MemoryDigestRef, id MemoryDigestID) bool {
	for _, ref := range refs {
		if ref.ID == id && id != "" {
			return true
		}
	}
	return false
}

func (a *BaseAgent) buildRuntimeRequest(input AgentInput) RuntimeRequest {
	disableSkillResources := strings.EqualFold(strings.TrimSpace(input.RuntimeOptions.ProviderSpecific[RuntimeDisableSkillResourcesProviderKey]), "true") ||
		strings.TrimSpace(input.RuntimeOptions.ProviderSpecific[RuntimeDisableSkillResourcesProviderKey]) == "1"
	tools := make([]RuntimeToolBinding, 0, len(a.bindings.Tools))
	for _, binding := range a.bindings.Tools {
		tools = append(tools, RuntimeToolBinding{
			ToolID:             binding.ToolID,
			Alias:              binding.Alias,
			Version:            binding.Version,
			GrantedPermissions: append([]string(nil), binding.GrantedPermissions...),
			SecretEnv:          cloneStringMap(binding.SecretEnv),
			Source:             binding.Source,
			ServerToolID:       binding.ServerToolID,
			DownstreamToolName: binding.DownstreamToolName,
			Summary:            binding.Summary,
			InputSchema:        cloneAnyMap(binding.InputSchema),
			OutputSchema:       cloneAnyMap(binding.OutputSchema),
		})
	}
	if len(input.RuntimeToolBindings) > 0 {
		tools = cloneRuntimeToolBindings(input.RuntimeToolBindings)
	}
	if !disableSkillResources {
		tools = appendSkillLoaderRuntimeTools(tools)
	}
	tools = filterRuntimeToolBindingsByAllowedIDs(tools, input.RuntimeOptions.ProviderSpecific[RuntimeAllowedToolIDsProviderKey])
	skills := make([]RuntimeSkillBinding, 0, len(a.bindings.Skills))
	for _, binding := range a.bindings.Skills {
		skills = append(skills, RuntimeSkillBinding{
			SkillID:       binding.SkillID,
			Alias:         binding.Alias,
			Version:       binding.Version,
			Summary:       binding.Summary,
			PackageDigest: binding.PackageDigest,
		})
	}
	if len(input.RuntimeSkillBindings) > 0 {
		skills = cloneRuntimeSkillBindings(input.RuntimeSkillBindings)
	}
	if disableSkillResources {
		skills = nil
	}
	options := a.runtimeOptions(input)
	inputMessages := a.runtimePromptMessages(input, options)
	contextTokenLimit := EffectiveRuntimeContextTokenLimit(options.ProviderSpecific)
	promptTokenBudget := RuntimePromptTokenBudget(contextTokenLimit)
	if a.profile.RuntimeSpec.Kind == RuntimeKindManaged {
		contextTokenLimit = EffectiveManagedRuntimeContextTokenLimit(options.ProviderSpecific)
		promptTokenBudget = contextTokenLimit - RuntimeContextOutputReserveTokens(contextTokenLimit)
	}
	if !runtimeManagesNativeContext(a.profile.RuntimeSpec.Kind) {
		promptTokenBudget = RuntimePromptTokenBudgetAfterTools(promptTokenBudget, tools)
	}
	inputMessages, contextStats := LimitRuntimeChatMessages(inputMessages, promptTokenBudget)
	options.ProviderSpecific[RuntimeContextTokenLimitProviderKey] = strconv.Itoa(contextTokenLimit)
	delete(options.ProviderSpecific, RuntimeContextTruncatedProviderKey)
	delete(options.ProviderSpecific, RuntimeContextOverflowProviderKey)
	if contextStats.Truncated {
		options.ProviderSpecific[RuntimeContextTruncatedProviderKey] = "true"
	}
	if contextStats.RequiredOverflow {
		options.ProviderSpecific[RuntimeContextOverflowProviderKey] = "true"
	}
	if systemPrompt := RuntimePromptMessagesSystemPrompt(inputMessages); systemPrompt != "" {
		options.SystemPrompt = systemPrompt
	}
	tenantID := firstNonEmpty(strings.TrimSpace(input.TenantID), strings.TrimSpace(string(a.profile.OrgIdentity.OrganizationRef.ID)), "global")
	executionPrincipal := NormalizeExecutionPrincipal(input.ExecutionPrincipal, tenantID, a.identity.AgentID)
	executionEnvelope := CloneExecutionEnvelope(input.ExecutionEnvelope)
	if executionEnvelope == nil {
		profileID := firstNonEmpty(strings.TrimSpace(string(a.profile.ID)), strings.TrimSpace(a.identity.AgentID.String()))
		executionEnvelope = &ExecutionEnvelope{
			TenantID:                  tenantID,
			RunID:                     input.RunID.String(),
			MissionID:                 input.Task.MissionID,
			TaskID:                    input.Task.ID,
			AgentID:                   a.identity.AgentID.String(),
			AgentProfileID:            profileID,
			PrincipalID:               executionPrincipal.ID,
			PrincipalKind:             executionPrincipal.Kind,
			OrganizationID:            strings.TrimSpace(string(a.profile.OrgIdentity.OrganizationRef.ID)),
			PermissionScopes:          append([]string(nil), executionPrincipal.PermissionScope...),
			BoundToolPermissionScopes: personalAssistantBoundToolPermissionScopes(a.profile, input, executionPrincipal, tools),
			ResponsibilityContext:     CloneResponsibilityContext(input.ContextPack.ResponsibilityContext),
		}
		if err := executionEnvelope.SealAt(a.now()); err != nil {
			// Keep the unsigned snapshot attached so the runtime rejects this
			// request instead of silently falling back to an unbound execution.
		}
	}
	return RuntimeRequest{
		TenantID:           tenantID,
		RunID:              input.RunID,
		AgentID:            a.identity.AgentID,
		ExecutionEnvelope:  executionEnvelope,
		ExecutionPrincipal: executionPrincipal,
		MissionID:          input.Task.MissionID,
		TaskID:             input.Task.ID,
		ContextPack:        input.ContextPack,
		InputMessages:      inputMessages,
		InputMedia:         cloneAgentInputMedia(input.InputMedia),
		ToolBindings:       tools,
		SkillBindings:      skills,
		RuntimeOptions:     options,
		TracePolicy:        TracePolicy{CaptureMessages: true, CaptureRawOutput: true, RedactSecrets: true},
	}
}

func cloneAgentInputMedia(blocks []ContentBlock) []ContentBlock {
	if len(blocks) == 0 {
		return nil
	}
	out := append([]ContentBlock(nil), blocks...)
	for index := range out {
		out[index].Data = append([]byte(nil), blocks[index].Data...)
	}
	return out
}

func (a *BaseAgent) validateRuntimeIdentity(input AgentInput) error {
	contextAgentID := input.ContextPack.AgentID
	profileAgentID := AgentID(a.profile.ID)
	if !contextAgentID.IsZero() && contextAgentID != a.identity.AgentID && contextAgentID != profileAgentID {
		return NewRuntimeBridgeError(RuntimeBridgeSessionMismatch, "runtime context agent does not match the executing agent", false)
	}
	missionID := strings.TrimSpace(input.Task.MissionID)
	contextMissionID := strings.TrimSpace(string(input.ContextPack.MissionID))
	if missionID != "" && contextMissionID != "" && missionID != contextMissionID {
		return NewRuntimeBridgeError(RuntimeBridgeSessionMismatch, "runtime context mission does not match the task", false)
	}
	taskID := strings.TrimSpace(input.Task.ID)
	contextTaskID := strings.TrimSpace(string(input.ContextPack.TaskID))
	if taskID != "" && contextTaskID != "" && taskID != contextTaskID {
		return NewRuntimeBridgeError(RuntimeBridgeSessionMismatch, "runtime context task does not match the task", false)
	}
	return nil
}

func appendSkillLoaderRuntimeTools(tools []RuntimeToolBinding) []RuntimeToolBinding {
	seen := map[ToolID]bool{}
	for _, binding := range tools {
		if binding.ToolID != "" {
			seen[binding.ToolID] = true
		}
	}
	for _, binding := range skillLoaderRuntimeToolBindings() {
		if seen[binding.ToolID] {
			continue
		}
		tools = append(tools, binding)
		seen[binding.ToolID] = true
	}
	return tools
}

func skillLoaderRuntimeToolBindings() []RuntimeToolBinding {
	return []RuntimeToolBinding{
		{
			ToolID:       "list_skills",
			Summary:      "List the skills currently bound to this Agent.",
			InputSchema:  runtimeObjectSchema("list_skills input", map[string]any{"query": runtimeStringSchema("Optional case-insensitive filter.")}, nil),
			OutputSchema: runtimeSkillListOutputSchema(),
		},
		{
			ToolID:       "load_skill",
			Summary:      "Load the SKILL.md instructions for a skill currently bound to this Agent.",
			InputSchema:  runtimeObjectSchema("load_skill input", map[string]any{"skill_id": runtimeStringSchema("Bound skill id to load.")}, []string{"skill_id"}),
			OutputSchema: runtimeSkillLoadOutputSchema(),
		},
		{
			ToolID:       "list_skill_resources",
			Summary:      "List resource files available inside a skill package currently bound to this Agent.",
			InputSchema:  runtimeObjectSchema("list_skill_resources input", map[string]any{"skill_id": runtimeStringSchema("Bound skill id whose resources should be listed.")}, []string{"skill_id"}),
			OutputSchema: runtimeSkillResourceListOutputSchema(),
		},
		{
			ToolID:  "load_resource",
			Summary: "Load a text or markdown resource file from a skill package currently bound to this Agent.",
			InputSchema: runtimeObjectSchema("load_resource input", map[string]any{
				"skill_id": runtimeStringSchema("Bound skill id whose package contains the resource."),
				"path":     runtimeStringSchema("Package-relative resource path."),
			}, []string{"skill_id", "path"}),
			OutputSchema: runtimeSkillResourceLoadOutputSchema(),
		},
	}
}

func runtimeObjectSchema(title string, properties map[string]any, required []string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"title":                title,
		"additionalProperties": false,
		"properties":           properties,
	}
	if len(required) > 0 {
		values := make([]any, 0, len(required))
		for _, item := range required {
			values = append(values, item)
		}
		schema["required"] = values
	}
	return schema
}

func runtimeStringSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func runtimeIntegerSchema(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

func runtimeSkillListOutputSchema() map[string]any {
	return runtimeObjectSchema("list_skills output", map[string]any{
		"skills": map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
		"total":  runtimeIntegerSchema("Total returned skills."),
	}, []string{"skills", "total"})
}

func runtimeSkillLoadOutputSchema() map[string]any {
	return runtimeObjectSchema("load_skill output", map[string]any{
		"skill_id": runtimeStringSchema("Loaded skill id."),
		"content":  runtimeStringSchema("Raw SKILL.md content."),
		"source":   runtimeStringSchema("Skill package source reference when available."),
	}, []string{"skill_id", "content"})
}

func runtimeSkillResourceListOutputSchema() map[string]any {
	return runtimeObjectSchema("list_skill_resources output", map[string]any{
		"skill_id":  runtimeStringSchema("Skill id."),
		"resources": map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
		"total":     runtimeIntegerSchema("Total returned resources."),
	}, []string{"skill_id", "resources", "total"})
}

func runtimeSkillResourceLoadOutputSchema() map[string]any {
	return runtimeObjectSchema("load_resource output", map[string]any{
		"skill_id":     runtimeStringSchema("Skill id."),
		"path":         runtimeStringSchema("Loaded resource path."),
		"content":      runtimeStringSchema("Text content when previewable."),
		"media_type":   runtimeStringSchema("Detected media type."),
		"preview_kind": runtimeStringSchema("markdown, text, json, image, or none."),
		"size":         runtimeIntegerSchema("File size in bytes."),
	}, []string{"skill_id", "path", "preview_kind"})
}

func cloneRuntimeChatMessages(messages []RuntimeChatMessage) []RuntimeChatMessage {
	if len(messages) == 0 {
		return nil
	}
	out := make([]RuntimeChatMessage, 0, len(messages))
	for _, message := range messages {
		message.ContextParts = append([]RuntimeContextPart(nil), message.ContextParts...)
		message.ToolCalls = append([]RuntimeToolCall(nil), message.ToolCalls...)
		out = append(out, message)
	}
	return out
}

func cloneRuntimeToolBindings(bindings []RuntimeToolBinding) []RuntimeToolBinding {
	if len(bindings) == 0 {
		return nil
	}
	out := make([]RuntimeToolBinding, 0, len(bindings))
	for _, binding := range bindings {
		out = append(out, RuntimeToolBinding{
			ToolID:             binding.ToolID,
			Alias:              binding.Alias,
			Version:            binding.Version,
			GrantedPermissions: append([]string(nil), binding.GrantedPermissions...),
			SecretEnv:          cloneStringMap(binding.SecretEnv),
			Source:             binding.Source,
			ServerToolID:       binding.ServerToolID,
			DownstreamToolName: binding.DownstreamToolName,
			Summary:            binding.Summary,
			InputSchema:        cloneAnyMap(binding.InputSchema),
			OutputSchema:       cloneAnyMap(binding.OutputSchema),
		})
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneRuntimeSkillBindings(bindings []RuntimeSkillBinding) []RuntimeSkillBinding {
	if len(bindings) == 0 {
		return nil
	}
	out := make([]RuntimeSkillBinding, 0, len(bindings))
	for _, binding := range bindings {
		out = append(out, RuntimeSkillBinding{
			SkillID:       binding.SkillID,
			Alias:         binding.Alias,
			Version:       binding.Version,
			Summary:       binding.Summary,
			PackageDigest: binding.PackageDigest,
		})
	}
	return out
}

func (a *BaseAgent) runtimeOptions(input AgentInput) RuntimeOptions {
	options := input.RuntimeOptions
	options.ExtraArgs = append([]string(nil), input.RuntimeOptions.ExtraArgs...)
	options.CustomArgs = append([]string(nil), input.RuntimeOptions.CustomArgs...)
	options.Environment = cloneStringMap(input.RuntimeOptions.Environment)
	options.ProviderSpecific = cloneStringMap(input.RuntimeOptions.ProviderSpecific)
	options.PromptBlocks = cloneRuntimePromptBlocks(input.RuntimeOptions.PromptBlocks)
	if !rawJSONHasValue(options.ProviderConfig) {
		if a.profile.RuntimeSpec.HasConfig() {
			options.ProviderConfig = append([]byte(nil), a.profile.RuntimeSpec.Config...)
		} else {
			options.ProviderConfig = nil
		}
	} else {
		options.ProviderConfig = append([]byte(nil), options.ProviderConfig...)
	}
	if rawJSONHasValue(input.RuntimeOptions.MCPConfig) {
		options.MCPConfig = append([]byte(nil), input.RuntimeOptions.MCPConfig...)
	} else {
		options.MCPConfig = nil
	}
	options.SandboxProfile = input.RuntimeOptions.SandboxProfile.Clone()
	options.WorkspacePolicy.WritableRoots = append([]string(nil), input.RuntimeOptions.WorkspacePolicy.WritableRoots...)
	options.WorkspacePolicy.Profile = input.RuntimeOptions.WorkspacePolicy.Profile.Clone()
	if root := strings.TrimSpace(a.profile.WorkspaceRoot); options.WorkingDirectory == "" && filepath.IsAbs(root) {
		root = filepath.Clean(root)
		options.WorkingDirectory = root
		if options.WorkspacePolicy.Root == "" {
			options.WorkspacePolicy = RuntimeWorkspacePolicy{
				Root:            root,
				WorkDir:         root,
				WritableRoots:   []string{root},
				SandboxMode:     "workspace-write",
				ApprovalPolicy:  "on-request",
				EnforceBoundary: true,
				Profile: RuntimeSandboxProfile{
					Mode:            RuntimeSandboxWorkspaceWrite,
					Approval:        RuntimeApprovalOnRequest,
					Root:            root,
					WorkDir:         root,
					WritableRoots:   []string{root},
					EnforceBoundary: true,
				},
			}
		}
	}
	systemContextPrompt := strings.TrimSpace(options.SystemContextPrompt)
	if shouldUseEmbeddedSystemPrompt(options.ProviderSpecific) {
		options.SystemPrompt = ""
	} else {
		options.SystemPrompt = a.runtimeSystemPrompt(options.SystemPrompt, systemContextPrompt, input)
	}
	options.SystemContextPrompt = ""
	if options.ProviderSpecific == nil {
		options.ProviderSpecific = map[string]string{}
	}
	if strings.TrimSpace(a.profile.RuntimeSpec.ConfigRef) != "" && strings.TrimSpace(options.ProviderSpecific["config_ref"]) == "" {
		options.ProviderSpecific["config_ref"] = a.profile.RuntimeSpec.ConfigRef
	}
	fillLLMProfileProviderSpecificDefaults(options.ProviderSpecific, a.profile.BaseAgent.Extensions)
	fillDiaryConfigProviderSpecificDefaults(options.ProviderSpecific, a.profile.BaseAgent.Extensions)
	fillProviderSpecificDefault(options.ProviderSpecific, "llm_api_key_ref", a.profile.BaseAgent.LLMAPIKeyRef)
	fillProviderSpecificDefault(options.ProviderSpecific, "skill_scope", a.profile.BaseAgent.SkillScope)
	fillProviderSpecificDefault(options.ProviderSpecific, "tool_scope", a.profile.BaseAgent.ToolScope)
	fillProviderSpecificDefault(options.ProviderSpecific, "mcp_scope", a.profile.BaseAgent.MCPScope)
	return options
}

func shouldUseEmbeddedSystemPrompt(providerSpecific map[string]string) bool {
	if providerSpecific == nil {
		return false
	}
	value := strings.TrimSpace(providerSpecific["discussion_external_cli_structured_prompt"])
	return strings.EqualFold(value, "true") || value == "1"
}

func approvedContinuationCommit(providerSpecific map[string]string) bool {
	if providerSpecific == nil {
		return false
	}
	continuation := strings.TrimSpace(providerSpecific["approval_continuation"])
	return (strings.EqualFold(continuation, "true") || continuation == "1") &&
		strings.TrimSpace(providerSpecific["approval_decision"]) == "approved" &&
		strings.TrimSpace(providerSpecific["approval_action_id"]) == "approve_once" &&
		strings.TrimSpace(providerSpecific["hil_receipt_effect"]) == "authorize" &&
		strings.TrimSpace(providerSpecific["hil_receipt_id"]) != "" &&
		strings.TrimSpace(providerSpecific["approval_material_kind"]) != "" &&
		strings.TrimSpace(providerSpecific["approval_material_hash"]) != ""
}

func (a *BaseAgent) runtimePromptMessages(input AgentInput, options RuntimeOptions) []RuntimeChatMessage {
	messages := BuildAgentPromptMessages(AgentPromptMessagesRequest{
		Profile:             a.profile,
		FallbackCorePrompt:  input.RuntimeOptions.SystemPrompt,
		RuntimeSystemPrompt: strings.TrimSpace(input.RuntimeOptions.SystemContextPrompt),
		ContextPack:         input.ContextPack,
		Task:                input.Task,
		InputMessages:       input.InputMessages,
		RuntimeVariables:    cloneStringMap(input.RuntimeVariables),
		RuntimeContextParts: append([]RuntimeContextPart(nil), input.RuntimeContextParts...),
		Blocks:              options.PromptBlocks,
	})
	if input.MemoryContextRendered {
		return messages
	}
	return appendMemoryContextPrompt(messages, input.ContextPack)
}

func (a *BaseAgent) runtimeSystemPrompt(fallbackCorePrompt string, runtimeSystemPrompt string, input AgentInput) string {
	return BuildAgentSystemPrompt(AgentSystemPromptRequest{
		Profile:             a.profile,
		FallbackCorePrompt:  fallbackCorePrompt,
		RuntimeSystemPrompt: runtimeSystemPrompt,
	})
}

func fillLLMProfileProviderSpecificDefaults(providerSpecific map[string]string, extensions map[string]json.RawMessage) {
	if providerSpecific == nil || len(extensions) == 0 {
		return
	}
	raw := extensions[BaseAgentLLMProfileExtensionKey]
	if len(raw) == 0 {
		return
	}
	var profile BaseAgentLLMProfileSpec
	if err := json.Unmarshal(raw, &profile); err != nil {
		return
	}
	fillProviderSpecificDefault(providerSpecific, "llm_profile_id", profile.ProfileID)
	fillProviderSpecificDefault(providerSpecific, "llm_provider", profile.Provider)
	fillProviderSpecificDefault(providerSpecific, "llm_base_url", profile.BaseURL)
	fillProviderSpecificDefault(providerSpecific, "api_key_ref", profile.APIKeyRef)
	fillProviderSpecificDefault(providerSpecific, "llm_api_key_ref", profile.APIKeyRef)
	fillProviderSpecificDefault(providerSpecific, "llm_model", profile.Model)
	if profile.ContextLength > 0 {
		fillProviderSpecificDefault(providerSpecific, "llm_context_length", strconv.Itoa(profile.ContextLength))
	}
	if profile.Temperature != 0 {
		fillProviderSpecificDefault(providerSpecific, "llm_temperature", strconv.FormatFloat(profile.Temperature, 'f', -1, 64))
	}
}

func (a *BaseAgent) flushTerminalDiary(ctx context.Context, runtime SessionRuntime, request RuntimeRequest, response RuntimeResponse, input AgentInput) {
	if a == nil || a.diaryFlush == nil {
		return
	}
	capabilities := runtime.Capabilities(context.Background())
	base := ctx
	if base == nil {
		base = context.Background()
	} else if base.Err() != nil {
		base = context.WithoutCancel(base)
	}
	flushCtx, cancel := context.WithTimeout(base, a.diaryFlushTimeout)
	defer cancel()
	outcome := a.diaryFlush.FlushTerminal(flushCtx, DiaryFlushRequest{
		Runtime: runtime, Request: request, Response: response, Capabilities: capabilities,
	})
	payload := map[string]string{
		"status":           "diary_flush_" + string(outcome.Status),
		"diary_trigger":    outcome.Trigger,
		"diary_reason":     outcome.Reason,
		"diary_path":       outcome.Path,
		"context_revision": outcome.ContextRevision,
		"attempts":         strconv.Itoa(outcome.Attempts),
	}
	if outcome.Error != "" {
		payload["error"] = outcome.Error
	}
	if outcome.RecordID != "" {
		payload["memory_id"] = outcome.RecordID
	}
	_ = a.emit(base, SignalRuntimeProgress, input, payload)
}

func (a *BaseAgent) flushTerminalDiaryAfterRuntimeError(ctx context.Context, runtime SessionRuntime, request RuntimeRequest, response RuntimeResponse, input AgentInput, cause error) {
	if strings.TrimSpace(response.ProviderMetadata["session_id"]) == "" &&
		strings.TrimSpace(request.RuntimeOptions.ProviderSpecific["scheduled_task_id"]) == "" {
		return
	}
	if response.RunID.IsZero() {
		response.RunID = request.RunID
	}
	if response.Status == "" {
		var runtimeErr RuntimePipelineError
		switch {
		case errors.As(cause, &runtimeErr) && runtimeErr.Status.IsTerminal():
			response.Status = runtimeErr.Status
		case errors.Is(cause, context.DeadlineExceeded):
			response.Status = RuntimeStatusTimeout
		case errors.Is(cause, context.Canceled):
			response.Status = RuntimeStatusCancelled
		default:
			response.Status = RuntimeStatusFailed
		}
	}
	if response.Error.Message == "" && cause != nil {
		response.Error.Message = cause.Error()
	}
	a.flushTerminalDiary(ctx, runtime, request, response, input)
}

func (a *BaseAgent) handleRuntimeTerminal(ctx context.Context, input AgentInput, response RuntimeResponse) (AgentResult, error) {
	message := response.Error.Message
	if message == "" {
		message = string(response.Status)
	}
	err := RuntimePipelineError{Status: response.Status, Message: message, ProviderMetadata: cloneStringMap(response.ProviderMetadata)}
	switch response.Status {
	case RuntimeStatusBlocked:
		_ = a.emit(ctx, SignalAgentBlocked, input, map[string]string{"error": message})
	case RuntimeStatusTimeout:
		_ = a.emit(ctx, SignalAgentTimeout, input, map[string]string{"error": message})
	case RuntimeStatusCancelled:
		_ = a.emit(ctx, SignalAgentCancelled, input, map[string]string{"error": message})
	default:
		_ = a.emit(ctx, SignalAgentFailed, input, map[string]string{"error": message})
	}
	return AgentResult{}, err
}

func (a *BaseAgent) emit(ctx context.Context, name SignalName, input AgentInput, payload map[string]string) error {
	if traceSession := durableTraceSessionFromContext(ctx); traceSession != nil {
		if err := traceSession.RecordLifecycle(ctx, TraceLifecycleEvent{
			Stage:    string(name),
			Status:   strings.TrimSpace(payload["status"]),
			At:       a.now(),
			Metadata: cloneStringMap(payload),
		}); err != nil {
			return TracePersistenceError{Stage: "lifecycle_" + string(name), Err: err}
		}
	}
	if a.signals == nil {
		return nil
	}
	if authority := strings.TrimSpace(input.RuntimeOptions.ProviderSpecific[MissionLifecycleAuthorityProviderKey]); authority != "" {
		payload = cloneStringMap(payload)
		if payload == nil {
			payload = map[string]string{}
		}
		payload[MissionLifecycleAuthorityProviderKey] = authority
	}
	return a.signals.EmitSignal(ctx, Signal{
		Name:           name,
		TenantID:       firstNonEmpty(strings.TrimSpace(input.TenantID), strings.TrimSpace(string(a.profile.OrgIdentity.OrganizationRef.ID)), "global"),
		RunID:          input.RunID,
		AgentID:        a.identity.AgentID,
		AgentProfileID: a.identity.AgentProfileID,
		MissionID:      input.Task.MissionID,
		TaskID:         input.Task.ID,
		At:             a.now(),
		Payload:        payload,
	})
}

func durableTraceSessionFromContext(ctx context.Context) DurableTraceSession {
	if ctx == nil {
		return nil
	}
	session, _ := ctx.Value(durableTraceContextKey{}).(DurableTraceSession)
	return session
}

func durableTraceEventContext(outer context.Context, eventCtx context.Context, session DurableTraceSession) context.Context {
	if eventCtx == nil {
		eventCtx = outer
	}
	if eventCtx == nil {
		eventCtx = context.Background()
	}
	if session != nil && durableTraceSessionFromContext(eventCtx) == nil {
		eventCtx = context.WithValue(eventCtx, durableTraceContextKey{}, session)
	}
	return eventCtx
}

func durableTraceTerminal(result AgentResult, response RuntimeResponse, cause error, completedAt time.Time) TraceTerminal {
	status := response.Status
	if cause != nil {
		var runtimeErr RuntimePipelineError
		switch {
		case errors.As(cause, &runtimeErr) && runtimeErr.Status.IsTerminal():
			status = runtimeErr.Status
		case errors.Is(cause, context.DeadlineExceeded):
			status = RuntimeStatusTimeout
		case errors.Is(cause, context.Canceled):
			status = RuntimeStatusCancelled
		default:
			status = RuntimeStatusFailed
		}
	} else if !status.IsTerminal() {
		status = RuntimeStatusCompleted
	}
	errorCode := strings.TrimSpace(response.Error.Code)
	errorMessage := strings.TrimSpace(response.Error.Message)
	if cause != nil {
		errorMessage = cause.Error()
		if errorCode == "" {
			var traceErr TracePersistenceError
			switch {
			case errors.Is(cause, ErrTraceQueueFull), errors.Is(cause, ErrTraceRunBudgetExceeded):
				errorCode = "resource_exhaustion"
			case errors.As(cause, &traceErr):
				errorCode = "trace_persistence_failed"
			case status == RuntimeStatusTimeout:
				errorCode = "runtime_timeout"
			case status == RuntimeStatusCancelled:
				errorCode = "runtime_cancelled"
			case status == RuntimeStatusBlocked:
				errorCode = "runtime_blocked"
			default:
				errorCode = "agent_pipeline_failed"
			}
		}
	}
	hasPartialOutput := len(response.Messages) > 0 || len(response.ToolCalls) > 0 || len(response.Artifacts) > 0
	return TraceTerminal{
		Status:       status,
		ErrorCode:    errorCode,
		ErrorMessage: errorMessage,
		Retryable:    response.Error.Retryable,
		Partial:      status != RuntimeStatusCompleted && hasPartialOutput,
		Response:     response,
		Result:       result,
		CompletedAt:  completedAt,
	}
}

func (a *BaseAgent) now() time.Time {
	if a.clock != nil {
		return a.clock.Now().UTC()
	}
	return time.Now().UTC()
}

func cloneStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func cloneRawMessageMap(src map[string]json.RawMessage) map[string]json.RawMessage {
	if src == nil {
		return nil
	}
	dst := make(map[string]json.RawMessage, len(src))
	for key, value := range src {
		dst[key] = append(json.RawMessage(nil), value...)
	}
	return dst
}

func fillProviderSpecificDefault(values map[string]string, key, value string) {
	if strings.TrimSpace(value) == "" || strings.TrimSpace(values[key]) != "" {
		return
	}
	values[key] = value
}
