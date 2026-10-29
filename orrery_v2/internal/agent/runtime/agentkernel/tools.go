package agentkernel

import (
	"context"
	"encoding/json"
	"errors"
	"orrery_v2/internal/agent/llm"
	"strings"
	"sync/atomic"

	jsonschema "github.com/google/jsonschema-go/jsonschema"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/providers/kernelcompat"
	"orrery_v2/internal/agent/runtime/providers/toolbridge"
)

type boundTool struct {
	binding   agentcore.RuntimeToolBinding
	validator *jsonschema.Resolved
}

func prepareTools(bindings []agentcore.RuntimeToolBinding) (map[string]boundTool, []*llm.Tool, error) {
	tools := make(map[string]boundTool, len(bindings))
	var infos []*llm.Tool
	for _, binding := range bindings {
		name := toolbridge.RuntimeToolName(binding)
		if name == "" || binding.ToolID == "" {
			return nil, nil, ErrConfiguration
		}
		if _, exists := tools[name]; exists {
			return nil, nil, ErrConfiguration
		}
		parameters := binding.InputSchema
		if len(parameters) == 0 {
			parameters = map[string]any{"type": "object", "additionalProperties": false}
		}
		raw, err := json.Marshal(parameters)
		if err != nil {
			return nil, nil, ErrConfiguration
		}
		var validation jsonschema.Schema
		if json.Unmarshal(raw, &validation) != nil {
			return nil, nil, ErrConfiguration
		}
		// A nil Loader rejects external schema references without making network
		// requests. Schemas are supplied by the host, never by the llm.
		resolved, err := validation.Resolve(nil)
		if err != nil {
			return nil, nil, ErrConfiguration
		}
		tools[name] = boundTool{binding: binding, validator: resolved}
		infos = append(infos, &llm.Tool{Name: name, Description: binding.Summary, Parameters: parameters})
	}
	return tools, infos, nil
}

type preparedCall struct {
	call            llm.ToolCall
	tool            boundTool
	input           map[string]any
	approvalGranted bool
}

// Validate the entire model batch before producing any tool side effect.
func (r *Runner) prepareCalls(calls []llm.ToolCall, seen map[string]bool) ([]preparedCall, error) {
	batch := map[string]bool{}
	prepared := make([]preparedCall, 0, len(calls))
	// Identity and binding failures are terminal even if an earlier member has
	// repairable arguments. Never hide a denied operation behind schema feedback.
	for _, call := range calls {
		_, exists := r.tools[call.Function.Name]
		if !exists {
			return nil, kernelcompat.NewExecutionError("tool_unbound", ErrToolDenied)
		}
		if strings.TrimSpace(call.ID) == "" || seen[call.ID] || batch[call.ID] || (call.Type != "" && call.Type != "function") {
			return nil, kernelcompat.NewExecutionError("tool_call_identity_invalid", ErrProtocol)
		}
		batch[call.ID] = true
	}
	for _, call := range calls {
		tool := r.tools[call.Function.Name]
		var input map[string]any
		if json.Unmarshal([]byte(call.Function.Arguments), &input) != nil || input == nil {
			return nil, newToolValidationFailure(call.ID, "tool_arguments_invalid")
		}
		if err := tool.validator.Validate(input); err != nil {
			failure := newToolValidationFailure(call.ID, "tool_schema_invalid").(*toolValidationFailure)
			failure.validationReason = err.Error()
			return nil, failure
		}
		prepared = append(prepared, preparedCall{call: call, tool: tool, input: input})
	}
	return prepared, nil
}

