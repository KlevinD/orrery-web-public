package discussionsession

import (
	"context"
	"errors"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/actioncontract"
)

type WorkspaceRootProvider interface {
	WorkspaceRoot(ctx context.Context) (string, error)
}

type PromptBlockProvider interface {
	PromptBlocks(ctx context.Context) ([]agentcore.RuntimePromptBlock, error)
}

type BaseAgentDiscussionAgentRunnerOptions struct {
	AttachmentProcessor   *RuntimeAttachmentProcessor
	Agent                 agentcore.Agent
	AgentID               string
	RuntimeSpec           agentcore.RuntimeSpec
	SystemPrompt          string
	PromptBlocks          []agentcore.RuntimePromptBlock
	PromptBlockProvider   PromptBlockProvider
	ActionInterpreter     actioncontract.ActionSemanticInterpreter
	WorkingDirectory      string
	WorkspaceRootProvider WorkspaceRootProvider
	Model                 string
	Timeout               time.Duration
}

type BaseAgentDiscussionAgentRunner struct {
	attachmentProcessor   *RuntimeAttachmentProcessor
	agent                 agentcore.Agent
	agentID               string
	spec                  agentcore.RuntimeSpec
	systemPrompt          string
	promptBlocks          []agentcore.RuntimePromptBlock
	promptBlockProvider   PromptBlockProvider
	actionInterpreter     actioncontract.ActionSemanticInterpreter
	workingDirectory      string
	workspaceRootProvider WorkspaceRootProvider
	model                 string
	timeout               time.Duration
}

type DiscussionAgentResultNormalizer struct{}

type EmptyRuntimeDiscussionSummaryGenerator struct{}

const EmptyRuntimeDiscussionSummaryText = "runtime completed without assistant output"

func (EmptyRuntimeDiscussionSummaryGenerator) GenerateSummary(input agentcore.AgentInput, response agentcore.RuntimeResponse, result agentcore.AgentResult) (agentcore.Summary, error) {
	summary, err := agentcore.DefaultSummaryGenerator{}.GenerateSummary(input, response, result)
	if err == nil {
		return summary, nil
	}
	if !errors.Is(err, agentcore.ErrSummaryUnavailable) {
		return agentcore.Summary{}, err
	}
	return agentcore.Summary{Text: EmptyRuntimeDiscussionSummaryText}, nil
}

func cloneAgentBindings(bindings agentcore.AgentBindings) agentcore.AgentBindings {
	return agentcore.AgentBindings{
		Tools:  append([]agentcore.ToolBinding(nil), bindings.Tools...),
		Skills: append([]agentcore.SkillBinding(nil), bindings.Skills...),
		Memory: cloneMemoryBinding(bindings.Memory),
	}
}

func cloneMemoryBinding(memory agentcore.MemoryBinding) agentcore.MemoryBinding {
	return agentcore.MemoryBinding{
		PersonalDigest:    memory.PersonalDigest,
		CollectiveDigests: append([]agentcore.MemoryDigestRef(nil), memory.CollectiveDigests...),
		Warnings:          append([]string(nil), memory.Warnings...),
	}
}

func NewBaseAgentDiscussionAgentRunner(opts BaseAgentDiscussionAgentRunnerOptions) *BaseAgentDiscussionAgentRunner {
	return &BaseAgentDiscussionAgentRunner{
		attachmentProcessor:   opts.AttachmentProcessor,
		agent:                 opts.Agent,
		agentID:               strings.TrimSpace(opts.AgentID),
		spec:                  opts.RuntimeSpec,
		systemPrompt:          opts.SystemPrompt,
		promptBlocks:          append([]agentcore.RuntimePromptBlock(nil), opts.PromptBlocks...),
		promptBlockProvider:   opts.PromptBlockProvider,
		actionInterpreter:     opts.ActionInterpreter,
		workingDirectory:      opts.WorkingDirectory,
		workspaceRootProvider: opts.WorkspaceRootProvider,
		model:                 opts.Model,
		timeout:               opts.Timeout,
	}
}

