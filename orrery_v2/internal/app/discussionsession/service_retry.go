package discussionsession

import (
	"context"
	"strings"
)

type preparedRetryCommand struct {
	Current  ConversationRecordView
	Message  MessageRecord
	Event    EventRecord
	Approved bool
}

func (s *Service) RetryMessage(ctx context.Context, viewer ViewerRef, cmd RetryMessageCommand) (ConversationDTO, error) {
	if err := s.validateRetryMessageCommand(viewer, cmd); err != nil {
		return ConversationDTO{}, err
	}
	commandRecord, claimed, err := s.claimDiscussionCommand(ctx, "retry_message", viewer, cmd.IdempotencyKey, cmd)
	if err != nil {
		return ConversationDTO{}, err
	}
	if !claimed {
		return s.replayDiscussionCommand(ctx, commandRecord)
	}
	failCommand := func(cause error) {
		_ = s.failDiscussionCommand(context.WithoutCancel(ctx), commandRecord, cause)
	}
	prepared, err := s.prepareRetryCommand(ctx, viewer, cmd, false)
	if err != nil {
		failCommand(err)
		return ConversationDTO{}, err
	}
	if !prepared.Approved {
		dto, err := s.finishPendingRetry(ctx, prepared)
		if err != nil {
			failCommand(err)
			return ConversationDTO{}, err
		}
		if err := s.completeDiscussionCommand(context.WithoutCancel(ctx), commandRecord, dto.ConversationID, prepared.Message.MessageID, ""); err != nil {
			return ConversationDTO{}, err
		}
		return dto, nil
	}

	turn, unlock, err := s.prepareRetryTurn(ctx, viewer, cmd, prepared, nil)
	if err != nil {
		failCommand(err)
		return ConversationDTO{}, err
	}
	defer unlock()
	dto, err := s.executeDiscussionTurn(ctx, turn)
	if err != nil {
		failCommand(err)
		return dto, err
	}
	if err := s.completeDiscussionCommand(context.WithoutCancel(ctx), commandRecord, turn.ConversationID, prepared.Message.MessageID, turn.TurnID); err != nil {
		return ConversationDTO{}, err
	}
	return dto, nil
}

func (s *Service) RetryMessageStream(ctx context.Context, viewer ViewerRef, cmd RetryMessageCommand) (<-chan StreamEventDTO, error) {
	if err := s.validateRetryMessageCommand(viewer, cmd); err != nil {
		return nil, err
	}
	commandRecord, claimed, err := s.claimDiscussionCommand(ctx, "retry_message", viewer, cmd.IdempotencyKey, cmd)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return s.replayDiscussionCommandStream(ctx, commandRecord)
	}
	prepared, err := s.prepareRetryCommand(ctx, viewer, cmd, true)
	if err != nil {
		_ = s.failDiscussionCommand(ctx, commandRecord, err)
		return nil, err
	}
	if !prepared.Approved {
		dto, err := s.finishPendingRetry(ctx, prepared)
		if err != nil {
			_ = s.failDiscussionCommand(ctx, commandRecord, err)
			return nil, err
		}
		if err := s.completeDiscussionCommand(context.WithoutCancel(ctx), commandRecord, dto.ConversationID, prepared.Message.MessageID, ""); err != nil {
			return nil, err
		}
		out := make(chan StreamEventDTO, 1)
		out <- StreamEventDTO{
			Kind:           StreamEventSnapshot,
			EventID:        "discussion-retry-pending-" + prepared.Event.EventID,
			ConversationID: dto.ConversationID,
			Revision:       dto.Revision,
			Conversation:   &dto,
		}
		close(out)
		return out, nil
	}

	turn, unlock, err := s.prepareRetryTurn(ctx, viewer, cmd, prepared, nil)
	if err != nil {
		_ = s.failDiscussionCommand(ctx, commandRecord, err)
		return nil, err
	}
	initial, err := s.repo.GetConversation(ctx, turn.ConversationID)
	if err != nil {
		unlock()
		_ = s.failDiscussionCommand(ctx, commandRecord, err)
		return nil, err
	}
	return s.executeDiscussionTurnStream(ctx, commandRecord, prepared.Message, turn, initial, unlock), nil
}

func (s *Service) validateRetryMessageCommand(viewer ViewerRef, cmd RetryMessageCommand) error {
	if s == nil || s.repo == nil {
		return ErrServiceDependency
	}
	if strings.TrimSpace(viewer.ViewerID) == "" || strings.TrimSpace(cmd.ConversationID) == "" || strings.TrimSpace(cmd.MessageID) == "" || strings.TrimSpace(cmd.IdempotencyKey) == "" {
		return ErrInvalidRequest
	}
	return nil
}

