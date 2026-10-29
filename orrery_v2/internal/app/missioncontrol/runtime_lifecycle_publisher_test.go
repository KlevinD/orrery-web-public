package missioncontrol_test

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/mission/projection"
	"orrery_v2/internal/platform/eventstore"
)

func TestRuntimeLifecyclePublisherAppendsMissionEventsAndRefreshesProjection(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 6, 6, 11, 0, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	events := &capturingMissionEvents{}
	ids := &sequenceContextIDGenerator{values: []string{"evt-started", "evt-completed"}}
	publisher := missioncontrol.NewRuntimeLifecyclePublisher(missioncontrol.RuntimeLifecyclePublisherOptions{
		Events:      events,
		Repo:        repo,
		IDGenerator: ids,
		Clock:       fixedClock{at: at},
	})

	if err := publisher.PublishRuntimeLifecycle(ctx, runtimedaemon.RuntimeLifecycleEvent{
		Kind:        runtimedaemon.RuntimeLifecycleStarted,
		MissionID:   "mission-1",
		TaskID:      "task-1",
		AgentID:     "agent-1",
		RunID:       "run-1",
		RuntimeKind: "codex",
		QueueID:     "mission:mission-1",
		Status:      runtimedaemon.RunStatusRunning.String(),
		WorkerID:    "worker-1",
		Message:     "agent run started",
	}); err != nil {
		t.Fatalf("PublishRuntimeLifecycle started returned error: %v", err)
	}
	if err := publisher.PublishRuntimeLifecycle(ctx, runtimedaemon.RuntimeLifecycleEvent{
		Kind:        runtimedaemon.RuntimeLifecycleCompleted,
		MissionID:   "mission-1",
		TaskID:      "task-1",
		AgentID:     "agent-1",
		RunID:       "run-1",
		RuntimeKind: "codex",
		QueueID:     "mission:mission-1",
		Status:      runtimedaemon.RunStatusCompleted.String(),
		WorkerID:    "worker-1",
		Message:     "agent run completed",
		Usage: map[string]agentcore.TokenUsage{
			"codex": {InputTokens: 900, OutputTokens: 260},
		},
	}); err != nil {
		t.Fatalf("PublishRuntimeLifecycle completed returned error: %v", err)
	}

	if len(events.events) != 2 {
		t.Fatalf("events = %+v, want started and completed", events.events)
	}
	if events.events[0].EventType != missionevent.EventAgentRunStarted || events.events[0].StreamVersion != 8 {
		t.Fatalf("started event = %+v", events.events[0])
	}
	if events.events[1].EventType != missionevent.EventAgentRunCompleted || events.events[1].StreamVersion != 9 {
		t.Fatalf("completed event = %+v", events.events[1])
	}
	updated, err := repo.GetMissionControl(ctx, "mission-1")
	if err != nil {
		t.Fatalf("GetMissionControl returned error: %v", err)
	}
	if updated.Checkpoint.Version != 9 {
		t.Fatalf("checkpoint version = %d, want 9", updated.Checkpoint.Version)
	}
	if len(updated.RuntimeSessions) != 1 {
		t.Fatalf("runtime sessions = %+v", updated.RuntimeSessions)
	}
	session := updated.RuntimeSessions[0]
	if session.Status != projection.RuntimeSessionCompleted || session.LastEvent != string(missionevent.EventAgentRunCompleted) || session.WorkerID != "worker-1" {
		t.Fatalf("runtime session = %+v", session)
	}
	if usage := session.Usage["codex"]; usage.InputTokens != 900 || usage.OutputTokens != 260 {
		t.Fatalf("runtime usage = %+v", session.Usage)
	}
}

