package missioncontrol_test

import (
	"context"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/mission/projection"
)

func TestCommandServiceStartAgentRunEnqueuesRuntimeAndProjectsQueuedSession(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.Discussion.DiscussionGroupID = "discussion-1"
	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	runStore := &capturingRunStore{}
	events := &capturingMissionEventSink{}
	ids := &sequenceContextIDGenerator{values: []string{"run-1", "evt-run-queued"}}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, events, ids, fixedClock{at: at})

	receipt, err := service.StartAgentRun(ctx,
		missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser},
		missioncontrol.StartAgentRunCommand{
			RequestID:      "corr-run",
			IdempotencyKey: "start-run-1",
			MissionID:      "mission-1",
			TaskID:         "task-1",
			AgentID:        "agent-1",
			RuntimeKind:    "codex",
			Prompt:         "Implement the task",
		})
	if err != nil {
		t.Fatalf("StartAgentRun returned error: %v", err)
	}

	if receipt.CommandID != "run-1" || receipt.Status != "accepted" || receipt.MissionID != "mission-1" || receipt.TaskID != "task-1" || !receipt.AcceptedAt.Equal(at) {
		t.Fatalf("receipt = %+v", receipt)
	}
	if len(runStore.runs) != 1 {
		t.Fatalf("enqueued runs = %+v", runStore.runs)
	}
	run := runStore.runs[0]
	if run.RunID != "run-1" || run.AgentID != "agent-1" || run.AgentProfileID != "agent-1" || run.AgentInstanceID != "agent-1:mission-1" || run.RuntimeID != "codex" || run.MissionID != "mission-1" || run.TaskID != "task-1" {
		t.Fatalf("run identity = %+v", run)
	}
	if run.Status != runtimedaemon.RunStatusQueued || run.Request.RunID != "run-1" || run.Request.MissionID != "mission-1" || run.Request.TaskID != "task-1" {
		t.Fatalf("run request/status = %+v", run)
	}
	if run.Request.RuntimeOptions.SystemPrompt == "" || run.Request.ContextPack.WorkerPersonalGoal == "" {
		t.Fatalf("run should carry base prompt placeholders and task context: %+v", run.Request)
	}
	if run.Request.RuntimeOptions.ProviderSpecific["queue_id"] != "mission:mission-1:agent:agent-1" {
		t.Fatalf("run queue id = %+v", run.Request.RuntimeOptions.ProviderSpecific)
	}
	if len(events.events) != 1 || events.events[0].EventType != missionevent.EventAgentRunQueued {
		t.Fatalf("events = %+v, want AgentRunQueued", events.events)
	}
	updated, err := repo.GetMissionControl(ctx, "mission-1")
	if err != nil {
		t.Fatalf("GetMissionControl returned error: %v", err)
	}
	if len(updated.RuntimeSessions) != 1 || updated.RuntimeSessions[0].RunID != "run-1" || updated.RuntimeSessions[0].Status != projection.RuntimeSessionQueued {
		t.Fatalf("projected runtime sessions = %+v", updated.RuntimeSessions)
	}
}

