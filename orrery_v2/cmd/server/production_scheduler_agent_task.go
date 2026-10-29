package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
	missioncore "orrery_v2/internal/mission/core"
)

const schedulerRunPermissionScopeKey = "permission_scope"
const schedulerAgentTaskRunSource = "scheduler_agent_task"
const schedulerAgentTaskViewerID = "scheduler-agent-task"
const schedulerResultContractProviderKey = "scheduler_result_contract"
const scheduledGenerationPromptBlockID = "orrery.scheduled_generation.v1"

const (
	nativeScheduledTaskCreator           = "system:orrery-native-migration"
	legacyNativeScheduledTaskCreator     = "system:native-scheduled-migration"
	peopleResponsibilityCreatorPrefix    = "system:people-responsibility:"
	nativeScheduledRuntimeProfileID      = "runtime-pi-orrery-native-scheduled"
	schedulerRuntimeConfigRefProviderKey = "scheduler_runtime_config_ref"
)

func isNativeScheduledTaskCreator(value string) bool {
	trimmed := strings.TrimSpace(value)
	switch trimmed {
	case nativeScheduledTaskCreator, legacyNativeScheduledTaskCreator:
		return true
	}
	suffix, ok := strings.CutPrefix(trimmed, peopleResponsibilityCreatorPrefix)
	if !ok {
		return false
	}
	parts := strings.Split(suffix, ":")
	return len(parts) == 2 && strings.TrimSpace(parts[0]) != "" && strings.TrimSpace(parts[1]) != ""
}

type schedulerMissionCommandPort interface {
	CreateMission(context.Context, missioncontrol.ViewerRef, missioncontrol.CreateMissionCommand) (missioncontrol.MissionDTO, error)
	SyncSchedulerMission(context.Context, missioncontrol.ViewerRef, missioncontrol.SyncSchedulerMissionCommand) (missioncontrol.MissionDTO, error)
	CreateTask(context.Context, missioncontrol.ViewerRef, missioncontrol.CreateTaskCommand) (missioncontrol.TaskCardDTO, error)
	AssignTask(context.Context, missioncontrol.ViewerRef, missioncontrol.AssignTaskCommand) (missioncontrol.CommandReceiptDTO, error)
}

type productionSchedulerAgentTaskPort struct {
	missions schedulerMissionCommandPort
	runs     *schedulerapp.Repository
}

type productionSchedulerMissionLifecycle struct {
	missions schedulerMissionCommandPort
}

