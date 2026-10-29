package discussionsession

import (
	"context"
	"strings"
)

func (s *Service) requireConversationAccess(ctx context.Context, viewer ViewerRef, conversationID string) (ConversationRecordView, error) {
	if s == nil || s.repo == nil {
		return ConversationRecordView{}, ErrServiceDependency
	}
	viewerID := strings.TrimSpace(viewer.ViewerID)
	conversationID = strings.TrimSpace(conversationID)
	if viewerID == "" || conversationID == "" {
		return ConversationRecordView{}, ErrInvalidRequest
	}
	view, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return ConversationRecordView{}, err
	}
	if !conversationAccessibleToViewer(view, viewer) {
		return ConversationRecordView{}, ErrForbidden
	}
	return s.withAvailableMissionLinks(ctx, view), nil
}

func conversationAccessibleToViewer(view ConversationRecordView, viewer ViewerRef) bool {
	viewerID := strings.TrimSpace(viewer.ViewerID)
	if viewerID == "" {
		return false
	}
	if tenantID := strings.TrimSpace(view.TenantID); tenantID != "" && tenantID != strings.TrimSpace(viewer.TenantID) {
		return false
	}
	if isPersonalAssistantConversationKind(view.ConversationKind) && view.PurposeKind != todoConversationPurposeKind &&
		viewer.ViewerKind == ViewerKindUser && view.TenantID != "" && view.TenantID == viewer.TenantID && view.OwnerID != "" && view.OwnerID == viewer.OwnerID {
		for _, identityID := range viewer.IdentityIDs {
			if identityID = strings.TrimSpace(identityID); identityID != "" && identityID == strings.TrimSpace(view.CreatedBy) {
				return true
			}
		}
	}
	if view.PurposeKind != todoConversationPurposeKind && strings.TrimSpace(view.CreatedBy) == viewerID {
		return true
	}
	memberKind := conversationViewerMemberKind(viewer.ViewerKind)
	return conversationMemberName(view.Members, memberKind, viewerID) != ""
}

func filterConversationsForViewer(records []ConversationRecordView, viewer ViewerRef) []ConversationRecordView {
	if len(records) == 0 {
		return records
	}
	out := make([]ConversationRecordView, 0, len(records))
	for _, record := range records {
		if conversationAccessibleToViewer(record, viewer) {
			out = append(out, record)
		}
	}
	return out
}
