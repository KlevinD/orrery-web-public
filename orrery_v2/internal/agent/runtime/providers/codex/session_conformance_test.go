package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/conformance"
	runtimesession "orrery_v2/internal/agent/runtime/session"
)

type nativeConformanceHarness struct {
	backend *NativeSession
	events  chan agentcore.SessionEnvelope
	count   atomic.Int64
	nextID  atomic.Int64

	mu          sync.Mutex
	permissions map[string]int64
}

func newNativeConformanceHarness(t *testing.T) conformance.Harness {
	t.Helper()
	spec := agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: conformance.SessionID}
	session := openNativeTestSession(t, "manual", spec, nativeConfig(), nil)
	harness := &nativeConformanceHarness{backend: session.backend, events: make(chan agentcore.SessionEnvelope, 512), permissions: make(map[string]int64)}
	go func() {
		defer close(harness.events)
		for event := range session.backend.Events() {
			switch event.Event.Kind {
			case agentcore.EventBackendBound, agentcore.EventCatalogUpdated, agentcore.EventPromptAccepted:
				continue
			}
			harness.events <- event
		}
	}()
	return harness
}

func (h *nativeConformanceHarness) Dispatch(ctx context.Context, command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	h.count.Add(1)
	return h.backend.Dispatch(ctx, command)
}

func (h *nativeConformanceHarness) Events() <-chan agentcore.SessionEnvelope { return h.events }
func (h *nativeConformanceHarness) Capabilities() agentcore.Capabilities {
	return h.backend.Capabilities()
}
func (h *nativeConformanceHarness) PendingPermissionRequests() []agentcore.PendingPermissionView {
	return h.backend.PendingPermissionRequests()
}
func (h *nativeConformanceHarness) DispatchCount() int { return int(h.count.Load()) }

func (h *nativeConformanceHarness) Emit(generation uint64, event agentcore.SessionEvent) {
	var frame map[string]any
	switch event.Kind {
	case agentcore.EventMessageDelta:
		turnID := h.turnID(generation)
		frame = nativeFixtureNotification("item/agentMessage/delta", map[string]any{"threadId": h.backend.boundThread(), "turnId": turnID, "itemId": event.MessageDelta.ItemID, "delta": event.MessageDelta.Text})
	case agentcore.EventTurnResult:
		turnID := h.turnID(generation)
		status := "completed"
		switch event.TurnResult.Outcome.Kind {
		case agentcore.TurnOutcomeFailed:
			status = "failed"
		case agentcore.TurnOutcomeCancelled:
			status = "interrupted"
		}
		turn := map[string]any{"id": turnID, "status": status}
		if event.TurnResult.ResultText != "" {
			turn["error"] = map[string]any{"message": event.TurnResult.ResultText}
		}
		frame = nativeFixtureNotification("turn/completed", map[string]any{"threadId": h.backend.boundThread(), "turn": turn})
	case agentcore.EventPermission:
		turnID := h.turnID(generation)
		wireID := h.nextID.Add(1) + 900
		h.mu.Lock()
		h.permissions[event.Permission.RequestID] = wireID
		h.mu.Unlock()
		frame = map[string]any{"jsonrpc": "2.0", "id": wireID, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": h.backend.boundThread(), "turnId": turnID, "itemId": "tool-" + event.Permission.RequestID}}
	case agentcore.EventPermissionResolved:
		h.mu.Lock()
		wireID := h.permissions[event.PermissionResolved.RequestID]
		delete(h.permissions, event.PermissionResolved.RequestID)
		h.mu.Unlock()
		frame = nativeFixtureNotification("serverRequest/resolved", map[string]any{"requestId": wireID})
	case agentcore.EventNotice:
		frame = nativeFixtureNotification("warning", map[string]any{"message": event.Notice.Message})
	default:
		panic(fmt.Sprintf("native conformance fixture cannot translate %s", event.Kind))
	}
	encoded, err := json.Marshal(frame)
	if err != nil {
		panic(err)
	}
	if err := h.backend.handleNativeLine(encoded); err != nil {
		panic(err)
	}
}

func (h *nativeConformanceHarness) EndEvents() { h.backend.closeEvents() }

func (h *nativeConformanceHarness) turnID(generation uint64) string {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		h.backend.mu.Lock()
		for turnID, candidate := range h.backend.turnGenerations {
			if candidate == generation {
				h.backend.mu.Unlock()
				return turnID
			}
		}
		h.backend.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	return fmt.Sprintf("turn-%d", generation)
}

func nativeFixtureNotification(method string, params map[string]any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
}

func TestNativeCodexPassesSharedConformance(t *testing.T) {
	conformance.RunSharedSuite(t, newNativeConformanceHarness)
}

func TestNativeCodexOrchestratorFlow(t *testing.T) {
	harness := newNativeConformanceHarness(t).(*nativeConformanceHarness)
	orchestrator, err := runtimesession.NewOrchestrator(conformance.SessionID, harness, nil, runtimesession.OrchestratorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer orchestrator.Close()
	command := nativeSend("orchestrated", "client", "hello")
	if _, err := orchestrator.Dispatch(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	harness.Emit(1, agentcore.SessionEvent{Kind: agentcore.EventMessageDelta, MessageDelta: &agentcore.MessageDeltaEvent{ItemID: "message", Text: "answer"}})
	harness.Emit(1, agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: &agentcore.TurnResultEvent{Epoch: 1, Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeEndTurn}}})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if orchestrator.Snapshot().State.Kind == runtimesession.StateIdle {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("orchestrated native state did not settle: %#v", orchestrator.Snapshot())
}
