package pi

import (
	"context"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

type nativePiApprovalBroker struct {
	session *NativeSession
}

func (s *NativeSession) PendingRuntimeApprovalRequest(requestID string) (runtimecore.ApprovalRequest, bool) {
	if s == nil {
		return runtimecore.ApprovalRequest{}, false
	}
	s.mu.Lock()
	pending, ok := s.approvals[strings.TrimSpace(requestID)]
	s.mu.Unlock()
	if !ok {
		return runtimecore.ApprovalRequest{}, false
	}
	request := pending.request
	request.Payload = append([]byte(nil), request.Payload...)
	request.Metadata = cloneStringMap(request.Metadata)
	return request, true
}

func (b nativePiApprovalBroker) RequestApproval(ctx context.Context, request runtimecore.ApprovalRequest, responder runtimecore.ApprovalResponder) error {
	if b.session == nil || responder == nil {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "pi approval bridge is unavailable", false)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestID := safePiIdentifier(firstNonEmpty(request.RequestID, request.ToolName), "pi-approval")
	request.RequestID = requestID
	request.ToolName = safePiIdentifier(firstNonEmpty(request.ToolName, "tool"), "tool")
	b.session.mu.Lock()
	if b.session.dead.Load() {
		b.session.mu.Unlock()
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "pi approval bridge is detached", false)
	}
	if _, exists := b.session.approvals[requestID]; exists {
		b.session.mu.Unlock()
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "duplicate pi approval request", false)
	}
	if _, exists := b.session.ui[requestID]; exists {
		b.session.mu.Unlock()
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "duplicate pi approval request", false)
	}
	b.session.approvals[requestID] = piNativeApproval{request: request, responder: responder, done: make(chan struct{})}
	pending := b.session.approvals[requestID]
	generation := b.session.generation
	b.session.mu.Unlock()
	b.session.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventPermission, Permission: &agentcore.PermissionEvent{
		RequestID: requestID, Kind: agentcore.PermissionKindTool, ToolName: request.ToolName,
		Context: &agentcore.PermissionContext{Options: []agentcore.PermissionOption{
			{OptionID: "allow_once", Kind: "allow_once"},
			{OptionID: "allow_always", Kind: "allow_always"},
			{OptionID: "deny", Kind: "deny"},
		}},
	}})
	go func() {
		select {
		case <-pending.done:
		case <-ctx.Done():
			b.session.mu.Lock()
			pending, exists := b.session.approvals[requestID]
			if exists {
				delete(b.session.approvals, requestID)
			}
			generation := b.session.generation
			b.session.mu.Unlock()
			if exists {
				close(pending.done)
				_ = pending.responder(runtimecore.ApprovalResponse{Decision: runtimecore.ApprovalDecisionDenied, Reason: "approval context ended"})
				b.session.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventPermissionResolved, PermissionResolved: &agentcore.PermissionResolvedEvent{RequestID: requestID, Kind: agentcore.PermissionKindTool}})
			}
		case <-b.session.ctx.Done():
		}
	}()
	return nil
}

func (nativePiApprovalBroker) RuntimeApprovalHoldTimeout() time.Duration { return 0 }

var _ runtimecore.ApprovalBroker = nativePiApprovalBroker{}
