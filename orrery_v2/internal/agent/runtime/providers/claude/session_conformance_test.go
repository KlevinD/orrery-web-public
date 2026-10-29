package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/conformance"
	runtimesession "orrery_v2/internal/agent/runtime/session"
)

type claudeConformanceHarness struct {
	backend *NativeSession
	events  chan agentcore.SessionEnvelope
	count   atomic.Int64
}

func newClaudeConformanceHarness(t *testing.T) conformance.Harness {
	t.Helper()
	spec := agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: conformance.SessionID}
	session := openClaudeTestSession(t, "manual", spec, claudeNativeConfig(), nil)
	harness := &claudeConformanceHarness{backend: session.backend, events: make(chan agentcore.SessionEnvelope, 512)}
	go func() {
		defer close(harness.events)
		for event := range session.backend.Events() {
			switch event.Event.Kind {
			case agentcore.EventBackendBound, agentcore.EventCatalogUpdated, agentcore.EventPromptAccepted, agentcore.EventConfigChanged, agentcore.EventProvisioning:
				continue
			}
			harness.events <- event
		}
	}()
	return harness
}

func (h *claudeConformanceHarness) Dispatch(ctx context.Context, command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	h.count.Add(1)
	return h.backend.Dispatch(ctx, command)
}

func (h *claudeConformanceHarness) Events() <-chan agentcore.SessionEnvelope { return h.events }
func (h *claudeConformanceHarness) Capabilities() agentcore.Capabilities {
	return h.backend.Capabilities()
}
func (h *claudeConformanceHarness) PendingPermissionRequests() []agentcore.PendingPermissionView {
	return h.backend.PendingPermissionRequests()
}
func (h *claudeConformanceHarness) DispatchCount() int { return int(h.count.Load()) }

func (h *claudeConformanceHarness) Emit(generation uint64, event agentcore.SessionEvent) {
	var frame map[string]any
	switch event.Kind {
	case agentcore.EventMessageDelta:
		frame = map[string]any{"type": "assistant", "message": map[string]any{"id": event.MessageDelta.ItemID, "content": []any{map[string]any{"type": "text", "text": event.MessageDelta.Text}}}}
	case agentcore.EventTurnResult:
		h.backend.mu.Lock()
		if len(h.backend.resultQueue) == 0 || h.backend.resultQueue[0] != generation {
			h.backend.resultQueue = append([]uint64{generation}, h.backend.resultQueue...)
		}
		h.backend.mu.Unlock()
		reason := "end_turn"
		switch event.TurnResult.Outcome.Kind {
		case agentcore.TurnOutcomeCancelled:
			reason = "cancelled"
		case agentcore.TurnOutcomeCompleted:
			if event.TurnResult.Outcome.StopReason != nil {
				switch event.TurnResult.Outcome.StopReason.Kind {
				case agentcore.StopReasonRefused:
					reason = "refusal"
				case agentcore.StopReasonTruncated:
					reason = string(event.TurnResult.Outcome.StopReason.Truncation)
				}
			}
		}
		frame = map[string]any{"type": "result", "subtype": "success", "is_error": event.TurnResult.IsError, "result": event.TurnResult.ResultText, "terminal_reason": reason}
	case agentcore.EventPermission:
		frame = map[string]any{"type": "control_request", "request_id": event.Permission.RequestID, "request": map[string]any{"subtype": "can_use_tool", "tool_name": firstClaudeString(event.Permission.ToolName, "Bash"), "tool_use_id": "tool-" + event.Permission.RequestID, "input": map[string]any{}}}
	case agentcore.EventPermissionResolved:
		frame = map[string]any{"type": "control_cancel_request", "request_id": event.PermissionResolved.RequestID}
	case agentcore.EventNotice:
		h.backend.emit(generation, event)
		return
	default:
		panic(fmt.Sprintf("Claude conformance fixture cannot translate %s", event.Kind))
	}
	encoded, err := json.Marshal(frame)
	if err != nil {
		panic(err)
	}
	if err := h.backend.handleClaudeLine(encoded); err != nil {
		panic(err)
	}
}

func (h *claudeConformanceHarness) EndEvents() { h.backend.closeEvents() }

func TestNativeClaudePassesSharedConformance(t *testing.T) {
	conformance.RunSharedSuite(t, newClaudeConformanceHarness)
}

func TestNativeClaudeOrchestratorFlow(t *testing.T) {
	harness := newClaudeConformanceHarness(t).(*claudeConformanceHarness)
	orchestrator, err := runtimesession.NewOrchestrator(conformance.SessionID, harness, nil, runtimesession.OrchestratorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer orchestrator.Close()
	if _, err := orchestrator.Dispatch(context.Background(), claudeSend("orchestrated", "client", "hello")); err != nil {
		t.Fatal(err)
	}
	harness.Emit(1, agentcore.SessionEvent{Kind: agentcore.EventMessageDelta, MessageDelta: &agentcore.MessageDeltaEvent{ItemID: "message", Text: "answer"}})
	harness.Emit(1, agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: &agentcore.TurnResultEvent{Epoch: 1, Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeEndTurn}}})
	if state := waitClaudeOrchestratorState(t, orchestrator); state != runtimesession.StateIdle {
		t.Fatalf("orchestrated state = %s", state)
	}
}

func waitClaudeOrchestratorState(t *testing.T, orchestrator *runtimesession.Orchestrator) runtimesession.StateKind {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if state := orchestrator.Snapshot().State.Kind; state == runtimesession.StateIdle || state == runtimesession.StateError {
			return state
		}
		time.Sleep(time.Millisecond)
	}
	return orchestrator.Snapshot().State.Kind
}
