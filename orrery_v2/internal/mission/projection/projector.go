package projection

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/platform/logging"
)

type Projector interface {
	Apply(ctx context.Context, view *ReadModel, event missionevent.Envelope) error
}

type DefaultProjector struct {
	logger logging.Logger
}

func NewProjector() *DefaultProjector {
	return &DefaultProjector{}
}

func NewProjectorWithLogger(logger logging.Logger) *DefaultProjector {
	return &DefaultProjector{logger: logger}
}

func (p *DefaultProjector) Apply(ctx context.Context, model *ReadModel, event missionevent.Envelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if model == nil {
		return fmt.Errorf("%w: read model is nil", ErrProjectionDependency)
	}
	model.ensureRuntimeState()
	if event.EventVersion != 1 {
		return fmt.Errorf("%w: %s version %d", ErrUnknownEventVersion, event.EventType, event.EventVersion)
	}
	if !missionevent.IsKnownEventType(event.EventType) {
		return fmt.Errorf("%w: %s", ErrUnknownEventType, event.EventType)
	}
	if model.Mission.MissionID.IsZero() {
		model.Mission.MissionID = event.MissionID
		model.Checkpoint.MissionID = event.MissionID
	}
	if event.MissionID != model.Mission.MissionID {
		return fmt.Errorf("%w: model=%s event=%s", ErrMissionMismatch, model.Mission.MissionID, event.MissionID)
	}
	if _, ok := model.seenEventIDs[event.EventID]; ok {
		return nil
	}
	if event.IdempotencyKey != "" {
		if _, ok := model.seenIdempotencyKeys[event.IdempotencyKey]; ok {
			return nil
		}
	}
	if event.StreamVersion != model.Checkpoint.Version+1 {
		return fmt.Errorf("%w: got version %d after checkpoint %d", ErrOutOfOrderEvent, event.StreamVersion, model.Checkpoint.Version)
	}
	if event.EventType == missionevent.EventMissionCreated && model.Checkpoint.Version > 0 {
		return fmt.Errorf("%w: MissionCreated cannot follow checkpoint %d", ErrInvalidEventSequence, model.Checkpoint.Version)
	}

	if err := applyEvent(model, event); err != nil {
		return err
	}
	p.logApplied(ctx, event)
	model.markApplied(event.EventID, event.IdempotencyKey)
	appendProgress(model, event)
	model.Checkpoint.Version = event.StreamVersion
	model.Checkpoint.UpdatedAt = event.OccurredAt
	model.UpdatedAt = event.OccurredAt
	return nil
}

func (p *DefaultProjector) logApplied(ctx context.Context, event missionevent.Envelope) {
	if p == nil || p.logger == nil {
		return
	}
	p.logger.Info(ctx, "mission projection event applied",
		logging.String("mission_id", event.MissionID.String()),
		logging.String("event_id", event.EventID),
		logging.String("event_type", string(event.EventType)),
		logging.Int("stream_version", event.StreamVersion),
		logging.String("correlation_id", event.CorrelationID),
	)
}

// ApplyRuntimeEventState applies the canonical runtime portion of an event
// without advancing projection bookkeeping. It is used to repair a runtime
// event whose durable stream position is already behind the read checkpoint.
func ApplyRuntimeEventState(model *ReadModel, event missionevent.Envelope) bool {
	if model == nil {
		return false
	}
	switch payload := event.Payload.(type) {
	case missionevent.RuntimeLifecyclePayload, missionevent.RuntimeFailurePayload:
	case *missionevent.RuntimeLifecyclePayload:
		if payload == nil {
			return false
		}
	case *missionevent.RuntimeFailurePayload:
		if payload == nil {
			return false
		}
	default:
		return false
	}
	return applyEvent(model, event) == nil
}

