package pi

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/conformance"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

type piConformanceHarness struct {
	backend *NativeSession
	events  chan agentcore.SessionEnvelope
	count   atomic.Int64
}

func newPiConformanceHarness(t *testing.T) conformance.Harness {
	t.Helper()
	_, backend := openPiNativeTestSession(t, true, agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: conformance.SessionID})
	harness := &piConformanceHarness{backend: backend, events: make(chan agentcore.SessionEnvelope, 512)}
	go func() {
		defer close(harness.events)
		for event := range backend.Events() {
			switch event.Event.Kind {
			case agentcore.EventBackendBound, agentcore.EventCatalogUpdated, agentcore.EventPromptAccepted:
				continue
			}
			harness.events <- event
		}
	}()
	return harness
}

func (h *piConformanceHarness) Dispatch(ctx context.Context, command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	h.count.Add(1)
	return h.backend.Dispatch(ctx, command)
}

func (h *piConformanceHarness) Events() <-chan agentcore.SessionEnvelope { return h.events }
func (h *piConformanceHarness) Capabilities() agentcore.Capabilities     { return h.backend.Capabilities() }
func (h *piConformanceHarness) PendingPermissionRequests() []agentcore.PendingPermissionView {
	return h.backend.PendingPermissionRequests()
}
func (h *piConformanceHarness) DispatchCount() int { return int(h.count.Load()) }

func (h *piConformanceHarness) Emit(generation uint64, event agentcore.SessionEvent) {
	if generation != h.backend.currentGeneration() {
		h.backend.emit(generation, event)
		return
	}
	switch event.Kind {
	case agentcore.EventMessageDelta:
		frame := map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "delta": event.MessageDelta.Text}}
		h.handle(frame)
	case agentcore.EventTurnResult:
		stopReason := "stop"
		message := event.TurnResult.ResultText
		switch event.TurnResult.Outcome.Kind {
		case agentcore.TurnOutcomeCancelled:
			stopReason = "aborted"
		case agentcore.TurnOutcomeFailed:
			stopReason = "error"
		}
		frame := map[string]any{"type": "agent_end", "messages": []any{map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": message}}, "stopReason": stopReason, "errorMessage": message}}}
		h.handle(frame)
		h.handle(map[string]any{"type": "agent_settled", "aborted": stopReason == "aborted"})
	case agentcore.EventPermission:
		broker := nativePiApprovalBroker{session: h.backend}
		if err := broker.RequestApproval(context.Background(), runtimecore.ApprovalRequest{RequestID: event.Permission.RequestID, ToolName: firstNonEmpty(event.Permission.ToolName, "tool")}, func(runtimecore.ApprovalResponse) error { return nil }); err != nil {
			panic(err)
		}
	case agentcore.EventPermissionResolved:
		h.backend.mu.Lock()
		delete(h.backend.approvals, event.PermissionResolved.RequestID)
		h.backend.mu.Unlock()
		h.backend.emit(generation, event)
	case agentcore.EventNotice:
		h.backend.emit(generation, event)
	default:
		panic(fmt.Sprintf("PI conformance fixture cannot translate %s", event.Kind))
	}
}

func (h *piConformanceHarness) handle(frame map[string]any) {
	raw, err := json.Marshal(frame)
	if err != nil {
		panic(err)
	}
	if err := h.backend.handlePiLine(raw); err != nil {
		panic(err)
	}
}

func (h *piConformanceHarness) EndEvents() { h.backend.closeEvents() }

func TestNativePiPassesSharedConformance(t *testing.T) {
	conformance.RunSharedSuite(t, newPiConformanceHarness)
}
