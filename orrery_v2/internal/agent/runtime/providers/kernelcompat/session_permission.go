package kernelcompat

import (
	"context"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func (s *Session) PendingPermissionRequests() []agentcore.PendingPermissionView {
	s.mu.Lock()
	defer s.mu.Unlock()
	permissions, ok := s.runner.(PermissionRunner)
	if !ok || !s.Capabilities().SupportedCommands.AnswerPermission || s.closed || s.active == nil || s.active.terminal || s.active.ctx.Err() != nil {
		return nil
	}
	var result []agentcore.PendingPermissionView
	for _, view := range permissions.PendingPermissionRequests() {
		if s.active.permissions[view.RequestID] {
			result = append(result, agentcore.PendingPermissionView{RequestID: view.RequestID, ToolName: view.ToolName})
		}
	}
	return result
}

func (s *Session) PendingRuntimeApprovalRequest(id string) (runtimecore.ApprovalRequest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	permissions, ok := s.runner.(PermissionRunner)
	if !ok || !s.Capabilities().SupportedCommands.AnswerPermission || s.closed || s.active == nil || s.active.terminal || s.active.ctx.Err() != nil || !s.active.permissions[id] {
		return runtimecore.ApprovalRequest{}, false
	}
	request, found := permissions.PendingRuntimeApprovalRequest(id)
	if !found || request.RequestID != id {
		return runtimecore.ApprovalRequest{}, false
	}
	request.Payload = append([]byte(nil), request.Payload...)
	metadata := make(map[string]string, len(request.Metadata))
	for key, value := range request.Metadata {
		metadata[key] = value
	}
	request.Metadata = metadata
	return request, true
}

func (s *Session) answerPermissionLocked(ctx context.Context, answer agentcore.AnswerPermissionCommand) (agentcore.CommandReceipt, error) {
	permissions, ok := s.runner.(PermissionRunner)
	if !ok || s.poisoned || s.active == nil || s.active.terminal || s.active.ctx.Err() != nil || !s.active.permissions[answer.RequestID] {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "managed kernel permission request is not pending", false)
	}
	if err := permissions.AnswerPermission(ctx, answer); err != nil {
		return agentcore.CommandReceipt{}, err
	}
	return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn}, nil
}

func (s *Session) validatePermissionEventLocked(current *turn, event agentcore.SessionEvent) error {
	permissions, supported := s.runner.(PermissionRunner)
	if !supported || !s.Capabilities().SupportedCommands.AnswerPermission || event.Validate() != nil {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "managed kernel permission protocol is unsupported", false)
	}
	valid := false
	if event.Kind == agentcore.EventPermission {
		permission := event.Permission
		material, found := permissions.PendingRuntimeApprovalRequest(permission.RequestID)
		_, used := current.permissions[permission.RequestID]
		valid = permission.Kind == agentcore.PermissionKindTool && !used && found && material.RequestID == permission.RequestID && material.ToolName == permission.ToolName && len(permission.Questions) == 0
		if valid {
			current.permissions[permission.RequestID] = true
		}
	} else {
		permission := event.PermissionResolved
		valid = permission.Kind == agentcore.PermissionKindTool && current.permissions[permission.RequestID]
		if valid {
			current.permissions[permission.RequestID] = false
		}
	}
	if !valid {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "managed kernel permission lifecycle is invalid", false)
	}
	return nil
}
