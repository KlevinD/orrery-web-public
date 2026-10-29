package main

import (
	"context"
	"errors"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/authorization"
	discussionsession "orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/identity"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	missionprojection "orrery_v2/internal/mission/projection"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/graphprotocol"
	"orrery_v2/internal/platform/uow"
	wstransport "orrery_v2/internal/transport/websocket"
)

func (p productionAPI) ListMissions(ctx context.Context, viewer missioncontrol.ViewerRef, query missioncontrol.ListMissionsQuery) (missioncontrol.MissionListDTO, error) {
	return p.missionQuery.ListMissions(ctx, viewer, query)
}

func (p productionAPI) GetMissionControlView(ctx context.Context, viewer missioncontrol.ViewerRef, missionID missioncore.MissionID) (missioncontrol.MissionControlViewDTO, error) {
	view, err := p.missionQuery.GetMissionControlView(ctx, viewer, missionID)
	if err != nil {
		return missioncontrol.MissionControlViewDTO{}, err
	}
	if view.Mission.OriginTodoID != "" {
		return p.filterTodoMissionDiscussion(ctx, viewer, view)
	}
	if view.Discussion.ConversationID == "" {
		origin, err := p.missionPersonalAssistantOrigin(ctx, viewer.Tenant(), missionID)
		if err != nil {
			return missioncontrol.MissionControlViewDTO{}, err
		}
		conversationID, err := p.ensureMissionDiscussionSession(ctx, viewer, view.Mission, origin)
		if err != nil {
			return missioncontrol.MissionControlViewDTO{}, err
		}
		view.Discussion.ConversationID = conversationID
	}
	return view, nil
}

func (p productionAPI) GetMissionOpsView(ctx context.Context, viewer missioncontrol.ViewerRef, missionID missioncore.MissionID) (missioncontrol.MissionOpsViewDTO, error) {
	view, err := p.missionQuery.GetMissionOpsView(ctx, viewer, missionID)
	if err != nil {
		return missioncontrol.MissionOpsViewDTO{}, err
	}
	control, err := p.missionQuery.GetMissionControlView(ctx, viewer, missionID)
	if err != nil {
		return missioncontrol.MissionOpsViewDTO{}, err
	}
	if strings.TrimSpace(control.Mission.OriginTodoID) == "" {
		return view, nil
	}
	allowed, err := p.canViewTodoMissionDiscussion(ctx, viewer, control.Discussion.ConversationID)
	if err != nil {
		return missioncontrol.MissionOpsViewDTO{}, err
	}
	if !allowed {
		return restrictTodoMissionOps(view), nil
	}
	return view, nil
}

func (p productionAPI) CreateMission(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CreateMissionCommand) (missioncontrol.MissionDTO, error) {
	if strings.TrimSpace(cmd.OriginKind) != "" || strings.TrimSpace(cmd.OriginTodoID) != "" || strings.TrimSpace(cmd.OriginDevelopmentProjectID) != "" || strings.TrimSpace(cmd.ExistingConversationID) != "" {
		return missioncontrol.MissionDTO{}, missioncontrol.ErrInvalidRequest
	}
	var origin discussionsession.PersonalAssistantOrigin
	var missionOrigin missioncontrol.PersonalAssistantMissionOrigin
	if sourceID := strings.TrimSpace(cmd.SourcePersonalAssistantConversationID); sourceID != "" {
		resolver, ok := p.discussions.(productionPersonalAssistantOriginResolver)
		if !ok {
			return missioncontrol.MissionDTO{}, missioncontrol.ErrServiceDependency
		}
		resolved, err := resolver.ResolvePersonalAssistantOrigin(ctx, discussionSessionViewer(viewer), sourceID)
		if err != nil {
			return missioncontrol.MissionDTO{}, err
		}
		origin = resolved
		missionOrigin, err = missioncontrol.NewPersonalAssistantMissionOrigin(origin.OwnerID, origin.AuthorizationSubjectID)
		if err != nil {
			return missioncontrol.MissionDTO{}, err
		}
		// The source conversation is transport-only. Only the opaque,
		// resolver-validated provenance crosses into Mission creation.
		cmd.SourcePersonalAssistantConversationID = ""
	}
	var mission missioncontrol.MissionDTO
	var err error
	if missionOrigin == (missioncontrol.PersonalAssistantMissionOrigin{}) {
		mission, err = p.missionCommand.CreateMission(ctx, viewer, cmd)
	} else {
		mission, err = p.missionCommand.CreateMissionWithPersonalAssistantOrigin(ctx, viewer, cmd, missionOrigin)
	}
	if err != nil {
		return missioncontrol.MissionDTO{}, err
	}
	if origin == (discussionsession.PersonalAssistantOrigin{}) {
		origin, err = p.missionPersonalAssistantOrigin(ctx, viewer.Tenant(), missioncore.MissionID(mission.MissionID))
		if err != nil {
			return missioncontrol.MissionDTO{}, err
		}
	}
	if _, err := p.ensureMissionDiscussionSession(ctx, viewer, mission, origin); err != nil {
		return missioncontrol.MissionDTO{}, err
	}
	if err := p.publishMissionCreatedToOntology(ctx, viewer, mission); err != nil {
		return missioncontrol.MissionDTO{}, err
	}
	return mission, nil
}