func (p productionSchedulerMissionLifecycle) EnsureScheduledMission(ctx context.Context, task schedulerapp.ScheduledTask) (string, error) {
	if p.missions == nil {
		return "", schedulerapp.ErrServiceDependency
	}
	task.TenantID = strings.TrimSpace(task.TenantID)
	task.ScheduledTaskID = strings.TrimSpace(task.ScheduledTaskID)
	task.Title = strings.TrimSpace(task.Title)
	if task.TenantID == "" || task.ScheduledTaskID == "" || task.Title == "" || task.Target.Mode != schedulerapp.TargetModeAgentTask {
		return "", schedulerapp.ErrInvalidRequest
	}
	missionID := strings.TrimSpace(task.MissionID)
	if missionID == "" {
		missionID = scheduledMissionID(task.TenantID, task.ScheduledTaskID)
		missionKey := "scheduler-agent-task:" + task.ScheduledTaskID
		initialHumanID := strings.TrimSpace(task.Target.ApprovalAssigneeID)
		if initialHumanID == "" && (task.CreatedByKind == schedulerapp.ViewerKindHuman || task.CreatedByKind == schedulerapp.ViewerKindUser) {
			initialHumanID = strings.TrimSpace(task.CreatorProfileID)
		}
		created, err := p.missions.CreateMission(ctx, schedulerMissionViewer(task.TenantID), missioncontrol.CreateMissionCommand{
			MissionID: missionID, RequestID: missionKey, IdempotencyKey: missionKey,
			Title: "Scheduled: " + task.Title, FinalGoal: strings.TrimSpace(task.Target.TaskGoal),
			RequesterID: strings.TrimSpace(task.CreatedBy), InitialHumanAgentID: initialHumanID,
			InitialHumanDisplayName: scheduledApprovalAssigneeDisplayName(initialHumanID, task.CreatorProfileID, task.CreatedByName), OriginKind: "scheduler",
			OriginID: task.ScheduledTaskID, InitialAgentProfileID: strings.TrimSpace(task.Target.AgentID),
		})
		if err != nil {
			return "", err
		}
		if created.MissionID != missionID {
			return "", fmt.Errorf("scheduled Mission identity conflict: got %s, want %s", created.MissionID, missionID)
		}
	}
	synced, err := p.missions.SyncSchedulerMission(ctx, schedulerMissionViewer(task.TenantID), missioncontrol.SyncSchedulerMissionCommand{
		IdempotencyKey: "scheduler-agent-task:" + task.ScheduledTaskID + ":sync",
		MissionID:      missioncore.MissionID(missionID), ScheduledTaskID: task.ScheduledTaskID,
		Title: "Scheduled: " + task.Title, FinalGoal: strings.TrimSpace(task.Target.TaskGoal), ScheduledTaskStatus: string(task.Status),
		ApprovalAssigneeID: strings.TrimSpace(task.Target.ApprovalAssigneeID),
	})
	if err != nil {
		return "", err
	}
	if synced.MissionID != missionID {
		return "", fmt.Errorf("scheduled Mission identity conflict: got %s, want %s", synced.MissionID, missionID)
	}
	return missionID, nil
}

func schedulerMissionViewer(tenantID string) missioncontrol.ViewerRef {
	return missioncontrol.ViewerRef{
		ViewerID: schedulerAgentTaskViewerID, ViewerKind: missioncontrol.ViewerKindSystem, TenantID: strings.TrimSpace(tenantID),
		AuthorizationSubjectID: schedulerAgentTaskViewerID, PermissionScope: []string{"mission.write"},
	}
}

