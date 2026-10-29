package daemon

import (
	"context"
	"fmt"
	"sync"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type memoryRunStore struct {
	mu         sync.Mutex
	runs       map[agentcore.AgentRunID]AgentRun
	heartbeats int
}

func newMemoryRunStore() *memoryRunStore {
	return &memoryRunStore{runs: map[agentcore.AgentRunID]AgentRun{}}
}

func (s *memoryRunStore) Enqueue(ctx context.Context, run AgentRun) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateAgentRunForEnqueue(run); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if run.Status == "" {
		run.Status = RunStatusQueued
	}
	s.runs[run.RunID] = run
	return nil
}

func (s *memoryRunStore) Claim(ctx context.Context, workerID string) (AgentRun, bool, error) {
	if err := ctx.Err(); err != nil {
		return AgentRun{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, run := range s.runs {
		if run.Status != RunStatusQueued {
			continue
		}
		run.Status = RunStatusRunning
		run.ClaimedBy = workerID
		run.Attempt++
		run.HeartbeatAt = time.Now().UTC()
		s.runs[id] = run
		return run, true, nil
	}
	return AgentRun{}, false, nil
}

func (s *memoryRunStore) Complete(ctx context.Context, runID agentcore.AgentRunID, response agentcore.RuntimeResponse) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[runID]
	run.Status = RunStatusCompleted
	run.Response = response
	s.runs[runID] = run
	return nil
}

func (s *memoryRunStore) Fail(ctx context.Context, runID agentcore.AgentRunID, message string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[runID]
	run.Status = RunStatusFailed
	run.LastError = message
	s.runs[runID] = run
	return nil
}

func (s *memoryRunStore) Block(ctx context.Context, runID agentcore.AgentRunID, message string, response agentcore.RuntimeResponse) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[runID]
	run.Status = RunStatusBlocked
	run.LastError = message
	run.Response = response
	s.runs[runID] = run
	return nil
}

func (s *memoryRunStore) Heartbeat(ctx context.Context, runID agentcore.AgentRunID, workerID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[runID]
	if run.ClaimedBy != workerID {
		return fmt.Errorf("run claimed by another worker")
	}
	s.heartbeats++
	run.HeartbeatAt = time.Now().UTC()
	s.runs[runID] = run
	return nil
}

func (s *memoryRunStore) get(runID agentcore.AgentRunID) (AgentRun, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[runID]
	return run, ok
}
