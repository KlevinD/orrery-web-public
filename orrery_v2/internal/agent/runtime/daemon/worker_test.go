package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
)

func TestWorkerClaimsRunExecutesAdapterAndSubmitsResponse(t *testing.T) {
	store := newMemoryRunStore()
	err := store.Enqueue(context.Background(), AgentRun{
		RunID:           "run-1",
		AgentID:         "agent-1",
		AgentProfileID:  "profile-1",
		AgentInstanceID: "agent-1:runtime",
		Request: agentcore.RuntimeRequest{
			RunID: "run-1",
		},
	})
	if err != nil {
		t.Fatalf("enqueue run: %v", err)
	}
	worker := NewWorker(WorkerOptions{
		WorkerID: "worker-1",
		Store:    store,
		Runtime:  fakeAdapter{},
	})

	claimed, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if !claimed {
		t.Fatalf("expected a claimed run")
	}
	record, ok := store.get("run-1")
	if !ok {
		t.Fatalf("run record missing")
	}
	if record.Status != RunStatusCompleted {
		t.Fatalf("status = %q", record.Status)
	}
	if record.Response.Status != agentcore.RuntimeStatusCompleted {
		t.Fatalf("response = %#v", record.Response)
	}
}

func TestWorkerClaimsRunThroughManagedBaseAgent(t *testing.T) {
	store := newMemoryRunStore()
	err := store.Enqueue(context.Background(), AgentRun{
		RunID:           "run-managed",
		AgentID:         "agent-1",
		AgentProfileID:  "profile-1",
		AgentInstanceID: "agent-1:runtime",
		Request: agentcore.RuntimeRequest{
			RunID:     "run-managed",
			AgentID:   "agent-1",
			MissionID: "mission-1",
			TaskID:    "task-1",
			ContextPack: agentcore.ContextPack{
				ID:                 "ctx-1",
				AgentID:            "agent-1",
				TaskID:             "task-1",
				MissionID:          "mission-1",
				WorkerPersonalGoal: "finish audited work",
			},
		},
	})
	if err != nil {
		t.Fatalf("enqueue run: %v", err)
	}
	signals := &workerSignalSink{}
	runtime := fakeAdapter{}
	agent := agentcore.NewBaseAgent(agentcore.BaseAgentConfig{
		Identity: agentcore.AgentIdentity{
			AgentID:        "agent-1",
			AgentProfileID: "profile-1",
			InstanceRef:    agentcore.AgentInstanceRef{ID: "agent-1:runtime"},
		},
		Profile: agentcore.AgentProfile{
			ID:          "profile-1",
			RealName:    "沈执行",
			DisplayName: "沈执行",
			AgentKind:   agentcore.AgentKindAI,
			Persona:     agentcore.AgentPersona{ResponsibilityBoundary: "负责执行托管运行并提交审计证据。"},
			BaseAgent: agentcore.BaseAgentSpec{
				InheritsBaseAgent: true,
				AuditRequired:     true,
				SystemPrompt:      "你是托管 Agent。",
				LLMAPIKeyRef:      "secret://system/llm-api-key",
				SkillScope:        "agent-runtime-daemon",
				ToolScope:         "agent-runtime-daemon",
				MCPScope:          "agent-runtime-daemon",
			},
			RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex},
		},
		Runtime:   runtime,
		Signals:   signals,
		Delivery:  noopRunCommitter{},
		Trace:     agentcore.NoopTraceRecorder{},
		Notifier:  agentcore.NoopNotifier{},
		Validator: agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error { return nil }),
		Normalizer: agentcore.ResultNormalizerFunc(func(input agentcore.AgentInput, response agentcore.RuntimeResponse, now time.Time) (agentcore.AgentResult, error) {
			return agentcore.AgentResult{
				ID:        "result-run-managed",
				MissionID: "mission-1",
				TaskID:    "task-1",
				AgentID:   "agent-1",
				RunID:     input.RunID,
				Summary:   agentcore.Summary{Text: "audited"},
				CreatedAt: now,
			}, nil
		}),
	})
	worker := NewWorker(WorkerOptions{
		WorkerID: "worker-1",
		Store:    store,
		Agent:    agent,
	})

	claimed, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if !claimed {
		t.Fatalf("expected a claimed run")
	}
	record, ok := store.get("run-managed")
	if !ok {
		t.Fatalf("run record missing")
	}
	if record.Response.Status != agentcore.RuntimeStatusCompleted {
		t.Fatalf("response = %#v", record.Response)
	}
	assertWorkerSignalSequence(t, signals.names(), []agentcore.SignalName{
		agentcore.SignalBeforeTaskReceived,
		agentcore.SignalBeforeRuntimeStart,
		agentcore.SignalRuntimeStarted,
		agentcore.SignalBeforeResultCommit,
		agentcore.SignalAfterResultCommit,
	})
}