func (p productionSchedulerAgentTaskPort) StartAgentTask(ctx context.Context, req schedulerapp.AgentTaskRequest) (schedulerapp.AgentTaskReceipt, error) {
	if p.missions == nil {
		return schedulerapp.AgentTaskReceipt{}, fmt.Errorf("Mission command service is not configured")
	}
	req.TenantID = strings.TrimSpace(req.TenantID)
	req.MissionID = strings.TrimSpace(req.MissionID)
	req.RunID = strings.TrimSpace(req.RunID)
	req.ScheduledTaskID = strings.TrimSpace(req.ScheduledTaskID)
	req.AgentID = strings.TrimSpace(req.AgentID)
	req.TaskGoal = strings.TrimSpace(req.TaskGoal)
	if req.TenantID == "" || req.RunID == "" || req.ScheduledTaskID == "" || req.AgentID == "" || req.TaskGoal == "" {
		return schedulerapp.AgentTaskReceipt{}, fmt.Errorf("tenant, scheduled run, scheduled task, Agent, and task goal are required")
	}
	if envelope := req.ExecutionEnvelope; envelope != nil {
		if err := envelope.Verify(); err != nil {
			return schedulerapp.AgentTaskReceipt{}, fmt.Errorf("invalid scheduled execution envelope: %w", err)
		}
		if (envelope.TenantID != "" && strings.TrimSpace(envelope.TenantID) != req.TenantID) ||
			(envelope.RunID != "" && strings.TrimSpace(envelope.RunID) != req.RunID) ||
			(envelope.ScheduledTaskID != "" && strings.TrimSpace(envelope.ScheduledTaskID) != req.ScheduledTaskID) ||
			(envelope.ScheduledRunID != "" && strings.TrimSpace(envelope.ScheduledRunID) != req.RunID) ||
			(envelope.AgentID != "" && strings.TrimSpace(envelope.AgentID) != req.AgentID) ||
			(envelope.AgentProfileID != "" && strings.TrimSpace(envelope.AgentProfileID) != req.AgentID) {
			return schedulerapp.AgentTaskReceipt{}, fmt.Errorf("scheduled execution envelope identity does not match request")
		}
	}
	viewer := schedulerMissionViewer(req.TenantID)
	missionKey := "scheduler-agent-task:" + req.ScheduledTaskID
	runKey := "scheduler-agent-task:" + req.RunID
	missionID := scheduledMissionID(req.TenantID, req.ScheduledTaskID)
	if req.MissionID != "" && req.MissionID != missionID {
		return schedulerapp.AgentTaskReceipt{}, fmt.Errorf("scheduled Mission identity conflict: got %s, want %s", req.MissionID, missionID)
	}
	if req.MissionID == "" {
		initialHumanID := strings.TrimSpace(req.ApprovalAssigneeID)
		if initialHumanID == "" && (req.CreatorKind == schedulerapp.ViewerKindHuman || req.CreatorKind == schedulerapp.ViewerKindUser) {
			initialHumanID = strings.TrimSpace(req.CreatorProfileID)
		}
		mission, err := p.missions.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{
			MissionID: missionID, RequestID: missionKey, IdempotencyKey: missionKey,
			Title: "Scheduled: " + firstNonBlank(req.Title, req.TaskGoal), FinalGoal: req.TaskGoal,
			RequesterID: strings.TrimSpace(req.PrincipalID), InitialHumanAgentID: initialHumanID, InitialHumanDisplayName: scheduledApprovalAssigneeDisplayName(initialHumanID, req.CreatorProfileID, req.CreatorName),
			OriginKind: "scheduler", OriginID: req.ScheduledTaskID, InitialAgentProfileID: req.AgentID,
		})
		if err != nil {
			return schedulerapp.AgentTaskReceipt{}, err
		}
		missionID = mission.MissionID
	}
	// Creating or assigning a Task can immediately activate its responsibility.
	// Persist the deterministic occurrence identity before that worker can start.
	if err := p.bindScheduledAgentTaskIdentity(ctx, req, missionID); err != nil {
		return schedulerapp.AgentTaskReceipt{}, err
	}
	task, err := p.missions.CreateTask(ctx, viewer, scheduledMissionTaskCommand(req, missionID))
	if err != nil {
		return schedulerapp.AgentTaskReceipt{}, err
	}
	if _, err := p.missions.AssignTask(ctx, viewer, missioncontrol.AssignTaskCommand{
		RequestID: runKey + ":assign", IdempotencyKey: runKey + ":assign", MissionID: missioncore.MissionID(missionID),
		TaskID: missioncore.TaskID(task.TaskID), AssigneeID: req.AgentID, Reason: "materialize scheduled Agent work as a responsibility",
	}); err != nil {
		return schedulerapp.AgentTaskReceipt{}, err
	}
	responsibilityID := responsibility.AssignmentID(req.TenantID, responsibility.WorkKindMissionTask, task.TaskID)
	return schedulerapp.AgentTaskReceipt{
		CommandID: responsibilityID, AgentID: req.AgentID, MissionID: missionID, TaskID: task.TaskID, ResponsibilityID: responsibilityID,
	}, nil
}

