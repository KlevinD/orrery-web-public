package missioncontrol

import (
	"context"

	agentcore "orrery_v2/internal/agent/core"
)

type RuntimeRunCancelDisposition string

const (
	RuntimeRunCancelApplied          RuntimeRunCancelDisposition = "applied"
	RuntimeRunCancelAlreadyCancelled RuntimeRunCancelDisposition = "already_cancelled"
	RuntimeRunCancelAlreadyTerminal  RuntimeRunCancelDisposition = "already_terminal"
	RuntimeRunCancelMissing          RuntimeRunCancelDisposition = "missing"
)

type RuntimeRunCancelResult struct {
	Disposition RuntimeRunCancelDisposition
	Status      string
}

type RuntimeConditionalRunStorePort interface {
	CancelWithResult(ctx context.Context, runID agentcore.AgentRunID, reason string) (RuntimeRunCancelResult, error)
}
