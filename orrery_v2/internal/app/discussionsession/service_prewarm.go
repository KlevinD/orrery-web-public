package discussionsession

import (
	"context"
	"strings"
	"time"
)

const discussionPrewarmTimeout = 2 * time.Minute

// PrewarmConversation creates or reuses an empty group draft and starts its
// runtime in the background. Existing discussions are warmed in place.
func (s *Service) PrewarmConversation(ctx context.Context, viewer ViewerRef, cmd PrewarmConversationCommand) (ConversationDTO, error) {
	if s == nil || s.repo == nil {
		return ConversationDTO{}, ErrServiceDependency
	}
	if strings.TrimSpace(viewer.ViewerID) == "" {
		return ConversationDTO{}, ErrInvalidRequest
	}

	conversationID := strings.TrimSpace(cmd.ConversationID)
	if conversationID != "" {
		view, err := s.requireConversationAccess(ctx, viewer, conversationID)
		if err != nil {
			return ConversationDTO{}, err
		}
		s.launchConversationPrewarm(ctx, viewer, view)
		return mapConversationDTO(view), nil
	}
	if strings.TrimSpace(cmd.IdempotencyKey) == "" {
		return ConversationDTO{}, ErrInvalidRequest
	}
	conversationKind, ok := normalizeCreatableConversationKind(cmd.ConversationKind)
	if !ok {
		return ConversationDTO{}, ErrInvalidRequest
	}
	cmd.ConversationKind = conversationKind

	commandRecord, claimed, err := s.claimDiscussionCommand(ctx, "prewarm_conversation", viewer, cmd.IdempotencyKey, cmd)
	if err != nil {
		return ConversationDTO{}, err
	}
	if !claimed {
		return s.replayDiscussionCommand(ctx, commandRecord)
	}
	failCommand := func(cause error) {
		_ = s.failDiscussionCommand(context.WithoutCancel(ctx), commandRecord, cause)
	}
	unlock := s.lockRunLifecycle("prewarm-draft:" + strings.TrimSpace(string(viewer.ViewerKind)) + ":" + strings.TrimSpace(viewer.ViewerID) + ":" + conversationKind)
	defer unlock()

	if draft, found, findErr := s.findReusableConversationDraft(ctx, viewer, conversationKind); findErr != nil {
		failCommand(findErr)
		return ConversationDTO{}, findErr
	} else if found {
		if err := s.completeDiscussionCommand(context.WithoutCancel(ctx), commandRecord, draft.ConversationID, "", ""); err != nil {
			return ConversationDTO{}, err
		}
		s.launchConversationPrewarm(ctx, viewer, draft)
		return mapConversationDTO(draft), nil
	}

	conversationID, err = s.newID()
	if err != nil {
		failCommand(err)
		return ConversationDTO{}, err
	}
	now := s.now()
	title := trimmedDiscussionTitle(cmd.Title)
	if title == "" {
		title = "New discussion"
	}
	conversation, members, err := s.newConversationWithMembers(ctx, conversationID, viewer, SendMessageCommand{Content: title, ConversationKind: conversationKind}, now)
	if err != nil {
		failCommand(err)
		return ConversationDTO{}, err
	}
	conversation.Title = title
	conversation.Status = "draft"
	if creatorName := conversationMemberName(members, "user", viewer.ViewerID); creatorName != "" {
		conversation.CreatedByName = creatorName
	}
	if _, err := s.repo.ApplyTurnMutation(ctx, TurnAggregateMutation{Conversation: conversation, Members: members}); err != nil {
		failCommand(err)
		return ConversationDTO{}, err
	}
	view, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		failCommand(err)
		return ConversationDTO{}, err
	}
	s.writeConversationAuthorization(ctx, conversationID)
	if err := s.completeDiscussionCommand(context.WithoutCancel(ctx), commandRecord, conversationID, "", ""); err != nil {
		return ConversationDTO{}, err
	}
	if err := s.appendRealtimeUserDiscussionListInvalidated(ctx, viewer.ViewerID, conversationID, "discussion-draft-created-"+conversationID); err != nil && s.logger != nil {
		s.logger.Warn(ctx, "discussion draft realtime notification failed", "conversation_id", conversationID, "error", err)
	}
	s.launchConversationPrewarm(ctx, viewer, view)
	return mapConversationDTO(view), nil
}

func (s *Service) findReusableConversationDraft(ctx context.Context, viewer ViewerRef, conversationKind string) (ConversationRecordView, bool, error) {
	records, err := s.repo.ListConversations(ctx)
	if err != nil {
		return ConversationRecordView{}, false, err
	}
	for _, record := range records {
		if !strings.EqualFold(strings.TrimSpace(record.Status), "draft") || normalizedStoredConversationKind(record.ConversationKind) != conversationKind {
			continue
		}
		if len(record.Messages) > 0 || !conversationAccessibleToViewer(record, viewer) || strings.TrimSpace(record.CreatedBy) != strings.TrimSpace(viewer.ViewerID) {
			continue
		}
		view, err := s.repo.GetConversation(ctx, record.ConversationID)
		if err != nil {
			return ConversationRecordView{}, false, err
		}
		return view, true, nil
	}
	return ConversationRecordView{}, false, nil
}

func (s *Service) launchConversationPrewarm(parent context.Context, viewer ViewerRef, view ConversationRecordView) {
	prewarmer, ok := s.discussionAgent.(DiscussionAgentPrewarmer)
	if !ok || strings.EqualFold(strings.TrimSpace(view.ConversationKind), "direct") || strings.TrimSpace(view.ActiveTurnID) != "" {
		return
	}
	if parent == nil {
		parent = context.Background()
	}
	agentID, err := s.configuredRunAgentIDForConversation(parent, view)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), discussionPrewarmTimeout)
	go func() {
		defer cancel()
		startedAt := time.Now()
		request := discussionAgentRequestFromConversation(
			view,
			RunRecord{RunID: "prewarm-" + view.ConversationID, ConversationID: view.ConversationID, AgentID: agentID},
			viewer,
			MessageRecord{MessageID: "prewarm-" + view.ConversationID, ConversationID: view.ConversationID},
			nil,
		)
		request.Mode = "prewarm"
		prewarmErr := s.prepareDiscussionAgentRequest(ctx, &request)
		if prewarmErr == nil {
			prewarmErr = prewarmer.PrewarmDiscussion(ctx, request)
		}
		if prewarmErr != nil {
			if s.logger != nil {
				s.logger.Warn(ctx, "discussion runtime prewarm failed", "conversation_id", view.ConversationID, "agent_id", agentID, "duration_ms", time.Since(startedAt).Milliseconds(), "error", prewarmErr)
			}
			return
		}
		if s.logger != nil {
			s.logger.Debug(ctx, "discussion runtime prewarm completed", "conversation_id", view.ConversationID, "agent_id", agentID, "duration_ms", time.Since(startedAt).Milliseconds())
		}
	}()
}