func (r *Runner) callRequest(call preparedCall) (runtimecore.RuntimeToolCallRequest, error) {
	req := r.request
	request := runtimecore.RuntimeToolCallRequest{
		TenantID: req.TenantID, RunID: req.RunID, AgentID: req.AgentID,
		ExecutionPrincipal: req.ExecutionPrincipal, Name: string(call.tool.binding.ToolID), CallID: call.call.ID,
		Input: call.input, ToolBindings: req.ToolBindings, SkillBindings: req.SkillBindings,
		ResponsibilityContext: agentcore.CloneResponsibilityContext(req.ContextPack.ResponsibilityContext),
		ExecutionEnvelope:     agentcore.CloneExecutionEnvelope(req.ExecutionEnvelope),
		ProviderSpecific:      req.RuntimeOptions.ProviderSpecific, WorkspaceRoot: req.RuntimeOptions.WorkspacePolicy.Root,
		WorkingDirectory: req.RuntimeOptions.WorkingDirectory, WorkspacePolicy: req.RuntimeOptions.WorkspacePolicy,
	}
	// Policy and dispatcher callbacks receive independent copies. Neither can
	// mutate the session's authority or the arguments already validated above.
	raw, err := json.Marshal(request)
	if err != nil {
		return runtimecore.RuntimeToolCallRequest{}, ErrConfiguration
	}
	var cloned runtimecore.RuntimeToolCallRequest
	if json.Unmarshal(raw, &cloned) != nil {
		return runtimecore.RuntimeToolCallRequest{}, ErrConfiguration
	}
	return cloned, nil
}

func (r *Runner) executeTool(ctx context.Context, call preparedCall, emit kernelcompat.Emit) (message *llm.Message, runErr error) {
	return r.executeClaimedTool(ctx, call, emit, nil)
}

func (r *Runner) authorizeTool(ctx context.Context, call preparedCall) error {
	if !explicitToolScopeAllows(r.request.ContextPack.ResponsibilityContext, call.tool.binding) {
		return ErrToolDenied
	}
	request, err := r.callRequest(call)
	if err != nil {
		return err
	}
	if _, err := invokeCallback(ctx, r.callbacks, func() (struct{}, error) {
		return struct{}{}, r.config.AuthorizeTool(ctx, request)
	}, nil); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, ErrCapacity) {
			return ErrCapacity
		}
		return ErrToolDenied
	}
	return ctx.Err()
}