func TestRuntimeLifecyclePublisherMapsTerminalFailureKinds(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 6, 6, 11, 30, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	if err := repo.Save(ctx, appRuntimeReadModel(at)); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	events := &capturingMissionEvents{}
	publisher := missioncontrol.NewRuntimeLifecyclePublisher(missioncontrol.RuntimeLifecyclePublisherOptions{
		Events:      events,
		Repo:        repo,
		IDGenerator: &sequenceContextIDGenerator{values: []string{"evt-timeout"}},
		Clock:       fixedClock{at: at},
	})

	if err := publisher.PublishRuntimeFailure(ctx, runtimedaemon.RuntimeFailureEvent{
		Kind:        runtimedaemon.RuntimeLifecycleTimedOut,
		MissionID:   "mission-1",
		TaskID:      "task-1",
		AgentID:     "agent-1",
		RunID:       "run-timeout",
		RuntimeKind: "codex",
		WorkerID:    "worker-1",
		Code:        "agent_runtime_timeout",
		Message:     "<think>private timeout analysis</think>\ndocument_id=timeout-doc",
	}); err != nil {
		t.Fatalf("PublishRuntimeFailure returned error: %v", err)
	}

	if len(events.events) != 1 || events.events[0].EventType != missionevent.EventAgentRunTimedOut {
		t.Fatalf("events = %+v, want AgentRunTimedOut", events.events)
	}
	payload, ok := events.events[0].Payload.(missionevent.RuntimeFailurePayload)
	if !ok || payload.Code != "agent_runtime_timeout" || payload.RunID != "run-timeout" || payload.Message != "任务执行超时，详细原因已记录到 Orrery。" {
		t.Fatalf("payload = %#v", events.events[0].Payload)
	}
	updated, err := repo.GetMissionControl(ctx, "mission-1")
	if err != nil {
		t.Fatal(err)
	}
	if updated.TaskBoard.Tasks[0].Status != projection.TaskStatusBlocked || len(updated.Blockers) != 1 || updated.Blockers[0].BlockerID != "runtime-failure:run-timeout" {
		t.Fatalf("timeout projection = %+v blockers=%+v", updated.TaskBoard.Tasks[0], updated.Blockers)
	}
}

func TestRuntimeLifecyclePublisherUsesEventStreamVersionWhenProjectionCheckpointLags(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 6, 6, 12, 30, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.Checkpoint.Version = 3
	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	events := &capturingMissionEvents{}
	publisher := missioncontrol.NewRuntimeLifecyclePublisher(missioncontrol.RuntimeLifecyclePublisherOptions{
		Events:      events,
		EventReader: capturingMissionEventReader{events: make([]missionevent.Envelope, 11)},
		Repo:        repo,
		IDGenerator: &sequenceContextIDGenerator{values: []string{"evt-started-lag"}},
		Clock:       fixedClock{at: at},
	})

	if err := publisher.PublishRuntimeLifecycle(ctx, runtimedaemon.RuntimeLifecycleEvent{
		Kind:        runtimedaemon.RuntimeLifecycleStarted,
		MissionID:   "mission-1",
		TaskID:      "task-1",
		AgentID:     "agent-1",
		RunID:       "run-lag",
		RuntimeKind: "managed",
		Status:      runtimedaemon.RunStatusRunning.String(),
		WorkerID:    "worker-1",
		Message:     "agent run started",
	}); err != nil {
		t.Fatalf("PublishRuntimeLifecycle returned error: %v", err)
	}
	if len(events.events) != 1 || events.events[0].StreamVersion != 12 {
		t.Fatalf("events = %+v, want stream version 12", events.events)
	}
	updated, err := repo.GetMissionControl(ctx, "mission-1")
	if err != nil {
		t.Fatalf("GetMissionControl returned error: %v", err)
	}
	if updated.Checkpoint.Version != 4 {
		t.Fatalf("checkpoint version = %d, want projection version 4", updated.Checkpoint.Version)
	}
	if len(updated.RuntimeSessions) != 1 || updated.RuntimeSessions[0].Status != projection.RuntimeSessionRunning {
		t.Fatalf("runtime sessions = %+v", updated.RuntimeSessions)
	}
}

