package agentkernel

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/providers/kernelcompat"
)

type toolApproval struct {
	ctx      context.Context
	request  runtimecore.ApprovalRequest
	answer   chan agentcore.PermissionDecision
	answered bool
}

func (r *Runner) requiresToolApproval(call preparedCall) bool {
	for _, id := range r.config.ApprovalToolIDs {
		if id == string(call.tool.binding.ToolID) {
			return true
		}
	}
	return false
}

func (r *Runner) awaitToolApproval(ctx context.Context, call preparedCall, emit kernelcompat.Emit) (runErr error) {
	wait, cancel := context.WithTimeout(ctx, r.config.ApprovalTimeout)
	defer cancel()
	if err := wait.Err(); err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"tool": string(call.tool.binding.ToolID), "tool_call_id": call.call.ID, "arguments": call.input})
	if err != nil || len(payload) > r.config.MaxResponseBytes {
		return ErrBudget
	}
	id := "agentcore-approval-" + uuid.NewString()
	pending := &toolApproval{ctx: wait, answer: make(chan agentcore.PermissionDecision, 1), request: runtimecore.ApprovalRequest{
		Source: "managed", RequestID: id, Kind: runtimecore.ApprovalKindTool, ToolName: string(call.tool.binding.ToolID), Payload: payload,
		Metadata:      map[string]string{"approval_tool_use_id": call.call.ID, "approval_native_tool_name": string(call.tool.binding.ToolID), "approval_immutable_input": "true"},
		WorkspaceRoot: r.request.RuntimeOptions.WorkspacePolicy.Root, WorkingDirectory: r.request.RuntimeOptions.WorkingDirectory,
	}}
	r.approvalMu.Lock()
	if r.pendingApproval != nil {
		r.approvalMu.Unlock()
		return ErrBusy
	}
	r.pendingApproval = pending
	r.approvalMu.Unlock()
	emitted := false
	defer func() {
		r.approvalMu.Lock()
		if r.pendingApproval == pending {
			r.pendingApproval = nil
		}
		r.approvalMu.Unlock()
		if emitted {
			if err := emit(agentcore.SessionEvent{Kind: agentcore.EventPermissionResolved, PermissionResolved: &agentcore.PermissionResolvedEvent{RequestID: id, Kind: agentcore.PermissionKindTool}}); err != nil && runErr == nil {
				runErr = err
			}
		}
	}()
	// Canonical events expose only the request identity and choices. The existing
	// server approval handler retrieves immutable material via the optional port.
	if err := emit(agentcore.SessionEvent{Kind: agentcore.EventPermission, Permission: &agentcore.PermissionEvent{
		RequestID: id, Kind: agentcore.PermissionKindTool, ToolName: pending.request.ToolName,
		Context: &agentcore.PermissionContext{Options: []agentcore.PermissionOption{{OptionID: "allow_once", Kind: "allow_once"}, {OptionID: "deny", Kind: "deny"}}},
	}}); err != nil {
		return err
	}
	emitted = true
	select {
	case decision := <-pending.answer:
		if err := wait.Err(); err != nil {
			return err
		}
		if decision == agentcore.PermissionDenied {
			return ErrToolDenied
		}
		return nil
	case <-wait.Done():
		return wait.Err()
	}
}

func (r *Runner) PendingPermissionRequests() []agentcore.PendingPermissionView {
	r.approvalMu.Lock()
	defer r.approvalMu.Unlock()
	pending := r.pendingApproval
	if pending == nil || pending.answered || pending.ctx.Err() != nil {
		return nil
	}
	return []agentcore.PendingPermissionView{{RequestID: pending.request.RequestID, ToolName: pending.request.ToolName}}
}

func (r *Runner) PendingRuntimeApprovalRequest(id string) (runtimecore.ApprovalRequest, bool) {
	r.approvalMu.Lock()
	defer r.approvalMu.Unlock()
	pending := r.pendingApproval
	if pending == nil || pending.answered || pending.ctx.Err() != nil || pending.request.RequestID != strings.TrimSpace(id) {
		return runtimecore.ApprovalRequest{}, false
	}
	request := pending.request
	request.Payload = append([]byte(nil), request.Payload...)
	request.Metadata = make(map[string]string, len(pending.request.Metadata))
	for key, value := range pending.request.Metadata {
		request.Metadata[key] = value
	}
	return request, true
}

func (r *Runner) AnswerPermission(ctx context.Context, answer agentcore.AnswerPermissionCommand) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.approvalMu.Lock()
	defer r.approvalMu.Unlock()
	pending := r.pendingApproval
	if pending == nil || pending.answered || pending.ctx.Err() != nil || pending.request.RequestID != strings.TrimSpace(answer.RequestID) {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "managed kernel permission request is not pending", false)
	}
	if len(answer.Answers) > 0 || (answer.Selected != "" && answer.Selected != "allow_once" && answer.Selected != "allow_always" && answer.Selected != "deny") {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "managed kernel permission answer cannot modify tool arguments", false)
	}
	switch answer.Decision {
	case agentcore.PermissionApproved, agentcore.PermissionAllowAlways, agentcore.PermissionDenied:
		if (answer.Selected == "deny" && answer.Decision != agentcore.PermissionDenied) || (strings.HasPrefix(answer.Selected, "allow_") && answer.Decision == agentcore.PermissionDenied) {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "managed kernel permission choices conflict", false)
		}
	default:
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "managed kernel permission decision is invalid", false)
	}
	// Even a host's remember response authorizes only this immutable invocation.
	// A later call must obtain a fresh decision; no grant is cached here.
	pending.answered = true
	pending.answer <- answer.Decision
	return nil
}

var _ kernelcompat.PermissionRunner = (*Runner)(nil)
