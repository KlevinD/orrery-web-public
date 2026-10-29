package missioncontrol_test

import (
	"context"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/missioncontrol"
	missionevent "orrery_v2/internal/mission/event"
)

func TestCommandServicePersistsQueuedEventBeforeReleasingRuntime(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 9, 20, 4, 0, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	if err := repo.Save(ctx, appRuntimeReadModel(at)); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	order := make([]string, 0, 2)
	runStore := &orderedRunStore{order: &order}
	events := &orderedMissionEventSink{order: &order}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, events, &sequenceContextIDGenerator{values: []string{"run-order", "evt-order"}}, fixedClock{at: at})

	if _, err := service.StartAgentRun(ctx, missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}, missioncontrol.StartAgentRunCommand{
		RequestID: "order-check", IdempotencyKey: "order-check", MissionID: "mission-1", TaskID: "task-1", AgentID: "agent-1", RuntimeKind: "codex",
	}); err != nil {
		t.Fatalf("StartAgentRun returned error: %v", err)
	}
	if strings.Join(order, ",") != "event,enqueue" {
		t.Fatalf("side-effect order = %v, want event,enqueue", order)
	}
}

type orderedRunStore struct {
	order *[]string
}

func (s *orderedRunStore) Enqueue(_ context.Context, _ runtimedaemon.AgentRun) error {
	*s.order = append(*s.order, "enqueue")
	return nil
}

func (s *orderedRunStore) Cancel(_ context.Context, _ agentcore.AgentRunID, _ string) error {
	return nil
}

type orderedMissionEventSink struct {
	order *[]string
}

func (s *orderedMissionEventSink) Append(_ context.Context, _ missionevent.Envelope) error {
	*s.order = append(*s.order, "event")
	return nil
}