func TestRuntimeLifecyclePublisherUpdatesOnlyTheRunTenant(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 7, 25, 12, 30, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	for _, tenantID := range []string{"tenant-a", "tenant-b"} {
		model := appRuntimeReadModel(at)
		model.TenantID = tenantID
		model.Mission.TenantID = tenantID
		if err := repo.SaveForTenant(ctx, tenantID, model); err != nil {
			t.Fatalf("SaveForTenant(%s): %v", tenantID, err)
		}
	}
	events := &capturingMissionEvents{}
	publisher := missioncontrol.NewRuntimeLifecyclePublisher(missioncontrol.RuntimeLifecyclePublisherOptions{
		Events: events, Repo: repo, IDGenerator: &sequenceContextIDGenerator{values: []string{"evt-tenant-a"}}, Clock: fixedClock{at: at},
	})

	if err := publisher.PublishRuntimeLifecycle(ctx, runtimedaemon.RuntimeLifecycleEvent{
		Kind: runtimedaemon.RuntimeLifecycleStarted, TenantID: "tenant-a", MissionID: "mission-1", TaskID: "task-1",
		AgentID: "agent-1", RunID: "run-tenant", RuntimeKind: "codex", Status: runtimedaemon.RunStatusRunning.String(),
	}); err != nil {
		t.Fatalf("PublishRuntimeLifecycle: %v", err)
	}
	if len(events.events) != 1 || events.events[0].Tenant() != "tenant-a" || events.events[0].StreamID != "tenant-a/mission-1" {
		t.Fatalf("events = %+v", events.events)
	}
	updatedA, err := repo.GetMissionControlForTenant(ctx, "tenant-a", "mission-1")
	if err != nil || len(updatedA.RuntimeSessions) != 1 {
		t.Fatalf("tenant-a projection = %+v, err=%v", updatedA, err)
	}
	updatedB, err := repo.GetMissionControlForTenant(ctx, "tenant-b", "mission-1")
	if err != nil || len(updatedB.RuntimeSessions) != 0 {
		t.Fatalf("tenant-b projection = %+v, err=%v", updatedB, err)
	}
}

func TestRuntimeLifecyclePublisherRetryRepairsProjectionWithoutDuplicatingDurableEvent(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 7, 26, 8, 0, 0, 0, time.UTC)
	base := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.Checkpoint.Version = 0
	model.RuntimeSessions = []projection.RuntimeSessionView{{RunID: "run-1", AgentID: "agent-1", TaskID: "task-1", Status: projection.RuntimeSessionRunning}}
	if err := base.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	repo := &failOnceRuntimeProjectionRepository{base: base, failSaves: 1}
	events := &durableRuntimeMissionEvents{}
	publisher := missioncontrol.NewRuntimeLifecyclePublisher(missioncontrol.RuntimeLifecyclePublisherOptions{
		Events: events, EventReader: events, Repo: repo,
		IDGenerator: &sequenceContextIDGenerator{values: []string{"evt-completed-first", "evt-completed-retry"}}, Clock: fixedClock{at: at},
	})
	event := runtimedaemon.RuntimeLifecycleEvent{
		Kind: runtimedaemon.RuntimeLifecycleCompleted, MissionID: "mission-1", TaskID: "task-1", AgentID: "agent-1", RunID: "run-1",
		Status: runtimedaemon.RunStatusCompleted.String(), Message: "durable result committed",
	}
	if err := publisher.PublishRuntimeLifecycle(ctx, event); err == nil {
		t.Fatal("first publish unexpectedly saved the projection")
	}
	if err := publisher.PublishRuntimeLifecycle(ctx, event); err != nil {
		t.Fatalf("retry publish: %v", err)
	}
	if len(events.events) != 1 {
		t.Fatalf("durable events = %+v, want one idempotent completion", events.events)
	}
	updated, err := base.GetMissionControl(ctx, "mission-1")
	if err != nil || updated.Checkpoint.Version != 1 || len(updated.RuntimeSessions) != 1 || updated.RuntimeSessions[0].Status != projection.RuntimeSessionCompleted {
		t.Fatalf("repaired projection = %+v err=%v", updated, err)
	}
}

