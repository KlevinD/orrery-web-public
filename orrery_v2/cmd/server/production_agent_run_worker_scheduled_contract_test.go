package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	schedulerapp "orrery_v2/internal/app/scheduler"
)

func TestEvaluateScheduledAgentResultRejectsMissingRequiredArtifact(t *testing.T) {
	contract := schedulerapp.AgentResultContract{Artifacts: []schedulerapp.ArtifactRequirement{{
		ID: "evidence", Kind: "log", PathPattern: filepath.Join(t.TempDir(), "evidence-*.json"), MinBytes: 1,
	}}}
	run := runtimedaemon.AgentRun{RunID: "run-missing", Request: agentcore.RuntimeRequest{RuntimeOptions: agentcore.RuntimeOptions{
		ProviderSpecific: map[string]string{schedulerResultContractProviderKey: mustEncodeSchedulerResultContract(t, contract)},
	}}}

	_, _, _, err := evaluateScheduledAgentResult(run, "completed", time.Now().Add(-time.Minute))
	if err == nil || !strings.Contains(err.Error(), "evidence") {
		t.Fatalf("error = %v, want missing evidence artifact", err)
	}
}

func TestEvaluateScheduledAgentResultReturnsVerifiedArtifactsAndEvidence(t *testing.T) {
	dir := t.TempDir()
	startedAt := time.Now().Add(-time.Minute)
	evidencePath := filepath.Join(dir, "evidence-2026-07-30.json")
	reportPath := filepath.Join(dir, "report-2026-07-30.md")
	if err := os.WriteFile(evidencePath, []byte(`{"ok":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reportPath, []byte("# Located Issues\nnone\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	contract := schedulerapp.AgentResultContract{Artifacts: []schedulerapp.ArtifactRequirement{
		{ID: "evidence", Kind: "log", PathPattern: filepath.Join(dir, "evidence-*.json"), MinBytes: 2},
		{ID: "report", Kind: "report", PathPattern: filepath.Join(dir, "report-*.md"), MinBytes: 2},
	}}
	run := runtimedaemon.AgentRun{RunID: "run-success", Request: agentcore.RuntimeRequest{RuntimeOptions: agentcore.RuntimeOptions{
		ProviderSpecific: map[string]string{schedulerResultContractProviderKey: mustEncodeSchedulerResultContract(t, contract)},
	}}}

	result, artifacts, evidence, err := evaluateScheduledAgentResult(run, "scan completed", startedAt)
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "scan completed" || len(result.Artifacts) != 2 || len(artifacts) != 2 || len(evidence) != 2 {
		t.Fatalf("result=%+v artifacts=%+v evidence=%+v", result, artifacts, evidence)
	}
	if result.Artifacts[0].URI != evidencePath || result.Artifacts[0].EvidenceID == "" {
		t.Fatalf("first artifact = %+v", result.Artifacts[0])
	}
}

func TestEvaluateScheduledAgentResultDoesNotPersistRawReasoning(t *testing.T) {
	run := runtimedaemon.AgentRun{RunID: "run-private-reasoning"}

	result, _, _, err := evaluateScheduledAgentResult(run, "<think>private reasoning</think>\nvisible answer", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "visible answer" || strings.Contains(result.Summary, "private reasoning") || strings.Contains(result.Summary, "<think>") {
		t.Fatalf("summary = %q", result.Summary)
	}
}

func TestIsNativeScheduledTaskCreatorAllowsOnlyManagedCreators(t *testing.T) {
	tests := map[string]bool{
		"system:orrery-native-migration":                                  true,
		"system:native-scheduled-migration":                               true,
		" system:people-responsibility:people-agent-binding-123:A000010 ": true,
		"system:people-responsibility:":                                   false,
		"system:people-responsibility:people-agent-binding-123":           false,
		"system:people-responsibility::A000010":                           false,
		"system:people-responsibility:binding:A000010:extra":              false,
		"system:responsibility-coordinator-bootstrap":                     false,
		"operator": false,
	}
	for creator, want := range tests {
		if got := isNativeScheduledTaskCreator(creator); got != want {
			t.Errorf("isNativeScheduledTaskCreator(%q) = %t, want %t", creator, got, want)
		}
	}
}

func TestProductionAgentRunWorkerLoadsScheduledResultContractFromRepository(t *testing.T) {
	contract := schedulerapp.AgentResultContract{Artifacts: []schedulerapp.ArtifactRequirement{{
		ID: "weekly-report", Kind: "report", PathPattern: "/reports/weekly-*.md", MinBytes: 128,
	}}}
	worker := productionAgentRunWorker{schedulerTasks: scheduledTaskContractSourceFunc(func(_ context.Context, id string) (schedulerapp.ScheduledTask, error) {
		if id != "scheduled-task-1" {
			t.Fatalf("scheduled task id = %q", id)
		}
		return schedulerapp.ScheduledTask{
			ScheduledTaskID: id,
			Title:           "Weekly intelligence",
			CreatedBy:       "system:people-responsibility:people-agent-binding-123:A000010",
			Target:          schedulerapp.Target{ApprovalAssigneeID: "human-ding", ResultContract: contract},
			Execution:       schedulerapp.ExecutionPolicy{TimeoutSeconds: 1800, LLMProfileID: "llm-minimax", SourceModel: "minimax/MiniMax-M3"},
			Delivery: schedulerapp.DeliveryPolicy{
				Mode: "announce", Channel: "feishu", RecipientID: "oc_group", AccountID: "main",
				ContentArtifactID: "weekly-report", Format: "markdown", BestEffort: true,
			},
		}, nil
	})}
	run := runtimedaemon.AgentRun{Request: agentcore.RuntimeRequest{RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{
		"scheduled_task_id": "scheduled-task-1",
	}}}}

	if err := worker.loadScheduledResultContract(context.Background(), &run); err != nil {
		t.Fatal(err)
	}
	if got := run.Request.RuntimeOptions.ProviderSpecific[schedulerResultContractProviderKey]; got != mustEncodeSchedulerResultContract(t, contract) {
		t.Fatalf("scheduler result contract = %q", got)
	}
	metadata := run.Request.RuntimeOptions.ProviderSpecific
	if metadata["timeout_seconds"] != "1800" || metadata["llm_profile_id"] != "llm-minimax" || metadata["source_model"] != "minimax/MiniMax-M3" {
		t.Fatalf("runtime policy metadata = %+v", metadata)
	}
	if metadata["approval_assigned_to"] != "human-ding" {
		t.Fatalf("approval assignee metadata = %+v", metadata)
	}
	if metadata["delivery_mode"] != "announce" || metadata["delivery_channel"] != "feishu" || metadata["delivery_recipient_id"] != "oc_group" || metadata["delivery_account_id"] != "main" || metadata["delivery_content_artifact_id"] != "weekly-report" || metadata["delivery_format"] != "markdown" || metadata["delivery_best_effort"] != "true" {
		t.Fatalf("delivery policy metadata = %+v", metadata)
	}
	if metadata[schedulerRuntimeConfigRefProviderKey] != "" {
		t.Fatalf("scheduler runtime config ref = %q", metadata[schedulerRuntimeConfigRefProviderKey])
	}
	if metadata[runtimecore.ToolCallTimeoutSecondsProviderKey] != "1800" {
		t.Fatalf("runtime tool timeout = %q", metadata[runtimecore.ToolCallTimeoutSecondsProviderKey])
	}
}

func TestProductionAgentRunWorkerUsesOccurrenceConfigurationSnapshot(t *testing.T) {
	snapshotContract := schedulerapp.AgentResultContract{Artifacts: []schedulerapp.ArtifactRequirement{{
		ID: "snapshot-report", Kind: "report", PathPattern: "/reports/snapshot.md", MinBytes: 64,
	}}}
	snapshotTask := schedulerapp.ScheduledTask{
		TenantID: "tenant-snapshot", ScheduledTaskID: "scheduled-task-snapshot", AgentTaskID: "agent-task-original", Title: "Original task", Description: "Original description",
		CreatedBy: "system:orrery-native-migration", CreatedByKind: schedulerapp.ViewerKindSystem, CreatorProfileID: "system:orrery-native-migration",
		Target: schedulerapp.Target{
			Mode: schedulerapp.TargetModeAgentTask, AgentID: "agent-snapshot", ApprovalAssigneeID: "human-original", TaskGoal: "Original goal", Prompt: "Original prompt",
			ResultContract: snapshotContract,
		},
		Policy: schedulerapp.Policy{MaxRetries: 2, RetryDelay: "15s"},
		Execution: schedulerapp.ExecutionPolicy{
			TimeoutSeconds: 900, LLMProfileID: "llm-original", SourceModel: "model-original", FallbackModels: []string{"fallback-original"}, AllowedToolIDs: []string{"tool-original"},
		},
		Delivery: schedulerapp.DeliveryPolicy{Mode: "announce", Channel: "feishu", RecipientID: "oc_snapshot", AccountID: "snapshot", Format: "markdown"},
	}
	source := scheduledRunConfigurationSourceStub{
		task: schedulerapp.ScheduledTask{
			TenantID: "tenant-snapshot", ScheduledTaskID: "scheduled-task-snapshot", Title: "Edited task", Description: "Edited description", CreatedBy: "human-edited",
			Target: schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, ResultContract: schedulerapp.AgentResultContract{Artifacts: []schedulerapp.ArtifactRequirement{{
				ID: "edited-report", Kind: "report", PathPattern: "/reports/edited.md",
			}}}, ApprovalAssigneeID: "human-edited"},
			Execution: schedulerapp.ExecutionPolicy{TimeoutSeconds: 60, LLMProfileID: "llm-edited", SourceModel: "model-edited"},
			Delivery:  schedulerapp.DeliveryPolicy{Mode: "announce", Channel: "feishu", RecipientID: "oc_edited", AccountID: "edited"},
		},
		run: schedulerapp.ScheduledRun{
			ScheduledRunID: "scheduled-run-snapshot", ScheduledTaskID: "scheduled-task-snapshot", TargetMode: schedulerapp.TargetModeAgentTask,
			TriggerType:                  schedulerapp.RunTriggerManual,
			ConfigurationSnapshotVersion: 1, ConfigurationSnapshot: &snapshotTask, TenantID: "tenant-snapshot", ScheduledTaskTitle: "Original task", ResultContract: snapshotContract,
			Delivery: snapshotTask.Delivery, AgentID: "agent-snapshot",
		},
	}
	worker := productionAgentRunWorker{schedulerTasks: source}
	run := runtimedaemon.AgentRun{Request: agentcore.RuntimeRequest{TenantID: "tenant-snapshot", RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{
		"scheduled_task_id": "scheduled-task-snapshot", "scheduled_run_id": "scheduled-run-snapshot",
		"agent_task_id": "agent-task-stale", "creator_id": "creator-stale", "creator_profile_id": "profile-stale", "max_attempts": "99", "retry_delay": "99m",
		"thinking": "stale", "light_context": "true", "delivery_best_effort": "true", "delivery_recipient_id": "oc_stale",
		schedulerResultContractProviderKey: `{"artifacts":[{"id":"stale"}]}`,
	}}}}

	if err := worker.loadScheduledResultContract(context.Background(), &run); err != nil {
		t.Fatal(err)
	}
	metadata := run.Request.RuntimeOptions.ProviderSpecific
	if metadata["generation_id"] != "scheduled-run-snapshot" || metadata["scheduled_trigger_type"] != "manual" {
		t.Fatalf("generation metadata = %+v", metadata)
	}
	if got := metadata[schedulerResultContractProviderKey]; got != mustEncodeSchedulerResultContract(t, snapshotContract) {
		t.Fatalf("result contract = %q, want occurrence snapshot", got)
	}
	if metadata["delivery_recipient_id"] != "oc_snapshot" || metadata["delivery_account_id"] != "snapshot" || metadata["delivery_format"] != "markdown" {
		t.Fatalf("delivery metadata = %+v, want occurrence snapshot", metadata)
	}
	if metadata["scheduled_task_name"] != "Original task" {
		t.Fatalf("scheduled task title = %q, want occurrence snapshot", metadata["scheduled_task_name"])
	}
	if metadata["scheduled_task_description"] != "Original description" || metadata["approval_assigned_to"] != "human-original" ||
		metadata["timeout_seconds"] != "900" || metadata["llm_profile_id"] != "llm-original" || metadata["source_model"] != "model-original" ||
		metadata["fallback_models"] != "fallback-original" || metadata[agentcore.RuntimeAllowedToolIDsProviderKey] != `["tool-original"]` {
		t.Fatalf("execution metadata = %+v, want occurrence snapshot", metadata)
	}
	if metadata[schedulerRuntimeConfigRefProviderKey] != "" || metadata[runtimecore.ToolCallTimeoutSecondsProviderKey] != "900" {
		t.Fatalf("native scheduler runtime metadata = %+v, want occurrence snapshot creator", metadata)
	}
	if metadata["agent_task_id"] != "agent-task-original" || metadata["creator_id"] != snapshotTask.CreatedBy ||
		metadata["creator_profile_id"] != snapshotTask.CreatorProfileID || metadata["max_attempts"] != "3" || metadata["retry_delay"] != "15s" {
		t.Fatalf("identity and retry metadata = %+v, want occurrence snapshot", metadata)
	}
	for _, staleKey := range []string{"thinking", "light_context", "delivery_best_effort"} {
		if _, exists := metadata[staleKey]; exists {
			t.Fatalf("stale mutable task metadata %q survived occurrence snapshot rebuild: %+v", staleKey, metadata)
		}
	}
}
