package main

import (
	"context"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/platform/execution"
	mcptransport "orrery_v2/internal/transport/mcp"
)

type responsibilityExecutionJobSource interface {
	Get(ctx context.Context, jobID string) (execution.Job, bool, error)
}

type boundExecutionEnvelopeSource interface {
	BoundExecutionEnvelope(agentcore.AgentRunID) (*agentcore.ExecutionEnvelope, *agentcore.ResponsibilityContext, bool)
}

// productionResponsibilityExecutionAuthority reads the queued Agent run back
// from server-owned storage. Runtime headers can identify a snapshot, but they
// cannot broaden the permission scopes persisted with that run.
type productionResponsibilityExecutionAuthority struct {
	jobs     responsibilityExecutionJobSource
	sessions boundExecutionEnvelopeSource
}

func (a productionResponsibilityExecutionAuthority) ResolveResponsibilityExecution(ctx context.Context, runID string) (*mcptransport.ResponsibilityExecutionAuthority, error) {
	runID = strings.TrimSpace(runID)
	if a.jobs == nil || runID == "" {
		return nil, mcptransport.ErrInvalidRequest
	}
	job, found, err := a.jobs.Get(ctx, runID)
	if err != nil {
		return nil, err
	}
	if !found || job.Topic != runtimedaemon.AgentRunTopic {
		if a.sessions == nil {
			return nil, nil
		}
		envelope, responsibility, bound := a.sessions.BoundExecutionEnvelope(agentcore.AgentRunID(runID))
		if !bound || envelope == nil {
			return nil, nil
		}
		return &mcptransport.ResponsibilityExecutionAuthority{
			RunID: runID, TenantID: envelope.TenantID, AgentID: envelope.AgentID,
			AgentProfileID: envelope.AgentProfileID, Context: responsibility, Envelope: envelope,
		}, nil
	}
	run, err := runtimedaemon.DecodeAgentRunPayload(job.Payload)
	if err != nil || run.RunID.String() != runID {
		return nil, mcptransport.ErrInvalidRequest
	}
	responsibility := agentcore.CloneResponsibilityContext(run.Request.ContextPack.ResponsibilityContext)
	envelope := agentcore.CloneExecutionEnvelope(run.Request.ExecutionEnvelope)
	if responsibility == nil && envelope == nil {
		return nil, nil
	}
	return &mcptransport.ResponsibilityExecutionAuthority{
		RunID:          run.RunID.String(),
		TenantID:       strings.TrimSpace(run.Request.TenantID),
		AgentID:        strings.TrimSpace(run.AgentID.String()),
		AgentProfileID: strings.TrimSpace(run.AgentProfileID.String()),
		Context:        responsibility,
		Envelope:       envelope,
	}, nil
}
