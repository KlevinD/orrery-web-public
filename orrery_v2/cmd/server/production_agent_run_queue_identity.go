package main

import (
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"strings"
)

func agentRunQueueID(run runtimedaemon.AgentRun) string {
	if run.Request.RuntimeOptions.ProviderSpecific != nil {
		if value := strings.TrimSpace(run.Request.RuntimeOptions.ProviderSpecific["queue_id"]); value != "" {
			return value
		}
	}
	if strings.TrimSpace(run.MissionID) != "" {
		return "mission:" + strings.TrimSpace(run.MissionID)
	}
	return ""
}
