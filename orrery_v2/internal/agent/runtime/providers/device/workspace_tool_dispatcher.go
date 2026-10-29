package device

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	runtimecore "orrery_v2/internal/agent/runtime/core"
)

type WorkspaceToolTarget struct{ DeviceID, GrantID, ProjectID, ActorID string }
type WorkspaceToolResolver func(context.Context, runtimecore.RuntimeToolCallRequest, string, string) (WorkspaceToolTarget, error)

// WorkspaceToolDispatcher preserves the host's run identity and returns the
// result to the existing model tool call. The resolver must authorize the owner
// and device from trusted server state; model input is never grant authority.
type WorkspaceToolDispatcher struct {
	Broker  DeviceWorkOrderBroker
	Resolve WorkspaceToolResolver
	Now     func() time.Time
}

func WorkspaceToolOperation(name string) string {
	switch name {
	case "desktop_list_files":
		return "list_files"
	case "desktop_read_excel":
		return "read_excel"
	case "desktop_write_excel":
		return "write_excel"
	}
	return ""
}

func workspaceToolID(parts ...string) string {
	raw, _ := json.Marshal(parts)
	sum := sha256.Sum256(raw)
	return "wt_" + hex.EncodeToString(sum[:24])
}

func (d WorkspaceToolDispatcher) CallRuntimeTool(ctx context.Context, call runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
	fail := func(code string, uncertain bool) (runtimecore.RuntimeToolCallResult, error) {
		output := map[string]any{"status": "failed", "code": code, "tool_call_id": call.CallID}
		if uncertain {
			output["outcome_uncertain"], output["retryable"] = true, false
		}
		return runtimecore.RuntimeToolCallResult{Output: output, OutcomeUncertain: uncertain}, nil
	}
	op := WorkspaceToolOperation(call.Name)
	if d.Broker == nil || d.Resolve == nil || op == "" || call.CallID == "" || call.RunID.IsZero() || call.AgentID.IsZero() || call.TenantID == "" {
		return fail("desktop_tool_unavailable", false)
	}
	args := make(map[string]any, len(call.Input))
	for k, v := range call.Input {
		args[k] = v
	}
	deviceID, ok := args["device_id"].(string)
	if !ok {
		return fail("invalid_device", false)
	}
	delete(args, "device_id")
	grantID, ok := args["workspace_grant_id"].(string)
	if !ok {
		return fail("invalid_grant", false)
	}
	delete(args, "workspace_grant_id")
	// Version, operation and tool-call identity are owned by the runtime, not the model.
	for _, key := range []string{"version", "operation", "call_id"} {
		if _, exists := args[key]; exists {
			return fail("invalid_arguments", false)
		}
	}
	args["version"], args["operation"], args["call_id"] = 1, op, workspaceToolID(call.TenantID, call.RunID.String(), call.CallID)
	raw, err := json.Marshal(args)
	if err != nil || len(raw) > 131072 {
		return fail("invalid_arguments", false)
	}
	var tool WorkspaceToolCall
	if decodeStrict(raw, &tool) != nil || tool.Validate() != nil {
		return fail("invalid_arguments", false)
	}
	target, err := d.Resolve(ctx, call, deviceID, grantID)
	if err != nil || target.DeviceID != deviceID || target.GrantID != grantID || !validIdentifier(target.ActorID) || !validIdentifier(target.ProjectID) {
		return fail("desktop_access_denied", false)
	}
	id := workspaceToolID(call.TenantID, call.RunID.String(), call.CallID, deviceID, grantID)
	now := time.Now().UTC()
	if d.Now != nil {
		now = d.Now().UTC()
	}
	// Resume a durable call before offering again, retaining its original deadline
	// and fingerprint. Changed input under the same call ID is a conflict.
	record, sub, err := d.Broker.Subscribe(ctx, call.TenantID, deviceID, id, 0)
	if err == nil {
		a, _ := json.Marshal(record.Order.Task.ToolCall)
		b, _ := json.Marshal(tool)
		if string(a) != string(b) || record.Order.OriginActorID != target.ActorID || record.Order.ProjectID != target.ProjectID {
			sub.Close()
			return fail("duplicate_conflict", false)
		}
	} else {
		if !IsProtocolError(err, ProtocolDeviceMismatch) {
			return fail("desktop_broker_unavailable", false)
		}
		deadline := now.Add(5 * time.Minute)
		if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
			deadline = end.UTC()
		}
		if envelope := call.ExecutionEnvelope; envelope != nil && !envelope.ExpiresAt.IsZero() && envelope.ExpiresAt.Before(deadline) {
			deadline = envelope.ExpiresAt.UTC()
		}
		order := WorkOrder{WorkOrderID: id, IdempotencyKey: id, ProjectID: target.ProjectID, OriginActorID: target.ActorID, WorkspaceGrantID: grantID, Task: WorkOrderTask{Kind: "workspace_tool", ToolCall: &tool}, RequiredCapabilities: tool.Capabilities(), RequestedArtifactClasses: []ArtifactClass{}, Deadline: deadline}
		record, sub, err = d.Broker.Offer(ctx, OfferRequest{TenantID: call.TenantID, DeviceID: deviceID, SessionID: workspaceToolID(call.RunID.String()), Order: order})
		if err != nil {
			return fail("desktop_offer_unavailable", op == "write_excel")
		}
	}
	defer sub.Close()
	deadline := record.Order.Deadline
	wait, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	var result map[string]any
	for {
		event, receiveErr := receiveWorkspaceToolEvent(wait, sub.Events(), record.State.Terminal())
		if receiveErr != nil {
			if errors.Is(receiveErr, io.EOF) {
				return fail("desktop_result_unavailable", op == "write_excel")
			}
			cancelCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			_ = d.Broker.Cancel(cancelCtx, call.TenantID, deviceID, id, "workspace tool call cancelled")
			stop()
			// An unacknowledged write must never be automatically retried with a new ID.
			out, _ := fail("desktop_timeout_or_cancelled", op == "write_excel")
			return out, nil
		} else {
			if event.Rejected != nil {
				return fail(string(event.Rejected.ReasonCode), false)
			}
			if frame := event.Frame; frame != nil && frame.Frame.Kind == "tool_result" {
				if frame.Frame.CallID != tool.CallID || len(frame.Frame.Content) > 196608 || json.Unmarshal([]byte(frame.Frame.Content), &result) != nil || result["call_id"] != tool.CallID || (result["status"] != "completed" && result["status"] != "failed") {
					return fail("desktop_invalid_result", op == "write_excel")
				}
			}
			if terminal := event.Terminal; terminal != nil {
				if result != nil {
					result["tool_call_id"] = call.CallID
					result["work_order_id"] = id
					if result["status"] == "completed" && terminal.Status != TerminalSucceeded {
						return runtimecore.RuntimeToolCallResult{Output: map[string]any{"status": "failed", "code": "desktop_terminal_inconsistent", "receipt": result, "retryable": false}, OutcomeUncertain: op == "write_excel"}, nil
					}
					return runtimecore.RuntimeToolCallResult{Output: result, OutcomeUncertain: result["outcome_uncertain"] == true}, nil
				}
				return fail(fmt.Sprintf("desktop_%s_without_result", terminal.Status), op == "write_excel")
			}
		}
	}
}

// A terminal record already has a complete, buffered durable replay. Retrieve
// its receipt even after the original execution deadline; never offer it again.
func receiveWorkspaceToolEvent(ctx context.Context, events <-chan BrokerEvent, terminalReplay bool) (BrokerEvent, error) {
	if terminalReplay {
		select {
		case event, open := <-events:
			if open {
				return event, nil
			}
		default:
		}
		return BrokerEvent{}, io.EOF
	}
	select {
	case event, open := <-events:
		if !open {
			return BrokerEvent{}, io.EOF
		}
		return event, nil
	case <-ctx.Done():
		return BrokerEvent{}, ctx.Err()
	}
}