func (p productionAPI) ensureMissionDiscussionSession(ctx context.Context, viewer missioncontrol.ViewerRef, mission missioncontrol.MissionDTO, origin discussionsession.PersonalAssistantOrigin) (string, error) {
	if strings.TrimSpace(mission.OriginTodoID) != "" {
		return "", nil
	}
	if p.discussions == nil {
		return "", nil
	}
	if p.missionRepo == nil {
		return "", missioncontrol.ErrServiceDependency
	}
	discussionViewer, err := missionDiscussionViewer(viewer, origin)
	if err != nil {
		return "", err
	}
	conversation, err := p.discussions.EnsureMissionConversation(ctx, discussionViewer, discussionsession.EnsureMissionConversationCommand{
		MissionID:                        mission.MissionID,
		Title:                            mission.Title,
		Status:                           mission.Status,
		CreatedAt:                        mission.CreatedAt,
		PersonalAssistantOriginOwnerID:   origin.OwnerID,
		PersonalAssistantOriginSubjectID: origin.AuthorizationSubjectID,
	})
	if err != nil {
		return "", err
	}
	if conversation.ConversationID == "" {
		return "", nil
	}
	missionID := missioncore.MissionID(mission.MissionID)
	for attempt := 0; attempt < 8; attempt++ {
		model, err := loadProductionMissionProjection(ctx, p.missionRepo, viewer.Tenant(), missionID)
		if err != nil {
			return "", err
		}
		if model.Discussion.ConversationID == conversation.ConversationID {
			if err := p.ensureMissionConversationTeamMembers(ctx, model, conversation.ConversationID); err != nil {
				return "", err
			}
			return conversation.ConversationID, nil
		}
		model.Discussion.ConversationID = conversation.ConversationID
		model.UpdatedAt = time.Now().UTC()
		if model.Checkpoint.MissionID.IsZero() {
			model.Checkpoint.MissionID = missionID
		}
		model.Checkpoint.Version++
		model.Checkpoint.UpdatedAt = model.UpdatedAt
		if err := saveProductionMissionProjection(ctx, p.missionRepo, viewer.Tenant(), model); err != nil {
			if errors.Is(err, missioncontrol.ErrConflict) {
				continue
			}
			return "", err
		}
		if err := p.ensureMissionConversationTeamMembers(ctx, model, conversation.ConversationID); err != nil {
			return "", err
		}
		return conversation.ConversationID, nil
	}
	return "", missioncontrol.ErrConflict
}

func (p productionAPI) missionPersonalAssistantOrigin(ctx context.Context, tenantID string, missionID missioncore.MissionID) (discussionsession.PersonalAssistantOrigin, error) {
	if p.missionRepo == nil {
		return discussionsession.PersonalAssistantOrigin{}, missioncontrol.ErrServiceDependency
	}
	model, err := loadProductionMissionProjection(ctx, p.missionRepo, tenantID, missionID)
	if err != nil {
		return discussionsession.PersonalAssistantOrigin{}, err
	}
	ownerID := strings.TrimSpace(model.Mission.PersonalAssistantOriginOwnerID)
	subjectID := strings.TrimSpace(model.Mission.PersonalAssistantOriginSubjectID)
	if (ownerID == "") != (subjectID == "") {
		return discussionsession.PersonalAssistantOrigin{}, missioncontrol.ErrInvalidRequest
	}
	return discussionsession.PersonalAssistantOrigin{OwnerID: ownerID, AuthorizationSubjectID: subjectID}, nil
}

