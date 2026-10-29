package discussionsession

import (
	"context"
	"strings"
)

func (s *Service) GetConversationPage(ctx context.Context, viewer ViewerRef, conversationID string, query ListMessagesQuery) (ConversationDTO, error) {
	if s == nil || s.repo == nil {
		return ConversationDTO{}, ErrServiceDependency
	}
	if strings.TrimSpace(viewer.ViewerID) == "" || strings.TrimSpace(conversationID) == "" {
		return ConversationDTO{}, ErrInvalidRequest
	}
	if _, err := s.requireConversationAccess(ctx, viewer, conversationID); err != nil {
		return ConversationDTO{}, err
	}
	limit, offset := normalizeMessagePage(query)
	view, page, err := s.repo.GetConversationMessagePage(ctx, strings.TrimSpace(conversationID), limit, offset)
	if err != nil {
		return ConversationDTO{}, err
	}
	view = s.withAvailableMissionLinks(ctx, view)
	if err := s.markCompletedConversationViewed(ctx, &view); err != nil {
		return ConversationDTO{}, err
	}
	return mapConversationDTOWithMessagePage(view, &page), nil
}

func (s *Service) ListMessages(ctx context.Context, viewer ViewerRef, conversationID string, query ListMessagesQuery) (MessagePageDTO, error) {
	if s == nil || s.repo == nil {
		return MessagePageDTO{}, ErrServiceDependency
	}
	if strings.TrimSpace(viewer.ViewerID) == "" || strings.TrimSpace(conversationID) == "" {
		return MessagePageDTO{}, ErrInvalidRequest
	}
	if _, err := s.requireConversationAccess(ctx, viewer, conversationID); err != nil {
		return MessagePageDTO{}, err
	}
	limit, offset := normalizeMessagePage(query)
	messages, page, err := s.repo.ListMessagesPage(ctx, strings.TrimSpace(conversationID), limit, offset)
	if err != nil {
		return MessagePageDTO{}, err
	}
	attachments, err := s.repo.ListAttachments(ctx, strings.TrimSpace(conversationID))
	if err != nil {
		return MessagePageDTO{}, err
	}
	return MessagePageDTO{Messages: mapMessagesWithAttachments(messages, attachments), Page: page}, nil
}
