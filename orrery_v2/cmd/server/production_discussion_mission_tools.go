package main

import (
	"context"
	"strings"

	discussionsession "orrery_v2/internal/app/discussionsession"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
)

// productionDiscussionMissionToolCommands keeps model tool arguments separate
// from the trusted source-conversation provenance used for PA-origin Missions.
type productionDiscussionMissionToolCommands struct {
	api *productionAPI
}

func (p productionDiscussionMissionToolCommands) CreateMission(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CreateMissionCommand) (missioncontrol.MissionDTO, error) {
	if p.api == nil {
		return missioncontrol.MissionDTO{}, missioncontrol.ErrServiceDependency
	}
	return p.api.CreateMission(ctx, viewer, cmd)
}

func (p productionDiscussionMissionToolCommands) CreateMissionFromDiscussion(ctx context.Context, viewer missioncontrol.ViewerRef, conversationID string, cmd missioncontrol.CreateMissionCommand) (missioncontrol.MissionDTO, error) {
	if p.api == nil || p.api.discussions == nil {
		return missioncontrol.MissionDTO{}, missioncontrol.ErrServiceDependency
	}
	conversation, err := p.api.discussions.GetConversation(ctx, discussionSessionViewer(viewer), strings.TrimSpace(conversationID))
	if err != nil {
		return missioncontrol.MissionDTO{}, err
	}
	if canInheritPersonalAssistantMissionOrigin(viewer) && (conversation.ConversationKind == discussionsession.ConversationKindPersonalAssistant || conversation.PersonalAssistantRuntime) {
		cmd.SourcePersonalAssistantConversationID = conversation.ConversationID
	}
	return p.api.CreateMission(ctx, viewer, cmd)
}

// PA provenance is a user identity, not a property of every agent executing
// in a PA-origin Mission group. The PA host is represented as a user viewer;
// physical worker agents must create under their own identity.
func canInheritPersonalAssistantMissionOrigin(viewer missioncontrol.ViewerRef) bool {
	switch viewer.ViewerKind {
	case missioncontrol.ViewerKindUser, missioncontrol.ViewerKindHuman:
		return true
	default:
		return false
	}
}

func (p productionDiscussionMissionToolCommands) UpdateMissionBrief(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.UpdateMissionBriefCommand) (missioncontrol.MissionDTO, error) {
	if p.api == nil || p.api.missionCommand == nil {
		return missioncontrol.MissionDTO{}, missioncontrol.ErrServiceDependency
	}
	return p.api.missionCommand.UpdateMissionBrief(ctx, viewer, cmd)
}

func (p productionDiscussionMissionToolCommands) CreateTask(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CreateTaskCommand) (missioncontrol.TaskCardDTO, error) {
	if p.api == nil {
		return missioncontrol.TaskCardDTO{}, missioncontrol.ErrServiceDependency
	}
	return p.api.CreateTask(ctx, viewer, cmd)
}

func (p productionDiscussionMissionToolCommands) AssignTask(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.AssignTaskCommand) (missioncontrol.CommandReceiptDTO, error) {
	if p.api == nil {
		return missioncontrol.CommandReceiptDTO{}, missioncontrol.ErrServiceDependency
	}
	return p.api.AssignTask(ctx, viewer, cmd)
}

func (p productionDiscussionMissionToolCommands) StartAgentRun(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.StartAgentRunCommand) (missioncontrol.CommandReceiptDTO, error) {
	if p.api == nil {
		return missioncontrol.CommandReceiptDTO{}, missioncontrol.ErrServiceDependency
	}
	return p.api.StartAgentRun(ctx, viewer, cmd)
}

func (p productionDiscussionMissionToolCommands) SubmitAgentResult(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.SubmitResultCommand) (missioncontrol.CommandReceiptDTO, error) {
	if p.api == nil {
		return missioncontrol.CommandReceiptDTO{}, missioncontrol.ErrServiceDependency
	}
	return p.api.SubmitAgentResult(ctx, viewer, cmd)
}

func (p productionDiscussionMissionToolCommands) CheckMissionCloseReadiness(ctx context.Context, viewer missioncontrol.ViewerRef, missionID, taskID string) (discussionsession.MissionCloseReadiness, error) {
	if p.api == nil || p.api.missionQuery == nil {
		return discussionsession.MissionCloseReadiness{}, missioncontrol.ErrServiceDependency
	}
	missionRef := missioncore.MissionID(strings.TrimSpace(missionID))
	control, err := p.api.missionQuery.GetMissionControlView(ctx, viewer, missionRef)
	if err != nil {
		return discussionsession.MissionCloseReadiness{}, err
	}
	ops, err := p.api.missionQuery.GetMissionOpsView(ctx, viewer, missionRef)
	if err != nil {
		return discussionsession.MissionCloseReadiness{}, err
	}
	for _, session := range ops.RuntimeSessions {
		switch strings.TrimSpace(strings.ToLower(session.Status)) {
		case "queued", "running":
			return discussionsession.MissionCloseReadiness{Status: discussionsession.MissionClosePending}, nil
		}
	}
	for _, task := range control.TaskBoard.Tasks {
		if strings.TrimSpace(task.TaskID) != strings.TrimSpace(taskID) {
			continue
		}
		switch strings.TrimSpace(strings.ToLower(task.Status)) {
		case "approved":
			return discussionsession.MissionCloseReadiness{Status: discussionsession.MissionCloseCompleted}, nil
		case "started", "blocked":
			if missionCloseViewerOwnsTask(viewer, task.Assignee) {
				return discussionsession.MissionCloseReadiness{Status: discussionsession.MissionCloseReady}, nil
			}
		}
		return discussionsession.MissionCloseReadiness{Status: discussionsession.MissionClosePending}, nil
	}
	return discussionsession.MissionCloseReadiness{Status: discussionsession.MissionClosePending}, nil
}

func missionCloseViewerOwnsTask(viewer missioncontrol.ViewerRef, assignee string) bool {
	assignee = strings.TrimSpace(assignee)
	return assignee != "" && (assignee == strings.TrimSpace(viewer.ViewerID) || assignee == strings.TrimSpace(viewer.AuthorizationSubjectID))
}
