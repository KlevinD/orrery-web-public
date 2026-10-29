package main

import (
	"context"
	"encoding/base64"
	"io"
	"strings"

	"orrery_v2/internal/agent/outputpolicy"
	"orrery_v2/internal/app/discussionsession"
	gwprotocol "orrery_v2/internal/gateway/protocol"
)

type gatewayImageDownloads interface {
	DownloadAttachment(context.Context, discussionsession.ViewerRef, string, string) (discussionsession.AttachmentDownloadDTO, error)
}

func (r *productionMessageGatewayRunner) emitGeneratedImage(ctx context.Context, viewer discussionsession.ViewerRef, result map[string]string, out chan<- gwprotocol.Event) bool {
	downloads, ok := r.discussions.(gatewayImageDownloads)
	if !ok {
		return false
	}
	d, err := downloads.DownloadAttachment(ctx, viewer, result["conversation_id"], result["attachment_id"])
	if err != nil {
		return false
	}
	defer d.Reader.Close()
	if d.Attachment.UploadedBy != viewer.ViewerID || !strings.HasPrefix(d.FileName, "generated-") || d.ContentType != "image/png" || d.SizeBytes > maxGeneratedImageBytes {
		return false
	}
	data, err := io.ReadAll(io.LimitReader(d.Reader, maxGeneratedImageBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxGeneratedImageBytes {
		return false
	}
	out <- gwprotocol.Event{Type: gwprotocol.EventAssistantImage, Attachment: &gwprotocol.Attachment{Type: "image", Name: d.FileName, MIMEType: "image/png", Bytes: int64(len(data)), Content: base64.StdEncoding.EncodeToString(data), Metadata: map[string]any{"markdown": result["markdown"]}}}
	return true
}

type gatewayFailureExplainer interface {
	ExplainOperationalFailureStream(context.Context, discussionsession.ViewerRef, discussionsession.ExplainOperationalFailureCommand) (<-chan discussionsession.StreamEventDTO, error)
}

func (r *productionMessageGatewayRunner) explainGatewayImageFailure(ctx context.Context, viewer discussionsession.ViewerRef, conversationID, messageID, key string, out chan<- gwprotocol.Event) {
	explainer, ok := r.discussions.(gatewayFailureExplainer)
	if !ok || conversationID == "" || messageID == "" {
		emitGatewayRuntimeUnavailable(out, "")
		return
	}
	events, err := explainer.ExplainOperationalFailureStream(ctx, viewer, discussionsession.ExplainOperationalFailureCommand{
		ConversationID: conversationID, MessageID: messageID, IdempotencyKey: key + ":image-delivery-explanation",
		Facts: "Image generation completed and the attachment was saved in this conversation. Preparing the generated image for channel delivery failed. The image was not delivered through the channel. Do not generate it again. The user can view the saved attachment in the personal-assistant conversation.",
	})
	if err != nil || events == nil {
		emitGatewayRuntimeUnavailable(out, "")
		return
	}
	var replies []gwprotocol.Event
	failed := false
	for event := range events {
		switch event.Kind {
		case discussionsession.StreamEventMessageCompleted:
			if event.Message != nil && event.Message.Role == "assistant" {
				if text := outputpolicy.ChatText(event.Message.Content); text != "" {
					replies = append(replies, gwprotocol.Event{Type: gwprotocol.EventAssistantTextDelta, Text: text})
				}
			}
		case discussionsession.StreamEventError:
			failed = true
		}
	}
	if failed || len(replies) == 0 {
		emitGatewayRuntimeUnavailable(out, "")
		return
	}
	for _, reply := range replies {
		out <- reply
	}
}
