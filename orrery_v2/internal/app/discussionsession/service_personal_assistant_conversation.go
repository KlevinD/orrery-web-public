package discussionsession

import (
	"context"
	"strings"
)

func (s *Service) findLatestPersonalAssistantConversation(ctx context.Context, viewer ViewerRef) (ConversationRecordView, bool, error) {
	if s == nil || s.repo == nil {
		return ConversationRecordView{}, false, ErrServiceDependency
	}
	viewerID := strings.TrimSpace(viewer.ViewerID)
	if viewerID == "" {
		return ConversationRecordView{}, false, ErrInvalidRequest
	}
	ownerID := firstNonBlank(strings.TrimSpace(viewer.OwnerID), viewerID)
	record, found, err := s.repo.findLatestPersonalAssistantConversation(ctx, viewer.TenantID, viewerID, ownerID, viewer.IdentityIDs...)
	if err != nil {
		return ConversationRecordView{}, false, err
	}
	if !found {
		return ConversationRecordView{}, false, nil
	}
	return ConversationRecordView{ConversationRecord: record}, true, nil
}

func (s *Service) resolvePersonalAssistantConversationForSend(ctx context.Context, viewer ViewerRef, cmd *SendMessageCommand) (string, error) {
	if cmd == nil || strings.TrimSpace(cmd.ConversationID) != "" || cmd.ForceNewConversation || !isPersonalAssistantConversationKind(cmd.ConversationKind) {
		return "", nil
	}
	existing, found, err := s.findLatestPersonalAssistantConversation(ctx, viewer)
	if err != nil || !found {
		return "", err
	}
	cmd.ConversationID = strings.TrimSpace(existing.ConversationID)
	return cmd.ConversationID, nil
}