func scheduledMissionTaskCommand(req schedulerapp.AgentTaskRequest, missionID string) missioncontrol.CreateTaskCommand {
	runKey := "scheduler-agent-task:" + strings.TrimSpace(req.RunID)
	criterion := "The scheduled goal is completed and the submitted result includes traceable evidence."
	expectedDeliverables, evidenceRequirements := scheduledMissionResultRequirements(req, criterion)
	executionEnvelope := agentcore.CloneExecutionEnvelope(req.ExecutionEnvelope)
	expectedTaskID := scheduledMissionTaskID(req.TenantID, req.RunID)
	if executionEnvelope != nil {
		if (executionEnvelope.MissionID != "" && strings.TrimSpace(executionEnvelope.MissionID) != strings.TrimSpace(missionID)) ||
			(executionEnvelope.TaskID != "" && strings.TrimSpace(executionEnvelope.TaskID) != expectedTaskID) {
			// CreateTask will reject this unsigned sentinel, keeping a stale
			// scheduler snapshot from being attached to another Mission Task.
			executionEnvelope = &agentcore.ExecutionEnvelope{RunID: strings.TrimSpace(req.RunID)}
		}
	}
	if executionEnvelope != nil {
		executionEnvelope.MissionID = strings.TrimSpace(missionID)
		executionEnvelope.TaskID = expectedTaskID
		executionEnvelope.EnvelopeID = ""
		executionEnvelope.EnvelopeDigest = ""
		if err := executionEnvelope.SealAt(time.Now().UTC()); err != nil {
			// CreateTask validates the snapshot and fails closed if this cannot
			// be resealed with the materialized Mission Task identity.
			executionEnvelope = &agentcore.ExecutionEnvelope{RunID: strings.TrimSpace(req.RunID)}
		}
	}
	return missioncontrol.CreateTaskCommand{
		RequestID: runKey + ":task", IdempotencyKey: runKey + ":task", MissionID: missioncore.MissionID(strings.TrimSpace(missionID)),
		TaskID: missioncore.TaskID(scheduledMissionTaskID(req.TenantID, req.RunID)), AssigneeID: strings.TrimSpace(req.AgentID),
		Goal: strings.TrimSpace(req.TaskGoal), WorkerPersonalGoal: firstNonBlank(req.Prompt, req.TaskGoal),
		Scope: "Scheduled task " + strings.TrimSpace(req.ScheduledTaskID) + " occurrence " + strings.TrimSpace(req.RunID), Priority: "normal",
		ExpectedDeliverables: expectedDeliverables, AcceptanceCriteria: []string{criterion}, EvidenceRequirements: evidenceRequirements,
		SourceKind: schedulerAgentTaskRunSource, SourceID: strings.TrimSpace(req.ScheduledTaskID), SourceStepID: strings.TrimSpace(req.RunID),
		ExecutionPermissionScopes: cleanScheduledPermissionScopes(req.Permissions),
		ExecutionAllowedToolIDs:   append([]string(nil), req.Execution.AllowedToolIDs...),
		ExecutionEnvelope:         executionEnvelope,
	}
}

func scheduledMissionID(tenantID, scheduledTaskID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(tenantID) + "\x00" + strings.TrimSpace(scheduledTaskID)))
	return "scheduled-mission-" + hex.EncodeToString(digest[:12])
}

func scheduledMissionResultRequirements(req schedulerapp.AgentTaskRequest, criterion string) ([]missioncontrol.ExpectedDeliverableCommand, []missioncontrol.EvidenceRequirementCommand) {
	if len(req.ResultContract.Artifacts) == 0 {
		return []missioncontrol.ExpectedDeliverableCommand{{ID: "deliverable-result", Title: firstNonBlank(req.Title, "Scheduled task result")}},
			[]missioncontrol.EvidenceRequirementCommand{{
				ID: "evidence-acceptance-1", Description: criterion, Kind: "tool_log", Required: true,
			}}
	}
	deliverables := make([]missioncontrol.ExpectedDeliverableCommand, 0, len(req.ResultContract.Artifacts))
	evidence := make([]missioncontrol.EvidenceRequirementCommand, 0, len(req.ResultContract.Artifacts))
	for _, artifact := range req.ResultContract.Artifacts {
		deliverables = append(deliverables, missioncontrol.ExpectedDeliverableCommand{
			ID: artifact.ID, Title: fmt.Sprintf("%s artifact matching %s", artifact.Kind, artifact.PathPattern),
		})
		evidence = append(evidence, missioncontrol.EvidenceRequirementCommand{
			ID:          "evidence-" + artifact.ID,
			Description: fmt.Sprintf("Verify %s exists at %s with at least %d bytes.", artifact.Kind, artifact.PathPattern, artifact.MinBytes),
			Kind:        "artifact", Required: true,
		})
	}
	return deliverables, evidence
}