func TestRuntimeLifecyclePublisherRetryRepairsCompletionSkippedByLaterCheckpoint(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 8, 9, 4, 0, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.Checkpoint.Version = 2
	model.RuntimeSessions = []projection.RuntimeSessionView{{
		RunID: "run-1", AgentID: "agent-1", TaskID: "task-1", Status: projection.RuntimeSessionRunning,
	}}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	completionKey := "agent-runtime|mission-1|run-1|AgentRunCompleted"
	events := &durableRuntimeMissionEvents{events: []missionevent.Envelope{
		{
			EventID: "evt-completed-durable", MissionID: "mission-1", StreamVersion: 1,
			EventType: missionevent.EventAgentRunCompleted, EventVersion: 1, IdempotencyKey: completionKey, OccurredAt: at,
			Payload: missionevent.RuntimeLifecyclePayload{
				TaskID: "task-1", AgentID: "agent-1", RunID: "run-1", Status: runtimedaemon.RunStatusCompleted.String(),
			},
		},
		{
			EventID: "evt-message-later", MissionID: "mission-1", StreamVersion: 2,
			EventType: missionevent.EventMessagePosted, EventVersion: 1, IdempotencyKey: "message-later", OccurredAt: at.Add(time.Second),
			Payload: missionevent.MessagePostedPayload{MessageID: "message-later", Body: "delivered"},
		},
	}}
	publisher := missioncontrol.NewRuntimeLifecyclePublisher(missioncontrol.RuntimeLifecyclePublisherOptions{
		Events: events, EventReader: events, Repo: repo,
		IDGenerator: &sequenceContextIDGenerator{values: []string{"evt-completed-retry"}}, Clock: fixedClock{at: at.Add(2 * time.Second)},
	})

	if err := publisher.PublishRuntimeLifecycle(ctx, runtimedaemon.RuntimeLifecycleEvent{
		Kind: runtimedaemon.RuntimeLifecycleCompleted, MissionID: "mission-1", TaskID: "task-1",
		AgentID: "agent-1", RunID: "run-1", Status: runtimedaemon.RunStatusCompleted.String(),
	}); err != nil {
		t.Fatalf("retry publish: %v", err)
	}

	updated, err := repo.GetMissionControl(ctx, "mission-1")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Checkpoint.Version != 2 || len(updated.RuntimeSessions) != 1 || updated.RuntimeSessions[0].Status != projection.RuntimeSessionCompleted {
		t.Fatalf("repaired projection = %+v", updated)
	}
	if len(events.events) != 2 {
		t.Fatalf("durable events = %+v", events.events)
	}
}

