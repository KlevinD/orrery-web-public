package discussionsession

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type recoveredSendMessagePlan struct {
	Command   CommandRecord
	Initial   ConversationRecordView
	Source    MessageRecord
	Turn      DiscussionTurnCommand
	Release   func()
	Completed bool
}

func (s *Service) savePendingSendMessageProgress(ctx context.Context, command *CommandRecord, conversationID string, source MessageRecord) error {
	if command == nil {
		return ErrInvalidRequest
	}
	command.ConversationID = strings.TrimSpace(conversationID)
	command.MessageID = strings.TrimSpace(source.MessageID)
	command.TurnID = strings.TrimSpace(source.TurnID)
	command.UpdatedAt = s.now()
	return s.repo.SavePendingCommandProgress(contextWithoutCancellation(ctx), *command)
}

func (s *Service) prepareRecoveredSendMessage(ctx context.Context, viewer ViewerRef, cmd SendMessageCommand, command CommandRecord, streaming bool) (recoveredSendMessagePlan, bool, error) {
	current, source, persistedTurn, turnExists, found, err := s.locateRecoveredSendMessage(ctx, viewer, cmd, &command)
	if err != nil || !found {
		return recoveredSendMessagePlan{}, found, err
	}
	if !turnExists {
		release := s.lockRunLifecycle(current.ConversationID)
		latest, err := s.requireConversationAccess(ctx, viewer, current.ConversationID)
		if err != nil {
			release()
			return recoveredSendMessagePlan{}, true, err
		}
		turn := s.newDiscussionTurnCommand(latest, source, viewer, DiscussionTurnOriginMessage, cmd.IdempotencyKey, nil)
		command.TurnID = turn.TurnID
		command.UpdatedAt = s.now()
		if err := s.repo.SavePendingCommandProgress(contextWithoutCancellation(ctx), command); err != nil {
			release()
			return recoveredSendMessagePlan{}, true, err
		}
		return recoveredSendMessagePlan{Command: command, Initial: latest, Source: source, Turn: turn, Release: release}, true, nil
	}

	activeTurn := false
	switch strings.ToLower(strings.TrimSpace(persistedTurn.Status)) {
	case "completed":
		completed, err := s.completeRecoveredSendMessageCommand(ctx, command, current)
		return completed, true, err
	case "failed", "cancelled":
		// A terminal interrupted attempt is retried below using the same source message.
	case "running", "pending":
		activeTurn = true
		if !s.discussionExecutionLeasesEnabled() {
			lastActivity := firstTime(persistedTurn.UpdatedAt, persistedTurn.StartedAt, persistedTurn.CreatedAt)
			if !lastActivity.IsZero() && lastActivity.After(s.now().Add(-s.turnRecoveryTimeout)) {
				return recoveredSendMessagePlan{}, true, ErrCommandInProgress
			}
		}
	default:
		return recoveredSendMessagePlan{}, true, ErrCommandInProgress
	}

	if err := s.validateRetryRuntime(ctx, current, source, streaming); err != nil {
		return recoveredSendMessagePlan{}, true, err
	}
	release := s.beginRetryLifecycle(current.ConversationID)
	s.invalidateRunsAfterOrAtMessage(current, source)
	if activeTurn {
		recovered, err := s.recoverInterruptedTurn(ctx, current.ConversationID, persistedTurn.TurnID, s.now())
		if err != nil {
			release()
			return recoveredSendMessagePlan{}, true, err
		}
		if !recovered {
			latestTurn, getErr := s.repo.GetTurn(ctx, persistedTurn.TurnID)
			if getErr == nil && strings.EqualFold(strings.TrimSpace(latestTurn.Status), "completed") {
				release()
				latest, viewErr := s.requireConversationAccess(ctx, viewer, current.ConversationID)
				if viewErr != nil {
					return recoveredSendMessagePlan{}, true, viewErr
				}
				completed, completeErr := s.completeRecoveredSendMessageCommand(ctx, command, latest)
				return completed, true, completeErr
			}
			release()
			if getErr != nil {
				return recoveredSendMessagePlan{}, true, getErr
			}
			return recoveredSendMessagePlan{}, true, ErrCommandInProgress
		}
	}

	latest, err := s.requireConversationAccess(ctx, viewer, current.ConversationID)
	if err != nil {
		release()
		return recoveredSendMessagePlan{}, true, err
	}
	source, ok := findMessageRecord(latest.Messages, source.MessageID)
	if !ok {
		release()
		return recoveredSendMessagePlan{}, true, ErrNotFound
	}
	if err := s.rollbackAfterMessageLocked(ctx, viewer, latest, source); err != nil {
		release()
		return recoveredSendMessagePlan{}, true, err
	}
	reloaded, err := s.requireConversationAccess(ctx, viewer, current.ConversationID)
	if err != nil {
		release()
		return recoveredSendMessagePlan{}, true, err
	}
	turn := s.newDiscussionTurnCommand(reloaded, source, viewer, DiscussionTurnOriginRetry, sendMessageRecoveryIdempotencyKey(command), nil)
	command.TurnID = turn.TurnID
	command.UpdatedAt = s.now()
	if err := s.repo.SavePendingCommandProgress(contextWithoutCancellation(ctx), command); err != nil {
		release()
		return recoveredSendMessagePlan{}, true, err
	}
	return recoveredSendMessagePlan{Command: command, Initial: reloaded, Source: source, Turn: turn, Release: release}, true, nil
}