func applyEvent(model *ReadModel, event missionevent.Envelope) error {
	switch payload := event.Payload.(type) {
	case missionevent.MissionCreatedPayload:
		ownerID := strings.TrimSpace(payload.PersonalAssistantOriginOwnerID)
		subjectID := strings.TrimSpace(payload.PersonalAssistantOriginSubjectID)
		if (ownerID == "") != (subjectID == "") {
			return fmt.Errorf("%w: MissionCreated personal assistant origin must be complete", ErrInvalidEventSequence)
		}
		model.Mission.Title = payload.Title
		model.Mission.FinalGoal = payload.FinalGoal
		model.Mission.OriginKind = payload.OriginKind
		model.Mission.OriginID = payload.OriginID
		model.Mission.OriginTodoID = payload.OriginTodoID
		model.Mission.ExecutionAttempt = payload.ExecutionAttempt
		model.Mission.DiscussionMode = payload.DiscussionMode
		model.Mission.OriginDevelopmentProjectID = payload.OriginDevelopmentProjectID
		model.Mission.PersonalAssistantOriginOwnerID = ownerID
		model.Mission.PersonalAssistantOriginSubjectID = subjectID
		model.Mission.Status = MissionStatusActive
		model.Mission.CreatedAt = event.OccurredAt
	case *missionevent.MissionCreatedPayload:
		return applyEvent(model, withPayload(event, *payload))
	case missionevent.MissionGoalUpdatedPayload:
		model.Mission.FinalGoal = payload.FinalGoal
	case *missionevent.MissionGoalUpdatedPayload:
		return applyEvent(model, withPayload(event, *payload))
	case missionevent.MissionPausedPayload:
		model.Mission.Status = MissionStatusPaused
	case *missionevent.MissionPausedPayload:
		return applyEvent(model, withPayload(event, *payload))
	case missionevent.MissionClosedPayload:
		if event.EventType == missionevent.EventMissionCompleted {
			model.Mission.Status = MissionStatusCompleted
		} else {
			model.Mission.Status = MissionStatusCancelled
			for i := range model.TaskBoard.Tasks {
				switch model.TaskBoard.Tasks[i].Status {
				case TaskStatusApproved, TaskStatusRejected, TaskStatusCancelled:
					continue
				}
				model.TaskBoard.Tasks[i].Status = TaskStatusCancelled
				model.TaskBoard.Tasks[i].UpdatedAt = event.OccurredAt
			}
		}
	case *missionevent.MissionClosedPayload:
		return applyEvent(model, withPayload(event, *payload))
	case missionevent.StaffingRequestedPayload:
		model.Team.StaffingRequests = upsertStaffingRequest(model.Team.StaffingRequests, StaffingRequestView{RequestID: payload.RequestID, RoleName: payload.RoleName, Reason: payload.Reason, CreatedAt: event.OccurredAt})
	case *missionevent.StaffingRequestedPayload:
		return applyEvent(model, withPayload(event, *payload))
	case missionevent.AgentPayload:
		status := TeamMemberJoined
		if event.EventType == missionevent.EventAgentInvited {
			status = TeamMemberInvited
		}
		if event.EventType == missionevent.EventAgentReleased {
			status = TeamMemberReleased
		}
		model.Team.Members = upsertTeamMember(model.Team.Members, TeamMemberView{
			AgentID: payload.AgentID, AgentProfileID: agentcore.AgentProfileID(payload.AgentID), DisplayName: payload.DisplayName, RealName: payload.RealName,
			RoleName: payload.RoleName, Department: payload.Department, Position: payload.Position,
			Status: status, JoinedAt: event.OccurredAt,
		})
	case *missionevent.AgentPayload:
		return applyEvent(model, withPayload(event, *payload))
	case missionevent.MissionAssignmentPayload:
		model.Team.Assignments = upsertAssignment(model.Team.Assignments, AssignmentView{AssignmentID: payload.AssignmentID, AgentID: payload.AgentID, AgentProfileID: agentcore.AgentProfileID(payload.AgentID), TaskID: payload.TaskID, MissionRole: payload.MissionRole, ChangedAt: event.OccurredAt})
		model.Team.Members = setMemberMissionRole(model.Team.Members, payload.AgentID, payload.MissionRole)
		model.TaskBoard.Tasks = upsertTask(model.TaskBoard.Tasks, TaskCardView{TaskID: payload.TaskID, Assignee: payload.AgentID, Status: TaskStatusAssigned, UpdatedAt: event.OccurredAt})
	case *missionevent.MissionAssignmentPayload:
		return applyEvent(model, withPayload(event, *payload))
	case missionevent.StaffingFactPayload:
		applyStaffingFactPayload(model, event, payload)
	case *missionevent.StaffingFactPayload:
		return applyEvent(model, withPayload(event, *payload))
	case missionevent.TaskContractCreatedPayload:
		card := TaskCardView{TaskID: payload.Contract.ID, Goal: payload.Contract.Goal, SourceKind: payload.Contract.SourceKind, SourceID: payload.Contract.SourceID, SourceStepID: payload.Contract.SourceStepID, ExecutionPermissionScopes: append([]string(nil), payload.Contract.ExecutionPermissionScopes...), ExecutionAllowedToolIDs: append([]string(nil), payload.Contract.ExecutionAllowedToolIDs...), ExecutionEnvelope: agentcore.CloneExecutionEnvelope(payload.Contract.ExecutionEnvelope), WorkerPersonalGoalSummary: payload.Contract.WorkerPersonalGoal, Status: TaskStatusCreated, Priority: payload.Contract.Priority, DueAt: payload.Contract.DuePolicy.DueAt, UpdatedAt: event.OccurredAt}
		for _, deliverable := range payload.Contract.ExpectedDeliverables {
			card.ExpectedDeliverables = append(card.ExpectedDeliverables, ExpectedDeliverableView{ID: string(deliverable.ID), Title: deliverable.Title})
		}
		for _, requirement := range payload.Contract.EvidenceRequirements {
			card.RequiredEvidence = append(card.RequiredEvidence, RequiredEvidenceView{ID: string(requirement.ID), Description: requirement.Description, Kind: string(requirement.Kind), Required: requirement.Required})
		}
		for _, dep := range payload.Contract.Dependencies {
			model.TaskBoard.Dependencies = upsertDependency(model.TaskBoard.Dependencies, TaskDependencyView{TaskID: payload.Contract.ID, DependsOn: dep.TaskID})
		}
		model.TaskBoard.Tasks = upsertTask(model.TaskBoard.Tasks, card)
		refreshMissingEvidence(model)
	case *missionevent.TaskContractCreatedPayload:
		return applyEvent(model, withPayload(event, *payload))
	case missionevent.TaskAssignedPayload:
		model.TaskBoard.Tasks = updateTaskStatus(model.TaskBoard.Tasks, payload.TaskID, TaskStatusAssigned, event.OccurredAt, func(card *TaskCardView) {
			card.Assignee = payload.AgentID
			if payload.Goal != "" {
				card.Goal = payload.Goal
			}
		})
	case *missionevent.TaskAssignedPayload:
		return applyEvent(model, withPayload(event, *payload))
	case missionevent.TaskAcceptedPayload:
		model.TaskBoard.Tasks = updateTaskStatus(model.TaskBoard.Tasks, payload.TaskID, TaskStatusAccepted, event.OccurredAt, func(card *TaskCardView) {
			card.Assignee = payload.AcceptedBy
		})
	case *missionevent.TaskAcceptedPayload:
		return applyEvent(model, withPayload(event, *payload))
	case missionevent.TaskStatusPayload:
		status := statusForEvent(event.EventType)
		model.TaskBoard.Tasks = updateTaskStatus(model.TaskBoard.Tasks, payload.TaskID, status, event.OccurredAt, nil)
	case *missionevent.TaskStatusPayload:
		return applyEvent(model, withPayload(event, *payload))
	case missionevent.AgentResultSubmittedPayload:
		model.TaskBoard.Tasks = updateTaskStatus(model.TaskBoard.Tasks, payload.TaskID, TaskStatusSubmitted, event.OccurredAt, func(card *TaskCardView) {
			card.Assignee = payload.AgentID
			card.LatestResultRef = payload.ResultRef
		})
	case *missionevent.AgentResultSubmittedPayload:
		return applyEvent(model, withPayload(event, *payload))
	case missionevent.DeliverableSubmittedPayload:
		model.TaskBoard.Tasks = updateTaskStatus(model.TaskBoard.Tasks, payload.TaskID, TaskStatusSubmitted, event.OccurredAt, nil)
	case *missionevent.DeliverableSubmittedPayload:
		return applyEvent(model, withPayload(event, *payload))
	case missionevent.EvidenceSubmittedPayload:
		item := EvidenceItemView{
			EvidenceID: payload.EvidenceID, TaskID: payload.TaskID, AgentID: payload.AgentID,
			EvidenceRequirementID: payload.EvidenceRequirementID, Kind: payload.Kind,
			ArtifactRef: payload.ArtifactRef, ToolLogRef: payload.ToolLogRef, ExternalURL: payload.ExternalURL,
			VisibleToPermissionKeys: payload.VisibleToPermissionKeys, SubmittedAt: event.OccurredAt,
		}
		model.Evidence.EvidenceItems = upsertEvidence(model.Evidence.EvidenceItems, item)
		refreshMissingEvidence(model)
	case *missionevent.EvidenceSubmittedPayload:
		return applyEvent(model, withPayload(event, *payload))
	case missionevent.QAPayload:
		applyQAPayload(model, event, payload)
	case *missionevent.QAPayload:
		return applyEvent(model, withPayload(event, *payload))
	case missionevent.ReworkRequestedPayload:
		model.QA.ReworkRequests = upsertReworkRequest(model.QA.ReworkRequests, ReworkRequestView{TaskID: payload.TaskID, QARef: payload.QARef, Reason: payload.Reason, Message: payload.Message, Status: ReworkStatusOpen, CreatedAt: event.OccurredAt})
		model.TaskBoard.Tasks = updateTaskStatus(model.TaskBoard.Tasks, payload.TaskID, TaskStatusReworkRequested, event.OccurredAt, nil)
	case *missionevent.ReworkRequestedPayload:
		return applyEvent(model, withPayload(event, *payload))
	case missionevent.ReworkAssignedPayload:
		model.QA.ReworkRequests = assignReworkRequest(model.QA.ReworkRequests, payload.ReworkRef, payload.TaskID, event.OccurredAt)
		model.TaskBoard.Tasks = updateTaskStatus(model.TaskBoard.Tasks, payload.TaskID, TaskStatusAssigned, event.OccurredAt, nil)
	case *missionevent.ReworkAssignedPayload:
		return applyEvent(model, withPayload(event, *payload))
	case missionevent.ReworkResolvedPayload:
		model.QA.ReworkRequests = resolveReworkRequest(model.QA.ReworkRequests, payload.ReworkRef, event.OccurredAt)
		status := taskStatusAfterReworkResolved(taskStatusOrDefault(model.TaskBoard.Tasks, payload.TaskID))
		model.TaskBoard.Tasks = updateTaskStatus(model.TaskBoard.Tasks, payload.TaskID, status, event.OccurredAt, nil)
	case *missionevent.ReworkResolvedPayload:
		return applyEvent(model, withPayload(event, *payload))
	case missionevent.DiscussionGroupCreatedPayload:
		model.Discussion.DiscussionGroupID = payload.DiscussionGroupID
	case *missionevent.DiscussionGroupCreatedPayload:
		return applyEvent(model, withPayload(event, *payload))
	case missionevent.MessagePostedPayload:
		message := MessageView{MessageID: payload.MessageID, DiscussionGroupID: payload.DiscussionGroupID, AuthorID: payload.AuthorID, AuthorType: payload.AuthorType, Body: payload.Body, Mentions: payload.Mentions, ReplyToMessageID: payload.ReplyToMessageID, Visibility: payload.Visibility, PostedAt: event.OccurredAt}
		model.Discussion.Messages = upsertMessage(model.Discussion.Messages, message)
		for _, mention := range payload.Mentions {
			model.Discussion.PendingMentions = upsertMention(model.Discussion.PendingMentions, MentionView{MessageID: payload.MessageID, AgentID: mention, CreatedAt: event.OccurredAt})
		}
	case *missionevent.MessagePostedPayload:
		return applyEvent(model, withPayload(event, *payload))
	case missionevent.InboxItemPayload:
		applyInboxPayload(model, event, payload)
	case *missionevent.InboxItemPayload:
		return applyEvent(model, withPayload(event, *payload))
	case missionevent.DecisionRecordedPayload:
		decision := DecisionView{DecisionID: payload.DecisionID, Summary: payload.Summary, SourceRefs: payload.SourceRefs, RecordedAt: event.OccurredAt}
		model.LatestDecisions = upsertDecision(model.LatestDecisions, decision)
		model.Discussion.Decisions = upsertDecision(model.Discussion.Decisions, decision)
	case *missionevent.DecisionRecordedPayload:
		return applyEvent(model, withPayload(event, *payload))
	case missionevent.ApprovalDecidedPayload:
		model.ApprovalRequests = decideApprovalRequest(model.ApprovalRequests, payload, event.OccurredAt)
	case *missionevent.ApprovalDecidedPayload:
		return applyEvent(model, withPayload(event, *payload))
	case missionevent.BlockerRaisedPayload:
		blocker := BlockerView{BlockerID: payload.BlockerID, TaskID: payload.TaskID, RaisedBy: payload.RaisedBy, Summary: payload.Summary, RaisedAt: event.OccurredAt}
		model.Blockers = upsertBlocker(model.Blockers, blocker)
		model.Discussion.Blockers = upsertBlocker(model.Discussion.Blockers, blocker)
		model.TaskBoard.Tasks = updateTaskStatus(model.TaskBoard.Tasks, payload.TaskID, taskStatusOrDefault(model.TaskBoard.Tasks, payload.TaskID), event.OccurredAt, func(card *TaskCardView) {
			card.BlockerRefs = appendUnique(card.BlockerRefs, payload.BlockerID)
		})
	case *missionevent.BlockerRaisedPayload:
		return applyEvent(model, withPayload(event, *payload))
	case missionevent.RuntimeLifecyclePayload:
		model.RuntimeSessions = upsertRuntimeSession(model.RuntimeSessions, runtimeSessionFromLifecycle(event, payload))
		if event.EventType == missionevent.EventAgentRunStarted && !taskProjectionStatusIsApproved(model.TaskBoard.Tasks, payload.TaskID) {
			clearRuntimeFailureBlockers(model, payload.TaskID)
			model.TaskBoard.Tasks = updateTaskStatus(model.TaskBoard.Tasks, payload.TaskID, TaskStatusStarted, event.OccurredAt, nil)
		}
	case *missionevent.RuntimeLifecyclePayload:
		return applyEvent(model, withPayload(event, *payload))
	case missionevent.RuntimeFailurePayload:
		model.RuntimeSessions = upsertRuntimeSession(model.RuntimeSessions, runtimeSessionFromFailure(event, payload))
		if taskProjectionStatusIsApproved(model.TaskBoard.Tasks, payload.TaskID) {
			break
		}
		status := statusForRuntimeEvent(event.EventType)
		if blocker, ok := runtimeFailureBlocker(event, payload); ok {
			model.Blockers = upsertBlocker(model.Blockers, blocker)
			model.Discussion.Blockers = upsertBlocker(model.Discussion.Blockers, blocker)
			model.TaskBoard.Tasks = updateTaskStatus(model.TaskBoard.Tasks, payload.TaskID, status, event.OccurredAt, func(card *TaskCardView) {
				card.BlockerRefs = appendUnique(card.BlockerRefs, blocker.BlockerID)
			})
		} else {
			model.TaskBoard.Tasks = updateTaskStatus(model.TaskBoard.Tasks, payload.TaskID, status, event.OccurredAt, nil)
		}
		if event.EventType == missionevent.EventAgentRunBlocked {
			model.ApprovalRequests = upsertApprovalRequest(model.ApprovalRequests, approvalRequestFromRuntimeFailure(event, payload))
		}
	case *missionevent.RuntimeFailurePayload:
		return applyEvent(model, withPayload(event, *payload))
	default:
		return fmt.Errorf("%w: %s", ErrUnknownEventType, event.EventType)
	}
	return nil
}

