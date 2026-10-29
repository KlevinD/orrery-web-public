package missioncontrol

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/mission/projection"
)

type StartAgentRunCommand struct {
	RequestID             string
	IdempotencyKey        string
	MissionID             missioncore.MissionID
	TaskID                missioncore.TaskID
	AgentID               string
	RuntimeKind           string
	Prompt                string
	QueueID               string
	RunID                 string
	ParentRunID           string
	Attempt               int
	PermissionScope       []string
	ResponsibilityContext *agentcore.ResponsibilityContext
	ExecutionEnvelope     *agentcore.ExecutionEnvelope
}

const responsibilityCoordinatorViewerID = "responsibility-coordinator"

func (s *CommandService) StartAgentRun(ctx context.Context, viewer ViewerRef, cmd StartAgentRunCommand) (CommandReceiptDTO, error) {
	return s.startAgentRun(ctx, viewer, cmd, "mission.edit", false)
}

// DispatchResponsibilityRun is the internal responsibility-coordinator entry
// point. It permits only the assigned Agent to dispatch its deterministic run;
// HTTP commands continue through StartAgentRun and require mission.edit.
func (s *CommandService) DispatchResponsibilityRun(ctx context.Context, viewer ViewerRef, cmd StartAgentRunCommand) (CommandReceiptDTO, error) {
	if viewer.ViewerKind != ViewerKindAgent || strings.TrimSpace(cmd.RunID) == "" || !validResponsibilityExecutionContext(cmd.ResponsibilityContext) {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	return s.startAgentRun(ctx, viewer, cmd, "mission.view", true)
}

func validResponsibilityExecutionContext(value *agentcore.ResponsibilityContext) bool {
	return value != nil &&
		strings.TrimSpace(value.ContextID) != "" &&
		strings.TrimSpace(value.ResponsibilityID) != "" &&
		strings.TrimSpace(value.PolicyID) != "" &&
		strings.TrimSpace(value.AgentProfileID) != "" &&
		value.ResponsibilityVersion > 0 &&
		value.PolicyVersion > 0
}

func (s *CommandService) startAgentRun(ctx context.Context, viewer ViewerRef, cmd StartAgentRunCommand, authorizationAction string, assignedAgentOnly bool) (CommandReceiptDTO, error) {
	if s == nil || s.runStore == nil || s.repo == nil || s.events == nil || s.ids == nil {
		return CommandReceiptDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return CommandReceiptDTO{}, err
	}
	if cmd.IdempotencyKey == "" || cmd.MissionID.IsZero() || cmd.TaskID.IsZero() || strings.TrimSpace(cmd.AgentID) == "" || strings.TrimSpace(cmd.RuntimeKind) == "" {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	if responsibility := cmd.ResponsibilityContext; responsibility != nil && !validResponsibilityExecutionContext(responsibility) {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	if cmd.ExecutionEnvelope != nil {
		if err := cmd.ExecutionEnvelope.Verify(); err != nil {
			return CommandReceiptDTO{}, ErrInvalidRequest
		}
		if !cmd.ExecutionEnvelope.ExpiresAt.IsZero() && !cmd.ExecutionEnvelope.ExpiresAt.After(s.nowUTC()) {
			return CommandReceiptDTO{}, ErrInvalidRequest
		}
	}
	if responsibility := cmd.ResponsibilityContext; responsibility != nil && !responsibility.ExpiresAt.IsZero() && !responsibility.ExpiresAt.After(s.nowUTC()) {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	if err := s.authorizeMissionAction(ctx, viewer, cmd.MissionID, authorizationAction); err != nil {
		return CommandReceiptDTO{}, err
	}
	model, err := loadMissionProjection(ctx, s.repo, viewer.Tenant(), cmd.MissionID)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	task, ok := findProjectionTask(model, cmd.TaskID)
	if !ok {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	if s.runtimeRestartGuard != nil {
		if err := s.runtimeRestartGuard.CheckTaskRestart(ctx, viewer.Tenant(), cmd.MissionID.String(), cmd.TaskID.String()); err != nil {
			return CommandReceiptDTO{}, err
		}
	}
	task, _, err = s.hydrateRuntimeTaskContract(ctx, viewer.Tenant(), cmd.MissionID, &model, task)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	delegationParent, delegatedResponsibilityLane := pausedSchedulerResponsibilityDelegation(model, viewer, cmd, task, s.nowUTC())
	pausedScheduledOccurrence := assignedAgentOnly && model.Mission.Status == projection.MissionStatusPaused && strings.TrimSpace(task.SourceKind) == "scheduler_agent_task"
	if model.Mission.Status != projection.MissionStatusActive && !pausedScheduledOccurrence && !delegatedResponsibilityLane {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	if assignedAgentOnly {
		viewerAgentID := strings.TrimSpace(viewer.ViewerID)
		if task.Assignee.String() != viewerAgentID || strings.TrimSpace(cmd.AgentID) != viewerAgentID {
			return CommandReceiptDTO{}, ErrProjectionNotFound
		}
	}
	member := teamMemberLookup(model.Team)[strings.TrimSpace(cmd.AgentID)]
	agentID := agentcore.AgentID(firstString(member.AgentID.String(), strings.TrimSpace(cmd.AgentID)))
	profileID := agentcore.AgentProfileID(firstString(member.AgentProfileID.String(), string(agentID)))
	instanceID := member.AgentInstanceID
	if instanceID.IsZero() {
		instanceID = agentcore.AgentInstanceID(firstString(task.Assignee.String(), string(agentID)) + ":" + cmd.MissionID.String())
	}
	runID := strings.TrimSpace(cmd.RunID)
	if runID != "" {
		for _, session := range model.RuntimeSessions {
			if session.RunID.String() == runID {
				if session.TaskID != cmd.TaskID || session.AgentID.String() != agentID.String() || strings.TrimSpace(session.RuntimeKind) != strings.TrimSpace(cmd.RuntimeKind) {
					return CommandReceiptDTO{}, ErrInvalidRequest
				}
				return CommandReceiptDTO{CommandID: runID, Status: "accepted", MissionID: cmd.MissionID.String(), TaskID: cmd.TaskID.String(), Version: model.Checkpoint.Version, AcceptedAt: session.QueuedAt}, nil
			}
		}
	}
	if !taskAcceptsNewAgentRun(task.Status) {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	for sessionIndex, session := range model.RuntimeSessions {
		if session.TaskID == cmd.TaskID && runtimeSessionIsActive(model.RuntimeSessions, sessionIndex) {
			return CommandReceiptDTO{}, ErrAgentRunAlreadyActive
		}
	}
	if runID == "" {
		runID, err = s.ids.NewIDContext()
		if err != nil {
			return CommandReceiptDTO{}, err
		}
	}
	if cmd.ExecutionEnvelope != nil && !executionEnvelopeMatchesRun(cmd.ExecutionEnvelope, viewer.Tenant(), runID, agentID, profileID, cmd.MissionID.String(), cmd.TaskID.String(), cmd.ResponsibilityContext) {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	eventID, err := s.ids.NewIDContext()
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	now := s.nowUTC()
	queueID := runtimeQueueID(cmd, model)
	promptBlocks, err := s.runtimePromptBlocks(ctx)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	conversationContext, err := s.runtimeConversationContext(ctx, model, agentID)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	runtimeRequest := runtimeRequestFromCommand(cmd, runID, agentID, model, task, now, queueID, promptBlocks, conversationContext)
	if runtimeRequest.ExecutionEnvelope == nil {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	if delegatedResponsibilityLane {
		applyResponsibilityDelegationMetadata(runtimeRequest.RuntimeOptions.ProviderSpecific, delegationParent)
	}
	run := runtimedaemon.AgentRun{
		RunID:           agentcore.AgentRunID(runID),
		AgentID:         agentID,
		AgentProfileID:  profileID,
		AgentInstanceID: instanceID,
		RuntimeID:       strings.TrimSpace(cmd.RuntimeKind),
		MissionID:       cmd.MissionID.String(),
		TaskID:          cmd.TaskID.String(),
		Status:          runtimedaemon.RunStatusQueued,
		Attempt:         cmd.Attempt,
		Task:            taskInputFromProjection(cmd, task),
		Request:         runtimeRequest,
	}
	if s.missions != nil && task.Status == projection.TaskStatusCreated {
		assignViewer := viewer
		if assignedAgentOnly {
			// Responsibility dispatch is authorized as the worker for mission.read.
			// Materializing a stale created card is a coordinator lifecycle write;
			// keep that privilege on the system coordinator instead of escalating the worker.
			assignViewer = ViewerRef{
				ViewerID:               responsibilityCoordinatorViewerID,
				ViewerKind:             ViewerKindSystem,
				TenantID:               viewer.Tenant(),
				AuthorizationSubjectID: responsibilityCoordinatorViewerID,
				PermissionScope:        []string{"mission.write"},
			}
		}
		if _, err := s.AssignTask(ctx, assignViewer, AssignTaskCommand{
			RequestID:      firstString(cmd.RequestID, cmd.IdempotencyKey),
			IdempotencyKey: cmd.IdempotencyKey + "|assign",
			MissionID:      cmd.MissionID,
			TaskID:         cmd.TaskID,
			AssigneeID:     agentID.String(),
			Reason:         "agent runtime run queued",
		}); err != nil {
			return CommandReceiptDTO{}, err
		}
		model, err = loadMissionProjection(ctx, s.repo, viewer.Tenant(), cmd.MissionID)
		if err != nil {
			return CommandReceiptDTO{}, err
		}
	}
	streamVersion, err := s.nextRuntimeMissionEventVersion(ctx, viewer.Tenant(), cmd.MissionID, model)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	eventMetadata := map[string]string{"command": "mission.ops.start_agent_run"}
	for key, value := range responsibilityContextMetadata(cmd.ResponsibilityContext) {
		eventMetadata[key] = value
	}
	if delegatedResponsibilityLane {
		applyResponsibilityDelegationMetadata(eventMetadata, delegationParent)
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
			TaskID:                cmd.TaskID,
			AgentID:               agentID,
			RunID:                 agentcore.AgentRunID(runID),
			RuntimeKind:           strings.TrimSpace(cmd.RuntimeKind),
			QueueID:               queueID,
			Status:                string(projection.RuntimeSessionQueued),
			Message:               "agent run queued",
			ResponsibilityContext: agentcore.CloneResponsibilityContext(cmd.ResponsibilityContext),
		},
		Metadata: eventMetadata,
	}
	if err := s.events.Append(ctx, event); err != nil {
		return CommandReceiptDTO{}, err
	}
	projectRuntimeEvent(&model, event, now)
	if err := saveMissionProjection(ctx, s.repo, viewer.Tenant(), model); err != nil {
		return CommandReceiptDTO{}, err
	}
	// Persist the queued event and projection before releasing the run to the
	// execution worker. The worker can publish AgentRunStarted immediately;
	// enqueueing first lets it race the queued event at the same stream version.
	if err := s.runStore.Enqueue(ctx, run); err != nil {
		cleanupErr := s.runStore.Cancel(context.WithoutCancel(ctx), agentcore.AgentRunID(runID), "Agent runtime enqueue failed")
		return CommandReceiptDTO{}, errors.Join(err, cleanupErr)
	}
	receipt := CommandReceiptDTO{CommandID: runID, Status: "accepted", MissionID: cmd.MissionID.String(), TaskID: cmd.TaskID.String(), Version: model.Checkpoint.Version, AcceptedAt: now}
	if err := s.recordViewerAudit(ctx, viewer, AuditEvent{ActorID: viewer.ViewerID, ViewerID: viewer.ViewerID, MissionID: cmd.MissionID.String(), EntityType: "agent_run", EntityID: runID, Action: "agent_run.start", Result: "succeeded", Severity: "info", CorrelationID: firstString(cmd.RequestID, cmd.IdempotencyKey), Summary: "queued agent run", Metadata: cloneStringMap(eventMetadata), OccurredAt: now}); err != nil {
		return CommandReceiptDTO{}, err
	}
	return receipt, nil
}

type responsibilityDelegationParent struct {
	RunID                   string
	TaskID                  string
	ResponsibilityContextID string
}

func pausedSchedulerResponsibilityDelegation(model projection.ReadModel, viewer ViewerRef, cmd StartAgentRunCommand, task projection.TaskCardView, now time.Time) (responsibilityDelegationParent, bool) {
	parentRunID := strings.TrimSpace(cmd.ParentRunID)
	viewerAgentID := strings.TrimSpace(viewer.ViewerID)
	if model.Mission.Status != projection.MissionStatusPaused ||
		!strings.EqualFold(strings.TrimSpace(model.Mission.OriginKind), "scheduler") ||
		viewer.ViewerKind != ViewerKindAgent || parentRunID == "" || viewerAgentID == "" ||
		task.Assignee.String() != strings.TrimSpace(cmd.AgentID) {
		return responsibilityDelegationParent{}, false
	}
	for _, session := range model.RuntimeSessions {
		if session.RunID.String() != parentRunID || session.Status != projection.RuntimeSessionRunning || session.AgentID.String() != viewerAgentID {
			continue
		}
		responsibility := session.ResponsibilityContext
		if !validResponsibilityExecutionContext(responsibility) ||
			responsibility.AgentProfileID != viewerAgentID ||
			(!responsibility.ExpiresAt.IsZero() && !responsibility.ExpiresAt.After(now)) ||
			(strings.TrimSpace(responsibility.MissionID) != "" && strings.TrimSpace(responsibility.MissionID) != model.Mission.MissionID.String()) ||
			(strings.TrimSpace(responsibility.TaskID) != "" && strings.TrimSpace(responsibility.TaskID) != session.TaskID.String()) ||
			!hasPermissionScope(responsibility.TaskPermissionScopes, "mission.write") {
			continue
		}
		parentTask, ok := findProjectionTask(model, session.TaskID)
		if !ok || strings.TrimSpace(parentTask.SourceKind) != "scheduler_agent_task" || strings.TrimSpace(parentTask.SourceID) != strings.TrimSpace(model.Mission.OriginID) {
			continue
		}
		return responsibilityDelegationParent{
			RunID:                   parentRunID,
			TaskID:                  session.TaskID.String(),
			ResponsibilityContextID: strings.TrimSpace(responsibility.ContextID),
		}, true
	}
	return responsibilityDelegationParent{}, false
}

func hasPermissionScope(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(wanted)) {
			return true
		}
	}
	return false
}

func applyResponsibilityDelegationMetadata(metadata map[string]string, parent responsibilityDelegationParent) {
	if metadata == nil {
		return
	}
	metadata["delegation_source"] = "responsibility_plan"
	metadata["delegated_by_run_id"] = strings.TrimSpace(parent.RunID)
	metadata["delegated_by_task_id"] = strings.TrimSpace(parent.TaskID)
	metadata["delegated_by_responsibility_context_id"] = strings.TrimSpace(parent.ResponsibilityContextID)
}

func taskAcceptsNewAgentRun(status projection.TaskStatus) bool {
	switch status {
	case projection.TaskStatusCreated,
		projection.TaskStatusAssigned,
		projection.TaskStatusAccepted,
		projection.TaskStatusStarted,
		projection.TaskStatusBlocked,
		projection.TaskStatusReworkRequested:
		return true
	default:
		return false
	}
}

func runtimeQueueID(cmd StartAgentRunCommand, model projection.ReadModel) string {
	if value := strings.TrimSpace(cmd.QueueID); value != "" {
		return value
	}
	if missionID := strings.TrimSpace(cmd.MissionID.String()); missionID != "" {
		if agentID := strings.TrimSpace(cmd.AgentID); agentID != "" {
			return "mission:" + missionID + ":agent:" + agentID
		}
		return "mission:" + missionID
	}
	if value := strings.TrimSpace(model.Discussion.ConversationID); value != "" {
		return "conversation:" + value
	}
	if value := strings.TrimSpace(model.Discussion.DiscussionGroupID); value != "" {
		return "discussion:" + value
	}
	return "default"
}

func responsibilityContextMetadata(context *agentcore.ResponsibilityContext) map[string]string {
	if context == nil {
		return nil
	}
	return map[string]string{
		"responsibility_context_id":      strings.TrimSpace(context.ContextID),
		"responsibility_id":              strings.TrimSpace(context.ResponsibilityID),
		"responsibility_actor_member_id": strings.TrimSpace(context.ActorMemberID),
		"responsibility_version":         strconv.FormatInt(context.ResponsibilityVersion, 10),
		"responsibility_policy_id":       strings.TrimSpace(context.PolicyID),
		"responsibility_policy_version":  strconv.FormatInt(context.PolicyVersion, 10),
		"responsibility_source":          strings.TrimSpace(context.Source),
	}
}

func responsibilityRunMetadata(metadata map[string]string, context *agentcore.ResponsibilityContext) map[string]string {
	out := cloneStringMap(metadata)
	if out == nil && context != nil {
		out = map[string]string{}
	}
	for key, value := range responsibilityContextMetadata(context) {
		out[key] = value
	}
	return out
}

func (s *CommandService) nextRuntimeMissionEventVersion(ctx context.Context, tenantID string, missionID missioncore.MissionID, model projection.ReadModel) (int, error) {
	if s != nil && s.eventReader != nil {
		events, err := loadMissionEvents(ctx, s.eventReader, tenantID, missionID)
		if err != nil {
			return 0, err
		}
		return len(events) + 1, nil
	}
	return model.Checkpoint.Version + 1, nil
}

func projectRuntimeEvent(model *projection.ReadModel, event missionevent.Envelope, fallbackAt time.Time) {
	if model == nil {
		return
	}
	at := event.OccurredAt
	if at.IsZero() {
		at = fallbackAt
	}
	projected := event
	projected.OccurredAt = at
	var runID string
	var details string
	progressStatus := "succeeded"
	switch payload := event.Payload.(type) {
	case missionevent.RuntimeLifecyclePayload:
		runID = payload.RunID.String()
		details = payload.Message
	case *missionevent.RuntimeLifecyclePayload:
		if payload == nil {
			return
		}
		runID = payload.RunID.String()
		details = payload.Message
	case missionevent.RuntimeFailurePayload:
		runID = payload.RunID.String()
		details = payload.Message
		progressStatus = "failed"
	case *missionevent.RuntimeFailurePayload:
		if payload == nil {
			return
		}
		runID = payload.RunID.String()
		details = payload.Message
		progressStatus = "failed"
	default:
		return
	}
	if !projection.ApplyRuntimeEventState(model, projected) {
		return
	}
	appendRuntimeProgress(model, projected, runID, details, at, progressStatus)
	model.Checkpoint.MissionID = event.MissionID
	model.Checkpoint.Version++
	model.Checkpoint.UpdatedAt = at
	model.UpdatedAt = at
}

func appendRuntimeProgress(model *projection.ReadModel, event missionevent.Envelope, runID, details string, at time.Time, status string) {
	model.ProgressGraph.Nodes = append(model.ProgressGraph.Nodes, projection.ProgressNodeView{
		ID:         event.EventID,
		EventID:    event.EventID,
		EventType:  string(event.EventType),
		Label:      string(event.EventType),
		Status:     status,
		Details:    details,
		OccurredAt: at,
		RefType:    "runtime_run",
		RefID:      runID,
	})
	if len(model.ProgressGraph.Nodes) > 1 {
		previous := model.ProgressGraph.Nodes[len(model.ProgressGraph.Nodes)-2]
		model.ProgressGraph.Edges = append(model.ProgressGraph.Edges, projection.ProgressEdgeView{From: previous.ID, To: event.EventID, Kind: "observed_after"})
	}
	model.ProgressGraph.CurrentFocus = []string{event.EventID}
}

func runtimeProjectionStatus(eventType missionevent.EventType, status string) projection.RuntimeSessionStatus {
	switch eventType {
	case missionevent.EventAgentRunQueued:
		return projection.RuntimeSessionQueued
	case missionevent.EventAgentRunStarted:
		return projection.RuntimeSessionRunning
	case missionevent.EventAgentRunCompleted:
		return projection.RuntimeSessionCompleted
	}
	switch strings.TrimSpace(status) {
	case string(projection.RuntimeSessionQueued):
		return projection.RuntimeSessionQueued
	case string(projection.RuntimeSessionRunning):
		return projection.RuntimeSessionRunning
	case string(projection.RuntimeSessionCompleted):
		return projection.RuntimeSessionCompleted
	default:
		return ""
	}
}

func runtimeFailureProjectionStatus(eventType missionevent.EventType) projection.RuntimeSessionStatus {
	switch eventType {
	case missionevent.EventAgentRunTimedOut:
		return projection.RuntimeSessionTimedOut
	case missionevent.EventAgentRunCancelled:
		return projection.RuntimeSessionCancelled
	case missionevent.EventAgentRunBlocked:
		return projection.RuntimeSessionBlocked
	default:
		return projection.RuntimeSessionFailed
	}
}

func (s *CommandService) runtimePromptBlocks(ctx context.Context) ([]agentcore.RuntimePromptBlock, error) {
	if s == nil || s.promptBlocks == nil {
		return nil, nil
	}
	blocks, err := s.promptBlocks.PromptBlocks(ctx)
	if err != nil {
		return nil, err
	}
	return agentcore.NormalizeRuntimePromptBlocks(blocks), nil
}

func (s *CommandService) runtimeConversationContext(ctx context.Context, model projection.ReadModel, agentID agentcore.AgentID) (RuntimeConversationContext, error) {
	conversationID := strings.TrimSpace(model.Discussion.ConversationID)
	if conversationID == "" {
		return RuntimeConversationContext{}, nil
	}
	if s == nil || s.conversationContext == nil {
		return RuntimeConversationContext{}, ErrServiceDependency
	}
	result, err := s.conversationContext.RuntimeConversationContext(ctx, RuntimeConversationContextRequest{
		TenantID: missionModelTenantID(model), MissionID: model.Mission.MissionID.String(), ConversationID: conversationID, AgentID: agentID,
	})
	if err != nil {
		return RuntimeConversationContext{}, err
	}
	if strings.TrimSpace(result.ConversationID) != conversationID || result.Digest.ID == "" {
		return RuntimeConversationContext{}, ErrServiceDependency
	}
	result.ConversationID = conversationID
	return result, nil
}

func runtimeRequestFromCommand(cmd StartAgentRunCommand, runID string, agentID agentcore.AgentID, model projection.ReadModel, task projection.TaskCardView, at time.Time, queueID string, promptBlocks []agentcore.RuntimePromptBlock, conversation RuntimeConversationContext) agentcore.RuntimeRequest {
	prompt := strings.TrimSpace(cmd.Prompt)
	if prompt == "" {
		prompt = task.WorkerPersonalGoalSummary
	}
	if prompt == "" {
		prompt = task.Goal
	}
	contextPack := agentcore.ContextPack{
		ID:                  agentcore.ContextPackID("ctx-" + runID),
		MissionID:           cmd.MissionID,
		ConversationID:      strings.TrimSpace(conversation.ConversationID),
		AgentID:             agentID,
		TaskID:              cmd.TaskID,
		MissionFinalGoal:    model.Mission.FinalGoal,
		DiscussionGroupGoal: model.Mission.FinalGoal,
		AgentMissionRole:    "worker",
		WorkerPersonalGoal:  prompt,
		TaskContractRef:     agentcore.TaskContractRef{ID: cmd.TaskID.String()},
		TaskContractSummary: task.Goal,
		OverridePolicy:      agentcore.OverridePolicyPreserveProtected,
		CreatedAt:           at,
		SourceRefs: []agentcore.ContextSourceRef{{
			Kind:      agentcore.ContextSourceKindTaskContract,
			Ref:       agentcore.ContextSourceTypedRef{TaskContractRef: &agentcore.TaskContractRef{ID: cmd.TaskID.String()}},
			Priority:  agentcore.SourcePriorityContract,
			Immutable: true,
		}},
	}
	if conversation.Digest.ID != "" {
		contextPack.DiscussionDigestRef = conversation.Digest
		contextPack.DiscussionDigestSummary = strings.TrimSpace(conversation.Digest.SummaryRef)
		digest := conversation.Digest
		contextPack.SourceRefs = append(contextPack.SourceRefs, agentcore.ContextSourceRef{
			Kind:     agentcore.ContextSourceKindDiscussionDigest,
			Ref:      agentcore.ContextSourceTypedRef{DiscussionDigestRef: &digest},
			Priority: agentcore.SourcePriorityContext,
		})
	}
	if responsibility := agentcore.CloneResponsibilityContext(cmd.ResponsibilityContext); responsibility != nil {
		contextPack.ResponsibilityContext = responsibility
		contextPack.SourceRefs = append(contextPack.SourceRefs, agentcore.ContextSourceRef{
			Kind: agentcore.ContextSourceKindResponsibility,
			Ref: agentcore.ContextSourceTypedRef{
				ResponsibilityRef: &agentcore.ResponsibilityContextRef{ID: responsibility.ContextID},
			},
			Priority:  agentcore.SourcePriorityContext,
			Immutable: true,
		})
	}
	member := teamMemberLookup(model.Team)[agentID.String()]
	profile := agentcore.AgentProfile{
		ID:          agentcore.AgentProfileID(firstString(member.AgentProfileID.String(), agentID.String())),
		RealName:    member.RealName,
		DisplayName: firstString(member.DisplayName, member.RealName, agentID.String()),
		RoleName:    firstString(member.RoleName, member.MissionRole, "worker"),
		Persona: agentcore.AgentPersona{
			ResponsibilityBoundary: firstString(member.MissionRole, task.Goal, "执行分配的 Mission Task。"),
		},
		BaseAgent: agentcore.BaseAgentSpec{
			SystemPrompt: agentcore.DefaultBaseAgentSystemPrompt(firstString(member.DisplayName, member.RealName, agentID.String()), firstString(member.RoleName, member.MissionRole, "worker")),
			SkillScope:   "profile_bound",
			ToolScope:    "profile_bound",
			MCPScope:     "profile_bound",
		},
	}
	taskInput := taskInputFromProjection(cmd, task)
	messages := agentcore.BuildAgentPromptMessages(agentcore.AgentPromptMessagesRequest{
		Profile:     profile,
		ContextPack: contextPack,
		Task:        taskInput,
		Blocks:      promptBlocks,
	})
	providerSpecific := runtimeProviderSpecific(cmd, queueID, conversation)
	if responsibility := contextPack.ResponsibilityContext; responsibility != nil {
		for key, value := range responsibilityContextMetadata(responsibility) {
			providerSpecific[key] = value
		}
	}
	if strings.TrimSpace(task.SourceKind) == "scheduler_agent_task" {
		providerSpecific["run_source"] = "scheduler_agent_task"
		providerSpecific["scheduled_task_id"] = strings.TrimSpace(task.SourceID)
		providerSpecific["scheduled_run_id"] = strings.TrimSpace(task.SourceStepID)
		providerSpecific["generation_id"] = strings.TrimSpace(task.SourceStepID)
	}
	memberProfileID := firstString(teamMemberLookup(model.Team)[agentID.String()].AgentProfileID.String(), agentID.String())
	executionEnvelope := runtimeExecutionEnvelope(cmd, runID, agentID, agentcore.AgentProfileID(memberProfileID), model, task, contextPack, at)
	if cmd.ExecutionEnvelope != nil {
		if !executionEnvelopeWithinBounds(cmd.ExecutionEnvelope, executionEnvelope) {
			executionEnvelope = nil
		} else {
			executionEnvelope = agentcore.CloneExecutionEnvelope(cmd.ExecutionEnvelope)
		}
	}
	return agentcore.RuntimeRequest{
		RunID:             agentcore.AgentRunID(runID),
		TenantID:          missionModelTenantID(model),
		AgentID:           agentID,
		ExecutionEnvelope: executionEnvelope,
		MissionID:         cmd.MissionID.String(),
		TaskID:            cmd.TaskID.String(),
		ContextPack:       contextPack,
		InputMessages:     messages,
		RuntimeOptions: agentcore.RuntimeOptions{
			SystemPrompt:     agentcore.RuntimePromptMessagesSystemPrompt(messages),
			PromptBlocks:     append([]agentcore.RuntimePromptBlock(nil), promptBlocks...),
			ProviderSpecific: providerSpecific,
		},
		TracePolicy: agentcore.TracePolicy{CaptureMessages: true, CaptureRawOutput: true, RedactSecrets: true},
	}
}

func runtimeProviderSpecific(cmd StartAgentRunCommand, queueID string, conversation RuntimeConversationContext) map[string]string {
	values := map[string]string{
		"runtime_kind":         strings.TrimSpace(cmd.RuntimeKind),
		"queue_id":             strings.TrimSpace(queueID),
		"conversation_id":      strings.TrimSpace(conversation.ConversationID),
		"discussion_id":        strings.TrimSpace(conversation.ConversationID),
		"discussion_digest_id": strings.TrimSpace(string(conversation.Digest.ID)),
	}
	seen := map[string]bool{}
	scopes := make([]string, 0, len(cmd.PermissionScope))
	for _, scope := range cmd.PermissionScope {
		scope = strings.TrimSpace(scope)
		if scope == "" || seen[scope] {
			continue
		}
		seen[scope] = true
		scopes = append(scopes, scope)
	}
	if len(scopes) > 0 {
		values["permission_scope"] = strings.Join(scopes, ",")
	}
	return values
}

func taskInputFromProjection(cmd StartAgentRunCommand, task projection.TaskCardView) agentcore.TaskInput {
	input := agentcore.TaskInput{
		ID:                 cmd.TaskID.String(),
		MissionID:          cmd.MissionID.String(),
		Goal:               task.Goal,
		WorkerPersonalGoal: task.WorkerPersonalGoalSummary,
	}
	for _, item := range task.AcceptanceCriteria {
		input.AcceptanceCriteria = append(input.AcceptanceCriteria, agentcore.TaskAcceptanceInput{ID: item.ID, Description: item.Description})
	}
	for _, item := range task.ExpectedDeliverables {
		input.ExpectedDeliverables = append(input.ExpectedDeliverables, agentcore.TaskDeliverableInput{ID: item.ID, Title: item.Title})
	}
	for _, item := range task.RequiredEvidence {
		input.EvidenceRequirements = append(input.EvidenceRequirements, agentcore.TaskEvidenceInput{ID: item.ID, Description: item.Description, Kind: item.Kind, Required: item.Required})
	}
	return input
}

func mergeRuntimeTaskContract(runtimeTask, canonicalTask projection.TaskCardView) (projection.TaskCardView, error) {
	if canonicalTask.TaskID != runtimeTask.TaskID {
		return projection.TaskCardView{}, ErrInvalidRequest
	}
	runtimeTask.Goal = canonicalTask.Goal
	runtimeTask.SourceKind = canonicalTask.SourceKind
	runtimeTask.SourceID = canonicalTask.SourceID
	runtimeTask.SourceStepID = canonicalTask.SourceStepID
	runtimeTask.ExecutionPermissionScopes = append([]string(nil), canonicalTask.ExecutionPermissionScopes...)
	runtimeTask.ExecutionAllowedToolIDs = append([]string(nil), canonicalTask.ExecutionAllowedToolIDs...)
	runtimeTask.ExecutionEnvelope = agentcore.CloneExecutionEnvelope(canonicalTask.ExecutionEnvelope)
	runtimeTask.WorkerPersonalGoalSummary = canonicalTask.WorkerPersonalGoalSummary
	runtimeTask.Priority = canonicalTask.Priority
	runtimeTask.DueAt = canonicalTask.DueAt
	runtimeTask.AcceptanceCriteria = append([]projection.AcceptanceCriterionView(nil), canonicalTask.AcceptanceCriteria...)
	runtimeTask.ExpectedDeliverables = append([]projection.ExpectedDeliverableView(nil), canonicalTask.ExpectedDeliverables...)
	runtimeTask.RequiredEvidence = append([]projection.RequiredEvidenceView(nil), canonicalTask.RequiredEvidence...)
	return runtimeTask, nil
}

func (s *CommandService) hydrateRuntimeTaskContract(ctx context.Context, tenantID string, missionID missioncore.MissionID, model *projection.ReadModel, task projection.TaskCardView) (projection.TaskCardView, projection.TaskCardView, error) {
	if s.runtimeTasks == nil {
		return task, projection.TaskCardView{}, nil
	}
	canonicalTask, err := s.runtimeTasks.ResolveRuntimeTask(ctx, tenantID, missionID, task.TaskID)
	if err != nil {
		return projection.TaskCardView{}, projection.TaskCardView{}, err
	}
	hydrated, err := mergeRuntimeTaskContract(task, canonicalTask)
	if err != nil {
		return projection.TaskCardView{}, projection.TaskCardView{}, err
	}
	if model == nil {
		return projection.TaskCardView{}, projection.TaskCardView{}, ErrInvalidRequest
	}
	for index := range model.TaskBoard.Tasks {
		if model.TaskBoard.Tasks[index].TaskID == hydrated.TaskID {
			model.TaskBoard.Tasks[index] = hydrated
			return hydrated, canonicalTask, nil
		}
	}
	return projection.TaskCardView{}, projection.TaskCardView{}, ErrInvalidRequest
}

func findProjectionTask(model projection.ReadModel, taskID missioncore.TaskID) (projection.TaskCardView, bool) {
	for _, task := range model.TaskBoard.Tasks {
		if task.TaskID == taskID {
			return task, true
		}
	}
	return projection.TaskCardView{}, false
}

func actorFromViewer(viewer ViewerRef) missionevent.ActorRef {
	kind := missionevent.ActorKindUser
	switch viewer.ViewerKind {
	case ViewerKindAgent:
		kind = missionevent.ActorKindAgent
	case ViewerKindHuman:
		kind = missionevent.ActorKindHuman
	case ViewerKindSystem:
		kind = missionevent.ActorKindSystem
	}
	return missionevent.ActorRef{Kind: kind, ID: viewer.ViewerID}
}

func (s *CommandService) nowUTC() time.Time {
	if s != nil && s.clock != nil {
		return s.clock.Now().UTC()
	}
	return time.Now().UTC()
}