func missionDiscussionViewer(viewer missioncontrol.ViewerRef, origin discussionsession.PersonalAssistantOrigin) (discussionsession.ViewerRef, error) {
	ownerID := strings.TrimSpace(origin.OwnerID)
	subjectID := strings.TrimSpace(origin.AuthorizationSubjectID)
	if (ownerID == "") != (subjectID == "") {
		return discussionsession.ViewerRef{}, missioncontrol.ErrInvalidRequest
	}
	if subjectID == "" {
		return discussionSessionViewer(viewer), nil
	}
	return discussionsession.ViewerRef{
		ViewerID:       subjectID,
		OwnerID:        ownerID,
		AgentProfileID: subjectID,
		ViewerKind:     discussionsession.ViewerKindUser,
		TenantID:       viewer.Tenant(),
	}, nil
}

func (p productionAPI) ensureMissionConversationTeamMembers(ctx context.Context, model missionprojection.ReadModel, conversationID string) error {
	if p.discussions == nil {
		return missioncontrol.ErrServiceDependency
	}
	tenantID := firstNonEmptyText(model.TenantID, model.Mission.TenantID, "global")
	missionID := model.Mission.MissionID.String()
	agentIDs := make([]string, 0, len(model.Team.Members))
	humanIDs := make([]string, 0, len(model.Team.Members))
	for _, member := range model.Team.Members {
		if member.Status == missionprojection.TeamMemberReleased || member.AgentProfileID.IsZero() {
			continue
		}
		if member.AgentKind == agentcore.AgentKindHuman {
			humanIDs = append(humanIDs, member.AgentProfileID.String())
			continue
		}
		stableAgentID := firstNonEmptyText(member.AgentID.String(), member.AgentProfileID.String())
		agentIDs = append(agentIDs, stableAgentID)
		if p.authorizationRelations == nil {
			return missioncontrol.ErrServiceDependency
		}
		if err := p.authorizationRelations.WriteMissionParticipant(ctx, authorization.SubjectRef{Kind: "agent", ID: stableAgentID}, missionID, tenantID); err != nil {
			return err
		}
		if discussionID := strings.TrimSpace(model.Discussion.DiscussionGroupID); discussionID != "" {
			if err := p.authorizationRelations.WriteDiscussionMember(ctx, authorization.SubjectRef{Kind: "agent", ID: stableAgentID}, discussionID, tenantID); err != nil {
				return err
			}
		}
	}
	request := missioncontrol.CanonicalConversationMembershipSetRequest{
		ActorKind: "system", ActorID: "mission-api", TenantID: tenantID, ConversationID: conversationID,
		MissionID: missionID, AgentIDs: agentIDs, ManagedSource: "mission-team:" + tenantID + "/" + missionID,
	}
	if todoID := strings.TrimSpace(model.Mission.OriginTodoID); strings.TrimSpace(model.Mission.OriginKind) == "todo" && todoID != "" {
		request.TodoID = todoID
		request.ManagedSource = "todo-mission:" + tenantID + "/" + todoID + "/" + missionID
		return p.discussions.ReconcileTodoMissionAgentMembers(ctx, request)
	}
	request.HumanIDs = humanIDs
	return p.discussions.ReconcileMissionAgentMembers(ctx, request)
}

func discussionSessionViewer(viewer missioncontrol.ViewerRef) discussionsession.ViewerRef {
	kind := discussionsession.ViewerKindUser
	viewerID := strings.TrimSpace(viewer.AuthorizationSubjectID)
	if viewerID == "" {
		viewerID = strings.TrimSpace(viewer.ViewerID)
	}
	agentProfileID := strings.TrimSpace(viewer.AuthorizationSubjectID)
	if strings.EqualFold(agentProfileID, agentcore.SystemSuperadminPrincipalID) {
		kind = discussionsession.ViewerKindSystem
		agentProfileID = ""
	}
	switch viewer.ViewerKind {
	case missioncontrol.ViewerKindAgent:
		kind = discussionsession.ViewerKindAgent
		viewerID = strings.TrimSpace(viewer.ViewerID)
		agentProfileID = ""
	case missioncontrol.ViewerKindSystem:
		kind = discussionsession.ViewerKindSystem
		viewerID = strings.TrimSpace(viewer.ViewerID)
		agentProfileID = ""
	}
	return discussionsession.ViewerRef{
		ViewerID:       viewerID,
		OwnerID:        strings.TrimSpace(viewer.ViewerID),
		AgentProfileID: agentProfileID,
		ViewerKind:     kind,
		TenantID:       viewer.Tenant(),
	}
}

