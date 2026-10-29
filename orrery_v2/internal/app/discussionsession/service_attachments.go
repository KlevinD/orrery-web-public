package discussionsession

import (
	"context"
	"errors"
	"strings"
)

func (s *Service) ListAttachments(ctx context.Context, viewer ViewerRef, conversationID string) (AttachmentPageDTO, error) {
	if s == nil || s.repo == nil {
		return AttachmentPageDTO{}, ErrServiceDependency
	}
	conversationID = strings.TrimSpace(conversationID)
	if strings.TrimSpace(viewer.ViewerID) == "" || conversationID == "" {
		return AttachmentPageDTO{}, ErrInvalidRequest
	}
	_, err := s.requireConversationAccess(ctx, viewer, conversationID)
	if err != nil {
		return AttachmentPageDTO{}, err
	}
	attachments, err := s.repo.ListAttachments(ctx, conversationID)
	if err != nil {
		return AttachmentPageDTO{}, err
	}
	return AttachmentPageDTO{Attachments: mapAttachments(attachments)}, nil
}

func (s *Service) UploadAttachment(ctx context.Context, viewer ViewerRef, cmd UploadAttachmentCommand) (AttachmentDTO, error) {
	if s == nil || s.repo == nil || s.attachments == nil {
		return AttachmentDTO{}, ErrServiceDependency
	}
	conversationID := strings.TrimSpace(cmd.ConversationID)
	fileName := strings.TrimSpace(cmd.FileName)
	if strings.TrimSpace(viewer.ViewerID) == "" || conversationID == "" || fileName == "" || cmd.Reader == nil {
		return AttachmentDTO{}, ErrInvalidRequest
	}
	view, err := s.requireConversationAccess(ctx, viewer, conversationID)
	if err != nil {
		return AttachmentDTO{}, err
	}
	if replay, found, err := s.findAttachmentUploadReplay(ctx, viewer, conversationID, cmd); err != nil {
		return AttachmentDTO{}, err
	} else if found {
		return mapAttachment(replay), nil
	}
	attachmentID, err := s.newID()
	if err != nil {
		return AttachmentDTO{}, err
	}
	workspaceRootDir := strings.TrimSpace(view.WorkspaceRootDir)
	if _, err := s.validateConversationWorkspaceRootAllowed(ctx, workspaceRootDir); err != nil {
		return AttachmentDTO{}, err
	}
	stored, err := s.attachments.SaveAttachment(ctx, conversationID, workspaceRootDir, attachmentID, fileName, cmd.Reader)
	if err != nil {
		return AttachmentDTO{}, err
	}
	size := stored.Size
	if size == 0 && cmd.SizeBytes > 0 {
		size = cmd.SizeBytes
	}
	now := s.now()
	record := AttachmentRecord{
		AttachmentID:     attachmentID,
		ConversationID:   conversationID,
		FileName:         fileName,
		ContentType:      strings.TrimSpace(cmd.ContentType),
		SizeBytes:        size,
		UploadedBy:       strings.TrimSpace(viewer.ViewerID),
		UploadedByKind:   string(viewer.ViewerKind),
		UploadedByName:   viewerDisplayName(viewer),
		StoragePath:      stored.Path,
		WorkspaceRootDir: workspaceRootDir,
		IdempotencyKey:   strings.TrimSpace(cmd.IdempotencyKey),
		CreatedAt:        now,
	}
	if err := s.repo.SaveAttachment(ctx, record); err != nil {
		return AttachmentDTO{}, err
	}
	view.UpdatedAt = firstTimeAfter(now, view.UpdatedAt)
	if err := s.repo.SaveConversation(ctx, view.ConversationRecord); err != nil {
		return AttachmentDTO{}, err
	}
	if err := s.appendRealtimeSnapshotInvalidated(ctx, conversationID, "attachment-uploaded", "attachment-"+attachmentID); err != nil {
		return AttachmentDTO{}, err
	}
	if updated, err := s.repo.GetConversation(ctx, conversationID); err == nil {
		if err := s.appendRealtimeConversationListInvalidated(ctx, updated, "attachment-"+attachmentID); err != nil {
			return AttachmentDTO{}, err
		}
	}
	return mapAttachment(record), nil
}

