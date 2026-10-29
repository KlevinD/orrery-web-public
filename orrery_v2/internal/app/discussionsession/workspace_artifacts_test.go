package discussionsession

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type workspaceFileWritingRunner struct {
	request DiscussionAgentRequest
}

func (r *workspaceFileWritingRunner) DiscussionAgentID() string {
	return "lead-agent"
}

func (r *workspaceFileWritingRunner) RunDiscussion(_ context.Context, request DiscussionAgentRequest) (DiscussionAgentResponse, error) {
	r.request = request
	if err := os.WriteFile(filepath.Join(request.Workspace.AgentWorkDir, "existing.txt"), []byte("updated"), 0o600); err != nil {
		return DiscussionAgentResponse{}, err
	}
	reportsDir := filepath.Join(request.Workspace.AgentWorkDir, "reports")
	if err := os.MkdirAll(reportsDir, 0o755); err != nil {
		return DiscussionAgentResponse{}, err
	}
	if err := os.WriteFile(filepath.Join(reportsDir, "launch.csv"), []byte("name,status\nalpha,ready\n"), 0o600); err != nil {
		return DiscussionAgentResponse{}, err
	}
	if err := os.WriteFile(filepath.Join(request.Workspace.AgentWorkDir, ".runtime-secret"), []byte("not downloadable"), 0o600); err != nil {
		return DiscussionAgentResponse{}, err
	}
	for _, name := range []string{"AGENT_CONTEXT.md", "AGENTS.md", "TOOL_SUMMARY.md", "SKILLS.md"} {
		if err := os.WriteFile(filepath.Join(request.Workspace.AgentWorkDir, name), []byte("system runtime context"), 0o600); err != nil {
			return DiscussionAgentResponse{}, err
		}
	}
	runtimeDir := filepath.Join(request.Workspace.AgentWorkDir, ".orrery", "execenv", "run-1", "system")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return DiscussionAgentResponse{}, err
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "runtime.json"), []byte("{}"), 0o600); err != nil {
		return DiscussionAgentResponse{}, err
	}
	return DiscussionAgentResponse{AssistantMessage: "文件已经生成。", Status: "completed", RuntimeKind: "codex"}, nil
}

