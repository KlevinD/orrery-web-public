package discussionsession

import (
	"context"
	"strings"
)

func (s *Service) ListConversations(ctx context.Context, viewer ViewerRef, query ListConversationsQuery) (ConversationPageDTO, error) {
	if s == nil || s.repo == nil {
		return ConversationPageDTO{}, ErrServiceDependency
	}
	if strings.TrimSpace(viewer.ViewerID) == "" {
		return ConversationPageDTO{}, ErrInvalidRequest
	}
	records, err := s.repo.ListConversations(ctx)
	if err != nil {
		return ConversationPageDTO{}, err
	}
	records = filterConversationsForViewer(records, viewer)
	records = filterHiddenConversationDrafts(records)
	records = prioritizeLatestPersonalAssistantConversation(records)
	pageNumber, pageSize := normalizeListPage(query)
	total := len(records)
	start := (pageNumber - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	page := ConversationPageDTO{
		Conversations: make([]ConversationSummaryDTO, 0, end-start),
		Page:          PageDTO{Page: pageNumber, PageSize: pageSize, Total: total},
	}
	for _, record := range records[start:end] {
		record = s.withAvailableMissionLinks(ctx, record)
		page.Conversations = append(page.Conversations, mapConversationSummaryDTO(record))
	}
	return page, nil
}

func prioritizeLatestPersonalAssistantConversation(records []ConversationRecordView) []ConversationRecordView {
	preferredIndex := -1
	for index, record := range records {
		if !isPersonalAssistantConversationKind(record.ConversationKind) {
			continue
		}
		if preferredIndex == -1 {
			preferredIndex = index
		}
		if hasGatewayInboundTurn(record) {
			preferredIndex = index
			break
		}
	}
	if preferredIndex >= 0 {
		if preferredIndex == 0 {
			return records
		}
		prioritized := make([]ConversationRecordView, 0, len(records))
		prioritized = append(prioritized, records[preferredIndex])
		prioritized = append(prioritized, records[:preferredIndex]...)
		prioritized = append(prioritized, records[preferredIndex+1:]...)
		return prioritized
	}
	return records
}

func hasGatewayInboundTurn(record ConversationRecordView) bool {
	for _, turn := range record.Turns {
		if strings.HasPrefix(strings.TrimSpace(turn.IdempotencyKey), "gwin_") {
			return true
		}
	}
	return false
}

func (s *Service) ListApprovalConversations(ctx context.Context, viewer ViewerRef) ([]ConversationDTO, error) {
	if s == nil || s.repo == nil {
		return nil, ErrServiceDependency
	}
	if strings.TrimSpace(viewer.ViewerID) == "" {
		return nil, ErrInvalidRequest
	}
	records, err := s.repo.ListApprovalConversations(ctx)
	if err != nil {
		return nil, err
	}
	records = filterConversationsForViewer(records, viewer)
	records = filterHiddenConversationDrafts(records)
	conversations := make([]ConversationDTO, 0, len(records))
	for _, record := range records {
		record = s.withAvailableMissionLinks(ctx, record)
		conversations = append(conversations, mapConversationDTO(record))
	}
	return conversations, nil
}

func filterHiddenConversationDrafts(records []ConversationRecordView) []ConversationRecordView {
	visible := make([]ConversationRecordView, 0, len(records))
	for _, record := range records {
		if strings.EqualFold(strings.TrimSpace(record.Status), "draft") && len(record.Messages) == 0 && len(record.Attachments) == 0 {
			continue
		}
		visible = append(visible, record)
	}
	return visible
}
