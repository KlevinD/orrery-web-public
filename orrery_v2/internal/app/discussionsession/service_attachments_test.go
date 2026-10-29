package discussionsession

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type workspaceReleasingRuntimeCleaner struct {
	capturingDiscussionRuntimeRecordCleaner
	released []string
}

func (c *workspaceReleasingRuntimeCleaner) ReleaseRuntimeWorkspace(_ context.Context, workspaceRoot string) error {
	c.released = append(c.released, workspaceRoot)
	return c.err
}

func TestServiceUploadAttachmentStoresFileAndListsDownloadMetadata(t *testing.T) {
	repo := newMemoryRepository(t)
	root := t.TempDir()
	service := newTestService(ServiceOptions{
		Repository:      repo,
		AttachmentStore: NewLocalAttachmentStore(root),
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("att-1"),
	})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Title: "Launch", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(ctx, ConversationMemberRecord{ConversationID: "conv-1", MemberKind: "user", MemberID: "user-1", DisplayName: "本地用户", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}

	attachment, err := service.UploadAttachment(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, UploadAttachmentCommand{
		ConversationID: "conv-1",
		FileName:       "../brief.txt",
		ContentType:    "text/plain",
		Reader:         strings.NewReader("hello attachment"),
	})
	if err != nil {
		t.Fatal(err)
	}

	if attachment.AttachmentID != "att-1" || attachment.FileName != "../brief.txt" || attachment.SizeBytes != int64(len("hello attachment")) || attachment.DownloadURL != "/api/discussions/conv-1/attachments/att-1/download" {
		t.Fatalf("attachment = %+v", attachment)
	}
	if !strings.Contains(filepath.ToSlash(attachment.StoragePath), "/conv-1/attachments/att-1-brief.txt") {
		t.Fatalf("storage path = %q", attachment.StoragePath)
	}
	bytes, err := os.ReadFile(attachment.StoragePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(bytes) != "hello attachment" {
		t.Fatalf("stored bytes = %q", bytes)
	}
	page, err := service.ListAttachments(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Attachments) != 1 || page.Attachments[0].AttachmentID != "att-1" {
		t.Fatalf("attachment page = %+v", page)
	}
	download, err := service.DownloadAttachment(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1", "att-1")
	if err != nil {
		t.Fatal(err)
	}
	defer download.Reader.Close()
	body, err := io.ReadAll(download.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello attachment" || download.FileName != "../brief.txt" || download.ContentType != "text/plain" {
		t.Fatalf("download = %+v body=%q", download, body)
	}
}

func TestServiceStartAttachmentDraftCreatesConversationWorkspaceForFirstUpload(t *testing.T) {
	repo := newMemoryRepository(t)
	root := t.TempDir()
	service := newTestService(ServiceOptions{
		Repository:      repo,
		AttachmentStore: NewLocalAttachmentStore(root),
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-draft", "att-1"),
	})
	ctx := context.Background()
	viewer := ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}

	draft, err := service.StartAttachmentDraft(ctx, viewer, UploadAttachmentCommand{
		FileName:       "brief.txt",
		ContentType:    "text/plain",
		Reader:         strings.NewReader("hello draft"),
		IdempotencyKey: "draft-upload-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	if draft.Conversation.ConversationID != "conv-draft" || draft.Conversation.Status != "completed" || draft.Conversation.Title != "brief.txt" {
		t.Fatalf("conversation = %+v", draft.Conversation)
	}
	if got := draft.Conversation.Members; len(got) != 3 || got[2].ID != "local-user" {
		t.Fatalf("draft members = %+v", got)
	}
	if draft.Attachment.AttachmentID != "att-1" || draft.Attachment.ConversationID != "conv-draft" {
		t.Fatalf("attachment = %+v", draft.Attachment)
	}
	if _, err := os.Stat(filepath.Join(root, "conv-draft", "attachments")); err != nil {
		t.Fatalf("draft workspace was not initialized: %v", err)
	}
	stored, err := repo.GetConversation(ctx, "conv-draft")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Messages) != 0 || len(stored.Attachments) != 1 {
		t.Fatalf("stored draft = messages %+v attachments %+v", stored.Messages, stored.Attachments)
	}
}

func TestServiceStartAttachmentDraftCreatesDirectConversationForFirstPrivateUpload(t *testing.T) {
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{
		Repository:      repo,
		AttachmentStore: NewLocalAttachmentStore(t.TempDir()),
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-direct-draft", "att-1"),
	})
	viewer := ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}

	draft, err := service.StartAttachmentDraft(context.Background(), viewer, UploadAttachmentCommand{
		DirectTargetID: "staffing-agent",
		FileName:       "brief.txt",
		Reader:         strings.NewReader("hello direct"),
		IdempotencyKey: "direct-draft-upload-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	if draft.Conversation.ConversationID != "conv-direct-draft" || draft.Conversation.ConversationKind != "direct" {
		t.Fatalf("direct draft conversation = %+v", draft.Conversation)
	}
	if draft.Conversation.DirectTargetKind != "agent" || draft.Conversation.DirectTargetID != "staffing-agent" || draft.Conversation.DirectAgentID != "staffing-agent" {
		t.Fatalf("direct target metadata = kind %q target %q agent %q", draft.Conversation.DirectTargetKind, draft.Conversation.DirectTargetID, draft.Conversation.DirectAgentID)
	}
	if draft.Conversation.Title != "秦若兰" || len(draft.Conversation.Messages) != 0 {
		t.Fatalf("direct draft title/messages = %q %+v", draft.Conversation.Title, draft.Conversation.Messages)
	}
	if draft.Attachment.ConversationID != "conv-direct-draft" {
		t.Fatalf("attachment = %+v", draft.Attachment)
	}
}

func TestServiceStartAttachmentDraftForceNewConversationSkipsExistingAgentSession(t *testing.T) {
	repo := newMemoryRepository(t)
	existing := ConversationRecord{
		ConversationID:   "conv-direct-existing",
		ConversationKind: "direct",
		DirectAgentID:    "staffing-agent",
		CreatedBy:        "local-user",
		CreatedAt:        testTime,
		UpdatedAt:        testTime,
	}
	if err := repo.SaveConversation(context.Background(), existing); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(context.Background(), ConversationMemberRecord{ConversationID: existing.ConversationID, MemberKind: "agent", MemberID: "staffing-agent", DisplayName: "Staffing Agent", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(context.Background(), ConversationMemberRecord{ConversationID: existing.ConversationID, MemberKind: "user", MemberID: "local-user", DisplayName: "Local User", CreatedAt: testTime.Add(time.Nanosecond)}); err != nil {
		t.Fatal(err)
	}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		AttachmentStore: NewLocalAttachmentStore(t.TempDir()),
		Clock:           fixedClock{now: testTime.Add(time.Minute)},
		IDGenerator:     fixedIDGen("conv-direct-new", "att-1"),
	})

	draft, err := service.StartAttachmentDraft(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}, UploadAttachmentCommand{
		ForceNewConversation: true,
		DirectTargetID:       "staffing-agent",
		FileName:             "brief.txt",
		Reader:               strings.NewReader("new session attachment"),
		IdempotencyKey:       "direct-draft-new-session",
	})
	if err != nil {
		t.Fatal(err)
	}

	if draft.Conversation.ConversationID != "conv-direct-new" || draft.Attachment.ConversationID != "conv-direct-new" {
		t.Fatalf("draft = %+v attachment = %+v", draft.Conversation, draft.Attachment)
	}
	oldConversation, err := repo.GetConversation(context.Background(), existing.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(oldConversation.Attachments) != 0 {
		t.Fatalf("existing direct conversation attachments = %+v, want unchanged", oldConversation.Attachments)
	}
}

func TestServiceUploadAttachmentRequiresConversationMember(t *testing.T) {
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{
		Repository:      repo,
		AttachmentStore: NewLocalAttachmentStore(t.TempDir()),
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("att-1"),
	})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Title: "Launch", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}

	_, err := service.UploadAttachment(ctx, ViewerRef{ViewerID: "intruder", ViewerKind: ViewerKindUser}, UploadAttachmentCommand{
		ConversationID: "conv-1",
		FileName:       "brief.txt",
		Reader:         strings.NewReader("nope"),
	})
	if err == nil {
		t.Fatal("expected error for non-member upload")
	}
}

