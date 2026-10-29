package projection

import (
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	missionevent "orrery_v2/internal/mission/event"
)

func runtimeSessionFromLifecycle(event missionevent.Envelope, payload missionevent.RuntimeLifecyclePayload) RuntimeSessionView {
	session := RuntimeSessionView{
		RunID:                 payload.RunID,
		AgentID:               payload.AgentID,
		TaskID:                payload.TaskID,
		RuntimeKind:           payload.RuntimeKind,
		QueueID:               payload.QueueID,
		Status:                runtimeStatusFromLifecycle(event.EventType, payload.Status),
		WorkerID:              payload.WorkerID,
		LastEvent:             string(event.EventType),
		LastMessage:           payload.Message,
		UpdatedAt:             event.OccurredAt,
		LastHeartbeatAt:       payload.LastHeartbeatAt,
		LeaseExpiresAt:        payload.LeaseExpiresAt,
		Usage:                 cloneRuntimeUsage(payload.Usage),
		ResponsibilityContext: agentcore.CloneResponsibilityContext(payload.ResponsibilityContext),
	}
	switch event.EventType {
	case missionevent.EventAgentRunQueued:
		session.QueuedAt = event.OccurredAt
	case missionevent.EventAgentRunStarted:
		session.StartedAt = event.OccurredAt
		session.IsAlive = !payload.LastHeartbeatAt.IsZero() && (payload.LeaseExpiresAt.IsZero() || payload.LeaseExpiresAt.After(event.OccurredAt))
		session.IsStale = !payload.LeaseExpiresAt.IsZero() && !payload.LeaseExpiresAt.After(event.OccurredAt)
	case missionevent.EventAgentRunCompleted:
		session.CompletedAt = event.OccurredAt
	}
	return session
}

func runtimeSessionFromFailure(event missionevent.Envelope, payload missionevent.RuntimeFailurePayload) RuntimeSessionView {
	return RuntimeSessionView{
		RunID:                 payload.RunID,
		AgentID:               payload.AgentID,
		TaskID:                payload.TaskID,
		Status:                runtimeStatusFromFailure(event.EventType),
		LastEvent:             string(event.EventType),
		LastMessage:           payload.Message,
		ErrorCode:             payload.Code,
		ErrorSummary:          payload.Message,
		ResponsibilityContext: agentcore.CloneResponsibilityContext(payload.ResponsibilityContext),
		UpdatedAt:             event.OccurredAt,
		CompletedAt:           event.OccurredAt,
	}
}

func upsertRuntimeSession(sessions []RuntimeSessionView, next RuntimeSessionView) []RuntimeSessionView {
	for i := range sessions {
		if sessions[i].RunID == next.RunID {
			if isTerminalRuntimeSessionStatus(sessions[i].Status) {
				return sessions
			}
			mergeRuntimeSession(&sessions[i], next)
			return sessions
		}
	}
	return append(sessions, next)
}

func mergeRuntimeSession(existing *RuntimeSessionView, next RuntimeSessionView) {
	if next.AgentID != "" {
		existing.AgentID = next.AgentID
	}
	if next.TaskID != "" {
		existing.TaskID = next.TaskID
	}
	if next.RuntimeKind != "" {
		existing.RuntimeKind = next.RuntimeKind
	}
	if next.QueueID != "" {
		existing.QueueID = next.QueueID
	}
	if next.Status != "" {
		existing.Status = next.Status
	}
	if next.WorkerID != "" {
		existing.WorkerID = next.WorkerID
	}
	if next.LastEvent != "" {
		existing.LastEvent = next.LastEvent
	}
	if next.LastMessage != "" {
		existing.LastMessage = next.LastMessage
	}
	if next.ErrorCode != "" {
		existing.ErrorCode = next.ErrorCode
	}
	if next.ErrorSummary != "" {
		existing.ErrorSummary = next.ErrorSummary
	}
	if !next.QueuedAt.IsZero() {
		existing.QueuedAt = next.QueuedAt
	}
	if !next.StartedAt.IsZero() {
		existing.StartedAt = next.StartedAt
	}
	if !next.CompletedAt.IsZero() {
		existing.CompletedAt = next.CompletedAt
	}
	if !next.LastHeartbeatAt.IsZero() {
		existing.LastHeartbeatAt = next.LastHeartbeatAt
	}
	if !next.LeaseExpiresAt.IsZero() {
		existing.LeaseExpiresAt = next.LeaseExpiresAt
	}
	if len(next.Usage) > 0 {
		existing.Usage = mergeRuntimeUsage(existing.Usage, next.Usage)
	}
	if next.ResponsibilityContext != nil {
		existing.ResponsibilityContext = agentcore.CloneResponsibilityContext(next.ResponsibilityContext)
	}
	existing.IsAlive = next.IsAlive
	existing.IsStale = next.IsStale
	if !next.UpdatedAt.IsZero() {
		existing.UpdatedAt = next.UpdatedAt
	}
}