func (s *Service) locateRecoveredSendMessage(ctx context.Context, viewer ViewerRef, cmd SendMessageCommand, command *CommandRecord) (ConversationRecordView, MessageRecord, TurnRecord, bool, bool, error) {
	if command == nil {
		return ConversationRecordView{}, MessageRecord{}, TurnRecord{}, false, false, ErrInvalidRequest
	}
	hasConversation := strings.TrimSpace(command.ConversationID) != ""
	hasMessage := strings.TrimSpace(command.MessageID) != ""
	hasTurn := strings.TrimSpace(command.TurnID) != ""
	if (hasConversation || hasMessage || hasTurn) && !(hasConversation && hasMessage && hasTurn) {
		return ConversationRecordView{}, MessageRecord{}, TurnRecord{}, false, true, fmt.Errorf("%w: pending send-message progress is incomplete", ErrServiceDependency)
	}
	if hasConversation {
		current, source, turn, exists, err := s.loadRecoveredSendMessage(ctx, viewer, cmd, *command)
		return current, source, turn, exists, true, err
	}

	turns, err := s.repo.findTurnsByIdempotencyKey(ctx, command.IdempotencyKey)
	if err != nil {
		return ConversationRecordView{}, MessageRecord{}, TurnRecord{}, false, false, err
	}
	for _, turn := range turns {
		candidate := *command
		candidate.ConversationID = strings.TrimSpace(turn.ConversationID)
		candidate.MessageID = strings.TrimSpace(turn.RootMessageID)
		candidate.TurnID = strings.TrimSpace(turn.TurnID)
		current, source, loadedTurn, exists, loadErr := s.loadRecoveredSendMessage(ctx, viewer, cmd, candidate)
		if errors.Is(loadErr, ErrNotFound) || errors.Is(loadErr, ErrForbidden) || errors.Is(loadErr, ErrIdempotencyMismatch) {
			continue
		}
		if loadErr != nil {
			return ConversationRecordView{}, MessageRecord{}, TurnRecord{}, false, false, loadErr
		}
		candidate.UpdatedAt = s.now()
		if err := s.repo.SavePendingCommandProgress(contextWithoutCancellation(ctx), candidate); err != nil {
			return ConversationRecordView{}, MessageRecord{}, TurnRecord{}, false, true, err
		}
		*command = candidate
		return current, source, loadedTurn, exists, true, nil
	}
	return ConversationRecordView{}, MessageRecord{}, TurnRecord{}, false, false, nil
}