func (r *Runner) executeClaimedTool(ctx context.Context, call preparedCall, emit kernelcompat.Emit, reserved *ToolExecution) (message *llm.Message, runErr error) {
	toolCtx, cancel := context.WithTimeout(ctx, r.config.ToolTimeout)
	defer cancel()
	intent, err := r.toolExecution(call)
	if err != nil {
		return nil, err
	}
	claim, claimed := ToolExecution{}, false
	if reserved != nil {
		if !sameExecution(*reserved, intent) || reserved.State != ToolExecutionStarted || reserved.ClaimID == "" {
			return nil, ErrJournal
		}
		claim, claimed = *reserved, true
	}
	finished, dispatched := false, false
	finish := func(state ToolExecutionState, reason string) error {
		finished = true
		// Cancellation must not erase the only durable record of a possible write.
		// Persist its outcome with a separate bounded cleanup context.
		finishCtx, stop := context.WithTimeout(context.WithoutCancel(toolCtx), r.config.JournalTimeout)
		defer stop()
		if r.config.ToolJournal.Finish(finishCtx, claim, state, reason) != nil {
			return ErrJournal
		}
		return nil
	}
	defer func() {
		if finished || !claimed {
			return
		}
		state, reason := ToolExecutionUnknown, "interrupted"
		if !dispatched {
			state, reason = ToolExecutionNotDispatched, "blocked_before_dispatch"
			if toolCtx.Err() != nil {
				reason = "cancelled_before_dispatch"
			}
		}
		if err := finish(state, reason); err != nil {
			message, runErr = nil, errors.Join(runErr, err)
		}
	}()
	// Batch admission is not a cached authorization decision. Recheck current
	// grants immediately before every actual dispatch, including reserved calls.
	if err := r.authorizeTool(toolCtx, call); err != nil {
		return nil, err
	}
	if r.requiresToolApproval(call) && !call.approvalGranted {
		cancel()
		if err := r.awaitToolApproval(ctx, call, emit); err != nil {
			return nil, err
		}
		// Human waiting does not consume the execution timeout. The enclosing
		// turn/task deadline still bounds it, and approval never caches authority.
		approvedCtx, stopExecution := context.WithTimeout(ctx, r.config.ToolTimeout)
		defer stopExecution()
		toolCtx = approvedCtx
		if err := r.authorizeTool(toolCtx, call); err != nil {
			return nil, err
		}
	}
	request, err := r.callRequest(call)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(call.input)
	event := agentcore.SessionEvent{Kind: agentcore.EventToolCall, ToolCall: &agentcore.ToolCallEvent{ToolUseID: call.call.ID, Name: call.call.Function.Name, Input: raw}}
	// Canonical events are a display projection, not execution arguments. Like
	// other providers, omit input that cannot cross that boundary (for example
	// an authorized absolute cwd or credential-bearing fields). The independent
	// validated request and private checkpoint retain the exact execution input.
	if event.Validate() != nil {
		event.ToolCall.Input = nil
	}
	if err := emit(event); err != nil {
		return nil, err
	}
	if !claimed {
		journalCtx, stopJournal := context.WithTimeout(toolCtx, r.config.JournalTimeout)
		claim, err = r.config.ToolJournal.Begin(journalCtx, intent)
		stopJournal()
		if err != nil {
			return nil, err
		}
		claimed = true
	}
	if err := toolCtx.Err(); err != nil {
		return nil, err
	}
	dispatched = true
	var toolInvoked atomic.Bool
	result, err := invokeCallback(toolCtx, r.callbacks, func() (runtimecore.RuntimeToolCallResult, error) {
		toolInvoked.Store(true)
		r.observeDependency(toolCtx, "tool_dispatch", call.input, nil, &call.tool.binding, call.call.ID)
		return r.config.Tools.CallRuntimeTool(toolCtx, request)
	}, nil)
	// A dispatcher can itself return ErrCapacity after applying a write. Only
	// executor rejection before callback entry proves that nothing was dispatched.
	if errors.Is(err, ErrCapacity) && !toolInvoked.Load() {
		if err := finish(ToolExecutionNotDispatched, "capacity_before_dispatch"); err != nil {
			return nil, err
		}
		return nil, ErrCapacity
	}
	// A transport error may follow an already-applied write. Stop the loop and
	// require confirmation instead of automatically replaying the operation.
	if err != nil || toolCtx.Err() != nil || result.OutcomeUncertain {
		reason := "transport_error"
		if result.OutcomeUncertain {
			reason = "outcome_unrecorded"
		} else if errors.Is(err, errCallbackPanic) {
			reason = "interrupted"
		} else if errors.Is(toolCtx.Err(), context.DeadlineExceeded) {
			reason = "timeout"
		} else if toolCtx.Err() != nil {
			reason = "cancelled"
		}
		if err := finish(ToolExecutionUnknown, reason); err != nil {
			return nil, err
		}
		if emitErr := emit(agentcore.SessionEvent{Kind: agentcore.EventToolResult, ToolResult: &agentcore.ToolResultEvent{
			ToolUseID: call.call.ID, IsError: true, Content: []agentcore.ToolResultContent{{Kind: agentcore.ToolResultText, Text: ErrTool.Error()}},
		}}); emitErr != nil {
			return nil, emitErr
		}
		if toolCtx.Err() != nil {
			return nil, toolCtx.Err()
		}
		return nil, ErrTool
	}
	raw, err = json.Marshal(result.Output)
	if err != nil {
		if err := finish(ToolExecutionUnknown, "invalid_result"); err != nil {
			return nil, err
		}
		return nil, ErrTool
	}
	if len(raw) > r.config.MaxResponseBytes {
		if err := finish(ToolExecutionUnknown, "result_budget"); err != nil {
			return nil, err
		}
		return nil, ErrBudget
	}
	isError := result.Output["status"] == "failed" || result.Output["status"] == "error"
	state, reason := ToolExecutionCompleted, ""
	if isError {
		state, reason = ToolExecutionFailed, "reported_failure"
	}
	if err := finish(state, reason); err != nil {
		return nil, err
	}
	if err := toolCtx.Err(); err != nil {
		return nil, err
	}
	if err := emit(agentcore.SessionEvent{Kind: agentcore.EventToolResult, ToolResult: &agentcore.ToolResultEvent{
		ToolUseID: call.call.ID, IsError: isError, Content: []agentcore.ToolResultContent{{Kind: agentcore.ToolResultText, Text: string(raw)}},
	}}); err != nil {
		return nil, err
	}
	return llm.ToolMessage(string(raw), call.call.ID), nil
}