func TestCommandServiceResponsibilityDispatchAllowsPausedScheduledTaskOccurrence(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.Mission.Status = projection.MissionStatusPaused
	model.TaskBoard.Tasks[0].SourceKind = "scheduler_agent_task"
	model.TaskBoard.Tasks[0].SourceID = "scheduled-task-1"
	model.TaskBoard.Tasks[0].SourceStepID = "scheduled-run-1"
	if err := repo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	runStore := &capturingRunStore{}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, &capturingMissionEventSink{}, &sequenceContextIDGenerator{values: []string{"evt-scheduled-run"}}, fixedClock{at: at})
	_, err := service.DispatchResponsibilityRun(ctx, missioncontrol.ViewerRef{ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent}, missioncontrol.StartAgentRunCommand{
		RequestID: "scheduled-dispatch", IdempotencyKey: "scheduled-dispatch", MissionID: "mission-1", TaskID: "task-1",
		AgentID: "agent-1", RuntimeKind: "codex", RunID: "agent-run-1", Attempt: 2,
		ResponsibilityContext: testResponsibilityExecutionContext("agent-1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata := runStore.runs[0].Request.RuntimeOptions.ProviderSpecific
	if metadata["run_source"] != "scheduler_agent_task" || metadata["scheduled_task_id"] != "scheduled-task-1" || metadata["scheduled_run_id"] != "scheduled-run-1" || metadata["generation_id"] != "scheduled-run-1" {
		t.Fatalf("scheduled runtime metadata = %+v", metadata)
	}
	if runStore.runs[0].Attempt != 2 {
		t.Fatalf("queued responsibility attempt = %d, want 2", runStore.runs[0].Attempt)
	}
}

func TestCommandServiceResponsibilityDispatchRejectsPausedNonScheduledTask(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.Mission.Status = projection.MissionStatusPaused
	if err := repo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	runStore := &capturingRunStore{}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, &capturingMissionEventSink{}, &sequenceContextIDGenerator{values: []string{"evt-paused-run"}}, fixedClock{at: at})
	_, err := service.DispatchResponsibilityRun(ctx, missioncontrol.ViewerRef{ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent}, missioncontrol.StartAgentRunCommand{
		RequestID: "paused-dispatch", IdempotencyKey: "paused-dispatch", MissionID: "mission-1", TaskID: "task-1",
		AgentID: "agent-1", RuntimeKind: "codex", RunID: "agent-run-paused",
	})
	if err != missioncontrol.ErrInvalidRequest {
		t.Fatalf("paused non-scheduled responsibility dispatch error = %v, want ErrInvalidRequest", err)
	}
	if len(runStore.runs) != 0 {
		t.Fatalf("paused non-scheduled responsibility enqueued runs: %+v", runStore.runs)
	}
}

func TestCommandServicePausesTodoMissionCancelsRunsAndRejectsFurtherDispatch(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 6, 6, 10, 5, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.Mission.OriginKind = "todo"
	model.Mission.OriginTodoID = "todo-1"
	model.RuntimeSessions = []projection.RuntimeSessionView{{
		RunID: "run-active", AgentID: "agent-1", TaskID: "task-1", RuntimeKind: "codex",
		Status: projection.RuntimeSessionRunning, QueuedAt: at.Add(-time.Minute), StartedAt: at, UpdatedAt: at,
	}}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	runStore := &capturingRunStore{}
	events := &capturingMissionEventSink{}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, events, &sequenceContextIDGenerator{values: []string{"evt-mission-paused", "evt-pause-cancel"}}, fixedClock{at: at.Add(time.Minute)})
	viewer := missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}
	command := missioncontrol.PauseTodoMissionCommand{
		IdempotencyKey: "todo-mission-pause:binding-1", MissionID: "mission-1", TodoID: "todo-1", Reason: "Todo scope changed",
	}
	if err := service.PauseTodoMission(ctx, viewer, command); err != nil {
		t.Fatal(err)
	}
	updated, err := repo.GetMissionControl(ctx, "mission-1")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Mission.Status != projection.MissionStatusPaused || len(updated.RuntimeSessions) != 1 || updated.RuntimeSessions[0].Status != projection.RuntimeSessionCancelled {
		t.Fatalf("paused Mission projection = %+v", updated)
	}
	if len(runStore.cancelled) != 1 || runStore.cancelled[0].runID != "run-active" || len(events.events) != 2 || events.events[0].EventType != missionevent.EventMissionPaused || events.events[1].EventType != missionevent.EventAgentRunCancelled {
		t.Fatalf("cancellations=%+v events=%+v", runStore.cancelled, events.events)
	}
	pauseNodeFound := false
	for _, node := range updated.ProgressGraph.Nodes {
		pauseNodeFound = pauseNodeFound || node.EventType == string(missionevent.EventMissionPaused)
	}
	if !pauseNodeFound {
		t.Fatalf("pause progress = %+v", updated.ProgressGraph)
	}
	if err := service.PauseTodoMission(ctx, viewer, command); err != nil {
		t.Fatalf("idempotent pause: %v", err)
	}
	if _, err := service.StartAgentRun(ctx, viewer, missioncontrol.StartAgentRunCommand{
		IdempotencyKey: "start-after-pause", MissionID: "mission-1", TaskID: "task-1", AgentID: "agent-1", RuntimeKind: "codex",
	}); err != missioncontrol.ErrInvalidRequest {
		t.Fatalf("StartAgentRun after pause error = %v", err)
	}
}