func (p productionAPI) DeleteMission(ctx context.Context, viewer missioncontrol.ViewerRef, missionID missioncore.MissionID) error {
	if err := viewer.Validate(); err != nil {
		return err
	}
	if p.missionRepo == nil {
		return missioncontrol.ErrServiceDependency
	}
	if err := p.authorizeMissionAction(ctx, viewer, missionID, "mission.manage"); err != nil {
		return err
	}
	model, err := loadProductionMissionProjection(ctx, p.missionRepo, viewer.Tenant(), missionID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(model.Mission.OriginTodoID) != "" {
		// Todo execution attempts are immutable history. They require an
		// explicit close/archive command rather than projection deletion.
		return missioncontrol.ErrConflict
	}
	if p.ontologySecurity == nil || p.ontologySecurity.Scopes == nil {
		return missioncontrol.ErrServiceDependency
	}
	rollbackBinding, err := prepareOntologySourceDeletion(
		ctx, p.ontologySecurity.Scopes,
		identity.ObjectRef{Type: "mission", ID: missionID.String()},
		[]string{missionScopeTenantID(viewer.Tenant(), missionID.String())}, "mission.delete:"+viewer.ViewerID,
	)
	if err != nil {
		return err
	}
	finishBindingDeletion := func(result error) error {
		return errors.Join(result, rollbackBinding(ctx))
	}
	if p.missionResponsibilityCleaner != nil {
		now := time.Now().UTC()
		if _, err := p.missionResponsibilityCleaner.TerminateMissionAssignments(
			ctx,
			viewer.Tenant(),
			missionID.String(),
			viewer.ViewerID,
			"Mission deleted",
			now,
		); err != nil {
			return errors.Join(err, rollbackBinding(ctx))
		}
	}
	if p.unitOfWork != nil && p.taskRepo != nil {
		if err := p.unitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
			return p.taskRepo.DeleteMissionTasks(ctx, tx, missionID)
		}); err != nil {
			return errors.Join(err, rollbackBinding(ctx))
		}
	}
	deleter, ok := p.missionRepo.(interface {
		DeleteForTenant(context.Context, string, missioncore.MissionID) error
	})
	if !ok {
		return errors.Join(missioncontrol.ErrServiceDependency, rollbackBinding(ctx))
	}
	if err := deleter.DeleteForTenant(ctx, viewer.Tenant(), missionID); err != nil {
		return errors.Join(err, rollbackBinding(ctx))
	}
	if p.missionLinkCleaner != nil {
		if err := p.missionLinkCleaner.DeleteMissionLinks(ctx, missionID.String()); err != nil {
			return finishBindingDeletion(err)
		}
	}
	if p.authorizationRelations != nil {
		participants := relationSubjectsFromTeamMembers(model.Team.Members)
		creator := relationSubjectFromMissionOwner(model)
		systemCreated := creator.Kind == "system"
		var relationErr error
		if strings.TrimSpace(model.Mission.OriginTodoID) != "" || systemCreated {
			relationErr = p.authorizationRelations.DeleteMissionParticipants(ctx, missionID.String(), viewer.Tenant(), participants)
		} else {
			relationErr = p.authorizationRelations.DeleteMissionRelations(ctx, creator, missionID.String(), viewer.Tenant(), participants)
		}
		if relationErr != nil {
			return finishBindingDeletion(relationErr)
		}
		for _, owner := range relationControlOwnersFromTeamMembers(model.Team.Members) {
			if owner == creator {
				continue
			}
			if err := p.authorizationRelations.DeleteMissionOwner(ctx, owner, missionID.String(), viewer.Tenant()); err != nil {
				return finishBindingDeletion(err)
			}
		}
		if systemCreated {
			if err := p.authorizationRelations.DeleteMissionAutomation(ctx, creator, missionID.String(), viewer.Tenant()); err != nil && !errors.Is(err, authorization.ErrUnsupported) {
				return finishBindingDeletion(err)
			}
		}
		if err := p.authorizationRelations.DeleteMissionAutomation(ctx, authorization.SubjectRef{Kind: "system", ID: qaPlanSystemViewerID}, missionID.String(), viewer.Tenant()); err != nil && !errors.Is(err, authorization.ErrUnsupported) {
			return finishBindingDeletion(err)
		}
		if err := p.authorizationRelations.DeleteMissionOrigins(ctx, missionID.String(), viewer.Tenant(), model.Mission.OriginTodoID, model.Mission.OriginDevelopmentProjectID); err != nil {
			return finishBindingDeletion(err)
		}
		if err := p.authorizationRelations.DeleteMissionOrigins(ctx, missionID.String(), viewer.Tenant(), model.Mission.OriginTodoID, model.Mission.OriginDevelopmentProjectID); err != nil {
			return finishBindingDeletion(err)
		}
		if discussionID := strings.TrimSpace(model.Discussion.DiscussionGroupID); discussionID != "" {
			discussionOwner := creator
			if systemCreated {
				discussionOwner = authorization.SubjectRef{}
			}
			return finishBindingDeletion(p.authorizationRelations.DeleteDiscussionRelations(ctx, discussionOwner, discussionID, missionID.String(), viewer.Tenant(), participants))
		}
	}
	return finishBindingDeletion(nil)
}

