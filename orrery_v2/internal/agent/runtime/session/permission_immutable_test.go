package session

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func TestImmutablePermissionCannotApproveModifiedArguments(t *testing.T) {
	for _, response := range []runtimecore.ApprovalResponse{
		{Decision: runtimecore.ApprovalDecisionApproved, Content: `{"content":"edited"}`},
		{Decision: runtimecore.ApprovalDecisionApproved, Metadata: map[string]any{"modified_input": `{"content":"edited"}`}},
		{Decision: runtimecore.ApprovalDecisionApproved},
	} {
		request := runtimecore.ApprovalRequest{Source: "managed", RequestID: "immutable", ToolName: "knowledge_add", Kind: runtimecore.ApprovalKindTool, Metadata: map[string]string{"approval_immutable_input": "true"}}
		backend := &approvalContextBackend{scriptedSessionBackend: newScriptedSessionBackend(), request: request}
		ctx := sessionPermissionContext(context.Background(), backend, request.RequestID)
		called := false
		handler := ApprovalPermissionHandler{Broker: runtimecore.ApprovalBrokerFunc(func(_ context.Context, material runtimecore.ApprovalRequest, respond runtimecore.ApprovalResponder) error {
			material.Metadata["approval_immutable_input"] = "false"
			return respond(response)
		})}
		err := handler.RequestSessionPermission(ctx, agentcore.SessionPermissionRequest{RuntimeKind: agentcore.RuntimeKindManaged, Envelope: agentcore.SessionEnvelope{Event: agentcore.SessionEvent{Kind: agentcore.EventPermission, Permission: &agentcore.PermissionEvent{RequestID: request.RequestID, Kind: agentcore.PermissionKindTool, ToolName: request.ToolName}}}}, func(agentcore.AnswerPermissionCommand) error { called = true; return nil })
		modified := response.Content != nil || response.Metadata["modified_input"] != nil
		if modified && (!agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeInvalidPayload) || called) {
			t.Fatalf("modified approval was delivered: %v", err)
		}
		if !modified && (err != nil || !called) {
			t.Fatalf("unchanged approval was rejected: %v", err)
		}
	}
}