func (r *BaseAgentDiscussionAgentRunner) RunDiscussion(ctx context.Context, request DiscussionAgentRequest) (DiscussionAgentResponse, error) {
	if r == nil || r.agent == nil {
		return DiscussionAgentResponse{}, ErrRuntimeUnavailable
	}
	base := r.agent.Base()
	if base == nil {
		return DiscussionAgentResponse{}, ErrRuntimeUnavailable
	}
	request.BoundTools = foldMissionPreparationTools(discussionAgentBoundToolsForRuntime(request, r.DiscussionAgentBoundTools()))
	request.BoundSkills = firstDiscussionAgentBoundSkills(request.BoundSkills, r.DiscussionAgentBoundSkills())
	input, err := r.agentInput(ctx, request)
	if err != nil {
		return DiscussionAgentResponse{}, NewRuntimeSetupErrorFromCause(string(r.spec.Kind), err)
	}
	ctx = contextWithRuntimeApproval(ctx, r.runtimeApprovalContext(request))
	result, err := base.Handle(ctx, input)
	if err != nil {
		if response, ok := blockedRuntimeResponseFromAgentError(r.agent, agentcore.AgentRunID(request.RunID), err); ok {
			out := discussionResponseWithBoundTools(response, r.spec.Kind, request.BoundTools)
			out.OutboundPrompt = runtimeOutboundPromptFromAgent(r.agent, agentcore.AgentRunID(request.RunID), r.spec.Kind)
			out.TraceRef = result.InternalTraceRef
			return out, nil
		}
		response, hasResponse := runtimeResponseFromAgent(r.agent, agentcore.AgentRunID(request.RunID))
		if hasResponse {
			out := discussionResponseWithBoundTools(response, r.spec.Kind, request.BoundTools)
			out.OutboundPrompt = runtimeOutboundPromptFromAgent(r.agent, agentcore.AgentRunID(request.RunID), r.spec.Kind)
			out.TraceRef = result.InternalTraceRef
			return out, r.runtimeHandleError(agentcore.AgentRunID(request.RunID), err)
		}
		return DiscussionAgentResponse{TraceRef: result.InternalTraceRef}, r.runtimeHandleError(agentcore.AgentRunID(request.RunID), err)
	}
	if response, ok := runtimeResponseFromAgent(r.agent, agentcore.AgentRunID(request.RunID)); ok {
		response = runtimeResponseWithResultSummary(response, result)
		if runtimeCompletedWithoutDiscussionOutput(response) {
			return DiscussionAgentResponse{}, NewRuntimeExecutionError(string(r.spec.Kind), runtimeEmptyOutputCause(response))
		}
		out := discussionResponseWithBoundTools(response, r.spec.Kind, request.BoundTools)
		out.OutboundPrompt = runtimeOutboundPromptFromAgent(r.agent, agentcore.AgentRunID(request.RunID), r.spec.Kind)
		out.TraceRef = result.InternalTraceRef
		return out, nil
	}
	response := runtimeResponseFromAgentResult(request, result)
	if runtimeCompletedWithoutDiscussionOutput(response) {
		return DiscussionAgentResponse{}, NewRuntimeExecutionError(string(r.spec.Kind), runtimeEmptyOutputCause(response))
	}
	out := discussionResponseWithBoundTools(response, r.spec.Kind, request.BoundTools)
	out.OutboundPrompt = runtimeOutboundPromptFromAgent(r.agent, agentcore.AgentRunID(request.RunID), r.spec.Kind)
	out.TraceRef = result.InternalTraceRef
	return out, nil
}

func (r *BaseAgentDiscussionAgentRunner) RunDiscussionStream(ctx context.Context, request DiscussionAgentRequest) (<-chan DiscussionAgentStreamEvent, error) {
	if r == nil || r.agent == nil {
		return nil, ErrRuntimeUnavailable
	}
	if r.agent.Base() == nil {
		return nil, ErrRuntimeUnavailable
	}
	request.BoundTools = foldMissionPreparationTools(discussionAgentBoundToolsForRuntime(request, r.DiscussionAgentBoundTools()))
	request.BoundSkills = firstDiscussionAgentBoundSkills(request.BoundSkills, r.DiscussionAgentBoundSkills())
	progress, unsubscribe := runtimeProgressFromAgent(r.agent, agentcore.AgentRunID(request.RunID))
	out := make(chan DiscussionAgentStreamEvent, 16)
	go r.forwardBaseAgentStream(ctx, out, request, progress, unsubscribe)
	return out, nil
}

func (r *BaseAgentDiscussionAgentRunner) runtimeApprovalContext(request DiscussionAgentRequest) runtimeApprovalContext {
	return runtimeApprovalContext{
		ConversationID:   request.ConversationID,
		RunID:            request.RunID,
		AgentID:          firstNonBlank(request.AgentID, r.agentID),
		LogicalAgentID:   request.LogicalAgentID,
		ConversationKind: request.ConversationKind,
		RuntimeKind:      string(r.spec.Kind),
		AssignedTo:       strings.TrimSpace(request.ProviderSpecific["approval_assigned_to"]),
	}
}

