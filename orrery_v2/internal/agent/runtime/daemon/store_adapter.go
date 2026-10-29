package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/execution"
)

const AgentRunTopic = "agent.runtime.run"

type Queue interface {
	Enqueue(ctx context.Context, job execution.Job) error
	Claim(ctx context.Context, workerID string) (execution.Job, bool, error)
	Complete(ctx context.Context, jobID string) error
	Fail(ctx context.Context, jobID string) error
}

type ResponseStore interface {
	SaveResponse(ctx context.Context, runID agentcore.AgentRunID, response agentcore.RuntimeResponse) error
	SaveFailure(ctx context.Context, runID agentcore.AgentRunID, message string) error
}

type PlatformRunStore struct {
	Queue     Queue
	Liveness  execution.LivenessBackend
	Responses ResponseStore
	LeaseTTL  time.Duration
	Now       func() time.Time
}

func (s PlatformRunStore) Enqueue(ctx context.Context, run AgentRun) error {
	job, err := ExecutionJobFromAgentRun(run)
	if err != nil {
		return err
	}
	return s.Queue.Enqueue(ctx, job)
}

func ExecutionJobFromAgentRun(run AgentRun) (execution.Job, error) {
	if err := validateAgentRunForEnqueue(run); err != nil {
		return execution.Job{}, err
	}
	payload, err := json.Marshal(run)
	if err != nil {
		return execution.Job{}, fmt.Errorf("marshal agent run: %w", err)
	}
	return execution.Job{
		ID:          run.RunID.String(),
		Topic:       AgentRunTopic,
		Payload:     payload,
		QueueKey:    runQueueID(run),
		Status:      execution.StatusQueued,
		MaxAttempts: 3,
	}, nil
}

func DecodeAgentRunPayload(payload []byte) (AgentRun, error) {
	var run AgentRun
	if err := json.Unmarshal(payload, &run); err != nil {
		return AgentRun{}, fmt.Errorf("decode agent run payload: %w", err)
	}
	run.Request.RuntimeOptions.ProviderConfig = normalizeQueuedOptionalJSON(run.Request.RuntimeOptions.ProviderConfig)
	run.Request.RuntimeOptions.MCPConfig = normalizeQueuedOptionalJSON(run.Request.RuntimeOptions.MCPConfig)
	return run, nil
}

func normalizeQueuedOptionalJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	return raw
}

func (s PlatformRunStore) Claim(ctx context.Context, workerID string) (AgentRun, bool, error) {
	if s.Queue == nil {
		return AgentRun{}, false, fmt.Errorf("platform execution queue is required")
	}
	job, ok, err := s.Queue.Claim(ctx, workerID)
	if err != nil || !ok {
		return AgentRun{}, ok, err
	}
	run, err := DecodeAgentRunPayload(job.Payload)
	if err != nil {
		_ = s.Queue.Fail(ctx, job.ID)
		return AgentRun{}, false, err
	}
	now := s.now()
	run.Status = RunStatusRunning
	run.ClaimedBy = workerID
	run.Attempt = job.Attempts + 1
	run.HeartbeatAt = now
	run.LeaseExpiresAt = now.Add(s.leaseTTL())
	if s.Liveness != nil {
		s.Liveness.Touch(ctx, runtimeLivenessID(run.RunID), s.leaseTTL(), now)
	}
	return run, true, nil
}

func (s PlatformRunStore) Complete(ctx context.Context, runID agentcore.AgentRunID, response agentcore.RuntimeResponse) error {
	if s.Responses != nil {
		if err := s.Responses.SaveResponse(ctx, runID, response); err != nil {
			return err
		}
	}
	if s.Liveness != nil {
		s.Liveness.Forget(ctx, runtimeLivenessID(runID))
	}
	return s.Queue.Complete(ctx, runID.String())
}

func (s PlatformRunStore) Fail(ctx context.Context, runID agentcore.AgentRunID, message string) error {
	if s.Responses != nil {
		if err := s.Responses.SaveFailure(ctx, runID, message); err != nil {
			return err
		}
	}
	if s.Liveness != nil {
		s.Liveness.Forget(ctx, runtimeLivenessID(runID))
	}
	return s.Queue.Fail(ctx, runID.String())
}

func (s PlatformRunStore) Block(ctx context.Context, runID agentcore.AgentRunID, message string, response agentcore.RuntimeResponse) error {
	if s.Responses != nil {
		if response.RunID == "" {
			response.RunID = runID
		}
		if response.Status == "" {
			response.Status = agentcore.RuntimeStatusBlocked
		}
		if response.Error.Message == "" {
			response.Error.Message = message
		}
		if err := s.Responses.SaveResponse(ctx, runID, response); err != nil {
			return err
		}
	}
	if s.Liveness != nil {
		s.Liveness.Forget(ctx, runtimeLivenessID(runID))
	}
	return s.Queue.Complete(ctx, runID.String())
}

func (s PlatformRunStore) Heartbeat(ctx context.Context, runID agentcore.AgentRunID, _ string) error {
	if s.Liveness != nil {
		s.Liveness.Touch(ctx, runtimeLivenessID(runID), s.leaseTTL(), s.now())
	}
	return nil
}

func (s PlatformRunStore) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s PlatformRunStore) leaseTTL() time.Duration {
	if s.LeaseTTL > 0 {
		return s.LeaseTTL
	}
	return time.Minute
}

func runtimeLivenessID(runID agentcore.AgentRunID) string {
	return "agent-run:" + runID.String()
}

func validateAgentRunForEnqueue(run AgentRun) error {
	if run.RunID == "" {
		return fmt.Errorf("run id is required")
	}
	if run.AgentID.IsZero() {
		return fmt.Errorf("agent id is required")
	}
	if run.AgentProfileID.IsZero() {
		return fmt.Errorf("agent profile id is required")
	}
	if run.AgentInstanceID.IsZero() {
		return fmt.Errorf("agent instance id is required")
	}
	return nil
}