func TestRuntimeLifecyclePublisherRetryRepairsFailureTaskAndBlockerBehindCheckpoint(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.Checkpoint.Version = 2
	model.RuntimeSessions = []projection.RuntimeSessionView{{
		RunID: "run-1", AgentID: "agent-1", TaskID: "task-1", Status: projection.RuntimeSessionRunning,
	}}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	failureKey := "agent-runtime|mission-1|run-1|AgentRunFailed"
	events := &durableRuntimeMissionEvents{events: []missionevent.Envelope{
		{
			EventID: "evt-failed-durable", MissionID: "mission-1", StreamVersion: 1,
			EventType: missionevent.EventAgentRunFailed, EventVersion: 1, IdempotencyKey: failureKey, OccurredAt: at,
			Payload: missionevent.RuntimeFailurePayload{
				TaskID: "task-1", AgentID: "agent-1", RunID: "run-1", Code: "agent_runtime_failed", Message: "provider rejected tool name",
			},
		},
		{
			EventID: "evt-message-later", MissionID: "mission-1", StreamVersion: 2,
			EventType: missionevent.EventMessagePosted, EventVersion: 1, IdempotencyKey: "message-later", OccurredAt: at.Add(time.Second),
			Payload: missionevent.MessagePostedPayload{MessageID: "message-later", Body: "delivered"},
		},
	}}
	publisher := missioncontrol.NewRuntimeLifecyclePublisher(missioncontrol.RuntimeLifecyclePublisherOptions{
		Events: events, EventReader: events, Repo: repo,
		IDGenerator: &sequenceContextIDGenerator{values: []string{"evt-failed-retry"}}, Clock: fixedClock{at: at.Add(2 * time.Second)},
	})

	if err := publisher.PublishRuntimeFailure(ctx, runtimedaemon.RuntimeFailureEvent{
		Kind: runtimedaemon.RuntimeLifecycleFailed, MissionID: "mission-1", TaskID: "task-1",
		AgentID: "agent-1", RunID: "run-1", Code: "agent_runtime_failed", Message: "provider rejected tool name",
	}); err != nil {
		t.Fatalf("retry publish: %v", err)
	}

	updated, err := repo.GetMissionControl(ctx, "mission-1")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Checkpoint.Version != 2 || updated.RuntimeSessions[0].Status != projection.RuntimeSessionFailed {
		t.Fatalf("runtime repair = %+v", updated)
	}
	if updated.TaskBoard.Tasks[0].Status != projection.TaskStatusBlocked || len(updated.TaskBoard.Tasks[0].BlockerRefs) != 1 || len(updated.Blockers) != 1 {
		t.Fatalf("task repair = %+v blockers=%+v", updated.TaskBoard.Tasks[0], updated.Blockers)
	}
	if len(events.events) != 2 {
		t.Fatalf("durable events = %+v", events.events)
	}
}

func TestRuntimeLifecyclePublisherRejectsDifferentExistingTerminalForRun(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.Checkpoint.Version = 1
	model.RuntimeSessions = []projection.RuntimeSessionView{{
		RunID: "run-terminal", AgentID: "agent-1", TaskID: "task-1", Status: projection.RuntimeSessionRunning,
	}}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	cancelled := missionevent.Envelope{
		EventID: "evt-cancelled", MissionID: "mission-1", StreamVersion: 1,
		EventType: missionevent.EventAgentRunCancelled, EventVersion: 1,
		IdempotencyKey: "cancel-run-terminal", OccurredAt: at,
		Payload: missionevent.RuntimeFailurePayload{
			TaskID: "task-1", AgentID: "agent-1", RunID: "run-terminal", Code: "agent_runtime_cancelled", Message: "cancelled",
		},
	}
	events := &durableRuntimeMissionEvents{events: []missionevent.Envelope{cancelled}}
	publisher := missioncontrol.NewRuntimeLifecyclePublisher(missioncontrol.RuntimeLifecyclePublisherOptions{
		Events: events, EventReader: events, Repo: repo,
		IDGenerator: &sequenceContextIDGenerator{values: []string{"unused"}}, Clock: fixedClock{at: at.Add(time.Second)},
	})

	err := publisher.PublishRuntimeLifecycle(ctx, runtimedaemon.RuntimeLifecycleEvent{
		Kind: runtimedaemon.RuntimeLifecycleCompleted, MissionID: "mission-1", TaskID: "task-1",
		AgentID: "agent-1", RunID: "run-terminal", Status: runtimedaemon.RunStatusCompleted.String(),
	})
	if !errors.Is(err, missioncontrol.ErrAgentRunTerminal) {
		t.Fatalf("PublishRuntimeLifecycle error = %v, want ErrAgentRunTerminal", err)
	}
	if len(events.events) != 1 || events.events[0].EventType != missionevent.EventAgentRunCancelled {
		t.Fatalf("durable events = %+v, want only cancellation", events.events)
	}
}

