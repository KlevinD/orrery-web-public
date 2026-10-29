package session

import (
	"context"
	"sync"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func TestRuntimePrewarmRetainsBackendSessionIDForLateRunSubscriber(t *testing.T) {
	connection := &prewarmBackendSessionConnection{}
	runtime, err := NewRuntime(RuntimeOptions{
		Kind:       agentcore.RuntimeKindPi,
		Connection: connection,
		Workspaces: NewWorkspaceBroker(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := canonicalTestRequest(t, "prewarm-backend-session")
	if err := runtime.PrewarmSession(context.Background(), request); err != nil {
		t.Fatalf("PrewarmSession: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for {
		runtime.mu.Lock()
		open := runtime.prewarmed[request.RunID]
		runtime.mu.Unlock()
		if open != nil && open.orchestrator.Snapshot().Sequence > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("prewarm backend binding was not consumed")
		}
		time.Sleep(time.Millisecond)
	}

	response, err := runtime.RunSession(context.Background(), request, nil)
	if err != nil {
		t.Fatalf("RunSession: %v", err)
	}
	if response.Status != agentcore.RuntimeStatusCompleted {
		t.Fatalf("status = %q, want completed", response.Status)
	}
	if got := response.ProviderMetadata["session_id"]; got != "provider-session-1" {
		t.Fatalf("provider session id = %q, want provider-session-1", got)
	}
}

type prewarmBackendSessionConnection struct {
	backend *prewarmBackendSession
}

func (c *prewarmBackendSessionConnection) OpenSession(_ context.Context, spec agentcore.SessionSpec, _ agentcore.SessionConfig) (agentcore.SessionBackend, error) {
	backend := &prewarmBackendSession{
		sessionID:        spec.SessionID,
		backendSessionID: "provider-session-1",
		events:           make(chan agentcore.SessionEnvelope, 8),
	}
	backend.emit(0, agentcore.SessionEvent{
		Kind: agentcore.EventBackendBound,
		BackendBound: &agentcore.BackendBoundEvent{
			BackendSessionID: backend.backendSessionID,
		},
	})
	c.backend = backend
	return backend, nil
}

func (c *prewarmBackendSessionConnection) CloseSession(context.Context, agentcore.SessionID) error {
	if c.backend != nil {
		c.backend.close()
	}
	return nil
}

func (c *prewarmBackendSessionConnection) Capabilities(context.Context) agentcore.Capabilities {
	return prewarmBackendCapabilities()
}

type prewarmBackendSession struct {
	sessionID        agentcore.SessionID
	backendSessionID string
	events           chan agentcore.SessionEnvelope
	closeOnce        sync.Once
}

func (s *prewarmBackendSession) Dispatch(_ context.Context, command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	s.emit(1, agentcore.SessionEvent{
		Kind:           agentcore.EventPromptAccepted,
		PromptAccepted: &agentcore.PromptAcceptedEvent{ClientMsgID: command.Send.Metadata.ClientMsgID},
	})
	s.emit(1, agentcore.SessionEvent{
		Kind: agentcore.EventMessageFinalized,
		MessageFinalized: &agentcore.MessageFinalizedEvent{
			Message: agentcore.FinalizedMessage{ItemID: "message-1", Kind: agentcore.ItemText, Content: "done", Sequence: 1},
		},
	})
	s.emit(1, agentcore.SessionEvent{
		Kind: agentcore.EventTurnResult,
		TurnResult: &agentcore.TurnResultEvent{
			Epoch:   1,
			Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeCompleted, StopReason: &agentcore.StopReason{Kind: agentcore.StopReasonEndTurn}},
		},
	})
	return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionStarted, TurnGeneration: 1}, nil
}

func (s *prewarmBackendSession) Events() <-chan agentcore.SessionEnvelope { return s.events }

func (s *prewarmBackendSession) Capabilities() agentcore.Capabilities {
	return prewarmBackendCapabilities()
}

func (s *prewarmBackendSession) PendingPermissionRequests() []agentcore.PendingPermissionView {
	return nil
}

func (s *prewarmBackendSession) BackendSessionID() string { return s.backendSessionID }

func (s *prewarmBackendSession) emit(generation uint64, event agentcore.SessionEvent) {
	s.events <- agentcore.SessionEnvelope{
		SessionID:      s.sessionID,
		TurnGeneration: generation,
		Timestamp:      time.Now().UTC(),
		Source:         agentcore.EventSourceProvider,
		Projection:     agentcore.EventProjectionCanonical,
		Event:          event,
	}
}

func (s *prewarmBackendSession) close() {
	s.closeOnce.Do(func() {
		close(s.events)
	})
}

func prewarmBackendCapabilities() agentcore.Capabilities {
	return agentcore.Capabilities{
		SupportedCommands: agentcore.CommandSet{Send: true},
		PromptBlocks:      agentcore.BlockSet{Text: true},
		PromptAccepted:    agentcore.PromptAcceptedNative,
		SupportsMultiTurn: true,
		Signals:           agentcore.SignalSet{TerminalResult: true},
	}
}

var _ agentcore.BackendConnection = (*prewarmBackendSessionConnection)(nil)
var _ agentcore.SessionBackend = (*prewarmBackendSession)(nil)
var _ backendSessionIDSource = (*prewarmBackendSession)(nil)
