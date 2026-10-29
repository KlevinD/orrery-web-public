package discussionsession

import (
	"context"
	"strings"
)

func (s *Service) commandMayDirectHumanAgentMention(ctx context.Context, cmd SendMessageCommand) bool {
	message := MessageRecord{Content: cmd.Content, Mentions: messageMentionRecordsFromDTO(cmd.Mentions)}
	if len(message.Mentions) == 0 {
		return false
	}
	conversationID := strings.TrimSpace(cmd.ConversationID)
	if conversationID != "" {
		view, err := s.repo.GetConversation(ctx, conversationID)
		if err != nil {
			return false
		}
		return len(discussionAgentMentionTargets(message, view.Members)) > 0
	}
	return len(discussionAgentMentionTargets(message, s.previewInitialAgentMembers(ctx))) > 0
}

func messageMentionRecordsFromDTO(mentions []MessageMentionDTO) []MessageMentionRecord {
	out := make([]MessageMentionRecord, 0, len(mentions))
	for _, mention := range mentions {
		out = append(out, MessageMentionRecord{TargetKind: strings.TrimSpace(mention.TargetKind), TargetID: strings.TrimSpace(mention.TargetID), DisplayName: strings.TrimSpace(mention.DisplayName)})
	}
	return out
}

func (s *Service) previewInitialAgentMembers(ctx context.Context) []ConversationMemberRecord {
	if s == nil {
		return nil
	}
	conversationID := "direct-mention-preflight"
	now := s.now()
	members := make([]ConversationMemberRecord, 0, len(s.initialAgentMembers))
	for i, seed := range s.initialAgentMembers {
		member, err := s.resolveAgentConversationMember(ctx, conversationID, seed.AgentID, seed.Role, now.AddDate(0, 0, i))
		if err != nil {
			continue
		}
		members = append(members, member)
	}
	return members
}

func (s *Service) completeDirectHumanMentions(ctx context.Context, conversationID string, source MessageRecord, onEvent func(StreamEventDTO) bool) (ConversationDTO, error) {
	if s == nil || s.repo == nil {
		return ConversationDTO{}, ErrServiceDependency
	}
	if s.mentionResponder == nil {
		return ConversationDTO{}, ErrRuntimeUnavailable
	}
	view, err := s.repo.GetConversation(ctx, strings.TrimSpace(conversationID))
	if err != nil {
		return ConversationDTO{}, err
	}
	latest := source.CreatedAt
	runCtx := ctx
	var cancel context.CancelFunc
	opts := discussionMentionDispatchOptions{}
	if onEvent != nil {
		runCtx, cancel = context.WithCancel(ctx)
		defer cancel()
		opts.OnEvent = onEvent
		opts.ActiveRunCancel = cancel
	}
	messages, err := s.respondToDiscussionMentionsFromMessage(runCtx, conversationID, source, mapMissionLinks(view.LinkedMissions), latest, opts, func(message MessageDTO) bool {
		if message.CreatedAt.After(latest) {
			latest = message.CreatedAt
		}
		if onEvent == nil {
			return true
		}
		return onEvent(StreamEventDTO{Kind: StreamEventMessageCompleted, Message: &message})
	})
	if err != nil {
		return ConversationDTO{}, err
	}
	for _, message := range messages {
		if message.CreatedAt.After(latest) {
			latest = message.CreatedAt
		}
	}
	updated, err := s.repo.GetConversation(ctx, strings.TrimSpace(conversationID))
	if err != nil {
		return ConversationDTO{}, err
	}
	updated.Status = latestDirectMentionStatus(updated.Runs)
	updated.UpdatedAt = firstTimeAfter(latest, source.CreatedAt)
	if isCompletedConversationStatus(updated.Status) {
		updated.ViewedAt = updated.UpdatedAt
	}
	if err := s.repo.SaveConversation(ctx, updated.ConversationRecord); err != nil {
		return ConversationDTO{}, err
	}
	updated, err = s.repo.GetConversation(ctx, strings.TrimSpace(conversationID))
	if err != nil {
		return ConversationDTO{}, err
	}
	if err := s.appendRealtimeConversationListInvalidated(ctx, updated, "direct-mention-completed-"+conversationID); err != nil {
		return ConversationDTO{}, err
	}
	dto := mapConversationDTO(updated)
	if onEvent != nil {
		_ = onEvent(StreamEventDTO{Kind: StreamEventSnapshot, Conversation: &dto})
	}
	return dto, nil
}

func latestDirectMentionStatus(runs []RunRecord) string {
	for i := len(runs) - 1; i >= 0; i-- {
		if status := strings.TrimSpace(runs[i].Status); status != "" {
			return status
		}
	}
	return "completed"
}