func TestRuntimeLifecyclePublisherDoesNotTreatAnotherRunTerminalAsConflict(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 9, 3, 9, 15, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.Checkpoint.Version = 1
	model.RuntimeSessions = []projection.RuntimeSessionView{{
		RunID: "run-completing", AgentID: "agent-1", TaskID: "task-1", Status: projection.RuntimeSessionRunning,
	}}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	events := &durableRuntimeMissionEvents{events: []missionevent.Envelope{{
		EventID: "evt-other-cancelled", MissionID: "mission-1", StreamVersion: 1,
		EventType: missionevent.EventAgentRunCancelled, EventVersion: 1,
		IdempotencyKey: "cancel-other-run", OccurredAt: at,
		Payload: missionevent.RuntimeFailurePayload{
			TaskID: "task-other", AgentID: "agent-2", RunID: "run-other", Code: "agent_runtime_cancelled", Message: "cancelled",
		},
	}}}
	publisher := missioncontrol.NewRuntimeLifecyclePublisher(missioncontrol.RuntimeLifecyclePublisherOptions{
		Events: events, EventReader: events, Repo: repo,
		IDGenerator: &sequenceContextIDGenerator{values: []string{"evt-completed"}}, Clock: fixedClock{at: at.Add(time.Second)},
	})

	if err := publisher.PublishRuntimeLifecycle(ctx, runtimedaemon.RuntimeLifecycleEvent{
		Kind: runtimedaemon.RuntimeLifecycleCompleted, MissionID: "mission-1", TaskID: "task-1",
		AgentID: "agent-1", RunID: "run-completing", Status: runtimedaemon.RunStatusCompleted.String(),
	}); err != nil {
		t.Fatalf("PublishRuntimeLifecycle: %v", err)
	}
	if len(events.events) != 2 || events.events[1].EventType != missionevent.EventAgentRunCompleted || events.events[1].StreamVersion != 2 {
		t.Fatalf("durable events = %+v, want other-run cancellation then completion", events.events)
	}
}

func TestRuntimeLifecyclePublisherDetectsDifferentTerminalWinningAppendRace(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 9, 3, 9, 30, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.Checkpoint.Version = 0
	model.RuntimeSessions = []projection.RuntimeSessionView{{
		RunID: "run-terminal-race", AgentID: "agent-1", TaskID: "task-1", Status: projection.RuntimeSessionRunning,
	}}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	events := &terminalWinningRuntimeMissionEvents{winner: missionevent.Envelope{
		EventID: "evt-cancelled-winner", MissionID: "mission-1", StreamVersion: 1,
		EventType: missionevent.EventAgentRunCancelled, EventVersion: 1,
		IdempotencyKey: "cancel-run-terminal-race", OccurredAt: at,
		Payload: missionevent.RuntimeFailurePayload{
			TaskID: "task-1", AgentID: "agent-1", RunID: "run-terminal-race", Code: "agent_runtime_cancelled", Message: "cancelled",
		},
	}}
	publisher := missioncontrol.NewRuntimeLifecyclePublisher(missioncontrol.RuntimeLifecyclePublisherOptions{
		Events: events, EventReader: events, Repo: repo,
		IDGenerator: &sequenceContextIDGenerator{values: []string{"evt-completed-loser"}}, Clock: fixedClock{at: at.Add(time.Second)},
	})

	err := publisher.PublishRuntimeLifecycle(ctx, runtimedaemon.RuntimeLifecycleEvent{
		Kind: runtimedaemon.RuntimeLifecycleCompleted, MissionID: "mission-1", TaskID: "task-1",
		AgentID: "agent-1", RunID: "run-terminal-race", Status: runtimedaemon.RunStatusCompleted.String(),
	})
	if !errors.Is(err, missioncontrol.ErrAgentRunTerminal) {
		t.Fatalf("PublishRuntimeLifecycle error = %v, want ErrAgentRunTerminal", err)
	}
	if events.appendCalls != 1 || len(events.events) != 1 || events.events[0].EventType != missionevent.EventAgentRunCancelled {
		t.Fatalf("append calls = %d durable events = %+v, want only winning cancellation", events.appendCalls, events.events)
	}
}

