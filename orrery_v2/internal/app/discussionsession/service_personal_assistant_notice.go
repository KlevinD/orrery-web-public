package discussionsession

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
)

// AppendPersonalAssistantNotice is an internal notification sink, not a user
// message. The caller resolves an authenticated human and checks source access.
// The first notice owns its content and conversation even after a new chat starts.
func (s *Service) AppendPersonalAssistantNotice(ctx context.Context, viewer ViewerRef, key, content string) error {
	return s.appendPersonalAssistantNotice(ctx, viewer, key, content, false)
}

// AppendPersonalAssistantReply records already-generated public model output.
// Its caller owns source authorization and durable model-output provenance.
func (s *Service) AppendPersonalAssistantReply(ctx context.Context, viewer ViewerRef, key, content string) error {
	return s.appendPersonalAssistantNotice(ctx, viewer, key, content, true)
}

func (s *Service) appendPersonalAssistantNotice(ctx context.Context, viewer ViewerRef, key, content string, reply bool) error {
	if s == nil || s.repo == nil {
		return ErrServiceDependency
	}
	viewer.TenantID, viewer.OwnerID, viewer.ViewerID = strings.TrimSpace(viewer.TenantID), strings.TrimSpace(viewer.OwnerID), strings.TrimSpace(viewer.ViewerID)
	key, content = strings.TrimSpace(key), strings.TrimSpace(content)
	if viewer.ViewerKind != ViewerKindUser || viewer.TenantID == "" || viewer.OwnerID == "" || viewer.ViewerID == "" || key == "" || content == "" {
		return ErrInvalidRequest
	}
	scope := fmt.Sprintf("%x", sha256.Sum256([]byte(viewer.TenantID+"\x00"+viewer.OwnerID)))
	key = scope + ":" + key
	const operation = "personal_assistant_notice"
	command, err := s.repo.GetCommand(ctx, operation, viewer, key)
	if errors.Is(err, ErrNotFound) {
		latest, found, err := s.findLatestPersonalAssistantConversation(ctx, viewer)
		if err != nil {
			return err
		}
		conversationID := "assistant-notices-" + scope[:24]
		if found && latest.TenantID == viewer.TenantID && latest.OwnerID == viewer.OwnerID {
			conversationID = latest.ConversationID
		}
		command, _, err = s.repo.ClaimCommand(ctx, CommandRecord{
			Operation: operation, ViewerID: viewer.ViewerID, ViewerKind: string(viewer.ViewerKind), IdempotencyKey: key,
			Fingerprint: scope, ConversationID: conversationID, Status: discussionCommandPending, CreatedAt: s.now(), UpdatedAt: s.now(),
		})
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if command.Fingerprint != scope || command.ConversationID == "" {
		return ErrForbidden
	}
	// Completed notices need no chat reload. Besides avoiding repeated full
	// history reads, this respects a user's later deletion of that chat.
	if command.Status == discussionCommandCompleted {
		return nil
	}
	unlock := s.lockRunLifecycle(command.ConversationID)
	defer unlock()
	view, err := s.repo.GetConversation(ctx, command.ConversationID)
	if errors.Is(err, ErrNotFound) && command.Status == discussionCommandPending && command.ConversationID == "assistant-notices-"+scope[:24] {
		conversation, members, createErr := s.newConversationWithMembers(ctx, command.ConversationID, viewer, SendMessageCommand{ConversationKind: ConversationKindPersonalAssistant}, s.now())
		if createErr != nil {
			return createErr
		}
		conversation.Status = "completed"
		if _, createErr = s.repo.CreateConversationWithMembers(ctx, conversation, members); createErr != nil && !errors.Is(createErr, ErrRevisionConflict) {
			return createErr
		}
		view, err = s.repo.GetConversation(ctx, command.ConversationID)
		if err == nil {
			s.writeConversationAuthorization(ctx, command.ConversationID)
		}
	}
	if err != nil {
		return err
	}
	if !isPersonalAssistantConversationKind(view.ConversationKind) || view.TenantID != viewer.TenantID || view.OwnerID != viewer.OwnerID || !conversationAccessibleToViewer(view, viewer) {
		return ErrForbidden
	}
	messageID := discussionNoticeMessageID(command.CommandID)
	message := MessageRecord{
		MessageID: messageID, ConversationID: view.ConversationID, Role: "system", AuthorID: "orrery", AuthorName: "Orrery", Content: content,
		RoutingSchemaVersion: 1, Metadata: map[string]string{discussionInterjectionDeliveryMetadataKey: discussionNoticeDelivery, "kind": "personal_assistant_attention"},
	}
	if reply {
		message.Role, message.AuthorID, message.AuthorName = "assistant", "personal-assistant", "AI 助手"
		message.Metadata["kind"] = "scheduled_task_result"
		message.Metadata["content_origin"] = "llm"
	}
	for attempt := 0; attempt < 3; attempt++ {
		if existing, found := findMessageRecord(view.Messages, messageID); found {
			message = existing
			break
		}
		message.CreatedAt = firstTimeAfter(s.now(), latestDiscussionOccurrenceAt(view))
		view.UpdatedAt = message.CreatedAt
		if _, err = s.repo.ApplyTurnMutation(ctx, TurnAggregateMutation{Conversation: view.ConversationRecord, ExpectedRevision: view.Revision, Messages: []MessageRecord{message}}); err == nil {
			break
		}
		if !errors.Is(err, ErrRevisionConflict) || attempt == 2 {
			return err
		}
		view, err = s.repo.GetConversation(ctx, command.ConversationID)
		if err != nil {
			return err
		}
		if view.TenantID != viewer.TenantID || view.OwnerID != viewer.OwnerID || !conversationAccessibleToViewer(view, viewer) {
			return ErrForbidden
		}
	}
	if err := s.publishNoticeRealtime(ctx, viewer, view, message); err != nil {
		return err
	}
	return s.completeDiscussionCommand(ctx, command, view.ConversationID, message.MessageID, "")
}
