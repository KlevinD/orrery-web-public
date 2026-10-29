package pi

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func TestNativePiExposesPendingApprovalRequestToCanonicalRuntime(t *testing.T) {
	_, session := openPiNativeTestSession(t, true, agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "pi-native-approval-context"})
	drainPiHandshake(t, session.Events())
	request := runtimecore.ApprovalRequest{
		Source: "pi", RequestID: "approval-context", Kind: runtimecore.ApprovalKindTool,
		ToolName: "mcp_marketing_marketing_wechat_draft_create",
		Payload:  json.RawMessage(`{"input":{"title":"Reviewed article"}}`),
		Metadata: map[string]string{"approval_force_manual": "true"}, WorkspaceRoot: "/srv/private/workspace",
	}
	if err := (nativePiApprovalBroker{session: session}).RequestApproval(context.Background(), request, func(runtimecore.ApprovalResponse) error { return nil }); err != nil {
		t.Fatal(err)
	}
	preserved, ok := session.PendingRuntimeApprovalRequest(request.RequestID)
	if !ok || preserved.WorkspaceRoot != request.WorkspaceRoot || string(preserved.Payload) != string(request.Payload) || preserved.Metadata["approval_force_manual"] != "true" {
		t.Fatalf("pending approval request = %#v ok=%v", preserved, ok)
	}
	preserved.Payload[0] = '['
	preserved.Metadata["approval_force_manual"] = "false"
	again, ok := session.PendingRuntimeApprovalRequest(request.RequestID)
	if !ok || string(again.Payload) != string(request.Payload) || again.Metadata["approval_force_manual"] != "true" {
		t.Fatalf("pending approval request was not cloned = %#v ok=%v", again, ok)
	}
}

func TestNativePiFailedApprovalDeliveryRemainsPendingForRetry(t *testing.T) {
	_, session := openPiNativeTestSession(t, true, agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "pi-native-approval-retry"})
	drainPiHandshake(t, session.Events())

	attempts := 0
	broker := nativePiApprovalBroker{session: session}
	if err := broker.RequestApproval(context.Background(), runtimecore.ApprovalRequest{RequestID: "approval-retry", ToolName: "shell"}, func(runtimecore.ApprovalResponse) error {
		attempts++
		if attempts == 1 {
			return errors.New("delivery failed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	command := agentcore.RuntimeCommand{
		CommandID: "answer-approval-retry", Kind: agentcore.RuntimeCommandAnswerPermission,
		AnswerPermission: &agentcore.AnswerPermissionCommand{RequestID: "approval-retry", Decision: agentcore.PermissionApproved},
	}
	if _, err := session.Dispatch(context.Background(), command); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeBackendRejected) {
		t.Fatalf("first approval delivery error = %v", err)
	}
	if len(session.PendingPermissionRequests()) != 1 {
		t.Fatal("failed approval delivery consumed the pending request")
	}
	command.CommandID = "answer-approval-retry-2"
	if _, err := session.Dispatch(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || len(session.PendingPermissionRequests()) != 0 {
		t.Fatalf("approval retry attempts=%d pending=%d", attempts, len(session.PendingPermissionRequests()))
	}
}
