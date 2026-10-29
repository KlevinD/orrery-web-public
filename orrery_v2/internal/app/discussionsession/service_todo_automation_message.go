package discussionsession

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
)

const (
	todoAutomationMessageAuthorID   = "orrery-todo-automation"
	todoAutomationMessageAuthorName = "Orrery"
)

// SendTodoAutomationMessage starts an Agent turn from a system-authored prompt
// in a canonical Todo conversation. It cannot be used for ordinary chats.
func (s *Service) SendTodoAutomationMessage(ctx context.Context, viewer ViewerRef, cmd SendTodoAutomationMessageCommand) (ConversationDTO, error) {
	if s == nil || s.repo == nil {
		return ConversationDTO{}, ErrServiceDependency
	}
	viewer.ViewerID = strings.TrimSpace(viewer.ViewerID)
	viewer.TenantID = strings.TrimSpace(viewer.TenantID)
	cmd.ConversationID = strings.TrimSpace(cmd.ConversationID)
	cmd.TodoID = strings.TrimSpace(cmd.TodoID)
	cmd.ManagedSource = strings.TrimSpace(cmd.ManagedSource)
	cmd.Content = strings.TrimSpace(cmd.Content)
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	if viewer.ViewerKind != ViewerKindSystem || viewer.ViewerID == "" || viewer.TenantID == "" || cmd.ConversationID == "" || cmd.TodoID == "" || cmd.Content == "" || cmd.IdempotencyKey == "" {
		return ConversationDTO{}, ErrInvalidRequest
	}
	if !todoManagedSourceMatches(viewer.TenantID, cmd.TodoID, cmd.ManagedSource) {
		return ConversationDTO{}, ErrForbidden
	}
	runtimeCommand := SendMessageCommand{ConversationID: cmd.ConversationID, Content: cmd.Content, Mentions: cmd.Mentions, IdempotencyKey: cmd.IdempotencyKey}
	if err := s.validateSendMessageRuntime(ctx, runtimeCommand, false); err != nil {
		return ConversationDTO{}, err
	}
	command, claimed, claimErr := s.claimDiscussionCommand(ctx, "send_todo_automation_message", viewer, cmd.IdempotencyKey, cmd)
	if claimErr != nil && !errors.Is(claimErr, ErrCommandInProgress) {
		return ConversationDTO{}, claimErr
	}
	if !claimed && claimErr == nil {
		return s.replayDiscussionCommand(ctx, command)
	}

	unlock := s.lockRunLifecycle(cmd.ConversationID)
	defer unlock()
	view, err := s.repo.GetConversation(ctx, cmd.ConversationID)
	if err != nil {
		if claimed {
			_ = s.failDiscussionCommand(context.WithoutCancel(ctx), command, err)
		}
		return ConversationDTO{}, err
	}
	if strings.TrimSpace(view.TenantID) != viewer.TenantID || view.PurposeKind != todoConversationPurposeKind || strings.TrimSpace(view.PurposeID) != cmd.TodoID {
		if claimed {
			_ = s.failDiscussionCommand(context.WithoutCancel(ctx), command, ErrForbidden)
		}
		return ConversationDTO{}, ErrForbidden
	}
	mentions, err := normalizeMessageMentions(cmd.Mentions, view.Members)
	if err != nil {
		if claimed {
			_ = s.failDiscussionCommand(context.WithoutCancel(ctx), command, err)
		}
		return ConversationDTO{}, err
	}

	messageID := todoAutomationMessageID(command.CommandID)
	turnID := stableDiscussionTurnID(DiscussionTurnOriginMessage, cmd.ConversationID, messageID, cmd.IdempotencyKey)
	command.ConversationID = cmd.ConversationID
	command.MessageID = messageID
	command.TurnID = turnID
	command.UpdatedAt = s.now()
	if err := s.repo.SavePendingCommandProgress(context.WithoutCancel(ctx), command); err != nil {
		if errors.Is(err, ErrRevisionConflict) {
			latest, getErr := s.repo.GetCommand(ctx, command.Operation, viewer, cmd.IdempotencyKey)
			if getErr == nil && latest.Status == discussionCommandCompleted {
				return s.replayDiscussionCommand(ctx, latest)
			}
		}
		return ConversationDTO{}, err
	}

	message, exists := findMessageRecord(view.Messages, messageID)
	if exists {
		if message.ConversationID != cmd.ConversationID || message.TurnID != turnID || message.Role != "system" || message.AuthorID != todoAutomationMessageAuthorID || message.Content != cmd.Content {
			return ConversationDTO{}, fmt.Errorf("%w: Todo automation command message identity is inconsistent", ErrServiceDependency)
		}
	} else {
		materializedDraft := strings.EqualFold(strings.TrimSpace(view.Status), "draft")
		if materializedDraft {
			view.Title = trimmedDiscussionTitle(cmd.Content)
			view.Status = "running"
		}
		message = MessageRecord{
			MessageID: messageID, ConversationID: cmd.ConversationID, TurnID: turnID,
			Role: "system", AuthorID: todoAutomationMessageAuthorID, AuthorName: todoAutomationMessageAuthorName,
			Content: cmd.Content, Mentions: mentions, RoutingSchemaVersion: 1,
		}
		for attempt := 0; attempt < 3; attempt++ {
			message.CreatedAt = firstTimeAfter(s.now(), latestDiscussionOccurrenceAt(view))
			view.UpdatedAt = message.CreatedAt
			committed, saveErr := s.repo.ApplyTurnMutation(ctx, TurnAggregateMutation{
				Conversation: view.ConversationRecord, ExpectedRevision: view.Revision,
				Messages: []MessageRecord{message}, RequireNoActiveTurn: true,
			})
			if saveErr == nil {
				view.ConversationRecord = committed
				break
			}
			if !errors.Is(saveErr, ErrRevisionConflict) || attempt == 2 {
				return ConversationDTO{}, saveErr
			}
			view, err = s.repo.GetConversation(ctx, cmd.ConversationID)
			if err != nil {
				return ConversationDTO{}, err
			}
		}
	}
	if err := s.appendRealtimeMessageCreated(ctx, message, viewer, false); err != nil && s.logger != nil {
		s.logger.Warn(ctx, "Todo automation message realtime notification failed", "conversation_id", cmd.ConversationID, "message_id", message.MessageID, "error", err)
	}
	if current, getErr := s.repo.GetConversation(ctx, cmd.ConversationID); getErr == nil {
		view = current
	} else {
		return ConversationDTO{}, getErr
	}
	if err := s.appendRealtimeConversationListInvalidated(ctx, view, "todo-automation-message-"+message.MessageID); err != nil && s.logger != nil {
		s.logger.Warn(ctx, "Todo automation conversation list notification failed", "conversation_id", cmd.ConversationID, "message_id", message.MessageID, "error", err)
	}

	if dto, reconciled, err := s.reconcileTodoAutomationTurn(ctx, command, message); reconciled || err != nil {
		return dto, err
	}
	turn := s.newDiscussionTurnCommand(view, message, viewer, DiscussionTurnOriginMessage, cmd.IdempotencyKey, nil)
	turn.NotificationBestEffort = true
	dto, err := s.executeDiscussionTurn(ctx, turn)
	if err != nil {
		if reconciledDTO, reconciled, reconcileErr := s.reconcileTodoAutomationTurn(context.WithoutCancel(ctx), command, message); reconciled {
			return reconciledDTO, reconcileErr
		}
		return dto, err
	}
	if err := s.completeDiscussionCommand(context.WithoutCancel(ctx), command, cmd.ConversationID, message.MessageID, turn.TurnID); err != nil {
		return ConversationDTO{}, err
	}
	return dto, nil
}