func applyStaffingFactPayload(model *ReadModel, event missionevent.Envelope, payload missionevent.StaffingFactPayload) {
	agentID, profileID, instanceID, ok := stableStaffingFactIdentity(payload)
	agentKind := agentcore.AgentKind(payload.AgentKind)
	switch event.EventType {
	case missionevent.EventStaffingDecisionRecorded:
		if payload.RequestID != "" {
			model.Team.StaffingRequests = upsertStaffingRequest(model.Team.StaffingRequests, StaffingRequestView{RequestID: payload.RequestID, RoleName: payload.MissionRole, Reason: payload.Reason, CreatedAt: event.OccurredAt})
		}
	case missionevent.EventAgentJoined, missionevent.EventAgentInvited, missionevent.EventAgentReleased:
		if !ok {
			return
		}
		status := TeamMemberJoined
		if event.EventType == missionevent.EventAgentInvited {
			status = TeamMemberInvited
		}
		if event.EventType == missionevent.EventAgentReleased {
			status = TeamMemberReleased
		}
		model.Team.Members = upsertTeamMember(model.Team.Members, TeamMemberView{
			MemberID:        payload.MemberID,
			AgentID:         agentID,
			AgentProfileID:  profileID,
			AgentInstanceID: instanceID,
			AgentKind:       agentKind,
			MissionRole:     payload.MissionRole,
			Status:          status,
			JoinedAt:        event.OccurredAt,
		})
	case missionevent.EventMissionAssignmentCreated, missionevent.EventMissionAssignmentChanged:
		if payload.AssignmentID == "" || !ok {
			return
		}
		model.Team.Assignments = upsertAssignment(model.Team.Assignments, AssignmentView{AssignmentID: payload.AssignmentID, MemberID: payload.MemberID, AgentID: agentID, AgentProfileID: profileID, AgentInstanceID: instanceID, AgentKind: agentKind, MissionRole: payload.MissionRole, ChangedAt: event.OccurredAt})
		model.Team.Members = setMemberMissionRole(model.Team.Members, agentID, payload.MissionRole)
	case missionevent.EventMissionAssignmentReleased:
		if !ok {
			return
		}
		model.Team.Members = upsertTeamMember(model.Team.Members, TeamMemberView{MemberID: payload.MemberID, AgentID: agentID, AgentProfileID: profileID, AgentInstanceID: instanceID, AgentKind: agentKind, MissionRole: payload.MissionRole, Status: TeamMemberReleased, JoinedAt: event.OccurredAt})
	}
}

