package discussionsession

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceSetConversationWorkspaceUsesServerDirectoryForRuntimeWorkspace(t *testing.T) {
	repo := newMemoryRepository(t)
	serverWorkspace := filepath.Join(t.TempDir(), "server-workspace")
	runner := &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "收到。", Status: "completed", RuntimeKind: "managed"}}
	service := newTestService(ServiceOptions{
		Repository:         repo,
		DiscussionAgent:    runner,
		AttachmentStore:    NewLocalAttachmentStore(t.TempDir()),
		WorkspaceAllowlist: staticWorkspaceAllowlist{serverWorkspace},
		Clock:              fixedClock{now: testTime},
		IDGenerator:        fixedIDGen("msg-user", "run-1", "msg-assistant"),
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

	updated, err := service.SetConversationWorkspace(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SetConversationWorkspaceCommand{
		ConversationID:   "conv-1",
		WorkspaceRootDir: serverWorkspace,
		IdempotencyKey:   "workspace-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.WorkspaceRootDir != serverWorkspace {
		t.Fatalf("workspace root = %q", updated.WorkspaceRootDir)
	}

	if _, err := service.SendMessage(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{ConversationID: "conv-1", Content: "继续", IdempotencyKey: "send-1"}); err != nil {
		t.Fatal(err)
	}
	workspace := runner.request.Workspace
	if workspace.RootDir != serverWorkspace {
		t.Fatalf("request workspace = %+v", workspace)
	}
	wantManaged := filepath.Join(serverWorkspace, ".orrery", "discussions", "conv-1")
	if workspace.AttachmentsDir != filepath.Join(wantManaged, "attachments") ||
		workspace.AgentsDir != filepath.Join(wantManaged, "agents") ||
		!strings.HasSuffix(filepath.ToSlash(workspace.AgentWorkDir), "/.orrery/discussions/conv-1/agents/顾明澈") {
		t.Fatalf("request workspace = %+v", workspace)
	}
	for _, path := range []string{workspace.RootDir, workspace.AttachmentsDir, workspace.AgentsDir, workspace.AgentWorkDir} {
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Fatalf("workspace path %q not created: info=%+v err=%v", path, info, err)
		}
	}
}

func TestServiceSetConversationWorkspaceRejectsRelativeDirectory(t *testing.T) {
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{Repository: repo, Clock: fixedClock{now: testTime}})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Title: "Launch", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(ctx, ConversationMemberRecord{ConversationID: "conv-1", MemberKind: "user", MemberID: "user-1", DisplayName: "本地用户", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}

	_, err := service.SetConversationWorkspace(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SetConversationWorkspaceCommand{
		ConversationID:   "conv-1",
		WorkspaceRootDir: "relative/path",
	})
	if err == nil {
		t.Fatal("expected relative workspace root to be rejected")
	}
}

func TestServiceSetConversationWorkspaceRejectsDirectoryOutsideAllowlist(t *testing.T) {
	repo := newMemoryRepository(t)
	allowed := filepath.Join(t.TempDir(), "allowed")
	outside := filepath.Join(t.TempDir(), "outside")
	service := newTestService(ServiceOptions{
		Repository:         repo,
		WorkspaceAllowlist: staticWorkspaceAllowlist{allowed},
		Clock:              fixedClock{now: testTime},
	})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Title: "Launch", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(ctx, ConversationMemberRecord{ConversationID: "conv-1", MemberKind: "user", MemberID: "user-1", DisplayName: "本地用户", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}

	_, err := service.SetConversationWorkspace(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SetConversationWorkspaceCommand{
		ConversationID:   "conv-1",
		WorkspaceRootDir: outside,
	})
	if err == nil {
		t.Fatal("expected workspace root outside allowlist to be rejected")
	}
	if _, statErr := os.Stat(outside); !os.IsNotExist(statErr) {
		t.Fatalf("outside workspace should not be created: %v", statErr)
	}
}

func TestApplyRequestedMessageWorkspaceAllowsSystemAutomation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repair", "batch-1")
	service := newTestService(ServiceOptions{
		Repository:         newMemoryRepository(t),
		WorkspaceAllowlist: staticWorkspaceAllowlist{filepath.Dir(filepath.Dir(root))},
		Clock:              fixedClock{now: testTime},
	})
	conversation := &ConversationRecord{ConversationID: "conv-1"}
	if err := service.applyRequestedMessageWorkspace(context.Background(), ViewerRef{ViewerID: "repair", ViewerKind: ViewerKindSystem}, SendMessageCommand{WorkspaceRootDir: root}, conversation); err != nil {
		t.Fatal(err)
	}
	if conversation.WorkspaceRootDir != root {
		t.Fatalf("workspace root = %q, want %q", conversation.WorkspaceRootDir, root)
	}
	for _, path := range []string{root, filepath.Join(root, ".orrery", "discussions")} {
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Fatalf("workspace path %q not created: info=%+v err=%v", path, info, err)
		}
	}
}

func TestApplyRequestedMessageWorkspaceRejectsNonSystemViewer(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repair")
	service := newTestService(ServiceOptions{
		Repository:         newMemoryRepository(t),
		WorkspaceAllowlist: staticWorkspaceAllowlist{filepath.Dir(root)},
		Clock:              fixedClock{now: testTime},
	})
	conversation := &ConversationRecord{ConversationID: "conv-1"}
	err := service.applyRequestedMessageWorkspace(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{WorkspaceRootDir: root}, conversation)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
	if conversation.WorkspaceRootDir != "" {
		t.Fatalf("workspace root mutated after rejection: %q", conversation.WorkspaceRootDir)
	}
}

func TestApplyRequestedMessageWorkspaceRejectsChangingExistingRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repair")
	other := filepath.Join(base, "other")
	service := newTestService(ServiceOptions{
		Repository:         newMemoryRepository(t),
		WorkspaceAllowlist: staticWorkspaceAllowlist{base},
		Clock:              fixedClock{now: testTime},
	})
	conversation := &ConversationRecord{ConversationID: "conv-1", WorkspaceRootDir: root}
	err := service.applyRequestedMessageWorkspace(context.Background(), ViewerRef{ViewerID: "repair", ViewerKind: ViewerKindSystem}, SendMessageCommand{WorkspaceRootDir: other}, conversation)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("error = %v, want ErrInvalidRequest", err)
	}
	if conversation.WorkspaceRootDir != root {
		t.Fatalf("existing workspace root changed after rejection: %q", conversation.WorkspaceRootDir)
	}
}

func TestSendMessageWorkspaceDoesNotChangeCommandFingerprint(t *testing.T) {
	base := SendMessageCommand{ConversationKind: ConversationKindGroup, Content: "repair", IdempotencyKey: "send-1"}
	withWorkspace := base
	withWorkspace.WorkspaceRootDir = filepath.Join(t.TempDir(), "repair")
	without, err := discussionCommandFingerprint("send_message", base)
	if err != nil {
		t.Fatal(err)
	}
	with, err := discussionCommandFingerprint("send_message", withWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	if without != with {
		t.Fatalf("workspace changed command fingerprint: without=%s with=%s", without, with)
	}
}

type staticWorkspaceAllowlist []string

func (s staticWorkspaceAllowlist) DiscussionWorkspaceAllowlist(context.Context) ([]string, error) {
	return append([]string(nil), s...), nil
}
