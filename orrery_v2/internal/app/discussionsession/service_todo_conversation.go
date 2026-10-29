package discussionsession

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

const todoConversationPurposeKind = "todo"

func (s *Service) EnsureTodoConversation(ctx context.Context, viewer ViewerRef, cmd EnsureTodoConversationCommand) (ConversationDTO, error) {
	if s == nil || s.repo == nil || s.agents == nil || s.humanProfiles == nil {
		return ConversationDTO{}, ErrServiceDependency
	}
	viewer.TenantID = strings.TrimSpace(viewer.TenantID)
	cmd.TenantID = strings.TrimSpace(cmd.TenantID)
	cmd.TodoID = strings.TrimSpace(cmd.TodoID)
	cmd.ManagedSource = strings.TrimSpace(cmd.ManagedSource)
	if cmd.TenantID == "" {
		cmd.TenantID = viewer.TenantID
	}
	if cmd.TenantID == "" || cmd.TenantID != viewer.TenantID || cmd.TodoID == "" || !todoManagedSourceMatches(cmd.TenantID, cmd.TodoID, cmd.ManagedSource) || strings.TrimSpace(viewer.ViewerID) == "" {
		return ConversationDTO{}, ErrInvalidRequest
	}
	conversationID := firstNonBlank(strings.TrimSpace(cmd.ExistingConversationID), todoConversationID(cmd.TenantID, cmd.TodoID))
	unlock := s.lockRunLifecycle(conversationID)
	defer unlock()
	if existing, err := s.repo.GetConversation(ctx, conversationID); err == nil {
		if strings.TrimSpace(existing.PurposeKind) == "" && conversationAccessibleToViewer(existing, viewer) {
			existing.TenantID = cmd.TenantID
			existing.PurposeKind = todoConversationPurposeKind
			existing.PurposeID = cmd.TodoID
			existing.WorkspaceID = strings.TrimSpace(cmd.WorkspaceID)
			existing.UpdatedAt = s.now()
			if err := s.repo.SaveConversation(ctx, existing.ConversationRecord); err != nil {
				return ConversationDTO{}, err
			}
			existing, err = s.repo.GetConversation(ctx, conversationID)
			if err != nil {
				return ConversationDTO{}, err
			}
		}
		if existing.TenantID != cmd.TenantID || existing.PurposeKind != todoConversationPurposeKind || existing.PurposeID != cmd.TodoID {
			return ConversationDTO{}, ErrRevisionConflict
		}
		if _, err := s.ReconcileConversationMembers(ctx, viewer, ReconcileConversationMembersCommand{ConversationID: conversationID, TargetIDs: cmd.InitialMemberIDs, ManagedSource: cmd.ManagedSource}); err != nil {
			return ConversationDTO{}, err
		}
		updated, err := s.repo.GetConversation(ctx, conversationID)
		if err != nil {
			return ConversationDTO{}, err
		}
		s.writeConversationAuthorization(ctx, conversationID)
		return mapConversationDTO(updated), nil
	} else if err != ErrNotFound {
		return ConversationDTO{}, err
	}

	now := s.now()
	allowMembers := true
	conversation := ConversationRecord{
		ConversationID: conversationID, TenantID: cmd.TenantID, PurposeKind: todoConversationPurposeKind, PurposeID: cmd.TodoID,
		WorkspaceID: strings.TrimSpace(cmd.WorkspaceID), MemberAdditionsAllowed: &allowMembers,
		Title: firstNonBlank(strings.TrimSpace(cmd.Title), "Todo "+cmd.TodoID), Status: "draft",
		CreatedBy: strings.TrimSpace(viewer.ViewerID), CreatedAt: now, UpdatedAt: now,
	}
	allowWorkerInternalQA := todoManagedSourceMatches(conversation.TenantID, conversation.PurposeID, cmd.ManagedSource)
	members, err := s.initialConversationMembersFromCommandWithPolicy(ctx, conversation, viewer, SendMessageCommand{InitialMemberIDs: cmd.InitialMemberIDs}, now, allowWorkerInternalQA)
	if err != nil {
		return ConversationDTO{}, err
	}
	for index := range members {
		members[index] = grantConversationMemberManagedSource(members[index], cmd.ManagedSource)
	}
	if name := conversationMemberName(members, conversationViewerMemberKind(viewer.ViewerKind), viewer.ViewerID); name != "" {
		conversation.CreatedByName = name
	}
	if _, err := s.repo.CreateConversationWithMembers(ctx, conversation, members); err != nil {
		if !errors.Is(err, ErrRevisionConflict) {
			return ConversationDTO{}, err
		}
		concurrent, getErr := s.repo.GetConversation(ctx, conversationID)
		if getErr != nil {
			return ConversationDTO{}, errors.Join(err, getErr)
		}
		if concurrent.TenantID != cmd.TenantID || concurrent.PurposeKind != todoConversationPurposeKind || concurrent.PurposeID != cmd.TodoID {
			return ConversationDTO{}, ErrRevisionConflict
		}
		if _, reconcileErr := s.ReconcileConversationMembers(ctx, viewer, ReconcileConversationMembersCommand{
			ConversationID: conversationID, TargetIDs: cmd.InitialMemberIDs, ManagedSource: cmd.ManagedSource,
		}); reconcileErr != nil {
			return ConversationDTO{}, reconcileErr
		}
	}
	created, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return ConversationDTO{}, err
	}
	s.writeConversationAuthorization(ctx, conversationID)
	return mapConversationDTO(created), nil
}