func (s *Service) reconcileTodoAutomationTurn(ctx context.Context, command CommandRecord, message MessageRecord) (ConversationDTO, bool, error) {
	turn, err := s.repo.GetTurn(ctx, command.TurnID)
	if errors.Is(err, ErrNotFound) {
		return ConversationDTO{}, false, nil
	}
	if err != nil {
		return ConversationDTO{}, true, err
	}
	if turn.ConversationID != command.ConversationID || turn.RootMessageID != message.MessageID || turn.IdempotencyKey != command.IdempotencyKey {
		return ConversationDTO{}, true, fmt.Errorf("%w: Todo automation turn identity is inconsistent", ErrServiceDependency)
	}
	switch strings.ToLower(strings.TrimSpace(turn.Status)) {
	case "completed", "failed", "cancelled":
		if err := s.completeDiscussionCommand(context.WithoutCancel(ctx), command, command.ConversationID, message.MessageID, turn.TurnID); err != nil && !errors.Is(err, ErrRevisionConflict) {
			return ConversationDTO{}, true, err
		}
		view, err := s.repo.GetConversation(ctx, command.ConversationID)
		if err != nil {
			return ConversationDTO{}, true, err
		}
		return mapConversationDTO(view), true, nil
	default:
		recovered, recoverErr := s.recoverInterruptedTurn(ctx, command.ConversationID, turn.TurnID, s.now())
		if recoverErr != nil {
			return ConversationDTO{}, true, recoverErr
		}
		if recovered {
			return s.reconcileTodoAutomationTurn(ctx, command, message)
		}
		return ConversationDTO{}, true, ErrCommandInProgress
	}
}

func todoAutomationMessageID(commandID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(commandID)))
	return fmt.Sprintf("todo-automation-%x", sum[:12])
}