func (p productionAPI) CancelMission(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CancelMissionCommand) (missioncontrol.CommandReceiptDTO, error) {
	return p.missionCommand.CancelMission(ctx, viewer, cmd)
}

func (p productionAPI) DeleteMissionTask(ctx context.Context, viewer missioncontrol.ViewerRef, missionID missioncore.MissionID, taskID missiontask.TaskID) error {
	if err := viewer.Validate(); err != nil {
		return err
	}
	if missionID.IsZero() || taskID.IsZero() {
		return missioncontrol.ErrInvalidRequest
	}
	if err := p.authorizeMissionAction(ctx, viewer, missionID, "mission.edit"); err != nil {
		return err
	}
	if p.missionRepo == nil {
		return missioncontrol.ErrServiceDependency
	}
	model, err := loadProductionMissionProjection(ctx, p.missionRepo, viewer.Tenant(), missionID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(model.Mission.OriginTodoID) != "" {
		return missioncontrol.ErrConflict
	}
	taskInMission := false
	for _, task := range model.TaskBoard.Tasks {
		if task.TaskID == missioncore.TaskID(taskID.String()) {
			taskInMission = true
			break
		}
	}
	if !taskInMission {
		return missioncontrol.ErrProjectionNotFound
	}
	if p.unitOfWork == nil || p.taskRepo == nil {
		return missioncontrol.ErrServiceDependency
	}
	if err := p.unitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		record, err := p.taskRepo.GetTask(ctx, tx, taskID)
		if err != nil {
			return err
		}
		if record.Contract.MissionID != missionID {
			return missioncontrol.ErrProjectionNotFound
		}
		return p.taskRepo.DeleteTask(ctx, tx, taskID)
	}); err != nil {
		return err
	}
	nextTasks := model.TaskBoard.Tasks[:0]
	for _, task := range model.TaskBoard.Tasks {
		if task.TaskID != missioncore.TaskID(taskID.String()) {
			nextTasks = append(nextTasks, task)
		}
	}
	model.TaskBoard.Tasks = nextTasks
	nextDeps := model.TaskBoard.Dependencies[:0]
	for _, dep := range model.TaskBoard.Dependencies {
		if dep.TaskID != missioncore.TaskID(taskID.String()) && dep.DependsOn != missioncore.TaskID(taskID.String()) {
			nextDeps = append(nextDeps, dep)
		}
	}
	model.TaskBoard.Dependencies = nextDeps
	model.UpdatedAt = time.Now().UTC()
	if model.Checkpoint.MissionID.IsZero() {
		model.Checkpoint.MissionID = missionID
	}
	model.Checkpoint.Version++
	model.Checkpoint.UpdatedAt = model.UpdatedAt
	return saveProductionMissionProjection(ctx, p.missionRepo, viewer.Tenant(), model)
}

func (p productionAPI) CreateTask(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CreateTaskCommand) (missioncontrol.TaskCardDTO, error) {
	task, err := p.missionCommand.CreateTask(ctx, viewer, cmd)
	if err != nil {
		return missioncontrol.TaskCardDTO{}, err
	}
	if err := p.publishMissionTaskCreatedToOntology(ctx, viewer, cmd.MissionID.String(), task); err != nil {
		return missioncontrol.TaskCardDTO{}, err
	}
	return task, nil
}