func (r *BaseAgentDiscussionAgentRunner) PromptDebug(request DiscussionAgentRequest) *DiscussionAgentPromptDebug {
	if r == nil {
		return nil
	}
	request.AgentID = firstNonBlank(request.AgentID, r.agentID)
	request.PersonaPrompt = firstNonBlank(request.PersonaPrompt, r.systemPrompt, "你是 Orrery Agent")
	request.BoundTools = foldMissionPreparationTools(discussionAgentBoundToolsForRuntime(request, r.DiscussionAgentBoundTools()))
	request.BoundSkills = firstDiscussionAgentBoundSkills(request.BoundSkills, r.DiscussionAgentBoundSkills())
	return &DiscussionAgentPromptDebug{
		Selected: RunDebugSelectedDTO{AgentID: r.agentID, RuntimeKind: string(r.spec.Kind), Model: r.model, ConfigRef: r.spec.ConfigRef},
		Redacted: false,
	}
}

func (r *BaseAgentDiscussionAgentRunner) DiscussionAgentID() string {
	if r == nil {
		return ""
	}
	return r.agentID
}

func (r *BaseAgentDiscussionAgentRunner) DiscussionAgentBoundTools() []DiscussionAgentBoundTool {
	if r == nil {
		return nil
	}
	return discussionAgentBoundToolsFromBindings(agentBindings(r.agent))
}

func (r *BaseAgentDiscussionAgentRunner) DiscussionAgentBoundSkills() []DiscussionAgentBoundSkill {
	if r == nil {
		return nil
	}
	return discussionAgentBoundSkillsFromBindings(agentBindings(r.agent))
}

func (r *BaseAgentDiscussionAgentRunner) runtimeHandleError(runID agentcore.AgentRunID, err error) error {
	if err == nil {
		return nil
	}
	var runtimeErr agentcore.RuntimePipelineError
	if errors.As(err, &runtimeErr) {
		return NewRuntimeExecutionError(string(r.spec.Kind), err.Error())
	}
	if _, ok := runtimeResponseFromAgent(r.agent, runID); ok {
		return NewRuntimeExecutionError(string(r.spec.Kind), err.Error())
	}
	if progress := runtimeProgressHistoryFromAgent(r.agent, runID); len(progress) > 0 {
		return NewRuntimeExecutionError(string(r.spec.Kind), err.Error())
	}
	return NewRuntimeSetupErrorFromCause(string(r.spec.Kind), err)
}

func blockedRuntimeResponseFromAgentError(agent agentcore.Agent, runID agentcore.AgentRunID, err error) (agentcore.RuntimeResponse, bool) {
	var runtimeErr agentcore.RuntimePipelineError
	if !errors.As(err, &runtimeErr) || runtimeErr.Status != agentcore.RuntimeStatusBlocked {
		return agentcore.RuntimeResponse{}, false
	}
	response, ok := runtimeResponseFromAgent(agent, runID)
	if !ok || response.Status != agentcore.RuntimeStatusBlocked {
		return agentcore.RuntimeResponse{}, false
	}
	return response, true
}

