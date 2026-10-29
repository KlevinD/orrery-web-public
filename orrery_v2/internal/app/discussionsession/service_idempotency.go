package discussionsession

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	discussionCommandPending   = "pending"
	discussionCommandCompleted = "completed"
	discussionCommandFailed    = "failed"
	maxSendMessageRetries      = 2
)

func (s *Service) claimDiscussionCommand(ctx context.Context, operation string, viewer ViewerRef, idempotencyKey string, payload any) (CommandRecord, bool, error) {
	if s == nil || s.repo == nil {
		return CommandRecord{}, false, ErrServiceDependency
	}
	fingerprint, err := discussionCommandFingerprint(operation, payload)
	if err != nil {
		return CommandRecord{}, false, err
	}
	now := s.now()
	command := CommandRecord{
		Operation:      strings.TrimSpace(operation),
		ViewerID:       strings.TrimSpace(viewer.ViewerID),
		ViewerKind:     strings.TrimSpace(string(viewer.ViewerKind)),
		IdempotencyKey: strings.TrimSpace(idempotencyKey),
		Fingerprint:    fingerprint,
		Status:         discussionCommandPending,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	existing, claimed, err := s.repo.ClaimCommand(ctx, command)
	if err != nil {
		return CommandRecord{}, false, err
	}
	if claimed {
		return existing, true, nil
	}
	if existing.Fingerprint != fingerprint {
		return existing, false, ErrIdempotencyMismatch
	}
	switch existing.Status {
	case discussionCommandCompleted:
		return existing, false, nil
	case discussionCommandPending:
		return existing, false, ErrCommandInProgress
	case discussionCommandFailed:
		return existing, false, replayDiscussionCommandError(existing)
	default:
		return existing, false, fmt.Errorf("%w: unsupported command status %q", ErrRevisionConflict, existing.Status)
	}
}

func (s *Service) claimSendMessageCommand(ctx context.Context, viewer ViewerRef, cmd SendMessageCommand) (CommandRecord, bool, bool, func(), error) {
	noopRelease := func() {}
	command, claimed, err := s.claimDiscussionCommand(ctx, "send_message", viewer, cmd.IdempotencyKey, cmd)
	if errors.Is(err, ErrIdempotencyMismatch) {
		compatible, compatibilityErr := s.matchesLegacyPersonalAssistantSendMessage(ctx, viewer, cmd, command)
		if compatibilityErr != nil {
			return CommandRecord{}, false, false, noopRelease, compatibilityErr
		}
		if !compatible {
			return command, claimed, false, noopRelease, err
		}
		switch command.Status {
		case discussionCommandCompleted:
			return command, false, false, noopRelease, nil
		case discussionCommandPending:
			// Continue below and take the same stale-command lease used by current fingerprints.
		case discussionCommandFailed:
			if !retryableSendMessageCommand(command) {
				return command, false, false, noopRelease, replayDiscussionCommandError(command)
			}
		default:
			return command, false, false, noopRelease, fmt.Errorf("%w: unsupported command status %q", ErrRevisionConflict, command.Status)
		}
	} else if err == nil {
		if !claimed {
			return command, false, false, noopRelease, nil
		}
		release, acquired, leaseErr := s.acquireCommandExecutionLease(ctx, command.CommandID)
		if leaseErr != nil {
			return CommandRecord{}, false, false, noopRelease, leaseErr
		}
		if !acquired {
			return command, false, false, noopRelease, ErrCommandInProgress
		}
		return command, true, false, release, nil
	} else if !errors.Is(err, ErrCommandInProgress) && !(command.Status == discussionCommandFailed && retryableSendMessageCommand(command)) {
		return command, claimed, false, noopRelease, err
	}
	release, acquired, leaseErr := s.acquireCommandExecutionLease(ctx, command.CommandID)
	if leaseErr != nil {
		return CommandRecord{}, false, false, noopRelease, leaseErr
	}
	if !acquired {
		return command, false, false, noopRelease, ErrCommandInProgress
	}
	keepLease := false
	defer func() {
		if !keepLease {
			release()
		}
	}()
	now := s.now()
	if command.Status == discussionCommandFailed {
		retried, ok, retryErr := s.repo.RetryFailedCommand(ctx, command, maxSendMessageRetries, now)
		if retryErr != nil {
			return CommandRecord{}, false, false, noopRelease, retryErr
		}
		if ok {
			keepLease = true
			return retried, true, true, release, nil
		}
		if retried.Status == discussionCommandCompleted {
			return retried, false, false, noopRelease, nil
		}
		if retried.Status == discussionCommandFailed {
			return retried, false, false, noopRelease, replayDiscussionCommandError(retried)
		}
		command = retried
	}
	staleBefore := now.Add(-s.turnRecoveryTimeout)
	if s.discussionExecutionLeasesEnabled() {
		staleBefore = now
	}
	reclaimed, ok, reclaimErr := s.repo.ReclaimStalePendingCommand(ctx, command, staleBefore, now)
	if reclaimErr != nil {
		return CommandRecord{}, false, false, noopRelease, reclaimErr
	}
	if ok {
		keepLease = true
		return reclaimed, true, true, release, nil
	}
	if reclaimed.Status == discussionCommandCompleted {
		return reclaimed, false, false, noopRelease, nil
	}
	switch reclaimed.Status {
	case discussionCommandPending:
		return reclaimed, false, false, noopRelease, ErrCommandInProgress
	case discussionCommandFailed:
		return reclaimed, false, false, noopRelease, replayDiscussionCommandError(reclaimed)
	default:
		return reclaimed, false, false, noopRelease, fmt.Errorf("%w: unsupported command status %q", ErrRevisionConflict, reclaimed.Status)
	}
}

func retryableSendMessageCommand(command CommandRecord) bool {
	if command.Status != discussionCommandFailed || command.RetryCount >= maxSendMessageRetries {
		return false
	}
	switch command.ErrorCode {
	case "runtime_unavailable", "runtime_execution", "deadline_exceeded", "internal":
		return true
	default:
		return false
	}
}

func (s *Service) completeDiscussionCommand(ctx context.Context, command CommandRecord, conversationID, messageID, turnID string) error {
	if s == nil || s.repo == nil {
		return ErrServiceDependency
	}
	command.Status = discussionCommandCompleted
	command.ConversationID = strings.TrimSpace(conversationID)
	command.MessageID = strings.TrimSpace(messageID)
	command.TurnID = strings.TrimSpace(turnID)
	command.ErrorCode = ""
	command.ErrorMessage = ""
	command.UpdatedAt = s.now()
	return s.repo.TransitionCommand(ctx, command, discussionCommandPending)
}

func (s *Service) failDiscussionCommand(ctx context.Context, command CommandRecord, cause error) error {
	if s == nil || s.repo == nil || command.CommandID == "" {
		return nil
	}
	command.Status = discussionCommandFailed
	command.ErrorCode = discussionCommandErrorCode(cause)
	command.ErrorMessage = discussionCommandErrorMessage(cause)
	command.UpdatedAt = s.now()
	return s.repo.TransitionCommand(contextWithoutCancellation(ctx), command, discussionCommandPending)
}

func (s *Service) replayDiscussionCommand(ctx context.Context, command CommandRecord) (ConversationDTO, error) {
	if strings.TrimSpace(command.ConversationID) == "" {
		return ConversationDTO{}, ErrNotFound
	}
	view, err := s.repo.GetConversation(ctx, command.ConversationID)
	if err != nil {
		return ConversationDTO{}, err
	}
	return mapConversationDTO(view), nil
}

func (s *Service) replayDiscussionCommandStream(ctx context.Context, command CommandRecord) (<-chan StreamEventDTO, error) {
	dto, err := s.replayDiscussionCommand(ctx, command)
	if err != nil {
		return nil, err
	}
	out := make(chan StreamEventDTO, len(dto.Messages)+4)
	out <- StreamEventDTO{
		Kind:           StreamEventSnapshot,
		EventID:        "command-replay-snapshot-" + command.CommandID,
		TurnID:         command.TurnID,
		ConversationID: command.ConversationID,
		Revision:       dto.Revision,
		Conversation:   &dto,
	}
	for i := range dto.Messages {
		message := dto.Messages[i]
		if !strings.EqualFold(strings.TrimSpace(message.Role), "assistant") || strings.TrimSpace(message.TurnID) != strings.TrimSpace(command.TurnID) {
			continue
		}
		out <- StreamEventDTO{
			Kind: StreamEventMessageCompleted, EventID: "command-replay-message-" + message.MessageID,
			TurnID: command.TurnID, ConversationID: command.ConversationID, Revision: dto.Revision,
			RunID: message.RunID, Message: &message,
		}
	}
	for i := len(dto.Runs) - 1; i >= 0; i-- {
		run := dto.Runs[i]
		if strings.TrimSpace(run.TurnID) != strings.TrimSpace(command.TurnID) {
			continue
		}
		out <- StreamEventDTO{
			Kind: StreamEventRunCompleted, EventID: "command-replay-run-" + run.RunID,
			TurnID: command.TurnID, ConversationID: command.ConversationID, Revision: dto.Revision,
			RunID: run.RunID, Run: &run,
		}
		break
	}
	out <- StreamEventDTO{
		Kind:           StreamEventTurnCompleted,
		EventID:        "command-replay-completed-" + command.CommandID,
		TurnID:         command.TurnID,
		ConversationID: command.ConversationID,
		Revision:       dto.Revision,
	}
	close(out)
	return out, nil
}

func discussionCommandFingerprint(operation string, payload any) (string, error) {
	raw, err := json.Marshal(struct {
		Operation string `json:"operation"`
		Payload   any    `json:"payload"`
	}{Operation: strings.TrimSpace(operation), Payload: payload})
	if err != nil {
		return "", fmt.Errorf("encode discussion command fingerprint: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// legacySendMessageCommandV1 is the command shape used before explicit
// personal-assistant session rotation was added. Field order is significant
// because command fingerprints hash the JSON representation.
type legacySendMessageCommandV1 struct {
	ConversationID       string
	ConversationKind     string
	MissionID            string
	MissionTaskID        string
	Content              string
	DirectAgentID        string
	DirectTargetKind     string
	DirectTargetID       string
	InitialMemberIDs     []string
	AllowMemberAdditions *bool
	AttachmentIDs        []string
	Mentions             []MessageMentionDTO
	IdempotencyKey       string
}

func legacyPersonalAssistantSendMessageFingerprint(cmd SendMessageCommand, conversationID string) (string, error) {
	return discussionCommandFingerprint("send_message", legacySendMessageCommandV1{
		ConversationID:       strings.TrimSpace(conversationID),
		ConversationKind:     cmd.ConversationKind,
		MissionID:            cmd.MissionID,
		MissionTaskID:        cmd.MissionTaskID,
		Content:              cmd.Content,
		DirectAgentID:        cmd.DirectAgentID,
		DirectTargetKind:     cmd.DirectTargetKind,
		DirectTargetID:       cmd.DirectTargetID,
		InitialMemberIDs:     cmd.InitialMemberIDs,
		AllowMemberAdditions: cmd.AllowMemberAdditions,
		AttachmentIDs:        cmd.AttachmentIDs,
		Mentions:             cmd.Mentions,
		IdempotencyKey:       cmd.IdempotencyKey,
	})
}

func replayDiscussionCommandError(command CommandRecord) error {
	message := strings.TrimSpace(command.ErrorMessage)
	switch command.ErrorCode {
	case "invalid_request":
		return fmt.Errorf("%w: %s", ErrInvalidRequest, message)
	case "forbidden":
		return fmt.Errorf("%w: %s", ErrForbidden, message)
	case "runtime_unavailable":
		return fmt.Errorf("%w: %s", ErrRuntimeUnavailable, message)
	case "runtime_execution":
		return fmt.Errorf("%w: %s", ErrRuntimeExecution, message)
	case "run_not_active":
		return fmt.Errorf("%w: %s", ErrRunNotActive, message)
	case "runtime_control":
		return fmt.Errorf("%w: %s", ErrRuntimeControl, message)
	default:
		return fmt.Errorf("%w: previous command failed: %s", ErrRuntimeExecution, message)
	}
}

func discussionCommandErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrInvalidRequest):
		return "invalid_request"
	case errors.Is(err, ErrForbidden):
		return "forbidden"
	case errors.Is(err, ErrRuntimeUnavailable):
		return "runtime_unavailable"
	case errors.Is(err, ErrRuntimeExecution):
		return "runtime_execution"
	case errors.Is(err, ErrRunNotActive):
		return "run_not_active"
	case errors.Is(err, ErrRuntimeControl):
		return "runtime_control"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	default:
		return "internal"
	}
}

func discussionCommandErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}

func contextWithoutCancellation(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}