func TestServiceListAttachmentsRequiresConversationMember(t *testing.T) {
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{
		Repository:      repo,
		AttachmentStore: NewLocalAttachmentStore(t.TempDir()),
		Clock:           fixedClock{now: testTime},
	})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Title: "Launch", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(ctx, ConversationMemberRecord{ConversationID: "conv-1", MemberKind: "user", MemberID: "user-1", DisplayName: "本地用户", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveAttachment(ctx, AttachmentRecord{AttachmentID: "att-1", ConversationID: "conv-1", FileName: "brief.txt", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}

	_, err := service.ListAttachments(ctx, ViewerRef{ViewerID: "intruder", ViewerKind: ViewerKindUser}, "conv-1")
	if err == nil {
		t.Fatal("expected error for non-member attachment listing")
	}
}

func TestServiceDeleteConversationRemovesAttachmentWorkspace(t *testing.T) {
	repo := newMemoryRepository(t)
	store := NewLocalAttachmentStore(t.TempDir())
	runtimeCleaner := &workspaceReleasingRuntimeCleaner{}
	service := newTestService(ServiceOptions{Repository: repo, AttachmentStore: store, RuntimeRecordCleaner: runtimeCleaner, Clock: fixedClock{now: testTime}, IDGenerator: fixedIDGen("att-1")})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-delete", Title: "Delete", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(ctx, ConversationMemberRecord{ConversationID: "conv-delete", MemberKind: "user", MemberID: "user-1", DisplayName: "本地用户", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	attachment, err := service.UploadAttachment(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, UploadAttachmentCommand{
		ConversationID: "conv-delete",
		FileName:       "brief.txt",
		Reader:         strings.NewReader("delete me"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(attachment.StoragePath); err != nil {
		t.Fatal(err)
	}

	if err := service.DeleteConversation(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-delete"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(attachment.StoragePath); !os.IsNotExist(err) {
		t.Fatalf("attachment file still exists, stat err=%v", err)
	}
	if len(runtimeCleaner.released) != 1 || runtimeCleaner.released[0] == "" {
		t.Fatalf("released runtime workspaces = %+v", runtimeCleaner.released)
	}
}

func TestServiceSendMessageBindsSelectedAttachmentsToMessageAndAgentRequest(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{
		AssistantMessage: "收到。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	root := t.TempDir()
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		AttachmentStore: NewLocalAttachmentStore(root),
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("msg-user", "run-1", "msg-assistant"),
	})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Title: "Launch", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	for _, member := range []ConversationMemberRecord{
		{ConversationID: "conv-1", MemberKind: "user", MemberID: "user-1", DisplayName: "本地用户", CreatedAt: testTime},
		{ConversationID: "conv-1", MemberKind: "agent", MemberID: "lead-agent", DisplayName: "顾明澈", Role: "Lead", CreatedAt: testTime},
		{ConversationID: "conv-1", MemberKind: "agent", MemberID: "staffing-agent", DisplayName: "秦若兰", Role: "Staffing", CreatedAt: testTime},
	} {
		if err := repo.SaveMember(ctx, member); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.SaveAttachment(ctx, AttachmentRecord{
		AttachmentID:   "att-1",
		ConversationID: "conv-1",
		FileName:       "brief.txt",
		SizeBytes:      12,
		StoragePath:    filepath.Join(root, "conv-1", "attachments", "att-1-brief.txt"),
		CreatedAt:      testTime,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveAttachment(ctx, AttachmentRecord{
		AttachmentID:   "att-old",
		ConversationID: "conv-1",
		MessageID:      "msg-old",
		FileName:       "old.txt",
		SizeBytes:      9,
		StoragePath:    filepath.Join(root, "conv-1", "attachments", "att-old-old.txt"),
		CreatedAt:      testTime,
	}); err != nil {
		t.Fatal(err)
	}

	dto, err := service.SendMessage(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{ConversationID: "conv-1", Content: "继续", AttachmentIDs: []string{"att-1"}, IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	request := runner.request
	if len(request.Attachments) != 1 || request.Attachments[0].AttachmentID != "att-1" {
		t.Fatalf("attachments = %+v", request.Attachments)
	}
	if request.Attachments[0].MessageID != "msg-user" {
		t.Fatalf("request attachment should be bound to current message: %+v", request.Attachments[0])
	}
	if len(dto.Messages) == 0 || len(dto.Messages[0].Attachments) != 1 || dto.Messages[0].Attachments[0].AttachmentID != "att-1" {
		t.Fatalf("user message attachments = %+v", dto.Messages)
	}
	if len(dto.Attachments) != 2 {
		t.Fatalf("conversation should still expose attachment archive, got %+v", dto.Attachments)
	}
	if request.Workspace.RootDir == "" || request.Workspace.AttachmentsDir == "" || request.Workspace.AgentWorkDir == "" {
		t.Fatalf("workspace = %+v", request.Workspace)
	}
	if !strings.HasSuffix(filepath.ToSlash(request.Workspace.AgentWorkDir), "/conv-1/agents/顾明澈") {
		t.Fatalf("agent workdir = %q", request.Workspace.AgentWorkDir)
	}
	if len(request.Workspace.MemberWorkDirs) != 2 {
		t.Fatalf("member workdirs = %+v", request.Workspace.MemberWorkDirs)
	}
	for _, path := range []string{
		request.Workspace.AttachmentsDir,
		filepath.Join(request.Workspace.AgentsDir, "顾明澈"),
		filepath.Join(request.Workspace.AgentsDir, "秦若兰"),
	} {
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Fatalf("workspace path %q not created: info=%+v err=%v", path, info, err)
		}
	}
}

func TestServiceSendMessageDoesNotSendUnboundWorkspaceAttachmentsToAgent(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "收到。", Status: "completed"}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		AttachmentStore: NewLocalAttachmentStore(t.TempDir()),
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("msg-user", "run-1", "msg-assistant"),
	})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Title: "Launch", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	for _, member := range []ConversationMemberRecord{
		{ConversationID: "conv-1", MemberKind: "user", MemberID: "user-1", DisplayName: "本地用户", CreatedAt: testTime},
		{ConversationID: "conv-1", MemberKind: "agent", MemberID: "lead-agent", DisplayName: "顾明澈", Role: "Lead", CreatedAt: testTime},
	} {
		if err := repo.SaveMember(ctx, member); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.SaveAttachment(ctx, AttachmentRecord{AttachmentID: "att-old", ConversationID: "conv-1", FileName: "old.txt", SizeBytes: 9, CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}

	if _, err := service.SendMessage(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{ConversationID: "conv-1", Content: "继续", IdempotencyKey: "send-1"}); err != nil {
		t.Fatal(err)
	}

	if len(runner.request.Attachments) != 0 {
		t.Fatalf("agent request should only include current message attachments, got %+v", runner.request.Attachments)
	}
}

func TestServiceSendMessageRejectsAttachmentFromAnotherConversation(t *testing.T) {
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "收到。", Status: "completed"}},
		AttachmentStore: NewLocalAttachmentStore(t.TempDir()),
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("msg-user", "run-1", "msg-assistant"),
	})
	ctx := context.Background()
	for _, conversationID := range []string{"conv-1", "conv-2"} {
		if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: conversationID, Title: "Launch", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
			t.Fatal(err)
		}
		if err := repo.SaveMember(ctx, ConversationMemberRecord{ConversationID: conversationID, MemberKind: "user", MemberID: "user-1", DisplayName: "本地用户", CreatedAt: testTime}); err != nil {
			t.Fatal(err)
		}
		if err := repo.SaveMember(ctx, ConversationMemberRecord{ConversationID: conversationID, MemberKind: "agent", MemberID: "lead-agent", DisplayName: "顾明澈", CreatedAt: testTime}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.SaveAttachment(ctx, AttachmentRecord{AttachmentID: "att-other", ConversationID: "conv-2", FileName: "other.txt", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}

	_, err := service.SendMessage(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{ConversationID: "conv-1", Content: "继续", AttachmentIDs: []string{"att-other"}, IdempotencyKey: "send-1"})
	if err == nil {
		t.Fatal("expected error for cross-conversation attachment")
	}
}
