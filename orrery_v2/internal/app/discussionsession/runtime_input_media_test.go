package discussionsession

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestDiscussionRuntimeInputMediaLoadsCurrentImageFromAttachmentWorkspace(t *testing.T) {
	root := t.TempDir()
	attachmentsDir := filepath.Join(root, "attachments")
	if err := os.MkdirAll(attachmentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	imageBytes := discussionTestPNG(t)
	imagePath := filepath.Join(attachmentsDir, "att-1-photo.png")
	if err := os.WriteFile(imagePath, imageBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	blocks, err := discussionRuntimeInputMedia(DiscussionAgentRequest{
		MessageID:   "message-1",
		Workspace:   DiscussionWorkspaceContext{RootDir: root, AttachmentsDir: attachmentsDir},
		Attachments: []AttachmentDTO{{MessageID: "message-1", ContentType: "image/png", StoragePath: imagePath}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || blocks[0].Kind != agentcore.ContentBlockImage || blocks[0].MediaType != "image/png" || !bytes.Equal(blocks[0].Data, imageBytes) {
		t.Fatalf("runtime image blocks = %+v", blocks)
	}
}

func TestServiceStartAttachmentDraftUsesPersonalAssistantConversationKind(t *testing.T) {
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{
		Repository:      repo,
		AttachmentStore: NewLocalAttachmentStore(t.TempDir()),
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("personal-conversation", "image-attachment"),
	})
	viewer := ViewerRef{ViewerID: "human-a", OwnerID: "account-a", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}
	imageBytes := discussionTestPNG(t)
	command := UploadAttachmentCommand{
		ConversationKind:     ConversationKindPersonalAssistant,
		ForceNewConversation: true,
		FileName:             "photo.png",
		ContentType:          "image/png",
		SizeBytes:            int64(len(imageBytes)),
		Reader:               bytes.NewReader(imageBytes),
		IdempotencyKey:       "weixin-image-run:attachment:1",
	}
	draft, err := service.StartAttachmentDraft(context.Background(), viewer, command)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Conversation.ConversationID != "personal-conversation" || draft.Conversation.ConversationKind != ConversationKindPersonalAssistant {
		t.Fatalf("personal assistant draft = %+v", draft.Conversation)
	}
	stored, err := repo.GetConversation(context.Background(), "personal-conversation")
	if err != nil || stored.OwnerID != "account-a" {
		t.Fatalf("stored personal assistant draft = %+v err=%v", stored.ConversationRecord, err)
	}
	boundAttachment, err := repo.GetAttachment(context.Background(), draft.Attachment.AttachmentID)
	if err != nil {
		t.Fatal(err)
	}
	boundAttachment.MessageID = "message-1"
	if _, err := repo.ApplyTurnMutation(context.Background(), TurnAggregateMutation{
		Conversation:     stored.ConversationRecord,
		ExpectedRevision: stored.Revision,
		Attachments:      []AttachmentRecord{boundAttachment},
	}); err != nil {
		t.Fatal(err)
	}
	command.Reader = bytes.NewReader(imageBytes)
	replayed, err := service.StartAttachmentDraft(context.Background(), viewer, command)
	if err != nil || replayed.Conversation.ConversationID != draft.Conversation.ConversationID || replayed.Attachment.AttachmentID != draft.Attachment.AttachmentID {
		t.Fatalf("replayed personal assistant draft = %+v err=%v", replayed, err)
	}
	command.FileName = "different.png"
	command.Reader = bytes.NewReader(imageBytes)
	if _, err := service.StartAttachmentDraft(context.Background(), viewer, command); !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatalf("changed attachment replay error = %v", err)
	}
}

func discussionTestPNG(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{B: 255, A: 255})
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
