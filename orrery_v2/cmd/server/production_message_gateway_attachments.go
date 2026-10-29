package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path"
	"strings"

	cloudaudio "orrery_v2/internal/agent/runtime/audio"

	"orrery_v2/internal/app/discussionsession"
	gwprotocol "orrery_v2/internal/gateway/protocol"
)

const maxGatewayInlineImageBytes = 16 << 20

type productionGatewayAttachmentDiscussionService interface {
	StartAttachmentDraft(context.Context, discussionsession.ViewerRef, discussionsession.UploadAttachmentCommand) (discussionsession.AttachmentDraftDTO, error)
	UploadAttachment(context.Context, discussionsession.ViewerRef, discussionsession.UploadAttachmentCommand) (discussionsession.AttachmentDTO, error)
}

type gatewayInlineAttachment struct {
	name      string
	mediaType string
	data      []byte
}

func (r *productionMessageGatewayRunner) prepareGatewayAttachments(
	ctx context.Context,
	viewer discussionsession.ViewerRef,
	cmd discussionsession.SendMessageCommand,
	attachments []gwprotocol.Attachment,
) (discussionsession.SendMessageCommand, error) {
	inline := make([]gatewayInlineAttachment, 0, len(attachments))
	totalBytes := 0
	for index, attachment := range attachments {
		decoded, ok, err := decodeGatewayInlineAttachment(attachment, index)
		if err != nil {
			return discussionsession.SendMessageCommand{}, err
		}
		if ok {
			totalBytes += len(decoded.data)
			if totalBytes > maxGatewayInlineImageBytes {
				return discussionsession.SendMessageCommand{}, errors.New("combined gateway inline images exceed the 16 MiB limit")
			}
			inline = append(inline, decoded)
		}
	}
	if len(inline) == 0 {
		return cmd, nil
	}
	service, ok := r.discussions.(productionGatewayAttachmentDiscussionService)
	if !ok {
		return discussionsession.SendMessageCommand{}, errors.New("discussion attachment service is unavailable")
	}
	attachmentIDs := append([]string(nil), cmd.AttachmentIDs...)
	conversationID := strings.TrimSpace(cmd.ConversationID)
	for index, attachment := range inline {
		upload := discussionsession.UploadAttachmentCommand{
			ConversationID:       conversationID,
			ConversationKind:     cmd.ConversationKind,
			ForceNewConversation: cmd.ForceNewConversation,
			DirectAgentID:        cmd.DirectAgentID,
			DirectTargetKind:     cmd.DirectTargetKind,
			DirectTargetID:       cmd.DirectTargetID,
			FileName:             attachment.name,
			ContentType:          attachment.mediaType,
			SizeBytes:            int64(len(attachment.data)),
			Reader:               bytes.NewReader(attachment.data),
			IdempotencyKey:       fmt.Sprintf("%s:attachment:%d", strings.TrimSpace(cmd.IdempotencyKey), index+1),
		}
		if conversationID == "" {
			upload.ConversationID = ""
			draft, err := service.StartAttachmentDraft(ctx, viewer, upload)
			if err != nil {
				return discussionsession.SendMessageCommand{}, fmt.Errorf("start gateway attachment draft: %w", err)
			}
			conversationID = strings.TrimSpace(draft.Conversation.ConversationID)
			if conversationID == "" || strings.TrimSpace(draft.Attachment.AttachmentID) == "" {
				return discussionsession.SendMessageCommand{}, errors.New("gateway attachment draft returned no identity")
			}
			attachmentIDs = append(attachmentIDs, strings.TrimSpace(draft.Attachment.AttachmentID))
			continue
		}
		upload.ConversationID = conversationID
		upload.ForceNewConversation = false
		stored, err := service.UploadAttachment(ctx, viewer, upload)
		if err != nil {
			return discussionsession.SendMessageCommand{}, fmt.Errorf("upload gateway attachment: %w", err)
		}
		if strings.TrimSpace(stored.AttachmentID) == "" {
			return discussionsession.SendMessageCommand{}, errors.New("gateway attachment upload returned no identity")
		}
		attachmentIDs = append(attachmentIDs, strings.TrimSpace(stored.AttachmentID))
	}
	cmd.ConversationID = conversationID
	cmd.ForceNewConversation = false
	cmd.AttachmentIDs = attachmentIDs
	return cmd, nil
}

