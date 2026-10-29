package main

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	missioncore "orrery_v2/internal/mission/core"
)

func TestProductionRuntimeLifecyclePublisherDoesNotFailRunForOntologyProjection(t *testing.T) {
	projectionErr := errors.New("ontology storage unavailable")
	mission := &runtimeLifecyclePublisherStub{}
	ontology := &runtimeLifecyclePublisherStub{lifecycleErr: projectionErr, failureErr: projectionErr}
	publisher := productionRuntimeLifecyclePublisher{mission: mission, ontology: ontology}
	event := runtimedaemon.RuntimeLifecycleEvent{MissionID: "mission-1", TaskID: "task-1", AgentID: "agent-1", RunID: "run-1"}

	if err := publisher.PublishRuntimeLifecycle(context.Background(), event); err != nil {
		t.Fatalf("PublishRuntimeLifecycle returned secondary projection error: %v", err)
	}
	if err := publisher.PublishRuntimeFailure(context.Background(), runtimedaemon.RuntimeFailureEvent{
		MissionID: "mission-1", TaskID: "task-1", AgentID: "agent-1", RunID: "run-1",
	}); err != nil {
		t.Fatalf("PublishRuntimeFailure returned secondary projection error: %v", err)
	}
	if mission.lifecycleCalls != 1 || mission.failureCalls != 1 || ontology.lifecycleCalls != 1 || ontology.failureCalls != 1 {
		t.Fatalf("publisher calls mission=%+v ontology=%+v", mission, ontology)
	}
}

func TestProductionRuntimeLifecyclePublisherReturnsMissionProjectionFailure(t *testing.T) {
	missionErr := errors.New("mission projection unavailable")
	mission := &runtimeLifecyclePublisherStub{lifecycleErr: missionErr}
	ontology := &runtimeLifecyclePublisherStub{}
	publisher := productionRuntimeLifecyclePublisher{mission: mission, ontology: ontology}

	err := publisher.PublishRuntimeLifecycle(context.Background(), runtimedaemon.RuntimeLifecycleEvent{
		MissionID: missioncore.MissionID("mission-1"), TaskID: missioncore.TaskID("task-1"), AgentID: agentcore.AgentID("agent-1"), RunID: agentcore.AgentRunID("run-1"),
	})
	if !errors.Is(err, missionErr) || ontology.lifecycleCalls != 0 {
		t.Fatalf("mission error=%v ontology calls=%d", err, ontology.lifecycleCalls)
	}
}

func TestProductionRuntimeLifecyclePublisherReturnsOntologyFailureWithoutMissionProjection(t *testing.T) {
	projectionErr := errors.New("ontology storage unavailable")
	ontology := &runtimeLifecyclePublisherStub{lifecycleErr: projectionErr}
	publisher := productionRuntimeLifecyclePublisher{ontology: ontology}

	err := publisher.PublishRuntimeLifecycle(context.Background(), runtimedaemon.RuntimeLifecycleEvent{RunID: "run-1"})
	if !errors.Is(err, projectionErr) {
		t.Fatalf("ontology-only lifecycle error = %v", err)
	}
}

type runtimeLifecyclePublisherStub struct {
	lifecycleErr                 error
	failureErr                   error
	lifecycleCalls, failureCalls int
}

func (s *runtimeLifecyclePublisherStub) PublishRuntimeLifecycle(context.Context, runtimedaemon.RuntimeLifecycleEvent) error {
	s.lifecycleCalls++
	return s.lifecycleErr
}

func (s *runtimeLifecyclePublisherStub) PublishRuntimeFailure(context.Context, runtimedaemon.RuntimeFailureEvent) error {
	s.failureCalls++
	return s.failureErr
}
