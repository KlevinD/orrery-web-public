package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/platform/config"
)

func TestConfiguredMissionAgentRunTimeoutDefaultsToOneHour(t *testing.T) {
	if got := configuredMissionAgentRunTimeout(config.Config{}); got != time.Hour {
		t.Fatalf("mission Agent timeout = %s, want 1h", got)
	}
}

func TestAgentRunLivenessHeartbeatTouchesAndForgetsRuntime(t *testing.T) {
	liveness := &capturingAgentRunLiveness{}
	stop := startAgentRunLivenessHeartbeat(context.Background(), liveness, "run-1", time.Millisecond, nil)
	deadline := time.Now().Add(time.Second)
	for liveness.touchCount() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	stop()
	touches, forgets, runtimeID := liveness.snapshot()
	if touches < 2 || forgets != 1 || runtimeID != "agent-run:run-1" {
		t.Fatalf("liveness touches=%d forgets=%d runtimeID=%q", touches, forgets, runtimeID)
	}
}

type capturingAgentRunLiveness struct {
	mu                   sync.Mutex
	touches, forgets     int
	lastRuntimeIdentifier string
}

func (l *capturingAgentRunLiveness) Touch(_ context.Context, runtimeID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.touches++
	l.lastRuntimeIdentifier = runtimeID
	return nil
}

func (l *capturingAgentRunLiveness) Forget(_ context.Context, runtimeID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.forgets++
	l.lastRuntimeIdentifier = runtimeID
	return nil
}

func (l *capturingAgentRunLiveness) touchCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.touches
}

func (l *capturingAgentRunLiveness) snapshot() (int, int, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.touches, l.forgets, l.lastRuntimeIdentifier
}
