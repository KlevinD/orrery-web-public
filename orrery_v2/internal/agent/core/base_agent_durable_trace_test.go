package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBaseAgentDurableTraceCoversRuntimeErrorsAndTerminals(t *testing.T) {
	for _, test := range []struct {
		name       string
		response   RuntimeResponse
		runtimeErr error
		wantStatus RuntimeStatus
	}{
		{name: "provider error", runtimeErr: errors.New("provider unavailable"), wantStatus: RuntimeStatusFailed},
		{name: "timeout", response: RuntimeResponse{Status: RuntimeStatusTimeout}, wantStatus: RuntimeStatusTimeout},
		{name: "cancelled", response: RuntimeResponse{Status: RuntimeStatusCancelled}, wantStatus: RuntimeStatusCancelled},
		{name: "blocked", response: RuntimeResponse{Status: RuntimeStatusBlocked}, wantStatus: RuntimeStatusBlocked},
		{name: "completed", response: durableTraceRuntimeResponse(), wantStatus: RuntimeStatusCompleted},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDurableTraceAgentFixture()
			fixture.runtime.response = test.response
			fixture.runtime.err = test.runtimeErr
			result, _ := fixture.agent.Handle(context.Background(), fixture.input)
			if fixture.recorder.beginCalls != 1 || fixture.recorder.session.finishCalls != 1 {
				t.Fatalf("begin=%d finish=%d", fixture.recorder.beginCalls, fixture.recorder.session.finishCalls)
			}
			if got := fixture.recorder.session.terminal.Status; got != test.wantStatus {
				t.Fatalf("terminal status=%s want=%s", got, test.wantStatus)
			}
			if result.InternalTraceRef != fixture.recorder.session.Ref() {
				t.Fatalf("trace ref=%q", result.InternalTraceRef)
			}
		})
	}
}

func TestBaseAgentTraceStartFailurePreventsRuntimeDispatch(t *testing.T) {
	fixture := newDurableTraceAgentFixture()
	fixture.recorder.beginErr = errors.New("trace storage unavailable")
	if _, err := fixture.agent.Handle(context.Background(), fixture.input); !errors.Is(err, fixture.recorder.beginErr) {
		t.Fatalf("error = %v", err)
	}
	if fixture.runtime.calls != 0 {
		t.Fatalf("runtime calls=%d", fixture.runtime.calls)
	}
}

func TestBaseAgentTraceFinishFailurePreventsSuccess(t *testing.T) {
	fixture := newDurableTraceAgentFixture()
	fixture.runtime.response = durableTraceRuntimeResponse()
	fixture.recorder.session.finishErr = errors.New("trace terminal unavailable")
	if _, err := fixture.agent.Handle(context.Background(), fixture.input); !errors.Is(err, fixture.recorder.session.finishErr) {
		t.Fatalf("error = %v", err)
	} else {
		var postCommitErr PostCommitPipelineError
		if !errors.As(err, &postCommitErr) || postCommitErr.Stage != "durable_trace_finalize" {
			t.Fatalf("error=%T %[1]v, want durable trace post-commit error", err)
		}
	}
}

func TestBaseAgentDurableTraceReceivesRequestLifecycleAndRuntimeEvents(t *testing.T) {
	fixture := newDurableTraceAgentFixture()
	fixture.runtime.response = durableTraceRuntimeResponse()
	fixture.runtime.events = []SessionEnvelope{
		validTraceToolCallEnvelope("safe"),
		validTraceToolResultEnvelope("complete"),
	}
	if _, err := fixture.agent.Handle(context.Background(), fixture.input); err != nil {
		t.Fatal(err)
	}
	if fixture.recorder.session.requestCalls != 1 {
		t.Fatalf("request calls=%d", fixture.recorder.session.requestCalls)
	}
	if len(fixture.recorder.session.lifecycle) == 0 || len(fixture.recorder.session.runtime) != 2 {
		t.Fatalf("lifecycle=%d runtime=%d", len(fixture.recorder.session.lifecycle), len(fixture.recorder.session.runtime))
	}
	if fixture.recorder.legacyCalls != 0 {
		t.Fatalf("legacy trace calls=%d", fixture.recorder.legacyCalls)
	}
}

func TestBaseAgentDurableTraceFinalizesAfterCallerCancellation(t *testing.T) {
	fixture := newDurableTraceAgentFixture()
	fixture.runtime.response = durableTraceRuntimeResponse()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture.agent.Handle(ctx, fixture.input); err != nil {
		t.Fatal(err)
	}
	if fixture.recorder.session.finishContextErr != nil {
		t.Fatalf("finish context remained cancelled: %v", fixture.recorder.session.finishContextErr)
	}
}

func TestBaseAgentDurableTraceClassifiesEventPersistenceFailure(t *testing.T) {
	fixture := newDurableTraceAgentFixture()
	fixture.runtime.events = []SessionEnvelope{validTraceToolCallEnvelope("safe")}
	fixture.recorder.session.runtimeErr = errors.New("event store unavailable")
	if _, err := fixture.agent.Handle(context.Background(), fixture.input); !errors.Is(err, fixture.recorder.session.runtimeErr) {
		t.Fatalf("error = %v", err)
	}
	if got := fixture.recorder.session.terminal.ErrorCode; got != "trace_persistence_failed" {
		t.Fatalf("terminal error code=%q", got)
	}
}