func (p productionAPI) publishMissionTaskCreatedToOntology(ctx context.Context, viewer missioncontrol.ViewerRef, missionID string, task missioncontrol.TaskCardDTO) error {
	if p.ontologyProject == nil {
		return nil
	}
	eventID := "mission-task-created:" + task.TaskID
	occurredAt := task.UpdatedAt
	return p.applyOntologyEvent(ctx, graphprotocol.DomainEvent{
		ID:       eventID,
		TenantID: missionScopeTenantID(viewer.Tenant(), missionID),
		Type:     "mission.task.created",
		StreamID: task.TaskID,
		Version:  1,
		Payload: map[string]any{
			"mission_id": missionID,
			"task_id":    task.TaskID,
			"title":      task.Title,
			"status":     task.Status,
			"event_id":   eventID,
		},
		PublishedBy:     graphprotocol.ActorRef{Kind: string(viewer.ViewerKind), ID: viewer.ViewerID},
		OccurredAt:      occurredAt,
		SourceAccessRef: missionOntologySourceRef(viewer.Tenant(), missionID),
	})
}

func (p productionAPI) AssignTask(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.AssignTaskCommand) (missioncontrol.CommandReceiptDTO, error) {
	return p.missionCommand.AssignTask(ctx, viewer, cmd)
}

func (p productionAPI) StartAgentRun(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.StartAgentRunCommand) (missioncontrol.CommandReceiptDTO, error) {
	return p.missionCommand.StartAgentRun(ctx, viewer, cmd)
}

func (p productionAPI) CancelAgentRun(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CancelAgentRunCommand) (missioncontrol.CommandReceiptDTO, error) {
	return p.missionCommand.CancelAgentRun(ctx, viewer, cmd)
}

func (p productionAPI) RetryAgentRun(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.RetryAgentRunCommand) (missioncontrol.CommandReceiptDTO, error) {
	return p.missionCommand.RetryAgentRun(ctx, viewer, cmd)
}

func (p productionAPI) SubmitAgentResult(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.SubmitResultCommand) (missioncontrol.CommandReceiptDTO, error) {
	return p.missionCommand.SubmitAgentResult(ctx, viewer, cmd)
}

func (p productionAPI) SubmitQADecision(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.QADecisionCommand) (missioncontrol.CommandReceiptDTO, error) {
	return p.missionCommand.SubmitQADecision(ctx, viewer, cmd)
}

func (p productionAPI) PostDiscussionMessage(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.PostDiscussionMessageCommand) (missioncontrol.CommandReceiptDTO, error) {
	receipt, err := p.missionCommand.PostDiscussionMessage(ctx, viewer, cmd)
	if err != nil {
		return missioncontrol.CommandReceiptDTO{}, err
	}
	if err := p.refreshMissionProjection(ctx, viewer.Tenant(), cmd.MissionID); err != nil {
		return missioncontrol.CommandReceiptDTO{}, err
	}
	return receipt, nil
}

func (p productionAPI) ReplyDiscussionMessage(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.ReplyDiscussionMessageCommand) (missioncontrol.CommandReceiptDTO, error) {
	receipt, err := p.missionCommand.ReplyDiscussionMessage(ctx, viewer, cmd)
	if err != nil {
		return missioncontrol.CommandReceiptDTO{}, err
	}
	if err := p.refreshMissionProjection(ctx, viewer.Tenant(), cmd.MissionID); err != nil {
		return missioncontrol.CommandReceiptDTO{}, err
	}
	return receipt, nil
}

func (p productionAPI) ListInbox(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.ListInboxCommand) (missioncontrol.InboxPageDTO, error) {
	return p.missionCommand.ListInbox(ctx, viewer, cmd)
}

func (p productionAPI) MarkInboxRead(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.InboxItemCommand) (missioncontrol.CommandReceiptDTO, error) {
	return p.missionCommand.MarkInboxRead(ctx, viewer, cmd)
}

func (p productionAPI) CloseInboxItem(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.InboxItemCommand) (missioncontrol.CommandReceiptDTO, error) {
	receipt, err := p.missionCommand.CloseInboxItem(ctx, viewer, cmd)
	if err != nil {
		return missioncontrol.CommandReceiptDTO{}, err
	}
	if err := p.refreshMissionProjection(ctx, viewer.Tenant(), missioncore.MissionID(receipt.MissionID)); err != nil {
		return missioncontrol.CommandReceiptDTO{}, err
	}
	return receipt, nil
}

func (p productionAPI) ListApprovals(ctx context.Context, viewer missioncontrol.ViewerRef, query missioncontrol.ApprovalListQuery) (missioncontrol.ApprovalPageDTO, error) {
	return p.approvals.ListApprovals(ctx, viewer, query)
}

func (p productionAPI) DecideApproval(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.ApprovalDecisionCommand) (missioncontrol.CommandReceiptDTO, error) {
	return p.approvals.DecideApproval(ctx, viewer, cmd)
}