func TestCommandServiceStartAgentRunUsesCanonicalConversationQueueAndDigest(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 6, 6, 10, 15, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.Discussion.ConversationID = "todo-conversation-1"
	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	provider := &capturingRuntimeConversationContextProvider{result: missioncontrol.RuntimeConversationContext{
		ConversationID: "todo-conversation-1",
		Digest:         agentcore.DiscussionDigestRef{ID: "digest-conversation-1", SummaryRef: "The approved plan requires tenant-safe implementation."},
	}}
	runStore := &capturingRunStore{}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, &capturingMissionEventSink{}, &sequenceContextIDGenerator{values: []string{"run-conversation", "evt-conversation"}}, fixedClock{at: at})
	service.SetRuntimeConversationContextProvider(provider)
	_, err := service.StartAgentRun(ctx, missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}, missioncontrol.StartAgentRunCommand{
		IdempotencyKey: "start-conversation", MissionID: "mission-1", TaskID: "task-1", AgentID: "agent-1", RuntimeKind: "codex",
	})
	if err != nil {
		t.Fatalf("StartAgentRun returned error: %v", err)
	}
	if provider.request.TenantID != "global" || provider.request.MissionID != "mission-1" || provider.request.ConversationID != "todo-conversation-1" || provider.request.AgentID != "agent-1" {
		t.Fatalf("conversation context request = %+v", provider.request)
	}
	if len(runStore.runs) != 1 {
		t.Fatalf("enqueued runs = %d", len(runStore.runs))
	}
	run := runStore.runs[0]
	if got := run.Request.RuntimeOptions.ProviderSpecific["queue_id"]; got != "mission:mission-1:agent:agent-1" {
		t.Fatalf("queue_id = %q", got)
	}
	if run.Request.ContextPack.ConversationID != "todo-conversation-1" || run.Request.ContextPack.DiscussionGroupID != "" || run.Request.ContextPack.DiscussionDigestRef.ID != "digest-conversation-1" {
		t.Fatalf("context pack = %+v", run.Request.ContextPack)
	}
	if run.Request.RuntimeOptions.ProviderSpecific["conversation_id"] != "todo-conversation-1" || run.Request.RuntimeOptions.ProviderSpecific["discussion_id"] != "todo-conversation-1" || run.Request.RuntimeOptions.ProviderSpecific["discussion_digest_id"] != "digest-conversation-1" {
		t.Fatalf("provider metadata = %+v", run.Request.RuntimeOptions.ProviderSpecific)
	}
	foundDigestSource := false
	for _, source := range run.Request.ContextPack.SourceRefs {
		foundDigestSource = foundDigestSource || source.Kind == agentcore.ContextSourceKindDiscussionDigest
	}
	if !foundDigestSource {
		t.Fatalf("discussion digest source missing: %+v", run.Request.ContextPack.SourceRefs)
	}
	var prompt strings.Builder
	for _, message := range run.Request.InputMessages {
		prompt.WriteString(message.Content)
	}
	if !strings.Contains(prompt.String(), "approved plan requires tenant-safe implementation") {
		t.Fatalf("runtime prompt does not contain canonical conversation digest: %s", prompt.String())
	}
}

func TestCommandServiceStartAgentRunReplaysFixedRunIDOnce(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 6, 6, 10, 30, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	runStore := &capturingRunStore{}
	events := &capturingMissionEventSink{}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, events, &sequenceContextIDGenerator{values: []string{"evt-fixed-run"}}, fixedClock{at: at})
	command := missioncontrol.StartAgentRunCommand{
		RequestID: "corr-fixed", IdempotencyKey: "start-fixed", MissionID: "mission-1", TaskID: "task-1",
		AgentID: "agent-1", RuntimeKind: "codex", Prompt: "Implement the task", RunID: "run-fixed-1",
	}
	first, err := service.StartAgentRun(ctx, missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}, command)
	if err != nil {
		t.Fatalf("first StartAgentRun returned error: %v", err)
	}
	second, err := service.StartAgentRun(ctx, missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}, command)
	if err != nil {
		t.Fatalf("replayed StartAgentRun returned error: %v", err)
	}
	if first.CommandID != "run-fixed-1" || second.CommandID != first.CommandID || !second.AcceptedAt.Equal(first.AcceptedAt) {
		t.Fatalf("receipts = %+v / %+v", first, second)
	}
	if len(runStore.runs) != 1 || len(events.events) != 1 {
		t.Fatalf("fixed run replay produced duplicate side effects: runs=%d events=%d", len(runStore.runs), len(events.events))
	}
	updated, err := repo.GetMissionControl(ctx, "mission-1")
	if err != nil {
		t.Fatalf("GetMissionControl returned error: %v", err)
	}
	if len(updated.RuntimeSessions) != 1 || updated.RuntimeSessions[0].RunID != "run-fixed-1" {
		t.Fatalf("runtime sessions = %+v", updated.RuntimeSessions)
	}
	conflict := command
	conflict.RuntimeKind = "openclaw"
	if _, err := service.StartAgentRun(ctx, missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}, conflict); err != missioncontrol.ErrInvalidRequest {
		t.Fatalf("conflicting fixed run replay error = %v, want ErrInvalidRequest", err)
	}
}

