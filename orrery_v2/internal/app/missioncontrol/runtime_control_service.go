package missioncontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/mission/projection"
)

type CancelAgentRunCommand struct {
	RequestID      string
	IdempotencyKey string
	MissionID      missioncore.MissionID
	TaskID         missioncore.TaskID
	AgentID        agentcore.AgentID
	RunID          agentcore.AgentRunID
	Reason         string
}

type RetryAgentRunCommand struct {
	RequestID        string
	IdempotencyKey   string
	MissionID        missioncore.MissionID
	RunID            agentcore.AgentRunID
	ReplacementRunID agentcore.AgentRunID
}

type ResumeApprovalRunCommand struct {
	RequestID      string
	IdempotencyKey string
	MissionID      missioncore.MissionID
	ApprovalRef    string
	ApprovalType   string
	Decision       string
	ActionID       string
	Message        string
	ModifiedInput  string
	ChoiceID       string
	Remember       bool
	Metadata       map[string]string
}

// ResumeDurableHILRunCommand continues a provider run after its in-memory
// approval responder disappears. The authoritative HIL receipt is validated
// without asking policy to approve the same action a second time.
type ResumeDurableHILRunCommand struct {
	IdempotencyKey string
	TenantID       string
	MissionID      missioncore.MissionID
	RunID          agentcore.AgentRunID
	ApprovalRef    string
	ToolName       string
	HILDecisionID  string
	HILReceiptID   string
	HILArgsHash    string
	ResolverID     string
	DecidedAt      string
	Metadata       map[string]string
}