func TestServiceExposesGeneratedWorkspaceFilesOnAssistantMessage(t *testing.T) {
	repo := newMemoryRepository(t)
	root := t.TempDir()
	agentWorkDir := filepath.Join(root, "conv-1", "agents", "顾明澈")
	if err := os.MkdirAll(agentWorkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentWorkDir, "existing.txt"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentWorkDir, "unchanged.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	runner := &workspaceFileWritingRunner{}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		AttachmentStore: NewLocalAttachmentStore(root),
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("msg-user", "run-1", "msg-assistant", "att-existing", "att-report"),
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

	detail, err := service.SendMessage(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{ConversationID: "conv-1", Content: "生成发布清单", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Messages) != 2 {
		t.Fatalf("messages = %+v", detail.Messages)
	}
	attachments := detail.Messages[1].Attachments
	if len(attachments) != 2 {
		t.Fatalf("generated attachments = %+v", attachments)
	}
	if attachments[0].FileName != "existing.txt" || attachments[1].FileName != "reports/launch.csv" {
		t.Fatalf("generated attachment names = %+v", attachments)
	}
	if attachments[0].UploadedByKind != "agent" || attachments[0].UploadedByName != "顾明澈" {
		t.Fatalf("generated attachment owner = %+v", attachments[0])
	}
	if strings.Contains(strings.Join([]string{attachments[0].FileName, attachments[1].FileName}, ","), "unchanged") {
		t.Fatalf("unchanged workspace file was exposed: %+v", attachments)
	}
	if strings.Contains(strings.Join([]string{attachments[0].FileName, attachments[1].FileName}, ","), "runtime-secret") {
		t.Fatalf("hidden workspace file was exposed: %+v", attachments)
	}
	for _, systemName := range []string{"AGENT_CONTEXT.md", "AGENTS.md", "TOOL_SUMMARY.md", "SKILLS.md", "runtime.json"} {
		if strings.Contains(strings.Join([]string{attachments[0].FileName, attachments[1].FileName}, ","), systemName) {
			t.Fatalf("system workspace file was exposed: %s in %+v", systemName, attachments)
		}
	}
	if err := os.WriteFile(filepath.Join(runner.request.Workspace.AgentWorkDir, "reports", "launch.csv"), []byte("changed later"), 0o600); err != nil {
		t.Fatal(err)
	}

	download, err := service.DownloadAttachment(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1", attachments[1].AttachmentID)
	if err != nil {
		t.Fatal(err)
	}
	defer download.Reader.Close()
	body, err := io.ReadAll(download.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "name,status\nalpha,ready\n" || download.FileName != "reports/launch.csv" {
		t.Fatalf("download name=%q body=%q", download.FileName, body)
	}
	if _, err := service.DownloadAttachment(ctx, ViewerRef{ViewerID: "intruder", ViewerKind: ViewerKindUser}, "conv-1", attachments[1].AttachmentID); err == nil {
		t.Fatal("expected generated file download to enforce conversation membership")
	}
}

func TestServiceAppendsMissionResultNoticeWithGeneratedWorkspaceFiles(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	root := t.TempDir()
	service := newTestService(ServiceOptions{
		Repository: repo, AttachmentStore: NewLocalAttachmentStore(root), Clock: fixedClock{now: testTime},
	})
	conversationID := "mission-conversation-1"
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID: conversationID, TenantID: "tenant-1", PurposeKind: "mission", PurposeID: "mission-1",
		Title: "Mission", Status: "completed", CreatedBy: "owner-1", CreatedAt: testTime, UpdatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	for _, member := range []ConversationMemberRecord{
		{ConversationID: conversationID, MemberKind: "user", MemberID: "owner-1", DisplayName: "Owner", CreatedAt: testTime},
		{ConversationID: conversationID, MemberKind: "agent", MemberID: "agent-1", DisplayName: "Builder", CreatedAt: testTime},
	} {
		if err := repo.SaveMember(ctx, member); err != nil {
			t.Fatal(err)
		}
	}
	workspace, err := service.PrepareAgentRunWorkspace(ctx, conversationID, "agent-1")
	if err != nil {
		t.Fatalf("PrepareAgentRunWorkspace: %v", err)
	}
	if workspace.AgentName != "Builder" || workspace.WorkDir == "" || workspace.Policy.WorkDir != workspace.WorkDir || !workspace.Policy.EnforceBoundary {
		t.Fatalf("prepared workspace = %+v", workspace)
	}
	if err := os.WriteFile(filepath.Join(workspace.WorkDir, "index.html"), []byte("<canvas></canvas>"), 0o600); err != nil {
		t.Fatal(err)
	}
	detail, err := service.AppendNoticeWithWorkspace(ctx, ViewerRef{
		ViewerID: "agent-1", ViewerKind: ViewerKindAgent, TenantID: "tenant-1",
	}, AppendNoticeCommand{
		ConversationID: conversationID, MissionID: "mission-1", MissionTaskID: "task-1",
		Content: "Playable game completed.", IdempotencyKey: "agent-result:run-1",
	}, workspace)
	if err != nil {
		t.Fatalf("AppendNoticeWithWorkspace: %v", err)
	}
	if len(detail.Messages) != 1 || len(detail.Messages[0].Attachments) != 1 {
		t.Fatalf("result notice = %+v", detail.Messages)
	}
	attachment := detail.Messages[0].Attachments[0]
	if attachment.FileName != "index.html" || attachment.UploadedBy != "agent-1" || attachment.UploadedByName != "Builder" {
		t.Fatalf("result attachment = %+v", attachment)
	}
	replayed, err := service.AppendNoticeWithWorkspace(ctx, ViewerRef{
		ViewerID: "agent-1", ViewerKind: ViewerKindAgent, TenantID: "tenant-1",
	}, AppendNoticeCommand{
		ConversationID: conversationID, MissionID: "mission-1", MissionTaskID: "task-1",
		Content: "Playable game completed.", IdempotencyKey: "agent-result:run-1",
	}, workspace)
	if err != nil || len(replayed.Messages) != 1 || len(replayed.Messages[0].Attachments) != 1 {
		t.Fatalf("replayed result notice = %+v err=%v", replayed.Messages, err)
	}
}

func TestGeneratedWorkspaceFilesIgnoreSymlinksAndOversizedFiles(t *testing.T) {
	root := t.TempDir()
	workspace := DiscussionWorkspaceContext{RootDir: root, AgentWorkDir: root, fileSnapshot: snapshotWorkspaceFiles(root)}
	if err := os.WriteFile(filepath.Join(root, "large.bin"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(filepath.Join(root, "large.bin"), maxGeneratedWorkspaceFileBytes+1); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.txt")); err != nil {
		t.Logf("symlink unavailable on this platform: %v", err)
	}

	if files := generatedWorkspaceFiles(workspace); len(files) != 0 {
		t.Fatalf("unsafe generated files = %+v", files)
	}
}

func TestPreparedAgentRunWorkspaceOpensOnlyStableGeneratedFiles(t *testing.T) {
	root := t.TempDir()
	workspace := DiscussionWorkspaceContext{RootDir: root, AgentWorkDir: root, fileSnapshot: snapshotWorkspaceFiles(root)}
	prepared := PreparedAgentRunWorkspace{RootDir: root, WorkDir: root, workspace: workspace}
	path := filepath.Join(root, "index.html")
	content := "<canvas></canvas>"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	files := prepared.GeneratedFiles()
	if len(files) != 1 || files[0].RelativePath != "index.html" || files[0].Size != int64(len(content)) {
		t.Fatalf("prepared generated files = %+v", files)
	}
	reader, err := files[0].Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	body, err := io.ReadAll(reader)
	closeErr := reader.Close()
	if err != nil || closeErr != nil || string(body) != content {
		t.Fatalf("read generated file body=%q err=%v close=%v", body, err, closeErr)
	}

	if err := os.WriteFile(path, []byte(content+"changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if reader, err := files[0].Open(); err == nil {
		_ = reader.Close()
		t.Fatal("expected a file changed after discovery to be rejected")
	}
}

func TestGeneratedWorkspaceFilesExcludeMCPConfig(t *testing.T) {
	root := t.TempDir()
	workspace := DiscussionWorkspaceContext{RootDir: root, AgentWorkDir: root, fileSnapshot: snapshotWorkspaceFiles(root)}
	if err := os.WriteFile(filepath.Join(root, "mcp_config.json"), []byte(`{"headers":{"Authorization":"Bearer secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "report.md"), []byte("safe report"), 0o600); err != nil {
		t.Fatal(err)
	}

	files := generatedWorkspaceFiles(workspace)
	if len(files) != 1 || files[0].relativePath != "report.md" {
		t.Fatalf("generated workspace files = %+v, want only report.md", files)
	}
}