func TestBaseAgentDurableTraceKeepsLifecycleWhenRuntimeUsesDetachedEventContext(t *testing.T) {
	fixture := newDurableTraceAgentFixture()
	fixture.runtime.response = durableTraceRuntimeResponse()
	fixture.runtime.detachedEventContext = true
	fixture.runtime.events = []SessionEnvelope{validTraceEnvelope(SessionEvent{
		Kind:        EventTurnStarted,
		TurnStarted: &TurnStartedEvent{Epoch: 1},
	})}
	if _, err := fixture.agent.Handle(context.Background(), fixture.input); err != nil {
		t.Fatal(err)
	}
	for _, event := range fixture.recorder.session.lifecycle {
		if event.Stage == string(SignalRuntimeStarted) {
			return
		}
	}
	t.Fatalf("runtime-start lifecycle missing: %+v", fixture.recorder.session.lifecycle)
}

type durableTraceAgentFixture struct {
	agent    *BaseAgent
	input    AgentInput
	runtime  *durableTraceRuntime
	recorder *capturingDurableTraceRecorder
}

func newDurableTraceAgentFixture() durableTraceAgentFixture {
	runtime := &durableTraceRuntime{}
	recorder := &capturingDurableTraceRecorder{session: &capturingDurableTraceSession{ref: "store://trace-v2/run-1"}}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1", RuntimeSpec: RuntimeSpec{Kind: RuntimeKindCodex}},
		Runtime:  runtime,
		Delivery: &capturingDelivery{},
		Trace:    recorder,
		Notifier: &recordingNotifier{},
		Validator: ResultValidatorFunc(func(AgentInput, AgentResult) error {
			return nil
		}),
	})
	return durableTraceAgentFixture{agent: agent, input: validAgentInput(), runtime: runtime, recorder: recorder}
}

func durableTraceRuntimeResponse() RuntimeResponse {
	return RuntimeResponse{
		RunID:  "run-1",
		Status: RuntimeStatusCompleted,
		Messages: []RuntimeMessage{{
			Type: RuntimeMessageText, Content: "completed",
		}},
	}
}

type durableTraceRuntime struct {
	testSessionRuntimeSupport
	response             RuntimeResponse
	err                  error
	events               []SessionEnvelope
	calls                int
	detachedEventContext bool
}

func (r *durableTraceRuntime) RunSession(ctx context.Context, _ RuntimeRequest, observer SessionEventObserver) (RuntimeResponse, error) {
	r.calls++
	for _, event := range r.events {
		if observer != nil {
			eventCtx := ctx
			if r.detachedEventContext {
				eventCtx = context.Background()
			}
			if err := observer(eventCtx, event); err != nil {
				return RuntimeResponse{}, err
			}
		}
	}
	return r.response, r.err
}

type capturingDurableTraceRecorder struct {
	session     *capturingDurableTraceSession
	beginCalls  int
	legacyCalls int
	beginErr    error
}

func (r *capturingDurableTraceRecorder) BeginTrace(context.Context, AgentInput) (DurableTraceSession, error) {
	r.beginCalls++
	return r.session, r.beginErr
}

func (r *capturingDurableTraceRecorder) RecordTrace(context.Context, AgentInput, RuntimeResponse) (string, error) {
	r.legacyCalls++
	return "legacy://trace", nil
}

type capturingDurableTraceSession struct {
	ref              string
	requestCalls     int
	lifecycle        []TraceLifecycleEvent
	runtime          []SessionEnvelope
	terminal         TraceTerminal
	finishCalls      int
	finishErr        error
	finishContextErr error
	runtimeErr       error
}

func (s *capturingDurableTraceSession) Ref() string              { return s.ref }
func (s *capturingDurableTraceSession) RuntimeAttemptID() string { return "host-attempt-a" }

func TestBaseAgentUsesHostAttemptBeforeFormalCommit(t *testing.T) {
	fixture := newDurableTraceAgentFixture()
	fixture.runtime.response = durableTraceRuntimeResponse()
	fixture.agent.normalizer = ResultNormalizerFunc(func(input AgentInput, response RuntimeResponse, now time.Time) (AgentResult, error) {
		r, err := (DefaultResultNormalizer{}).Normalize(input, response, now)
		r.RuntimeAttemptID = "model-forged-attempt"
		return r, err
	})
	fixture.agent.delivery = attemptCheckingCommitter{t: t}
	result, err := fixture.agent.Handle(context.Background(), fixture.input)
	if err != nil || result.RuntimeAttemptID != "host-attempt-a" {
		t.Fatal("host attempt lost", err)
	}
}

type attemptCheckingCommitter struct{ t *testing.T }

func (c attemptCheckingCommitter) SubmitAgentResult(_ context.Context, r AgentResult, _ CommitOptions) error {
	if r.RuntimeAttemptID != "host-attempt-a" {
		c.t.Fatal("model-supplied attempt reached formal commit")
	}
	return nil
}

func (s *capturingDurableTraceSession) RecordLifecycle(_ context.Context, event TraceLifecycleEvent) error {
	s.lifecycle = append(s.lifecycle, event)
	return nil
}

func (s *capturingDurableTraceSession) RecordRequest(context.Context, RuntimeRequest) error {
	s.requestCalls++
	return nil
}

func (s *capturingDurableTraceSession) RecordRuntime(_ context.Context, event SessionEnvelope) error {
	s.runtime = append(s.runtime, event)
	return s.runtimeErr
}

func (s *capturingDurableTraceSession) Finish(ctx context.Context, terminal TraceTerminal) error {
	s.finishCalls++
	s.terminal = terminal
	s.finishContextErr = ctx.Err()
	return s.finishErr
}
