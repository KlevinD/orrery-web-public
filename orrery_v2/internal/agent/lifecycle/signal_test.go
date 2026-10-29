package lifecycle

import (
	"context"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
	missiontask "orrery_v2/internal/mission/task"
)

func TestSignalEnvelopeRequiresCorrelationAndRunID(t *testing.T) {
	signal := SignalEnvelope{
		SignalID:       SignalID("signal-001"),
		Name:           BeforeTaskAccepted,
		MissionID:      missioncore.MissionID("mission-001"),
		AgentID:        agentcore.AgentID("agent-001"),
		AgentProfileID: agentcore.AgentProfileID("profile-001"),
		TaskID:         missioncore.TaskID("task-001"),
		Attempt:        1,
		EmittedAt:      time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC),
		Payload:        TaskAcceptedPayload{AcceptedBy: agentcore.AgentID("agent-001")},
	}

	err := signal.Validate()
	if err == nil {
		t.Fatal("expected invalid signal when correlation_id and run_id are missing")
	}
	if !IsInvalidSignal(err) {
		t.Fatalf("expected invalid signal error, got %v", err)
	}
	if !IsMissingCorrelationID(err) {
		t.Fatalf("expected missing correlation id marker, got %v", err)
	}
}

func TestSignalEnvelopeRequiresStableAgentProfileIdentity(t *testing.T) {
	signal := validSignal()
	signal.AgentProfileID = ""

	err := signal.Validate()
	if err == nil {
		t.Fatal("expected invalid signal when agent_profile_id is missing")
	}
	if !IsInvalidSignal(err) {
		t.Fatalf("expected invalid signal error, got %v", err)
	}
	if !strings.Contains(err.Error(), "agent_profile_id") {
		t.Fatalf("expected validation error to mention agent_profile_id, got %v", err)
	}
}

func TestSignalBuilderCarriesStableAgentProfileIdentity(t *testing.T) {
	builder := NewSignalBuilder(SignalBuilderOptions{})
	state := validRunState()
	state.AgentProfileID = agentcore.AgentProfileID("profile-builder-001")

	signal, err := builder.FromState(state, AfterTaskAccepted, EmptyPayload{}, SignalBuildOptions{})
	if err != nil {
		t.Fatalf("build signal failed: %v", err)
	}
	if signal.AgentProfileID != state.AgentProfileID {
		t.Fatalf("agent profile id = %s, want %s", signal.AgentProfileID, state.AgentProfileID)
	}
}

