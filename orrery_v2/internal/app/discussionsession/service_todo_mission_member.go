package discussionsession

import (
	"context"
	"strings"

	"orrery_v2/internal/app/missioncontrol"
)

func (s *Service) EnsureTodoMissionAgentMember(ctx context.Context, request missioncontrol.CanonicalConversationMembershipRequest) error {
	if s == nil || s.repo == nil || s.agents == nil {
		return ErrServiceDependency
	}
	tenantID := strings.TrimSpace(request.TenantID)
	conversationID := strings.TrimSpace(request.ConversationID)
	missionID := strings.TrimSpace(request.MissionID)
	todoID := strings.TrimSpace(request.TodoID)
	agentID := strings.TrimSpace(request.AgentID)
	managedSource := strings.TrimSpace(request.ManagedSource)
	if strings.TrimSpace(request.ActorKind) != "system" || strings.TrimSpace(request.ActorID) == "" {
		return ErrForbidden
	}
	if tenantID == "" || conversationID == "" || missionID == "" || todoID == "" || agentID == "" || managedSource == "" {
		return ErrInvalidRequest
	}
	if managedSource != todoMissionManagedSource(tenantID, todoID, missionID) {
		return ErrForbidden
	}

	unlock := s.lockRunLifecycleForContext(ctx, conversationID)
	defer unlock()
	view, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(view.TenantID) != tenantID || view.PurposeKind != todoConversationPurposeKind || strings.TrimSpace(view.PurposeID) != todoID || !conversationLinksMissionID(view.LinkedMissions, missionID) {
		return ErrForbidden
	}
	return s.ensureManagedMissionAgentMember(ctx, view, agentID, managedSource)
}

func (s *Service) ReconcileTodoMissionAgentMembers(ctx context.Context, request missioncontrol.CanonicalConversationMembershipSetRequest) error {
	if s == nil || s.repo == nil || s.agents == nil {
		return ErrServiceDependency
	}
	tenantID := strings.TrimSpace(request.TenantID)
	conversationID := strings.TrimSpace(request.ConversationID)
	missionID := strings.TrimSpace(request.MissionID)
	todoID := strings.TrimSpace(request.TodoID)
	managedSource := strings.TrimSpace(request.ManagedSource)
	if strings.TrimSpace(request.ActorKind) != "system" || strings.TrimSpace(request.ActorID) == "" {
		return ErrForbidden
	}
	if tenantID == "" || conversationID == "" || missionID == "" || todoID == "" || managedSource == "" {
		return ErrInvalidRequest
	}
	if managedSource != todoMissionManagedSource(tenantID, todoID, missionID) {
		return ErrForbidden
	}

	unlock := s.lockRunLifecycleForContext(ctx, conversationID)
	defer unlock()
	view, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(view.TenantID) != tenantID || view.PurposeKind != todoConversationPurposeKind || strings.TrimSpace(view.PurposeID) != todoID || !conversationLinksMissionID(view.LinkedMissions, missionID) {
		return ErrForbidden
	}
	return s.reconcileManagedMissionAgentMembers(ctx, view, request.AgentIDs, managedSource)
}

func todoMissionManagedSource(tenantID, todoID, missionID string) string {
	return "todo-mission:" + strings.TrimSpace(tenantID) + "/" + strings.TrimSpace(todoID) + "/" + strings.TrimSpace(missionID)
}

func conversationLinksMissionID(links []LinkedMissionRecord, missionID string) bool {
	for _, link := range links {
		if strings.TrimSpace(link.MissionID) == strings.TrimSpace(missionID) {
			return true
		}
	}
	return false
}

var _ missioncontrol.CanonicalConversationMembershipPort = (*Service)(nil)
