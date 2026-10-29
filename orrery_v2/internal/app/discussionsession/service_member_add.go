package discussionsession

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func (s *Service) AddConversationMembers(ctx context.Context, viewer ViewerRef, cmd AddConversationMembersCommand) (ConversationDTO, error) {
	if s == nil || s.repo == nil || s.agents == nil {
		return ConversationDTO{}, ErrServiceDependency
	}
	conversationID := strings.TrimSpace(cmd.ConversationID)
	if conversationID == "" || strings.TrimSpace(viewer.ViewerID) == "" || len(nonEmptyStrings(cmd.TargetIDs)) == 0 {
		return ConversationDTO{}, ErrInvalidRequest
	}
	unlock := s.lockRunLifecycle(conversationID)
	defer unlock()
	view, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return ConversationDTO{}, err
	}
	if err := ensureConversationMemberAdditionsAllowed(view.ConversationRecord); err != nil {
		return ConversationDTO{}, err
	}
	if conversationMemberName(view.Members, conversationViewerMemberKind(viewer.ViewerKind), strings.TrimSpace(viewer.ViewerID)) == "" {
		return ConversationDTO{}, ErrForbidden
	}
	entries, err := s.resolveConversationMemberAddEntries(ctx, cmd.TargetIDs)
	if err != nil {
		return ConversationDTO{}, err
	}
	explicitGrantTargets := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		kind := conversationMemberKindForDirectoryEntry(entry)
		id := conversationDirectoryEntryMemberID(entry)
		found := false
		for _, member := range view.Members {
			if member.MemberKind == kind && member.MemberID == id {
				found = true
				if len(conversationMemberManagedSources(member)) > 0 {
					explicitGrantTargets[kind+":"+id] = struct{}{}
				}
			}
		}
		if !found {
			explicitGrantTargets[kind+":"+id] = struct{}{}
		}
	}
	if _, err := s.addDiscussionMemberEntries(ctx, view, entries, "", s.now()); err != nil {
		return ConversationDTO{}, err
	}
	updated, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return ConversationDTO{}, err
	}
	grantedAt := s.now()
	for _, member := range updated.Members {
		if _, ok := explicitGrantTargets[member.MemberKind+":"+member.MemberID]; !ok {
			continue
		}
		member.ManagedSource = ""
		member.ManagedSources = nil
		member.ExplicitGrantorID = strings.TrimSpace(viewer.ViewerID)
		member.ExplicitGrantorKind = conversationViewerMemberKind(viewer.ViewerKind)
		member.ExplicitGrantedAt = grantedAt
		if err := s.repo.SaveMember(ctx, member); err != nil {
			return ConversationDTO{}, err
		}
	}
	updated, err = s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return ConversationDTO{}, err
	}
	return mapConversationDTO(updated), nil
}

func (s *Service) resolveConversationMemberAddEntries(ctx context.Context, targetIDs []string) ([]agentcore.AgentDirectoryEntry, error) {
	page, err := s.agents.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{EnabledOnly: true, IncludeAI: true, IncludeHumans: true})
	if err != nil {
		return nil, fmt.Errorf("%w: query personnel directory: %v", ErrConversationMember, err)
	}
	ids := nonEmptyStrings(targetIDs)
	entries := make([]agentcore.AgentDirectoryEntry, 0, len(ids))
	for _, id := range ids {
		entry, ok := findConversationMemberAddEntry(page.Entries, id)
		if !ok {
			return nil, fmt.Errorf("%w: target %s missing from enabled personnel directory", ErrConversationMember, id)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (s *Service) convertDirectConversationToGroupIfNeeded(ctx context.Context, conversationID string, at time.Time) error {
	if s == nil || s.repo == nil {
		return ErrServiceDependency
	}
	view, err := s.repo.GetConversation(ctx, strings.TrimSpace(conversationID))
	if err != nil {
		return err
	}
	if strings.TrimSpace(view.ConversationKind) != "direct" || len(view.Members) <= 2 {
		return nil
	}
	view.ConversationKind = ""
	view.DirectAgentID = ""
	view.DirectTargetKind = ""
	view.DirectTargetID = ""
	view.UpdatedAt = firstTimeAfter(firstTime(at, s.now()), view.UpdatedAt)
	if strings.TrimSpace(view.Title) == "" {
		view.Title = "聊天"
	}
	return s.repo.SaveConversation(ctx, view.ConversationRecord)
}
