package device

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
	"orrery_v2/internal/platform/testkit"
)

type deviceConformanceHarness struct {
	backend *SessionBackend
	link    *DeviceLink
	now     time.Time
	events  chan agentcore.SessionEnvelope
	count   atomic.Int64
	nextID  atomic.Int64

	mu          sync.Mutex
	workOrderID string
	sequence    int64
	terminal    bool
	writeMu     sync.Mutex
	ended       bool
}

func newDeviceConformanceHarness(t *testing.T) conformance.Harness {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	repository, err := NewSQLRepository(ctx, testkit.NewTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	broker, err := NewDurableBroker(BrokerOptions{Repository: repository, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := NewConnection(ConnectionConfig{Broker: broker, Now: func() time.Time { return now }, CloseBroker: true})
	if err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(RuntimeConfig{DeviceID: "device_conformance_001", WorkspaceGrantID: "grant_conformance_001"})
	request := agentcore.RuntimeRequest{TenantID: "tenant-conformance", RunID: "run-conformance", AgentID: "agent-conformance", RuntimeOptions: agentcore.RuntimeOptions{ProviderConfig: config}, Deadline: now.Add(time.Hour)}
	if err := connection.BindRuntimeRequest(conformance.SessionID, request); err != nil {
		t.Fatal(err)
	}
	backend, err := connection.OpenSession(ctx, agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: conformance.SessionID}, agentcore.SessionConfig{WorkspaceRef: "ws-conformance"})
	if err != nil {
		t.Fatal(err)
	}
	link := attachWelcomedDevice(t, broker, "tenant-conformance", "device_conformance_001", "conn_conformance_001", now)
	harness := &deviceConformanceHarness{backend: backend.(*SessionBackend), link: link, now: now, events: make(chan agentcore.SessionEnvelope, 512)}
	go func() {
		for envelope := range harness.backend.Events() {
			if envelope.Event.Kind == agentcore.EventPromptAccepted {
				continue
			}
			harness.write(envelope)
		}
	}()
	t.Cleanup(func() { connection.Close() })
	return harness
}

func (h *deviceConformanceHarness) Dispatch(ctx context.Context, command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	h.count.Add(1)
	type result struct {
		receipt agentcore.CommandReceipt
		err     error
	}
	done := make(chan result, 1)
	go func() { receipt, err := h.backend.Dispatch(ctx, command); done <- result{receipt: receipt, err: err} }()
	if command.Kind != agentcore.RuntimeCommandSend {
		value := <-done
		return value.receipt, value.err
	}
	select {
	case value := <-done:
		return value.receipt, value.err
	case envelope, ok := <-h.link.Outbound():
		if !ok {
			return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "device conformance link closed", true)
		}
		if envelope.Type != MessageWorkOrderOffer {
			return agentcore.CommandReceipt{}, fmt.Errorf("unexpected device outbound %s", envelope.Type)
		}
		order := envelope.Payload.(WorkOrder)
		h.mu.Lock()
		h.workOrderID = order.WorkOrderID
		h.mu.Unlock()
		h.send(MessageWorkOrderAccepted, WorkOrderAcceptedPayload{WorkOrderID: order.WorkOrderID, AcceptedAt: h.now})
		value := <-done
		return value.receipt, value.err
	}
}

func (h *deviceConformanceHarness) Events() <-chan agentcore.SessionEnvelope { return h.events }
func (h *deviceConformanceHarness) Capabilities() agentcore.Capabilities {
	return h.backend.Capabilities()
}
func (h *deviceConformanceHarness) PendingPermissionRequests() []agentcore.PendingPermissionView {
	return h.backend.PendingPermissionRequests()
}
func (h *deviceConformanceHarness) DispatchCount() int { return int(h.count.Load()) }

func (h *deviceConformanceHarness) Emit(generation uint64, event agentcore.SessionEvent) {
	h.mu.Lock()
	workOrderID := h.workOrderID
	if event.Kind == agentcore.EventTurnResult && h.terminal {
		h.mu.Unlock()
		return
	}
	switch event.Kind {
	case agentcore.EventMessageDelta, agentcore.EventPermission, agentcore.EventPermissionResolved:
		h.sequence++
	case agentcore.EventTurnResult:
		h.sequence++
		h.terminal = true
	}
	sequence := h.sequence
	h.mu.Unlock()
	switch event.Kind {
	case agentcore.EventMessageDelta:
		h.send(MessageWorkOrderFrame, WorkOrderFramePayload{WorkOrderID: workOrderID, Sequence: sequence, Frame: RuntimeFrame{Kind: "text", Channel: "output", Content: event.MessageDelta.Text}})
	case agentcore.EventTurnResult:
		status := TerminalSucceeded
		if event.TurnResult.Outcome.Kind == agentcore.TurnOutcomeFailed {
			status = TerminalFailed
		}
		if event.TurnResult.Outcome.Kind == agentcore.TurnOutcomeCancelled {
			status = TerminalCancelled
		}
		h.send(MessageWorkOrderTerminal, WorkOrderTerminalPayload{WorkOrderID: workOrderID, Sequence: sequence, Status: status, Summary: event.TurnResult.ResultText})
	case agentcore.EventPermission:
		h.send(MessageWorkOrderFrame, WorkOrderFramePayload{WorkOrderID: workOrderID, Sequence: sequence, Frame: RuntimeFrame{Kind: "permission", Phase: "waiting", CallID: opaqueIdentifier(event.Permission.RequestID, "call"), Category: "other"}})
	case agentcore.EventPermissionResolved:
		h.send(MessageWorkOrderFrame, WorkOrderFramePayload{WorkOrderID: workOrderID, Sequence: sequence, Frame: RuntimeFrame{Kind: "permission", Phase: "resolved", CallID: opaqueIdentifier(event.PermissionResolved.RequestID, "call"), Category: "other"}})
	case agentcore.EventNotice:
		h.write(agentcore.SessionEnvelope{SessionID: conformance.SessionID, TurnGeneration: generation, Timestamp: h.now, Source: agentcore.EventSourceProvider, Projection: agentcore.EventProjectionCanonical, Event: event})
	default:
		panic(fmt.Sprintf("device conformance fixture cannot translate %s", event.Kind))
	}
}

func (h *deviceConformanceHarness) EndEvents() {
	h.writeMu.Lock()
	if !h.ended {
		h.ended = true
		close(h.events)
	}
	h.writeMu.Unlock()
}

func (h *deviceConformanceHarness) write(envelope agentcore.SessionEnvelope) {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	if h.ended {
		return
	}
	h.events <- envelope
}

func (h *deviceConformanceHarness) send(kind MessageType, payload any) {
	id := h.nextID.Add(1)
	messageID := fmt.Sprintf("msg_conformance_%06d", id)
	if err := receiveDeviceEnvelope(h.link, h.now, messageID, kind, payload); err != nil {
		panic(err)
	}
}

func TestDeviceSessionPassesSharedConformance(t *testing.T) {
	conformance.RunSharedSuite(t, newDeviceConformanceHarness)
}
