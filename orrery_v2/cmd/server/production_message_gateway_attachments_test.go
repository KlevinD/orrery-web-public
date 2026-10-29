package main

import (
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"testing"

	"orrery_v2/internal/app/discussionsession"
	gwprotocol "orrery_v2/internal/gateway/protocol"
)

type fakeProductionGatewayAttachmentService struct {
	fakeProductionGatewayDiscussionService
	draftCommand discussionsession.UploadAttachmentCommand
	draftBytes   []byte
}

func (f *fakeProductionGatewayAttachmentService) StartAttachmentDraft(_ context.Context, _ discussionsession.ViewerRef, cmd discussionsession.UploadAttachmentCommand) (discussionsession.AttachmentDraftDTO, error) {
	f.draftCommand = cmd
	f.draftBytes, _ = io.ReadAll(cmd.Reader)
	return discussionsession.AttachmentDraftDTO{
		Conversation: discussionsession.ConversationDTO{ConversationID: "personal-conversation"},
		Attachment:   discussionsession.AttachmentDTO{AttachmentID: "image-attachment", ConversationID: "personal-conversation"},
	}, nil
}

func (f *fakeProductionGatewayAttachmentService) UploadAttachment(_ context.Context, _ discussionsession.ViewerRef, _ discussionsession.UploadAttachmentCommand) (discussionsession.AttachmentDTO, error) {
	return discussionsession.AttachmentDTO{}, nil
}

func TestProductionGatewayPersistsInlineImageInPersonalAssistantConversation(t *testing.T) {
	imageBytes := gatewayTestPNG(t)
	service := &fakeProductionGatewayAttachmentService{}
	runner := newProductionMessageGatewayRunner(service)
	cmd := discussionsession.SendMessageCommand{
		ConversationKind:     discussionsession.ConversationKindPersonalAssistant,
		ForceNewConversation: true,
		IdempotencyKey:       "weixin-image-run",
	}
	prepared, err := runner.prepareGatewayAttachments(context.Background(), discussionsession.ViewerRef{ViewerID: "human-a"}, cmd, []gwprotocol.Attachment{{
		Type: "image", Name: "photo.png", MIMEType: "image/png", Bytes: int64(len(imageBytes)),
		Content: base64.StdEncoding.EncodeToString(imageBytes), Metadata: map[string]any{"content_encoding": "base64"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.ConversationID != "personal-conversation" || prepared.ForceNewConversation || len(prepared.AttachmentIDs) != 1 || prepared.AttachmentIDs[0] != "image-attachment" {
		t.Fatalf("prepared command = %+v", prepared)
	}
	if service.draftCommand.ConversationKind != discussionsession.ConversationKindPersonalAssistant || !service.draftCommand.ForceNewConversation || service.draftCommand.ContentType != "image/png" {
		t.Fatalf("draft command = %+v", service.draftCommand)
	}
	if string(service.draftBytes) != string(imageBytes) {
		t.Fatal("stored gateway image bytes do not match")
	}
}

func TestProductionGatewayRejectsSpoofedInlineImage(t *testing.T) {
	_, _, err := decodeGatewayInlineAttachment(gwprotocol.Attachment{
		Type: "image", MIMEType: "image/png", Content: base64.StdEncoding.EncodeToString([]byte("not an image")),
		Metadata: map[string]any{"content_encoding": "base64"},
	}, 0)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("spoofed image error = %v", err)
	}
}

func gatewayTestPNG(t *testing.T) []byte {
	t.Helper()
	var out strings.Builder
	writer := base64.NewEncoder(base64.StdEncoding, &out)
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{G: 255, A: 255})
	if err := png.Encode(writer, img); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := base64.StdEncoding.DecodeString(out.String())
	if err != nil {
		t.Fatal(err)
	}
	return data
}
