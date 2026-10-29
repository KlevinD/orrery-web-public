package discussionsession

import (
	"context"
	"strings"

	"orrery_v2/internal/app/missioncontrol"
)

// EnsureMissionAgentMember grants a managed Mission agent access to the
// canonical conversation. Todo conversations use the stricter Todo-specific
// entry point so their canonical ownership cannot be bypassed.
func (s *Service) EnsureMissionAgentMember(ctx context.Context, request missioncontrol.CanonicalConversationMembershipRequest) error {
	if s == nil || s.repo == nil || s.agents == nil {
		return ErrServiceDependency
	}
	tenantID := strings.TrimSpace(request.TenantID)
	conversationID := strings.TrimSpace(request.ConversationID)
	missionID := strings.TrimSpace(request.MissionID)
	agentID := strings.TrimSpace(request.AgentID)
	managedSource := strings.TrimSpace(request.ManagedSource)
	if strings.TrimSpace(request.ActorKind) != "system" || strings.TrimSpace(request.ActorID) == "" {
		return ErrForbidden
	}
	if tenantID == "" || conversationID == "" || missionID == "" || agentID == "" || managedSource == "" || strings.TrimSpace(request.TodoID) != "" {
		return ErrInvalidRequest
	}
	if managedSource != missionTaskAssigneeManagedSource(tenantID, missionID) && managedSource != missionTeamManagedSource(tenantID, missionID) {
		return ErrForbidden
	}

	unlock := s.lockRunLifecycleForContext(ctx, conversationID)
	defer unlock()
	view, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	conversationTenantID := firstNonBlank(strings.TrimSpace(view.TenantID), "global")
	if conversationTenantID != tenantID || strings.TrimSpace(view.ConversationKind) == "direct" || strings.TrimSpace(view.PurposeKind) != "" || !conversationLinksMissionID(view.LinkedMissions, missionID) {
		return ErrForbidden
	}
	return s.ensureManagedMissionAgentMember(ctx, view, agentID, managedSource)
}

func (s *Service) ReconcileMissionAgentMembers(ctx context.Context, request missioncontrol.CanonicalConversationMembershipSetRequest) error {
	if s == nil || s.repo == nil || s.agents == nil {
		return ErrServiceDependency
	}
	tenantID := strings.TrimSpace(request.TenantID)
	conversationID := strings.TrimSpace(request.ConversationID)
	missionID := strings.TrimSpace(request.MissionID)
	managedSource := strings.TrimSpace(request.ManagedSource)
	if strings.TrimSpace(request.ActorKind) != "system" || strings.TrimSpace(request.ActorID) == "" {
		return ErrForbidden
	}
	if tenantID == "" || conversationID == "" || missionID == "" || managedSource == "" || strings.TrimSpace(request.TodoID) != "" {
		return ErrInvalidRequest
	}
	if managedSource != missionTaskAssigneeManagedSource(tenantID, missionID) && managedSource != missionTeamManagedSource(tenantID, missionID) {
		return ErrForbidden
	}

	unlock := s.lockRunLifecycleForContext(ctx, conversationID)
	defer unlock()
	view, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	conversationTenantID := firstNonBlank(strings.TrimSpace(view.TenantID), "global")
	if conversationTenantID != tenantID || strings.TrimSpace(view.ConversationKind) == "direct" || strings.TrimSpace(view.PurposeKind) != "" || !conversationLinksMissionID(view.LinkedMissions, missionID) {
		return ErrForbidden
	}
	if err := s.reconcileManagedMissionAgentMembers(ctx, view, request.AgentIDs, managedSource); err != nil {
		return err
	}
	return s.reconcileManagedMissionHumanMembers(ctx, view, request.HumanIDs, missionID, managedSource)
}

func (s *Service) ensureManagedMissionAgentMember(ctx context.Context, view ConversationRecordView, agentID, managedSource string) error {
	for _, member := range view.Members {
		if strings.TrimSpace(member.MemberKind) == "agent" && strings.TrimSpace(member.MemberID) == agentID {
			if strings.TrimSpace(member.ExplicitGrantorID) == "" && !conversationMemberHasManagedSource(member, managedSource) {
				_, _, err := s.repo.MutateMemberManagedSource(ctx, member, managedSource, true)
				return err
			}
			return nil
		}
	}
	entries, err := s.resolveConversationMemberAddEntries(ctx, []string{agentID})
	if err != nil {
		return err
	}
	if _, err := s.addDiscussionMemberEntriesWithPolicy(ctx, view, entries, "Mission 执行者", s.now(), true); err != nil {
		return err
	}
	updated, err := s.repo.GetConversation(ctx, view.ConversationID)
	if err != nil {
		return err
	}
	for _, member := range updated.Members {
		if strings.TrimSpace(member.MemberKind) != "agent" || strings.TrimSpace(member.MemberID) != agentID {
			continue
		}
		if strings.TrimSpace(member.ExplicitGrantorID) == "" && !conversationMemberHasManagedSource(member, managedSource) {
			_, _, err := s.repo.MutateMemberManagedSource(ctx, member, managedSource, true)
			return err
		}
		return nil
	}
	return ErrConversationMember
}

func (s *Service) reconcileManagedMissionAgentMembers(ctx context.Context, view ConversationRecordView, agentIDs []string, managedSource string) error {
	entries, err := s.resolveConversationMemberAddEntries(ctx, agentIDs)
	if err != nil {
		return err
	}
	desired := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if conversationMemberKindForDirectoryEntry(entry) != "agent" {
			return ErrInvalidRequest
		}
		agentID := conversationDirectoryEntryMemberID(entry)
		desired[agentID] = struct{}{}
		if err := s.ensureManagedMissionAgentMember(ctx, view, agentID, managedSource); err != nil {
			return err
		}
	}
	updated, err := s.repo.GetConversation(ctx, view.ConversationID)
	if err != nil {
		return err
	}
	for _, member := range updated.Members {
		if strings.TrimSpace(member.MemberKind) != "agent" || !conversationMemberHasManagedSource(member, managedSource) {
			continue
		}
		if _, ok := desired[strings.TrimSpace(member.MemberID)]; ok {
			continue
		}
		if _, exists, err := s.repo.MutateMemberManagedSource(ctx, member, managedSource, false); err != nil {
			return err
		} else if !exists {
			s.deleteConversationMemberAuthorization(ctx, view.ConversationID, view.TenantID, member)
		}
	}
	return nil
}

func missionTaskAssigneeManagedSource(tenantID, missionID string) string {
	return "mission-task-assignee:" + strings.TrimSpace(tenantID) + "/" + strings.TrimSpace(missionID)
}

func missionTeamManagedSource(tenantID, missionID string) string {
	return "mission-team:" + strings.TrimSpace(tenantID) + "/" + strings.TrimSpace(missionID)
}