func TestCommandServiceStartAgentRunRejectsAnotherActiveRunForTaskAndAgent(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 6, 6, 10, 45, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.RuntimeSessions = []projection.RuntimeSessionView{{
		RunID: "run-direct", AgentID: "agent-1", TaskID: "task-1", RuntimeKind: "codex",
		Status: projection.RuntimeSessionRunning, QueuedAt: at.Add(-time.Minute), StartedAt: at, UpdatedAt: at,
	}}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	runStore := &capturingRunStore{}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, &capturingMissionEventSink{}, &sequenceContextIDGenerator{values: []string{"evt-duplicate"}}, fixedClock{at: at})

	_, err := service.StartAgentRun(ctx, missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}, missioncontrol.StartAgentRunCommand{
		IdempotencyKey: "start-duplicate", MissionID: "mission-1", TaskID: "task-1", AgentID: "agent-1", RuntimeKind: "codex", RunID: "run-duplicate",
	})
	if err != missioncontrol.ErrAgentRunAlreadyActive {
		t.Fatalf("duplicate active run error = %v, want ErrAgentRunAlreadyActive", err)
	}
	if len(runStore.runs) != 0 {
		t.Fatalf("duplicate active run was enqueued: %+v", runStore.runs)
	}
}

func TestCommandServiceStartAgentRunRejectsTerminalTask(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 6, 6, 10, 50, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.TaskBoard.Tasks[0].Status = projection.TaskStatusApproved
	if err := repo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	runStore := &capturingRunStore{}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, &capturingMissionEventSink{}, &sequenceContextIDGenerator{values: []string{"evt-terminal"}}, fixedClock{at: at})

	_, err := service.StartAgentRun(ctx, missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}, missioncontrol.StartAgentRunCommand{
		IdempotencyKey: "start-terminal", MissionID: "mission-1", TaskID: "task-1", AgentID: "agent-1", RuntimeKind: "codex", RunID: "run-terminal",
	})
	if err != missioncontrol.ErrInvalidRequest {
		t.Fatalf("terminal task run error = %v, want ErrInvalidRequest", err)
	}
	if len(runStore.runs) != 0 {
		t.Fatalf("terminal task run was enqueued: %+v", runStore.runs)
	}
}

func TestCommandServiceResponsibilityDispatchStartsOnlyAssignedAgentWithStableRunID(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 6, 6, 11, 0, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	if err := repo.Save(ctx, appRuntimeReadModel(at)); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	runStore := &capturingRunStore{}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, &capturingMissionEventSink{}, &sequenceContextIDGenerator{values: []string{"evt-agent-own"}}, fixedClock{at: at})
	base := missioncontrol.StartAgentRunCommand{
		IdempotencyKey: "agent-own", MissionID: "mission-1", TaskID: "task-1", AgentID: "agent-1", RuntimeKind: "codex", RunID: "run-agent-own",
		ResponsibilityContext: testResponsibilityExecutionContext("agent-1"),
	}
	if _, err := service.DispatchResponsibilityRun(ctx, missioncontrol.ViewerRef{ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent}, base); err != nil {
		t.Fatalf("assigned Agent could not start its task: %v", err)
	}
	withoutRunID := base
	withoutRunID.RunID = ""
	if _, err := service.DispatchResponsibilityRun(ctx, missioncontrol.ViewerRef{ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent}, withoutRunID); err != missioncontrol.ErrInvalidRequest {
		t.Fatalf("responsibility dispatch without stable run id error = %v, want ErrInvalidRequest", err)
	}
	withoutContext := base
	withoutContext.RunID = "run-agent-without-context"
	withoutContext.ResponsibilityContext = nil
	if _, err := service.DispatchResponsibilityRun(ctx, missioncontrol.ViewerRef{ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent}, withoutContext); err != missioncontrol.ErrInvalidRequest {
		t.Fatalf("responsibility dispatch without context error = %v, want ErrInvalidRequest", err)
	}
	withoutPolicy := base
	withoutPolicy.RunID = "run-agent-without-policy"
	withoutPolicy.ResponsibilityContext = agentcore.CloneResponsibilityContext(base.ResponsibilityContext)
	withoutPolicy.ResponsibilityContext.PolicyID = ""
	if _, err := service.DispatchResponsibilityRun(ctx, missioncontrol.ViewerRef{ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent}, withoutPolicy); err != missioncontrol.ErrInvalidRequest {
		t.Fatalf("responsibility dispatch without policy error = %v, want ErrInvalidRequest", err)
	}
	otherAgent := base
	otherAgent.IdempotencyKey = "agent-other"
	otherAgent.RunID = "run-agent-other"
	otherAgent.AgentID = "agent-2"
	if _, err := service.DispatchResponsibilityRun(ctx, missioncontrol.ViewerRef{ViewerID: "agent-2", ViewerKind: missioncontrol.ViewerKindAgent}, otherAgent); err != missioncontrol.ErrProjectionNotFound {
		t.Fatalf("unassigned Agent error = %v, want ErrProjectionNotFound", err)
	}
	delegate := base
	delegate.IdempotencyKey = "agent-delegate"
	delegate.RunID = "run-agent-delegate"
	delegate.AgentID = "agent-2"
	if _, err := service.DispatchResponsibilityRun(ctx, missioncontrol.ViewerRef{ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent}, delegate); err != missioncontrol.ErrProjectionNotFound {
		t.Fatalf("Agent delegation with view permission error = %v, want ErrProjectionNotFound", err)
	}
	if len(runStore.runs) != 1 {
		t.Fatalf("unauthorized Agent starts enqueued runs: %+v", runStore.runs)
	}
}

