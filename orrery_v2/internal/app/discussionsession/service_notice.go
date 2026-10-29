package discussionsession

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
)

const discussionNoticeDelivery = "passive_notice"

// AppendNotice records orchestration status without starting or steering an
// Agent turn. The caller must already be a member of the conversation.
func (s *Service) AppendNotice(ctx context.Context, viewer ViewerRef, cmd AppendNoticeCommand) (ConversationDTO, error) {
	return s.appendNotice(ctx, viewer, cmd, nil)
}

// AppendNoticeWithWorkspace appends the passive result notice and attaches
// files changed since PrepareAgentRunWorkspace captured its pre-run snapshot.
func (s *Service) AppendNoticeWithWorkspace(ctx context.Context, viewer ViewerRef, cmd AppendNoticeCommand, workspace PreparedAgentRunWorkspace) (ConversationDTO, error) {
	if strings.TrimSpace(workspace.ConversationID) == "" || !strings.EqualFold(strings.TrimSpace(workspace.ConversationID), strings.TrimSpace(cmd.ConversationID)) || strings.TrimSpace(workspace.AgentID) == "" {
		return ConversationDTO{}, ErrInvalidRequest
	}
	if viewer.ViewerKind != ViewerKindAgent || !strings.EqualFold(strings.TrimSpace(viewer.ViewerID), strings.TrimSpace(workspace.AgentID)) {
		return ConversationDTO{}, ErrForbidden
	}
	cmd.Metadata = cloneStringMap(cmd.Metadata)
	if cmd.Metadata == nil {
		cmd.Metadata = map[string]string{}
	}
	cmd.Metadata["agent_id"] = workspace.AgentID
	cmd.Metadata["agent_name"] = firstNonBlank(workspace.AgentName, workspace.AgentID)
	return s.appendNotice(ctx, viewer, cmd, &workspace)
}