func cloneRuntimeUsage(src map[string]agentcore.TokenUsage) map[string]agentcore.TokenUsage {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]agentcore.TokenUsage, len(src))
	for model, usage := range src {
		model = strings.TrimSpace(model)
		if model == "" || isZeroRuntimeUsage(usage) {
			continue
		}
		out[model] = usage
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeRuntimeUsage(dst map[string]agentcore.TokenUsage, src map[string]agentcore.TokenUsage) map[string]agentcore.TokenUsage {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = map[string]agentcore.TokenUsage{}
	}
	for model, usage := range src {
		model = strings.TrimSpace(model)
		if model == "" || isZeroRuntimeUsage(usage) {
			continue
		}
		current, seen := dst[model]
		current.CachedInputTokens += usage.CachedInputTokens
		current.ReasoningTokens += usage.ReasoningTokens
		current.CachedInputTokensKnown = usage.CachedInputTokensKnown && (!seen || current.CachedInputTokensKnown)
		current.ReasoningTokensKnown = usage.ReasoningTokensKnown && (!seen || current.ReasoningTokensKnown)
		current.InputTokens += usage.InputTokens
		current.OutputTokens += usage.OutputTokens
		current.CacheReadTokens += usage.CacheReadTokens
		current.CacheWriteTokens += usage.CacheWriteTokens
		dst[model] = current
	}
	if len(dst) == 0 {
		return nil
	}
	return dst
}

func isZeroRuntimeUsage(usage agentcore.TokenUsage) bool {
	return usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.CacheReadTokens == 0 && usage.CacheWriteTokens == 0 && usage.CachedInputTokens == 0 && usage.ReasoningTokens == 0 && !usage.CachedInputTokensKnown && !usage.ReasoningTokensKnown
}

func runtimeStatusFromLifecycle(eventType missionevent.EventType, status string) RuntimeSessionStatus {
	switch eventType {
	case missionevent.EventAgentRunQueued:
		return RuntimeSessionQueued
	case missionevent.EventAgentRunStarted:
		return RuntimeSessionRunning
	case missionevent.EventAgentRunCompleted:
		return RuntimeSessionCompleted
	}
	switch strings.TrimSpace(status) {
	case string(RuntimeSessionQueued):
		return RuntimeSessionQueued
	case string(RuntimeSessionRunning):
		return RuntimeSessionRunning
	case string(RuntimeSessionCompleted):
		return RuntimeSessionCompleted
	default:
		return ""
	}
}

func runtimeStatusFromFailure(eventType missionevent.EventType) RuntimeSessionStatus {
	switch eventType {
	case missionevent.EventAgentRunTimedOut:
		return RuntimeSessionTimedOut
	case missionevent.EventAgentRunCancelled:
		return RuntimeSessionCancelled
	case missionevent.EventAgentRunBlocked:
		return RuntimeSessionBlocked
	default:
		return RuntimeSessionFailed
	}
}

func isTerminalRuntimeSessionStatus(status RuntimeSessionStatus) bool {
	switch status {
	case RuntimeSessionCompleted, RuntimeSessionFailed, RuntimeSessionTimedOut, RuntimeSessionCancelled, RuntimeSessionBlocked:
		return true
	default:
		return false
	}
}
