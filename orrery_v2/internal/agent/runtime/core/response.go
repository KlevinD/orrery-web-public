package core

import agentcore "orrery_v2/internal/agent/core"

type RuntimeStatus = agentcore.RuntimeStatus

const (
	RuntimeStatusCompleted = agentcore.RuntimeStatusCompleted
	RuntimeStatusFailed    = agentcore.RuntimeStatusFailed
	RuntimeStatusBlocked   = agentcore.RuntimeStatusBlocked
	RuntimeStatusTimeout   = agentcore.RuntimeStatusTimeout
	RuntimeStatusCancelled = agentcore.RuntimeStatusCancelled
)

type RuntimeResponse = agentcore.RuntimeResponse
type RuntimeFailure = agentcore.RuntimeFailure
type TokenUsage = agentcore.TokenUsage