func (s *Service) appendNotice(ctx context.Context, viewer ViewerRef, cmd AppendNoticeCommand, workspace *PreparedAgentRunWorkspace) (ConversationDTO, error) {
	if s == nil || s.repo == nil {
		return ConversationDTO{}, ErrServiceDependency
	}
	cmd.ConversationID = strings.TrimSpace(cmd.ConversationID)
	cmd.MissionID = strings.TrimSpace(cmd.MissionID)
	cmd.MissionTaskID = strings.TrimSpace(cmd.MissionTaskID)
	cmd.Content = strings.TrimSpace(cmd.Content)
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	cmd.Metadata = cloneStringMap(cmd.Metadata)
	if strings.TrimSpace(viewer.ViewerID) == "" || cmd.ConversationID == "" || cmd.Content == "" || cmd.IdempotencyKey == "" ||
		(cmd.MissionID == "") != (cmd.MissionTaskID == "") {
		return ConversationDTO{}, ErrInvalidRequest
	}

	unlock := s.lockRunLifecycle(cmd.ConversationID)
	defer unlock()
	view, err := s.requireConversationAccess(ctx, viewer, cmd.ConversationID)
	if err != nil {
		return ConversationDTO{}, err
	}
	command, claimed, err := s.claimDiscussionCommand(ctx, "append_notice", viewer, cmd.IdempotencyKey, cmd)
	if err != nil {
		if errors.Is(err, ErrCommandInProgress) {
			return s.reconcilePendingNoticeCommand(ctx, viewer, view, command, workspace)
		}
		return ConversationDTO{}, err
	}
	if !claimed {
		return mapConversationDTO(view), nil
	}
	failCommand := func(cause error) {
		_ = s.failDiscussionCommand(context.WithoutCancel(ctx), command, cause)
	}

	messageID := discussionNoticeMessageID(command.CommandID)
	attachmentIDs, err := s.messageAttachmentIDs(ctx, cmd.ConversationID, messageID, cmd.AttachmentIDs)
	if err != nil {
		failCommand(err)
		return ConversationDTO{}, err
	}
	var attachments []AttachmentRecord
	for _, id := range attachmentIDs {
		attachment, err := s.repo.GetAttachment(ctx, id)
		if err != nil {
			failCommand(err)
			return ConversationDTO{}, err
		}
		attachment.MessageID = messageID
		attachments = append(attachments, attachment)
	}
	metadata := cloneStringMap(cmd.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata[discussionInterjectionDeliveryMetadataKey] = discussionNoticeDelivery
	message := MessageRecord{
		MessageID: messageID, ConversationID: cmd.ConversationID, MissionID: cmd.MissionID, MissionTaskID: cmd.MissionTaskID,
		Role: "system", AuthorID: "orrery", AuthorName: "Orrery", Content: cmd.Content,
		RoutingSchemaVersion: 1, Metadata: metadata,
	}

	for attempt := 0; attempt < 3; attempt++ {
		if existing, ok := findMessageRecord(view.Messages, messageID); ok {
			message = existing
			break
		}
		message.CreatedAt = firstTimeAfter(s.now(), latestDiscussionOccurrenceAt(view))
		view.UpdatedAt = message.CreatedAt
		if _, err = s.repo.ApplyTurnMutation(ctx, TurnAggregateMutation{
			Conversation: view.ConversationRecord, ExpectedRevision: view.Revision, Messages: []MessageRecord{message},
			Attachments: attachments,
		}); err == nil {
			break
		}
		if !errors.Is(err, ErrRevisionConflict) || attempt == 2 {
			failCommand(err)
			return ConversationDTO{}, err
		}
		view, err = s.requireConversationAccess(ctx, viewer, cmd.ConversationID)
		if err != nil {
			failCommand(err)
			return ConversationDTO{}, err
		}
	}
	if workspace != nil {
		s.captureGeneratedWorkspaceFiles(ctx, message, workspace.workspace)
	}
	if err := s.publishNoticeRealtime(ctx, viewer, view, message); err != nil {
		return ConversationDTO{}, err
	}
	if err := s.completeDiscussionCommand(context.WithoutCancel(ctx), command, cmd.ConversationID, message.MessageID, ""); err != nil {
		return ConversationDTO{}, err
	}
	updated, err := s.repo.GetConversation(ctx, cmd.ConversationID)
	if err != nil {
		return ConversationDTO{}, err
	}
	return mapConversationDTO(updated), nil
}

func (s *Service) reconcilePendingNoticeCommand(ctx context.Context, viewer ViewerRef, view ConversationRecordView, command CommandRecord, workspace *PreparedAgentRunWorkspace) (ConversationDTO, error) {
	messageID := discussionNoticeMessageID(command.CommandID)
	message, ok := findMessageRecord(view.Messages, messageID)
	if !ok {
		return ConversationDTO{}, ErrCommandInProgress
	}
	if workspace != nil && !noticeMessageHasAttachments(view.Attachments, messageID) {
		s.captureGeneratedWorkspaceFiles(ctx, message, workspace.workspace)
	}
	if err := s.publishNoticeRealtime(ctx, viewer, view, message); err != nil {
		return ConversationDTO{}, err
	}
	if err := s.completeDiscussionCommand(context.WithoutCancel(ctx), command, view.ConversationID, message.MessageID, ""); err != nil {
		return ConversationDTO{}, err
	}
	updated, err := s.repo.GetConversation(ctx, view.ConversationID)
	if err != nil {
		return ConversationDTO{}, err
	}
	return mapConversationDTO(updated), nil
}

func noticeMessageHasAttachments(records []AttachmentRecord, messageID string) bool {
	for _, record := range records {
		if strings.EqualFold(strings.TrimSpace(record.MessageID), strings.TrimSpace(messageID)) {
			return true
		}
	}
	return false
}

func (s *Service) publishNoticeRealtime(ctx context.Context, viewer ViewerRef, view ConversationRecordView, message MessageRecord) error {
	if err := s.appendRealtimeMessageCreated(ctx, message, viewer, false); err != nil {
		return err
	}
	if err := s.appendRealtimeMessageCompleted(ctx, message); err != nil {
		return err
	}
	if err := s.appendRealtimeSnapshotInvalidated(ctx, message.ConversationID, "notice_appended", message.MessageID); err != nil {
		return err
	}
	return s.appendRealtimeConversationListInvalidated(ctx, view, "notice-appended-"+message.MessageID)
}

func discussionNoticeMessageID(commandID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(commandID)))
	return fmt.Sprintf("notice-%x", sum[:12])
}