func (p productionAPI) Snapshot(ctx context.Context, missionID string, req wstransport.SubscribeRequest) (missioncontrol.MissionControlViewDTO, error) {
	viewer, err := streamViewer(req)
	if err != nil {
		return missioncontrol.MissionControlViewDTO{}, err
	}
	return p.GetMissionControlView(ctx, viewer, missioncore.MissionID(missionID))
}

func (p productionAPI) Patches(ctx context.Context, missionID string, req wstransport.SubscribeRequest, afterVersion int) ([]missioncontrol.MissionControlPatchDTO, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	viewer, err := streamViewer(req)
	if err != nil {
		return nil, err
	}
	if p.missionRepo == nil || p.stream == nil {
		return nil, missioncontrol.ErrServiceDependency
	}
	if err := p.authorizeMissionAction(ctx, viewer, missioncore.MissionID(missionID), "mission.view"); err != nil {
		return nil, err
	}
	tenantRepo, ok := p.missionRepo.(interface {
		GetMissionControlForTenant(context.Context, string, missioncore.MissionID) (missionprojection.ReadModel, error)
	})
	if !ok {
		return nil, missioncontrol.ErrServiceDependency
	}
	model, err := tenantRepo.GetMissionControlForTenant(ctx, viewer.Tenant(), missioncore.MissionID(missionID))
	if err != nil {
		return nil, err
	}
	if model.Checkpoint.Version <= afterVersion {
		return nil, nil
	}
	patch, err := p.stream.BuildPatch(ctx, viewer, model, afterVersion)
	if err != nil {
		return nil, err
	}
	patch.Payload, err = p.filterTodoMissionDiscussion(ctx, viewer, patch.Payload)
	if err != nil {
		return nil, err
	}
	return []missioncontrol.MissionControlPatchDTO{patch}, nil
}

func (p productionAPI) filterTodoMissionDiscussion(ctx context.Context, viewer missioncontrol.ViewerRef, view missioncontrol.MissionControlViewDTO) (missioncontrol.MissionControlViewDTO, error) {
	if strings.TrimSpace(view.Mission.OriginTodoID) == "" {
		return view, nil
	}
	allowed, err := p.canViewTodoMissionDiscussion(ctx, viewer, view.Discussion.ConversationID)
	if err != nil {
		return missioncontrol.MissionControlViewDTO{}, err
	}
	if !allowed {
		return restrictTodoMissionDiscussion(view), nil
	}
	return view, nil
}

