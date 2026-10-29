package improvement

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func TestRuntimeEvidenceTraceCapturesBeforeDispatchAndAlwaysFinalizes(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	evidence := NewRuntimeEvidenceStore(store.core)
	req, e := runtimeEvidenceFixture(t, "ignored")
	base := &runtimeEvidenceTestTrace{}
	recorder := RuntimeEvidenceRecorder{Recorder: base, Evidence: evidence, Scope: func(agentcore.RuntimeRequest) (RuntimeEvidenceScope, error) { return e.Scope, nil }, Now: func() time.Time { return e.CapturedAt }}
	session, err := recorder.BeginTrace(ctx, agentcore.AgentInput{})
	if err != nil {
		t.Fatal(err)
	}
	wrapped := session.(*runtimeEvidenceTraceSession)
	base.beforeRequest = func() {
		got, err := evidence.GetRequest(ctx, e.Scope.TenantID, wrapped.attempt)
		if err != nil || got.ControlsSHA256 != e.ControlsSHA256 {
			t.Fatalf("request delegated before capture: %+v %v", got, err)
		}
	}
	if err := session.RecordRequest(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := session.RecordLifecycle(ctx, agentcore.TraceLifecycleEvent{}); err != nil {
		t.Fatal(err)
	}
	if err := session.RecordRuntime(ctx, agentcore.SessionEnvelope{}); err != nil {
		t.Fatal(err)
	}
	base.finishErr = errors.New("trace finalization failed")
	if err := session.Finish(ctx, runtimeEvidenceTerminal()); !errors.Is(err, base.finishErr) {
		t.Fatal(err)
	}
	if terminal, err := evidence.GetTerminal(ctx, e.Scope.TenantID, wrapped.attempt); err != nil || terminal.TraceFinalization != "failed" {
		t.Fatalf("trace failure was not preserved: %+v %v", terminal, err)
	}
	if base.finishes != 1 || base.requests != 1 || base.events != 1 || base.lifecycle != 1 {
		t.Fatalf("trace delegation: %+v", base)
	}
	second, err := recorder.BeginTrace(ctx, agentcore.AgentInput{})
	if err != nil || second.(*runtimeEvidenceTraceSession).attempt == wrapped.attempt {
		t.Fatalf("attempt reused: %v", err)
	}
	// RSI failure must still finalize the base trace and be visible to BaseAgent.
	if err := second.RecordRequest(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := store.core.Close(); err != nil {
		t.Fatal(err)
	}
	base.finishErr = nil
	if err := second.Finish(ctx, runtimeEvidenceTerminal()); err == nil {
		t.Fatal("evidence persistence failure hidden")
	}
	if base.finishes != 2 {
		t.Fatal("RSI failure prevented trace finalization")
	}
}

func TestRuntimeEvidenceTraceRejectsInvalidOrChangedRequest(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	req, e := runtimeEvidenceFixture(t, "ignored")
	base := &runtimeEvidenceTestTrace{}
	recorder := RuntimeEvidenceRecorder{Recorder: base, Evidence: NewRuntimeEvidenceStore(store.core), Scope: func(agentcore.RuntimeRequest) (RuntimeEvidenceScope, error) { return e.Scope, nil }, Now: func() time.Time { return e.CapturedAt }}
	session, err := recorder.BeginTrace(ctx, agentcore.AgentInput{})
	if err != nil {
		t.Fatal(err)
	}
	bad := req
	bad.AgentID = ""
	if err := session.RecordRequest(ctx, bad); !errors.Is(err, ErrInvalid) || base.requests != 0 {
		t.Fatalf("invalid request delegated: %v", err)
	}
	if err := session.RecordRequest(ctx, req); err != nil {
		t.Fatal(err)
	}
	req.RuntimeOptions.MaxTurns++
	if err := session.RecordRequest(ctx, req); !errors.Is(err, ErrConflict) || base.requests != 1 {
		t.Fatalf("changed request delegated: %v", err)
	}
}

type runtimeEvidenceTestTrace struct {
	requests, finishes, events, lifecycle int
	finishErr                             error
	beforeRequest                         func()
}

func (s *runtimeEvidenceTestTrace) BeginTrace(context.Context, agentcore.AgentInput) (agentcore.DurableTraceSession, error) {
	return s, nil
}
func (s *runtimeEvidenceTestTrace) RecordTrace(context.Context, agentcore.AgentInput, agentcore.RuntimeResponse) (string, error) {
	return "trace://run-a", nil
}
func (s *runtimeEvidenceTestTrace) Ref() string { return "trace://run-a" }
func (s *runtimeEvidenceTestTrace) RecordLifecycle(context.Context, agentcore.TraceLifecycleEvent) error {
	s.lifecycle++
	return nil
}
func (s *runtimeEvidenceTestTrace) RecordRequest(context.Context, agentcore.RuntimeRequest) error {
	if s.beforeRequest != nil {
		s.beforeRequest()
	}
	s.requests++
	return nil
}
func (s *runtimeEvidenceTestTrace) RecordRuntime(context.Context, agentcore.SessionEnvelope) error {
	s.events++
	return nil
}
func (s *runtimeEvidenceTestTrace) Finish(context.Context, agentcore.TraceTerminal) error {
	s.finishes++
	return s.finishErr
}