func scheduledMissionTaskID(tenantID, scheduledRunID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(tenantID) + "\x00" + strings.TrimSpace(scheduledRunID)))
	return "scheduled-task-" + hex.EncodeToString(digest[:12])
}

func encodeSchedulerResultContract(contract schedulerapp.AgentResultContract) (string, error) {
	if len(contract.Artifacts) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(contract)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func schedulerAgentRunProviderSpecific(req schedulerapp.AgentTaskRequest, queueID string) map[string]string {
	values := map[string]string{
		"run_source":          schedulerAgentTaskRunSource,
		"queue_id":            strings.TrimSpace(queueID),
		"scheduled_task_id":   strings.TrimSpace(req.ScheduledTaskID),
		"agent_task_id":       strings.TrimSpace(req.AgentTaskID),
		"scheduled_task_name": strings.TrimSpace(req.Title),
		"creator_id":          strings.TrimSpace(req.PrincipalID),
		"creator_profile_id":  strings.TrimSpace(req.CreatorProfileID),
		"max_attempts":        fmt.Sprintf("%d", max(1, req.MaxAttempts)),
		"retry_delay":         strings.TrimSpace(req.RetryDelay),
	}
	if value := strings.TrimSpace(req.ApprovalAssigneeID); value != "" {
		values["approval_assigned_to"] = value
	}
	if description := strings.TrimSpace(req.Description); description != "" {
		values["scheduled_task_description"] = description
	}
	if scope := strings.Join(cleanScheduledPermissionScopes(req.Permissions), ","); scope != "" {
		values[schedulerRunPermissionScopeKey] = scope
	}
	if req.Execution.TimeoutSeconds > 0 {
		values["timeout_seconds"] = strconv.Itoa(req.Execution.TimeoutSeconds)
	}
	if value := strings.TrimSpace(req.Execution.LLMProfileID); value != "" {
		values["llm_profile_id"] = value
	}
	if value := strings.TrimSpace(req.Execution.SourceModel); value != "" {
		values["source_model"] = value
	}
	if value := strings.TrimSpace(req.Execution.Thinking); value != "" {
		values["thinking"] = value
	}
	if req.Execution.LightContext {
		values["light_context"] = "true"
	}
	if len(req.Execution.AllowedToolIDs) > 0 {
		encoded, _ := json.Marshal(req.Execution.AllowedToolIDs)
		values[agentcore.RuntimeAllowedToolIDsProviderKey] = string(encoded)
	}
	if len(req.Execution.FallbackModels) > 0 {
		values["fallback_models"] = strings.Join(req.Execution.FallbackModels, ",")
	}
	if mode := strings.TrimSpace(req.Delivery.Mode); mode != "" {
		values["delivery_mode"] = mode
	}
	if channel := strings.TrimSpace(req.Delivery.Channel); channel != "" {
		values["delivery_channel"] = channel
	}
	if recipient := strings.TrimSpace(req.Delivery.RecipientID); recipient != "" {
		values["delivery_recipient_id"] = recipient
	}
	if account := strings.TrimSpace(req.Delivery.AccountID); account != "" {
		values["delivery_account_id"] = account
	}
	if artifactID := strings.TrimSpace(req.Delivery.ContentArtifactID); artifactID != "" {
		values["delivery_content_artifact_id"] = artifactID
	}
	if format := strings.TrimSpace(req.Delivery.Format); format != "" {
		values["delivery_format"] = format
	}
	if req.Delivery.BestEffort {
		values["delivery_best_effort"] = "true"
	}
	return values
}

func scheduledApprovalAssigneeDisplayName(assigneeID, creatorProfileID, creatorName string) string {
	if strings.EqualFold(strings.TrimSpace(assigneeID), strings.TrimSpace(creatorProfileID)) {
		return strings.TrimSpace(creatorName)
	}
	return strings.TrimPrefix(strings.TrimSpace(assigneeID), "human-")
}

func cleanScheduledPermissionScopes(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}
	return out
}