func decodeGatewayInlineAttachment(attachment gwprotocol.Attachment, index int) (gatewayInlineAttachment, bool, error) {
	content := strings.TrimSpace(attachment.Content)
	if content == "" {
		return gatewayInlineAttachment{}, false, nil
	}
	isAudio := strings.EqualFold(strings.TrimSpace(attachment.Type), "audio")
	if !strings.EqualFold(strings.TrimSpace(attachment.Type), "image") && !isAudio {
		return gatewayInlineAttachment{}, false, errors.New("gateway inline attachment must be an image")
	}
	encoding, _ := attachment.Metadata["content_encoding"].(string)
	if !strings.EqualFold(strings.TrimSpace(encoding), "base64") {
		return gatewayInlineAttachment{}, false, errors.New("gateway inline image must use base64 encoding")
	}
	if len(content) > base64.StdEncoding.EncodedLen(maxGatewayInlineImageBytes) {
		return gatewayInlineAttachment{}, false, errors.New("gateway inline image exceeds the 16 MiB limit")
	}
	data, err := base64.StdEncoding.Strict().DecodeString(content)
	if err != nil {
		return gatewayInlineAttachment{}, false, errors.New("gateway inline image is not valid base64")
	}
	if len(data) == 0 || len(data) > maxGatewayInlineImageBytes {
		return gatewayInlineAttachment{}, false, errors.New("gateway inline image is empty or exceeds the 16 MiB limit")
	}
	if attachment.Bytes > 0 && attachment.Bytes != int64(len(data)) {
		return gatewayInlineAttachment{}, false, errors.New("gateway inline image size does not match its metadata")
	}
	if isAudio {
		extension, err := cloudaudio.ValidateAudio(data, attachment.MIMEType)
		if err != nil {
			return gatewayInlineAttachment{}, false, err
		}
		return gatewayInlineAttachment{name: fmt.Sprintf("gateway-voice-%d%s", index+1, extension), mediaType: cloudaudio.DetectMIMEType(data), data: data}, true, nil
	}
	declaredType, _, err := mime.ParseMediaType(strings.TrimSpace(attachment.MIMEType))
	if err != nil || !supportedGatewayImageType(declaredType) {
		return gatewayInlineAttachment{}, false, errors.New("gateway inline image has an unsupported media type")
	}
	detectedType := strings.ToLower(strings.TrimSpace(http.DetectContentType(data)))
	if !supportedGatewayImageType(detectedType) || !sameGatewayImageType(declaredType, detectedType) {
		return gatewayInlineAttachment{}, false, errors.New("gateway inline image content does not match its media type")
	}
	name := path.Base(strings.ReplaceAll(strings.TrimSpace(attachment.Name), "\\", "/"))
	if name == "" || name == "." || name == "/" {
		name = fmt.Sprintf("gateway-image-%d%s", index+1, gatewayImageExtension(detectedType))
	}
	return gatewayInlineAttachment{name: name, mediaType: detectedType, data: data}, true, nil
}

func supportedGatewayImageType(mediaType string) bool {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "image/jpeg", "image/jpg", "image/png", "image/gif", "image/webp", "image/bmp":
		return true
	default:
		return false
	}
}

func sameGatewayImageType(left, right string) bool {
	left = strings.ToLower(strings.TrimSpace(left))
	right = strings.ToLower(strings.TrimSpace(right))
	if left == "image/jpg" {
		left = "image/jpeg"
	}
	if right == "image/jpg" {
		right = "image/jpeg"
	}
	return left == right
}

func gatewayImageExtension(mediaType string) string {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/bmp":
		return ".bmp"
	default:
		return ".bin"
	}
}
