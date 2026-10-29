package discussionsession

import (
	"context"
	"strings"
	"time"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

func RuntimeToolCallDTO(name string, callID string, input map[string]any) ToolCallDTO {
	return ToolCallDTO{
		ToolCallID: strings.TrimSpace(callID),
		Name:       strings.TrimSpace(name),
		Status:     "completed",
		Arguments:  mapAnyStringValues(input),
	}
}

func (s *Service) ApplyRuntimeToolCall(ctx context.Context, conversationID string, runID string, agentID string, sourceMessageID string, mode string, call ToolCallDTO, boundTools []DiscussionAgentBoundTool, at time.Time) (ToolCallDTO, []LinkedMissionDTO, error) {
	return s.applyRuntimeToolCall(ctx, conversationID, runID, agentID, ViewerRef{}, sourceMessageID, mode, call, boundTools, at)
}

func (s *Service) ApplyRuntimeToolCallAs(ctx context.Context, conversationID string, runID string, agentID string, principal ViewerRef, sourceMessageID string, mode string, call ToolCallDTO, boundTools []DiscussionAgentBoundTool, at time.Time) (ToolCallDTO, []LinkedMissionDTO, error) {
	return s.applyRuntimeToolCall(ctx, conversationID, runID, agentID, principal, sourceMessageID, mode, call, boundTools, at)
}

func runtimeToolPrincipalForConversation(view ConversationRecordView, principal ViewerRef) (ViewerRef, error) {
	if strings.TrimSpace(principal.ViewerID) == "" {
		return principal, nil
	}
	if tenantID := strings.TrimSpace(principal.TenantID); tenantID != "" && tenantID != strings.TrimSpace(view.TenantID) {
		return ViewerRef{}, ErrForbidden
	}
	if principal.ViewerKind != ViewerKindUser && principal.ViewerKind != ViewerKindHuman {
		return principal, nil
	}

	principalID := strings.TrimSpace(principal.ViewerID)
	profileID := strings.TrimSpace(view.CreatedBy)
	ownerID := firstNonBlank(view.OwnerID, profileID)
	if principalID == ownerID && profileID != "" {
		principal.OwnerID = ownerID
		principal.ViewerID = profileID
		return principal, nil
	}
	if principalID == profileID && profileID != "" {
		principal.OwnerID = ownerID
		return principal, nil
	}
	return ViewerRef{}, ErrForbidden
}

func runtimeToolMissionViewer(agentID, tenantID string, principal ViewerRef) missioncontrol.ViewerRef {
	if strings.TrimSpace(principal.ViewerID) == "" {
		return discussionAgentViewer(agentID, tenantID)
	}
	viewer := missionViewer(principal)
	if principal.ViewerKind != ViewerKindUser && principal.ViewerKind != ViewerKindHuman {
		viewer.PermissionScope = []string{"mission.read", "mission.write", "discussion.read", "discussion.write"}
	}
	return viewer
}