func (r *BaseAgentDiscussionAgentRunner) forwardBaseAgentStream(ctx context.Context, out chan<- DiscussionAgentStreamEvent, request DiscussionAgentRequest, progress <-chan agentcore.RuntimeMessage, unsubscribe func()) {
	defer close(out)
	approvalEvents := make(chan EventDTO, 16)
	approvalContext := r.runtimeApprovalContext(request)
	approvalContext.notify = func(record EventRecord) {
		approval := mapEvent(record)
		select {
		case <-ctx.Done():
		case approvalEvents <- approval:
		}
	}
	ctx = contextWithRuntimeApproval(ctx, approvalContext)
	if unsubscribe == nil {
		unsubscribe = func() {}
	}
	defer unsubscribe()
	send := func(event DiscussionAgentStreamEvent) bool {
		select {
		case <-ctx.Done():
			return false
		case out <- event:
			return true
		}
	}
	resultCh := make(chan struct {
		result agentcore.AgentResult
		err    error
	}, 1)
	go func() {
		input, inputErr := r.agentInput(ctx, request)
		if inputErr != nil {
			resultCh <- struct {
				result agentcore.AgentResult
				err    error
			}{err: inputErr}
			return
		}
		result, err := r.agent.Base().Handle(ctx, input)
		resultCh <- struct {
			result agentcore.AgentResult
			err    error
		}{result: result, err: err}
	}()
	streamedText := false
	emittedProgress := 0
	emitProgressMessage := func(message agentcore.RuntimeMessage) bool {
		for _, event := range runtimeMessageStreamEvents(message) {
			if event.Kind == DiscussionAgentStreamTokenDelta {
				streamedText = true
			}
			if !send(event) {
				return false
			}
		}
		return true
	}
	drainProgress := func() bool {
		for progress != nil {
			select {
			case message, ok := <-progress:
				if !ok {
					progress = nil
					continue
				}
				emittedProgress++
				if !emitProgressMessage(message) {
					return false
				}
				continue
			default:
			}
			break
		}
		return true
	}
	for progress != nil || resultCh != nil {
		if !drainProgress() {
			return
		}
		select {
		case approval := <-approvalEvents:
			approvalCopy := approval
			if !send(DiscussionAgentStreamEvent{Kind: DiscussionAgentStreamApprovalRequested, Approval: &approvalCopy}) {
				return
			}
		case message, ok := <-progress:
			if !ok {
				progress = nil
				continue
			}
			emittedProgress++
			if !emitProgressMessage(message) {
				return
			}
		case result := <-resultCh:
			resultCh = nil
			if result.err != nil {
				if !drainProgress() {
					return
				}
				history := runtimeProgressHistoryFromAgent(r.agent, agentcore.AgentRunID(request.RunID))
				for _, message := range history[emittedProgress:] {
					if !emitProgressMessage(message) {
						return
					}
				}
				if response, ok := blockedRuntimeResponseFromAgentError(r.agent, agentcore.AgentRunID(request.RunID), result.err); ok {
					discussionResponse := discussionResponseWithBoundTools(response, r.spec.Kind, request.BoundTools)
					discussionResponse.OutboundPrompt = runtimeOutboundPromptFromAgent(r.agent, agentcore.AgentRunID(request.RunID), r.spec.Kind)
					discussionResponse.TraceRef = result.result.InternalTraceRef
					if !streamedText && strings.TrimSpace(discussionResponse.AssistantMessage) != "" {
						if !send(DiscussionAgentStreamEvent{Kind: DiscussionAgentStreamTokenDelta, Delta: strings.TrimSpace(discussionResponse.AssistantMessage)}) {
							return
						}
					}
					_ = send(DiscussionAgentStreamEvent{Kind: DiscussionAgentStreamRunCompleted, Response: discussionResponse})
					return
				}
				event := DiscussionAgentStreamEvent{Kind: DiscussionAgentStreamRuntimeError, Error: r.runtimeHandleError(agentcore.AgentRunID(request.RunID), result.err), Response: DiscussionAgentResponse{TraceRef: result.result.InternalTraceRef}}
				if response, ok := runtimeResponseFromAgent(r.agent, agentcore.AgentRunID(request.RunID)); ok {
					event.Response = discussionResponseWithBoundTools(response, r.spec.Kind, request.BoundTools)
					event.Response.OutboundPrompt = runtimeOutboundPromptFromAgent(r.agent, agentcore.AgentRunID(request.RunID), r.spec.Kind)
					event.Response.TraceRef = result.result.InternalTraceRef
				}
				_ = send(event)
				return
			}
			if !drainProgress() {
				return
			}
			history := runtimeProgressHistoryFromAgent(r.agent, agentcore.AgentRunID(request.RunID))
			for _, message := range history[emittedProgress:] {
				if !emitProgressMessage(message) {
					return
				}
			}
			response, ok := runtimeResponseFromAgent(r.agent, agentcore.AgentRunID(request.RunID))
			if !ok {
				response = runtimeResponseFromAgentResult(request, result.result)
			} else {
				response = runtimeResponseWithResultSummary(response, result.result)
			}
			if runtimeCompletedWithoutDiscussionOutput(response) {
				_ = send(DiscussionAgentStreamEvent{Kind: DiscussionAgentStreamRuntimeError, Error: NewRuntimeExecutionError(string(r.spec.Kind), runtimeEmptyOutputCause(response))})
				return
			}
			discussionResponse := discussionResponseWithBoundTools(response, r.spec.Kind, request.BoundTools)
			discussionResponse.OutboundPrompt = runtimeOutboundPromptFromAgent(r.agent, agentcore.AgentRunID(request.RunID), r.spec.Kind)
			discussionResponse.TraceRef = result.result.InternalTraceRef
			if !streamedText && strings.TrimSpace(discussionResponse.AssistantMessage) != "" {
				if !send(DiscussionAgentStreamEvent{Kind: DiscussionAgentStreamTokenDelta, Delta: strings.TrimSpace(discussionResponse.AssistantMessage)}) {
					return
				}
			}
			_ = send(DiscussionAgentStreamEvent{Kind: DiscussionAgentStreamRunCompleted, Response: discussionResponse})
			return
		case <-ctx.Done():
			return
		}
	}
}