func stableStaffingFactIdentity(payload missionevent.StaffingFactPayload) (agentcore.AgentID, agentcore.AgentProfileID, agentcore.AgentInstanceID, bool) {
	profileID := agentcore.AgentProfileID(strings.TrimSpace(payload.AgentProfileID))
	if profileID.IsZero() {
		return "", "", "", false
	}
	instanceID := agentcore.AgentInstanceID(strings.TrimSpace(payload.AgentInstanceID))
	if instanceID.IsZero() {
		instanceID = agentcore.AgentInstanceID(profileID)
	}
	if instanceID != agentcore.AgentInstanceID(profileID) {
		return "", "", "", false
	}
	return agentcore.AgentID(profileID), profileID, agentcore.AgentInstanceID(profileID), true
}

func withPayload(event missionevent.Envelope, payload missionevent.Payload) missionevent.Envelope {
	event.Payload = payload
	return event
}

func applyQAPayload(model *ReadModel, event missionevent.Envelope, payload missionevent.QAPayload) {
	switch event.EventType {
	case missionevent.EventQARequested:
		model.QA.Checks = upsertQACheck(model.QA.Checks, QACheckView{QARef: payload.QARef, TaskID: payload.TaskID, Status: QAStatusRequested, Message: payload.Message, CreatedAt: event.OccurredAt})
		model.TaskBoard.Tasks = updateTaskQAStatus(model.TaskBoard.Tasks, payload.TaskID, QAStatusRequested, event.OccurredAt)
	case missionevent.EventQAApproved:
		model.QA.Approvals = append(model.QA.Approvals, QADecisionView{QARef: payload.QARef, TaskID: payload.TaskID, Reason: payload.Reason, Message: payload.Message, DecidedAt: event.OccurredAt})
		model.QA.Checks = resolveRequestedQACheck(model.QA.Checks, payload.QARef, payload.TaskID, QAStatusApproved, payload.Message)
		model.TaskBoard.Tasks = updateTaskStatus(updateTaskQAStatus(model.TaskBoard.Tasks, payload.TaskID, QAStatusApproved, event.OccurredAt), payload.TaskID, TaskStatusApproved, event.OccurredAt, nil)
	case missionevent.EventQASkipped:
		// A skipped QA path is an automatic approval, not evidence of a requested QA check.
		model.QA.Approvals = append(model.QA.Approvals, QADecisionView{QARef: payload.QARef, TaskID: payload.TaskID, Reason: payload.Reason, Message: payload.Message, DecidedAt: event.OccurredAt})
		model.TaskBoard.Tasks = updateTaskStatus(updateTaskQAStatus(model.TaskBoard.Tasks, payload.TaskID, QAStatusApproved, event.OccurredAt), payload.TaskID, TaskStatusApproved, event.OccurredAt, nil)
	case missionevent.EventQARejected:
		model.QA.Rejections = append(model.QA.Rejections, QADecisionView{QARef: payload.QARef, TaskID: payload.TaskID, Reason: payload.Reason, Message: payload.Message, DecidedAt: event.OccurredAt})
		model.QA.Checks = resolveRequestedQACheck(model.QA.Checks, payload.QARef, payload.TaskID, QAStatusRejected, payload.Message)
		model.TaskBoard.Tasks = updateTaskStatus(updateTaskQAStatus(model.TaskBoard.Tasks, payload.TaskID, QAStatusRejected, event.OccurredAt), payload.TaskID, TaskStatusRejected, event.OccurredAt, nil)
	}
}

