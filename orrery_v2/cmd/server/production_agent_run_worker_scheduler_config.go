package main

import (
	"context"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	schedulerapp "orrery_v2/internal/app/scheduler"
)

func runtimePermissionScope(run runtimedaemon.AgentRun) []string {
	if run.Request.RuntimeOptions.ProviderSpecific == nil {
		return nil
	}
	raw := strings.TrimSpace(run.Request.RuntimeOptions.ProviderSpecific[schedulerRunPermissionScopeKey])
	if raw == "" {
		return nil
	}
	out := []string{}
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		value := strings.TrimSpace(part)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func (w productionAgentRunWorker) loadScheduledResultContract(ctx context.Context, run *runtimedaemon.AgentRun) error {
	if run == nil {
		return fmt.Errorf("agent run is required")
	}
	providerSpecific := run.Request.RuntimeOptions.ProviderSpecific
	if providerSpecific == nil {
		providerSpecific = map[string]string{}
		run.Request.RuntimeOptions.ProviderSpecific = providerSpecific
	}
	scheduledTaskID := strings.TrimSpace(providerSpecific["scheduled_task_id"])
	if scheduledTaskID == "" {
		return nil
	}
	if w.schedulerTasks == nil {
		return fmt.Errorf("scheduler task repository is not configured")
	}
	task, err := w.schedulerTasks.GetTask(ctx, scheduledTaskID)
	if err != nil {
		return fmt.Errorf("load scheduled task result contract: %w", err)
	}
	if strings.TrimSpace(task.ScheduledTaskID) != scheduledTaskID {
		return fmt.Errorf("scheduled task configuration identity does not match the runtime request")
	}
	scheduledRunID := strings.TrimSpace(providerSpecific["scheduled_run_id"])
	if scheduledRunID != "" {
		providerSpecific["generation_id"] = scheduledRunID
	}
	if source, ok := w.schedulerTasks.(schedulerRunConfigurationSource); ok && scheduledRunID != "" {
		occurrence, err := source.GetRun(ctx, scheduledRunID)
		if err != nil {
			return fmt.Errorf("load scheduled run configuration snapshot: %w", err)
		}
		if strings.TrimSpace(occurrence.ScheduledRunID) != scheduledRunID || occurrence.ScheduledTaskID != scheduledTaskID || occurrence.TargetMode != schedulerapp.TargetModeAgentTask {
			return fmt.Errorf("scheduled run configuration identity does not match the runtime request")
		}
		providerSpecific["scheduled_trigger_type"] = strings.TrimSpace(string(occurrence.TriggerType))
		if occurrence.ConfigurationSnapshotVersion > 0 {
			configuration, err := schedulerapp.ResolveScheduledRunConfiguration(task, occurrence)
			if err != nil {
				return fmt.Errorf("validate scheduled run configuration snapshot: %w", err)
			}
			requestTenantID := strings.TrimSpace(run.Request.TenantID)
			taskTenantID := strings.TrimSpace(task.TenantID)
			occurrenceTenantID := strings.TrimSpace(occurrence.TenantID)
			configurationTenantID := strings.TrimSpace(configuration.TenantID)
			if requestTenantID == "" || taskTenantID == "" || occurrenceTenantID == "" ||
				configurationTenantID == "" || requestTenantID != taskTenantID || requestTenantID != occurrenceTenantID || requestTenantID != configurationTenantID {
				return fmt.Errorf("scheduled run configuration tenant does not match the task and runtime request")
			}
			configurationAgentID := strings.TrimSpace(configuration.Target.AgentID)
			for _, runtimeAgentID := range []string{run.AgentID.String(), run.Request.AgentID.String()} {
				if runtimeAgentID = strings.TrimSpace(runtimeAgentID); runtimeAgentID != "" && runtimeAgentID != configurationAgentID {
					return fmt.Errorf("scheduled run configuration Agent does not match the runtime request")
				}
			}
			task = configuration
		}
	}
	taskMetadata := schedulerAgentRunProviderSpecific(schedulerapp.AgentTaskRequest{
		ScheduledTaskID:    task.ScheduledTaskID,
		AgentTaskID:        firstNonBlank(task.AgentTaskID, schedulerapp.AgentTaskIDForScheduledTask(task.ScheduledTaskID)),
		Title:              task.Title,
		Description:        task.Description,
		ApprovalAssigneeID: task.Target.ApprovalAssigneeID,
		PrincipalID:        task.CreatedBy,
		CreatorProfileID:   task.CreatorProfileID,
		MaxAttempts:        max(1, task.Policy.MaxRetries+1),
		RetryDelay:         task.Policy.RetryDelay,
		Execution:          task.Execution,
		Delivery:           task.Delivery,
	}, providerSpecific["queue_id"])
	delete(providerSpecific, schedulerRuntimeConfigRefProviderKey)
	delete(providerSpecific, runtimecore.ToolCallTimeoutSecondsProviderKey)
	// Task provenance does not select a runtime. Keep the signed occurrence's
	// execution budget while buildAgent respects the configured Agent profile.
	providerSpecific[runtimecore.ToolCallTimeoutSecondsProviderKey] = strings.TrimSpace(taskMetadata["timeout_seconds"])
	delete(providerSpecific, schedulerResultContractProviderKey)
	encoded, err := encodeSchedulerResultContract(task.Target.ResultContract)
	if err != nil {
		return fmt.Errorf("encode scheduled task result contract: %w", err)
	}
	if encoded != "" {
		providerSpecific[schedulerResultContractProviderKey] = encoded
	}
	snapshotMetadataKeys := []string{
		"agent_task_id", "scheduled_task_name", "scheduled_task_description", "creator_id", "creator_profile_id", "max_attempts", "retry_delay",
		"timeout_seconds", "llm_profile_id", "source_model",
		"thinking", "light_context", "fallback_models", agentcore.RuntimeAllowedToolIDsProviderKey, "delivery_mode", "delivery_channel", "delivery_recipient_id",
		"delivery_account_id", "delivery_content_artifact_id", "delivery_format", "delivery_best_effort", "approval_assigned_to",
	}
	for _, key := range snapshotMetadataKeys {
		delete(providerSpecific, key)
	}
	for _, key := range snapshotMetadataKeys {
		if value := strings.TrimSpace(taskMetadata[key]); value != "" {
			providerSpecific[key] = value
		}
	}
	return nil
}