func (p productionAPI) canViewTodoMissionDiscussion(ctx context.Context, viewer missioncontrol.ViewerRef, conversationID string) (bool, error) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return false, nil
	}
	if p.discussions == nil {
		return false, missioncontrol.ErrServiceDependency
	}
	if _, err := p.discussions.GetConversation(ctx, discussionSessionViewer(viewer), conversationID); err != nil {
		if errors.Is(err, discussionsession.ErrForbidden) || errors.Is(err, discussionsession.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func restrictTodoMissionOps(view missioncontrol.MissionOpsViewDTO) missioncontrol.MissionOpsViewDTO {
	keptFocus := make([]missioncontrol.OpsFocusDTO, 0, len(view.CurrentFocus))
	for _, focus := range view.CurrentFocus {
		if todoMissionOpsDiscussionSource(focus.Kind, focus.SourceType) {
			continue
		}
		keptFocus = append(keptFocus, focus)
	}
	keptActions := make([]missioncontrol.ActionCardDTO, 0, len(view.ActionCards))
	for _, action := range view.ActionCards {
		if todoMissionOpsDiscussionSource(action.Kind, action.SourceType) {
			continue
		}
		keptActions = append(keptActions, action)
	}
	view.CurrentFocus = keptFocus
	view.ActionCards = keptActions
	for index := range view.RuntimeSessions {
		view.RuntimeSessions[index].ErrorSummary = ""
	}
	for index := range view.AgentLanes {
		view.AgentLanes[index].InboxOpenCount = 0
		for sessionIndex := range view.AgentLanes[index].RuntimeSessions {
			view.AgentLanes[index].RuntimeSessions[sessionIndex].ErrorSummary = ""
		}
	}
	return view
}

func todoMissionOpsDiscussionSource(kind, sourceType string) bool {
	switch strings.ToLower(strings.TrimSpace(sourceType)) {
	case "inbox", "discussion", "message":
		return true
	}
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "inbox", "reply_to_agent":
		return true
	default:
		return false
	}
}

func restrictTodoMissionDiscussion(view missioncontrol.MissionControlViewDTO) missioncontrol.MissionControlViewDTO {
	view.Discussion = missioncontrol.DiscussionDTO{
		Restricted: true, Messages: []missioncontrol.MessageDTO{}, PendingMentions: []missioncontrol.MentionDTO{},
		Decisions: []missioncontrol.DecisionDTO{}, Blockers: []missioncontrol.BlockerDTO{},
	}
	view.LatestDecisions = []missioncontrol.DecisionDTO{}
	view.InboxSummary = missioncontrol.InboxSummaryDTO{Items: []missioncontrol.InboxItemDTO{}}
	keptNodes := make([]missioncontrol.ProgressNodeDTO, 0, len(view.ProgressGraph.Nodes))
	keptIDs := make(map[string]struct{}, len(view.ProgressGraph.Nodes))
	for _, node := range view.ProgressGraph.Nodes {
		if todoMissionDiscussionEvent(node.EventType) {
			continue
		}
		keptNodes = append(keptNodes, node)
		keptIDs[node.ID] = struct{}{}
	}
	keptEdges := make([]missioncontrol.ProgressEdgeDTO, 0, len(view.ProgressGraph.Edges))
	for _, edge := range view.ProgressGraph.Edges {
		_, fromOK := keptIDs[edge.From]
		_, toOK := keptIDs[edge.To]
		if fromOK && toOK {
			keptEdges = append(keptEdges, edge)
		}
	}
	keptFocus := make([]string, 0, len(view.ProgressGraph.CurrentFocus))
	for _, id := range view.ProgressGraph.CurrentFocus {
		if _, ok := keptIDs[id]; ok {
			keptFocus = append(keptFocus, id)
		}
	}
	view.ProgressGraph.Nodes = keptNodes
	view.ProgressGraph.Edges = keptEdges
	view.ProgressGraph.CurrentFocus = keptFocus
	return view
}

func todoMissionDiscussionEvent(eventType string) bool {
	switch strings.ToLower(strings.TrimSpace(eventType)) {
	case "discussiongroupcreated", "messageposted", "agentmentioned", "agentreplied", "decisionrecorded", "blockerraised":
		return true
	default:
		return false
	}
}

func (p productionAPI) authorizeMissionAction(ctx context.Context, viewer missioncontrol.ViewerRef, missionID missioncore.MissionID, action string) error {
	if p.authorizationService == nil {
		return missioncontrol.ErrServiceDependency
	}
	if viewer.ViewerKind == missioncontrol.ViewerKindSystem && strings.TrimSpace(viewer.ViewerID) == agentcore.SystemSuperadminPrincipalID && strings.TrimSpace(viewer.AuthorizationSubjectID) == agentcore.SystemSuperadminPrincipalID {
		return nil
	}
	subjectID := strings.TrimSpace(viewer.AuthorizationSubjectID)
	if subjectID == "" {
		subjectID = strings.TrimSpace(viewer.ViewerID)
	}
	principal := identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "internal",
		Subject:         subjectID,
		UserID:          subjectID,
		Username:        subjectID,
		ViewerKind:      string(viewer.ViewerKind),
		TenantID:        viewer.Tenant(),
		CoarseScopes:    append([]string(nil), viewer.PermissionScope...),
	})
	object := identity.ObjectRef{Type: "mission", ID: missionID.String(), TenantID: viewer.Tenant()}
	err := p.authorizationService.Can(ctx, principal, action, object)
	if action == "mission.view" && errors.Is(err, authorization.ErrDenied) {
		err = p.authorizationService.Can(ctx, principal, "mission.monitor", object)
	}
	if errors.Is(err, authorization.ErrDenied) {
		return missioncontrol.ErrProjectionNotFound
	}
	return err
}

func streamViewer(req wstransport.SubscribeRequest) (missioncontrol.ViewerRef, error) {
	viewer := missioncontrol.ViewerRef{
		ViewerID:               req.ViewerID,
		ViewerKind:             missioncontrol.ViewerKind(req.ViewerKind),
		TenantID:               req.TenantID,
		AuthorizationSubjectID: req.AuthorizationSubjectID,
		MissionRoles:           append([]string(nil), req.MissionRoles...),
		PermissionScope:        append([]string(nil), req.PermissionScope...),
	}
	if err := viewer.Validate(); err != nil {
		return missioncontrol.ViewerRef{}, err
	}
	return viewer, nil
}