func (s *Service) BindTodoWorkspace(ctx context.Context, viewer ViewerRef, conversationID, todoID, workspaceID string) (ConversationDTO, error) {
	if s == nil || s.repo == nil {
		return ConversationDTO{}, ErrServiceDependency
	}
	conversationID, todoID, workspaceID = strings.TrimSpace(conversationID), strings.TrimSpace(todoID), strings.TrimSpace(workspaceID)
	if viewer.ViewerKind != ViewerKindSystem || strings.TrimSpace(viewer.TenantID) == "" || conversationID == "" || todoID == "" || workspaceID == "" {
		return ConversationDTO{}, ErrForbidden
	}
	unlock := s.lockRunLifecycle(conversationID)
	defer unlock()
	view, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return ConversationDTO{}, err
	}
	if view.TenantID != strings.TrimSpace(viewer.TenantID) || view.PurposeKind != todoConversationPurposeKind || view.PurposeID != todoID {
		return ConversationDTO{}, ErrForbidden
	}
	if view.WorkspaceID != "" && view.WorkspaceID != workspaceID {
		return ConversationDTO{}, ErrRevisionConflict
	}
	view.WorkspaceID, view.WorkspaceRootDir, view.UpdatedAt = workspaceID, "", s.now()
	if err := s.repo.SaveConversation(ctx, view.ConversationRecord); err != nil {
		return ConversationDTO{}, err
	}
	updated, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return ConversationDTO{}, err
	}
	return mapConversationDTO(updated), nil
}

