package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/platform/execution"
)

type productionAgentRunQueue struct {
	queue *execution.QueueService
}

func (q productionAgentRunQueue) Enqueue(ctx context.Context, run runtimedaemon.AgentRun) error {
	if q.queue == nil {
		return fmt.Errorf("agent runtime execution queue is not configured")
	}
	job, err := runtimedaemon.ExecutionJobFromAgentRun(run)
	if err != nil {
		return err
	}
	if raw := strings.TrimSpace(run.Request.RuntimeOptions.ProviderSpecific["max_attempts"]); raw != "" {
		if maxAttempts, parseErr := strconv.Atoi(raw); parseErr == nil && maxAttempts > 0 && maxAttempts <= 21 {
			job.MaxAttempts = maxAttempts
		}
	}
	return q.queue.Enqueue(ctx, job)
}

func (q productionAgentRunQueue) Cancel(ctx context.Context, runID agentcore.AgentRunID, reason string) error {
	if q.queue == nil {
		return fmt.Errorf("agent runtime execution queue is not configured")
	}
	return q.queue.Cancel(ctx, runID.String(), reason)
}

func (q productionAgentRunQueue) CancelWithResult(ctx context.Context, runID agentcore.AgentRunID, reason string) (missioncontrol.RuntimeRunCancelResult, error) {
	if q.queue == nil {
		return missioncontrol.RuntimeRunCancelResult{}, fmt.Errorf("agent runtime execution queue is not configured")
	}
	result, err := q.queue.CancelWithResult(ctx, runID.String(), reason)
	if err != nil {
		return missioncontrol.RuntimeRunCancelResult{}, err
	}
	disposition := missioncontrol.RuntimeRunCancelDisposition(result.Disposition)
	return missioncontrol.RuntimeRunCancelResult{Disposition: disposition, Status: string(result.Status)}, nil
}
