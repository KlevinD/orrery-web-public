package conformance

import (
	"context"
	"os"
	"sync"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

type scriptedHarness struct {
	events chan agentcore.SessionEnvelope
	mu     sync.Mutex
	count  int
	next   uint64
	once   sync.Once
}

func newScriptedHarness(*testing.T) Harness {
	return &scriptedHarness{events: make(chan agentcore.SessionEnvelope, 256)}
}

func (h *scriptedHarness) Dispatch(_ context.Context, command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.count++
	if command.Kind == agentcore.RuntimeCommandSend {
		h.next++
		return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionStarted, TurnGeneration: h.next}, nil
	}
	return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn}, nil
}

func (h *scriptedHarness) Events() <-chan agentcore.SessionEnvelope { return h.events }

func (h *scriptedHarness) Capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{
		SupportedCommands: agentcore.CommandSet{Send: true, CancelTurn: true, CancelSession: true, AnswerPermission: true},
		PromptBlocks:      agentcore.BlockSet{Text: true},
		PromptAccepted:    agentcore.PromptAcceptedNative,
		SupportsQueue:     true,
		SupportsMultiTurn: true,
	}
}

func (h *scriptedHarness) PendingPermissionRequests() []agentcore.PendingPermissionView {
	return nil
}

func (h *scriptedHarness) Emit(generation uint64, event agentcore.SessionEvent) {
	h.events <- agentcore.SessionEnvelope{SessionID: SessionID, TurnGeneration: generation, Event: event}
}

func (h *scriptedHarness) EndEvents() { h.once.Do(func() { close(h.events) }) }

func (h *scriptedHarness) DispatchCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.count
}

func TestScriptedBackendPassesSharedConformance(t *testing.T) {
	RunSharedSuite(t, newScriptedHarness)
}

func TestOrrerySessionContractTrace(t *testing.T) {
	file, err := os.Open("testdata/orrery_session_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	fixture, err := DecodeContractFixture(file)
	if err != nil {
		t.Fatal(err)
	}
	if fixture.Contract != "orrery-session-v1" {
		t.Fatalf("fixture contract = %s", fixture.Contract)
	}
	if err := RunContractFixture(fixture); err != nil {
		t.Fatal(err)
	}
}
