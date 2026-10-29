package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestBaseAgentBoundsTerminalDiaryFlushWithoutChangingBusinessResult(t *testing.T) {
	flush := &blockingDiaryFlush{}
	runtime := &capturingDiaryRuntime{response: RuntimeResponse{
		Status:           RuntimeStatusCompleted,
		Messages:         []RuntimeMessage{{Type: RuntimeMessageText, Content: "Completed work.", Status: RuntimeMessageStatusFinal}},
		ProviderMetadata: map[string]string{"session_id": "session-1"},
	}}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1", OrgIdentity: AgentOrgIdentity{OrganizationRef: OrganizationRef{ID: "tenant-a"}}},
		Runtime:  runtime, DiaryFlush: flush, DiaryFlushTimeout: 20 * time.Millisecond,
		Delivery: &capturingDelivery{}, Trace: NoopTraceRecorder{}, Notifier: NoopNotifier{},
	})
	input := validAgentInput()
	input.Task.AcceptanceCriteria = nil
	input.Task.EvidenceRequirements = nil
	input.Task.ExpectedDeliverables = nil
	started := time.Now()
	result, err := agent.Handle(context.Background(), input)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if result.Summary.Text != "Completed work." || time.Since(started) > time.Second {
		t.Fatalf("result=%+v elapsed=%s", result, time.Since(started))
	}
	if !flush.deadlineObserved {
		t.Fatal("terminal diary flush did not receive a bounded context")
	}
}

func TestBaseAgentRunsTerminalDiaryFlushBeforeCompletedResultCommit(t *testing.T) {
	delivery := &capturingDelivery{}
	flush := &recordingDiaryFlush{delivery: delivery, outcome: DiaryFlushOutcome{Status: DiaryFlushFailed, Reason: "maintenance_failed", Error: "model unavailable"}}
	runtime := &capturingDiaryRuntime{response: RuntimeResponse{
		Status:           RuntimeStatusCompleted,
		Messages:         []RuntimeMessage{{Type: RuntimeMessageText, Content: "Completed work.", Status: RuntimeMessageStatusFinal}},
		ProviderMetadata: map[string]string{"session_id": "session-1"},
	}}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1", OrgIdentity: AgentOrgIdentity{OrganizationRef: OrganizationRef{ID: "tenant-a"}}},
		Runtime:  runtime, DiaryFlush: flush, Delivery: delivery, Trace: NoopTraceRecorder{}, Notifier: NoopNotifier{},
	})
	input := validAgentInput()
	input.Task.AcceptanceCriteria = nil
	input.Task.EvidenceRequirements = nil
	input.Task.ExpectedDeliverables = nil

	result, err := agent.Handle(context.Background(), input)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if result.Summary.Text != "Completed work." || delivery.committed == nil {
		t.Fatalf("result=%+v delivery=%+v", result, delivery.committed)
	}
	if flush.calls != 1 || !flush.calledBeforeCommit {
		t.Fatalf("flush calls=%d beforeCommit=%t", flush.calls, flush.calledBeforeCommit)
	}
	if runtime.request.TenantID != "tenant-a" || flush.request.Request.TenantID != "tenant-a" {
		t.Fatalf("tenant propagation runtime=%q flush=%q", runtime.request.TenantID, flush.request.Request.TenantID)
	}
	if !flush.prepared {
		t.Fatalf("main runtime request was not prepared for provider-native flush")
	}
}

func TestBaseAgentRunsTerminalDiaryFlushWhenBeforeRuntimeFinishSignalFails(t *testing.T) {
	signalErr := errors.New("injected before-runtime-finish signal failure")
	flush := &recordingDiaryFlush{outcome: DiaryFlushOutcome{Status: DiaryFlushNoContent}}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1", OrgIdentity: AgentOrgIdentity{OrganizationRef: OrganizationRef{ID: "tenant-a"}}},
		Runtime: &capturingDiaryRuntime{response: RuntimeResponse{
			Status:           RuntimeStatusCompleted,
			Messages:         []RuntimeMessage{{Type: RuntimeMessageText, Content: "Completed work.", Status: RuntimeMessageStatusFinal}},
			ProviderMetadata: map[string]string{"session_id": "session-1"},
		}},
		Signals:    failOnSignalSink{name: SignalBeforeRuntimeFinish, err: signalErr},
		DiaryFlush: flush,
		Trace:      NoopTraceRecorder{},
		Notifier:   NoopNotifier{},
	})
	_, err := agent.Handle(context.Background(), validAgentInput())
	if !errors.Is(err, signalErr) {
		t.Fatalf("Handle error = %v, want injected signal failure", err)
	}
	if flush.calls != 1 {
		t.Fatalf("terminal diary flush calls = %d, want 1", flush.calls)
	}
}

func TestBaseAgentRunsTerminalDiaryFlushForAllRuntimeTerminalStatuses(t *testing.T) {
	for _, status := range []RuntimeStatus{RuntimeStatusFailed, RuntimeStatusBlocked, RuntimeStatusCancelled, RuntimeStatusTimeout} {
		t.Run(string(status), func(t *testing.T) {
			flush := &recordingDiaryFlush{outcome: DiaryFlushOutcome{Status: DiaryFlushNoContent}}
			agent := NewBaseAgent(BaseAgentConfig{
				Identity:   AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
				Profile:    AgentProfile{ID: "profile-1", OrgIdentity: AgentOrgIdentity{OrganizationRef: OrganizationRef{ID: "tenant-a"}}},
				Runtime:    &capturingDiaryRuntime{response: RuntimeResponse{Status: status, Error: RuntimeFailure{Message: string(status)}, ProviderMetadata: map[string]string{"session_id": "session-1"}}},
				DiaryFlush: flush, Trace: NoopTraceRecorder{}, Notifier: NoopNotifier{},
			})
			_, err := agent.Handle(context.Background(), validAgentInput())
			var runtimeErr RuntimePipelineError
			if !errors.As(err, &runtimeErr) || runtimeErr.Status != status {
				t.Fatalf("Handle error = %v, want runtime status %s", err, status)
			}
			if flush.calls != 1 {
				t.Fatalf("flush calls = %d, want 1", flush.calls)
			}
		})
	}
}