func (s *CommandService) RetryAgentRun(ctx context.Context, viewer ViewerRef, cmd RetryAgentRunCommand) (CommandReceiptDTO, error) {
	if s == nil || s.runStore == nil || s.repo == nil || s.events == nil || s.ids == nil {
		return CommandReceiptDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return CommandReceiptDTO{}, err
	}
	if cmd.IdempotencyKey == "" || cmd.MissionID.IsZero() || cmd.RunID == "" {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	if err := s.authorizeMissionAction(ctx, viewer, cmd.MissionID, "mission.edit"); err != nil {
		return CommandReceiptDTO{}, err
	}
	model, err := loadMissionProjection(ctx, s.repo, viewer.Tenant(), cmd.MissionID)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	if model.Mission.Status != projection.MissionStatusActive {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	session, ok := findRuntimeSession(model, cmd.RunID)
	if !ok || !isRetryableRuntimeSession(session.Status) {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	if s.runtimeRestartGuard != nil {
		if err := s.runtimeRestartGuard.CheckTaskRestart(ctx, viewer.Tenant(), cmd.MissionID.String(), session.TaskID.String()); err != nil {
			return CommandReceiptDTO{}, err
		}
	}
	task, ok := findProjectionTask(model, session.TaskID)
	if !ok {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	task, canonicalTask, err := s.hydrateRuntimeTaskContract(ctx, viewer.Tenant(), cmd.MissionID, &model, task)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	if task.Status == projection.TaskStatusApproved || canonicalTask.Status == projection.TaskStatusApproved {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	runID := strings.TrimSpace(cmd.ReplacementRunID.String())
	if runID == "" {
		generated, err := s.ids.NewIDContext()
		if err != nil {
			return CommandReceiptDTO{}, err
		}
		runID = generated
	} else if cmd.ReplacementRunID == cmd.RunID {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	eventID, err := s.ids.NewIDContext()
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	now := s.nowUTC()
	queueID := strings.TrimSpace(session.QueueID)
	if queueID == "" {
		queueID = runtimeQueueID(StartAgentRunCommand{MissionID: cmd.MissionID, TaskID: session.TaskID}, model)
	}
	runtimeKind := strings.TrimSpace(session.RuntimeKind)
	if runtimeKind == "" {
		runtimeKind = "managed"
	}
	start := StartAgentRunCommand{
		RequestID:             cmd.RequestID,
		IdempotencyKey:        cmd.IdempotencyKey,
		MissionID:             cmd.MissionID,
		TaskID:                session.TaskID,
		AgentID:               session.AgentID.String(),
		RuntimeKind:           runtimeKind,
		QueueID:               queueID,
		ResponsibilityContext: agentcore.CloneResponsibilityContext(session.ResponsibilityContext),
	}
	promptBlocks, err := s.runtimePromptBlocks(ctx)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	conversationContext, err := s.runtimeConversationContext(ctx, model, session.AgentID)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	run := runtimedaemon.AgentRun{
		RunID:           agentcore.AgentRunID(runID),
		AgentID:         session.AgentID,
		AgentProfileID:  agentProfileIDForRuntime(model, session.AgentID),
		AgentInstanceID: agentInstanceIDForRuntime(model, session.AgentID, cmd.MissionID),
		RuntimeID:       runtimeKind,
		MissionID:       cmd.MissionID.String(),
		TaskID:          session.TaskID.String(),
		Status:          runtimedaemon.RunStatusQueued,
		Task:            taskInputFromProjection(start, task),
		Request:         runtimeRequestFromCommand(start, runID, session.AgentID, model, task, now, queueID, promptBlocks, conversationContext),
	}
	if err := s.runStore.Enqueue(ctx, run); err != nil {
		return CommandReceiptDTO{}, err
	}
	streamVersion, err := s.nextRuntimeMissionEventVersion(ctx, viewer.Tenant(), cmd.MissionID, model)
	if err != nil {
		cleanupErr := s.runStore.Cancel(context.WithoutCancel(ctx), agentcore.AgentRunID(runID), "Mission retry event version lookup failed")
		return CommandReceiptDTO{}, errors.Join(err, cleanupErr)
	}
	event := missionevent.Envelope{
		EventID:        eventID,
		TenantID:       viewer.Tenant(),
		MissionID:      cmd.MissionID,
		StreamID:       missionevent.MissionStreamID(viewer.Tenant(), cmd.MissionID),
		StreamVersion:  streamVersion,
		EventType:      missionevent.EventAgentRunQueued,
		EventVersion:   1,
		Actor:          actorFromViewer(viewer),
		CorrelationID:  firstString(cmd.RequestID, cmd.IdempotencyKey),
		IdempotencyKey: cmd.IdempotencyKey,
		OccurredAt:     now,
		Payload: missionevent.RuntimeLifecyclePayload{
			TaskID:                session.TaskID,
			AgentID:               session.AgentID,
			RunID:                 agentcore.AgentRunID(runID),
			RuntimeKind:           runtimeKind,
			QueueID:               queueID,
			Status:                string(projection.RuntimeSessionQueued),
			Message:               "agent run retry queued",
			ResponsibilityContext: agentcore.CloneResponsibilityContext(session.ResponsibilityContext),
		},
		Metadata: responsibilityRunMetadata(map[string]string{"command": "mission.ops.retry_agent_run", "retry_of_run_id": cmd.RunID.String()}, session.ResponsibilityContext),
	}
	if err := s.events.Append(ctx, event); err != nil {
		cleanupErr := s.runStore.Cancel(context.WithoutCancel(ctx), agentcore.AgentRunID(runID), "Mission retry event append failed")
		return CommandReceiptDTO{}, errors.Join(err, cleanupErr)
	}
	projectRuntimeEvent(&model, event, now)
	if err := saveMissionProjection(ctx, s.repo, viewer.Tenant(), model); err != nil {
		cleanupErr := s.runStore.Cancel(context.WithoutCancel(ctx), agentcore.AgentRunID(runID), "Mission projection commit failed")
		return CommandReceiptDTO{}, errors.Join(err, cleanupErr)
	}
	receipt := CommandReceiptDTO{CommandID: runID, Status: "accepted", MissionID: cmd.MissionID.String(), TaskID: session.TaskID.String(), Version: model.Checkpoint.Version, AcceptedAt: now}
	if err := s.recordViewerAudit(ctx, viewer, AuditEvent{
		ActorID:       viewer.ViewerID,
		ViewerID:      viewer.ViewerID,
		MissionID:     cmd.MissionID.String(),
		EntityType:    "agent_run",
		EntityID:      runID,
		Action:        "agent_run.retry",
		Result:        "succeeded",
		Severity:      "info",
		CorrelationID: firstString(cmd.RequestID, cmd.IdempotencyKey),
		Summary:       "queued retry agent run",
		Metadata:      responsibilityRunMetadata(map[string]string{"retry_of_run_id": cmd.RunID.String()}, session.ResponsibilityContext),
		OccurredAt:    now,
	}); err != nil {
		return CommandReceiptDTO{}, err
	}
	return receipt, nil
}

func (s *CommandService) ResumeRunAfterApproval(ctx context.Context, viewer ViewerRef, cmd ResumeApprovalRunCommand) (CommandReceiptDTO, error) {
	if err := viewer.Validate(); err != nil {
		return CommandReceiptDTO{}, err
	}
	if cmd.IdempotencyKey == "" || cmd.MissionID.IsZero() || strings.TrimSpace(cmd.ApprovalRef) == "" || strings.TrimSpace(cmd.Decision) == "" {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	if err := s.authorizeMissionAction(ctx, viewer, cmd.MissionID, "mission.qa_approve"); err != nil {
		return CommandReceiptDTO{}, err
	}
	if !isProviderApprovalType(cmd.ApprovalType, cmd.Metadata) {
		return CommandReceiptDTO{}, nil
	}
	if cmd.Decision != "approved" && cmd.Decision != "answered" {
		return CommandReceiptDTO{}, nil
	}
	if s == nil || s.runStore == nil || s.repo == nil || s.events == nil || s.ids == nil {
		return CommandReceiptDTO{}, ErrServiceDependency
	}
	model, err := loadMissionProjection(ctx, s.repo, viewer.Tenant(), cmd.MissionID)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	if model.Mission.Status != projection.MissionStatusActive {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	request, ok := findApprovalRequest(model, cmd.ApprovalType, cmd.ApprovalRef)
	if !ok {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	runIDFromApproval := approvalRunID(cmd.Metadata)
	session, ok := findRuntimeSession(model, runIDFromApproval)
	if !ok || session.Status != projection.RuntimeSessionBlocked {
		return CommandReceiptDTO{}, nil
	}
	task, ok := findProjectionTask(model, session.TaskID)
	if !ok {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	task, _, err = s.hydrateRuntimeTaskContract(ctx, viewer.Tenant(), cmd.MissionID, &model, task)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	runID, err := s.ids.NewIDContext()
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	eventID, err := s.ids.NewIDContext()
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	now := s.nowUTC()
	queueID := strings.TrimSpace(session.QueueID)
	if queueID == "" {
		queueID = runtimeQueueID(StartAgentRunCommand{MissionID: cmd.MissionID, TaskID: session.TaskID}, model)
	}
	runtimeKind := strings.TrimSpace(session.RuntimeKind)
	if runtimeKind == "" {
		runtimeKind = "managed"
	}
	decisionMetadata := cloneStringMap(cmd.Metadata)
	if decisionMetadata == nil {
		decisionMetadata = cloneStringMap(request.Metadata)
	}
	continuation := runtimecore.ApprovalContinuation{
		Decision:      runtimecore.ApprovalDecisionApproved,
		ActionID:      cmd.ActionID,
		Effect:        firstString(strings.TrimSpace(decisionMetadata["decision_effect"]), approvalDecisionEffectForAction(cmd.ActionID)),
		Message:       cmd.Message,
		ModifiedInput: cmd.ModifiedInput,
		ChoiceID:      cmd.ChoiceID,
		Remember:      cmd.Remember || strings.EqualFold(decisionMetadata["decision_remember"], "true"),
		Metadata:      decisionMetadata,
	}
	start := StartAgentRunCommand{
		RequestID:             cmd.RequestID,
		IdempotencyKey:        cmd.IdempotencyKey,
		MissionID:             cmd.MissionID,
		TaskID:                session.TaskID,
		AgentID:               session.AgentID.String(),
		RuntimeKind:           runtimeKind,
		QueueID:               queueID,
		Prompt:                runtimecore.ApprovalContinuationPrompt(decisionMetadata, continuation),
		ResponsibilityContext: agentcore.CloneResponsibilityContext(session.ResponsibilityContext),
	}
	promptBlocks, err := s.runtimePromptBlocks(ctx)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	conversationContext, err := s.runtimeConversationContext(ctx, model, session.AgentID)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	run := runtimedaemon.AgentRun{
		RunID:           agentcore.AgentRunID(runID),
		AgentID:         session.AgentID,
		AgentProfileID:  agentProfileIDForRuntime(model, session.AgentID),
		AgentInstanceID: agentInstanceIDForRuntime(model, session.AgentID, cmd.MissionID),
		RuntimeID:       runtimeKind,
		MissionID:       cmd.MissionID.String(),
		TaskID:          session.TaskID.String(),
		Status:          runtimedaemon.RunStatusQueued,
		Task:            taskInputFromProjection(start, task),
		Request:         runtimeRequestFromCommand(start, runID, session.AgentID, model, task, now, queueID, promptBlocks, conversationContext),
	}
	run.Request.RuntimeOptions.ResumeSessionID = firstString(strings.TrimSpace(decisionMetadata[runtimecore.ApprovalMetadataSessionID]), strings.TrimSpace(decisionMetadata["session_id"]))
	providerSpecific := runtimecore.ApprovalContinuationProviderSpecific(decisionMetadata, continuation)
	if run.Request.RuntimeOptions.ProviderSpecific == nil {
		run.Request.RuntimeOptions.ProviderSpecific = map[string]string{}
	}
	for key, value := range providerSpecific {
		run.Request.RuntimeOptions.ProviderSpecific[key] = value
	}
	run.Request.RuntimeOptions.ProviderSpecific["approval_continuation_of_run_id"] = session.RunID.String()
	if err := s.runStore.Enqueue(ctx, run); err != nil {
		return CommandReceiptDTO{}, err
	}
	streamVersion, err := s.nextRuntimeMissionEventVersion(ctx, viewer.Tenant(), cmd.MissionID, model)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	event := missionevent.Envelope{
		EventID:        eventID,
		TenantID:       viewer.Tenant(),
		MissionID:      cmd.MissionID,
		StreamID:       missionevent.MissionStreamID(viewer.Tenant(), cmd.MissionID),
		StreamVersion:  streamVersion,
		EventType:      missionevent.EventAgentRunQueued,
		EventVersion:   1,
		Actor:          actorFromViewer(viewer),
		CorrelationID:  firstString(cmd.RequestID, cmd.IdempotencyKey),
		IdempotencyKey: cmd.IdempotencyKey,
		OccurredAt:     now,
		Payload: missionevent.RuntimeLifecyclePayload{
			TaskID:                session.TaskID,
			AgentID:               session.AgentID,
			RunID:                 agentcore.AgentRunID(runID),
			RuntimeKind:           runtimeKind,
			QueueID:               queueID,
			Status:                string(projection.RuntimeSessionQueued),
			Message:               "agent run approval continuation queued",
			ResponsibilityContext: agentcore.CloneResponsibilityContext(session.ResponsibilityContext),
		},
		Metadata: responsibilityRunMetadata(map[string]string{
			"command":                         "mission.ops.resume_approval_run",
			"approval_ref":                    cmd.ApprovalRef,
			"approval_type":                   cmd.ApprovalType,
			"approval_continuation_of_run_id": session.RunID.String(),
		}, session.ResponsibilityContext),
	}
	if err := s.events.Append(ctx, event); err != nil {
		return CommandReceiptDTO{}, err
	}
	projectRuntimeEvent(&model, event, now)
	if err := saveMissionProjection(ctx, s.repo, viewer.Tenant(), model); err != nil {
		cleanupErr := s.runStore.Cancel(context.WithoutCancel(ctx), agentcore.AgentRunID(runID), "Mission projection commit failed")
		return CommandReceiptDTO{}, errors.Join(err, cleanupErr)
	}
	receipt := CommandReceiptDTO{CommandID: runID, Status: "accepted", MissionID: cmd.MissionID.String(), TaskID: session.TaskID.String(), Version: model.Checkpoint.Version, AcceptedAt: now}
	_ = s.recordViewerAudit(ctx, viewer, AuditEvent{
		ActorID:       viewer.ViewerID,
		ViewerID:      viewer.ViewerID,
		MissionID:     cmd.MissionID.String(),
		EntityType:    "agent_run",
		EntityID:      runID,
		Action:        "agent_run.approval_continuation",
		Result:        "succeeded",
		Severity:      "info",
		CorrelationID: firstString(cmd.RequestID, cmd.IdempotencyKey),
		Summary:       "queued approval continuation agent run",
		Metadata:      responsibilityRunMetadata(map[string]string{"approval_ref": cmd.ApprovalRef, "approval_type": cmd.ApprovalType, "blocked_run_id": session.RunID.String()}, session.ResponsibilityContext),
		OccurredAt:    now,
	})
	return receipt, nil
}

// ResumeRunAfterDurableHIL queues exactly one continuation for a detached
// mission runtime. The run ID is derived from the single-use HIL receipt so a
// retry between queue insertion and projection persistence cannot duplicate
// execution.
func (s *CommandService) ResumeRunAfterDurableHIL(ctx context.Context, cmd ResumeDurableHILRunCommand) (CommandReceiptDTO, error) {
	if s == nil || s.runStore == nil || s.repo == nil || s.events == nil || s.ids == nil {
		return CommandReceiptDTO{}, ErrServiceDependency
	}
	cmd.TenantID = strings.TrimSpace(cmd.TenantID)
	cmd.ApprovalRef = strings.TrimSpace(cmd.ApprovalRef)
	cmd.ToolName = strings.TrimSpace(cmd.ToolName)
	cmd.HILDecisionID = strings.TrimSpace(cmd.HILDecisionID)
	cmd.HILReceiptID = strings.TrimSpace(cmd.HILReceiptID)
	cmd.HILArgsHash = strings.TrimSpace(cmd.HILArgsHash)
	if cmd.IdempotencyKey == "" || cmd.TenantID == "" || cmd.MissionID.IsZero() || cmd.RunID == "" || cmd.ApprovalRef == "" ||
		cmd.ToolName != "mcp_marketing_marketing_wechat_draft_create" || cmd.HILDecisionID == "" || cmd.HILReceiptID == "" || cmd.HILArgsHash == "" {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	metadata := cloneStringMap(cmd.Metadata)
	if metadata == nil {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	if strings.TrimSpace(metadata["approval_run_id"]) != cmd.RunID.String() ||
		strings.TrimSpace(metadata[runtimecore.ApprovalMetadataRequestID]) != cmd.ApprovalRef ||
		strings.TrimSpace(metadata[runtimecore.ApprovalMetadataToolName]) != cmd.ToolName ||
		strings.TrimSpace(metadata["hil_decision_id"]) != cmd.HILDecisionID ||
		strings.TrimSpace(metadata["hil_receipt_id"]) != cmd.HILReceiptID ||
		strings.TrimSpace(metadata["hil_receipt_effect"]) != "authorize" ||
		strings.TrimSpace(metadata["approval_material_kind"]) != "wechat_draft" ||
		strings.TrimSpace(metadata["approval_material_hash"]) == "" ||
		strings.TrimSpace(metadata["approval_material_account_id"]) == "" ||
		strings.TrimSpace(metadata["approval_material_intent_id"]) == "" ||
		strings.TrimSpace(metadata["approval_material_intent_hash"]) == "" {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	model, err := loadMissionProjection(ctx, s.repo, cmd.TenantID, cmd.MissionID)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	if model.Mission.Status != projection.MissionStatusActive &&
		!(model.Mission.Status == projection.MissionStatusPaused && strings.EqualFold(strings.TrimSpace(model.Mission.OriginKind), "scheduler")) {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	if hasAppliedRuntimeIdempotencyKey(model, cmd.IdempotencyKey) {
		return CommandReceiptDTO{CommandID: durableHILContinuationRunID(cmd.HILReceiptID, cmd.ApprovalRef).String(), Status: "accepted", MissionID: cmd.MissionID.String(), Version: model.Checkpoint.Version}, nil
	}
	session, ok := findRuntimeSession(model, cmd.RunID)
	if !ok {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	task, ok := findProjectionTask(model, session.TaskID)
	if !ok {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	task, _, err = s.hydrateRuntimeTaskContract(ctx, cmd.TenantID, cmd.MissionID, &model, task)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	runID := durableHILContinuationRunID(cmd.HILReceiptID, cmd.ApprovalRef)
	eventID, err := s.ids.NewIDContext()
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	now := s.nowUTC()
	queueID := strings.TrimSpace(session.QueueID)
	if queueID == "" {
		queueID = runtimeQueueID(StartAgentRunCommand{MissionID: cmd.MissionID, TaskID: session.TaskID}, model)
	}
	runtimeKind := continuationRuntimeKind(session.RuntimeKind)
	metadata["approval_id"] = firstString(strings.TrimSpace(metadata["approval_id"]), cmd.ApprovalRef)
	metadata["approval_mode"] = "manual"
	metadata["approval_decided_by"] = firstString(strings.TrimSpace(cmd.ResolverID), strings.TrimSpace(metadata["approval_decided_by"]), "hil")
	metadata["approval_decided_at"] = firstString(strings.TrimSpace(cmd.DecidedAt), strings.TrimSpace(metadata["approval_decided_at"]), now.Format(time.RFC3339Nano))
	continuation := runtimecore.ApprovalContinuation{
		Decision: runtimecore.ApprovalDecisionApproved,
		ActionID: "approve_once",
		Effect:   "once",
		Metadata: metadata,
	}
	continuationPrompt := durableHILWechatContinuationPrompt(metadata, continuation)
	start := StartAgentRunCommand{
		RequestID: cmd.HILDecisionID, IdempotencyKey: cmd.IdempotencyKey,
		MissionID: cmd.MissionID, TaskID: session.TaskID, AgentID: session.AgentID.String(),
		RuntimeKind: runtimeKind, QueueID: queueID,
		Prompt:                continuationPrompt,
		ResponsibilityContext: agentcore.CloneResponsibilityContext(session.ResponsibilityContext),
	}
	promptBlocks, err := s.runtimePromptBlocks(ctx)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	conversationContext, err := s.runtimeConversationContext(ctx, model, session.AgentID)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	run := runtimedaemon.AgentRun{
		RunID: runID, AgentID: session.AgentID,
		AgentProfileID:  agentProfileIDForRuntime(model, session.AgentID),
		AgentInstanceID: agentInstanceIDForRuntime(model, session.AgentID, cmd.MissionID),
		RuntimeID:       runtimeKind, MissionID: cmd.MissionID.String(), TaskID: session.TaskID.String(),
		Status: runtimedaemon.RunStatusQueued, Task: taskInputFromProjection(start, task),
		Request: runtimeRequestFromCommand(start, runID.String(), session.AgentID, model, task, now, queueID, promptBlocks, conversationContext),
	}
	// Detached HIL continuations can run long after the provider process that
	// created the approval request has exited. Keep the original session in the
	// approval metadata for audit, but open a fresh provider session using the
	// fully bound continuation context instead of making execution depend on a
	// stale backend session handshake.
	run.Request.RuntimeOptions.ResumeSessionID = ""
	minimizeDurableHILContinuationRun(&run, continuationPrompt)
	run.Request.RuntimeOptions.ProviderSpecific = durableHILContinuationProviderSpecific(metadata, continuation)
	run.Request.RuntimeOptions.ProviderSpecific["approval_continuation_of_run_id"] = session.RunID.String()
	run.Request.RuntimeOptions.ProviderSpecific["hil_args_hash"] = cmd.HILArgsHash
	if err := s.runStore.Enqueue(ctx, run); err != nil {
		return CommandReceiptDTO{}, err
	}
	streamVersion, err := s.nextRuntimeMissionEventVersion(ctx, cmd.TenantID, cmd.MissionID, model)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	viewer := ViewerRef{ViewerID: "hil-runtime-bridge", ViewerKind: ViewerKindSystem, TenantID: cmd.TenantID}
	event := missionevent.Envelope{
		EventID: eventID, TenantID: cmd.TenantID, MissionID: cmd.MissionID,
		StreamID: missionevent.MissionStreamID(cmd.TenantID, cmd.MissionID), StreamVersion: streamVersion,
		EventType: missionevent.EventAgentRunQueued, EventVersion: 1, Actor: actorFromViewer(viewer),
		CorrelationID: cmd.HILDecisionID, IdempotencyKey: cmd.IdempotencyKey, OccurredAt: now,
		Payload: missionevent.RuntimeLifecyclePayload{
			TaskID: session.TaskID, AgentID: session.AgentID, RunID: runID, RuntimeKind: runtimeKind,
			QueueID: queueID, Status: string(projection.RuntimeSessionQueued),
			Message:               "detached HIL approval continuation queued",
			ResponsibilityContext: agentcore.CloneResponsibilityContext(session.ResponsibilityContext),
		},
		Metadata: responsibilityRunMetadata(map[string]string{
			"command": "mission.ops.resume_durable_hil_run", "approval_ref": cmd.ApprovalRef,
			"approval_continuation_of_run_id": session.RunID.String(), "hil_decision_id": cmd.HILDecisionID,
			"hil_receipt_id": cmd.HILReceiptID,
		}, session.ResponsibilityContext),
	}
	if err := s.events.Append(ctx, event); err != nil {
		return CommandReceiptDTO{}, err
	}
	projectRuntimeEvent(&model, event, now)
	model.AppliedEventIDs = append(model.AppliedEventIDs, event.EventID)
	model.AppliedIdempotencyKeys = append(model.AppliedIdempotencyKeys, cmd.IdempotencyKey)
	if err := saveMissionProjection(ctx, s.repo, cmd.TenantID, model); err != nil {
		cleanupErr := s.runStore.Cancel(context.WithoutCancel(ctx), runID, "Mission projection commit failed")
		return CommandReceiptDTO{}, errors.Join(err, cleanupErr)
	}
	return CommandReceiptDTO{CommandID: runID.String(), Status: "accepted", MissionID: cmd.MissionID.String(), TaskID: session.TaskID.String(), Version: model.Checkpoint.Version, AcceptedAt: now}, nil
}

func durableHILContinuationRunID(receiptID, approvalRef string) agentcore.AgentRunID {
	binding := strings.TrimSpace(receiptID) + "|" + strings.TrimSpace(approvalRef)
	digest := sha256.Sum256([]byte(binding))
	return agentcore.AgentRunID("hil-continuation-" + strings.TrimSpace(receiptID) + "-" + hex.EncodeToString(digest[:6]))
}

func hasAppliedRuntimeIdempotencyKey(model projection.ReadModel, key string) bool {
	for _, applied := range model.AppliedIdempotencyKeys {
		if applied == key {
			return true
		}
	}
	return false
}

func durableHILContinuationProviderSpecific(metadata map[string]string, continuation runtimecore.ApprovalContinuation) map[string]string {
	providerSpecific := runtimecore.ApprovalContinuationProviderSpecific(metadata, continuation)
	for _, key := range []string{
		"approval_id", "approval_mode", "approval_decided_by", "approval_decided_at",
		"hil_decision_id", "hil_receipt_id", "hil_receipt_effect",
		"approval_material_kind", "approval_material_hash", "approval_material_account_id",
		"approval_material_intent_id", "approval_material_intent_hash",
	} {
		if value := strings.TrimSpace(metadata[key]); value != "" {
			providerSpecific[key] = value
		}
	}
	return providerSpecific
}

type runtimeFailureCommandEventInput struct {
	MissionID      missioncore.MissionID
	TaskID         missioncore.TaskID
	AgentID        agentcore.AgentID
	RunID          agentcore.AgentRunID
	RequestID      string
	IdempotencyKey string
	EventType      missionevent.EventType
	Code           string
	Message        string
	CommandName    string
	Metadata       map[string]string
	StreamVersion  int
}

func (s *CommandService) runtimeFailureCommandEvent(ctx context.Context, viewer ViewerRef, input runtimeFailureCommandEventInput, model projection.ReadModel) (missionevent.Envelope, error) {
	eventID, err := s.ids.NewIDContext()
	if err != nil {
		return missionevent.Envelope{}, err
	}
	streamVersion := input.StreamVersion
	if streamVersion <= 0 {
		return missionevent.Envelope{}, ErrInvalidRequest
	}
	metadata := cloneStringMap(input.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["command"] = input.CommandName
	responsibility := runtimeResponsibilityContext(model, input.RunID)
	metadata = responsibilityRunMetadata(metadata, responsibility)
	return missionevent.Envelope{
		EventID:        eventID,
		TenantID:       viewer.Tenant(),
		MissionID:      input.MissionID,
		StreamID:       missionevent.MissionStreamID(viewer.Tenant(), input.MissionID),
		StreamVersion:  streamVersion,
		EventType:      input.EventType,
		EventVersion:   1,
		Actor:          actorFromViewer(viewer),
		CorrelationID:  firstString(input.RequestID, input.IdempotencyKey),
		IdempotencyKey: input.IdempotencyKey,
		OccurredAt:     s.nowUTC(),
		Payload: missionevent.RuntimeFailurePayload{
			TaskID:                input.TaskID,
			AgentID:               input.AgentID,
			RunID:                 input.RunID,
			Code:                  input.Code,
			Message:               input.Message,
			Metadata:              cloneStringMap(input.Metadata),
			ResponsibilityContext: responsibility,
		},
		Metadata: metadata,
	}, nil
}

func continuationRuntimeKind(value string) string {
	if kind := strings.TrimSpace(value); kind != "" {
		return kind
	}
	return string(agentcore.RuntimeKindPi)
}

func findRuntimeSession(model projection.ReadModel, runID agentcore.AgentRunID) (projection.RuntimeSessionView, bool) {
	for _, session := range model.RuntimeSessions {
		if session.RunID == runID {
			return session, true
		}
	}
	return projection.RuntimeSessionView{}, false
}

func findApprovalRequest(model projection.ReadModel, approvalType, approvalRef string) (projection.ApprovalRequestView, bool) {
	for _, request := range model.ApprovalRequests {
		if request.ApprovalType == approvalType && request.RequestID == approvalRef {
			return request, true
		}
	}
	return projection.ApprovalRequestView{}, false
}

func isProviderApprovalType(approvalType string, metadata map[string]string) bool {
	if strings.TrimSpace(metadata["approval_request_id"]) == "" {
		return false
	}
	if strings.TrimSpace(metadata["approval_source"]) != "" || strings.TrimSpace(metadata["approval_provider"]) != "" {
		return true
	}
	switch approvalType {
	case "command_execution", "file_change", "mcp_elicitation", "tool_use", "network_access", "user_input", "permission_request":
		return strings.TrimSpace(metadata["approval_kind"]) != ""
	default:
		return false
	}
}

func approvalRunID(metadata map[string]string) agentcore.AgentRunID {
	for _, key := range []string{"approval_run_id", "run_id", "runtime_run_id"} {
		if value := strings.TrimSpace(metadata[key]); value != "" {
			return agentcore.AgentRunID(value)
		}
	}
	return ""
}

func approvalDecisionEffectForAction(actionID string) string {
	switch strings.TrimSpace(actionID) {
	case "approve_session":
		return "session"
	case "approve_edited":
		return "modified"
	case "modify":
		return "modify"
	case "answer":
		return "answer"
	case "needs_context":
		return "needs_context"
	case "deny", "reject", "dismiss":
		return "deny"
	default:
		return "once"
	}
}

func isTerminalRuntimeSession(status projection.RuntimeSessionStatus) bool {
	switch status {
	case projection.RuntimeSessionCompleted, projection.RuntimeSessionFailed, projection.RuntimeSessionTimedOut, projection.RuntimeSessionCancelled, projection.RuntimeSessionBlocked:
		return true
	default:
		return false
	}
}

func isRetryableRuntimeSession(status projection.RuntimeSessionStatus) bool {
	switch status {
	case projection.RuntimeSessionFailed, projection.RuntimeSessionTimedOut, projection.RuntimeSessionCancelled, projection.RuntimeSessionBlocked:
		return true
	default:
		return false
	}
}

func agentProfileIDForRuntime(model projection.ReadModel, agentID agentcore.AgentID) agentcore.AgentProfileID {
	member := teamMemberLookup(model.Team)[agentID.String()]
	if !member.AgentProfileID.IsZero() {
		return member.AgentProfileID
	}
	return agentcore.AgentProfileID(agentID.String())
}

func agentInstanceIDForRuntime(model projection.ReadModel, agentID agentcore.AgentID, missionID missioncore.MissionID) agentcore.AgentInstanceID {
	member := teamMemberLookup(model.Team)[agentID.String()]
	if !member.AgentInstanceID.IsZero() {
		return member.AgentInstanceID
	}
	return agentcore.AgentInstanceID(agentID.String() + ":" + missionID.String())
}