func applyInboxPayload(model *ReadModel, event missionevent.Envelope, payload missionevent.InboxItemPayload) {
	status := "open"
	closedAt := timeZero()
	if event.EventType == missionevent.EventInboxItemClosed {
		status = "closed"
		closedAt = event.OccurredAt
		model.Discussion.PendingMentions = removePendingMention(model.Discussion.PendingMentions, payload.MessageID, payload.OwnerID)
	}
	model.InboxSummary.Items = upsertInboxItem(model.InboxSummary.Items, InboxItemView{InboxItemID: payload.InboxItemID, OwnerID: payload.OwnerID, MessageID: payload.MessageID, Reason: payload.Reason, Status: status, CreatedAt: event.OccurredAt, ClosedAt: closedAt})
	open := 0
	for _, item := range model.InboxSummary.Items {
		if item.Status == "open" {
			open++
		}
	}
	model.InboxSummary.OpenCount = open
}

func approvalRequestFromRuntimeFailure(event missionevent.Envelope, payload missionevent.RuntimeFailurePayload) ApprovalRequestView {
	metadata := cloneProjectionMetadata(payload.Metadata)
	requestID := firstNonBlank(metadata["approval_request_id"], payload.RunID.String(), event.EventID)
	source := firstNonBlank(metadata["approval_source"], metadata["approval_provider"])
	toolName := metadata["approval_tool_name"]
	approvalType := firstNonBlank(metadata["approval_type"], "permission_request")
	sourceID := firstNonBlank(joinNonBlank(":", source, toolName), requestID)
	title := "Provider approval required"
	if toolName != "" {
		title = "Approve " + toolName
	}
	summary := firstNonBlank(payload.Message, "Agent run is waiting for user approval")
	return ApprovalRequestView{
		RequestID:    requestID,
		ApprovalType: approvalType,
		Status:       "pending",
		Priority:     "normal",
		MissionID:    event.MissionID,
		TaskID:       payload.TaskID,
		SourceType:   "provider_tool",
		SourceID:     sourceID,
		Title:        title,
		Summary:      summary,
		RequestedBy:  payload.AgentID,
		Metadata:     metadata,
		CreatedAt:    event.OccurredAt,
	}
}

func upsertApprovalRequest(requests []ApprovalRequestView, next ApprovalRequestView) []ApprovalRequestView {
	if next.RequestID == "" || next.ApprovalType == "" {
		return requests
	}
	for i := range requests {
		if requests[i].RequestID == next.RequestID && requests[i].ApprovalType == next.ApprovalType {
			if next.Status != "" {
				requests[i].Status = next.Status
			}
			if next.Priority != "" {
				requests[i].Priority = next.Priority
			}
			if !next.MissionID.IsZero() {
				requests[i].MissionID = next.MissionID
			}
			if !next.TaskID.IsZero() {
				requests[i].TaskID = next.TaskID
			}
			if next.SourceType != "" {
				requests[i].SourceType = next.SourceType
			}
			if next.SourceID != "" {
				requests[i].SourceID = next.SourceID
			}
			if next.Title != "" {
				requests[i].Title = next.Title
			}
			if next.Summary != "" {
				requests[i].Summary = next.Summary
			}
			if !next.RequestedBy.IsZero() {
				requests[i].RequestedBy = next.RequestedBy
			}
			if !next.AssignedTo.IsZero() {
				requests[i].AssignedTo = next.AssignedTo
			}
			if len(next.EvidenceRefs) > 0 {
				requests[i].EvidenceRefs = append([]string(nil), next.EvidenceRefs...)
			}
			if len(next.Metadata) > 0 {
				requests[i].Metadata = cloneProjectionMetadata(next.Metadata)
			}
			if !next.CreatedAt.IsZero() {
				requests[i].CreatedAt = next.CreatedAt
			}
			if !next.DecidedAt.IsZero() {
				requests[i].DecidedAt = next.DecidedAt
			}
			return requests
		}
	}
	return append(requests, next)
}

func decideApprovalRequest(requests []ApprovalRequestView, decision missionevent.ApprovalDecidedPayload, decidedAt time.Time) []ApprovalRequestView {
	for i := range requests {
		if requests[i].RequestID != decision.ApprovalRef || requests[i].ApprovalType != decision.ApprovalType {
			continue
		}
		status := strings.TrimSpace(decision.Decision)
		if status == "" {
			status = "closed"
		}
		requests[i].Status = status
		requests[i].DecidedAt = decidedAt
		if len(decision.Metadata) > 0 {
			requests[i].Metadata = mergeProjectionMetadata(requests[i].Metadata, decision.Metadata)
		}
		return requests
	}
	return requests
}

