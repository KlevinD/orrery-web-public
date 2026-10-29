package discussionsession

import (
	"context"
	"strconv"
	"strings"
	"time"
)

const (
	DiscussionHILModeManual = "manual"
	DiscussionHILModeAuto   = "auto"
	DiscussionHILModeNone   = "none"
	hilModeUpdateAttempts   = 3
	hilModeRealtimeTimeout  = 10 * time.Second
)

type SetConversationHILModeCommand struct {
	ConversationID string
	Mode           string
	IdempotencyKey string
}

func (s *Service) SetConversationHILMode(ctx context.Context, viewer ViewerRef, cmd SetConversationHILModeCommand) (ConversationHILModeDTO, error) {
	if s == nil || s.repo == nil {
		return ConversationHILModeDTO{}, ErrServiceDependency
	}
	conversationID := strings.TrimSpace(cmd.ConversationID)
	mode, ok := validDiscussionHILMode(cmd.Mode)
	if conversationID == "" || strings.TrimSpace(viewer.ViewerID) == "" || !ok {
		return ConversationHILModeDTO{}, ErrInvalidRequest
	}
	var updated ConversationRecord
	for attempt := 0; attempt < hilModeUpdateAttempts; attempt++ {
		record, err := s.repo.GetConversationRecord(ctx, conversationID)
		if err != nil {
			return ConversationHILModeDTO{}, err
		}
		if tenantID := strings.TrimSpace(record.TenantID); tenantID != "" && tenantID != strings.TrimSpace(viewer.TenantID) {
			return ConversationHILModeDTO{}, ErrForbidden
		}
		if (viewer.ViewerKind != ViewerKindUser && viewer.ViewerKind != ViewerKindHuman) || strings.TrimSpace(record.CreatedBy) != strings.TrimSpace(viewer.ViewerID) {
			return ConversationHILModeDTO{}, ErrForbidden
		}
		currentMode, currentModeIsExplicit := validDiscussionHILMode(record.HILMode)
		if currentModeIsExplicit && currentMode == mode {
			return mapConversationHILModeDTO(record), nil
		}
		updated = record
		updated.HILMode = mode
		updated.UpdatedAt = firstTimeAfter(s.now(), record.UpdatedAt)
		updated.Revision = record.Revision + 1
		if updated.Revision <= 0 {
			updated.Revision = 1
		}
		swapped, err := s.repo.CompareAndSwapConversation(ctx, record, updated)
		if err != nil {
			return ConversationHILModeDTO{}, err
		}
		if swapped {
			s.appendConversationHILModeNotifications(ctx, updated)
			return mapConversationHILModeDTO(updated), nil
		}
	}
	return ConversationHILModeDTO{}, ErrRevisionConflict
}

func (s *Service) appendConversationHILModeNotifications(ctx context.Context, updated ConversationRecord) {
	revision := strconv.FormatInt(updated.Revision, 10)
	conversationID := updated.ConversationID
	notifyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), hilModeRealtimeTimeout)
	go func() {
		defer cancel()
		if err := s.appendRealtimeConversationEvent(notifyCtx, RealtimeEventDiscussionHILModeUpdated, "discussion-hil-mode-updated-"+conversationID+"-"+revision, conversationID, map[string]string{
			"conversation_id": conversationID,
			"hil_mode":        normalizeDiscussionHILMode(updated.HILMode),
			"revision":        revision,
			"updated_at":      realtimeTime(updated.UpdatedAt),
		}); err != nil && s.logger != nil {
			s.logger.Warn(notifyCtx, "discussion HIL mode realtime notification failed", "conversation_id", conversationID, "error", err)
		}
		if err := s.appendRealtimeConversationListInvalidated(notifyCtx, ConversationRecordView{ConversationRecord: updated}, "hil-mode-"+conversationID+"-"+revision); err != nil && s.logger != nil {
			s.logger.Warn(notifyCtx, "discussion HIL mode list notification failed", "conversation_id", conversationID, "error", err)
		}
	}()
}

func mapConversationHILModeDTO(record ConversationRecord) ConversationHILModeDTO {
	return ConversationHILModeDTO{
		ConversationID: record.ConversationID,
		Revision:       record.Revision,
		HILMode:        normalizeDiscussionHILMode(record.HILMode),
		UpdatedAt:      record.UpdatedAt,
	}
}

func normalizeDiscussionHILMode(value string) string {
	mode, ok := validDiscussionHILMode(value)
	if !ok {
		return DiscussionHILModeManual
	}
	return mode
}

func validDiscussionHILMode(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case DiscussionHILModeManual:
		return DiscussionHILModeManual, true
	case DiscussionHILModeAuto, "automatic":
		return DiscussionHILModeAuto, true
	case DiscussionHILModeNone:
		return DiscussionHILModeNone, true
	default:
		return "", false
	}
}
