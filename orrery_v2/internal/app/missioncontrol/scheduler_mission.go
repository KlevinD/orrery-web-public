package missioncontrol

import (
	"context"
	"strings"

	"orrery_v2/internal/app/authorization"
	missioncore "orrery_v2/internal/mission/core"
	discussion "orrery_v2/internal/mission/discussion"
	"orrery_v2/internal/mission/projection"
	missionteam "orrery_v2/internal/mission/team"
)

type SyncSchedulerMissionCommand struct {
	IdempotencyKey      string
	MissionID           missioncore.MissionID
	ScheduledTaskID     string
	Title               string
	FinalGoal           string
	ScheduledTaskStatus string
	ApprovalAssigneeID  string
}

func (s *LifecycleService) SyncSchedulerMission(ctx context.Context, viewer ViewerRef, cmd SyncSchedulerMissionCommand) (MissionDTO, error) {
	if s == nil || s.repo == nil {
		return MissionDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return MissionDTO{}, err
	}
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	cmd.ScheduledTaskID = strings.TrimSpace(cmd.ScheduledTaskID)
	cmd.Title = strings.TrimSpace(cmd.Title)
	cmd.FinalGoal = strings.TrimSpace(cmd.FinalGoal)
	cmd.ScheduledTaskStatus = strings.TrimSpace(cmd.ScheduledTaskStatus)
	cmd.ApprovalAssigneeID = strings.TrimSpace(cmd.ApprovalAssigneeID)
	if viewer.ViewerKind != ViewerKindSystem || cmd.IdempotencyKey == "" || cmd.MissionID.IsZero() || cmd.ScheduledTaskID == "" || cmd.Title == "" || cmd.FinalGoal == "" {
		return MissionDTO{}, ErrInvalidRequest
	}
	wantedStatus, err := schedulerMissionStatus(cmd.ScheduledTaskStatus)
	if err != nil {
		return MissionDTO{}, err
	}
	if err := s.authorizeMissionAction(ctx, viewer, cmd.MissionID, "mission.edit"); err != nil {
		return MissionDTO{}, err
	}
	model, err := loadMissionProjection(ctx, s.repo, viewer.Tenant(), cmd.MissionID)
	if err != nil {
		return MissionDTO{}, err
	}
	if strings.TrimSpace(model.Mission.OriginKind) != "scheduler" || strings.TrimSpace(model.Mission.OriginID) != cmd.ScheduledTaskID {
		return MissionDTO{}, ErrProjectionNotFound
	}
	if model.Mission.Status == projection.MissionStatusCompleted || (model.Mission.Status == projection.MissionStatusCancelled && wantedStatus != projection.MissionStatusCancelled) {
		return MissionDTO{}, ErrInvalidRequest
	}
	assigneeAdded, err := s.ensureSchedulerApprovalAssignee(ctx, viewer, cmd, &model)
	if err != nil {
		return MissionDTO{}, err
	}
	if !assigneeAdded && model.Mission.Title == cmd.Title && model.Mission.FinalGoal == cmd.FinalGoal && model.Mission.Status == wantedStatus {
		return MapViewDTO(model, viewer).Mission, nil
	}
	model.Mission.Title = cmd.Title
	model.Mission.FinalGoal = cmd.FinalGoal
	model.Mission.Status = wantedStatus
	model = bumpModel(model, s.now())
	if err := saveMissionProjection(ctx, s.repo, viewer.Tenant(), model); err != nil {
		return MissionDTO{}, err
	}
	return MapViewDTO(model, viewer).Mission, nil
}

func (s *LifecycleService) ensureSchedulerApprovalAssignee(ctx context.Context, viewer ViewerRef, cmd SyncSchedulerMissionCommand, model *projection.ReadModel) (bool, error) {
	assigneeID := strings.TrimSpace(cmd.ApprovalAssigneeID)
	if assigneeID == "" || model == nil || schedulerMissionHasHuman(*model, assigneeID) {
		return false, nil
	}
	if !s.humanBootstrap.Enabled || s.teamBootstrap == nil || s.humanProfiles == nil || s.discuss == nil || s.authzRelations == nil {
		return false, ErrServiceDependency
	}
	groupID := discussion.DiscussionGroupID(strings.TrimSpace(model.Discussion.DiscussionGroupID))
	if groupID == "" {
		return false, ErrServiceDependency
	}
	assignment, err := s.bootstrapInitialHumanAgent(ctx, groupID, cmd.MissionID, CreateMissionCommand{
		IdempotencyKey:          cmd.IdempotencyKey + "|approval-assignee|" + assigneeID,
		InitialHumanAgentID:     assigneeID,
		InitialHumanDisplayName: strings.TrimPrefix(assigneeID, "human-"),
	})
	if err != nil {
		return false, err
	}
	if assignment.ID == "" {
		return false, ErrServiceDependency
	}
	*model = s.upsertSystemRoleAssignments(ctx, *model, []missionteam.MissionAssignment{assignment}, model.UpdatedAt)
	subject := authorization.SubjectRef{Kind: "user", ID: assigneeID}
	if err := s.authzRelations.WriteMissionParticipant(ctx, subject, cmd.MissionID.String(), viewer.Tenant()); err != nil {
		return false, err
	}
	if err := s.authzRelations.WriteDiscussionMember(ctx, subject, string(groupID), viewer.Tenant()); err != nil {
		return false, err
	}
	return true, nil
}

func schedulerMissionHasHuman(model projection.ReadModel, assigneeID string) bool {
	for _, member := range model.Team.Members {
		if strings.EqualFold(strings.TrimSpace(string(member.AgentProfileID)), assigneeID) || strings.EqualFold(strings.TrimSpace(string(member.AgentID)), assigneeID) {
			return true
		}
	}
	return false
}

func schedulerMissionStatus(status string) (projection.MissionStatus, error) {
	switch strings.TrimSpace(status) {
	case "enabled":
		return projection.MissionStatusActive, nil
	case "disabled":
		return projection.MissionStatusPaused, nil
	case "archived":
		return projection.MissionStatusCancelled, nil
	default:
		return "", ErrInvalidRequest
	}
}