func (s *Service) prepareRetryCommand(ctx context.Context, viewer ViewerRef, cmd RetryMessageCommand, streaming bool) (preparedRetryCommand, error) {
	current, err := s.requireConversationAccess(ctx, viewer, cmd.ConversationID)
	if err != nil {
		return preparedRetryCommand{}, err
	}
	message, ok := findMessageRecord(current.Messages, strings.TrimSpace(cmd.MessageID))
	if !ok || message.Role != "user" {
		return preparedRetryCommand{}, ErrNotFound
	}
	if err := s.validateRetryRuntime(ctx, current, message, streaming); err != nil {
		return preparedRetryCommand{}, err
	}
	if message.AuthorID != viewer.ViewerID && current.CreatedBy != viewer.ViewerID {
		return preparedRetryCommand{}, ErrForbidden
	}
	if cmd.Approve && current.CreatedBy != viewer.ViewerID {
		return preparedRetryCommand{}, ErrForbidden
	}

	requiresApproval := message.AuthorID != current.CreatedBy
	approved := !requiresApproval
	var event EventRecord
	if cmd.Approve {
		pending, ok := findPendingRetryEvent(current.Events, message.MessageID)
		if !ok {
			return preparedRetryCommand{}, ErrNotFound
		}
		event, err = s.approveRetryEvent(ctx, pending, viewer)
		approved = err == nil
	} else {
		if message.AuthorID != viewer.ViewerID {
			return preparedRetryCommand{}, ErrForbidden
		}
		event, err = s.saveRetryEvent(ctx, current.ConversationID, message, viewer, cmd.IdempotencyKey, requiresApproval, approved)
	}
	if err != nil {
		return preparedRetryCommand{}, err
	}
	return preparedRetryCommand{Current: current, Message: message, Event: event, Approved: approved}, nil
}

func (s *Service) finishPendingRetry(ctx context.Context, prepared preparedRetryCommand) (ConversationDTO, error) {
	updated, err := s.repo.GetConversation(ctx, prepared.Current.ConversationID)
	if err != nil {
		return ConversationDTO{}, err
	}
	updated.Status = "retry_pending"
	updated.UpdatedAt = prepared.Event.CreatedAt
	if err := s.repo.SaveConversation(ctx, updated.ConversationRecord); err != nil {
		return ConversationDTO{}, err
	}
	updated, err = s.repo.GetConversation(ctx, updated.ConversationID)
	if err != nil {
		return ConversationDTO{}, err
	}
	return mapConversationDTO(updated), nil
}

func (s *Service) prepareRetryTurn(ctx context.Context, viewer ViewerRef, cmd RetryMessageCommand, prepared preparedRetryCommand, onEvent func(StreamEventDTO) bool) (DiscussionTurnCommand, func(), error) {
	// Retry is the fencing operation for an in-flight turn. It must invalidate
	// the old runs before waiting on any ordinary-turn execution lock, because a
	// provider may ignore cancellation indefinitely. Persisted run invalidation
	// prevents that stale execution from committing after rollback.
	release := s.beginRetryLifecycle(prepared.Current.ConversationID)
	s.invalidateRunsAfterOrAtMessage(prepared.Current, prepared.Message)
	if err := s.rollbackAfterMessageLocked(ctx, viewer, prepared.Current, prepared.Message); err != nil {
		release()
		return DiscussionTurnCommand{}, nil, err
	}
	reloaded, err := s.repo.GetConversation(ctx, prepared.Current.ConversationID)
	if err != nil {
		release()
		return DiscussionTurnCommand{}, nil, err
	}
	turn := s.newDiscussionTurnCommand(reloaded, prepared.Message, viewer, DiscussionTurnOriginRetry, cmd.IdempotencyKey, onEvent)
	return turn, release, nil
}

func (s *Service) validateRetryRuntime(_ context.Context, current ConversationRecordView, message MessageRecord, streaming bool) error {
	plan := resolveDiscussionTurnTargets(current, message, s.discussionAgentID())
	if plan.Mode == discussionTurnTargetDirectAgent && len(plan.Members) == 0 {
		return ErrInvalidRequest
	}
	if plan.requiresMentionResponder() {
		if s.mentionResponder == nil {
			return ErrRuntimeUnavailable
		}
		return nil
	}
	if !plan.requiresDiscussionAgent() {
		return nil
	}
	if streaming {
		_, ok := s.discussionAgent.(DiscussionAgentStreamRunner)
		if s.discussionAgent == nil || !ok {
			return s.runtimeUnavailableError()
		}
	} else if s.discussionAgent == nil {
		return s.runtimeUnavailableError()
	}
	if _, err := s.configuredRunAgentID(); err != nil {
		return err
	}
	return nil
}

func (s *Service) saveRetryEvent(ctx context.Context, conversationID string, message MessageRecord, viewer ViewerRef, idempotencyKey string, requiresApproval bool, approved bool) (EventRecord, error) {
	eventID, err := s.newID()
	if err != nil {
		return EventRecord{}, err
	}
	now := s.now()
	status := "pending"
	approvedBy := ""
	approvedAt := ""
	if approved {
		status = "approved"
		approvedBy = viewer.ViewerID
		approvedAt = formatTime(now)
	}
	event := EventRecord{
		EventID:        eventID,
		ConversationID: conversationID,
		Kind:           "message_retry_requested",
		Status:         status,
		Summary:        "Retry user message " + message.MessageID,
		Metadata: map[string]string{
			"request_id":        strings.TrimSpace(idempotencyKey),
			"message_id":        message.MessageID,
			"requested_by":      viewer.ViewerID,
			"requires_approval": boolString(requiresApproval),
			"approved_by":       approvedBy,
			"approved_at":       approvedAt,
		},
		CreatedAt: now,
	}
	if err := s.repo.SaveEvent(ctx, event); err != nil {
		return EventRecord{}, err
	}
	return event, nil
}

func (s *Service) approveRetryEvent(ctx context.Context, event EventRecord, viewer ViewerRef) (EventRecord, error) {
	if event.Metadata == nil {
		event.Metadata = map[string]string{}
	}
	now := s.now()
	event.Status = "approved"
	event.Metadata["approved_by"] = viewer.ViewerID
	event.Metadata["approved_at"] = formatTime(now)
	if err := s.repo.SaveEvent(ctx, event); err != nil {
		return EventRecord{}, err
	}
	return event, nil
}
