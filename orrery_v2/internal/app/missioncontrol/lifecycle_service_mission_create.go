package missioncontrol

import "context"

func (s *LifecycleService) CreateMissionWithPersonalAssistantOrigin(ctx context.Context, viewer ViewerRef, cmd CreateMissionCommand, origin PersonalAssistantMissionOrigin) (MissionDTO, error) {
	return s.createMission(ctx, viewer, cmd, origin)
}

func (s *LifecycleService) createMission(ctx context.Context, viewer ViewerRef, cmd CreateMissionCommand, origin PersonalAssistantMissionOrigin) (MissionDTO, error) {
	if err := s.validateMissionCreateRequest(viewer, &cmd, origin); err != nil {
		return MissionDTO{}, err
	}
	intent, err := s.ensureMissionCreationIntent(ctx, viewer, cmd, origin)
	if err != nil {
		return MissionDTO{}, err
	}
	intent, err = s.advanceMissionCreation(ctx, intent.IntentKey)
	if err != nil {
		return MissionDTO{}, err
	}
	return MapViewDTO(intent.Model, viewer).Mission, nil
}

func (s *LifecycleService) validateMissionCreateRequest(viewer ViewerRef, cmd *CreateMissionCommand, origin PersonalAssistantMissionOrigin) error {
	if s == nil || s.router == nil || s.repo == nil || s.authorization == nil || s.authzRelations == nil {
		return ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return err
	}
	*cmd = normalizeMissionCreationCommand(*cmd)
	if cmd.IdempotencyKey == "" || cmd.FinalGoal == "" || cmd.SourcePersonalAssistantConversationID != "" {
		return ErrInvalidRequest
	}
	if (!origin.isZero() && !origin.valid()) || (origin.valid() && origin.subject() != firstString(viewer.AuthorizationSubjectID, viewer.ViewerID)) {
		return ErrInvalidRequest
	}
	return validateMissionCreationOrigin(viewer, *cmd)
}

func validateMissionCreationOrigin(viewer ViewerRef, cmd CreateMissionCommand) error {
	hasInternalLink := cmd.MissionID != "" || cmd.OriginKind != "" || cmd.OriginID != "" || cmd.OriginTodoID != "" || cmd.ExecutionAttempt != 0 || cmd.DiscussionMode != "" || cmd.OriginDevelopmentProjectID != "" || cmd.ExistingConversationID != "" || cmd.InitialAgentProfileID != ""
	if !hasInternalLink {
		return nil
	}
	if viewer.ViewerKind != ViewerKindSystem {
		return ErrInvalidRequest
	}
	if cmd.OriginKind == "" {
		if cmd.MissionID != "" || cmd.OriginID != "" || cmd.OriginTodoID != "" || cmd.ExecutionAttempt != 0 || cmd.DiscussionMode != "" || cmd.OriginDevelopmentProjectID != "" || cmd.InitialAgentProfileID != "" {
			return ErrInvalidRequest
		}
		return nil
	}
	switch cmd.OriginKind {
	case "todo":
		if cmd.MissionID != "" || cmd.OriginID == "" || cmd.OriginTodoID == "" || cmd.OriginID != cmd.OriginTodoID || cmd.ExecutionAttempt <= 0 || cmd.DiscussionMode != "existing_conversation" || cmd.ExistingConversationID == "" {
			return ErrInvalidRequest
		}
	case "scheduler":
		if cmd.OriginID == "" || cmd.OriginTodoID != "" || cmd.ExecutionAttempt != 0 || cmd.DiscussionMode != "" || cmd.OriginDevelopmentProjectID != "" || cmd.ExistingConversationID != "" || cmd.InitialAgentProfileID == "" {
			return ErrInvalidRequest
		}
	default:
		return ErrInvalidRequest
	}
	return nil
}