func mergeProjectionMetadata(existing, next map[string]string) map[string]string {
	merged := cloneProjectionMetadata(existing)
	if merged == nil {
		merged = map[string]string{}
	}
	for key, value := range cloneProjectionMetadata(next) {
		merged[key] = value
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func cloneProjectionMetadata(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for key, value := range src {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			out[key] = strings.TrimSpace(value)
		}
	}
	return out
}

func joinNonBlank(sep string, values ...string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return strings.Join(parts, sep)
}

func removePendingMention(mentions []MentionView, messageID string, ownerID agentcore.AgentID) []MentionView {
	out := mentions[:0]
	for _, mention := range mentions {
		if mention.MessageID == messageID && mention.AgentID == ownerID {
			continue
		}
		out = append(out, mention)
	}
	return out
}

func appendProgress(model *ReadModel, event missionevent.Envelope) {
	node := ProgressNodeView{ID: event.EventID, EventID: event.EventID, EventType: string(event.EventType), Label: string(event.EventType), Status: progressStatus(event.EventType), OccurredAt: event.OccurredAt}
	if refType, refID := progressRef(event); refID != "" {
		node.RefType = refType
		node.RefID = refID
	}
	node.Details = progressDetails(event)
	if len(model.ProgressGraph.Nodes) > 0 {
		prev := model.ProgressGraph.Nodes[len(model.ProgressGraph.Nodes)-1]
		model.ProgressGraph.Edges = append(model.ProgressGraph.Edges, ProgressEdgeView{From: prev.ID, To: node.ID, Kind: "observed_after"})
	}
	model.ProgressGraph.Nodes = append(model.ProgressGraph.Nodes, node)
	model.ProgressGraph.CurrentFocus = []string{node.ID}
}

func progressRef(event missionevent.Envelope) (string, string) {
	switch payload := event.Payload.(type) {
	case missionevent.TaskAcceptedPayload:
		return "task", payload.TaskID.String()
	case *missionevent.TaskAcceptedPayload:
		return "task", payload.TaskID.String()
	case missionevent.AgentResultSubmittedPayload:
		return "result", payload.ResultRef
	case *missionevent.AgentResultSubmittedPayload:
		return "result", payload.ResultRef
	case missionevent.EvidenceSubmittedPayload:
		return "evidence", string(payload.EvidenceID)
	case *missionevent.EvidenceSubmittedPayload:
		return "evidence", string(payload.EvidenceID)
	case missionevent.QAPayload:
		return "qa", payload.QARef
	case *missionevent.QAPayload:
		return "qa", payload.QARef
	case missionevent.ReworkRequestedPayload:
		return "task", payload.TaskID.String()
	case *missionevent.ReworkRequestedPayload:
		return "task", payload.TaskID.String()
	case missionevent.ReworkAssignedPayload:
		return "task", payload.TaskID.String()
	case *missionevent.ReworkAssignedPayload:
		return "task", payload.TaskID.String()
	case missionevent.ReworkResolvedPayload:
		return "task", payload.TaskID.String()
	case *missionevent.ReworkResolvedPayload:
		return "task", payload.TaskID.String()
	case missionevent.BlockerRaisedPayload:
		return "blocker", payload.BlockerID
	case *missionevent.BlockerRaisedPayload:
		return "blocker", payload.BlockerID
	case missionevent.MissionPausedPayload:
		return "todo", payload.TodoID
	case *missionevent.MissionPausedPayload:
		return "todo", payload.TodoID
	case missionevent.RuntimeLifecyclePayload:
		return "runtime_run", payload.RunID.String()
	case *missionevent.RuntimeLifecyclePayload:
		return "runtime_run", payload.RunID.String()
	case missionevent.RuntimeFailurePayload:
		return "runtime_run", payload.RunID.String()
	case *missionevent.RuntimeFailurePayload:
		return "runtime_run", payload.RunID.String()
	default:
		return "", ""
	}
}

func progressDetails(event missionevent.Envelope) string {
	switch payload := event.Payload.(type) {
	case missionevent.AgentResultSubmittedPayload:
		return strings.TrimSpace(payload.Summary)
	case *missionevent.AgentResultSubmittedPayload:
		return strings.TrimSpace(payload.Summary)
	case missionevent.SummarySubmittedPayload:
		return strings.TrimSpace(payload.Summary)
	case *missionevent.SummarySubmittedPayload:
		return strings.TrimSpace(payload.Summary)
	case missionevent.QAPayload:
		if event.EventType == missionevent.EventAcceptanceReportCreated {
			return strings.TrimSpace(payload.Reason)
		}
		return ""
	case *missionevent.QAPayload:
		if event.EventType == missionevent.EventAcceptanceReportCreated {
			return strings.TrimSpace(payload.Reason)
		}
		return ""
	case missionevent.RuntimeFailurePayload:
		return strings.TrimSpace(payload.Message)
	case *missionevent.RuntimeFailurePayload:
		return strings.TrimSpace(payload.Message)
	case missionevent.RuntimeLifecyclePayload:
		return strings.TrimSpace(payload.Message)
	case *missionevent.RuntimeLifecyclePayload:
		return strings.TrimSpace(payload.Message)
	case missionevent.MissionPausedPayload:
		return strings.TrimSpace(payload.Reason)
	case *missionevent.MissionPausedPayload:
		return strings.TrimSpace(payload.Reason)
	case missionevent.MissionClosedPayload:
		return strings.TrimSpace(payload.Reason)
	case *missionevent.MissionClosedPayload:
		return strings.TrimSpace(payload.Reason)
	default:
		return ""
	}
}

func progressStatus(eventType missionevent.EventType) string {
	switch eventType {
	case missionevent.EventQARejected, missionevent.EventTaskRejected, missionevent.EventAgentRunFailed, missionevent.EventAgentRunTimedOut:
		return "failed"
	case missionevent.EventTaskBlocked, missionevent.EventBlockerRaised, missionevent.EventReworkRequested, missionevent.EventMissionPaused:
		return "blocked"
	case missionevent.EventTaskCancelled, missionevent.EventAgentRunCancelled, missionevent.EventMissionCancelled:
		return "cancelled"
	default:
		return "succeeded"
	}
}

func statusForEvent(eventType missionevent.EventType) TaskStatus {
	switch eventType {
	case missionevent.EventTaskStarted:
		return TaskStatusStarted
	case missionevent.EventTaskBlocked:
		return TaskStatusBlocked
	case missionevent.EventTaskSubmitted:
		return TaskStatusSubmitted
	case missionevent.EventTaskApproved:
		return TaskStatusApproved
	case missionevent.EventTaskRejected:
		return TaskStatusRejected
	case missionevent.EventTaskCancelled:
		return TaskStatusCancelled
	default:
		return TaskStatusCreated
	}
}

func statusForRuntimeEvent(eventType missionevent.EventType) TaskStatus {
	switch eventType {
	case missionevent.EventAgentRunCancelled:
		return TaskStatusCancelled
	default:
		return TaskStatusBlocked
	}
}

func taskProjectionStatusIsApproved(tasks []TaskCardView, taskID missioncore.TaskID) bool {
	return taskStatusOrDefault(tasks, taskID) == TaskStatusApproved
}

const runtimeFailureBlockerPrefix = "runtime-failure:"

func runtimeFailureBlocker(event missionevent.Envelope, payload missionevent.RuntimeFailurePayload) (BlockerView, bool) {
	if event.EventType == missionevent.EventAgentRunCancelled || payload.TaskID.IsZero() {
		return BlockerView{}, false
	}
	identity := strings.TrimSpace(payload.RunID.String())
	if identity == "" {
		identity = strings.TrimSpace(event.EventID)
	}
	if identity == "" {
		return BlockerView{}, false
	}
	return BlockerView{
		BlockerID: runtimeFailureBlockerPrefix + identity,
		TaskID:    payload.TaskID,
		RaisedBy:  payload.AgentID,
		Summary:   firstNonBlank(payload.Message, payload.Code, "Agent runtime failed"),
		RaisedAt:  event.OccurredAt,
	}, true
}

func clearRuntimeFailureBlockers(model *ReadModel, taskID missioncore.TaskID) {
	if model == nil || taskID.IsZero() {
		return
	}
	for index := range model.TaskBoard.Tasks {
		if model.TaskBoard.Tasks[index].TaskID != taskID {
			continue
		}
		refs := make([]string, 0, len(model.TaskBoard.Tasks[index].BlockerRefs))
		for _, ref := range model.TaskBoard.Tasks[index].BlockerRefs {
			if !strings.HasPrefix(ref, runtimeFailureBlockerPrefix) {
				refs = append(refs, ref)
			}
		}
		model.TaskBoard.Tasks[index].BlockerRefs = refs
		break
	}
	model.Blockers = withoutRuntimeFailureBlockers(model.Blockers, taskID)
	model.Discussion.Blockers = withoutRuntimeFailureBlockers(model.Discussion.Blockers, taskID)
}

func withoutRuntimeFailureBlockers(blockers []BlockerView, taskID missioncore.TaskID) []BlockerView {
	out := make([]BlockerView, 0, len(blockers))
	for _, blocker := range blockers {
		if blocker.TaskID == taskID && strings.HasPrefix(blocker.BlockerID, runtimeFailureBlockerPrefix) {
			continue
		}
		out = append(out, blocker)
	}
	return out
}

func timeZero() time.Time {
	return time.Time{}
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func updateTaskStatus(tasks []TaskCardView, taskID missioncore.TaskID, status TaskStatus, at time.Time, mutate func(*TaskCardView)) []TaskCardView {
	for i := range tasks {
		if tasks[i].TaskID == taskID {
			tasks[i].Status = status
			tasks[i].UpdatedAt = at
			if mutate != nil {
				mutate(&tasks[i])
			}
			return tasks
		}
	}
	card := TaskCardView{TaskID: taskID, Status: status, UpdatedAt: at}
	if mutate != nil {
		mutate(&card)
	}
	return append(tasks, card)
}

func updateTaskQAStatus(tasks []TaskCardView, taskID missioncore.TaskID, status QAStatus, at time.Time) []TaskCardView {
	return updateTaskStatus(tasks, taskID, taskStatusOrDefault(tasks, taskID), at, func(card *TaskCardView) {
		card.QAStatus = status
	})
}

func taskStatusOrDefault(tasks []TaskCardView, taskID missioncore.TaskID) TaskStatus {
	for _, task := range tasks {
		if task.TaskID == taskID && task.Status != "" {
			return task.Status
		}
	}
	return TaskStatusCreated
}

func taskStatusAfterReworkResolved(current TaskStatus) TaskStatus {
	if current == "" || current == TaskStatusCreated || current == TaskStatusReworkRequested {
		return TaskStatusAssigned
	}
	return current
}

func upsertTask(tasks []TaskCardView, next TaskCardView) []TaskCardView {
	for i := range tasks {
		if tasks[i].TaskID == next.TaskID {
			mergeTask(&tasks[i], next)
			return tasks
		}
	}
	return append(tasks, next)
}

func mergeTask(existing *TaskCardView, next TaskCardView) {
	if next.Assignee != "" {
		existing.Assignee = next.Assignee
	}
	if next.Goal != "" {
		existing.Goal = next.Goal
	}
	if next.SourceKind != "" {
		existing.SourceKind = next.SourceKind
	}
	if next.SourceID != "" {
		existing.SourceID = next.SourceID
	}
	if next.SourceStepID != "" {
		existing.SourceStepID = next.SourceStepID
	}
	if next.ExecutionPermissionScopes != nil {
		existing.ExecutionPermissionScopes = append([]string(nil), next.ExecutionPermissionScopes...)
	}
	if next.ExecutionAllowedToolIDs != nil {
		existing.ExecutionAllowedToolIDs = append([]string(nil), next.ExecutionAllowedToolIDs...)
	}
	if next.ExecutionEnvelope != nil {
		existing.ExecutionEnvelope = agentcore.CloneExecutionEnvelope(next.ExecutionEnvelope)
	}
	if next.WorkerPersonalGoalSummary != "" {
		existing.WorkerPersonalGoalSummary = next.WorkerPersonalGoalSummary
	}
	if next.Status != "" {
		existing.Status = next.Status
	}
	if next.Priority != "" {
		existing.Priority = next.Priority
	}
	if !next.DueAt.IsZero() {
		existing.DueAt = next.DueAt
	}
	if len(next.AcceptanceCriteria) > 0 {
		existing.AcceptanceCriteria = next.AcceptanceCriteria
	}
	if len(next.ExpectedDeliverables) > 0 {
		existing.ExpectedDeliverables = next.ExpectedDeliverables
	}
	if len(next.RequiredEvidence) > 0 {
		existing.RequiredEvidence = next.RequiredEvidence
	}
	if next.LatestResultRef != "" {
		existing.LatestResultRef = next.LatestResultRef
	}
	if next.QAStatus != "" {
		existing.QAStatus = next.QAStatus
	}
	for _, ref := range next.BlockerRefs {
		existing.BlockerRefs = appendUnique(existing.BlockerRefs, ref)
	}
	if !next.UpdatedAt.IsZero() {
		existing.UpdatedAt = next.UpdatedAt
	}
}

func upsertTeamMember(members []TeamMemberView, next TeamMemberView) []TeamMemberView {
	for i := range members {
		if members[i].AgentID == next.AgentID {
			if next.MemberID != "" {
				members[i].MemberID = next.MemberID
			}
			if next.AgentProfileID != "" {
				members[i].AgentProfileID = next.AgentProfileID
			}
			if next.AgentInstanceID != "" {
				members[i].AgentInstanceID = next.AgentInstanceID
			}
			if next.AgentKind != "" {
				members[i].AgentKind = next.AgentKind
			}
			if next.DisplayName != "" {
				members[i].DisplayName = next.DisplayName
			}
			if next.RealName != "" {
				members[i].RealName = next.RealName
			}
			if next.RoleName != "" {
				members[i].RoleName = next.RoleName
			}
			if next.Department != "" {
				members[i].Department = next.Department
			}
			if next.Position != "" {
				members[i].Position = next.Position
			}
			if next.MissionRole != "" {
				members[i].MissionRole = next.MissionRole
			}
			members[i].Status = next.Status
			members[i].JoinedAt = next.JoinedAt
			return members
		}
	}
	return append(members, next)
}

func setMemberMissionRole(members []TeamMemberView, agentID agentcore.AgentID, role string) []TeamMemberView {
	for i := range members {
		if members[i].AgentID == agentID {
			members[i].MissionRole = role
		}
	}
	return members
}

func upsertAssignment(assignments []AssignmentView, next AssignmentView) []AssignmentView {
	for i := range assignments {
		if assignments[i].AssignmentID == next.AssignmentID {
			if next.MemberID == "" {
				next.MemberID = assignments[i].MemberID
			}
			assignments[i] = next
			return assignments
		}
	}
	return append(assignments, next)
}

func upsertStaffingRequest(requests []StaffingRequestView, next StaffingRequestView) []StaffingRequestView {
	for i := range requests {
		if requests[i].RequestID == next.RequestID {
			requests[i] = next
			return requests
		}
	}
	return append(requests, next)
}

func upsertDependency(deps []TaskDependencyView, next TaskDependencyView) []TaskDependencyView {
	for _, dep := range deps {
		if dep == next {
			return deps
		}
	}
	return append(deps, next)
}

func upsertEvidence(items []EvidenceItemView, next EvidenceItemView) []EvidenceItemView {
	for i := range items {
		if items[i].EvidenceID == next.EvidenceID {
			items[i] = next
			return items
		}
	}
	return append(items, next)
}

func upsertQACheck(checks []QACheckView, next QACheckView) []QACheckView {
	for i := range checks {
		if checks[i].QARef == next.QARef && checks[i].TaskID == next.TaskID {
			checks[i] = next
			return checks
		}
	}
	return append(checks, next)
}

func resolveRequestedQACheck(checks []QACheckView, qaRef string, taskID missioncore.TaskID, status QAStatus, message string) []QACheckView {
	for i := range checks {
		if checks[i].QARef != qaRef || checks[i].TaskID != taskID || checks[i].Status != QAStatusRequested {
			continue
		}
		checks[i].Status = status
		if strings.TrimSpace(message) != "" {
			checks[i].Message = message
		}
		return checks
	}
	return checks
}

func upsertReworkRequest(requests []ReworkRequestView, next ReworkRequestView) []ReworkRequestView {
	for i := range requests {
		if requests[i].QARef == next.QARef && requests[i].TaskID == next.TaskID {
			requests[i] = next
			return requests
		}
	}
	return append(requests, next)
}

func assignReworkRequest(requests []ReworkRequestView, reworkRef string, taskID missioncore.TaskID, at time.Time) []ReworkRequestView {
	for i := range requests {
		if requests[i].QARef == reworkRef {
			requests[i].Status = ReworkStatusAssigned
			if requests[i].TaskID == "" {
				requests[i].TaskID = taskID
			}
			return requests
		}
	}
	return append(requests, ReworkRequestView{TaskID: taskID, QARef: reworkRef, Status: ReworkStatusAssigned, CreatedAt: at})
}

func resolveReworkRequest(requests []ReworkRequestView, reworkRef string, at time.Time) []ReworkRequestView {
	for i := range requests {
		if requests[i].QARef == reworkRef {
			requests[i].Status = ReworkStatusResolved
			requests[i].ResolvedAt = at
			return requests
		}
	}
	return append(requests, ReworkRequestView{QARef: reworkRef, Status: ReworkStatusResolved, ResolvedAt: at})
}

func upsertMessage(messages []MessageView, next MessageView) []MessageView {
	for i := range messages {
		if messages[i].MessageID == next.MessageID {
			messages[i] = next
			return messages
		}
	}
	return append(messages, next)
}

func upsertMention(mentions []MentionView, next MentionView) []MentionView {
	for _, mention := range mentions {
		if mention.MessageID == next.MessageID && mention.AgentID == next.AgentID {
			return mentions
		}
	}
	return append(mentions, next)
}

func upsertInboxItem(items []InboxItemView, next InboxItemView) []InboxItemView {
	for i := range items {
		if items[i].InboxItemID == next.InboxItemID {
			if next.Status == "closed" {
				items[i].Status = next.Status
				items[i].ClosedAt = next.ClosedAt
				return items
			}
			items[i] = next
			return items
		}
	}
	return append(items, next)
}

func upsertDecision(decisions []DecisionView, next DecisionView) []DecisionView {
	for i := range decisions {
		if decisions[i].DecisionID == next.DecisionID {
			decisions[i] = next
			return decisions
		}
	}
	return append(decisions, next)
}

func upsertBlocker(blockers []BlockerView, next BlockerView) []BlockerView {
	for i := range blockers {
		if blockers[i].BlockerID == next.BlockerID {
			blockers[i] = next
			return blockers
		}
	}
	return append(blockers, next)
}
