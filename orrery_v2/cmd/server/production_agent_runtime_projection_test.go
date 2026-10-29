package main

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	agentlifecycle "orrery_v2/internal/agent/lifecycle"
	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/platform/testkit"
)

func TestMissionLifecycleProjectionHandlerAppendsAtNextTenantStreamVersion(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	sink := missionevent.NewStoreSink(env.EventStore)
	reader := missionevent.NewStoreReader(env.EventStore)
	missionID := missioncore.MissionID("mission-tenant-a")
	at := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	if err := sink.Append(ctx, missionevent.Envelope{
		EventID: "event-existing", TenantID: "tenant-a", MissionID: missionID,
		StreamID: missionevent.MissionStreamID("tenant-a", missionID), StreamVersion: 1,
		EventType: missionevent.EventTaskStarted, EventVersion: 1,
		Actor:          missionevent.ActorRef{Kind: missionevent.ActorKindSystem, ID: "runtime"},
		IdempotencyKey: "existing", OccurredAt: at,
		Payload: missionevent.TaskStatusPayload{TaskID: "task-1"},
	}); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	handler := missionLifecycleProjectionHandler{mapper: missionevent.NewSignalMapper(), sink: sink, reader: reader}
	signal := agentlifecycle.SignalEnvelope{
		SignalID: "signal-accepted", Name: agentlifecycle.AfterTaskAccepted, TenantID: "tenant-a",
		MissionID: missionID, AgentID: "agent-1", AgentProfileID: "profile-1", TaskID: "task-1", RunID: "run-1",
		CorrelationID: "run-1", Attempt: 1, EmittedAt: at.Add(time.Second),
		Payload: agentlifecycle.TaskAcceptedPayload{AcceptedBy: agentcore.AgentID("agent-1")},
	}
	if err := handler.HandleSignal(ctx, signal); err != nil {
		t.Fatalf("HandleSignal: %v", err)
	}
	if err := handler.HandleSignal(ctx, signal); err != nil {
		t.Fatalf("idempotent HandleSignal: %v", err)
	}
	events, err := reader.LoadMissionForTenant(ctx, "tenant-a", missionID)
	if err != nil {
		t.Fatalf("LoadMissionForTenant: %v", err)
	}
	if len(events) != 2 || events[1].StreamVersion != 2 || events[1].EventType != missionevent.EventTaskAccepted {
		t.Fatalf("tenant mission events = %+v", events)
	}
	global, err := reader.LoadMission(ctx, missionID)
	if err != nil {
		t.Fatalf("LoadMission global: %v", err)
	}
	if len(global) != 0 {
		t.Fatalf("tenant lifecycle event leaked into global stream: %+v", global)
	}
}

func TestMissionLifecycleProjectionHandlerDefersWorkerTerminalSignals(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	sink := missionevent.NewStoreSink(env.EventStore)
	reader := missionevent.NewStoreReader(env.EventStore)
	handler := missionLifecycleProjectionHandler{mapper: missionevent.NewSignalMapper(), sink: sink, reader: reader}
	at := time.Date(2026, 7, 26, 11, 0, 0, 0, time.UTC)
	signal := agentlifecycle.SignalEnvelope{
		SignalID: "signal-worker-failed", Name: agentlifecycle.AgentFailed, TenantID: "tenant-a",
		MissionID: "mission-1", AgentID: "agent-1", AgentProfileID: "profile-1", TaskID: "task-1", RunID: "run-1",
		CorrelationID: "run-1", Attempt: 1, EmittedAt: at,
		Payload:  agentlifecycle.FailurePayload{Code: "failed", Message: "runtime failed"},
		Metadata: map[string]string{agentcore.MissionLifecycleAuthorityProviderKey: agentcore.MissionLifecycleAuthorityRunWorker},
	}
	if err := handler.HandleSignal(ctx, signal); err != nil {
		t.Fatal(err)
	}
	events, err := reader.LoadMissionForTenant(ctx, "tenant-a", "mission-1")
	if err != nil || len(events) != 0 {
		t.Fatalf("worker-owned signal events = %+v err=%v", events, err)
	}
	delete(signal.Metadata, agentcore.MissionLifecycleAuthorityProviderKey)
	signal.SignalID = "signal-unowned-failed"
	if err := handler.HandleSignal(ctx, signal); err != nil {
		t.Fatal(err)
	}
	events, err = reader.LoadMissionForTenant(ctx, "tenant-a", "mission-1")
	if err != nil || len(events) != 1 || events[0].EventType != missionevent.EventAgentRunFailed {
		t.Fatalf("unowned signal events = %+v err=%v", events, err)
	}
}