func (s *Service) StartAttachmentDraft(ctx context.Context, viewer ViewerRef, cmd UploadAttachmentCommand) (AttachmentDraftDTO, error) {
	if s == nil || s.repo == nil || s.attachments == nil {
		return AttachmentDraftDTO{}, ErrServiceDependency
	}
	fileName := strings.TrimSpace(cmd.FileName)
	if strings.TrimSpace(viewer.ViewerID) == "" || fileName == "" || cmd.Reader == nil || strings.TrimSpace(cmd.ConversationID) != "" {
		return AttachmentDraftDTO{}, ErrInvalidRequest
	}
	conversationKind := strings.TrimSpace(cmd.ConversationKind)
	if conversationKind != "" {
		if _, ok := normalizeCreatableConversationKind(conversationKind); !ok {
			return AttachmentDraftDTO{}, ErrInvalidRequest
		}
	}
	conversationID := ""
	target := directTargetFromCommand(SendMessageCommand{
		DirectAgentID:    cmd.DirectAgentID,
		DirectTargetKind: cmd.DirectTargetKind,
		DirectTargetID:   cmd.DirectTargetID,
	})
	if isPersonalAssistantConversationKind(conversationKind) && target.ID != "" {
		return AttachmentDraftDTO{}, ErrInvalidRequest
	}
	if target.ID != "" && target.Kind == "" {
		resolved, err := s.resolveDirectConversationTargetRef(ctx, target.ID)
		if err != nil {
			return AttachmentDraftDTO{}, err
		}
		target = resolved
	}
	if replay, found, err := s.findAttachmentDraftReplay(ctx, viewer, cmd, target); err != nil {
		return AttachmentDraftDTO{}, err
	} else if found {
		return replay, nil
	}
	if isPersonalAssistantConversationKind(conversationKind) {
		if !cmd.ForceNewConversation {
			existing, found, err := s.findLatestPersonalAssistantConversation(ctx, viewer)
			if err != nil {
				return AttachmentDraftDTO{}, err
			}
			if found {
				conversationID = existing.ConversationID
			}
		}
	} else if target.ID != "" && !cmd.ForceNewConversation {
		existing, found, err := s.findExistingDirectConversation(ctx, viewer, target)
		if err != nil {
			return AttachmentDraftDTO{}, err
		}
		if found {
			conversationID = existing.ConversationID
		}
	}
	if conversationID == "" {
		id, err := s.newID()
		if err != nil {
			return AttachmentDraftDTO{}, err
		}
		now := s.now()
		conversationID = id
		conversation, members, err := s.newConversationWithMembers(ctx, conversationID, viewer, SendMessageCommand{
			Content:          fileName,
			ConversationKind: conversationKind,
			DirectAgentID:    cmd.DirectAgentID,
			DirectTargetKind: cmd.DirectTargetKind,
			DirectTargetID:   cmd.DirectTargetID,
		}, now)
		if err != nil {
			return AttachmentDraftDTO{}, err
		}
		conversation.Status = "completed"
		conversation.UpdatedAt = now
		conversation.ViewedAt = now
		if creatorName := conversationMemberName(members, "user", viewer.ViewerID); creatorName != "" {
			conversation.CreatedByName = creatorName
		}
		if err := s.repo.SaveConversation(ctx, conversation); err != nil {
			return AttachmentDraftDTO{}, err
		}
		if err := s.saveConversationMembers(ctx, members); err != nil {
			return AttachmentDraftDTO{}, err
		}
	}
	attachment, err := s.UploadAttachment(ctx, viewer, UploadAttachmentCommand{
		ConversationID: conversationID,
		FileName:       cmd.FileName,
		ContentType:    cmd.ContentType,
		SizeBytes:      cmd.SizeBytes,
		Reader:         cmd.Reader,
		IdempotencyKey: cmd.IdempotencyKey,
	})
	if err != nil {
		return AttachmentDraftDTO{}, err
	}
	updated, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return AttachmentDraftDTO{}, err
	}
	return AttachmentDraftDTO{Conversation: mapConversationDTO(updated), Attachment: attachment}, nil
}

func (s *Service) findAttachmentUploadReplay(ctx context.Context, viewer ViewerRef, conversationID string, cmd UploadAttachmentCommand) (AttachmentRecord, bool, error) {
	key := strings.TrimSpace(cmd.IdempotencyKey)
	if key == "" {
		return AttachmentRecord{}, false, nil
	}
	candidates, err := s.repo.listAttachmentsByIdempotencyKey(ctx, key)
	if err != nil {
		return AttachmentRecord{}, false, err
	}
	for _, candidate := range candidates {
		if candidate.ConversationID != conversationID || candidate.UploadedBy != strings.TrimSpace(viewer.ViewerID) {
			continue
		}
		if !attachmentReplayMetadataMatches(candidate, cmd) {
			return AttachmentRecord{}, false, ErrIdempotencyMismatch
		}
		return candidate, true, nil
	}
	return AttachmentRecord{}, false, nil
}

