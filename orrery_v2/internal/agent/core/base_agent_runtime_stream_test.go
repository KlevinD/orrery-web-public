package core

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestBaseAgentConsumesCanonicalProgressAndTerminalResponse(t *testing.T) {
	runtime := &scriptedSessionRuntime{
		events: []SessionEnvelope{
			{
				SessionID: "session-a", TurnGeneration: 1, Sequence: 1,
				Event: SessionEvent{Kind: EventTurnStarted, TurnStarted: &TurnStartedEvent{Epoch: 1}},
			},
			{
				SessionID: "session-a", TurnGeneration: 1, Sequence: 2,
				Event: SessionEvent{
					Kind: EventToolCall,
					ToolCall: &ToolCallEvent{
						ToolUseID: "call-a", Name: "go.test", Input: json.RawMessage(`{"package":"./..."}`),
					},
				},
			},
		},
		response: RuntimeResponse{RunID: "run-1", Status: RuntimeStatusCompleted, Messages: []RuntimeMessage{{Type: RuntimeMessageText, Content: "Done."}}},
	}
	recorder := &recordingSignalSink{}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1", OrgIdentity: AgentOrgIdentity{OrganizationRef: OrganizationRef{ID: "tenant-a"}}, RuntimeSpec: RuntimeSpec{Kind: RuntimeKindCodex}},
		Runtime:  runtime, Signals: recorder, Delivery: &capturingDelivery{}, Trace: &recordingTraceRecorder{}, Notifier: &recordingNotifier{},
		Validator: ResultValidatorFunc(func(AgentInput, AgentResult) error { return nil }),
	})
	input := validAgentInput()
	input.TenantID = "tenant-a"
	if _, err := agent.Handle(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if runtime.request.TenantID != "tenant-a" || runtime.request.AgentID != "agent-1" {
		t.Fatalf("runtime request identity = %#v", runtime.request)
	}
	assertSignalPresent(t, recorder.Names(), SignalRuntimeStarted)
	assertSignalPresent(t, recorder.Names(), SignalRuntimeProgress)
	for _, signal := range recorder.signals {
		if signal.Name == SignalRuntimeProgress && signal.Payload["event_kind"] == string(EventToolCall) {
			if signal.Payload["tool"] != "go.test" || signal.Payload["call_id"] != "call-a" {
				t.Fatalf("canonical progress payload = %#v", signal.Payload)
			}
			return
		}
	}
	t.Fatal("canonical tool progress was not emitted")
}

func TestBaseAgentRejectsContextIdentityOverrideAndPreservesRequestTenant(t *testing.T) {
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1", OrgIdentity: AgentOrgIdentity{OrganizationRef: OrganizationRef{ID: "tenant-a"}}},
	})
	input := validAgentInput()
	input.TenantID = "tenant-b"
	input.ContextPack.AgentID = "agent-other"
	if err := agent.validateRuntimeIdentity(input); !IsRuntimeBridgeError(err, RuntimeBridgeSessionMismatch) {
		t.Fatalf("agent mismatch error = %v", err)
	}
	request := agent.BuildRuntimeRequest(AgentInput{TenantID: "tenant-b", RunID: "run-a", RuntimeOptions: RuntimeOptions{WorkingDirectory: t.TempDir()}})
	if request.TenantID != "tenant-b" {
		t.Fatalf("request tenant was not preserved: %q", request.TenantID)
	}
}

type scriptedSessionRuntime struct {
	events   []SessionEnvelope
	response RuntimeResponse
	request  RuntimeRequest
}

func (r *scriptedSessionRuntime) RunSession(ctx context.Context, request RuntimeRequest, observer SessionEventObserver) (RuntimeResponse, error) {
	r.request = request
	for _, event := range r.events {
		if observer != nil {
			if err := observer(ctx, event); err != nil {
				return RuntimeResponse{}, err
			}
		}
	}
	return r.response, nil
}

func (*scriptedSessionRuntime) CancelSession(context.Context, AgentRunID, string) error { return nil }
func (*scriptedSessionRuntime) SteerSession(context.Context, AgentRunID, string) error  { return nil }
func (*scriptedSessionRuntime) PrewarmSession(context.Context, RuntimeRequest) error    { return nil }
func (*scriptedSessionRuntime) ReleaseRuntimeWorkspace(context.Context, string) error   { return nil }
func (*scriptedSessionRuntime) Capabilities(context.Context) Capabilities {
	return Capabilities{SupportedCommands: CommandSet{Send: true}, Signals: SignalSet{TerminalResult: true, ToolLifecycle: true}, SupportsResume: true, Transport: TransportEvidence{Provider: "test", Protocol: "canonical", Version: time.Now().UTC().Format(time.RFC3339)}}
}
