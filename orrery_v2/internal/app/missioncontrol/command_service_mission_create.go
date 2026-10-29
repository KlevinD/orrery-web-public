package missioncontrol

import (
	"context"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

// PersonalAssistantMissionOriginCreatePort is intentionally separate from the
// regular command port. The opaque origin can only be minted by server-side
// source-conversation resolution.
type PersonalAssistantMissionOriginCreatePort interface {
	CreateMissionWithPersonalAssistantOrigin(ctx context.Context, viewer ViewerRef, cmd CreateMissionCommand, origin PersonalAssistantMissionOrigin) (MissionDTO, error)
}

func (s *CommandService) CreateMission(ctx context.Context, viewer ViewerRef, cmd CreateMissionCommand) (MissionDTO, error) {
	if s == nil || s.missions == nil {
		return MissionDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return MissionDTO{}, err
	}
	cmd, err := prepareMissionCreationCommand(viewer, cmd)
	if err != nil {
		return MissionDTO{}, err
	}
	mission, err := s.missions.CreateMission(ctx, viewer, cmd)
	if err != nil {
		return MissionDTO{}, err
	}
	if err := s.recordViewerAudit(ctx, viewer, missionCreationAuditEvent(viewer, cmd, mission.MissionID, mission.CreatedAt)); err != nil {
		return MissionDTO{}, err
	}
	return mission, nil
}

// CreateMissionWithPersonalAssistantOrigin is the only command-layer route
// that accepts PA provenance. Its input is not part of the transport command.
func (s *CommandService) CreateMissionWithPersonalAssistantOrigin(ctx context.Context, viewer ViewerRef, cmd CreateMissionCommand, origin PersonalAssistantMissionOrigin) (MissionDTO, error) {
	if s == nil || s.missions == nil {
		return MissionDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return MissionDTO{}, err
	}
	if !origin.valid() {
		return MissionDTO{}, ErrInvalidRequest
	}
	cmd, err := prepareMissionCreationCommand(viewer, cmd)
	if err != nil {
		return MissionDTO{}, err
	}
	missions, ok := s.missions.(PersonalAssistantMissionOriginCreatePort)
	if !ok {
		return MissionDTO{}, ErrServiceDependency
	}
	mission, err := missions.CreateMissionWithPersonalAssistantOrigin(ctx, viewer, cmd, origin)
	if err != nil {
		return MissionDTO{}, err
	}
	if err := s.recordViewerAudit(ctx, viewer, missionCreationAuditEvent(viewer, cmd, mission.MissionID, mission.CreatedAt)); err != nil {
		return MissionDTO{}, err
	}
	return mission, nil
}

func prepareMissionCreationCommand(viewer ViewerRef, cmd CreateMissionCommand) (CreateMissionCommand, error) {
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	cmd.SourcePersonalAssistantConversationID = strings.TrimSpace(cmd.SourcePersonalAssistantConversationID)
	if cmd.IdempotencyKey == "" || cmd.SourcePersonalAssistantConversationID != "" {
		return CreateMissionCommand{}, ErrInvalidRequest
	}
	if cmd.RequesterID == "" {
		cmd.RequesterID = viewer.ViewerID
	}
	if viewer.ViewerKind == ViewerKindUser || viewer.ViewerKind == ViewerKindHuman {
		viewerProfileID := firstString(viewer.AuthorizationSubjectID, viewer.ViewerID)
		cmd.InitialHumanAgentID = ""
		if !agentcore.IsReservedNonWorkforceProfileID(viewerProfileID) {
			cmd.InitialHumanAgentID = viewerProfileID
		}
	}
	return cmd, nil
}