func TestWorkerPublishesRuntimeLifecycleEventsForCompletedRun(t *testing.T) {
	store := newMemoryRunStore()
	err := store.Enqueue(context.Background(), AgentRun{
		RunID:           "run-lifecycle",
		AgentID:         "agent-1",
		AgentProfileID:  "profile-1",
		AgentInstanceID: "agent-1:runtime",
		RuntimeID:       "codex",
		MissionID:       "mission-1",
		TaskID:          "task-1",
		Request: agentcore.RuntimeRequest{
			RunID:     "run-lifecycle",
			AgentID:   "agent-1",
			MissionID: "mission-1",
			TaskID:    "task-1",
		},
	})
	if err != nil {
		t.Fatalf("enqueue run: %v", err)
	}
	sink := &capturingRuntimeLifecycleSink{}
	worker := NewWorker(WorkerOptions{
		WorkerID:  "worker-1",
		Store:     store,
		Runtime:   fakeAdapter{},
		Lifecycle: sink,
	})

	claimed, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if !claimed {
		t.Fatalf("expected a claimed run")
	}
	if len(sink.events) != 2 {
		t.Fatalf("lifecycle events = %+v, want started and completed", sink.events)
	}
	started := sink.events[0]
	completed := sink.events[1]
	if started.Kind != RuntimeLifecycleStarted || completed.Kind != RuntimeLifecycleCompleted {
		t.Fatalf("event kinds = %s, %s", started.Kind, completed.Kind)
	}
	for _, event := range sink.events {
		if event.MissionID != "mission-1" || event.TaskID != "task-1" || event.AgentID != "agent-1" || event.RunID != "run-lifecycle" {
			t.Fatalf("event identity = %+v", event)
		}
		if event.RuntimeKind != "codex" || event.WorkerID != "worker-1" {
			t.Fatalf("event runtime identity = %+v", event)
		}
	}
	if completed.Status != RunStatusCompleted.String() {
		t.Fatalf("completed status = %q", completed.Status)
	}
}

func TestWorkerPublishesBlockedApprovalMetadata(t *testing.T) {
	store := newMemoryRunStore()
	err := store.Enqueue(context.Background(), AgentRun{
		RunID:           "run-blocked",
		AgentID:         "agent-1",
		AgentProfileID:  "profile-1",
		AgentInstanceID: "agent-1:runtime",
		RuntimeID:       "codex",
		MissionID:       "mission-1",
		TaskID:          "task-1",
		Request: agentcore.RuntimeRequest{
			RunID:     "run-blocked",
			AgentID:   "agent-1",
			MissionID: "mission-1",
			TaskID:    "task-1",
			ContextPack: agentcore.ContextPack{
				ID:                 "ctx-1",
				AgentID:            "agent-1",
				TaskID:             "task-1",
				MissionID:          "mission-1",
				WorkerPersonalGoal: "finish work",
			},
		},
	})
	if err != nil {
		t.Fatalf("enqueue run: %v", err)
	}
	sink := &capturingRuntimeLifecycleSink{}
	agent := agentcore.NewBaseAgent(agentcore.BaseAgentConfig{
		Identity: agentcore.AgentIdentity{
			AgentID:        "agent-1",
			AgentProfileID: "profile-1",
			InstanceRef:    agentcore.AgentInstanceRef{ID: "agent-1:runtime"},
		},
		Profile:  agentcore.AgentProfile{ID: "profile-1", RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}},
		Runtime:  blockedAdapter{},
		Trace:    agentcore.NoopTraceRecorder{},
		Notifier: agentcore.NoopNotifier{},
	})
	worker := NewWorker(WorkerOptions{
		WorkerID:  "worker-1",
		Store:     store,
		Agent:     agent,
		Lifecycle: sink,
	})

	claimed, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if !claimed {
		t.Fatalf("expected a claimed run")
	}
	if len(sink.failures) != 1 {
		t.Fatalf("failures = %+v, want one blocked failure", sink.failures)
	}
	failure := sink.failures[0]
	if failure.Kind != RuntimeLifecycleBlocked {
		t.Fatalf("failure kind = %s, want blocked", failure.Kind)
	}
	record, ok := store.get("run-blocked")
	if !ok || record.Status != RunStatusBlocked {
		t.Fatalf("blocked run should stay blocked without queue retry: %+v", record)
	}
	if failure.Metadata["approval_source"] != "codex" ||
		failure.Metadata["approval_request_id"] != "cmd-1" ||
		failure.Metadata["approval_type"] != "permission_request" {
		t.Fatalf("failure metadata = %+v", failure.Metadata)
	}
}

