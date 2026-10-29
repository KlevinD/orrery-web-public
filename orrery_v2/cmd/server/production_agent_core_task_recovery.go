package main

import (
	"errors"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
)

// The selected instance's actual runtime decides task policy; queued metadata
// cannot enable recovery on PI, public chat, or another executor.
func configureQueuedAgentCoreTask(run *runtimedaemon.AgentRun, kind agentcore.RuntimeKind) {
	if run == nil || kind != agentcore.RuntimeKindManaged || run.RunID.IsZero() || strings.TrimSpace(run.MissionID) == "" || strings.TrimSpace(run.TaskID) == "" {
		return
	}
	values := cloneStringMap(run.Request.RuntimeOptions.ProviderSpecific)
	if values == nil {
		values = map[string]string{}
	}
	values["agentcore_task_mode"] = "long"
	values["agentcore_task_recovery"] = "automatic"
	run.Request.RuntimeOptions.ProviderSpecific = values
}

func queuedAgentCoreTaskEnabled(run runtimedaemon.AgentRun, kind agentcore.RuntimeKind) bool {
	return kind == agentcore.RuntimeKindManaged && !run.RunID.IsZero() && strings.TrimSpace(run.MissionID) != "" && strings.TrimSpace(run.TaskID) != "" &&
		run.Request.RuntimeOptions.ProviderSpecific["agentcore_task_mode"] == "long" && run.Request.RuntimeOptions.ProviderSpecific["agentcore_task_recovery"] == "automatic"
}

func queuedAgentCoreTaskCanRetry(run runtimedaemon.AgentRun, kind agentcore.RuntimeKind, err error) bool {
	if !queuedAgentCoreTaskEnabled(run, kind) {
		return false
	}
	var failure agentcore.RuntimePipelineError
	return errors.As(err, &failure) && (failure.Status == agentcore.RuntimeStatusFailed || failure.Status == agentcore.RuntimeStatusTimeout) &&
		failure.ProviderMetadata["task_resume_ready"] == "true" && strings.HasPrefix(failure.ProviderMetadata["session_id"], "agentcore-task-")
}

func queuedAgentCoreTaskMustStop(run runtimedaemon.AgentRun, kind agentcore.RuntimeKind, err error) bool {
	if !queuedAgentCoreTaskEnabled(run, kind) {
		return false
	}
	var bridge *agentcore.RuntimeBridgeError
	return errors.As(err, &bridge) && !bridge.Retryable
}