func (s *Service) ReconcileConversationMembers(ctx context.Context, viewer ViewerRef, cmd ReconcileConversationMembersCommand) (ConversationDTO, error) {
	if s == nil || s.repo == nil || s.agents == nil {
		return ConversationDTO{}, ErrServiceDependency
	}
	conversationID := strings.TrimSpace(cmd.ConversationID)
	managedSource := strings.TrimSpace(cmd.ManagedSource)
	if conversationID == "" || managedSource == "" || strings.TrimSpace(viewer.ViewerID) == "" {
		return ConversationDTO{}, ErrInvalidRequest
	}
	view, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return ConversationDTO{}, err
	}
	if view.PurposeKind != todoConversationPurposeKind {
		return ConversationDTO{}, ErrForbidden
	}
	if !todoManagedSourceMatches(view.TenantID, view.PurposeID, managedSource) {
		return ConversationDTO{}, ErrForbidden
	}
	if viewer.ViewerKind == ViewerKindSystem {
		if strings.TrimSpace(viewer.TenantID) != strings.TrimSpace(view.TenantID) {
			return ConversationDTO{}, ErrForbidden
		}
	} else if !conversationAccessibleToViewer(view, viewer) {
		return ConversationDTO{}, ErrForbidden
	}
	entries, err := s.resolveConversationMemberAddEntries(ctx, cmd.TargetIDs)
	if err != nil {
		return ConversationDTO{}, err
	}
	desired := map[string]bool{}
	for _, entry := range entries {
		desired[conversationMemberKindForDirectoryEntry(entry)+":"+conversationDirectoryEntryMemberID(entry)] = true
	}
	if viewer.ViewerKind != ViewerKindSystem {
		desired[conversationViewerMemberKind(viewer.ViewerKind)+":"+strings.TrimSpace(viewer.ViewerID)] = true
	}
	for _, member := range view.Members {
		key := strings.TrimSpace(member.MemberKind) + ":" + strings.TrimSpace(member.MemberID)
		if conversationMemberHasManagedSource(member, managedSource) && !desired[key] {
			if _, exists, err := s.repo.MutateMemberManagedSource(ctx, member, managedSource, false); err != nil {
				return ConversationDTO{}, err
			} else if !exists {
				s.deleteConversationMemberAuthorization(ctx, view.ConversationID, view.TenantID, member)
			}
		}
	}
	updated, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return ConversationDTO{}, err
	}
	if _, err := s.addDiscussionMemberEntriesWithPolicy(ctx, updated, entries, "Todo 参与者", s.now(), true); err != nil {
		return ConversationDTO{}, err
	}
	updated, err = s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return ConversationDTO{}, err
	}
	for _, member := range updated.Members {
		key := strings.TrimSpace(member.MemberKind) + ":" + strings.TrimSpace(member.MemberID)
		if desired[key] && strings.TrimSpace(member.ExplicitGrantorID) == "" && !conversationMemberHasManagedSource(member, managedSource) {
			if _, _, err := s.repo.MutateMemberManagedSource(ctx, member, managedSource, true); err != nil {
				return ConversationDTO{}, err
			}
		}
	}
	updated, err = s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return ConversationDTO{}, err
	}
	s.writeConversationAuthorization(ctx, conversationID)
	return mapConversationDTO(updated), nil
}

func (s *Service) LinkMission(ctx context.Context, viewer ViewerRef, conversationID string, link LinkedMissionRecord) (ConversationDTO, error) {
	view, err := s.requireConversationAccess(ctx, viewer, strings.TrimSpace(conversationID))
	if err != nil {
		return ConversationDTO{}, err
	}
	if strings.TrimSpace(link.MissionID) == "" {
		return ConversationDTO{}, ErrInvalidRequest
	}
	link.ConversationID = view.ConversationID
	if link.CreatedAt.IsZero() {
		link.CreatedAt = s.now()
	}
	if err := s.repo.SaveMissionLink(ctx, link); err != nil {
		return ConversationDTO{}, err
	}
	updated, err := s.repo.GetConversation(ctx, view.ConversationID)
	if err != nil {
		return ConversationDTO{}, err
	}
	s.writeConversationAuthorization(ctx, view.ConversationID)
	return mapConversationDTO(updated), nil
}

func todoConversationID(tenantID, todoID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(tenantID) + "\x00" + strings.TrimSpace(todoID)))
	return "todo-discussion-" + hex.EncodeToString(sum[:12])
}

func todoManagedSourceMatches(tenantID, todoID, managedSource string) bool {
	tenantID = strings.TrimSpace(tenantID)
	todoID = strings.TrimSpace(todoID)
	return tenantID != "" && todoID != "" && strings.TrimSpace(managedSource) == "todo:"+tenantID+"/"+todoID
}