func TestRuntimeLifecyclePublisherRetriesWhenUnrelatedEventWinsAppendRace(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 9, 3, 9, 45, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.Checkpoint.Version = 1
	model.RuntimeSessions = []projection.RuntimeSessionView{{
		RunID: "run-after-conflict", AgentID: "agent-1", TaskID: "task-1", Status: projection.RuntimeSessionRunning,
	}}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	events := &terminalWinningRuntimeMissionEvents{winner: missionevent.Envelope{
		EventID: "evt-unrelated-winner", MissionID: "mission-1", StreamVersion: 1,
		EventType: missionevent.EventMessagePosted, EventVersion: 1,
		IdempotencyKey: "unrelated-message", OccurredAt: at,
		Payload: missionevent.MessagePostedPayload{MessageID: "message-1", Body: "unrelated"},
	}}
	publisher := missioncontrol.NewRuntimeLifecyclePublisher(missioncontrol.RuntimeLifecyclePublisherOptions{
		Events: events, EventReader: events, Repo: repo,
		IDGenerator: &sequenceContextIDGenerator{values: []string{"evt-completed-after-conflict"}}, Clock: fixedClock{at: at.Add(time.Second)},
	})

	if err := publisher.PublishRuntimeLifecycle(ctx, runtimedaemon.RuntimeLifecycleEvent{
		Kind: runtimedaemon.RuntimeLifecycleCompleted, MissionID: "mission-1", TaskID: "task-1",
		AgentID: "agent-1", RunID: "run-after-conflict", Status: runtimedaemon.RunStatusCompleted.String(),
	}); err != nil {
		t.Fatalf("PublishRuntimeLifecycle: %v", err)
	}
	if events.appendCalls != 2 || len(events.events) != 2 || events.events[1].EventType != missionevent.EventAgentRunCompleted || events.events[1].StreamVersion != 2 {
		t.Fatalf("append calls = %d durable events = %+v, want retry at stream version 2", events.appendCalls, events.events)
	}
}

type capturingMissionEvents struct {
	events []missionevent.Envelope
}

type durableRuntimeMissionEvents struct {
	events []missionevent.Envelope
}

type terminalWinningRuntimeMissionEvents struct {
	events      []missionevent.Envelope
	winner      missionevent.Envelope
	appendCalls int
}

func (e *terminalWinningRuntimeMissionEvents) Append(_ context.Context, event missionevent.Envelope) error {
	e.appendCalls++
	if len(e.events) == 0 {
		e.events = append(e.events, e.winner)
		return eventstore.ErrConflict
	}
	e.events = append(e.events, event)
	return nil
}

func (e *terminalWinningRuntimeMissionEvents) LoadMission(_ context.Context, _ missioncore.MissionID) ([]missionevent.Envelope, error) {
	return append([]missionevent.Envelope(nil), e.events...), nil
}

func (e *durableRuntimeMissionEvents) Append(_ context.Context, event missionevent.Envelope) error {
	for _, existing := range e.events {
		if event.IdempotencyKey != "" && existing.IdempotencyKey == event.IdempotencyKey {
			return nil
		}
	}
	e.events = append(e.events, event)
	return nil
}

func (e *durableRuntimeMissionEvents) LoadMission(_ context.Context, _ missioncore.MissionID) ([]missionevent.Envelope, error) {
	return append([]missionevent.Envelope(nil), e.events...), nil
}

type failOnceRuntimeProjectionRepository struct {
	base      *missioncontrol.MemoryProjectionRepository
	failSaves int
}

func (r *failOnceRuntimeProjectionRepository) GetMissionControl(ctx context.Context, missionID missioncore.MissionID) (projection.ReadModel, error) {
	return r.base.GetMissionControl(ctx, missionID)
}

func (r *failOnceRuntimeProjectionRepository) Save(ctx context.Context, model projection.ReadModel) error {
	if r.failSaves > 0 {
		r.failSaves--
		return errors.New("projection store unavailable")
	}
	return r.base.Save(ctx, model)
}

func (e *capturingMissionEvents) Append(_ context.Context, event missionevent.Envelope) error {
	e.events = append(e.events, event)
	return nil
}