func TestBaseAgentRunsTerminalDiaryFlushAfterStreamedRuntimeError(t *testing.T) {
	flush := &recordingDiaryFlush{outcome: DiaryFlushOutcome{Status: DiaryFlushNoContent}}
	runtime := &streamErrorDiaryRuntime{}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1", OrgIdentity: AgentOrgIdentity{OrganizationRef: OrganizationRef{ID: "tenant-a"}}},
		Runtime:  runtime, DiaryFlush: flush, Trace: NoopTraceRecorder{}, Notifier: NoopNotifier{},
	})
	_, err := agent.Handle(context.Background(), validAgentInput())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Handle error = %v, want context.Canceled", err)
	}
	if flush.calls != 1 || flush.request.Response.ProviderMetadata["session_id"] != "session-1" {
		t.Fatalf("flush calls=%d request=%+v", flush.calls, flush.request)
	}
}

func TestBaseAgentRunsScheduledTerminalDiaryFlushAfterSessionlessTimeout(t *testing.T) {
	flush := &recordingDiaryFlush{outcome: DiaryFlushOutcome{Status: DiaryFlushWritten}}
	runtime := &sessionlessErrorDiaryRuntime{err: context.DeadlineExceeded}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1", OrgIdentity: AgentOrgIdentity{OrganizationRef: OrganizationRef{ID: "tenant-a"}}},
		Runtime:  runtime, DiaryFlush: flush, Trace: NoopTraceRecorder{}, Notifier: NoopNotifier{},
	})
	input := validAgentInput()
	input.RuntimeOptions.ProviderSpecific = map[string]string{
		"run_source":        "scheduler_agent_task",
		"scheduled_task_id": "scheduled-task-1",
		"scheduled_run_id":  "scheduled-run-1",
	}

	_, err := agent.Handle(context.Background(), input)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Handle error = %v, want context deadline exceeded", err)
	}
	if flush.calls != 1 {
		t.Fatalf("flush calls = %d, want 1", flush.calls)
	}
	response := flush.request.Response
	if response.RunID != input.RunID || response.Status != RuntimeStatusTimeout || response.Error.Message != context.DeadlineExceeded.Error() ||
		strings.TrimSpace(response.ProviderMetadata["session_id"]) != "" {
		t.Fatalf("normalized sessionless terminal response = %+v", response)
	}
}

type recordingDiaryFlush struct {
	delivery           *capturingDelivery
	prepared           bool
	calls              int
	calledBeforeCommit bool
	request            DiaryFlushRequest
	outcome            DiaryFlushOutcome
}

type blockingDiaryFlush struct {
	deadlineObserved bool
}

type failOnSignalSink struct {
	name SignalName
	err  error
}

func (s failOnSignalSink) EmitSignal(_ context.Context, signal Signal) error {
	if signal.Name == s.name {
		return s.err
	}
	return nil
}

func (f *blockingDiaryFlush) PrepareRuntimeRequest(_ context.Context, req RuntimeRequest) RuntimeRequest {
	return req
}

func (f *blockingDiaryFlush) FlushTerminal(ctx context.Context, _ DiaryFlushRequest) DiaryFlushOutcome {
	_, f.deadlineObserved = ctx.Deadline()
	<-ctx.Done()
	return DiaryFlushOutcome{Status: DiaryFlushFailed, Reason: "timeout", Error: ctx.Err().Error()}
}

func (f *recordingDiaryFlush) PrepareRuntimeRequest(_ context.Context, req RuntimeRequest) RuntimeRequest {
	f.prepared = true
	if req.RuntimeOptions.ProviderSpecific == nil {
		req.RuntimeOptions.ProviderSpecific = map[string]string{}
	}
	req.RuntimeOptions.ProviderSpecific["diary_test_prepared"] = "true"
	return req
}

func (f *recordingDiaryFlush) FlushTerminal(_ context.Context, req DiaryFlushRequest) DiaryFlushOutcome {
	f.calls++
	f.request = req
	f.calledBeforeCommit = f.delivery == nil || f.delivery.committed == nil
	return f.outcome
}

type capturingDiaryRuntime struct {
	testSessionRuntimeSupport
	request  RuntimeRequest
	response RuntimeResponse
}

type streamErrorDiaryRuntime struct {
	testSessionRuntimeSupport
}

type sessionlessErrorDiaryRuntime struct {
	testSessionRuntimeSupport
	err error
}

func (r *streamErrorDiaryRuntime) RunSession(ctx context.Context, request RuntimeRequest, observer SessionEventObserver) (RuntimeResponse, error) {
	observeTestRuntimeStart(ctx, observer)
	return RuntimeResponse{RunID: request.RunID, Status: RuntimeStatusCancelled, ProviderMetadata: map[string]string{"session_id": "session-1"}}, context.Canceled
}

func (r *sessionlessErrorDiaryRuntime) RunSession(context.Context, RuntimeRequest, SessionEventObserver) (RuntimeResponse, error) {
	return RuntimeResponse{}, r.err
}

func (r *capturingDiaryRuntime) RunSession(ctx context.Context, req RuntimeRequest, observer SessionEventObserver) (RuntimeResponse, error) {
	r.request = req
	observeTestRuntimeStart(ctx, observer)
	return r.response, nil
}