func TestCommandServiceStartAgentRunUsesMissionEventStreamVersionWhenProjectionCheckpointLags(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.Checkpoint.Version = 2
	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	runStore := &capturingRunStore{}
	events := &capturingMissionEventSink{}
	ids := &sequenceContextIDGenerator{values: []string{"run-2", "evt-run-queued-2"}}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, events, ids, fixedClock{at: at})
	service.SetRuntimeEventReader(capturingMissionEventReader{events: make([]missionevent.Envelope, 8)})

	receipt, err := service.StartAgentRun(ctx,
		missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser},
		missioncontrol.StartAgentRunCommand{
			RequestID:      "corr-run-2",
			IdempotencyKey: "start-run-2",
			MissionID:      "mission-1",
			TaskID:         "task-1",
			AgentID:        "agent-1",
			RuntimeKind:    "codex",
			Prompt:         "Implement the task",
		})
	if err != nil {
		t.Fatalf("StartAgentRun returned error: %v", err)
	}
	if receipt.Version != 3 {
		t.Fatalf("receipt version = %d, want projection version 3", receipt.Version)
	}
	if len(events.events) != 1 || events.events[0].StreamVersion != 9 {
		t.Fatalf("events = %+v, want stream version 9 from event reader", events.events)
	}
	updated, err := repo.GetMissionControl(ctx, "mission-1")
	if err != nil {
		t.Fatalf("GetMissionControl returned error: %v", err)
	}
	if updated.Checkpoint.Version != 3 {
		t.Fatalf("checkpoint version = %d, want projection version 3", updated.Checkpoint.Version)
	}
	if len(updated.RuntimeSessions) != 1 || updated.RuntimeSessions[0].RunID != "run-2" || updated.RuntimeSessions[0].Status != projection.RuntimeSessionQueued {
		t.Fatalf("projected runtime sessions = %+v", updated.RuntimeSessions)
	}
}

func TestCommandServiceCancelAgentRunCancelsRuntimeAndProjectsCancelledSession(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 6, 6, 13, 0, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.RuntimeSessions = []projection.RuntimeSessionView{{
		RunID:       "run-1",
		AgentID:     "agent-1",
		TaskID:      "task-1",
		RuntimeKind: "codex",
		QueueID:     "discussion:discussion-1",
		Status:      projection.RuntimeSessionRunning,
		UpdatedAt:   at.Add(-time.Minute),
	}}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	runStore := &capturingRunStore{}
	events := &capturingMissionEventSink{}
	ids := &sequenceContextIDGenerator{values: []string{"evt-run-cancelled"}}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, events, ids, fixedClock{at: at})

	receipt, err := service.CancelAgentRun(ctx,
		missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser},
		missioncontrol.CancelAgentRunCommand{
			RequestID:      "corr-cancel",
			IdempotencyKey: "cancel-run-1",
			MissionID:      "mission-1",
			RunID:          "run-1",
			Reason:         "operator stopped duplicate work",
		})
	if err != nil {
		t.Fatalf("CancelAgentRun returned error: %v", err)
	}

	if receipt.CommandID != "run-1" || receipt.Status != "accepted" || receipt.MissionID != "mission-1" || receipt.TaskID != "task-1" || !receipt.AcceptedAt.Equal(at) {
		t.Fatalf("receipt = %+v", receipt)
	}
	if len(runStore.cancelled) != 1 || runStore.cancelled[0].runID != "run-1" || runStore.cancelled[0].reason != "operator stopped duplicate work" {
		t.Fatalf("cancelled runs = %+v", runStore.cancelled)
	}
	if len(events.events) != 1 || events.events[0].EventType != missionevent.EventAgentRunCancelled || events.events[0].IdempotencyKey != "cancel-run-1" {
		t.Fatalf("events = %+v, want AgentRunCancelled", events.events)
	}
	payload, ok := events.events[0].Payload.(missionevent.RuntimeFailurePayload)
	if !ok {
		t.Fatalf("event payload = %T, want RuntimeFailurePayload", events.events[0].Payload)
	}
	if payload.RunID != "run-1" || payload.TaskID != "task-1" || payload.AgentID != "agent-1" || payload.Code != "agent_runtime_cancelled" || payload.Message != "operator stopped duplicate work" {
		t.Fatalf("payload = %+v", payload)
	}
	updated, err := repo.GetMissionControl(ctx, "mission-1")
	if err != nil {
		t.Fatalf("GetMissionControl returned error: %v", err)
	}
	if len(updated.RuntimeSessions) != 1 || updated.RuntimeSessions[0].Status != projection.RuntimeSessionCancelled || !updated.RuntimeSessions[0].CompletedAt.Equal(at) {
		t.Fatalf("projected runtime sessions = %+v", updated.RuntimeSessions)
	}
}