func TestWorkerLeavesUnspecifiedRunRuntimeSystemManaged(t *testing.T) {
	agent, err := newWorkerBaseAgent(AgentRun{
		RunID:           "run-default-runtime",
		AgentID:         "agent-default-runtime",
		AgentProfileID:  "profile-default-runtime",
		AgentInstanceID: "agent-default-runtime:runtime",
		Request: agentcore.RuntimeRequest{
			RunID:   "run-default-runtime",
			AgentID: "agent-default-runtime",
		},
	}, fakeAdapter{})
	if err != nil {
		t.Fatalf("new worker base agent: %v", err)
	}

	if agent.Base().RuntimeSpec().HasKind() {
		t.Fatalf("unspecified worker runtime should remain system-managed and unset: %+v", agent.Base().RuntimeSpec())
	}
}

func TestWorkerBaseAgentUsesLeanDefaultBaseAgentTemplate(t *testing.T) {
	agent, err := newWorkerBaseAgent(AgentRun{
		RunID:           "run-shared-template",
		AgentID:         "agent-template",
		AgentProfileID:  "profile-template",
		AgentInstanceID: "agent-template:runtime",
		Request: agentcore.RuntimeRequest{
			RunID:   "run-shared-template",
			AgentID: "agent-template",
		},
	}, fakeAdapter{})
	if err != nil {
		t.Fatalf("new worker base agent: %v", err)
	}

	spec := agent.Base().ManagedSpec()
	for _, want := range []string{
		"你是 Orrery 系统托管的 Agent「周执行」。",
		"岗位职责：Queued runtime executor。",
		"周执行",
		"Queued runtime executor",
	} {
		if !strings.Contains(spec.SystemPrompt, want) {
			t.Fatalf("worker BaseAgent prompt missing %q: %s", want, spec.SystemPrompt)
		}
	}
	for _, removed := range []string{
		"全局 BaseAgent 模板",
		"Memory 占位",
		"Knowledge 占位",
		"<capabilities>",
	} {
		if strings.Contains(spec.SystemPrompt, removed) {
			t.Fatalf("worker BaseAgent prompt should not include legacy prompt section %q: %s", removed, spec.SystemPrompt)
		}
	}
	if spec.SkillScope != "profile_bound" || spec.ToolScope != "profile_bound" || spec.MCPScope != "profile_bound" {
		t.Fatalf("worker BaseAgent scopes should use the shared default template: %+v", spec)
	}
}

func TestWorkerBaseAgentUsesExplicitProfileIdentity(t *testing.T) {
	agent, err := newWorkerBaseAgent(AgentRun{
		RunID:           "run-profile-identity",
		AgentID:         "agent-stable",
		AgentProfileID:  "profile-real",
		AgentInstanceID: "instance-real",
		Request: agentcore.RuntimeRequest{
			RunID:   "run-profile-identity",
			AgentID: "agent-stable",
		},
	}, fakeAdapter{})
	if err != nil {
		t.Fatalf("new worker base agent: %v", err)
	}

	identity := agent.Base().Identity()
	if identity.AgentID != "agent-stable" || identity.AgentProfileID != "profile-real" || identity.InstanceRef.ID != "instance-real" {
		t.Fatalf("identity = %+v", identity)
	}
	if profile := agent.Profile(); profile.ID != "profile-real" {
		t.Fatalf("profile id = %s, want profile-real", profile.ID)
	}
}

func TestWorkerBaseAgentRequiresExplicitProfileIdentity(t *testing.T) {
	_, err := newWorkerBaseAgent(AgentRun{
		RunID:           "run-missing-profile",
		AgentID:         "agent-stable",
		AgentInstanceID: "instance-real",
		Request: agentcore.RuntimeRequest{
			RunID:   "run-missing-profile",
			AgentID: "agent-stable",
		},
	}, fakeAdapter{})

	if err == nil {
		t.Fatalf("expected missing profile id error")
	}
}

func TestRunStoreRequiresExplicitAgentInstanceForAdapterOnlyRun(t *testing.T) {
	store := newMemoryRunStore()
	err := store.Enqueue(context.Background(), AgentRun{
		RunID:          "run-missing-instance",
		AgentID:        "agent-1",
		AgentProfileID: "profile-1",
		Request: agentcore.RuntimeRequest{
			RunID:   "run-missing-instance",
			AgentID: "agent-1",
		},
	})

	if err == nil {
		t.Fatalf("expected missing instance id error")
	}
}

type fakeAdapter struct{}

