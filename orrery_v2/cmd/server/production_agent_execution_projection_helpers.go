package main

import (
	"strings"
	"time"

	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/platform/graphprotocol"
)

func timeProjectionRevision(value time.Time) (int64, time.Time) {
	if value.IsZero() {
		value = time.Unix(0, 1).UTC()
	}
	value = value.UTC()
	return max(int64(1), value.UnixNano()), value
}

func projectionTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func ontologyAgentRunID(runtimeRunID string) string {
	runtimeRunID = strings.TrimSpace(runtimeRunID)
	if runtimeRunID == "" {
		return ""
	}
	return "agent-run:" + runtimeRunID
}

func workforceAgentOntologySourceRef(tenantID, agentID string) graphprotocol.AccessRef {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "system" {
		return trustedOntologySourceRef("resource", agentID, tenantID)
	}
	return organizationOntologySourceRef(tenantID)
}

func runtimeAgentVersionID(agentID, runtimeKind string) string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return ""
	}
	runtimeKind = strings.TrimSpace(runtimeKind)
	if runtimeKind == "" {
		runtimeKind = "default"
	}
	return agentID + ":" + runtimeKind
}

func runtimeSourceKind(event runtimedaemon.RuntimeLifecycleEvent) string {
	if strings.TrimSpace(event.ScheduledTaskID) != "" {
		return "scheduled_task"
	}
	if strings.TrimSpace(string(event.MissionID)) != "" {
		return "mission_task"
	}
	return "manual"
}

func runtimeSourceRef(event runtimedaemon.RuntimeLifecycleEvent) string {
	return firstNonBlank(event.ScheduledTaskID, string(event.TaskID), string(event.MissionID), event.QueueID)
}

func runtimeTaskStatus(event runtimedaemon.RuntimeLifecycleEvent) string {
	if event.Kind == runtimedaemon.RuntimeLifecycleCompleted {
		return "completed"
	}
	if event.Kind == runtimedaemon.RuntimeLifecycleStarted {
		return "running"
	}
	return string(event.Kind)
}

func runtimeEventProjectionRevision(event runtimedaemon.RuntimeLifecycleEvent) (int64, time.Time) {
	occurredAt := event.CompletedAt
	if occurredAt.IsZero() {
		occurredAt = event.StartedAt
	}
	if occurredAt.IsZero() {
		occurredAt = event.LastHeartbeatAt
	}
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	return timeProjectionRevision(occurredAt)
}