func TestCommandServiceRetryAgentRunCreatesNewRunLinkedToPreviousAttempt(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 6, 6, 14, 0, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.Discussion.DiscussionGroupID = "discussion-1"
	model.RuntimeSessions = []projection.RuntimeSessionView{{
		RunID:        "run-failed",
		AgentID:      "agent-1",
		TaskID:       "task-1",
		RuntimeKind:  "codex",
		QueueID:      "discussion:discussion-1",
		Status:       projection.RuntimeSessionFailed,
		ErrorSummary: "handler failed",
		ResponsibilityContext: &agentcore.ResponsibilityContext{
			ContextID: "snapshot-1", ResponsibilityID: "responsibility-1", ResponsibilityVersion: 3,
			PolicyID: "policy-1", PolicyVersion: 2, ActorMemberID: "member-1", AgentProfileID: "agent-1",
		},
		CompletedAt: at.Add(-time.Minute),
		UpdatedAt:   at.Add(-time.Minute),
	}}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	runStore := &capturingRunStore{}
	events := &capturingMissionEventSink{}
	ids := &sequenceContextIDGenerator{values: []string{"run-retry", "evt-run-retry-queued"}}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, events, ids, fixedClock{at: at})

	receipt, err := service.RetryAgentRun(ctx,
		missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser},
		missioncontrol.RetryAgentRunCommand{
			RequestID:      "corr-retry",
			IdempotencyKey: "retry-run-1",
			MissionID:      "mission-1",
			RunID:          "run-failed",
		})
	if err != nil {
		t.Fatalf("RetryAgentRun returned error: %v", err)
	}

	if receipt.CommandID != "run-retry" || receipt.Status != "accepted" || receipt.TaskID != "task-1" {
		t.Fatalf("receipt = %+v", receipt)
	}
	if len(runStore.runs) != 1 {
		t.Fatalf("enqueued runs = %+v", runStore.runs)
	}
	run := runStore.runs[0]
	if run.RunID != "run-retry" || run.AgentID != "agent-1" || run.RuntimeID != "codex" || run.Request.RuntimeOptions.ProviderSpecific["queue_id"] != "discussion:discussion-1" {
		t.Fatalf("retry run = %+v", run)
	}
	if run.Request.ContextPack.ResponsibilityContext == nil || run.Request.ContextPack.ResponsibilityContext.ContextID != "snapshot-1" || run.Request.RuntimeOptions.ProviderSpecific["responsibility_actor_member_id"] != "member-1" {
		t.Fatalf("retry responsibility context = %+v", run.Request.ContextPack.ResponsibilityContext)
	}
	if len(events.events) != 1 || events.events[0].EventType != missionevent.EventAgentRunQueued {
		t.Fatalf("events = %+v, want AgentRunQueued", events.events)
	}
	if events.events[0].Metadata["command"] != "mission.ops.retry_agent_run" || events.events[0].Metadata["retry_of_run_id"] != "run-failed" {
		t.Fatalf("event metadata = %+v", events.events[0].Metadata)
	}
	if events.events[0].Metadata["responsibility_context_id"] != "snapshot-1" || events.events[0].Metadata["responsibility_id"] != "responsibility-1" {
		t.Fatalf("responsibility event metadata = %+v", events.events[0].Metadata)
	}
	updated, err := repo.GetMissionControl(ctx, "mission-1")
	if err != nil {
		t.Fatalf("GetMissionControl returned error: %v", err)
	}
	if len(updated.RuntimeSessions) != 2 || updated.RuntimeSessions[1].RunID != "run-retry" || updated.RuntimeSessions[1].Status != projection.RuntimeSessionQueued {
		t.Fatalf("projected runtime sessions = %+v", updated.RuntimeSessions)
	}
	if updated.RuntimeSessions[1].ResponsibilityContext == nil || updated.RuntimeSessions[1].ResponsibilityContext.ContextID != "snapshot-1" {
		t.Fatalf("projected retry responsibility context = %+v", updated.RuntimeSessions[1].ResponsibilityContext)
	}
}