func (s *Service) findAttachmentDraftReplay(ctx context.Context, viewer ViewerRef, cmd UploadAttachmentCommand, target directConversationTargetRef) (AttachmentDraftDTO, bool, error) {
	key := strings.TrimSpace(cmd.IdempotencyKey)
	if key == "" {
		return AttachmentDraftDTO{}, false, nil
	}
	candidates, err := s.repo.listAttachmentsByIdempotencyKey(ctx, key)
	if err != nil {
		return AttachmentDraftDTO{}, false, err
	}
	for _, candidate := range candidates {
		if candidate.UploadedBy != strings.TrimSpace(viewer.ViewerID) {
			continue
		}
		view, accessErr := s.requireConversationAccess(ctx, viewer, candidate.ConversationID)
		if accessErr != nil {
			if errors.Is(accessErr, ErrForbidden) || errors.Is(accessErr, ErrNotFound) {
				continue
			}
			return AttachmentDraftDTO{}, false, accessErr
		}
		if normalizedStoredConversationKind(view.ConversationKind) != attachmentDraftConversationKind(cmd.ConversationKind, target) {
			continue
		}
		if target.ID != "" && (!strings.EqualFold(strings.TrimSpace(view.DirectTargetKind), strings.TrimSpace(target.Kind)) || !strings.EqualFold(strings.TrimSpace(view.DirectTargetID), strings.TrimSpace(target.ID))) {
			continue
		}
		if !attachmentReplayMetadataMatches(candidate, cmd) {
			return AttachmentDraftDTO{}, false, ErrIdempotencyMismatch
		}
		return AttachmentDraftDTO{Conversation: mapConversationDTO(view), Attachment: mapAttachment(candidate)}, true, nil
	}
	return AttachmentDraftDTO{}, false, nil
}

func attachmentDraftConversationKind(value string, target directConversationTargetRef) string {
	if isPersonalAssistantConversationKind(value) {
		return ConversationKindPersonalAssistant
	}
	if strings.TrimSpace(target.ID) != "" {
		return ConversationKindDirect
	}
	return ConversationKindGroup
}

func attachmentReplayMetadataMatches(record AttachmentRecord, cmd UploadAttachmentCommand) bool {
	if strings.TrimSpace(record.FileName) != strings.TrimSpace(cmd.FileName) || !strings.EqualFold(strings.TrimSpace(record.ContentType), strings.TrimSpace(cmd.ContentType)) {
		return false
	}
	return cmd.SizeBytes <= 0 || record.SizeBytes == cmd.SizeBytes
}

func (s *Service) DownloadAttachment(ctx context.Context, viewer ViewerRef, conversationID string, attachmentID string) (AttachmentDownloadDTO, error) {
	if s == nil || s.repo == nil || s.attachments == nil {
		return AttachmentDownloadDTO{}, ErrServiceDependency
	}
	conversationID = strings.TrimSpace(conversationID)
	attachmentID = strings.TrimSpace(attachmentID)
	if strings.TrimSpace(viewer.ViewerID) == "" || conversationID == "" || attachmentID == "" {
		return AttachmentDownloadDTO{}, ErrInvalidRequest
	}
	_, err := s.requireConversationAccess(ctx, viewer, conversationID)
	if err != nil {
		return AttachmentDownloadDTO{}, err
	}
	record, err := s.repo.GetAttachment(ctx, attachmentID)
	if err != nil {
		return AttachmentDownloadDTO{}, err
	}
	if strings.TrimSpace(record.ConversationID) != conversationID {
		return AttachmentDownloadDTO{}, ErrNotFound
	}
	workspaceRootDir := attachmentWorkspaceRootDir(record)
	if _, err := s.validateConversationWorkspaceRootAllowed(ctx, workspaceRootDir); err != nil {
		return AttachmentDownloadDTO{}, err
	}
	reader, err := s.attachments.OpenAttachment(ctx, record, workspaceRootDir)
	if err != nil {
		return AttachmentDownloadDTO{}, err
	}
	dto := mapAttachment(record)
	return AttachmentDownloadDTO{
		Attachment:  dto,
		Reader:      reader,
		FileName:    dto.FileName,
		ContentType: dto.ContentType,
		SizeBytes:   dto.SizeBytes,
	}, nil
}

func attachmentWorkspaceRootDir(record AttachmentRecord) string {
	return strings.TrimSpace(record.WorkspaceRootDir)
}