func (fakeAdapter) RunSession(ctx context.Context, _ agentcore.RuntimeRequest, observer agentcore.SessionEventObserver) (agentcore.RuntimeResponse, error) {
	if observer != nil {
		if err := observer(ctx, agentcore.SessionEnvelope{
			SessionID:      "test-session",
			TurnGeneration: 1,
			Sequence:       1,
			Timestamp:      time.Now().UTC(),
			Source:         agentcore.EventSourceProvider,
			Projection:     agentcore.EventProjectionCanonical,
			Event: agentcore.SessionEvent{
				Kind:        agentcore.EventTurnStarted,
				TurnStarted: &agentcore.TurnStartedEvent{Epoch: 1},
			},
		}); err != nil {
			return agentcore.RuntimeResponse{}, err
		}
	}
	return agentcore.RuntimeResponse{
		Status: agentcore.RuntimeStatusCompleted,
		Usage: map[string]agentcore.TokenUsage{
			"codex": {InputTokens: 100, OutputTokens: 40},
		},
	}, nil
}

type blockedAdapter struct{}

func (blockedAdapter) RunSession(context.Context, agentcore.RuntimeRequest, agentcore.SessionEventObserver) (agentcore.RuntimeResponse, error) {
	return agentcore.RuntimeResponse{
		Status: agentcore.RuntimeStatusBlocked,
		Error:  agentcore.RuntimeFailure{Code: "runtime_result_error", Message: "waiting for approval", Retryable: true},
		ProviderMetadata: map[string]string{
			"approval_source":     "codex",
			"approval_request_id": "cmd-1",
			"approval_type":       "permission_request",
		},
	}, nil
}

func (fakeAdapter) CancelSession(context.Context, agentcore.AgentRunID, string) error { return nil }
func (fakeAdapter) SteerSession(context.Context, agentcore.AgentRunID, string) error  { return nil }
func (fakeAdapter) PrewarmSession(context.Context, agentcore.RuntimeRequest) error    { return nil }
func (fakeAdapter) ReleaseRuntimeWorkspace(context.Context, string) error             { return nil }
func (fakeAdapter) Capabilities(context.Context) agentcore.Capabilities {
	return agentcore.Capabilities{Signals: agentcore.SignalSet{TerminalResult: true}}
}

func (blockedAdapter) CancelSession(context.Context, agentcore.AgentRunID, string) error { return nil }
func (blockedAdapter) SteerSession(context.Context, agentcore.AgentRunID, string) error  { return nil }
func (blockedAdapter) PrewarmSession(context.Context, agentcore.RuntimeRequest) error    { return nil }
func (blockedAdapter) ReleaseRuntimeWorkspace(context.Context, string) error             { return nil }
func (blockedAdapter) Capabilities(context.Context) agentcore.Capabilities {
	return agentcore.Capabilities{Signals: agentcore.SignalSet{TerminalResult: true}}
}

type noopRunCommitter struct{}

func (noopRunCommitter) SubmitAgentResult(context.Context, agentcore.AgentResult, agentcore.CommitOptions) error {
	return nil
}

type workerSignalSink struct {
	signals []agentcore.Signal
}

func (s *workerSignalSink) EmitSignal(_ context.Context, signal agentcore.Signal) error {
	s.signals = append(s.signals, signal)
	return nil
}

func (s *workerSignalSink) names() []agentcore.SignalName {
	out := make([]agentcore.SignalName, 0, len(s.signals))
	for _, signal := range s.signals {
		out = append(out, signal.Name)
	}
	return out
}

func assertWorkerSignalSequence(t *testing.T, got, want []agentcore.SignalName) {
	t.Helper()
	cursor := 0
	for _, name := range got {
		if cursor < len(want) && name == want[cursor] {
			cursor++
		}
	}
	if cursor != len(want) {
		t.Fatalf("signal sequence missing %v in %v", want, got)
	}
}

type capturingRuntimeLifecycleSink struct {
	events   []RuntimeLifecycleEvent
	failures []RuntimeFailureEvent
}

func (s *capturingRuntimeLifecycleSink) PublishRuntimeLifecycle(_ context.Context, event RuntimeLifecycleEvent) error {
	s.events = append(s.events, event)
	return nil
}

func (s *capturingRuntimeLifecycleSink) PublishRuntimeFailure(_ context.Context, event RuntimeFailureEvent) error {
	s.failures = append(s.failures, event)
	s.events = append(s.events, RuntimeLifecycleEvent{
		Kind:        event.Kind,
		MissionID:   missioncore.MissionID(event.MissionID),
		TaskID:      missioncore.TaskID(event.TaskID),
		AgentID:     event.AgentID,
		RunID:       event.RunID,
		RuntimeKind: event.RuntimeKind,
		WorkerID:    event.WorkerID,
		Status:      RunStatusFailed.String(),
		Message:     event.Message,
	})
	return nil
}