func testResponsibilityExecutionContext(agentID string) *agentcore.ResponsibilityContext {
	return &agentcore.ResponsibilityContext{
		ContextID:             "responsibility-context-test",
		ResponsibilityID:      "responsibility-test",
		ResponsibilityVersion: 1,
		PolicyID:              "responsibility-policy-test",
		PolicyVersion:         1,
		AgentProfileID:        agentID,
	}
}

func TestCommandServiceRetryAgentRunRejectsActiveRuntimeSession(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 6, 6, 15, 0, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.RuntimeSessions = []projection.RuntimeSessionView{{
		RunID:       "run-active",
		AgentID:     "agent-1",
		TaskID:      "task-1",
		RuntimeKind: "codex",
		Status:      projection.RuntimeSessionRunning,
		UpdatedAt:   at,
	}}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	runStore := &capturingRunStore{}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, &capturingMissionEventSink{}, &sequenceContextIDGenerator{}, fixedClock{at: at})

	_, err := service.RetryAgentRun(ctx,
		missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser},
		missioncontrol.RetryAgentRunCommand{
			RequestID:      "corr-retry-active",
			IdempotencyKey: "retry-active",
			MissionID:      "mission-1",
			RunID:          "run-active",
		})

	if err != missioncontrol.ErrInvalidRequest {
		t.Fatalf("RetryAgentRun error = %v, want ErrInvalidRequest", err)
	}
	if len(runStore.runs) != 0 {
		t.Fatalf("active retry should not enqueue runs: %+v", runStore.runs)
	}
}

func TestCommandServiceResumeRunAfterDurableHILQueuesBoundWechatContinuationOnce(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 8, 21, 14, 30, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.TenantID = "system"
	model.Mission.TenantID = "system"
	model.Mission.Status = projection.MissionStatusPaused
	model.Mission.OriginKind = "scheduler"
	model.RuntimeSessions = []projection.RuntimeSessionView{{
		RunID: "responsibility-run-1", AgentID: "agent-1", TaskID: "task-1", RuntimeKind: "pi",
		QueueID: "responsibility:agent-1", Status: projection.RuntimeSessionCompleted,
		ResponsibilityContext: testResponsibilityExecutionContext("agent-1"), CompletedAt: at.Add(-time.Minute), UpdatedAt: at.Add(-time.Minute),
	}}
	if err := repo.SaveForTenant(ctx, "system", model); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	runStore := &capturingRunStore{}
	events := &capturingMissionEventSink{}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, events, &sequenceContextIDGenerator{values: []string{"evt-hil-continuation"}}, fixedClock{at: at})
	metadata := map[string]string{
		"approval_run_id": "responsibility-run-1", "approval_request_id": "tool-call-1",
		"approval_session_id": "pi-session-1", "approval_kind": "tool_use",
		"approval_tool_name": "mcp_marketing_marketing_wechat_draft_create",
		"hil_decision_id":    "decision-1", "hil_receipt_id": "receipt-1", "hil_receipt_effect": "authorize",
		"approval_material_kind": "wechat_draft", "approval_material_hash": "sha256:material",
		"approval_material_account_id": "wechat-main", "approval_material_intent_id": "intent-1",
		"approval_material_intent_hash": "sha256:intent",
	}
	command := missioncontrol.ResumeDurableHILRunCommand{
		IdempotencyKey: "hil-runtime-mission-resume:decision-1:receipt-1", TenantID: "system", MissionID: "mission-1",
		RunID: "responsibility-run-1", ApprovalRef: "tool-call-1",
		ToolName: "mcp_marketing_marketing_wechat_draft_create", HILDecisionID: "decision-1",
		HILReceiptID: "receipt-1", HILArgsHash: "sha256:args", ResolverID: "ding", DecidedAt: at.Format(time.RFC3339Nano),
		Metadata: metadata,
	}
	receipt, err := service.ResumeRunAfterDurableHIL(ctx, command)
	if err != nil {
		t.Fatalf("ResumeRunAfterDurableHIL returned error: %v", err)
	}
	if !strings.HasPrefix(receipt.CommandID, "hil-continuation-receipt-1-") || len(runStore.runs) != 1 || len(events.events) != 1 {
		t.Fatalf("receipt/runs/events = %+v / %+v / %+v", receipt, runStore.runs, events.events)
	}
	run := runStore.runs[0]
	provider := run.Request.RuntimeOptions.ProviderSpecific
	if run.Request.RuntimeOptions.ResumeSessionID != "" || provider["approval_session_id"] != "pi-session-1" || provider["approval_action_id"] != "approve_once" ||
		provider["hil_decision_id"] != "decision-1" || provider["hil_receipt_id"] != "receipt-1" ||
		provider["approval_material_account_id"] != "wechat-main" || provider["approval_material_intent_id"] != "intent-1" ||
		provider["approval_continuation_of_run_id"] != "responsibility-run-1" {
		t.Fatalf("durable HIL provider binding = %+v", provider)
	}
	if len(run.Request.InputMessages) != 2 || run.Request.InputMessages[1].Role != "user" ||
		!strings.Contains(run.Request.InputMessages[1].Content, "wechat/publish-intents/intent-1.json") ||
		!strings.Contains(run.Request.InputMessages[1].Content, "share_thumbnail_path") ||
		run.Request.RuntimeOptions.MaxTurns != 6 || run.Request.ContextPack.DiscussionDigestRef.ID != "" ||
		run.Task.Goal != "Execute the approved WeChat draft creation exactly once." {
		t.Fatalf("durable HIL continuation was not minimized: run=%+v", run)
	}
	if _, err := service.ResumeRunAfterDurableHIL(ctx, command); err != nil {
		t.Fatalf("idempotent replay returned error: %v", err)
	}
	if len(runStore.runs) != 1 || len(events.events) != 1 {
		t.Fatalf("idempotent replay duplicated continuation: runs=%d events=%d", len(runStore.runs), len(events.events))
	}
}

