package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

var captureStateFailure = errors.New("state write unavailable")
var capturePublishFailure = errors.New("event publication unavailable")

type failingCaptureStore struct {
	RunStore
	calls int
	alive bool
}

func (s *failingCaptureStore) Fail(ctx context.Context, _ agentcore.AgentRunID, _ string) error {
	s.calls++
	s.alive = ctx.Err() == nil
	return captureStateFailure
}
func (s *failingCaptureStore) Block(ctx context.Context, _ agentcore.AgentRunID, _ string, _ agentcore.RuntimeResponse) error {
	s.calls++
	s.alive = ctx.Err() == nil
	return captureStateFailure
}

type failingCapturePublisher struct {
	calls int
	alive bool
}

func (s *failingCapturePublisher) PublishRuntimeLifecycle(context.Context, RuntimeLifecycleEvent) error {
	return nil
}
func (s *failingCapturePublisher) PublishRuntimeFailure(ctx context.Context, _ RuntimeFailureEvent) error {
	s.calls++
	s.alive = ctx.Err() == nil
	return capturePublishFailure
}

func TestWorkerPreservesStateAndPublicationFailuresAfterExecutionCancellation(t *testing.T) {
	for _, kind := range []RuntimeLifecycleKind{RuntimeLifecycleFailed, RuntimeLifecycleBlocked} {
		t.Run(string(kind), func(t *testing.T) {
			store := &failingCaptureStore{RunStore: newMemoryRunStore()}
			publisher := &failingCapturePublisher{}
			worker := NewWorker(WorkerOptions{Store: store, Lifecycle: publisher})
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := worker.failRun(ctx, AgentRun{RunID: "run-1", MissionID: "mission-1", TaskID: "task-1", AgentID: "agent-1"}, "worker-1", kind, "failed", "failure", agentcore.RuntimeResponse{}, nil)
			if !errors.Is(err, captureStateFailure) || !errors.Is(err, capturePublishFailure) {
				t.Fatalf("secondary error lost: %v", err)
			}
			if store.calls != 1 || publisher.calls != 1 || !store.alive || !publisher.alive {
				t.Fatalf("failure reporting inherited cancelled lease: %+v %+v", store, publisher)
			}
			if !strings.Contains(err.Error(), "persist") || !strings.Contains(err.Error(), "publish") {
				t.Fatalf("failure stages missing: %v", err)
			}
		})
	}
}