func TestCoreSignalSinkCarriesStableAgentProfileIdentity(t *testing.T) {
	bus := &capturingSignalBus{}
	sink := NewCoreSignalSink(bus)

	err := sink.EmitSignal(context.Background(), agentcore.Signal{
		Name:           agentcore.SignalAfterTaskAccepted,
		TenantID:       "tenant-a",
		RunID:          agentcore.AgentRunID("run-001"),
		AgentID:        agentcore.AgentID("agent-001"),
		AgentProfileID: agentcore.AgentProfileID("profile-core-001"),
		MissionID:      "mission-001",
		TaskID:         "task-001",
		At:             time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("emit core signal failed: %v", err)
	}
	if bus.signal.AgentProfileID != agentcore.AgentProfileID("profile-core-001") {
		t.Fatalf("agent profile id = %s, want profile-core-001", bus.signal.AgentProfileID)
	}
	if bus.signal.TenantID != "tenant-a" {
		t.Fatalf("tenant id = %s, want tenant-a", bus.signal.TenantID)
	}
}

func TestCoreSignalSinkMapsTaskAcceptedPayload(t *testing.T) {
	bus := &capturingSignalBus{}
	sink := NewCoreSignalSink(bus)

	err := sink.EmitSignal(context.Background(), agentcore.Signal{
		Name:           agentcore.SignalAfterTaskAccepted,
		RunID:          agentcore.AgentRunID("run-001"),
		AgentID:        agentcore.AgentID("agent-001"),
		AgentProfileID: agentcore.AgentProfileID("profile-core-001"),
		MissionID:      "mission-001",
		TaskID:         "task-001",
		At:             time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("emit core signal failed: %v", err)
	}
	want := TaskAcceptedPayload{AcceptedBy: agentcore.AgentID("agent-001")}
	if bus.signal.Payload != want {
		t.Fatalf("payload = %#v, want %#v", bus.signal.Payload, want)
	}
}

func TestCoreSignalSinkMapsFailurePayloads(t *testing.T) {
	for _, tc := range []struct {
		name       string
		signalName agentcore.SignalName
		wantCode   string
		wantMsg    string
		payload    map[string]string
	}{
		{
			name:       "failed with error",
			signalName: agentcore.SignalAgentFailed,
			wantCode:   string(agentcore.SignalAgentFailed),
			wantMsg:    "runtime failed",
			payload:    map[string]string{"error": "runtime failed"},
		},
		{
			name:       "cancelled with message and code",
			signalName: agentcore.SignalAgentCancelled,
			wantCode:   "request_cancelled",
			wantMsg:    "request context cancelled",
			payload:    map[string]string{"code": "request_cancelled", "message": "request context cancelled"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bus := &capturingSignalBus{}
			sink := NewCoreSignalSink(bus)

			err := sink.EmitSignal(context.Background(), agentcore.Signal{
				Name:           tc.signalName,
				RunID:          agentcore.AgentRunID("run-001"),
				AgentID:        agentcore.AgentID("agent-001"),
				AgentProfileID: agentcore.AgentProfileID("profile-core-001"),
				MissionID:      "mission-001",
				TaskID:         "task-001",
				At:             time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC),
				Payload:        tc.payload,
			})
			if err != nil {
				t.Fatalf("emit core signal failed: %v", err)
			}
			got, ok := bus.signal.Payload.(FailurePayload)
			if !ok {
				t.Fatalf("payload = %T, want FailurePayload", bus.signal.Payload)
			}
			if got.Code != tc.wantCode || got.Message != tc.wantMsg {
				t.Fatalf("payload = %#v, want code %q message %q", got, tc.wantCode, tc.wantMsg)
			}
		})
	}
}

func TestSignalEnvelopeRejectsFullMemoryAndTracePayloads(t *testing.T) {
	base := validSignal()

	for name, payload := range map[string]SignalPayload{
		"full memory": UnsafePayloadMarker{ContainsFullMemory: true},
		"full trace":  UnsafePayloadMarker{ContainsFullTrace: true},
	} {
		t.Run(name, func(t *testing.T) {
			base.Payload = payload
			err := base.Validate()
			if err == nil {
				t.Fatal("expected invalid signal")
			}
			if !IsInvalidSignal(err) {
				t.Fatalf("expected invalid signal error, got %v", err)
			}
		})
	}
}

func TestAgentRunStateUsesCoreContracts(t *testing.T) {
	state := AgentRunState{
		MissionID:      missioncore.MissionID("mission-001"),
		AgentID:        agentcore.AgentID("agent-001"),
		AgentProfileID: agentcore.AgentProfileID("profile-001"),
		TaskID:         missioncore.TaskID("task-001"),
		RunID:          agentcore.AgentRunID("run-001"),
		Attempt:        2,
		TaskContract: missiontask.TaskContract{
			ID:        missioncore.TaskID("task-001"),
			MissionID: missioncore.MissionID("mission-001"),
		},
		ContextPack: agentcore.ContextPack{
			MissionID: missioncore.MissionID("mission-001"),
			TaskID:    missioncore.TaskID("task-001"),
			AgentID:   agentcore.AgentID("agent-001"),
		},
		AgentResult: agentcore.AgentResult{
			MissionID: missioncore.MissionID("mission-001"),
			TaskID:    missioncore.TaskID("task-001"),
			AgentID:   agentcore.AgentID("agent-001"),
			RunID:     agentcore.AgentRunID("run-001"),
		},
		Metadata: map[string]string{"runtime_response_ref": "artifact://runtime/001"},
	}

	state.AddWarning("cost estimate unavailable")
	state.AddError(HandlerFailure{HandlerName: "permission", SignalName: BeforeTaskAccepted})
	state.MarkDegraded("memory_digest")

	if state.TaskContract.ID != state.TaskID || state.ContextPack.AgentID != state.AgentID || state.AgentProfileID.IsZero() {
		t.Fatalf("state identifiers drifted: %#v", state)
	}
	if len(state.Warnings) != 1 || len(state.Errors) != 1 || len(state.DegradedCapabilities) != 1 {
		t.Fatalf("expected state annotations, got %#v", state)
	}
}

func TestSignalNamesExposeRequiredStageOrder(t *testing.T) {
	want := []SignalName{
		BeforeTaskReceived,
		AfterTaskReceived,
		BeforeTaskAccepted,
		AfterTaskAccepted,
		BeforeContextBuild,
		AfterContextBuild,
		BeforeRuntimeStart,
		RuntimeStarted,
		RuntimeProgress,
		BeforeRuntimeFinish,
		AfterRuntimeFinish,
		BeforeResultNormalize,
		AfterResultNormalize,
		BeforeResultValidate,
		AfterResultValidate,
		BeforeResultCommit,
		AfterResultCommit,
		BeforeAgentReply,
		AfterAgentReply,
		AgentBlocked,
		AgentFailed,
		AgentCancelled,
		AgentTimeout,
	}

	got := AllSignalNames()
	if len(got) != len(want) {
		t.Fatalf("expected %d signal names, got %d: %#v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("signal order drifted at %d: want %s got %s", i, want[i], got[i])
		}
	}
}

func validSignal() SignalEnvelope {
	return SignalEnvelope{
		SignalID:       SignalID("signal-001"),
		Name:           BeforeTaskAccepted,
		MissionID:      missioncore.MissionID("mission-001"),
		AgentID:        agentcore.AgentID("agent-001"),
		AgentProfileID: agentcore.AgentProfileID("profile-001"),
		TaskID:         missioncore.TaskID("task-001"),
		RunID:          agentcore.AgentRunID("run-001"),
		CorrelationID:  CorrelationID("corr-001"),
		Attempt:        1,
		EmittedAt:      time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC),
		Payload:        TaskAcceptedPayload{AcceptedBy: agentcore.AgentID("agent-001")},
		Metadata:       map[string]string{"source": "test"},
	}
}

type capturingSignalBus struct {
	signal SignalEnvelope
}

func (b *capturingSignalBus) Emit(_ context.Context, signal SignalEnvelope) (EmitReport, error) {
	b.signal = signal
	return EmitReport{}, nil
}

func (b *capturingSignalBus) Subscribe(SignalName, SignalHandler, HandlerOptions) (SubscriptionID, error) {
	return "", nil
}

func (b *capturingSignalBus) Unsubscribe(SubscriptionID) error {
	return nil
}