func appRuntimeReadModel(at time.Time) projection.ReadModel {
	return projection.ReadModel{
		Mission: projection.MissionView{MissionID: missioncore.MissionID("mission-1"), Title: "Mission", FinalGoal: "Ship", Status: projection.MissionStatusActive, CreatedAt: at},
		Team: projection.TeamView{Members: []projection.TeamMemberView{{
			AgentID: "agent-1", AgentProfileID: "agent-1", AgentInstanceID: "agent-1:mission-1", AgentKind: "ai", DisplayName: "Ari", MissionRole: "worker", Status: projection.TeamMemberJoined, JoinedAt: at,
		}}},
		TaskBoard: projection.TaskBoardView{Tasks: []projection.TaskCardView{{
			TaskID: "task-1", Assignee: "agent-1", Goal: "Implement", WorkerPersonalGoalSummary: "Implement the task", Status: projection.TaskStatusAssigned, UpdatedAt: at,
		}}},
		Checkpoint: projection.Checkpoint{MissionID: "mission-1", Version: 7, UpdatedAt: at},
		UpdatedAt:  at,
	}
}

type capturingRunStore struct {
	runs      []runtimedaemon.AgentRun
	cancelled []capturedRunCancellation
}

func (s *capturingRunStore) Enqueue(_ context.Context, run runtimedaemon.AgentRun) error {
	s.runs = append(s.runs, run)
	return nil
}

func (s *capturingRunStore) Cancel(_ context.Context, runID agentcore.AgentRunID, reason string) error {
	s.cancelled = append(s.cancelled, capturedRunCancellation{runID: runID, reason: reason})
	return nil
}

type capturedRunCancellation struct {
	runID  agentcore.AgentRunID
	reason string
}

type capturingMissionEventSink struct {
	events []missionevent.Envelope
}

func (s *capturingMissionEventSink) Append(_ context.Context, event missionevent.Envelope) error {
	s.events = append(s.events, event)
	return nil
}

type capturingMissionEventReader struct {
	events []missionevent.Envelope
}

type capturingRuntimeConversationContextProvider struct {
	request missioncontrol.RuntimeConversationContextRequest
	result  missioncontrol.RuntimeConversationContext
	err     error
}

func (p *capturingRuntimeConversationContextProvider) RuntimeConversationContext(_ context.Context, request missioncontrol.RuntimeConversationContextRequest) (missioncontrol.RuntimeConversationContext, error) {
	p.request = request
	return p.result, p.err
}

func (r capturingMissionEventReader) LoadMission(_ context.Context, _ missioncore.MissionID) ([]missionevent.Envelope, error) {
	return append([]missionevent.Envelope(nil), r.events...), nil
}

type sequenceContextIDGenerator struct {
	values []string
}

func (g *sequenceContextIDGenerator) NewIDContext() (string, error) {
	if len(g.values) == 0 {
		return "generated-id", nil
	}
	next := g.values[0]
	g.values = g.values[1:]
	return next, nil
}

type fixedClock struct {
	at time.Time
}

func (c fixedClock) Now() time.Time {
	return c.at
}

var _ = agentcore.AgentRunID("")