func (s *Service) matchesLegacyPersonalAssistantSendMessage(ctx context.Context, viewer ViewerRef, cmd SendMessageCommand, command CommandRecord) (bool, error) {
	conversationKind, ok := normalizeCreatableConversationKind(cmd.ConversationKind)
	if !ok || conversationKind != ConversationKindPersonalAssistant || strings.TrimSpace(cmd.ConversationID) != "" || cmd.ForceNewConversation {
		return false, nil
	}

	hasConversation := strings.TrimSpace(command.ConversationID) != ""
	hasMessage := strings.TrimSpace(command.MessageID) != ""
	hasTurn := strings.TrimSpace(command.TurnID) != ""
	candidates := make([]CommandRecord, 0, 1)
	if hasConversation || hasMessage || hasTurn {
		if !(hasConversation && hasMessage && hasTurn) {
			return false, nil
		}
		candidates = append(candidates, command)
	} else {
		turns, err := s.repo.findTurnsByIdempotencyKey(ctx, command.IdempotencyKey)
		if err != nil {
			return false, err
		}
		for _, turn := range turns {
			candidate := command
			candidate.ConversationID = strings.TrimSpace(turn.ConversationID)
			candidate.MessageID = strings.TrimSpace(turn.RootMessageID)
			candidate.TurnID = strings.TrimSpace(turn.TurnID)
			candidates = append(candidates, candidate)
		}
	}

	for _, candidate := range candidates {
		current, _, _, _, err := s.loadRecoveredSendMessage(ctx, viewer, cmd, candidate)
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) || errors.Is(err, ErrIdempotencyMismatch) {
			continue
		}
		if err != nil {
			return false, err
		}
		fingerprint, err := legacyPersonalAssistantSendMessageFingerprint(cmd, current.ConversationID)
		if err != nil {
			return false, err
		}
		if fingerprint == strings.TrimSpace(command.Fingerprint) {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) loadRecoveredSendMessage(ctx context.Context, viewer ViewerRef, cmd SendMessageCommand, command CommandRecord) (ConversationRecordView, MessageRecord, TurnRecord, bool, error) {
	if strings.TrimSpace(cmd.ConversationID) != "" && strings.TrimSpace(cmd.ConversationID) != strings.TrimSpace(command.ConversationID) {
		return ConversationRecordView{}, MessageRecord{}, TurnRecord{}, false, ErrIdempotencyMismatch
	}
	current, err := s.requireConversationAccess(ctx, viewer, command.ConversationID)
	if err != nil {
		return ConversationRecordView{}, MessageRecord{}, TurnRecord{}, false, err
	}
	if strings.TrimSpace(cmd.ConversationKind) != "" {
		conversationKind, ok := normalizeCreatableConversationKind(cmd.ConversationKind)
		if !ok || normalizedStoredConversationKind(current.ConversationKind) != conversationKind {
			return ConversationRecordView{}, MessageRecord{}, TurnRecord{}, false, ErrIdempotencyMismatch
		}
	}
	source, ok := findMessageRecord(current.Messages, command.MessageID)
	if !ok {
		return ConversationRecordView{}, MessageRecord{}, TurnRecord{}, false, ErrNotFound
	}
	if !strings.EqualFold(strings.TrimSpace(source.Role), "user") || strings.TrimSpace(source.AuthorID) != strings.TrimSpace(viewer.ViewerID) || strings.TrimSpace(source.Content) != strings.TrimSpace(cmd.Content) {
		return ConversationRecordView{}, MessageRecord{}, TurnRecord{}, false, ErrIdempotencyMismatch
	}
	turn, err := s.repo.GetTurn(ctx, command.TurnID)
	if errors.Is(err, ErrNotFound) {
		return current, source, TurnRecord{}, false, nil
	}
	if err != nil {
		return ConversationRecordView{}, MessageRecord{}, TurnRecord{}, false, err
	}
	turnIdempotencyKey := strings.TrimSpace(turn.IdempotencyKey)
	if strings.TrimSpace(turn.ConversationID) != strings.TrimSpace(command.ConversationID) || strings.TrimSpace(turn.RootMessageID) != strings.TrimSpace(command.MessageID) ||
		(turnIdempotencyKey != strings.TrimSpace(command.IdempotencyKey) && turnIdempotencyKey != sendMessageRecoveryIdempotencyKey(command)) {
		return ConversationRecordView{}, MessageRecord{}, TurnRecord{}, false, ErrIdempotencyMismatch
	}
	return current, source, turn, true, nil
}

func (s *Service) completeRecoveredSendMessageCommand(ctx context.Context, command CommandRecord, current ConversationRecordView) (recoveredSendMessagePlan, error) {
	transitionErr := s.completeDiscussionCommand(contextWithoutCancellation(ctx), command, command.ConversationID, command.MessageID, command.TurnID)
	if transitionErr != nil && !errors.Is(transitionErr, ErrRevisionConflict) {
		return recoveredSendMessagePlan{}, transitionErr
	}
	latest, err := s.repo.GetCommand(ctx, command.Operation, ViewerRef{ViewerID: command.ViewerID, ViewerKind: ViewerKind(command.ViewerKind)}, command.IdempotencyKey)
	if err != nil {
		return recoveredSendMessagePlan{}, err
	}
	if latest.Status != discussionCommandCompleted {
		return recoveredSendMessagePlan{}, ErrRevisionConflict
	}
	command = latest
	return recoveredSendMessagePlan{Command: command, Initial: current, Completed: true}, nil
}

func sendMessageRecoveryIdempotencyKey(command CommandRecord) string {
	return "send-message-recovery-" + strings.TrimSpace(command.CommandID)
}
