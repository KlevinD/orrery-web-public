package missioncontrol

import (
	"context"
	"errors"
	"strings"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	missioncore "orrery_v2/internal/mission/core"
	discussion "orrery_v2/internal/mission/discussion"
)

func (s *CommandService) authorizeMissionAction(ctx context.Context, viewer ViewerRef, missionID missioncore.MissionID, action string) error {
	if s == nil || s.authorization == nil {
		return ErrServiceDependency
	}
	if missionID.IsZero() {
		return ErrInvalidRequest
	}
	if isBreakglassSystemViewer(viewer) {
		return nil
	}
	err := s.authorization.Can(ctx, missionPrincipal(viewer), action, identity.ObjectRef{Type: "mission", ID: missionID.String(), TenantID: viewer.Tenant()})
	if errors.Is(err, authorization.ErrDenied) {
		return ErrProjectionNotFound
	}
	return err
}

func (s *CommandService) authorizeDiscussionAction(ctx context.Context, viewer ViewerRef, discussionID discussion.DiscussionGroupID, action string) error {
	if s == nil || s.authorization == nil {
		return ErrServiceDependency
	}
	if strings.TrimSpace(string(discussionID)) == "" {
		return ErrInvalidRequest
	}
	if isBreakglassSystemViewer(viewer) {
		return nil
	}
	err := s.authorization.Can(ctx, missionPrincipal(viewer), action, identity.ObjectRef{Type: "discussion", ID: string(discussionID), TenantID: viewer.Tenant()})
	if errors.Is(err, authorization.ErrDenied) {
		return ErrProjectionNotFound
	}
	return err
}
