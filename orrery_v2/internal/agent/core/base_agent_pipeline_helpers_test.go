package core

import (
	"context"
	"testing"
	"time"

	missioncore "orrery_v2/internal/mission/core"
)

type fakePipelineRuntime struct {
	testSessionRuntimeSupport
	response RuntimeResponse
	err      error
}

type capturingRuntimeRequest struct {
	testSessionRuntimeSupport
	response RuntimeResponse
	request  RuntimeRequest
}

func (r *capturingRuntimeRequest) RunSession(ctx context.Context, req RuntimeRequest, observer SessionEventObserver) (RuntimeResponse, error) {
	r.request = req
	observeTestRuntimeStart(ctx, observer)
	return r.response, nil
}

func (r fakePipelineRuntime) RunSession(ctx context.Context, _ RuntimeRequest, observer SessionEventObserver) (RuntimeResponse, error) {
	observeTestRuntimeStart(ctx, observer)
	return r.response, r.err
}

type observingRuntime struct {
	testSessionRuntimeSupport
	response                    RuntimeResponse
	finished                    bool
	finishedBeforeStartedSignal bool
}

func (r *observingRuntime) RunSession(ctx context.Context, _ RuntimeRequest, observer SessionEventObserver) (RuntimeResponse, error) {
	observeTestRuntimeStart(ctx, observer)
	r.finished = true
	return r.response, nil
}

type testSessionRuntimeSupport struct{}

func (testSessionRuntimeSupport) CancelSession(context.Context, AgentRunID, string) error { return nil }
func (testSessionRuntimeSupport) SteerSession(context.Context, AgentRunID, string) error  { return nil }
func (testSessionRuntimeSupport) PrewarmSession(context.Context, RuntimeRequest) error    { return nil }
func (testSessionRuntimeSupport) ReleaseRuntimeWorkspace(context.Context, string) error   { return nil }
func (testSessionRuntimeSupport) Capabilities(context.Context) Capabilities {
	return Capabilities{SupportsResume: true, Signals: SignalSet{ToolLifecycle: true, TerminalResult: true}}
}

func observeTestRuntimeStart(ctx context.Context, observer SessionEventObserver) {
	if observer == nil {
		return
	}
	_ = observer(ctx, SessionEnvelope{TurnGeneration: 1, Event: SessionEvent{Kind: EventTurnStarted, TurnStarted: &TurnStartedEvent{}}})
}

type capturingDelivery struct {
	committed *AgentResult
	options   CommitOptions
}

type observingSignalSink struct {
	recordingSignalSink
	runtime *observingRuntime
}

func (s *observingSignalSink) EmitSignal(ctx context.Context, signal Signal) error {
	if signal.Name == SignalRuntimeStarted && s.runtime.finished {
		s.runtime.finishedBeforeStartedSignal = true
	}
	return s.recordingSignalSink.EmitSignal(ctx, signal)
}

func (d *capturingDelivery) SubmitAgentResult(_ context.Context, result AgentResult, opts CommitOptions) error {
	copied := result
	d.committed = &copied
	d.options = opts
	return nil
}

type fixedClock struct {
	t time.Time
}

func (c fixedClock) Now() time.Time { return c.t }

type recordingSignalSink struct {
	signals []Signal
}

func (s *recordingSignalSink) EmitSignal(_ context.Context, signal Signal) error {
	s.signals = append(s.signals, signal)
	return nil
}

func (s *recordingSignalSink) Names() []SignalName {
	out := make([]SignalName, 0, len(s.signals))
	for _, signal := range s.signals {
		out = append(out, signal.Name)
	}
	return out
}

type staticSummaryGenerator struct {
	summary Summary
	err     error
}

func (g staticSummaryGenerator) GenerateSummary(AgentInput, RuntimeResponse, AgentResult) (Summary, error) {
	return g.summary, g.err
}

type recordingTraceRecorder struct {
	ref   string
	calls int
}

func (r *recordingTraceRecorder) RecordTrace(context.Context, AgentInput, RuntimeResponse) (string, error) {
	r.calls++
	return r.ref, nil
}

type recordingNotifier struct {
	calls int
}

func (n *recordingNotifier) Notify(context.Context, AgentInput, AgentResult) error {
	n.calls++
	return nil
}

func assertSignalPresent(t *testing.T, names []SignalName, want SignalName) {
	t.Helper()
	for _, name := range names {
		if name == want {
			return
		}
	}
	t.Fatalf("missing signal %s in %#v", want, names)
}

func validAgentInput() AgentInput {
	return AgentInput{
		RunID: "run-1",
		ContextPack: ContextPack{
			ID:                  "context-1",
			MissionID:           missioncore.MissionID("mission-1"),
			DiscussionGroupID:   "discussion-1",
			AgentID:             "agent-1",
			TaskID:              missioncore.TaskID("task-1"),
			MissionFinalGoal:    "Ship mission control",
			DiscussionGroupGoal: "Coordinate implementation",
			WorkerPersonalGoal:  "Implement runtime",
			TaskContractRef:     TaskContractRef{ID: "task-1"},
			SourceRefs: []ContextSourceRef{{
				Kind:      ContextSourceKindTaskContract,
				Ref:       ContextSourceTypedRef{TaskContractRef: &TaskContractRef{ID: "task-1"}},
				Priority:  SourcePriorityContract,
				Immutable: true,
			}},
			OverridePolicy: OverridePolicyPreserveProtected,
		},
		Task: TaskInput{
			ID:                 "task-1",
			MissionID:          "mission-1",
			Goal:               "Implement runtime",
			WorkerPersonalGoal: "Implement runtime",
			ExpectedDeliverables: []TaskDeliverableInput{{
				ID:    "deliverable-1",
				Title: "Patch",
			}},
			AcceptanceCriteria: []TaskAcceptanceInput{{
				ID:          "acceptance-1",
				Description: "Tests pass",
			}},
			EvidenceRequirements: []TaskEvidenceInput{{
				ID:          "evidence-1",
				Description: "Test log",
				Kind:        string(EvidenceKindTestLog),
			}},
		},
	}
}
